// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"testing"
	"time"
)

// TestStrictBitrateUnachievableTerminates pins the fix for the rate-loop hang.
//
// aacenc.c:1297-1305 sits inside `do { ... } while (1)` with no lambda floor and
// no iteration cap (its `its++` is in the ABR branch, not this one), so when the
// strict target is below anything the quantizer can reach, rate_bits < frame_bits
// stays true forever and the C encoder hangs. Before the fix this test hung too:
// at 1000 bps / 44100 Hz the second EncodeFrame never returned.
//
// The port terminates with an error instead. This is a deliberate deviation from
// the C, and it is output-preserving: lambda only reaches the floor on inputs
// where the C never terminates at all, so every bitstream the C can actually
// produce is unchanged.
func TestStrictBitrateUnachievableTerminates(t *testing.T) {
	e, err := New(Config{SampleRate: 44100, Bitrate: 1000, Channels: 1, StrictBitrate: true})
	if err != nil {
		t.Fatal(err)
	}

	frame := make([]float32, 1024)
	state := uint32(0xdeadbeef)
	fill := func() {
		for i := range frame {
			state = state*1664525 + 1013904223
			frame[i] = 0.9 * float32(int32(state)) / 2147483648.0 // full-scale noise
		}
	}

	done := make(chan error, 1)
	go func() {
		for range 4 {
			fill()
			if _, err := e.EncodeFrame(nil, [][]float32{frame}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error for an unachievable strict bitrate, got nil")
		}
		t.Logf("terminated with: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("EncodeFrame hung: the strict-bitrate rate loop did not terminate")
	}
}

// A strict bitrate that IS achievable must still encode normally, i.e. the floor
// check must not fire on any input the C would have handled.
func TestStrictBitrateAchievableStillEncodes(t *testing.T) {
	e, err := New(Config{SampleRate: 44100, Bitrate: 128000, Channels: 1, StrictBitrate: true})
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, 1024)
	state := uint32(0x12345678)
	got := 0
	for range 6 {
		for i := range frame {
			state = state*1664525 + 1013904223
			frame[i] = 0.5 * float32(int32(state)) / 2147483648.0
		}
		au, err := e.EncodeFrame(nil, [][]float32{frame})
		if err != nil {
			t.Fatalf("achievable strict bitrate returned an error: %v", err)
		}
		got += len(au)
	}
	if got == 0 {
		t.Fatal("no access units emitted at an achievable strict bitrate")
	}
	t.Logf("128 kbps strict: %d bytes over 6 frames", got)
}
