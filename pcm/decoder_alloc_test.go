// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPCMDecodePooledAllocs proves a pooled public decoder decodes with zero
// per-frame allocation after warm-up. It warms one Decoder, re-arms it on a
// fresh source with Reset (the reset-in-place path that reuses the internal
// decoder, buffers and the bufio reader), then measures heap mallocs across the
// whole Read-loop drain of the second stream. The malloc count must be a small
// constant, never scaling with the frame count.
func TestPCMDecodePooledAllocs(t *testing.T) {
	for _, name := range []string{streamMono, streamStereo, "hs_s96_192k"} {
		data, err := os.ReadFile(filepath.Join(decoderTestdata, name+".adts"))
		if err != nil {
			t.Skipf("%s: %v", name, err)
		}

		// Warm-up drain grows d.frame and d.buf to steady-state capacity.
		d, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("NewDecoder: %v", err)
		}
		ch := d.Info().Channels
		if _, err := io.Copy(io.Discard, d); err != nil {
			t.Fatalf("warm drain: %v", err)
		}

		// Re-arm and drain repeatedly on a fresh source: the reset-in-place
		// path. Measure mallocs per drain and keep the minimum, so a transient
		// runtime allocation on one pass cannot flake the gate; the steady
		// state reproduces its true floor.
		buf := make([]byte, 8192)
		minMallocs := ^uint64(0)
		var frames int64
		for range 4 {
			if err := d.Reset(bytes.NewReader(data)); err != nil {
				t.Fatalf("Reset: %v", err)
			}
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			var total int64
			for {
				n, rerr := d.Read(buf)
				total += int64(n)
				if errors.Is(rerr, io.EOF) {
					break
				}
				if rerr != nil {
					t.Fatalf("Read: %v", rerr)
				}
			}
			runtime.ReadMemStats(&m1)
			if m := m1.Mallocs - m0.Mallocs; m < minMallocs {
				minMallocs = m
			}
			frames = total / int64(1024*ch*2)
		}
		t.Logf("%-16s ch=%d %d frames: %d mallocs (min of 4 passes) in pooled Read loop", name, ch, frames, minMallocs)
		// The reset-in-place decode must not allocate per frame. Allow a tiny
		// constant for runtime bookkeeping; fail if it scales with frames.
		if minMallocs > 4 {
			t.Errorf("%s: %d mallocs across %d frames (want a small constant, not per-frame)", name, minMallocs, frames)
		}
	}
}
