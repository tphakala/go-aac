// SPDX-License-Identifier: LGPL-2.1-or-later

package tx

import "math"

// IMDCT is the int32 half-length inverse MDCT, the port of the
// mdct_inv_int32_c codelet (ff_tx_mdct_inv + ff_tx_mdct_init,
// libavutil/tx_template.c:1223-1342 @ d09d5afc3a) over an fftN_ns_int32_c
// subtransform. n is the coefficient count (the av_tx_init len): the AAC
// decoder uses n=1024, scale 1.0/1024*128 and n=128, scale 1.0/128*128
// (init_dsp, libavcodec/aac/aacdec.c:1263-1296).
type IMDCT struct {
	n   int         // s->len
	m   []int       // s->map: gather revtab, entries pre-doubled (<<1)
	exp []complex32 // s->exp: [0:n/2] pre-shuffled copy, [n/2:n] ordered
	z   []complex32 // FFT work buffer (n/2 points)
}

// NewIMDCT builds the inverse-MDCT context. n must be a power of two with
// n/2 between 32 and 512 (the AAC decoder needs 128 and 1024). scale is the
// float the C caller hands av_tx_init (SCALE_TYPE is float for TX_INT32).
func NewIMDCT(n int, scale float32) *IMDCT {
	half := n / 2

	// Subtransform map: fftN_ns codelet init -> ff_tx_gen_ptwo_revtab with
	// FF_TX_MAP_GATHER; ff_tx_mdct_init copies it (FF_TX_PRESHUFFLE path,
	// libavutil/tx_template.c:1254-1256).
	m := ptwoRevtabGather(half, 1)

	// Twiddles: ff_tx_mdct_gen_exp (libavutil/tx_template.c:2107-2134).
	// The function's len4 is n/2 here; pre_tab is set for inverse
	// transforms, doubling the table: ordered copy at [len4:], shuffled
	// copy at [:len4].
	scaleD := float64(scale) // s->scale_d = *(SCALE_TYPE*)scale, widened
	theta := 1.0 / 8.0
	if scaleD < 0 {
		theta += float64(half)
	}
	s := math.Sqrt(math.Abs(scaleD))
	exp := make([]complex32, 2*half)
	for i := range half {
		alpha := math.Pi / 2 * (float64(i) + theta) / float64(half)
		exp[half+i] = complex32{
			rescale(math.Cos(alpha) * s),
			rescale(math.Sin(alpha) * s),
		}
	}
	for i := range half {
		exp[i] = exp[half+m[i]]
	}

	// "Saves a multiply in a hot path." (libavutil/tx_template.c:1266-1268)
	for i := range half {
		m[i] <<= 1
	}

	return &IMDCT{n: n, m: m, exp: exp, z: make([]complex32, half)}
}

// Tables returns copies of the permutation map (entries pre-doubled, as
// stored) and the twiddle table split into re/im planes. Differential-test
// hook: the C harness dumps the live AVTXContext map/exp for comparison.
func (t *IMDCT) Tables() (m []int, expRe, expIm []int32) {
	m = make([]int, len(t.m))
	copy(m, t.m)
	expRe = make([]int32, len(t.exp))
	expIm = make([]int32, len(t.exp))
	for i, e := range t.exp {
		expRe[i] = e.re
		expIm[i] = e.im
	}
	return m, expRe, expIm
}

// Transform runs the inverse MDCT: n spectral coefficients in src to n
// output samples in dst (the half-length output convention; the missing
// halves are the even/odd symmetric extensions the caller reconstructs
// during windowing). Mirrors ff_tx_mdct_inv (libavutil/tx_template.c:
// 1312-1342 @ d09d5afc3a) with stride == sizeof(int32), dst != src.
func (t *IMDCT) Transform(dst, src []int32) {
	len2 := t.n >> 1
	len4 := t.n >> 2
	src = src[:t.n]
	z := t.z[:len2]

	// Pre-rotation folded with the input gather: the map entries are
	// pre-doubled, in2[-k] indexes from the end.
	last := t.n - 1
	for i := range len2 {
		k := t.m[i]
		tre := src[last-k]
		tim := src[k]
		z[i].re, z[i].im = cmul(tre, tim, t.exp[i].re, t.exp[i].im)
	}

	fftPow2(z, len2)

	// Post-rotation against the ordered twiddle copy.
	e := t.exp[len2:]
	for i := range len4 {
		i0 := len4 + i
		i1 := len4 - i - 1
		src1re, src1im := z[i1].im, z[i1].re
		src0re, src0im := z[i0].im, z[i0].re
		r1, im0 := cmul(src1re, src1im, e[i1].im, e[i1].re)
		r0, im1 := cmul(src0re, src0im, e[i0].im, e[i0].re)
		z[i1].re = r1
		z[i0].im = im0
		z[i0].re = r0
		z[i1].im = im1
	}

	// The C writes the complex work array straight into the caller's
	// int32 buffer (dst == z); copy out in {re, im} memory order.
	dst = dst[:t.n]
	for i := range len2 {
		dst[2*i] = z[i].re
		dst[2*i+1] = z[i].im
	}
}
