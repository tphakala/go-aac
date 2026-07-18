// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"io"
	"strings"
	"testing"

	aac "github.com/tphakala/go-aac"
)

// These tests cover the Config.Coder plumbing without the pinned FFmpeg
// oracle: they compare go-aac against itself (high-level pcm path versus the
// low-level aac path), so they run in the plain "go test ./pcm/" run.
const (
	coderTestRate     = 48000
	coderTestChannels = 2
	coderTestBits     = 16

	// coderTestSamples is sized to a whole number of frames plus a short
	// final frame (3072 + 517 samples), so both the full-frame and
	// short-final-frame paths run.
	coderTestSamples = 3*aac.FrameSize + 517
)

// lowLevelADTS encodes src (per-channel float32, the exact values the pcm
// Encoder derives from the same PCM via srcFloats) through the low-level
// aac.Encoder with the given coder, wrapping each access unit in an ADTS
// header. It replicates pcm.Encoder's 1024-sample framing, short final frame
// and drain, so it is an independent oracle for the Config.Coder plumbing:
// byte-identical output proves the coder reached the codec.
func lowLevelADTS(t *testing.T, src [][]float32, coder aac.Coder) []byte {
	t.Helper()
	enc, err := aac.NewEncoder(aac.EncoderConfig{
		SampleRate: coderTestRate,
		Channels:   coderTestChannels,
		Bitrate:    128000,
		Coder:      coder,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var au []byte
	frame := make([][]float32, coderTestChannels)
	writeAU := func() {
		if len(au) == 0 {
			return
		}
		framed, herr := aac.AppendADTSHeader(nil, coderTestRate, coderTestChannels, len(au))
		if herr != nil {
			t.Fatal(herr)
		}
		out.Write(framed)
		out.Write(au)
	}
	n := len(src[0])
	for off := 0; off < n; off += aac.FrameSize {
		end := min(off+aac.FrameSize, n)
		for c := range coderTestChannels {
			frame[c] = src[c][off:end]
		}
		au, err = enc.EncodeFrame(au[:0], frame)
		if err != nil {
			t.Fatal(err)
		}
		writeAU()
	}
	for !enc.Drained() {
		au, err = enc.EncodeFrame(au[:0], nil)
		if err != nil {
			t.Fatal(err)
		}
		writeAU()
	}
	return out.Bytes()
}

// encodeOnce runs a complete NewEncoder/Write/Close on a dedicated (non-pooled)
// Encoder and returns the ADTS bytes.
func encodeOnce(t *testing.T, cfg Config, pcmBytes []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	e, err := NewEncoder(&buf, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcmBytes); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestCoderValidationRejectsUnknown proves an out-of-range coder is rejected
// on both the one-shot and the constructor paths, before any output.
func TestCoderValidationRejectsUnknown(t *testing.T) {
	cfg := Config{SampleRate: coderTestRate, BitDepth: coderTestBits, Channels: coderTestChannels, Bitrate: 128000, Coder: 99}

	err := EncodeInterleaved(io.Discard, cfg, genPCM16(coderTestSamples, coderTestChannels))
	if err == nil {
		t.Fatal("EncodeInterleaved accepted an unknown coder")
	}
	if !strings.Contains(err.Error(), "unknown coder") {
		t.Fatalf("EncodeInterleaved error %q does not name the coder", err)
	}

	if _, err := NewEncoder(io.Discard, cfg); err == nil {
		t.Fatal("NewEncoder accepted an unknown coder")
	} else if !strings.Contains(err.Error(), "unknown coder") {
		t.Fatalf("NewEncoder error %q does not name the coder", err)
	}
}

// TestCoderDefaultIsNMR proves the zero-value Coder is aac.CoderNMR: a Config
// with Coder unset must produce byte-identical output to one that sets NMR
// explicitly. This guards the bit-exactness constraint that existing output is
// unchanged.
func TestCoderDefaultIsNMR(t *testing.T) {
	pcmBytes := genPCM16(coderTestSamples, coderTestChannels)
	base := Config{SampleRate: coderTestRate, BitDepth: coderTestBits, Channels: coderTestChannels, Bitrate: 128000}
	explicit := base
	explicit.Coder = aac.CoderNMR

	if !bytes.Equal(encodeOnce(t, base, pcmBytes), encodeOnce(t, explicit, pcmBytes)) {
		t.Fatal("zero-value Coder output differs from explicit CoderNMR output")
	}
}

// TestCoderPlumbingParity proves, for each of the three coders, that the
// high-level pcm.EncodeInterleaved output is byte-identical to the low-level
// aac.Encoder output driven with the same coder. A dropped or defaulted coder
// on the high-level path would diverge for TwoLoop and Fast.
func TestCoderPlumbingParity(t *testing.T) {
	pcmBytes := genPCM16(coderTestSamples, coderTestChannels)
	src := srcFloats(pcmBytes, coderTestChannels, coderTestBits)
	for _, tc := range []struct {
		name  string
		coder aac.Coder
	}{
		{"nmr", aac.CoderNMR},
		{"twoloop", aac.CoderTwoLoop},
		{"fast", aac.CoderFast},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{SampleRate: coderTestRate, BitDepth: coderTestBits, Channels: coderTestChannels, Bitrate: 128000, Coder: tc.coder}
			var high bytes.Buffer
			if err := EncodeInterleaved(&high, cfg, pcmBytes); err != nil {
				t.Fatal(err)
			}
			low := lowLevelADTS(t, src, tc.coder)
			if !bytes.Equal(high.Bytes(), low) {
				t.Fatalf("coder %s: high-level pcm output (%d B) differs from low-level oracle (%d B)", tc.name, high.Len(), len(low))
			}
		})
	}
}

// TestCoderPooledReuse proves the coder flows through a reused encoder, both on
// the explicit Reset path and through EncodeInterleaved's internal pool. If it
// did not, a reused encoder primed as NMR would keep encoding NMR when a later
// call asks for Fast, so its bytes would not match a fresh Fast encode.
func TestCoderPooledReuse(t *testing.T) {
	pcmBytes := genPCM16(coderTestSamples, coderTestChannels)
	base := Config{SampleRate: coderTestRate, BitDepth: coderTestBits, Channels: coderTestChannels, Bitrate: 128000}
	nmrCfg := base
	nmrCfg.Coder = aac.CoderNMR
	fastCfg := base
	fastCfg.Coder = aac.CoderFast

	freshFast := encodeOnce(t, fastCfg, pcmBytes)

	// Deterministic reuse: the SAME Encoder does NMR, then Fast. This exercises
	// the enc.Reset(cfg.encoderConfig()) branch the pooling path relies on.
	var reused bytes.Buffer
	e, err := NewEncoder(io.Discard, nmrCfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcmBytes); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e.Reset(&reused, fastCfg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Write(pcmBytes); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reused.Bytes(), freshFast) {
		t.Fatalf("reused-encoder Fast output (%d B) differs from fresh Fast (%d B); coder did not flow through Reset", reused.Len(), len(freshFast))
	}

	// Pooled path: NMR then Fast back to back through EncodeInterleaved's
	// internal pool must also yield the fresh Fast bytes.
	if err := EncodeInterleaved(io.Discard, nmrCfg, pcmBytes); err != nil {
		t.Fatal(err)
	}
	var pooled bytes.Buffer
	if err := EncodeInterleaved(&pooled, fastCfg, pcmBytes); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pooled.Bytes(), freshFast) {
		t.Fatalf("pooled Fast encode (%d B) differs from fresh Fast (%d B)", pooled.Len(), len(freshFast))
	}
}
