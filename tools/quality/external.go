package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	aac "github.com/tphakala/go-aac"
)

// tools holds the resolved paths of the black-box binaries; "" means absent.
// libfdk records whether the resolved ffmpeg build exposes the libfdk_aac
// encoder, so the harness adds that reference column only when it can.
type tools struct {
	ffmpeg, visqol, peaq string
	libfdk               bool
}

// detectTools resolves each binary. ffmpeg is resolved in priority order:
// an explicit -ffmpeg flag, then $GOAAC_FFMPEG (the pinned-oracle convention
// the rest of the suite uses, so a developer with the pinned build gets it
// without a flag), then "ffmpeg" on PATH. visqol and peaq resolve from their
// flag or default name on PATH. Absence is not an error here; callers decide.
func detectTools(ctx context.Context, ffmpegFlag, visqolFlag, peaqFlag string) tools {
	look := func(flag, def string) string {
		name := def
		if flag != "" {
			name = flag
		}
		p, err := exec.LookPath(name)
		if err != nil {
			return ""
		}
		return p
	}
	ffmpeg := ""
	switch {
	case ffmpegFlag != "":
		ffmpeg = look(ffmpegFlag, "")
	case os.Getenv("GOAAC_FFMPEG") != "":
		// The oracle convention points GOAAC_FFMPEG at the binary itself, so it
		// is used as given rather than looked up on PATH.
		if p := os.Getenv("GOAAC_FFMPEG"); usableFile(p) {
			ffmpeg = p
		}
	default:
		ffmpeg = look("", "ffmpeg")
	}
	tl := tools{
		ffmpeg: ffmpeg,
		visqol: look(visqolFlag, "visqol"),
		peaq:   look(peaqFlag, "peaq-odg"),
	}
	if tl.ffmpeg != "" {
		tl.libfdk = hasLibFDK(ctx, tl.ffmpeg)
	}
	return tl
}

// usableFile reports whether path names an existing regular file, so a
// GOAAC_FFMPEG pointing at a directory (the easy mistake of naming the build
// tree instead of the binary) is treated as absent rather than exec'd.
func usableFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// ffmpegVersion returns the first line of `ffmpeg -version`, or unknownVersion.
func ffmpegVersion(ctx context.Context, ffmpeg string) string {
	if ffmpeg == "" {
		return unknownVersion
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-version").Output()
	if err != nil {
		return unknownVersion
	}
	line, _, _ := strings.Cut(string(out), "\n")
	if line = strings.TrimSpace(line); line == "" {
		return unknownVersion
	}
	return line
}

// libfdkEncoderLine matches ffmpeg's `-encoders` row for libfdk_aac: an audio
// encoder line begins with "A" and five capability flags, then the codec name.
var libfdkEncoderLine = regexp.MustCompile(`(?m)^\s*A\S{5}\s+libfdk_aac\s`)

// hasLibFDK reports whether the ffmpeg build lists the libfdk_aac encoder.
func hasLibFDK(ctx context.Context, ffmpeg string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return false
	}
	return parseEncodersForLibFDK(string(out))
}

// parseEncodersForLibFDK reports whether an `ffmpeg -encoders` listing exposes
// the libfdk_aac encoder. Split out so it is unit-testable without ffmpeg.
func parseEncodersForLibFDK(out string) bool {
	return libfdkEncoderLine.MatchString(out)
}

// coderName maps an aac.Coder to ffmpeg's -aac_coder value. It panics on an
// unknown coder, mirroring the oracle harness's cCoderName: an unmapped coder
// is a programming error, not a runtime condition to paper over.
func coderName(c aac.Coder) string {
	switch c {
	case aac.CoderNMR:
		return "nmr"
	case aac.CoderTwoLoop:
		return "twoloop"
	case aac.CoderFast:
		return "fast"
	default:
		panic(fmt.Sprintf("quality: unknown coder %d", c))
	}
}

