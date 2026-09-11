package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/tphakala/go-aac/internal/quality"
)

// mkResult builds one encoder's result. mos is NaN for a case the external tool
// did not measure, which is what makes the partial-NaN paths reachable.
func mkResult(snr, lsd, mos float64) encoderResult {
	return encoderResult{
		Lag: 0, Bytes: 1000, MOS: mos, ODG: math.NaN(),
		Metrics: quality.Metrics{SNR: snr, BandSNR: snr, SegSNR: snr, LSD: lsd, PreEcho: math.NaN(), Bandwidth: 16000},
	}
}

// sampleCases is deliberately ASYMMETRIC: at 128 kbps go-aac wins SNR on two of
// three programs and LSD on one, so a flipped win direction changes the counts.
// Exactly one program has a finite MOS, so a summary that divided by the program
// count instead of the compared count would be wrong. A second sample rate is
// present so the per-rate grouping is exercised.
func sampleCases() []caseResult {
	return []caseResult{
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 128, Coder: "nmr",
			GoAAC: mkResult(30, 2, 4.5), FFmpegAAC: mkResult(28, 3, 4.1)},
		{Program: "b", Channels: 1, SampleRate: 44100, Kbps: 128, Coder: "nmr",
			GoAAC: mkResult(20, 4, math.NaN()), FFmpegAAC: mkResult(26, 2, math.NaN())},
		{Program: "c", Channels: 1, SampleRate: 44100, Kbps: 128, Coder: "nmr",
			GoAAC: mkResult(35, 5, math.NaN()), FFmpegAAC: mkResult(31, 1, math.NaN())},
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 192, Coder: "nmr",
			GoAAC: mkResult(50, 1, math.NaN()), FFmpegAAC: mkResult(50, 1, math.NaN())},
		{Program: "a", Channels: 2, SampleRate: 48000, Kbps: 128, Coder: "nmr",
			GoAAC: mkResult(40, 2, math.NaN()), FFmpegAAC: mkResult(38, 2, math.NaN())},
	}
}

func TestSummarize(t *testing.T) {
	rows := summarize(sampleCases())
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3 (two bitrates at 44100 plus one at 48000)", len(rows))
	}
	if rows[0].SampleRate != 44100 || rows[0].Kbps != 128 ||
		rows[1].SampleRate != 44100 || rows[1].Kbps != 192 ||
		rows[2].SampleRate != 48000 || rows[2].Kbps != 128 {
		t.Fatalf("rows not grouped and sorted by (rate, kbps): %+v", rows)
	}

	r := rows[0]
	if r.Programs != 3 {
		t.Fatalf("programs = %d, want 3", r.Programs)
	}
	// (30-28 + 20-26 + 35-31)/3 = 0
	if got := r.MeanDelta[mSNR]; math.Abs(got) > 1e-9 {
		t.Fatalf("mean SNR delta = %v, want 0", got)
	}
	// Distinct counts, so an inverted direction cannot reproduce them.
	if r.Wins[mSNR] != 2 || r.Wins[mLSD] != 1 {
		t.Fatalf("wins SNR=%d LSD=%d, want 2 and 1", r.Wins[mSNR], r.Wins[mLSD])
	}
	// One program carried a finite MOS, so the mean is that single delta and the
	// denominator is 1, not 3.
	if got := r.MeanDelta[mMOS]; math.Abs(got-0.4) > 1e-9 {
		t.Fatalf("mean MOS delta = %v, want 0.4 (the one compared program)", got)
	}
	if r.Compared[mMOS] != 1 || r.Compared[mSNR] != 3 {
		t.Fatalf("compared MOS=%d SNR=%d, want 1 and 3", r.Compared[mMOS], r.Compared[mSNR])
	}
	if !math.IsNaN(r.MeanDelta[mPreEcho]) || !math.IsNaN(r.MeanDelta[mODG]) {
		t.Fatalf("all-NaN metrics must summarize to NaN: %+v", r.MeanDelta)
	}
	// Bandwidth is informational, so it is never counted as a win.
	if r.Wins[mBandwidth] != 0 {
		t.Fatalf("Bandwidth wins = %d, want 0 (it is not scored)", r.Wins[mBandwidth])
	}
	if rows[1].Wins[mSNR] != 0 || rows[1].MeanDelta[mSNR] != 0 {
		t.Fatalf("a tie is neither a win nor a delta: %+v", rows[1])
	}
}

