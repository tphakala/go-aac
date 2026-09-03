// SPDX-License-Identifier: LGPL-2.1-or-later

// Package oracletest holds the FFmpeg/afconvert oracle scaffolding shared by
// the differential and parity tests in the root aac package and the pcm
// package. Both once carried byte-identical copies of the pinned-ffmpeg
// discovery, the bounded-subprocess pattern and the ADTS decode helper; those
// copies drifted apart (one gained a timeout context, the other a richer
// failure diagnosis) which is exactly the silent-divergence hazard this
// package removes by giving them a single home.
//
// Every helper takes testing.TB rather than *testing.T so the same code serves
// tests and could serve benchmarks, and none of it imports the packages under
// test, so it is safe to import from either.
package oracletest

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Timeout bounds one ffmpeg or afconvert invocation. The longest oracle in the
// suite is the 24 s stereo castanet encode at 192 kb/s, measured under 3 s on a
// laptop, so this is a wide margin for a loaded runner while still surfacing a
// hung oracle as a named failure instead of the test binary's own timeout panic
// twenty minutes later.
const Timeout = 2 * time.Minute

// Ctx returns a context that expires after Timeout, for bounding a subprocess a
// caller runs itself via exec.CommandContext. The cancel is registered with
// t.Cleanup so the caller passes the context straight to exec.CommandContext
// without carrying a defer.
func Ctx(tb testing.TB) context.Context {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), Timeout)
	tb.Cleanup(cancel)
	return ctx
}

// Run runs ffmpeg with args under Timeout and returns its stdout. Every caller
// passes -v error (or its -loglevel error long form), so anything on stderr is
// a failure. The three ways it can
// fail are reported apart, because they need different fixes: it never started,
// it ran out of time, or it ran and failed or complained.
//
// "Never started" is keyed on a nil ProcessState rather than on an error type.
// Wait sets ProcessState for every process that actually ran, including one the
// deadline killed, while both spawn failures leave it nil: *exec.Error when a
// bare name misses on PATH, and *fs.PathError from fork/exec when a path is
// missing, a directory, or not executable. GOAAC_FFMPEG is a path that
// FFmpegBin has stat'ed, so it is the second of those that this test suite can
// actually produce.
func Run(tb testing.TB, ffmpeg string, args ...string) []byte {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), Timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	switch {
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		tb.Fatalf("ffmpeg did not finish within %v: %v", Timeout, args)
	case err != nil && cmd.ProcessState == nil:
		tb.Fatalf("cannot run GOAAC_FFMPEG=%q: %v", ffmpeg, err)
	case err != nil:
		tb.Fatalf("ffmpeg failed: %v\n%s", err, stderr.String())
	}
	if stderr.Len() > 0 {
		tb.Fatalf("ffmpeg -v error reported diagnostics:\n%s", stderr.String())
	}
	return stdout.Bytes()
}

// FFmpegBin returns the pinned FFmpeg CLI named by GOAAC_FFMPEG, skipping the
// test (or failing it under GOAAC_REQUIRE_ORACLE) when it is unset or unusable.
// The gate compares against FFmpeg pinned at d09d5afc3a; a distro ffmpeg is not
// a valid oracle (it predates the NMR coder), so there is deliberately no
// default path to fall back on.
func FFmpegBin(tb testing.TB) string {
	tb.Helper()
	p := os.Getenv("GOAAC_FFMPEG")
	if p == "" {
		SkipOrFatalOracle(tb, "GOAAC_FFMPEG is not set; skipping the pinned-ffmpeg oracle test")
	}
	fi, err := os.Stat(p)
	if err != nil {
		SkipOrFatalOracle(tb, fmt.Sprintf("GOAAC_FFMPEG=%q is not usable: %v", p, err))
	}
	// Stat succeeds on a directory, and the easy mistake is pointing at the
	// FFmpeg build tree instead of the binary inside it. Catch it here, where
	// the message can say so, rather than at the first exec.
	if fi.IsDir() {
		SkipOrFatalOracle(tb, fmt.Sprintf("GOAAC_FFMPEG=%q is a directory, not the ffmpeg binary", p))
	}
	return p
}

// SkipOrFatalOracle skips the calling test, or fails it when
// GOAAC_REQUIRE_ORACLE is set to a non-empty value.
//
// Skipping is right for a contributor without the pinned FFmpeg. It is wrong
// for a runner whose whole job is to run the gate: a mistyped path or a broken
// build would skip every differential test and still print ok, which is exactly
// how a rate-control regression once passed CI green. The CI oracle job sets
// GOAAC_REQUIRE_ORACLE so that an absent oracle reports red.
func SkipOrFatalOracle(tb testing.TB, msg string) {
	tb.Helper()
	if os.Getenv("GOAAC_REQUIRE_ORACLE") != "" {
		tb.Fatalf("GOAAC_REQUIRE_ORACLE is set, so a missing oracle is a failure: %s", msg)
	}
	tb.Skip(msg)
}

