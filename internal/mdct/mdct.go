// SPDX-License-Identifier: LGPL-2.1-or-later
package mdct

import "math"

// MDCT computes a forward MDCT with n output coefficients from 2n inputs:
//
//	X[k] = scale * sum_{j=0}^{2n-1} x[j] * cos(pi/n * (j + 0.5 + n/2) * (k + 0.5))
//
// Mirrors libavutil/tx_template.c:ff_tx_mdct_fwd and ff_tx_mdct_gen_exp
// @ d09d5afc3a (fold 2n reals into n/2 complex points, n/2-point forward
// FFT, post-rotation; sqrt(scale) folded into the twiddles twice).
type MDCT struct {
	n            int
	fft          *fft
	expRe, expIm []float64
	zre, zim     []float64
}

// New returns an MDCT producing n coefficients from 2n input samples with
// output scale factor scale.
//
// n must be a positive power of two divisible by 4: the FFT's bit-reversal
// assumes a power-of-two length and Transform's second loop steps over n/4
// index pairs. An invalid n would not panic, it would quietly produce wrong
// coefficients, so it is rejected here. AAC uses 1024 and 128.
//
// Transform writes through the per-instance scratch buffers, so an *MDCT must
// not be shared across goroutines without external synchronization. The encoder
// gives each channel its own.
func New(n int, scale float64) *MDCT {
	if n <= 0 || n%4 != 0 || n&(n-1) != 0 {
		panic("mdct: n must be a positive power of two divisible by 4")
	}
	m := &MDCT{
		n:     n,
		fft:   newFFT(n / 2),
		expRe: make([]float64, n/2),
		expIm: make([]float64, n/2),
		zre:   make([]float64, n/2),
		zim:   make([]float64, n/2),
	}
	s := math.Sqrt(math.Abs(scale))
	for i := range n / 2 {
		a := math.Pi * (float64(i) + 0.125) / float64(n)
		m.expRe[i] = math.Cos(a) * s
		m.expIm[i] = math.Sin(a) * s
	}
	return m
}

// Transform writes n coefficients to dst from 2n samples in src.
//
// Both slices are re-sliced to their exact length first, so a caller that
// passes a short buffer fails here rather than part-way through, leaving dst
// half written.
func (m *MDCT) Transform(dst, src []float32) {
	n := m.n
	dst = dst[:n]
	src = src[:2*n]
	len2, len4, len3 := n/2, n/4, 3*(n/2)
	for i := range len2 {
		k := 2 * i
		var tre, tim float64
		if k < len2 {
			tre = float64(-src[len2+k]) + float64(src[len2-1-k])
			tim = float64(-src[len3+k]) - float64(src[len3-1-k])
		} else {
			tre = float64(-src[len2+k]) - float64(src[5*len2-1-k])
			tim = float64(src[k-len2]) - float64(src[len3-1-k])
		}
		er, ei := m.expRe[i], m.expIm[i]
		m.zim[i] = tre*er - tim*ei
		m.zre[i] = tre*ei + tim*er
	}
	m.fft.transform(m.zre, m.zim)
	for i := range len4 {
		i0, i1 := len4+i, len4-i-1
		e0r, e0i := m.expRe[i0], m.expIm[i0]
		e1r, e1i := m.expRe[i1], m.expIm[i1]
		dst[2*i1+1] = float32(m.zre[i0]*e0i - m.zim[i0]*e0r)
		dst[2*i0] = float32(m.zre[i0]*e0r + m.zim[i0]*e0i)
		dst[2*i0+1] = float32(m.zre[i1]*e1i - m.zim[i1]*e1r)
		dst[2*i1] = float32(m.zre[i1]*e1r + m.zim[i1]*e1i)
	}
}
