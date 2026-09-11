package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/quality"
)

// The quality regression gate. It re-measures go-aac's own objective quality on
// a small deterministic corpus and fails when any gated metric regresses past a
// tolerance against a committed baseline. It gates on go-aac's absolute metrics,
// not the go-aac-versus-ffmpeg delta: go-aac encode -> this repo's pcm decode ->
// cross-correlation align -> Compare against the source is fully in-repo and
// deterministic on a given architecture, so it is a stable signal for an encoder
// change and needs no external binary. ffmpeg stays the human-facing external
// reference in `task quality` and the oracle-gated harness tests below, not this
// gate's oracle.
//
// The committed baseline is deterministic per architecture and differs across
// architectures only at the ~1e-13 level (the encoder and pcm decoder are
// bit-exact cross-arch; only float64 FMA reassociation in the FFT/SNR math
// varies), far under the dB tolerances here, so one baseline gates every CI
// leg. The test is guarded by GOAAC_QUALITY_BASELINE=1 so a bare local
// `go test ./...` skips this non-trivial run unless opted in; CI's test job sets
// it on every OS. UPDATE_QUALITY_BASELINE=1 rewrites the baseline; refreshing it
// is a deliberate, reviewed action, the same model as the encoder's golden gate.

const (
	baselineSchemaVersion = 1
	baselineSeconds       = 2
	baselinePath          = "testdata/baseline.json"
)

// Directional regression tolerances in dB: SNR, BandSNR, SegSNR are
// higher-is-better; LSD and PreEcho are lower-is-better. They are wide enough
// to absorb floating-point reassociation noise on a single architecture and
// tight enough to catch a real quality change, which a quality-tuning PR is
// expected to trip and then re-freeze the baseline for.
const (
	tolSNR     = 0.5
	tolBandSNR = 0.5
	tolSegSNR  = 0.5
	tolLSD     = 0.5
	tolPreEcho = 1.0
)

// baselineCase is one case's gated go-aac metrics. Non-finite values (a metric
// undefined for the case, e.g. PreEcho on a program with no transients) encode
// as JSON null via nullable, matching the report's convention.
type baselineCase struct {
	Program    string   `json:"program"`
	SampleRate int      `json:"sample_rate"`
	Kbps       int      `json:"kbps"`
	Coder      string   `json:"coder"`
	Lag        int      `json:"lag"`
	SNR        *float64 `json:"snr"`
	BandSNR    *float64 `json:"band_snr"`
	SegSNR     *float64 `json:"seg_snr"`
	LSD        *float64 `json:"lsd"`
	PreEcho    *float64 `json:"pre_echo"`
	PreEchoN   int      `json:"pre_echo_events"`
}

// baselineFile is the committed baseline document.
type baselineFile struct {
	SchemaVersion int            `json:"schema_version"`
	Seconds       int            `json:"seconds"`
	Note          string         `json:"note"`
	Cases         []baselineCase `json:"cases"`
}

// baselineProgramNames is the gate corpus: a representative slice of the
// synthetic corpus (tonal, noisy, band-limited, transient, nature, and a stereo
// case), run at 44100 Hz, the low and high ends of the default bitrate set, and
// every coder so a regression in any one is caught. Every program here was
// verified to encode at both gate bitrates under all three coders and to align
// at exactly the encoder delay: a program whose cross-correlation peak an
// alternate coder can shift (the MultiTone-derived multitone and
// stereo-decorrelated do this under twoloop) is deliberately excluded so the
// zero-tolerance Lag gate stays stable. Refresh the baseline
// (task quality:gate:update) after any change to this set.
var (
	baselineProgramNames = []string{"harmonic-vibrato", "pink-noise", "band-noise", "tone-click", "bird-chirps", "stereo-wide"}
	baselineRates        = []int{44100}
	baselineBitrates     = []int{64, 192}
	baselineCoders       = []aac.Coder{aac.CoderNMR, aac.CoderTwoLoop, aac.CoderFast}
)

// baselineSpecs expands the gate corpus into cases, in a stable order.
func baselineSpecs(t *testing.T) []caseSpec {
	t.Helper()
	specs := make([]caseSpec, 0, len(baselineRates)*len(baselineProgramNames)*len(baselineBitrates)*len(baselineCoders))
	for _, sr := range baselineRates {
		for _, name := range baselineProgramNames {
			p, ok := quality.ProgramByName(name)
			if !ok {
				t.Fatalf("baseline corpus names unknown program %q", name)
			}
			for _, kbps := range baselineBitrates {
				for _, coder := range baselineCoders {
					specs = append(specs, caseSpec{Program: p, SampleRate: sr, Kbps: kbps, Coder: coder, Seconds: baselineSeconds})
				}
			}
		}
	}
	return specs
}

