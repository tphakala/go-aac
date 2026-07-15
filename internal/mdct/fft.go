// SPDX-License-Identifier: LGPL-2.1-or-later

// Package mdct implements the forward MDCT used by the AAC encoder,
// numerically equivalent to FFmpeg's AV_TX_FLOAT_MDCT (libavutil/tx
// @ d09d5afc3a). Internals are float64 for headroom; the API is float32.
package mdct

import "math"

// fft is an iterative radix-2 forward DFT (negative exponent) for
// power-of-two sizes, with precomputed twiddles and bit-reversal table.
type fft struct {
	n   int
	rev []int
	cos []float64
	sin []float64
}

func newFFT(n int) *fft {
	f := &fft{n: n, rev: make([]int, n), cos: make([]float64, n/2), sin: make([]float64, n/2)}
	for i := range n {
		r := 0
		for b := 1; b < n; b <<= 1 {
			r <<= 1
			if i&b != 0 {
				r |= 1
			}
		}
		f.rev[i] = r
	}
	for k := range n / 2 {
		f.cos[k] = math.Cos(2 * math.Pi * float64(k) / float64(n))
		f.sin[k] = math.Sin(2 * math.Pi * float64(k) / float64(n))
	}
	return f
}

// transform computes X[k] = sum x[j] * exp(-2*pi*i*j*k/n) in place.
func (f *fft) transform(re, im []float64) {
	n := f.n
	for i := range n {
		if r := f.rev[i]; r > i {
			re[i], re[r] = re[r], re[i]
			im[i], im[r] = im[r], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for base := 0; base < n; base += size {
			for j := range half {
				k := j * step
				wr, wi := f.cos[k], -f.sin[k]
				i0, i1 := base+j, base+j+half
				tr := re[i1]*wr - im[i1]*wi
				ti := re[i1]*wi + im[i1]*wr
				re[i1], im[i1] = re[i0]-tr, im[i0]-ti
				re[i0], im[i0] = re[i0]+tr, im[i0]+ti
			}
		}
	}
}
