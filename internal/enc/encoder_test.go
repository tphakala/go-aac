// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"math"
	"testing"
)

func testFrame() []float32 {
	f := make([]float32, 1024)
	for i := range f {
		f[i] = float32(0.5 * math.Sin(2*math.Pi*997*float64(i)/44100))
	}
	return f
}

func TestPrimingAndDrain(t *testing.T) {
	e, err := New(Config{SampleRate: 44100, Bitrate: 128000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	frame := testFrame()
	out, err := e.EncodeFrame(nil, [][]float32{frame})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("first frame produced %d bytes, want 0 (priming)", len(out))
	}
	// Second call emits the first packet.
	out, err = e.EncodeFrame(nil, [][]float32{frame})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("second frame produced no packet")
	}
	// N=2048 queued + 1024 priming: expect exactly 2 more flush packets.
	packets := 1
	for !e.Drained() {
		out, err = e.EncodeFrame(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 0 {
			packets++
		}
	}
	if packets != 3 {
		t.Errorf("total packets %d, want 3 (N/1024 + 1)", packets)
	}
	if out, _ = e.EncodeFrame(nil, nil); len(out) != 0 {
		t.Error("drained encoder still produces packets")
	}
}

func TestNaNInputRejected(t *testing.T) {
	e, err := New(Config{SampleRate: 44100, Bitrate: 128000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.EncodeFrame(nil, [][]float32{testFrame()}); err != nil { // clean priming frame
		t.Fatalf("priming frame should not encode: %v", err)
	}
	// The ingest guard rejects non-finite input on the carrying frame, so the
	// NaN surfaces here rather than one frame later at the post-MDCT guard.
	frame := testFrame()
	frame[100] = float32(math.NaN())
	if _, err = e.EncodeFrame(nil, [][]float32{frame}); err == nil {
		t.Fatal("NaN input not rejected")
	}
}

// TestStrictBitrateCap drives the strict-CBR path (bit_rate_tolerance == 0,
// aacenc.c:1297-1306 @ d09d5afc3a): every frame must fit rate_bits before
// TYPE_END, so every AU payload is at most rate_bits+3 bits rounded up to a
// byte. Noisy full-band input at a low rate forces the lambda shrink loop.
func TestStrictBitrateCap(t *testing.T) {
	const bitrate = 48000
	e, err := New(Config{SampleRate: 44100, Bitrate: bitrate, Channels: 1, StrictBitrate: true})
	if err != nil {
		t.Fatal(err)
	}
	rateBits := bitrate * 1024 / 44100
	maxAU := (rateBits + 3 + 7) / 8
	frame := make([]float32, 1024)
	state := uint32(0xdeadbeef)
	var au []byte
	for n := range 20 {
		for i := range frame {
			state = state*1664525 + 1013904223
			frame[i] = 0.5 * float32(int32(state)) / 2147483648.0
		}
		au, err = e.EncodeFrame(au[:0], [][]float32{frame})
		if err != nil {
			t.Fatal(err)
		}
		if len(au) > maxAU {
			t.Errorf("frame %d: AU %d bytes exceeds strict cap %d", n, len(au), maxAU)
		}
	}
}

func TestEncodeSteadyStateAllocs(t *testing.T) {
	e, err := New(Config{SampleRate: 44100, Bitrate: 128000, Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	frame := testFrame()
	dst := make([]byte, 0, 4096)
	for range 8 { // warm up past priming and lambda settling
		if dst, err = e.EncodeFrame(dst[:0], [][]float32{frame}); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		var errIn error
		dst, errIn = e.EncodeFrame(dst[:0], [][]float32{frame})
		if errIn != nil {
			t.Fatal(errIn)
		}
	})
	if allocs != 0 {
		t.Errorf("EncodeFrame allocates %.1f times per frame, want 0", allocs)
	}
}

func TestEncodeSteadyStateAllocsStereo(t *testing.T) {
	e, err := New(Config{SampleRate: 44100, Bitrate: 128000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	frame := [][]float32{testFrame(), testFrame()}
	dst := make([]byte, 0, 8192)
	for range 8 {
		if dst, err = e.EncodeFrame(dst[:0], frame); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		var errIn error
		dst, errIn = e.EncodeFrame(dst[:0], frame)
		if errIn != nil {
			t.Fatal(errIn)
		}
	})
	if allocs != 0 {
		t.Errorf("stereo EncodeFrame allocates %.1f times per frame, want 0", allocs)
	}
}
