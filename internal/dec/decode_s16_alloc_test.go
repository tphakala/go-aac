// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
)

// splitADTSFrames splits a raw ADTS byte stream into its constituent frames.
func splitADTSFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var frames [][]byte
	for pos := 0; pos < len(data); {
		sync := FindSync(data, pos)
		if sync < 0 {
			break
		}
		pos = sync
		h, err := ParseADTS(bits.NewReader(data[pos:]))
		if err != nil || pos+h.FrameLength > len(data) {
			break
		}
		frames = append(frames, data[pos:pos+h.FrameLength])
		pos += h.FrameLength
	}
	return frames
}

// TestAppendS16SteadyStateAllocs measures per-frame allocations of the
// steady-state decode path (single reused decoder, reused output buffer) with
// testing.AllocsPerRun, the D3 gate metric. It reports allocs per frame for a
// mono and a stereo stream after warm-up and requires exactly zero.
func TestAppendS16SteadyStateAllocs(t *testing.T) {
	cases := []string{"sine_m8_24k", "tonal_s48_128k"}
	for _, name := range cases {
		data, err := os.ReadFile(filepath.Join("testdata", name+".adts"))
		if err != nil {
			t.Skipf("%s: %v", name, err)
		}
		frames := splitADTSFrames(t, data)
		if len(frames) == 0 {
			t.Fatalf("%s: no frames", name)
		}

		d := NewADTS()
		dst := make([]byte, 0, 1<<14)
		// Warm up: configure, lazily build the IMDCT contexts, grow dst.
		for _, f := range frames {
			dst, _, err = d.AppendS16(dst[:0], f)
			if err != nil {
				t.Fatalf("%s warmup: %v", name, err)
			}
		}

		perRun := testing.AllocsPerRun(50, func() {
			for _, f := range frames {
				dst, _, _ = d.AppendS16(dst[:0], f)
			}
		})
		t.Logf("%-16s %d frames: %.1f allocs/run = %.4f allocs/frame",
			name, len(frames), perRun, perRun/float64(len(frames)))
		if perRun != 0 {
			t.Errorf("%s: steady-state path allocates %.1f times per run (want 0)", name, perRun)
		}
	}
}
