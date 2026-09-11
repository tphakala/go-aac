package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/quality"
)

// Exit codes: success is 0, a run where some case failed is 1, setup errors
// (flags, missing ffmpeg) are 2, and an interrupted run (Ctrl-C) is 3.
const (
	exitOK          = 0
	exitCases       = 1
	exitSetup       = 2
	exitInterrupted = 3
)

// unknownVersion is reported when a provenance value cannot be determined.
const unknownVersion = "unknown"

// errEmptyList is returned by the comma-list parsers for an empty result.
var errEmptyList = errors.New("empty list")

// bitrateCeilingKbps is the AAC buffer-model ceiling as a whole-stream bitrate,
// the most either encoder can spend: 6144 bits per channel per 1024-sample
// frame, at the maximum 2 channels and 48 kHz. A target above this is clamped
// by both encoders, so the labels would lie; parseFlags rejects it at setup.
const bitrateCeilingKbps = 6144 * 2 * 48000 / 1024 / 1000

func main() { os.Exit(realMain()) }

// realMain wires up signal handling and runs. It is separate from main so the
// os.Exit lives alone: NotifyContext's stop must be deferred (and so must run
// on the normal return here), which cannot sit in a function that also calls
// os.Exit.
func realMain() int {
	// NotifyContext so a Ctrl-C cancels the run rather than hard-killing the
	// process: run's deferred work-directory cleanup then executes on the
	// normal return, instead of being skipped by an uncaught signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal has cancelled ctx, restore the default handler so
	// a second Ctrl-C hard-kills: otherwise NotifyContext keeps swallowing
	// signals until stop() runs, and a run stuck draining a wedged external
	// tool could not be force-quit.
	go func() {
		<-ctx.Done()
		stop()
	}()
	return run(ctx, os.Args[1:], os.Stderr)
}

// options is the parsed command line.
type options struct {
	rates, bitrates []int
	coders          []aac.Coder
	programs        []quality.Program
	seconds         int
	jobs            int
	ffmpeg, visqol  string
	peaq, work      string
	keep            bool
	crosscheck      bool
	out, jsonOut    string
}

