// SPDX-License-Identifier: LGPL-2.1-or-later
package mdct

import (
	"math"
	"testing"
)

// naiveDFT computes X[k] = sum x[j] * exp(-2*pi*i*j*k/n) directly.
func naiveDFT(re, im []float64) (outRe, outIm []float64) {
	n := len(re)
	outRe, outIm = make([]float64, n), make([]float64, n)
	for k := range n {
		for j := range n {
			a := -2 * math.Pi * float64(j) * float64(k) / float64(n)
			c, s := math.Cos(a), math.Sin(a)
			outRe[k] += re[j]*c - im[j]*s
			outIm[k] += re[j]*s + im[j]*c
		}
	}
	return outRe, outIm
}

func TestFFTMatchesNaiveDFT(t *testing.T) {
	for _, n := range []int{8, 64, 512} {
		re, im := make([]float64, n), make([]float64, n)
		seed := uint32(0x1f2e3d4c)
		for i := range n {
			seed = seed*1664525 + 1013904223
			re[i] = float64(int32(seed)) / (1 << 31)
			seed = seed*1664525 + 1013904223
			im[i] = float64(int32(seed)) / (1 << 31)
		}
		wr, wi := naiveDFT(re, im)
		f := newFFT(n)
		f.transform(re, im)
		for k := range n {
			if math.Abs(re[k]-wr[k]) > 1e-9 || math.Abs(im[k]-wi[k]) > 1e-9 {
				t.Fatalf("n=%d k=%d: got (%g,%g) want (%g,%g)", n, k, re[k], im[k], wr[k], wi[k])
			}
		}
	}
}
