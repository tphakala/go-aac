// SPDX-License-Identifier: LGPL-2.1-or-later
package mdct

import (
	"math"
	"testing"
)

func directMDCT(x []float32, n int, scale float64) []float64 {
	out := make([]float64, n)
	for k := range n {
		sum := 0.0
		for j := range 2 * n {
			sum += float64(x[j]) * math.Cos(math.Pi/float64(n)*(float64(j)+0.5+float64(n)/2)*(float64(k)+0.5))
		}
		out[k] = scale * sum
	}
	return out
}

func TestMDCTMatchesDirectForm(t *testing.T) {
	for _, n := range []int{128, 1024} {
		src := make([]float32, 2*n)
		seed := uint32(0x1f2e3d4c)
		for i := range src {
			seed = seed*1664525 + 1013904223
			src[i] = float32(int32(seed)) / (1 << 31)
		}
		want := directMDCT(src, n, 32768)
		dst := make([]float32, n)
		New(n, 32768).Transform(dst, src)
		peak := 0.0
		for _, v := range want {
			if math.Abs(v) > peak {
				peak = math.Abs(v)
			}
		}
		maxErr := 0.0
		for k := range n {
			if e := math.Abs(float64(dst[k])-want[k]) / peak; e > maxErr {
				maxErr = e
			}
			if math.Abs(float64(dst[k])-want[k]) > 1e-4*peak {
				t.Fatalf("n=%d k=%d: got %g want %g (peak %g)", n, k, dst[k], want[k], peak)
			}
		}
		t.Logf("n=%d: max |go-direct|/peak = %.3g", n, maxErr)
	}
}
