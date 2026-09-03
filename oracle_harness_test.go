// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
	"github.com/tphakala/go-aac/internal/oracletest"
)

// The C side of the differential gates: running the pinned ffmpeg, deriving
// its options from an enc.Config, and scoring a Go stream against the C
// stream. Every oracle test in this package goes through these, so a Go
// config and its C mirror are never two separately maintained literals, and
// every subprocess is bounded. oracletest.FFmpegBin is the entry point that
// finds the binary or skips.

// cfgMirroredFields is enc.Config's field count, every one of which cEncode
// and cToolArgs must account for: carried to an ffmpeg option, or rejected
// outright the way StrictBitrate is. cToolArgs asserts it; see the caveat
// there for what a count can and cannot catch.
const cfgMirroredFields = 11

// cCoderName maps an internal coder kind onto the C's -aac_coder value.
func cCoderName(kind enc.CoderKind) string {
	switch kind {
	case enc.CoderNMR:
		return coderNMR
	case enc.CoderTwoLoop:
		return coderTwoLoop
	case enc.CoderFast:
		return coderFast
	default:
		panic(fmt.Sprintf("no C coder name for enc.CoderKind %d", kind))
	}
}

// cToolArgs derives the ffmpeg codec options that mirror cfg, so a Go config
// and its C reference are one literal rather than two that a transposed table
// row could silently unpair. The zero values of the switches are the C
// defaults (aac_tns 1, aac_pns 1, aac_ms -1 auto, aac_is 1, aacenc.c:1655-1658
// @ d09d5afc3a), so only a switched-off tool emits an option: all four are
// declared with the same -1..1 range and differ only in default, and 0 turns
// each off. The coder is always
// named, because the Go zero value and the C default (AAC_CODER_NMR,
// aacenc.c:1651) agree today but nothing else keeps them agreeing. A field
// with no C mirror is a test-author error, not a silent omission.
func cToolArgs(t *testing.T, cfg enc.Config) []string {
	t.Helper()
	// The doc above promises that a field with no C mirror is a test-author
	// error rather than a silent omission. This catches a field ADDED or
	// REMOVED; a rename, or a removal and an addition in one change, keeps the
	// count and still needs a reader. cEncode mirrors SampleRate, Bitrate and
	// Channels; the switches below mirror the rest; StrictBitrate fails loudly
	// just below. It fires only where an oracle test runs, since oracletest.FFmpegBin
	// skips without GOAAC_FFMPEG, so CI reaches it in the oracle job alone.
	if n := reflect.TypeFor[enc.Config]().NumField(); n != cfgMirroredFields {
		t.Fatalf("enc.Config has %d fields, cEncode and cToolArgs mirror %d: add the "+
			"ffmpeg option for the new field (or a t.Fatal for it) and bump the count",
			n, cfgMirroredFields)
	}
	if cfg.StrictBitrate {
		t.Fatal("cToolArgs: enc.Config.StrictBitrate has no ffmpeg option mirror here")
	}
	args := []string{"-aac_coder", cCoderName(cfg.Coder)}
	if cfg.NMRSpeed != 0 {
		args = append(args, "-aac_nmr_speed", fmt.Sprint(cfg.NMRSpeed))
	}
	if cfg.Cutoff != 0 {
		args = append(args, "-cutoff", fmt.Sprint(cfg.Cutoff))
	}
	if cfg.DisableTNS {
		args = append(args, "-aac_tns", "0")
	}
	if cfg.DisablePNS {
		args = append(args, "-aac_pns", "0")
	}
	if cfg.DisableMS {
		args = append(args, "-aac_ms", "0")
	}
	if cfg.DisableIS {
		args = append(args, "-aac_is", "0")
	}
	return args
}

// cEncode runs the pinned C encoder over rawPath, raw f32le as oracletest.WriteRawF32
// lays it out, at the settings that mirror cfg, and writes an ADTS stream to
// outPath. It returns nothing: a caller that needs the bytes reads the file,
// which is also what checkGateVsC scores, so size and PSNR see the same bytes.
func cEncode(t *testing.T, ffmpeg, rawPath string, cfg enc.Config, outPath string) {
	t.Helper()
	args := make([]string, 0, 32)
	args = append(args, "-v", "error", "-y", "-f", "f32le",
		"-ar", fmt.Sprint(cfg.SampleRate), "-ac", fmt.Sprint(cfg.Channels), "-i", rawPath,
		"-c:a", "aac")
	args = append(args, cToolArgs(t, cfg)...)
	args = append(args, "-b:a", fmt.Sprint(cfg.Bitrate), "-flags", "+bitexact",
		"-f", "adts", outPath)
	if out := oracletest.Run(t, ffmpeg, args...); len(out) > 0 {
		t.Fatalf("C encode wrote %d bytes to stdout", len(out))
	}
}

// rawF32Path writes src once under a per-test temp dir and returns the path.
// A gate's source is loop-invariant across its cells and only the two streams
// are per cell, so the 8.5 MB castanet pair is written once per test rather
// than once per subtest.
func rawF32Path(t *testing.T, src [][]float32) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.f32")
	oracletest.WriteRawF32(t, p, src)
	return p
}

// gateSignal is one source a gate sweeps its cells over: the planar samples
// the Go encoder and the PSNR scoring read, and the raw f32le path the C
// encoder reads, written once per test by newGateSignal.
type gateSignal struct {
	name string
	src  [][]float32
	raw  string
}