// parseFlags parses args into options, resolving the program and coder lists.
func parseFlags(args []string) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("quality", flag.ContinueOnError)
	rates := fs.String("rates", "44100", "comma-separated sample rates (44100, 48000)")
	bitrates := fs.String("bitrates", "64,96,128,192", "comma-separated whole-stream bitrates in kbps")
	coders := fs.String("coders", "nmr,twoloop,fast", "comma-separated coders (nmr, twoloop, fast)")
	programs := fs.String("programs", "", "comma-separated program names to run (default: all synthetic programs)")
	corpus := fs.String("corpus", "", "directory of WAV files to add as programs (named by file name)")
	fs.IntVar(&o.seconds, "seconds", 6, "program length in seconds for synthetic programs")
	fs.IntVar(&o.jobs, "jobs", runtime.GOMAXPROCS(0), "number of cases to run concurrently (each forks CPU-heavy external tools)")
	fs.StringVar(&o.ffmpeg, "ffmpeg", "", "ffmpeg binary (default: $GOAAC_FFMPEG, else ffmpeg on PATH)")
	fs.StringVar(&o.visqol, "visqol", "", "visqol binary (default: visqol on PATH; skipped when absent)")
	fs.StringVar(&o.peaq, "peaq", "", "PEAQ binary printing 'Objective Difference Grade:' (default: peaq-odg on PATH; skipped when absent)")
	fs.StringVar(&o.work, "work", "", "work directory for intermediate files (default: a temp dir next to -out)")
	fs.BoolVar(&o.keep, "keep", false, "keep the work directory")
	fs.BoolVar(&o.crosscheck, "crosscheck", false, "decode each stream through ffmpeg's aac_fixed too and warn when it diverges from the pcm decode (diagnostic only, never fails a case)")
	fs.StringVar(&o.out, "out", "tools/quality/out/report.md", "markdown report path")
	fs.StringVar(&o.jsonOut, "json", "tools/quality/out/report.json", "JSON report path")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	var err error
	if o.rates, err = parseInts(*rates); err != nil {
		return nil, fmt.Errorf("-rates: %w", err)
	}
	if o.bitrates, err = parseInts(*bitrates); err != nil {
		return nil, fmt.Errorf("-bitrates: %w", err)
	}
	if o.coders, err = parseCoders(*coders); err != nil {
		return nil, fmt.Errorf("-coders: %w", err)
	}
	if o.seconds <= 0 {
		return nil, errors.New("-seconds must be positive")
	}
	if o.jobs < 1 {
		return nil, errors.New("-jobs must be positive")
	}
	// Duplicates would run every case for a repeated value again and, worse,
	// double it in the per-row summary (which counts programs per row).
	o.rates = dedupInts(o.rates)
	o.bitrates = dedupInts(o.bitrates)
	// Validate at setup, not per case. An unvalidated rate reaches Program.Gen,
	// where a negative one panics in make and 0 makes the chirp generator's
	// zero-length period loop forever, and it is not a rate pcm.Config accepts.
	for _, r := range o.rates {
		if r != 44100 && r != 48000 {
			return nil, fmt.Errorf("-rates: unsupported sample rate %d (want 44100 or 48000)", r)
		}
	}
	// A non-positive bitrate is rejected outright; one above the buffer-model
	// ceiling would be clamped by both encoders and so mislabel the case.
	for _, kbps := range o.bitrates {
		if kbps <= 0 {
			return nil, fmt.Errorf("-bitrates: %d must be positive", kbps)
		}
		if kbps > bitrateCeilingKbps {
			return nil, fmt.Errorf("-bitrates: %d kbps exceeds the AAC buffer-model ceiling of %d kbps", kbps, bitrateCeilingKbps)
		}
	}
	if o.programs, err = selectPrograms(*programs, *corpus, o.rates); err != nil {
		return nil, err
	}
	return o, nil
}

// run is the whole program: parse, execute the grid, write the reports, return
// the exit code. Diagnostics and progress go to errw.
func run(ctx context.Context, args []string, errw io.Writer) int {
	o, err := parseFlags(args)
	if err != nil {
		return fail(errw, err)
	}
	tl := detectTools(ctx, o.ffmpeg, o.visqol, o.peaq)
	// ffmpeg is the reference encoder, so its absence is a setup error rather
	// than an empty column.
	if tl.ffmpeg == "" {
		return fail(errw, errors.New("no ffmpeg found: set GOAAC_FFMPEG, pass -ffmpeg, or install ffmpeg on PATH"))
	}
	// An explicitly named perceptual tool that does not resolve is a setup
	// error, not a reason to quietly omit its column: the user asked for it.
	for _, t := range []struct{ flag, name, got string }{
		{o.visqol, "-visqol", tl.visqol}, {o.peaq, "-peaq", tl.peaq},
	} {
		if t.flag != "" && t.got == "" {
			return fail(errw, fmt.Errorf("%s %q not found", t.name, t.flag))
		}
	}
	// Both report directories, before the grid: creating only one meant a
	// completed run could discard its JSON at the final syscall.
	for _, p := range []string{o.out, o.jsonOut} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(errw, err)
		}
	}
	workDir := o.work
	if workDir == "" {
		workDir, err = os.MkdirTemp(filepath.Dir(o.out), "work-")
		if err != nil {
			return fail(errw, err)
		}
		if !o.keep {
			defer func() { _ = os.RemoveAll(workDir) }()
		}
	}

	rep := &report{
		SchemaVersion: reportSchemaVersion,
		GeneratedUTC:  time.Now().UTC().Format(time.RFC3339),
		GoAACRev:      vcsRevision(),
		FFmpegVersion: ffmpegVersion(ctx, tl.ffmpeg),
		LibFDK:        tl.libfdk,
		Seconds:       o.seconds,
	}
	if tl.visqol != "" {
		rep.Tools = append(rep.Tools, "visqol")
	}
	if tl.peaq != "" {
		rep.Tools = append(rep.Tools, "peaq-odg")
	}

	failed, err := runGrid(ctx, tl, o, workDir, rep, errw)
	if err != nil {
		return fail(errw, err)
	}
	// A cancelled run (Ctrl-C) is not a result. Leaving the previous report in
	// place and returning non-zero is safer than overwriting it with a
	// truncated one that looks structurally normal.
	if ctx.Err() != nil {
		logf(errw, "quality: interrupted, leaving any previous report in place\n")
		return exitInterrupted
	}
	rep.Failed = failed
	rep.Attempted = len(rep.Cases) + failed
	if err := writeFile(o.out, func(f *os.File) error { return writeMarkdown(f, rep) }); err != nil {
		return fail(errw, err)
	}
	if err := writeFile(o.jsonOut, func(f *os.File) error { return writeJSON(f, rep) }); err != nil {
		return fail(errw, err)
	}
	logf(errw, "wrote %s and %s (%d cases, %d failed)\n", o.out, o.jsonOut, len(rep.Cases), failed)
	if failed > 0 {
		return exitCases
	}
	return exitOK
}