// measureGoAAC runs one case through go-aac only (encode, pcm decode, align,
// score), reusing the production measure path with no external tools and no
// cross-check. It returns the metrics and the alignment lag, which is go-aac's
// algorithmic delay and part of the gate.
func measureGoAAC(t *testing.T, spec caseSpec) (m quality.Metrics, lag int) {
	t.Helper()
	ref := genRef(spec.Program, spec.SampleRate, spec.Seconds)
	if len(ref) == 0 || len(ref[0]) == 0 {
		t.Fatalf("empty reference for %s at %d Hz", spec.Program.Name, spec.SampleRate)
	}
	stream, err := encodeGoAAC(ref, spec.SampleRate, spec.Kbps, spec.Coder)
	if err != nil {
		t.Fatalf("%s %d/%d %s: go-aac encode: %v", spec.Program.Name, spec.SampleRate, spec.Kbps, coderName(spec.Coder), err)
	}
	r, err := measure(t.Context(), tools{}, t.TempDir(), "go-aac", "goaac", ref, stream, spec.SampleRate, false, io.Discard)
	if err != nil {
		t.Fatalf("%s %d/%d %s: measure: %v", spec.Program.Name, spec.SampleRate, spec.Kbps, coderName(spec.Coder), err)
	}
	return r.Metrics, r.Lag
}

// toBaselineCase captures the gated metrics of one case.
func toBaselineCase(spec caseSpec, m quality.Metrics, lag int) baselineCase {
	return baselineCase{
		Program: spec.Program.Name, SampleRate: spec.SampleRate, Kbps: spec.Kbps, Coder: coderName(spec.Coder), Lag: lag,
		SNR: nullable(m.SNR), BandSNR: nullable(m.BandSNR), SegSNR: nullable(m.SegSNR),
		LSD: nullable(m.LSD), PreEcho: nullable(m.PreEcho), PreEchoN: m.PreEchoN,
	}
}

// TestQualityBaseline is the regression gate. See the file comment for what it
// gates on and why it is guarded by an environment variable.
func TestQualityBaseline(t *testing.T) {
	update := os.Getenv("UPDATE_QUALITY_BASELINE") != ""
	if !update && os.Getenv("GOAAC_QUALITY_BASELINE") == "" {
		t.Skip("set GOAAC_QUALITY_BASELINE=1 to run the quality regression gate (or UPDATE_QUALITY_BASELINE=1 to refresh the baseline)")
	}
	// Refreshing and gating are mutually exclusive intents: with both set the
	// update path would silently overwrite the baseline and report a pass,
	// hiding any regression. Force one explicit intent.
	if update && os.Getenv("GOAAC_QUALITY_BASELINE") != "" {
		t.Fatal("set only one of UPDATE_QUALITY_BASELINE (refresh) or GOAAC_QUALITY_BASELINE (gate); with both set, refreshing would overwrite the baseline instead of checking against it")
	}

	specs := baselineSpecs(t)
	cur := make([]baselineCase, len(specs))
	for i, spec := range specs {
		m, lag := measureGoAAC(t, spec)
		cur[i] = toBaselineCase(spec, m, lag)
	}

	if update {
		writeBaseline(t, cur)
		return
	}

	base := readBaseline(t)
	if base.SchemaVersion != baselineSchemaVersion {
		t.Fatalf("baseline schema version %d, want %d: refresh with UPDATE_QUALITY_BASELINE=1", base.SchemaVersion, baselineSchemaVersion)
	}
	// A seconds mismatch means the baseline was measured at a different program
	// length; the metrics would not be comparable. Fail with the same refresh
	// hint as the schema check rather than reporting confusing metric drift.
	if base.Seconds != baselineSeconds {
		t.Fatalf("baseline seconds %d, want %d: refresh with UPDATE_QUALITY_BASELINE=1", base.Seconds, baselineSeconds)
	}
	byKey := make(map[string]baselineCase, len(base.Cases))
	for _, bc := range base.Cases {
		byKey[caseKey(bc.Program, bc.SampleRate, bc.Kbps, bc.Coder)] = bc
	}
	if len(cur) != len(base.Cases) {
		t.Errorf("measured %d cases but baseline has %d: refresh with UPDATE_QUALITY_BASELINE=1", len(cur), len(base.Cases))
	}
	for _, cc := range cur {
		bc, ok := byKey[caseKey(cc.Program, cc.SampleRate, cc.Kbps, cc.Coder)]
		if !ok {
			t.Errorf("%s %d/%d %s: no baseline case; refresh with UPDATE_QUALITY_BASELINE=1", cc.Program, cc.SampleRate, cc.Kbps, cc.Coder)
			continue
		}
		t.Run(fmt.Sprintf("%s_%d_%d_%s", cc.Program, cc.SampleRate, cc.Kbps, cc.Coder), func(t *testing.T) {
			checkRegression(t, &bc, &cc)
		})
	}
}

// checkRegression fails t for every gated metric of cur that differs from base
// beyond its tolerance, in either direction: a regression, or an improvement
// that must be locked into a refreshed baseline. It defers the decision to the
// pure regressions helper so the same logic is unit-testable without a
// testing.T.
func checkRegression(t *testing.T, base, cur *baselineCase) {
	t.Helper()
	for _, msg := range regressions(base, cur) {
		t.Error(msg)
	}
}