func newGateSignal(t *testing.T, name string, src [][]float32) gateSignal {
	t.Helper()
	return gateSignal{name: name, src: src, raw: rawF32Path(t, src)}
}

// gateBounds are what checkGateVsC asserts. size is the absolute stream-size
// delta in percent; mean and worst are PSNR deltas in dB, the mean over
// channels and the worst channel. A mean of math.Inf(-1) disables the mean
// rule, since no finite delta can fall below it.
type gateBounds struct {
	size  float64
	mean  float64
	worst float64
}

var (
	// phase4Bounds are the same-settings bounds (see gateCastanetSecs and
	// the constants beside it in phase4_e2e_test.go for why the reference is
	// reproducible enough to assert against). M/S and I/S trade quantization
	// error between the two channels, so per-channel PSNR is not stable under
	// a stereo-tool decision flip: the gate is the mean delta, with a
	// worst-channel backstop against a collapse the mean would average away.
	phase4Bounds = gateBounds{size: gateSizeBound, mean: gateMeanBound, worst: gateWorstBound}
	// phase3Bounds predate the mean rule: TNS is off on both sides and the
	// worst channel alone is gated, at the tighter figure. Kept as they were
	// when the phase 3 gate scored itself, so folding it into checkGateVsC
	// changed the code and not the contract. The size figure equals Phase 4's
	// today but is deliberately its own literal rather than gateSizeBound: the
	// sentence above promises these numbers stay as they were, and sharing the
	// constant would let a Phase 4 retune move Phase 3's contract silently.
	phase3Bounds = gateBounds{size: 3.0, mean: math.Inf(-1), worst: -0.5}
)

// checkGateVsC scores one Go stream against the C stream produced from the
// same source at the same settings: stream size, then decoded PSNR per
// channel. Both streams are decoded by the same pinned ffmpeg, so the decoder
// cancels out and what is left is the encoders' difference. cPath is the C
// stream on disk (where cEncode wrote it); the size check reads it and the
// PSNR check hands the same path to ffmpeg, so both score the one artifact
// cEncode wrote and no in-memory copy can drift from it.
//
// It deliberately does NOT call t.Helper(): it makes four distinct assertions
// (the empty-stream precondition, size, mean PSNR and worst-channel PSNR), and
// marking it a helper reports all of them at the single call site instead of
// at the line that actually failed.
//
//nolint:thelper // this is the gate body for one cell, not an assertion wrapper
func checkGateVsC(t *testing.T, ffmpeg string, src [][]float32, goStream []byte, cPath string, b gateBounds) {
	cStream, err := os.ReadFile(cPath)
	if err != nil {
		t.Fatal(err)
	}
	// A gate scored against an empty stream is no gate: an empty cStream makes
	// sizeDelta NaN, and math.Abs(NaN) > bound is false, so every size check
	// would pass silently. Reject either side being empty up front.
	if len(goStream) == 0 || len(cStream) == 0 {
		t.Fatalf("empty stream, cannot gate: Go %d bytes, C %d bytes (%s)",
			len(goStream), len(cStream), cPath)
	}
	sizeDelta := 100 * (float64(len(goStream)) - float64(len(cStream))) /
		float64(len(cStream))
	if math.Abs(sizeDelta) > b.size {
		t.Errorf("stream size %+.2f%% vs C, gate demands within %.0f%%",
			sizeDelta, b.size)
	}

	ch := len(src)
	worstDelta := math.Inf(1)
	meanDelta := 0.0
	decG := oracletest.DecodeStream(t, ffmpeg, goStream, ch, EncoderDelay)
	decC := oracletest.DecodeFile(t, ffmpeg, cPath, ch, EncoderDelay)
	for c := range ch {
		pg := oracletest.PSNRPrefix(src[c], decG[c], EncoderDelay)
		pc := oracletest.PSNRPrefix(src[c], decC[c], EncoderDelay)
		t.Logf("ch %d: Go %.2f dB, C %.2f dB (%+.2f), size %+.2f%%",
			c, pg, pc, pg-pc, sizeDelta)
		worstDelta = math.Min(worstDelta, pg-pc)
		meanDelta += (pg - pc) / float64(ch)
	}
	if meanDelta < b.mean {
		t.Errorf("mean PSNR %.2f dB below the C encoder's, gate allows %.1f dB",
			meanDelta, b.mean)
	}
	if worstDelta < b.worst {
		t.Errorf("worst-channel PSNR %.2f dB below the C encoder's, backstop is %.1f dB",
			worstDelta, b.worst)
	}
}

// gateCellVsC runs one same-settings cell end to end: the C encoder and the
// Go encoder over sig at cfg, then checkGateVsC with b.
//
// Not a t.Helper() either, for a different reason: it makes no assertions, but
// leaving it unmarked reports a failure at the C encode or the Go encode line
// rather than collapsing both onto the subtest's call site.
//
//nolint:thelper // this is the cell body, not an assertion wrapper
func gateCellVsC(t *testing.T, ffmpeg string, sig gateSignal, cfg enc.Config, b gateBounds) {
	dir := t.TempDir()
	cPath := filepath.Join(dir, "c.adts")
	cEncode(t, ffmpeg, sig.raw, cfg, cPath)
	goStream := encodeADTSPlanar(t, cfg, sig.src)
	checkGateVsC(t, ffmpeg, sig.src, goStream, cPath, b)
}
