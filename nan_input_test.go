// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

// cleanFrames builds a finite planar PCM frame of FrameSize samples per
// channel, using distinct phases so stereo channels differ.
func cleanFrames(channels int) [][]float32 {
	out := make([][]float32, channels)
	for ch := range out {
		out[ch] = synthFrame(FrameSize, float64(ch))
	}
	return out
}

// TestEncodeFrameRejectsNonFinite pins the ingest guard added for issue #18:
// int32(NaN) is 0 on arm64 and -2^31 on amd64, so a NaN in input PCM would make
// encoder output differ between architectures. Every non-finite value (NaN,
// +Inf, -Inf), at the first, middle or last sample of any channel, in mono and
// stereo configs, must be rejected with ErrInvalidAudio on the call that
// carries it, and nothing must be appended.
func TestEncodeFrameRejectsNonFinite(t *testing.T) {
	values := []struct {
		name string
		v    float32
	}{
		{"NaN", float32(math.NaN())},
		{"+Inf", float32(math.Inf(1))},
		{"-Inf", float32(math.Inf(-1))},
	}
	positions := []struct {
		name string
		idx  int
	}{
		{"first", 0},
		{"middle", FrameSize / 2},
		{"last", FrameSize - 1},
	}
	configs := []struct {
		name     string
		channels int
	}{
		{"mono", 1},
		{"stereo", 2},
	}
	for _, cfg := range configs {
		for badCh := range cfg.channels {
			for _, pos := range positions {
				for _, val := range values {
					name := fmt.Sprintf("%s/ch%d/%s/%s", cfg.name, badCh, pos.name, val.name)
					t.Run(name, func(t *testing.T) {
						e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: cfg.channels, Bitrate: 96000})
						if err != nil {
							t.Fatal(err)
						}
						// Prime with a clean frame so the bad frame lands in
						// steady state rather than the priming call.
						if _, err := e.EncodeFrame(nil, cleanFrames(cfg.channels)); err != nil {
							t.Fatalf("priming: %v", err)
						}
						bad := cleanFrames(cfg.channels)
						bad[badCh][pos.idx] = val.v
						dst := []byte{0xAA, 0xBB} // sentinel prefix to prove nothing is appended
						out, err := e.EncodeFrame(dst, bad)
						if !errors.Is(err, ErrInvalidAudio) {
							t.Fatalf("bad value at ch%d[%d]=%v: err = %v, want ErrInvalidAudio", badCh, pos.idx, val.v, err)
						}
						if len(out) != len(dst) {
							t.Fatalf("appended %d bytes on rejection, want 0", len(out)-len(dst))
						}
					})
				}
			}
		}
	}
}

// TestEncodeFrameNonFiniteInLookaheadCaughtOnCarryingFrame proves the ingest
// guard fires on the exact call that submits the bad sample, not one frame
// later. The encoder buffers one frame of lookahead, so a NaN that sits in what
// would be lookahead-only would, without an ingest check, taint the current
// frame's psy analysis one frame before the post-MDCT spectral guard fires,
// letting a NaN-influenced access unit be emitted (and its int(float)
// conversions are arch-dependent). Here the carrying call must return
// ErrInvalidAudio and append nothing.
func TestEncodeFrameNonFiniteInLookaheadCaughtOnCarryingFrame(t *testing.T) {
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	var dst []byte
	if dst, err = e.EncodeFrame(dst, cleanFrames(1)); err != nil { // priming, emits nothing
		t.Fatalf("priming: %v", err)
	}
	if dst, err = e.EncodeFrame(dst, cleanFrames(1)); err != nil { // steady state, emits an access unit
		t.Fatalf("clean steady-state frame: %v", err)
	}
	if len(dst) == 0 {
		t.Fatal("clean steady-state frame emitted no bytes")
	}
	before := len(dst)
	// Place the NaN at the last sample: it enters the lookahead region and,
	// without the ingest check, would not reach the post-MDCT guard on this
	// call. The ingest check must reject it here regardless.
	bad := cleanFrames(1)
	bad[0][FrameSize-1] = float32(math.NaN())
	out, err := e.EncodeFrame(dst, bad)
	if !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("lookahead NaN: err = %v, want ErrInvalidAudio on the carrying frame", err)
	}
	if len(out) != before {
		t.Fatalf("appended %d bytes on rejection, want 0", len(out)-before)
	}
}

// FuzzEncodeFrameNonFinite feeds random float32 bit patterns through the public
// API. Property: EncodeFrame never panics, and returns ErrInvalidAudio iff a
// submitted sample is non-finite (NaN or Inf). A single call on a fresh encoder
// isolates the ingest guard: the first call is the priming call, which returns
// before transforming any samples, so the ingest scan is the only thing that
// can produce ErrInvalidAudio and the iff property stays exact.
func FuzzEncodeFrameNonFinite(f *testing.F) {
	f.Add(uint32(0x7fc00000), 0, 0)    // quiet NaN, first sample, mono
	f.Add(uint32(0x3f000000), 500, 1)  // 0.5, mid, mono
	f.Add(uint32(0x7f800000), 1023, 3) // +Inf, last sample, stereo ch1
	f.Add(uint32(0xff800000), 200, 2)  // -Inf, stereo ch0
	f.Add(uint32(0x80000000), 10, 0)   // negative zero (finite)
	f.Add(uint32(0x7f7fffff), 42, 1)   // FLT_MAX (finite)
	f.Fuzz(func(t *testing.T, bitsIn uint32, idx, sel int) {
		channels := 1 + (sel & 1)
		v := math.Float32frombits(bitsIn)
		pos := idx % FrameSize
		if pos < 0 {
			pos += FrameSize
		}
		badCh := 0
		if channels == 2 && (sel>>1)&1 == 1 {
			badCh = 1
		}
		e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Bitrate: 96000})
		if err != nil {
			t.Fatal(err)
		}
		frame := make([][]float32, channels)
		for ch := range frame {
			frame[ch] = make([]float32, FrameSize) // finite zeros
		}
		frame[badCh][pos] = v

		_, encErr := e.EncodeFrame(nil, frame) // never panics
		nonFinite := math.IsNaN(float64(v)) || math.IsInf(float64(v), 0)
		gotInvalid := errors.Is(encErr, ErrInvalidAudio)
		if nonFinite && !gotInvalid {
			t.Fatalf("non-finite sample %v (bits %#x) at ch%d[%d]: err = %v, want ErrInvalidAudio", v, bitsIn, badCh, pos, encErr)
		}
		if !nonFinite && gotInvalid {
			t.Fatalf("finite sample %v (bits %#x) at ch%d[%d] rejected as ErrInvalidAudio", v, bitsIn, badCh, pos)
		}
	})
}