// caseJob is one dispatched comparison. ref is the quantized reference,
// generated once per (sample rate, program) and shared read-only across that
// program's bitrate and coder cases (and across the workers running them).
type caseJob struct {
	idx  int // 1-based, dense over dispatched jobs; also names the case dir
	spec caseSpec
	ref  [][]float64
}

// buildJobs expands the (rate, program, bitrate, coder) grid into the dispatch
// list, generating and quantizing each program's reference once per sample
// rate and reusing it across bitrates and coders. Rates a program cannot
// serve, and empty programs, are logged and dropped here so the job list holds
// only real cases. It checks ctx before each reference generation so a Ctrl-C
// during this phase stops promptly instead of building out the whole grid.
func buildJobs(ctx context.Context, o *options, errw io.Writer) []caseJob {
	var jobs []caseJob
	for _, sr := range o.rates {
		for i := range o.programs {
			if ctx.Err() != nil {
				return jobs // cancelled: stop generating references
			}
			p := o.programs[i]
			if !p.RunsAt(sr) {
				logf(errw, "skip: %s has no data at %d Hz\n", p.Name, sr)
				continue
			}
			// Generate and quantize once per (rate, program) and share the
			// result read-only across this program's bitrate and coder cases.
			ref := genRef(p, sr, o.seconds)
			if len(ref) == 0 {
				logf(errw, "skip: %s is empty at %d Hz\n", p.Name, sr)
				continue
			}
			for _, kbps := range o.bitrates {
				for _, coder := range o.coders {
					jobs = append(jobs, caseJob{
						idx:  len(jobs) + 1,
						spec: caseSpec{Program: p, SampleRate: sr, Kbps: kbps, Coder: coder, Seconds: o.seconds},
						ref:  ref,
					})
				}
			}
		}
	}
	return jobs
}

// genRef generates program p at sampleRate for seconds and quantizes it to the
// 16-bit signal both encoders compare against, returning nil for an empty
// program. Building the reference once here is what lets a program's bitrate
// and coder cases share it.
func genRef(p quality.Program, sampleRate, seconds int) [][]float64 {
	raw := p.Gen(sampleRate, sampleRate*seconds)
	if len(raw) == 0 || len(raw[0]) == 0 {
		return nil
	}
	ref := make([][]float64, len(raw))
	for c := range raw {
		ref[c] = quality.Quantize16(raw[c])
	}
	return ref
}

