// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"bytes"
	"math"
	"testing"

	"github.com/tphakala/go-aac/internal/coder"
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

// nmrSwitchCfg is the stereo config the coder-switch tests reuse; only the
// coder field changes between the arms so the psy model and the retained
// buffers keep the same shape, which is the pooled reuse the fix targets.
func nmrSwitchCfg(c CoderKind) Config {
	return Config{SampleRate: 48000, Channels: 2, Bitrate: 128000, Coder: c}
}

// encodeSwitchStream encodes a fixed stereo signal and drains, returning
// every emitted packet.
func encodeSwitchStream(t *testing.T, e *Encoder) [][]byte {
	t.Helper()
	var frames [][]byte
	for i := range 16 {
		in := [][]float32{
			resetTestFrame(1024, float64(i*1024)),
			resetTestFrame(1024, float64(i*1024)+13),
		}
		out, err := e.EncodeFrame(nil, in)
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

// dirtyNMRState scribbles sentinels into every exported field of the retained
// NMR state. It is belt and braces, not the gate: the real NMR stream that runs
// first leaves a far more hostile carry-over than any sentinel (measured after
// one 16-frame stream: Lam[0] 3380.5, LamRC 0.171, RCFill 460, SideEMA -170.9,
// FramesSinceShort 15), and that is what makes the byte-identity comparison fail
// if the zeroing in Reset is dropped.
//
// Three groups of fields cannot be caught by a byte-identity test at all, by
// construction rather than by weak sentinels: Nd and Nb are per-frame scratch
// that nmrBandCurve writes before every read (bounded by bnc[b]), Counted is
// zeroed at SearchForQuantizersNMR entry, and Lam is only a warm-start hint that
// falls outside the acceptance bracket and triggers a full re-search. That is
// most of the ~99 KiB. The rate-control carry-over (SideEMA, LamRC, RCFill, and
// the short-block trio) is the part a stale value actually reaches the
// bitstream through, and it is what the comparison pins.
//
// The unexported trellis scratch is out of reach from here, which is fine while
// Reset scrubs by whole-struct assignment (that covers it) and the SIMD kernel
// writes every cell it reads within a call. Converting Reset to a field-by-field
// scrub would need its own gate inside package coder.
func dirtyNMRState(st *coder.NMRState) {
	for b := range st.Nd {
		for c := range st.Nd[b] {
			st.Nd[b][c] = 7.5
			st.Nb[b][c] = 77
		}
	}
	for ch := range st.Lam {
		st.Lam[ch] = 3.25
		st.Counted[ch] = 4242
	}
	st.SideEMA, st.SideInited = 999, true
	st.RCFrameNum, st.LamRC, st.RCFill = 123, 5.5, -4096
	st.FramesSinceShort, st.PrevWasShort, st.RunBurst = 9, true, 2.5
}

// nonNMRCoders are the coders a pooled encoder can be reset to while the NMR
// state stays retained. Retention is unconditional, so both belong in the
// round-trip test, not just the first one that came to mind.
var nonNMRCoders = []struct {
	name  string
	coder CoderKind
}{
	{"fast", CoderFast},
	{"twoloop", CoderTwoLoop},
}

// TestNMRStateRetainedAcrossCoderSwitch is the issue #45 contract. A pooled
// encoder driven NMR -> other -> NMR must keep the same ~99 KiB NMRState across
// the whole round trip (no drop, no reallocation), the stream it produces
// afterwards must be byte-identical to a fresh NMR encoder's (nothing stale
// carried over), and the middle arm must not touch the retained state at all.
func TestNMRStateRetainedAcrossCoderSwitch(t *testing.T) {
	fresh, err := New(nmrSwitchCfg(CoderNMR))
	if err != nil {
		t.Fatal(err)
	}
	want := encodeSwitchStream(t, fresh)

	for _, other := range nonNMRCoders {
		t.Run(other.name, func(t *testing.T) {
			pooled, err := New(nmrSwitchCfg(CoderNMR))
			if err != nil {
				t.Fatal(err)
			}
			retained := pooled.nmr
			if retained == nil {
				t.Fatal("New with CoderNMR left nmr nil")
			}
			encodeSwitchStream(t, pooled) // a real NMR stream dirties the state

			freshOther, err := New(nmrSwitchCfg(other.coder))
			if err != nil {
				t.Fatal(err)
			}
			wantOther := encodeSwitchStream(t, freshOther)

			if err := pooled.Reset(nmrSwitchCfg(other.coder)); err != nil {
				t.Fatal(err)
			}
			if pooled.nmr != retained {
				t.Fatalf("Reset to %s dropped the retained NMR state (%p -> %p)",
					other.name, retained, pooled.nmr)
			}
			// This middle arm is what the guard change earns its keep on: e.nmr is
			// non-nil here now, so a guard still keyed on nil-ness would let a
			// non-NMR stream read and write the previous stream's NMR carry-over.
			// Snapshot the state the side-bits accounting would write, and compare
			// the arm against a fresh encoder of the same coder in bytes and stats.
			sideEMA, sideInited := retained.SideEMA, retained.SideInited
			gotOther := encodeSwitchStream(t, pooled)
			if retained.SideEMA != sideEMA || retained.SideInited != sideInited {
				t.Errorf("the %s stream wrote the retained NMR side-bits state (SideEMA %v -> %v, inited %v -> %v): the rate accounting is still gated on nmr != nil",
					other.name, sideEMA, retained.SideEMA, sideInited, retained.SideInited)
			}
			if got, want := pooled.Stats().LambdaSum, freshOther.Stats().LambdaSum; got != want {
				t.Errorf("%s LambdaSum %v, want %v: the Qavg stat is still gated on nmr != nil, so it folded the retained NMR lambda",
					other.name, got, want)
			}
			compareStreams(t, other.name+" arm", wantOther, gotOther)
			dirtyNMRState(pooled.nmr) // nothing here may survive the switch back

			if err := pooled.Reset(nmrSwitchCfg(CoderNMR)); err != nil {
				t.Fatal(err)
			}
			if pooled.nmr != retained {
				t.Fatalf("Reset back to CoderNMR reallocated the NMR state (%p -> %p)",
					retained, pooled.nmr)
			}
			compareStreams(t, "CoderNMR arm after the round trip", want,
				encodeSwitchStream(t, pooled))
		})
	}
}

// compareStreams fails the test on the first packet that differs, which is the
// gate for "stale state did not survive". It rejects an empty expectation so a
// future change that stops emitting cannot turn the comparison vacuous.
func compareStreams(t *testing.T, what string, want, got [][]byte) {
	t.Helper()
	coded := 0
	for _, f := range want {
		coded += len(f)
	}
	if coded == 0 {
		t.Fatalf("%s: the reference stream is empty, the comparison would pass vacuously", what)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: frame count %d, want %d", what, len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(want[i], got[i]) {
			t.Fatalf("%s: frame %d differs from the fresh encoder (%d vs %d bytes)",
				what, i, len(want[i]), len(got[i]))
		}
	}
}

// TestResetNoAllocAcrossCoderSwitch pins the allocation win of issue #45: a
// pooled encoder cycling NMR -> other -> NMR must not allocate, where before the
// fix the switch back reallocated the ~99 KiB NMRState every cycle (which the
// old code reports as exactly 1.00/op). The errors are captured rather than
// fataled inside the closure: a t.Fatal there would Goexit out of the
// measurement and skip the assertion below.
func TestResetNoAllocAcrossCoderSwitch(t *testing.T) {
	for _, other := range nonNMRCoders {
		t.Run(other.name, func(t *testing.T) {
			e, err := New(nmrSwitchCfg(CoderNMR))
			if err != nil {
				t.Fatal(err)
			}
			var resetErr error
			allocs := testing.AllocsPerRun(20, func() {
				if err := e.Reset(nmrSwitchCfg(other.coder)); err != nil && resetErr == nil {
					resetErr = err
				}
				if err := e.Reset(nmrSwitchCfg(CoderNMR)); err != nil && resetErr == nil {
					resetErr = err
				}
			})
			if resetErr != nil {
				t.Fatal(resetErr)
			}
			if allocs != 0 {
				t.Errorf("NMR -> %s -> NMR reset cycle allocates %.2f/op, want 0",
					other.name, allocs)
			}
		})
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
