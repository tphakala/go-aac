// SPDX-License-Identifier: LGPL-2.1-or-later
package mdct

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func readF32(t *testing.T, path string, n int) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw) != 4*n {
		t.Fatalf("%s: %d bytes, want %d", path, len(raw), 4*n)
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

func TestMDCTGolden(t *testing.T) {
	for _, c := range []struct {
		n       int
		in, out string
	}{
		{1024, "testdata/mdct1024_in.f32", "testdata/mdct1024_out.f32"},
		{128, "testdata/mdct128_in.f32", "testdata/mdct128_out.f32"},
	} {
		src := readF32(t, c.in, 2*c.n)
		want := readF32(t, c.out, c.n)
		dst := make([]float32, c.n)
		New(c.n, 32768).Transform(dst, src)
		peak := float32(0)
		for _, v := range want {
			if a := float32(math.Abs(float64(v))); a > peak {
				peak = a
			}
		}
		maxErr := 0.0
		for k := range want {
			if e := math.Abs(float64(dst[k]-want[k])) / float64(peak); e > maxErr {
				maxErr = e
			}
			if math.Abs(float64(dst[k]-want[k])) > 1e-4*float64(peak) {
				t.Fatalf("n=%d k=%d: got %g want %g", c.n, k, dst[k], want[k])
			}
		}
		t.Logf("n=%d: max |go-c|/peak = %.3g", c.n, maxErr)
	}
}
