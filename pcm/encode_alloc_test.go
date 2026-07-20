// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"errors"
	"io"
	"testing"
)

// TestEncodeSteadyStateAllocs is the public-API allocation gate required
// by docs/go-design.md: steady-state encoding through pcm.Encoder.Write
// must not allocate. testing.AllocsPerRun warms up once before counting,
// and an extra manual warm-up lets the carry and output buffers reach
// their final capacity first.
func TestEncodeSteadyStateAllocs(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		chunk int // deliberately not frame-aligned in some cases
	}{
		{"16bit_mono_frame_aligned", Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}, 2048},
		{"24bit_stereo_32KiB", Config{SampleRate: 48000, BitDepth: 24, Channels: 2, Bitrate: 128000}, 32768},
		{"32bit_stereo_odd", Config{SampleRate: 44100, BitDepth: 32, Channels: 2, Bitrate: 128000}, 7919},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewEncoder(io.Discard, tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			pcm16 := genPCM16(8192, tc.cfg.Channels)
			pcm := pcm16
			if tc.cfg.BitDepth != 16 {
				pcm = widen16(pcm16, tc.cfg.BitDepth)
			}
			chunk := pcm[:tc.chunk]
			for range 8 { // warm up all growth paths
				if _, err := enc.Write(chunk); err != nil {
					t.Fatal(err)
				}
			}
			allocs := testing.AllocsPerRun(50, func() {
				if _, err := enc.Write(chunk); err != nil {
					t.Fatal(err)
				}
			})
			t.Logf("%s: %.2f allocs/Write", tc.name, allocs)
			if allocs > 0 {
				t.Errorf("steady-state Write allocates %.2f/op, want 0", allocs)
			}
		})
	}
}

// TestEncodeInterleavedReuseAllocs guards the sync.Pool path: repeated
// same-shape one-shot encodes must reuse the pooled encoder's large state
// (about 650 KiB) and now the psychoacoustic context too (issue #41). A warmed
// pool is allocation-free in a normal build; the bound stays generous because
// `go test -race` instruments sync.Pool and inflates the count to a handful.
// The exact zero-allocation gate for the psy reuse lives in the pool-free
// internal/psy TestResetNoAllocSameChannels, which stays at 0 even under -race.
func TestEncodeInterleavedReuseAllocs(t *testing.T) {
	cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}
	pcm := genPCM16(4096+100, 2)
	// Prime the pool.
	if err := EncodeInterleaved(io.Discard, cfg, pcm); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(50, func() {
		if err := EncodeInterleaved(io.Discard, cfg, pcm); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("EncodeInterleaved same-shape reuse: %.2f allocs/op", allocs)
	// encodeReuseMaxAllocs is build-tag dependent (raceflag_*_test.go): exactly 0
	// in a normal build (warmed pool + reused psy context allocate nothing),
	// relaxed under -race where the detector instruments sync.Pool. Either way a
	// dropped pool re-allocates the ~650 KiB workspace and costs hundreds of allocs.
	if allocs > encodeReuseMaxAllocs {
		t.Errorf("EncodeInterleaved allocates %.0f/op (want <= %d); pool or psy reuse regressed", allocs, encodeReuseMaxAllocs)
	}
}

// TestEncodeInterleavedConcurrent proves the documented concurrency
// contract: concurrent EncodeInterleaved calls (racing over the pool) must
// each produce exactly the stream a serial call produces. Run with -race.
func TestEncodeInterleavedConcurrent(t *testing.T) {
	type job struct {
		cfg Config
		pcm []byte
		ref []byte
	}
	jobs := make([]job, 4)
	for i := range jobs {
		cfg := Config{SampleRate: 48000, BitDepth: 16, Channels: 1 + i%2, Bitrate: 96000 + 16000*i}
		pcm := genPCM16(20000+i*1111, cfg.Channels)
		var refBuf sliceWriter
		if err := EncodeInterleaved(&refBuf, cfg, pcm); err != nil {
			t.Fatal(err)
		}
		jobs[i] = job{cfg: cfg, pcm: pcm, ref: refBuf.b}
	}
	const goroutines = 8
	const iters = 10
	errc := make(chan error, goroutines)
	for g := range goroutines {
		go func() {
			for it := range iters {
				j := jobs[(g+it)%len(jobs)]
				var out sliceWriter
				if err := EncodeInterleaved(&out, j.cfg, j.pcm); err != nil {
					errc <- err
					return
				}
				if !equalBytes(out.b, j.ref) {
					errc <- errMismatch
					return
				}
			}
			errc <- nil
		}()
	}
	for range goroutines {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
}

type sliceWriter struct{ b []byte }

func (s *sliceWriter) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

var errMismatch = errors.New("concurrent one-shot produced a stream different from the serial reference")

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