// parseCoders parses a comma-separated coder list (nmr, twoloop, fast) into a
// deduplicated slice preserving first-seen order. An unknown name is an error.
func parseCoders(s string) ([]aac.Coder, error) {
	byName := map[string]aac.Coder{"nmr": aac.CoderNMR, "twoloop": aac.CoderTwoLoop, "fast": aac.CoderFast}
	var out []aac.Coder
	seen := map[aac.Coder]bool{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		c, ok := byName[part]
		if !ok {
			return nil, fmt.Errorf("unknown coder %q (want nmr, twoloop, or fast)", part)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errEmptyList
	}
	return out, nil
}

// refKind names which ffmpeg AAC encoder produces a reference stream.
type refKind int

const (
	refFFmpegAAC refKind = iota // ffmpeg's native aac encoder (-c:a aac)
	refLibFDK                   // ffmpeg's libfdk_aac encoder, when the build has it
)

// refEncoder is a resolved ffmpeg reference encoder: which kind, and the
// ffmpeg binary that runs it.
type refEncoder struct {
	kind refKind
	bin  string
}

// encode runs the ffmpeg reference encoder on rawName (interleaved f32le, the
// exact quantized samples the go-aac side encodes) inside dir, writing outName
// as an ADTS stream, and returns the stream bytes. The native aac path mirrors
// the oracle harness's cEncode exactly (every tool switch at its default, so
// only -aac_coder and -b:a are set); the libfdk path takes libfdk's own
// defaults. -flags +bitexact keeps the native encoder's output reproducible.
func (r refEncoder) encode(ctx context.Context, dir, rawName, outName string, sampleRate, channels, kbps int, coder aac.Coder) ([]byte, error) {
	args := []string{"-v", "error", "-y", "-f", "f32le",
		"-ar", fmt.Sprint(sampleRate), "-ac", fmt.Sprint(channels), "-i", rawName}
	switch r.kind {
	case refFFmpegAAC:
		args = append(args, "-c:a", "aac", "-aac_coder", coderName(coder),
			"-b:a", fmt.Sprint(kbps*1000), "-flags", "+bitexact", "-f", "adts", outName)
	case refLibFDK:
		// libfdk_aac has no -aac_coder; it takes its own tuning (afterburner on)
		// and ffmpeg inserts the f32->s16 resample it needs.
		args = append(args, "-c:a", "libfdk_aac",
			"-b:a", fmt.Sprint(kbps*1000), "-f", "adts", outName)
	default:
		return nil, fmt.Errorf("quality: unknown reference encoder kind %d", r.kind)
	}
	if out, err := runTool(ctx, dir, r.bin, args...); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	return os.ReadFile(filepath.Join(dir, outName))
}

// Result-line patterns of the external perceptual tools. Both can print nan,
// so the capture admits it.
var (
	mosRe = regexp.MustCompile(`(?i)MOS-LQO:\s*([-+]?(?:nan|inf|[0-9.]+))`)
	odgRe = regexp.MustCompile(`(?i)Objective Difference Grade:\s*([-+]?(?:nan|inf|[0-9.]+))`)
)

// runTool runs an external binary inside dir and returns its combined output,
// bounded by cmdTimeout.
func runTool(ctx context.Context, dir, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	// WaitDelay so a tool that is killed on cancellation but leaks a child still
	// holding the output pipe cannot wedge cmd.Run forever.
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	return out.String(), err
}

// to48k returns the name of a 48 kHz copy of wav inside dir, resampling with
// ffmpeg when needed. It fails when the rate is not 48 kHz and ffmpeg is
// absent.
func to48k(ctx context.Context, tl tools, dir, wav string, sampleRate int) (string, error) {
	if sampleRate == 48000 {
		return wav, nil
	}
	if tl.ffmpeg == "" {
		return "", fmt.Errorf("%s: %d Hz needs ffmpeg to resample to 48 kHz", wav, sampleRate)
	}
	out := strings.TrimSuffix(wav, ".wav") + "-48k.wav"
	if txt, err := runTool(ctx, dir, tl.ffmpeg, "-v", "error", "-y", "-i", wav, "-ar", "48000", out); err != nil {
		return "", fmt.Errorf("ffmpeg resample: %w: %s", err, strings.TrimSpace(txt))
	}
	return out, nil
}

// perceptualTool describes one external 48 kHz reference-versus-degraded
// scorer: how to build its argument list and how to read its result line.
type perceptualTool struct {
	name string
	bin  string
	args func(ref, deg string) []string
	re   *regexp.Regexp
}

// runPerceptual resamples both WAVs to 48 kHz when needed, runs the tool
// inside dir with relative file names, and parses the last result line.
func runPerceptual(ctx context.Context, tl tools, dir, refWav, degWav string, sampleRate int, pt perceptualTool) (float64, error) {
	ref48, err := to48k(ctx, tl, dir, refWav, sampleRate)
	if err != nil {
		return 0, err
	}
	deg48, err := to48k(ctx, tl, dir, degWav, sampleRate)
	if err != nil {
		return 0, err
	}
	out, err := runTool(ctx, dir, pt.bin, pt.args(ref48, deg48)...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w: %s", pt.name, err, strings.TrimSpace(out))
	}
	return parseLast(pt.re, out, pt.name)
}

// runVisqol scores degWav against refWav with ViSQOL in audio mode and
// returns MOS-LQO.
func runVisqol(ctx context.Context, tl tools, dir, refWav, degWav string, sampleRate int) (float64, error) {
	return runPerceptual(ctx, tl, dir, refWav, degWav, sampleRate, perceptualTool{
		name: "visqol",
		bin:  tl.visqol,
		args: func(ref, deg string) []string { return []string{"--reference_file", ref, "--degraded_file", deg} },
		re:   mosRe,
	})
}

// runPEAQ scores degWav against refWav with the PEAQ basic model and returns
// the Objective Difference Grade.
func runPEAQ(ctx context.Context, tl tools, dir, refWav, degWav string, sampleRate int) (float64, error) {
	return runPerceptual(ctx, tl, dir, refWav, degWav, sampleRate, perceptualTool{
		name: "peaq",
		bin:  tl.peaq,
		args: func(ref, deg string) []string { return []string{"--basic", ref, deg} },
		re:   odgRe,
	})
}

// parseLast returns the last numeric capture of re in out. A nan or inf
// capture parses to NaN (the tool ran but had nothing to say), not an error.
func parseLast(re *regexp.Regexp, out, what string) (float64, error) {
	m := re.FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0, fmt.Errorf("no %s result line in output: %s", what, strings.TrimSpace(out))
	}
	v := m[len(m)-1][1]
	bare := strings.ToLower(strings.TrimLeft(v, "+-"))
	if bare == "nan" || bare == "inf" {
		return math.NaN(), nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: unparsable result %q: %w", what, v, err)
	}
	return f, nil
}