// runGrid runs every case concurrently (up to o.jobs at a time), appending
// results to rep in deterministic grid order and returning how many cases
// failed. Only setup-class errors (a work directory that cannot be created)
// are returned; a per-case failure is counted, not fatal. A cancelled context
// (Ctrl-C) stops dispatch and returns what completed so far; run() then
// discards that partial result and exits with exitInterrupted.
func runGrid(ctx context.Context, tl tools, o *options, workDir string, rep *report, errw io.Writer) (int, error) {
	jobs := buildJobs(ctx, o, errw)
	total := len(jobs)
	// Every program was skipped (for instance a corpus whose files match none
	// of -rates): with nothing to run the harness would otherwise write empty
	// reports and exit 0, which reads as a clean pass. A cancelled build is a
	// different case; run() turns that into the interrupted exit code.
	if total == 0 && ctx.Err() == nil {
		return 0, errors.New("no cases to run: every program was skipped (check -rates against the corpus and -programs)")
	}

	// Cancel the shared context on a setup error so in-flight workers stop.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]*caseResult, total) // nil = not completed
	failedFlags := make([]bool, total)    // true = ran and failed
	var (
		wg        sync.WaitGroup
		setupOnce sync.Once
		setupErr  error
	)
	// Serialize every log write for the run: workers emit progress lines here
	// AND, through runCase, per-tool warnings from measure, all to one sink
	// concurrently. os.Stderr locks internally, but a non-*os.File writer (a
	// test buffer) does not, so guard the sink itself rather than one call site.
	logw := &syncWriter{w: errw}
	// Reclaim each finished case's dir only when the whole work tree is a
	// throwaway temp dir the run deletes at the end anyway. An explicit -work
	// dir (or -keep) means the user wants to inspect artifacts, so leave those.
	reclaim := o.work == "" && !o.keep
	sem := make(chan struct{}, o.jobs)

dispatch:
	for ji := range jobs {
		// Acquire a worker slot, but stay cancelable: a plain sem send would
		// block past a Ctrl-C while every slot is busy, delaying the stop.
		select {
		case <-ctx.Done():
			break dispatch // cancelled: dispatch no more jobs
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(ji int) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			j := &jobs[ji]
			// Index-prefixed: a corpus file may share a synthetic program's
			// name, and two cases writing one directory would collide.
			dir := filepath.Join(workDir, fmt.Sprintf("%03d-%s-%d-%d-%s", j.idx, j.spec.Program.Name, j.spec.SampleRate, j.spec.Kbps, coderName(j.spec.Coder)))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				setupOnce.Do(func() { setupErr = err })
				cancel()
				return
			}
			start := time.Now()
			res, err := runCase(ctx, tl, dir, j.spec, j.ref, o.crosscheck, logw)
			if err != nil {
				failedFlags[ji] = true
				logf(logw, "[%d/%d] %s %d Hz %d kbps %s: FAILED: %v\n", j.idx, total, j.spec.Program.Name, j.spec.SampleRate, j.spec.Kbps, coderName(j.spec.Coder), err)
				return // leave the dir in place for inspection
			}
			results[ji] = &res
			if reclaim {
				_ = os.RemoveAll(dir) // reclaim as we go; nothing reads it now
			}
			logf(logw, "[%d/%d] %s %d Hz %d kbps %s: go-aac SNR %s LSD %s | ffmpeg SNR %s LSD %s (%.1fs)\n",
				j.idx, total, j.spec.Program.Name, j.spec.SampleRate, j.spec.Kbps, coderName(j.spec.Coder),
				fmtMetric(res.GoAAC.Metrics.SNR), fmtMetric(res.GoAAC.Metrics.LSD),
				fmtMetric(res.FFmpegAAC.Metrics.SNR), fmtMetric(res.FFmpegAAC.Metrics.LSD),
				time.Since(start).Seconds())
		}(ji)
	}
	wg.Wait()
	if setupErr != nil {
		return 0, setupErr
	}
	// Append in index order: the report stays byte-stable regardless of the
	// order workers happened to finish in.
	failed := 0
	for ji := range jobs {
		switch {
		case results[ji] != nil:
			rep.Cases = append(rep.Cases, *results[ji])
		case failedFlags[ji]:
			failed++
		}
	}
	return failed, nil
}

