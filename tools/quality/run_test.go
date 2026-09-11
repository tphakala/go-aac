package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/quality"
)

// TestRunGridSetupError needs no external tools: a work directory that is
// actually a regular file makes every per-case MkdirAll fail (ENOTDIR) before
// any encoding, which runGrid must surface as a setup-class error, not a
// per-case failure count.
func TestRunGridSetupError(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "iamafile")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		coders:   []aac.Coder{aac.CoderNMR},
		jobs:     2,
		seconds:  1,
		programs: []quality.Program{{Name: "z", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	rep := &report{}
	failed, err := runGrid(t.Context(), tools{}, o, notADir, rep, io.Discard)
	if err == nil {
		t.Fatal("runGrid returned nil error when the work directory is a file")
	}
	if failed != 0 || len(rep.Cases) != 0 {
		t.Fatalf("a setup error must not report cases: failed=%d cases=%d", failed, len(rep.Cases))
	}
}

// TestRunGridCancelled needs no external tools: an already-cancelled context
// makes runGrid dispatch nothing and produce no cases, which is what lets run()
// skip the report write and exit non-zero instead of overwriting a prior report
// with a truncated one.
func TestRunGridCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		coders:   []aac.Coder{aac.CoderNMR},
		jobs:     2,
		seconds:  1,
		programs: []quality.Program{{Name: "a", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	rep := &report{}
	failed, err := runGrid(ctx, tools{}, o, t.TempDir(), rep, io.Discard)
	if err != nil {
		t.Fatalf("a cancelled grid is not a setup error: %v", err)
	}
	if failed != 0 || len(rep.Cases) != 0 {
		t.Fatalf("a cancelled grid must produce no cases: failed=%d cases=%d", failed, len(rep.Cases))
	}
}

// TestRunGridEmptyIsSetupError: a run where every program is skipped (here the
// only program is pinned to a rate the run does not request) must be a setup
// error, not a silent 0-case success that writes empty reports and exits 0.
func TestRunGridEmptyIsSetupError(t *testing.T) {
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		coders:   []aac.Coder{aac.CoderNMR},
		jobs:     2,
		seconds:  1,
		programs: []quality.Program{{Name: "p48", Channels: 1, SampleRate: 48000, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	rep := &report{}
	if _, err := runGrid(t.Context(), tools{}, o, t.TempDir(), rep, io.Discard); err == nil {
		t.Fatal("an all-skipped grid must be a setup error, not a silent 0-case success")
	}
	if len(rep.Cases) != 0 {
		t.Fatalf("empty grid must produce no cases, got %d", len(rep.Cases))
	}
}

// TestAlignTrim pins that alignTrim applies the measured lag to every channel
// and trims both sides to the common length. A positive lag drops leading deg
// samples; a negative lag drops leading ref samples.
func TestAlignTrim(t *testing.T) {
	ref := [][]float64{{1, 2, 3, 4, 5}, {5, 4, 3, 2, 1}}
	// deg leads by 2 (its samples appear 2 ahead of ref), so AlignLag on the
	// noise-free integer ramp finds lag 2 and both sides shift to align.
	deg := [][]float64{{0, 0, 1, 2, 3, 4, 5}, {0, 0, 5, 4, 3, 2, 1}}
	refA, degA, lag := alignTrim(ref, deg, alignMinLag, alignMaxLag)
	if lag != 2 {
		t.Fatalf("lag = %d, want 2", lag)
	}
	for c := range refA {
		if len(refA[c]) != len(degA[c]) {
			t.Fatalf("channel %d: refA %d, degA %d, want equal", c, len(refA[c]), len(degA[c]))
		}
		for i := range refA[c] {
			if refA[c][i] != degA[c][i] {
				t.Fatalf("channel %d sample %d: ref %v deg %v, want aligned-equal", c, i, refA[c][i], degA[c][i])
			}
		}
	}
}

// TestEncodeDecodeRoundTrip runs the pure go-aac path with no external tools:
// encode a program through pcm.EncodeInterleaved at each coder, decode it back
// with go-aac's own pcm decoder, align, and confirm the priming lag is exactly
// EncoderDelay and the coded SNR is high. This exercises encodeGoAAC,
// decodeStream, deinterleaveS16LE, and alignTrim end to end.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	// harmonic-vibrato aligns at the encoder delay under every coder (a
	// MultiTone-derived program can shift the correlation peak under twoloop).
	p, ok := quality.ProgramByName("harmonic-vibrato")
	if !ok {
		t.Fatal("harmonic-vibrato program missing")
	}
	ref := genRef(p, 44100, 2)
	for _, coder := range []aac.Coder{aac.CoderNMR, aac.CoderTwoLoop, aac.CoderFast} {
		stream, err := encodeGoAAC(ref, 44100, 128, coder)
		if err != nil {
			t.Fatalf("%s: encode: %v", coderName(coder), err)
		}
		deg, err := decodeStream(stream)
		if err != nil {
			t.Fatalf("%s: decode: %v", coderName(coder), err)
		}
		if len(deg) != 1 {
			t.Fatalf("%s: decoded %d channels, want 1", coderName(coder), len(deg))
		}
		refA, degA, lag := alignTrim(ref, deg, alignMinLag, alignMaxLag)
		if lag != aac.EncoderDelay {
			t.Fatalf("%s: lag = %d, want EncoderDelay %d", coderName(coder), lag, aac.EncoderDelay)
		}
		if snr := quality.SNR(refA[0], degA[0]); snr < 20 {
			t.Fatalf("%s: round-trip SNR = %.1f dB, want a clean tone above 20 dB", coderName(coder), snr)
		}
	}
}

// TestGoAACCodersDiffer pins that the coder actually reaches the encoder: the
// three coders must produce byte-different streams for the same input, so a
// dropped Coder field (falling back to the zero-value nmr for all three) is
// caught. Pure go-aac, no external tool.
func TestGoAACCodersDiffer(t *testing.T) {
	p, ok := quality.ProgramByName(progMultitone)
	if !ok {
		t.Fatal("multitone program missing")
	}
	ref := genRef(p, 44100, 2)
	streams := map[string][]byte{}
	for _, coder := range []aac.Coder{aac.CoderNMR, aac.CoderTwoLoop, aac.CoderFast} {
		s, err := encodeGoAAC(ref, 44100, 96, coder)
		if err != nil {
			t.Fatalf("%s: %v", coderName(coder), err)
		}
		streams[coderName(coder)] = s
	}
	if bytes.Equal(streams["nmr"], streams["twoloop"]) {
		t.Fatal("nmr and twoloop produced identical bytes: the coder is not reaching the encoder")
	}
	if bytes.Equal(streams["nmr"], streams["fast"]) {
		t.Fatal("nmr and fast produced identical bytes: the coder is not reaching the encoder")
	}
	if bytes.Equal(streams["twoloop"], streams["fast"]) {
		t.Fatal("twoloop and fast produced identical bytes: the coder is not reaching the encoder")
	}
}

// TestEncodeInputParity is the parity proof that both encoders see the same
// input: for a 16-bit-quantized reference, the S16 bytes go-aac consumes decode
// back to exactly the reference, and the float32 values ffmpeg reads equal it
// exactly too. Both representations are therefore the identical signal.
func TestEncodeInputParity(t *testing.T) {
	p, ok := quality.ProgramByName("pink-noise")
	if !ok {
		t.Fatal("pink-noise program missing")
	}
	ref := genRef(p, 44100, 1)
	// S16 path: pack then unpack is lossless for an already-quantized signal.
	back, err := deinterleaveS16LE(quality.PackS16LE(ref), len(ref))
	if err != nil {
		t.Fatal(err)
	}
	for c := range ref {
		for i := range ref[c] {
			if back[c][i] != ref[c][i] {
				t.Fatalf("S16 round-trip ch%d[%d] = %v, want %v", c, i, back[c][i], ref[c][i])
			}
			// f32 path: the value ffmpeg reads is exact for a k/32768 sample.
			if float64(float32(ref[c][i])) != ref[c][i] {
				t.Fatalf("f32 conversion ch%d[%d] = %v is not exact for the quantized sample %v", c, i, float64(float32(ref[c][i])), ref[c][i])
			}
		}
	}
}

// TestDeinterleaveS16LERejectsRagged: a byte count that is not a whole number
// of channel frames is a layout disagreement, not a tail to silently drop.
func TestDeinterleaveS16LERejectsRagged(t *testing.T) {
	if _, err := deinterleaveS16LE([]byte{1, 2, 3}, 1); err == nil {
		t.Fatal("odd byte count for S16 must error")
	}
	if _, err := deinterleaveS16LE([]byte{1, 2, 3, 4, 5, 6}, 2); err == nil {
		t.Fatal("6 bytes is 1.5 stereo frames and must error")
	}
	out, err := deinterleaveS16LE([]byte{0x00, 0x40, 0x00, 0xC0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 0x4000 = 16384 -> 0.5; 0xC000 = -16384 -> -0.5.
	if out[0][0] != 0.5 || out[1][0] != -0.5 {
		t.Fatalf("decoded (%v, %v), want (0.5, -0.5) little-endian", out[0][0], out[1][0])
	}
}
