// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func synthFrame(n int, phase float64) []float32 {
	out := make([]float32, n)
	for i := range n {
		out[i] = float32(0.4 * math.Sin(2*math.Pi*440*(float64(i)+phase)/48000))
	}
	return out
}

func TestEncoderConfigValidate(t *testing.T) {
	if _, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1}); err != nil {
		t.Fatalf("minimal valid config rejected: %v", err)
	}
	bad := []EncoderConfig{
		{SampleRate: 22050, Channels: 1},
		{SampleRate: 48000, Channels: 0},
		{SampleRate: 48000, Channels: 3},
		{SampleRate: 48000, Channels: 1, Bitrate: -1},
		{SampleRate: 48000, Channels: 1, Cutoff: 24001},
		{SampleRate: 48000, Channels: 1, Coder: 99},
	}
	for _, cfg := range bad {
		if _, err := NewEncoder(cfg); err == nil {
			t.Errorf("config %+v accepted, want error", cfg)
		}
	}
}

// TestEncodeFramePrimingAndDrain pins the frame-level contract: the first
// call emits nothing (priming), every later call emits one access unit,
// and the drain loop emits the queued remainder then stops.
func TestEncodeFramePrimingAndDrain(t *testing.T) {
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	frame := synthFrame(FrameSize, 0)
	dst, err := e.EncodeFrame(nil, [][]float32{frame})
	if err != nil {
		t.Fatal(err)
	}
	if len(dst) != 0 {
		t.Fatalf("priming call emitted %d bytes, want 0", len(dst))
	}
	dst, err = e.EncodeFrame(dst, [][]float32{frame})
	if err != nil {
		t.Fatal(err)
	}
	if len(dst) == 0 {
		t.Fatal("second call emitted nothing")
	}
	drains := 0
	for !e.Drained() {
		var out []byte
		out, err = e.EncodeFrame(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 0 {
			drains++
		}
	}
	if drains == 0 {
		t.Fatal("drain emitted no access units")
	}
	// Drained: further nil calls append nothing.
	out, err := e.EncodeFrame(nil, nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("post-drain call: %d bytes, err %v", len(out), err)
	}
}

func TestEncodeFrameInvalidAudio(t *testing.T) {
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	frame := synthFrame(FrameSize, 0)
	if _, err := e.EncodeFrame(nil, [][]float32{frame}); err != nil { // priming
		t.Fatal(err)
	}
	// Samples are checked at ingest, so the NaN is rejected on the same
	// call that carries it.
	frame[100] = float32(math.NaN())
	_, got := e.EncodeFrame(nil, [][]float32{frame})
	if !errors.Is(got, ErrInvalidAudio) {
		t.Fatalf("NaN input: %v, want ErrInvalidAudio", got)
	}
}

func TestEncodeFrameArgumentErrors(t *testing.T) {
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	mono := [][]float32{synthFrame(FrameSize, 0)}
	if _, err := e.EncodeFrame(nil, mono); err == nil {
		t.Error("wrong channel count accepted")
	}
	tooLong := [][]float32{make([]float32, FrameSize+1), make([]float32, FrameSize+1)}
	if _, err := e.EncodeFrame(nil, tooLong); err == nil {
		t.Error("oversized frame accepted")
	}
	ragged := [][]float32{make([]float32, FrameSize), make([]float32, FrameSize-1)}
	if _, err := e.EncodeFrame(nil, ragged); err == nil {
		t.Error("ragged channel slices accepted")
	}
}

// TestAppendADTSHeaderPublic checks the exported framing helper against
// the package's own unexported writer (already golden-tested against
// FFmpeg's muxer bytes) and its argument validation.
func TestAppendADTSHeaderPublic(t *testing.T) {
	got, err := AppendADTSHeader(nil, 48000, 1, 300)
	if err != nil {
		t.Fatal(err)
	}
	want := appendADTSHeader(nil, 3, 1, 300)
	if !bytes.Equal(got, want) {
		t.Fatalf("public header % x, internal % x", got, want)
	}
	if _, err := AppendADTSHeader(nil, 22222, 1, 300); err == nil {
		t.Error("bad sample rate accepted")
	}
	if _, err := AppendADTSHeader(nil, 48000, 0, 300); err == nil {
		t.Error("channel config 0 accepted")
	}
	if _, err := AppendADTSHeader(nil, 48000, 1, maxADTSPayload+1); err == nil {
		t.Error("oversized payload accepted")
	}
}

// TestEncoderResetByteIdentity: the low-level Reset must be
// indistinguishable from a fresh NewEncoder.
func TestEncoderResetByteIdentity(t *testing.T) {
	cfg := EncoderConfig{SampleRate: 44100, Channels: 1, Bitrate: 128000, Coder: CoderTwoLoop}
	encode := func(e *Encoder) []byte {
		var out []byte
		var err error
		for i := range 20 {
			out, err = e.EncodeFrame(out, [][]float32{synthFrame(FrameSize, float64(i*FrameSize))})
			if err != nil {
				t.Fatal(err)
			}
		}
		for !e.Drained() {
			if out, err = e.EncodeFrame(out, nil); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	fresh, err := NewEncoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := encode(fresh)

	reused, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 2, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	// Dirty it with a different shape (stereo frames for the stereo config).
	stereo := [][]float32{synthFrame(FrameSize, 3), synthFrame(FrameSize, 7)}
	var scratch []byte
	for range 10 {
		if scratch, err = reused.EncodeFrame(scratch, stereo); err != nil {
			t.Fatal(err)
		}
	}
	for !reused.Drained() {
		if scratch, err = reused.EncodeFrame(scratch, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := reused.Reset(cfg); err != nil {
		t.Fatal(err)
	}
	if got := encode(reused); !bytes.Equal(got, want) {
		t.Fatalf("Reset encoder differs from fresh (%d vs %d bytes)", len(got), len(want))
	}
}

// TestDefaultBitrateIsTheZeroValueTarget pins the exported constant to the
// behaviour it documents: leaving Bitrate zero must encode exactly as though
// the caller had passed DefaultBitrate. A caller that pre-sizes a buffer from
// DefaultBitrate depends on this, so drift between the two has to break here.
func TestDefaultBitrateIsTheZeroValueTarget(t *testing.T) {
	encode := func(bitrate int) []byte {
		t.Helper()
		e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: bitrate})
		if err != nil {
			t.Fatal(err)
		}
		var out []byte
		for i := range 20 {
			if out, err = e.EncodeFrame(out, [][]float32{synthFrame(FrameSize, float64(i*FrameSize))}); err != nil {
				t.Fatal(err)
			}
		}
		for !e.Drained() {
			if out, err = e.EncodeFrame(out, nil); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	implicit, explicit := encode(0), encode(DefaultBitrate)
	if !bytes.Equal(implicit, explicit) {
		t.Fatalf("Bitrate 0 and Bitrate DefaultBitrate (%d) differ: %d vs %d bytes",
			DefaultBitrate, len(implicit), len(explicit))
	}
}

// TestEncodeFrameSteadyStateAllocs is the low-level allocation gate: with
// capacity in dst, EncodeFrame allocates nothing, for all three coders.
func TestEncodeFrameSteadyStateAllocs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		coder Coder
		ch    int
	}{
		{"nmr_mono", CoderNMR, 1},
		{"nmr_stereo", CoderNMR, 2},
		{"twoloop_stereo", CoderTwoLoop, 2},
		{"fast_mono", CoderFast, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: tc.ch, Bitrate: 128000, Coder: tc.coder})
			if err != nil {
				t.Fatal(err)
			}
			frames := make([][]float32, tc.ch)
			for c := range frames {
				frames[c] = synthFrame(FrameSize, float64(c)*17)
			}
			dst := make([]byte, 0, 4096)
			for range 8 {
				if dst, err = e.EncodeFrame(dst[:0], frames); err != nil {
					t.Fatal(err)
				}
			}
			allocs := testing.AllocsPerRun(50, func() {
				if dst, err = e.EncodeFrame(dst[:0], frames); err != nil {
					t.Fatal(err)
				}
			})
			t.Logf("%s: %.2f allocs/frame", tc.name, allocs)
			if allocs > 0 {
				t.Errorf("EncodeFrame allocates %.2f/op, want 0", allocs)
			}
		})
	}
}