// logf writes a progress or diagnostic line; a failed write to the log sink is
// deliberately ignored (there is nowhere else to report it).
func logf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// fail prints a setup error and returns the setup exit code.
func fail(errw io.Writer, err error) int {
	logf(errw, "quality: %v\n", err)
	return exitSetup
}

// syncWriter serializes concurrent writes to an underlying writer, so the
// parallel grid's log lines neither corrupt nor tear.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// writeFile creates path and hands it to fn, closing it afterwards.
func writeFile(path string, fn func(*os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// parseInts parses a non-empty comma-separated integer list.
func parseInts(s string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, errEmptyList
	}
	return out, nil
}

// dedupInts returns xs with later duplicates removed, preserving first-seen
// order. It allocates a fresh slice, leaving the input untouched.
func dedupInts(xs []int) []int {
	seen := make(map[int]bool, len(xs))
	out := make([]int, 0, len(xs))
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// selectPrograms returns the synthetic programs named in filter (all when
// empty) plus one program per WAV file in corpus. rates is the effective
// -rates set, which every corpus file must declare one of.
func selectPrograms(filter, corpus string, rates []int) ([]quality.Program, error) {
	var progs []quality.Program
	switch filter {
	case "":
		progs = quality.Programs()
	case "none":
		// Explicitly no synthetic programs, for a corpus-only run. Left as an
		// empty list; the guard below rejects a run with nothing to compare.
		progs = nil
	default:
		// Deduplicate names the way -rates/-bitrates are deduped: a repeat
		// would otherwise run that program twice per (rate, bitrate, coder) and
		// double-weight it in the summary's per-program mean.
		seen := make(map[string]bool)
		for name := range strings.SplitSeq(filter, ",") {
			name = strings.TrimSpace(name)
			if seen[name] {
				continue
			}
			seen[name] = true
			p, ok := quality.ProgramByName(name)
			if !ok {
				return nil, fmt.Errorf("unknown program %q", name)
			}
			progs = append(progs, p)
		}
	}
	if corpus == "" {
		if len(progs) == 0 {
			return nil, errors.New("no programs selected (-programs none needs a -corpus)")
		}
		return progs, nil
	}
	entries, err := os.ReadDir(corpus)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		// Regular files only: opening a fifo named *.wav blocks forever.
		if !e.Type().IsRegular() || !strings.EqualFold(filepath.Ext(e.Name()), ".wav") {
			continue
		}
		p, err := wavProgram(filepath.Join(corpus, e.Name()), rates)
		if err != nil {
			return nil, err
		}
		progs = append(progs, p)
	}
	if len(progs) == 0 {
		return nil, fmt.Errorf("no programs selected (corpus %q has no usable WAV files)", corpus)
	}
	return progs, nil
}

// wavProgram wraps a WAV file as a Program. Its Gen ignores nSamples in favor
// of the file's own length and returns empty channels when asked for a
// different sample rate than the file's, since resampling would change what is
// being measured.
func wavProgram(path string, rates []int) (quality.Program, error) {
	f, err := os.Open(path)
	if err != nil {
		return quality.Program{}, err
	}
	sr, ch, err := quality.ReadWAV(f)
	_ = f.Close() // read-only handle; the parse result is what matters
	if err != nil {
		return quality.Program{}, fmt.Errorf("%s: %w", path, err)
	}
	// A corpus program runs only at its own rate, so one outside the -rates set
	// contributes no case at all. This also rejects the 0 a malformed fmt chunk
	// can declare, which Program.SampleRate reads as the "any rate" sentinel.
	if !slices.Contains(rates, sr) {
		return quality.Program{}, fmt.Errorf("%s: sample rate %d is not one of -rates %v", path, sr, rates)
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return quality.Program{
		Name:       name,
		Channels:   len(ch),
		SampleRate: sr, // a corpus file is measured only at its own rate
		Gen:        func(_, _ int) [][]float64 { return ch },
	}, nil
}

// vcsRevision returns the short VCS revision embedded by the Go toolchain, or
// unknownVersion (a build without VCS stamping, or a non-git checkout).
func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownVersion
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return unknownVersion
}
