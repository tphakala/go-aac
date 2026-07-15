// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"bytes"
	"math"
	"testing"
)

func resetTestFrame(n int, phase float64) []float32 {
	out := make([]float32, n)
	for i := range n {
		out[i] = float32(0.4 * math.Sin(2*math.Pi*440*(float64(i)+phase)/48000))
	}
	return out
}

// TestResetByteIdentity is the Reset contract: encoding after Reset must
// be byte-identical to encoding with a fresh New encoder, even after the
// encoder was dirtied with a different shape and coder.
func TestResetByteIdentity(t *testing.T) {
	cfg := Config{SampleRate: 44100, Channels: 1, Bitrate: 128000, Coder: CoderTwoLoop}
	encode := func(e *Encoder) [][]byte {
		var frames [][]byte
		for i := range 20 {
			out, err := e.EncodeFrame(nil, [][]float32{resetTestFrame(1024, float64(i*1024))})
			if err != nil {
				t.Fatal(err)
			}
			frames = append(frames, out)
		}
		for !e.Drained() {
			out, err := e.EncodeFrame(nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			frames = append(frames, out)
		}
		return frames
	}
	fresh, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := encode(fresh)

	dirty, err := New(Config{SampleRate: 48000, Channels: 2, Bitrate: 96000})
	if err != nil {
		t.Fatal(err)
	}
	df := [][]float32{resetTestFrame(1024, 3), resetTestFrame(1024, 7)}
	for range 10 {
		if _, err := dirty.EncodeFrame(nil, df); err != nil {
			t.Fatal(err)
		}
	}
	if err := dirty.Reset(cfg); err != nil {
		t.Fatal(err)
	}
	got := encode(dirty)
	if len(got) != len(want) {
		t.Fatalf("frame count %d vs %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(want[i], got[i]) {
			t.Fatalf("first differing frame: %d (%d vs %d bytes)", i, len(want[i]), len(got[i]))
		}
	}
}

// TestCutoffBandwidth pins the user-cutoff branch of aacenc.c:1591-1592:
// a positive Cutoff sets the coding bandwidth verbatim, bypassing both
// the NMR rate map and the final clamp; Cutoff 0 keeps the tuned map
// (128k mono NMR: 18000 + 2000*32000/96000 = 18666).
func TestCutoffBandwidth(t *testing.T) {
	e, err := New(Config{SampleRate: 48000, Channels: 1, Bitrate: 128000, Cutoff: 12000})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Bandwidth(); got != 12000 {
		t.Fatalf("bandwidth %d, want the verbatim cutoff 12000", got)
	}
	e2, err := New(Config{SampleRate: 48000, Channels: 1, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if got := e2.Bandwidth(); got != 18666 {
		t.Fatalf("automatic bandwidth %d, want 18666 (NMR rate map)", got)
	}
}

// TestStatsCounters checks the aacenc.c:1352-1386 mirror: counters
// accumulate per emitted frame and Reset clears them.
func TestStatsCounters(t *testing.T) {
	e, err := New(Config{SampleRate: 48000, Channels: 2, Bitrate: 128000})
	if err != nil {
		t.Fatal(err)
	}
	df := [][]float32{resetTestFrame(1024, 0), resetTestFrame(1024, 11)}
	emitted := 0
	for range 12 {
		out, err := e.EncodeFrame(nil, df)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 0 {
			emitted++
		}
	}
	st := e.Stats()
	if st.LambdaCount != int64(emitted) || st.Chans != 2*int64(emitted) {
		t.Fatalf("LambdaCount %d Chans %d, want %d and %d", st.LambdaCount, st.Chans, emitted, 2*emitted)
	}
	if st.ChBands == 0 || st.LambdaSum <= 0 {
		t.Fatalf("empty band/lambda accounting: %+v", st)
	}
	if err := e.Reset(Config{SampleRate: 48000, Channels: 2, Bitrate: 128000}); err != nil {
		t.Fatal(err)
	}
	if st := e.Stats(); st != (Stats{}) {
		t.Fatalf("Reset did not clear stats: %+v", st)
	}
}