// DecodeFile decodes an ADTS file to planar float32 with the pinned ffmpeg, one
// slice per channel, failing on any decoder diagnostic. It deinterleaves here
// so no caller carries the split.
//
// minSamples is a per-channel floor below which the decode is treated as no
// result rather than scored, because a decode too short to score is a defect,
// not a pass. The two PSNR scorers react to a short decode differently
// (PSNRPrefix accumulates nothing and returns NaN, which a `<` floor passes;
// PSNRStrict returns -Inf, which it fails), so the floor is enforced here,
// once, ahead of either. Callers that score the available prefix pass
// EncoderDelay (a decode of exactly the priming length is then caught too);
// callers with no such floor pass 0, which still rejects an empty decode.
func DecodeFile(tb testing.TB, ffmpeg, path string, channels, minSamples int) [][]float32 {
	tb.Helper()
	raw := Run(tb, ffmpeg, "-v", "error", "-i", path,
		"-f", "f32le", "-c:a", "pcm_f32le", "-")
	n := len(raw) / 4 / channels
	if n <= minSamples {
		tb.Fatalf("ffmpeg decoded %s to %d samples per channel, at or below the "+
			"%d-sample floor, too short to score against the source "+
			"(%d bytes, %d channels)", path, n, minSamples, len(raw), channels)
	}
	out := make([][]float32, channels)
	for c := range out {
		out[c] = make([]float32, n)
	}
	for i := range n {
		for c := range channels {
			out[c][i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*channels+c)*4:]))
		}
	}
	return out
}

// DecodeStream writes an in-memory ADTS stream to a temp file and decodes it
// through DecodeFile, so a caller holding bytes rather than a path does not
// carry the write.
func DecodeStream(tb testing.TB, ffmpeg string, stream []byte, channels, minSamples int) [][]float32 {
	tb.Helper()
	p := filepath.Join(tb.TempDir(), "stream.adts")
	if err := os.WriteFile(p, stream, 0o644); err != nil {
		tb.Fatal(err)
	}
	return DecodeFile(tb, ffmpeg, p, channels, minSamples)
}

// WriteRawF32 writes planar src as the interleaved little-endian f32le that the
// C encoder reads with -f f32le -ac len(src). It is the encode-side twin of the
// decode helpers: a test that feeds the pinned encoder the same samples the Go
// encoder sees writes them once through here. Pass a single-channel src for a
// mono encode.
func WriteRawF32(tb testing.TB, path string, src [][]float32) {
	tb.Helper()
	ch := len(src)
	raw := make([]byte, 4*ch*len(src[0]))
	for i := range src[0] {
		for c := range ch {
			binary.LittleEndian.PutUint32(raw[4*(i*ch+c):], math.Float32bits(src[c][i]))
		}
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		tb.Fatal(err)
	}
}

// PSNRPrefix and PSNRStrict both score dec against src at full scale 1.0 after
// dropping delay priming samples, and differ only in how they treat a decode
// shorter than delay+len(src). They are kept as two named functions, rather
// than unified, because the two gate families deliberately want different
// truncation policies and unifying them would move the pass/fail line of one.

// PSNRPrefix scores whatever prefix decoded, stopping at the end of dec. It
// suits gates that assert a floor over the samples that exist and pin
// truncation elsewhere (the decode helper's minSamples floor).
func PSNRPrefix(src, dec []float32, delay int) float64 {
	var mse float64
	n := 0
	for i := range src {
		if delay+i >= len(dec) {
			break
		}
		d := float64(src[i]) - float64(dec[delay+i])
		mse += d * d
		n++
	}
	mse /= float64(n)
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(1/mse)
}

// PSNRStrict returns -Inf for a truncated decode instead of scoring a prefix as
// perfect, so a stream that decodes short fails any PSNR floor. It suits gates
// where a short decode is itself the defect being guarded against.
func PSNRStrict(src, dec []float32, delay int) float64 {
	if len(dec) < delay+len(src) {
		return math.Inf(-1)
	}
	var mse float64
	for i := range src {
		d := float64(src[i]) - float64(dec[delay+i])
		mse += d * d
	}
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(1/(mse/float64(len(src))))
}