// TestSummarizeSplitsByCoder: two cases identical but for the coder must stay in
// distinct rows, so a regression in one coder is not averaged into the other.
func TestSummarizeSplitsByCoder(t *testing.T) {
	cases := []caseResult{
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 128, Coder: "nmr", GoAAC: mkResult(40, 2, math.NaN()), FFmpegAAC: mkResult(38, 2, math.NaN())},
		{Program: "a", Channels: 1, SampleRate: 44100, Kbps: 128, Coder: "fast", GoAAC: mkResult(30, 2, math.NaN()), FFmpegAAC: mkResult(38, 2, math.NaN())},
	}
	rows := summarize(cases)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2 (one per coder)", len(rows))
	}
	if rows[0].Coder == rows[1].Coder {
		t.Fatalf("rows not split by coder: both %q", rows[0].Coder)
	}
}

func TestWriteMarkdownAndJSON(t *testing.T) {
	rep := &report{SchemaVersion: reportSchemaVersion, GeneratedUTC: "2026-09-11T00:00:00Z", GoAACRev: "abc1234",
		FFmpegVersion: "ffmpeg version n7.1", LibFDK: false, Seconds: 6, Attempted: 6, Failed: 1, Cases: sampleCases()}
	var md bytes.Buffer
	if err := writeMarkdown(&md, rep); err != nil {
		t.Fatal(err)
	}
	s := md.String()

	// A format-verb error corrupts the header without failing anything else,
	// which is exactly how a summary table can ship unrenderable.
	if strings.Contains(s, "%!") {
		t.Fatalf("format-verb error in the rendered markdown:\n%s", s)
	}
	if strings.Contains(s, "NaN") {
		t.Fatalf("markdown must render a non-finite value as n/a:\n%s", s)
	}
	// One fully rendered numeric row pins the delta's SIGN, the go/ffmpeg column
	// order, the coder cell, and the kHz conversion of Bandwidth.
	const wantRow = "| a | 128 | nmr | 1 | 0 | 0 | 30.00 | 28.00 | 2.00 |"
	if !strings.Contains(s, wantRow) {
		t.Fatalf("row a not rendered as %q:\n%s", wantRow, s)
	}
	if !strings.Contains(s, "16.00 | 16.00 | 0.00") {
		t.Fatalf("Bandwidth must render in kHz:\n%s", s)
	}
	for _, want := range []string{
		"go-aac versus ffmpeg quality report", "ffmpeg version n7.1", "abc1234",
		"libfdk_aac: absent in this ffmpeg build", "## Versus ffmpeg aac", "### 44100 Hz", "### 48000 Hz",
		"## Summary", notMeasured, "External metrics: none", "6 attempted, 1 failed", "2/3",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("markdown missing %q:\n%s", want, s)
		}
	}
	// libfdk absent: no libfdk comparison section.
	if strings.Contains(s, "Versus libfdk_aac") {
		t.Fatalf("libfdk section rendered when the build has no libfdk_aac:\n%s", s)
	}
	// A metric nothing compared reports n/a wins, not a clean sweep of losses.
	if strings.Contains(s, "0/3") {
		t.Fatalf("an unmeasured metric must not report wins over the program count:\n%s", s)
	}

	var js bytes.Buffer
	if err := writeJSON(&js, rep); err != nil {
		t.Fatal(err)
	}
	var back struct {
		SchemaVersion int    `json:"schema_version"`
		FFmpegVersion string `json:"ffmpeg_version"`
		LibFDK        bool   `json:"libfdk"`
		Attempted     int    `json:"attempted"`
		Failed        int    `json:"failed"`
		Cases         []struct {
			Program string `json:"program"`
			Coder   string `json:"coder"`
			GoAAC   struct {
				SNR     *float64 `json:"snr"`
				PreEcho *float64 `json:"pre_echo"`
				MOS     *float64 `json:"mos"`
			} `json:"goaac"`
			LibFDK *struct{} `json:"libfdk_aac"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatalf("json: %v\n%s", err, js.String())
	}
	if back.FFmpegVersion != rep.FFmpegVersion || len(back.Cases) != 5 || back.Cases[0].Program != "a" || back.Cases[0].Coder != "nmr" {
		t.Fatalf("json round trip: %+v", back)
	}
	if back.Attempted != 6 || back.Failed != 1 || back.SchemaVersion != reportSchemaVersion || back.LibFDK {
		t.Fatalf("json header: attempted=%d failed=%d schema=%d libfdk=%v", back.Attempted, back.Failed, back.SchemaVersion, back.LibFDK)
	}
	c := back.Cases[0].GoAAC
	if c.SNR == nil || *c.SNR != 30 || c.PreEcho != nil || c.MOS == nil || *c.MOS != 4.5 {
		t.Fatalf("json nullable floats: snr=%v pre_echo=%v mos=%v", c.SNR, c.PreEcho, c.MOS)
	}
	if back.Cases[0].LibFDK != nil {
		t.Fatalf("json libfdk_aac must be null when the build has no libfdk_aac")
	}
}

// TestLibFDKColumnOptional: when the ffmpeg build carries libfdk_aac, the report
// gains a "Versus libfdk_aac" section and each case's JSON libfdk_aac is
// populated; when it does not, both are omitted.
func TestLibFDKColumnOptional(t *testing.T) {
	fdk := mkResult(33, 2.5, math.NaN())
	cases := []caseResult{{
		Program: "a", Channels: 1, SampleRate: 44100, Kbps: 128, Coder: "nmr",
		GoAAC: mkResult(40, 2, math.NaN()), FFmpegAAC: mkResult(38, 2, math.NaN()), LibFDK: &fdk,
	}}
	rep := &report{SchemaVersion: reportSchemaVersion, LibFDK: true, Cases: cases}
	var md bytes.Buffer
	if err := writeMarkdown(&md, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "## Versus libfdk_aac") {
		t.Fatalf("libfdk present: report is missing the libfdk section:\n%s", md.String())
	}
	if !strings.Contains(md.String(), "libfdk_aac: present") {
		t.Fatalf("header must note libfdk present:\n%s", md.String())
	}
	var js bytes.Buffer
	if err := writeJSON(&js, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), `"libfdk_aac": {`) {
		t.Fatalf("libfdk present: JSON case must carry a libfdk_aac object:\n%s", js.String())
	}
}

// TestCellEscapes: a program name comes from a corpus file name, so a pipe or a
// newline in it must not split a cell or forge a row.
func TestCellEscapes(t *testing.T) {
	if got := cell("a|b"); got != `a\|b` {
		t.Fatalf("cell(%q) = %q", "a|b", got)
	}
	if got := cell("row\ninject"); got != "row inject" {
		t.Fatalf("cell with a newline = %q", got)
	}
}

func TestFmtMetric(t *testing.T) {
	if fmtMetric(math.NaN()) != notMeasured || fmtMetric(math.Inf(1)) != notMeasured ||
		fmtMetric(1.234) != "1.23" || fmtMetric(-0.5) != "-0.50" {
		t.Fatalf("fmtMetric: %q %q %q", fmtMetric(math.NaN()), fmtMetric(math.Inf(1)), fmtMetric(1.234))
	}
}

// TestMetricsTableComplete: every metric must be readable and have a direction.
// The single table makes a half-added metric impossible, and this pins that it
// stays a single complete table.
func TestMetricsTableComplete(t *testing.T) {
	seen := map[string]bool{}
	r := mkResult(1, 2, 3)
	for _, m := range metrics {
		if m.name == "" || m.get == nil {
			t.Fatalf("metric %+v is incomplete", m)
		}
		if seen[m.name] {
			t.Fatalf("duplicate metric %q", m.name)
		}
		seen[m.name] = true
		if m.better != 1 && m.better != -1 {
			t.Fatalf("metric %q has direction %d, want +1 or -1", m.name, m.better)
		}
		if math.IsNaN(m.get(&r)) && m.name != mPreEcho && m.name != mODG {
			t.Fatalf("metric %q reads NaN from a fully populated result", m.name)
		}
	}
	if len(metrics) != 8 {
		t.Fatalf("%d metrics, want 8", len(metrics))
	}
}
