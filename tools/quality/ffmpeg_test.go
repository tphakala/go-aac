package main

import (
	"bytes"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/oracletest"
	"github.com/tphakala/go-aac/internal/quality"
)

// requireFFmpeg returns a tools using the pinned oracle ffmpeg, skipping (or,
// under GOAAC_REQUIRE_ORACLE, failing) when it is not configured. A distro
// ffmpeg is not a valid oracle for this suite, so these tests bind to
// GOAAC_FFMPEG like the rest of the differential gate.
func requireFFmpeg(t *testing.T) tools {
	t.Helper()
	bin := oracletest.FFmpegBin(t)
	return tools{ffmpeg: bin, libfdk: hasLibFDK(t.Context(), bin)}
}

// abs helps the size-parity check read cleanly.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestQualityHarnessFFmpeg runs one real case end to end: go-aac and ffmpeg's
// native aac, both decoded by this project's pcm decoder, aligned, and scored.
// It pins that go-aac's tagless stream lands at EncoderDelay, that the ffmpeg
// stream decodes and aligns to a sane measurement (the proof that pcm decodes a
// third-party ADTS stream), and sanity-bounds the metrics and the size gap.
func TestQualityHarnessFFmpeg(t *testing.T) {
	tl := requireFFmpeg(t)
	prog, ok := quality.ProgramByName("tone-click")
	if !ok {
		t.Fatal("tone-click program missing")
	}
	spec := caseSpec{Program: prog, SampleRate: 44100, Kbps: 64, Coder: aac.CoderNMR, Seconds: 2}
	res, err := runCase(t.Context(), tl, t.TempDir(), spec, genRef(prog, spec.SampleRate, spec.Seconds), false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.GoAAC.Lag != aac.EncoderDelay {
		t.Fatalf("go-aac lag = %d, want EncoderDelay %d", res.GoAAC.Lag, aac.EncoderDelay)
	}
	// ffmpeg's aac primes on the same order as go-aac; the exact value is not
	// pinned (it is ffmpeg's, not ours), only that alignment found a positive,
	// bounded delay rather than failing.
	if res.FFmpegAAC.Lag <= 0 || res.FFmpegAAC.Lag > alignMaxLag {
		t.Fatalf("ffmpeg lag = %d, want a positive delay within the search window", res.FFmpegAAC.Lag)
	}
	for _, r := range []encoderResult{res.GoAAC, res.FFmpegAAC} {
		if r.Bytes < 500 {
			t.Fatalf("%s: stream only %d bytes", r.Name, r.Bytes)
		}
		if r.Metrics.SNR < 5 || r.Metrics.SNR > quality.SNRCap {
			t.Fatalf("%s: SNR %v out of sane range", r.Name, r.Metrics.SNR)
		}
		if math.IsNaN(r.Metrics.LSD) || r.Metrics.LSD <= 0 {
			t.Fatalf("%s: LSD %v", r.Name, r.Metrics.LSD)
		}
		if r.Metrics.PreEchoN == 0 || math.IsNaN(r.Metrics.PreEcho) {
			t.Fatalf("%s: tone-click produced no pre-echo events", r.Name)
		}
	}
	// Both encoders target the same bitrate, so their ADTS sizes are within a
	// quarter of each other; a wildly different size means -b:a did not take.
	if d := abs(res.FFmpegAAC.Bytes - res.GoAAC.Bytes); d > res.GoAAC.Bytes/4 {
		t.Fatalf("stream sizes diverge: go-aac %d, ffmpeg %d (delta %d > 25%%)", res.GoAAC.Bytes, res.FFmpegAAC.Bytes, d)
	}
	if !tl.libfdk && res.LibFDK != nil {
		t.Fatal("LibFDK result present though the ffmpeg build has no libfdk_aac")
	}
}

// TestQualityHarnessStereoShortTail: a stereo program whose length is not a
// whole number of frames exercises the short final frame and the two-channel
// alignment path.
func TestQualityHarnessStereoShortTail(t *testing.T) {
	tl := requireFFmpeg(t)
	// stereo-wide aligns stably at the encoder delay under every coder, so the
	// test is not a latent flake if the pinned coder is ever changed; it is
	// 2-channel and 1 s is still a short final frame.
	prog, ok := quality.ProgramByName("stereo-wide")
	if !ok {
		t.Fatal("stereo-wide program missing")
	}
	// 1 s at 44.1 kHz is 43.07 frames of 1024: the last go-aac frame is short.
	spec := caseSpec{Program: prog, SampleRate: 44100, Kbps: 192, Coder: aac.CoderNMR, Seconds: 1}
	res, err := runCase(t.Context(), tl, t.TempDir(), spec, genRef(prog, spec.SampleRate, spec.Seconds), false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res.Channels != 2 || res.GoAAC.Lag != aac.EncoderDelay {
		t.Fatalf("stereo case: channels=%d go-aac lag=%d", res.Channels, res.GoAAC.Lag)
	}
	if res.GoAAC.Metrics.SNR < 5 || res.FFmpegAAC.Metrics.SNR < 5 {
		t.Fatalf("stereo SNRs %v / %v implausibly low", res.GoAAC.Metrics.SNR, res.FFmpegAAC.Metrics.SNR)
	}
}

// TestCrossCheck drives the -crosscheck diagnostic both ways against a real
// go-aac stream decoded by ffmpeg's aac_fixed: this project's pcm decode of the
// same stream agrees and stays silent, while a phase-inverted decode (same
// energy, so its SNR is defined, but about -6 dB against ffmpeg's) trips the
// warning. It needs the oracle ffmpeg but not a second encoder, since
// encodeGoAAC produces the stream.
func TestCrossCheck(t *testing.T) {
	tl := requireFFmpeg(t)
	prog, ok := quality.ProgramByName(progMultitone)
	if !ok {
		t.Fatalf("program %q missing", progMultitone)
	}
	const sr, kbps = 44100, 128
	ref := genRef(prog, sr, 2)
	stream, err := encodeGoAAC(ref, sr, kbps, aac.CoderNMR)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const aacName = "x.aac"
	if err := os.WriteFile(filepath.Join(dir, aacName), stream, 0o644); err != nil {
		t.Fatal(err)
	}
	deg, err := decodeStream(stream)
	if err != nil {
		t.Fatal(err)
	}

	// Agreement: pcm and ffmpeg aac_fixed decodes of the same stream must not
	// trip the divergence warning.
	var buf bytes.Buffer
	crossCheck(t.Context(), tl, dir, "go-aac", aacName, deg, sr, &buf)
	if s := buf.String(); s != "" {
		t.Fatalf("agreeing pcm and ffmpeg decodes must be silent, got: %q", s)
	}

	// Divergence: a phase-inverted copy scores about -6 dB against ffmpeg's and
	// must trip the warning.
	inv := make([][]float64, len(deg))
	for c := range deg {
		inv[c] = make([]float64, len(deg[c]))
		for i, v := range deg[c] {
			inv[c][i] = -v
		}
	}
	buf.Reset()
	crossCheck(t.Context(), tl, dir, "go-aac", aacName, inv, sr, &buf)
	if !strings.Contains(buf.String(), "diverge") {
		t.Fatalf("phase-inverted decode must trip the divergence warning, got: %q", buf.String())
	}
}

// TestCrossCheckWorstChannel pins the worst-channel reducer against a stereo
// stream with one divergent channel, two ways. The AGREEING case pins the
// worst-over-mean choice; the SILENT case pins the NaN skip. Both must warn.
func TestCrossCheckWorstChannel(t *testing.T) {
	tl := requireFFmpeg(t)
	prog, ok := quality.ProgramByName("stereo-wide")
	if !ok {
		t.Fatal("stereo-wide program missing")
	}
	const sr, kbps = 44100, 128
	ref := genRef(prog, sr, 2)
	stream, err := encodeGoAAC(ref, sr, kbps, aac.CoderNMR)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const aacName = "s.aac"
	if err := os.WriteFile(filepath.Join(dir, aacName), stream, 0o644); err != nil {
		t.Fatal(err)
	}
	deg, err := decodeStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(deg) != 2 {
		t.Fatalf("expected a stereo decode, got %d channels", len(deg))
	}
	invRight := make([]float64, len(deg[1]))
	for i, v := range deg[1] {
		invRight[i] = -v
	}

	// Left agrees, right diverges: the worst channel warns; a mean would not.
	var buf bytes.Buffer
	crossCheck(t.Context(), tl, dir, "go-aac", aacName, [][]float64{deg[0], invRight}, sr, &buf)
	if !strings.Contains(buf.String(), "diverge") {
		t.Fatalf("an agreeing left channel must not hide a divergent right (a mean would): %q", buf.String())
	}

	// Left is digital silence (SNR NaN, skipped), right diverges: the skip must
	// not turn the min into NaN and swallow the warning.
	buf.Reset()
	crossCheck(t.Context(), tl, dir, "go-aac", aacName, [][]float64{make([]float64, len(deg[0])), invRight}, sr, &buf)
	if !strings.Contains(buf.String(), "diverge") {
		t.Fatalf("a silent left channel (NaN) must be skipped, not hide a divergent right: %q", buf.String())
	}
}

// TestRunGridOrderAndReclaim exercises the concurrent path end to end: rep.Cases
// lands in deterministic grid order regardless of which worker finishes first,
// every case succeeds, and the per-case dir reclaim honors the -work/-keep gate.
// Needs the oracle ffmpeg (it runs full cases).
func TestRunGridOrderAndReclaim(t *testing.T) {
	tl := requireFFmpeg(t)
	progs := make([]quality.Program, 0, 2)
	for _, name := range []string{progMultitone, progToneClick} {
		p, ok := quality.ProgramByName(name)
		if !ok {
			t.Fatalf("program %q missing", name)
		}
		progs = append(progs, p)
	}
	newOpts := func(jobs int, work string) *options {
		return &options{rates: []int{44100}, bitrates: []int{128, 192}, coders: []aac.Coder{aac.CoderNMR}, programs: progs, seconds: 1, jobs: jobs, work: work}
	}
	runIt := func(o *options, workDir string) []caseResult {
		rep := &report{}
		failed, err := runGrid(t.Context(), tl, o, workDir, rep, io.Discard)
		if err != nil {
			t.Fatalf("runGrid: %v", err)
		}
		if failed != 0 {
			t.Fatalf("runGrid reported %d failed cases", failed)
		}
		return rep.Cases
	}

	// A throwaway temp tree (o.work == "") at -jobs 4: order must be the grid
	// order even though workers finish out of order, and each case dir must be
	// reclaimed as it completes.
	autoDir := t.TempDir()
	cases := runIt(newOpts(4, ""), autoDir)
	want := []struct {
		program string
		kbps    int
	}{{progMultitone, 128}, {progMultitone, 192}, {progToneClick, 128}, {progToneClick, 192}}
	if len(cases) != len(want) {
		t.Fatalf("got %d cases, want %d", len(cases), len(want))
	}
	for i, w := range want {
		if cases[i].Program != w.program || cases[i].Kbps != w.kbps {
			t.Fatalf("case %d = %s/%d, want %s/%d (order must be deterministic under -jobs>1)", i, cases[i].Program, cases[i].Kbps, w.program, w.kbps)
		}
	}
	if entries, err := os.ReadDir(autoDir); err != nil || len(entries) != 0 {
		t.Fatalf("reclaim: temp work dir should be empty after the run, has %d entries (err %v)", len(entries), err)
	}

	// An explicit -work dir must keep its artifacts for inspection: reclaim is
	// gated on o.work == "".
	keepDir := t.TempDir()
	_ = runIt(newOpts(2, keepDir), keepDir)
	kept, err := os.ReadDir(keepDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != len(want) {
		t.Fatalf("explicit -work must keep %d case dirs, found %d", len(want), len(kept))
	}
}

// TestTo48k covers the two branches that need no ffmpeg: a 48 kHz input is
// returned unchanged, and a non-48 kHz rate with ffmpeg absent is an error
// rather than a silent skip that would hand a scorer an unresampled file.
func TestTo48k(t *testing.T) {
	if out, err := to48k(t.Context(), tools{}, t.TempDir(), "x.wav", 48000); err != nil || out != "x.wav" {
		t.Fatalf("48 kHz bypass: out=%q err=%v", out, err)
	}
	if _, err := to48k(t.Context(), tools{}, t.TempDir(), "x.wav", 44100); err == nil {
		t.Fatal("44.1 kHz without ffmpeg must error")
	}
}