// regressions returns one message per gated metric of cur that moved past its
// tolerance against base in either direction (a regression, or an improvement
// the baseline must be refreshed to lock in); an empty result means the case
// passes. A baseline null (metric undefined for the case) is skipped; a current
// value that turned non-finite where the baseline was finite is a regression.
func regressions(base, cur *baselineCase) []string {
	// SNR is finite for every active (non-silent) program in the corpus, so a
	// null baseline SNR means the baseline file is degenerate or corrupt, not
	// that the metric is legitimately undefined (as LSD and PreEcho can be for a
	// tonal program). Fail loudly rather than skip every metric below and pass
	// vacuously on a broken baseline.
	if base.SNR == nil {
		return []string{"baseline SNR is null: the baseline looks corrupt, refresh with UPDATE_QUALITY_BASELINE=1"}
	}
	var out []string
	// Alignment lag is go-aac's algorithmic delay. A shifted delay is scored away
	// by the cross-correlation aligner, so SNR/LSD would still pass while gapless
	// playback and A/V sync broke; gate it with zero tolerance. It is a stable
	// integer argmax of a sharply peaked cross-correlation, so ~1e-13 cross-arch
	// perturbations cannot move it. PreEchoN is deliberately NOT gated: it counts
	// attacks in the reference only (independent of the encoder), so it cannot
	// signal an encoder regression; it is kept as informational context. The
	// transient quality signal lives in the PreEcho dB value below.
	if cur.Lag != base.Lag {
		out = append(out, fmt.Sprintf("alignment lag changed: %d, baseline %d (encoder delay regression)", cur.Lag, base.Lag))
	}
	for _, m := range []struct {
		name      string
		base, cur *float64
		tol       float64
		lower     bool // true: lower is better (LSD, PreEcho)
	}{
		{"SNR", base.SNR, cur.SNR, tolSNR, false},
		{"BandSNR", base.BandSNR, cur.BandSNR, tolBandSNR, false},
		{"SegSNR", base.SegSNR, cur.SegSNR, tolSegSNR, false},
		{"LSD", base.LSD, cur.LSD, tolLSD, true},
		{"PreEcho", base.PreEcho, cur.PreEcho, tolPreEcho, true},
	} {
		if m.base == nil {
			continue
		}
		if m.cur == nil {
			out = append(out, fmt.Sprintf("%s regressed to undefined (baseline %.3f dB)", m.name, *m.base))
			continue
		}
		if m.lower && *m.cur > *m.base+m.tol {
			out = append(out, fmt.Sprintf("%s regressed: %.3f dB, baseline %.3f dB, tolerance %.2f dB (higher is worse)", m.name, *m.cur, *m.base, m.tol))
		}
		if !m.lower && *m.cur < *m.base-m.tol {
			out = append(out, fmt.Sprintf("%s regressed: %.3f dB, baseline %.3f dB, tolerance %.2f dB (lower is worse)", m.name, *m.cur, *m.base, m.tol))
		}
		// Ratchet the other direction too: a significant improvement also trips
		// the gate, so a quality-tuning PR must refresh the baseline to lock the
		// gain in. Left stale, an improved metric lets a later regression back to
		// the old value pass unseen. The regression and improvement conditions are
		// mutually exclusive (cur beyond base+tol vs beyond base-tol), so a metric
		// trips at most one, and a change within tolerance trips neither.
		if m.lower && *m.cur < *m.base-m.tol {
			out = append(out, fmt.Sprintf("%s improved past tolerance: %.3f dB, baseline %.3f dB (lower is better); refresh with UPDATE_QUALITY_BASELINE=1 to lock it in", m.name, *m.cur, *m.base))
		}
		if !m.lower && *m.cur > *m.base+m.tol {
			out = append(out, fmt.Sprintf("%s improved past tolerance: %.3f dB, baseline %.3f dB (higher is better); refresh with UPDATE_QUALITY_BASELINE=1 to lock it in", m.name, *m.cur, *m.base))
		}
	}
	return out
}

func caseKey(program string, sampleRate, kbps int, coder string) string {
	return fmt.Sprintf("%s|%d|%d|%s", program, sampleRate, kbps, coder)
}

// writeBaseline rewrites the committed baseline from the measured cases.
func writeBaseline(t *testing.T, cases []baselineCase) {
	t.Helper()
	doc := baselineFile{
		SchemaVersion: baselineSchemaVersion,
		Seconds:       baselineSeconds,
		Note:          "go-aac objective quality regression baseline; refresh with UPDATE_QUALITY_BASELINE=1 (a deliberate, reviewed action).",
		Cases:         cases,
	}
	if err := os.MkdirAll(filepath.Dir(baselinePath), 0o755); err != nil {
		t.Fatalf("create baseline dir: %v", err)
	}
	f, err := os.Create(baselinePath)
	if err != nil {
		t.Fatalf("create baseline: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Fatalf("close baseline: %v", cerr)
		}
	}()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatalf("encode baseline: %v", err)
	}
	t.Logf("wrote %s (%d cases)", baselinePath, len(cases))
}

// readBaseline loads the committed baseline.
func readBaseline(t *testing.T) baselineFile {
	t.Helper()
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline (generate it with UPDATE_QUALITY_BASELINE=1): %v", err)
	}
	var doc baselineFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	return doc
}