// TestEncodeFrameRejectsAudioAfterShortFrame enforces the documented rule that
// only the final frame of a stream may be shorter than FrameSize. A short frame
// followed by more audio would otherwise inject zero-padding mid-stream.
func TestEncodeFrameRejectsAudioAfterShortFrame(t *testing.T) {
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	full := synthFrame(FrameSize, 0)
	short := synthFrame(FrameSize/2, 1)
	var dst []byte
	if dst, err = e.EncodeFrame(dst, [][]float32{full}); err != nil { // priming
		t.Fatal(err)
	}
	if dst, err = e.EncodeFrame(dst, [][]float32{short}); err != nil { // short is allowed once
		t.Fatalf("short frame rejected: %v", err)
	}
	if _, err = e.EncodeFrame(dst, [][]float32{full}); err == nil {
		t.Fatal("expected an error submitting audio after a short frame")
	}
	if _, err = e.EncodeFrame(dst, nil); err != nil { // nil flush is still allowed
		t.Fatalf("flush after short frame rejected: %v", err)
	}
}

// TestZeroValueEncoderPanicFree covers the "no panics escape the API" contract
// for a zero-value Encoder (enc is nil): the methods must error or return safe
// zero values rather than dereference nil.
func TestZeroValueEncoderPanicFree(t *testing.T) {
	var e Encoder
	if _, err := e.EncodeFrame(nil, [][]float32{synthFrame(FrameSize, 0)}); !errors.Is(err, ErrEncoderClosed) {
		t.Fatalf("zero-value EncodeFrame err = %v, want ErrEncoderClosed", err)
	}
	if !e.Drained() {
		t.Fatal("zero-value Drained = false, want true")
	}
	if asc := e.AudioSpecificConfig(); asc != nil {
		t.Fatalf("zero-value AudioSpecificConfig = % x, want nil", asc)
	}
	_ = e.Stats() // must not panic on a zero-value encoder
}
