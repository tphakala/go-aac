// SPDX-License-Identifier: LGPL-2.1-or-later

// Package tx ports the int32 flavor of FFmpeg's libavutil/tx transform
// framework, exactly as instantiated for AV_TX_INT32_MDCT by the fixed-point
// AAC decoder: the split-radix power-of-two FFT codelets and their
// fixed-point twiddle tables. Init-time table code may use package math
// directly (docs/go-design.md); the transform path is pure integer.
//
// Every rounding step mirrors the C bit for bit. The macros of
// libavutil/tx_priv.h @ d09d5afc3a translate as:
//
//	CMUL    64-bit products, +0x40000000, arithmetic >>31 (cmul below)
//	BF      wrapping 32-bit add/sub (the C casts through unsigned;
//	        Go int32 arithmetic wraps identically)
//	RESCALE av_clip64(llrintf(x * 2147483648.0), INT32_MIN, INT32_MAX):
//	        the DOUBLE product is narrowed to FLOAT by llrintf's argument
//	        conversion, then rounded to integer ties-to-even (rescale below)
package tx

import (
	"math"
	"math/bits"
)

// complex32 mirrors AVComplexInt32 (libavutil/tx.h @ d09d5afc3a).
type complex32 struct{ re, im int32 }

// rescale mirrors the TX_INT32 RESCALE macro (libavutil/tx_priv.h:138
// @ d09d5afc3a). The double->float narrowing is load-bearing: llrintf takes
// a float, so the C converts the double product first. It also makes the
// port robust against 1-ulp libm differences in cos/sin, which the float
// rounding absorbs.
func rescale(x float64) int32 {
	f := float64(float32(x * 2147483648.0)) // llrintf's implicit conversion
	v := math.RoundToEven(f)                // llrintf, FE_TONEAREST
	if v >= math.MaxInt32 {
		return math.MaxInt32 // av_clip64 upper bound (cos(0) lands here)
	}
	if v <= math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// srTabs holds ff_tx_tab_8 .. ff_tx_tab_512 (int32 flavor), indexed by
// log2(len)-3. Mirrors the SR_TABLE init loop of libavutil/tx_template.c:65-77
// @ d09d5afc3a: len/4 rescaled cosines plus a forced trailing zero.
var srTabs [7][]int32

func init() {
	for j := range srTabs {
		n := 8 << j
		tab := make([]int32, n/4+1)
		freq := 2 * math.Pi / float64(n)
		for i := range n / 4 {
			tab[i] = rescale(math.Cos(float64(i) * freq))
		}
		tab[n/4] = 0
		srTabs[j] = tab
	}
}

// srTab returns the twiddle table for a split-radix FFT of length n
// (8 <= n <= 512, power of two).
func srTab(n int) []int32 {
	return srTabs[log2(n)-3]
}

// log2 returns the base-2 log of a power of two (mirrors the C's av_log2).
// Setup-path only: srTab uses it to pick a twiddle table, not the inner loop.
func log2(n int) int {
	return bits.Len(uint(n)) - 1
}

// cmul mirrors the TX_INT32 CMUL macro (libavutil/tx_priv.h:115-124
// @ d09d5afc3a): dre = round(bre*are - bim*aim), dim = round(bim*are +
// bre*aim), each rounded by +0x40000000 then an arithmetic shift right 31.
// The final int32 conversions truncate like the C's (int) casts.
func cmul(are, aim, bre, bim int32) (dre, dim int32) {
	accu := int64(bre)*int64(are) - int64(bim)*int64(aim)
	dre = int32((accu + 0x40000000) >> 31)
	accu = int64(bim)*int64(are) + int64(bre)*int64(aim)
	dim = int32((accu + 0x40000000) >> 31)
	return dre, dim
}

// butterflies mirrors the BUTTERFLIES macro (libavutil/tx_template.c:540-552
// @ d09d5afc3a). t1, t2, t5, t6 are the values the caller computed into the
// C macro's ambient temporaries; all arithmetic wraps.
func butterflies(z []complex32, a0, a1, a2, a3 int, t1, t2, t5, t6 int32) {
	r0, i0 := z[a0].re, z[a0].im
	r1, i1 := z[a1].re, z[a1].im
	t3 := t5 - t1
	t5 += t1
	z[a2].re = r0 - t5
	z[a0].re = r0 + t5
	z[a3].im = i1 - t3
	z[a1].im = i1 + t3
	t4 := t2 - t6
	t6 += t2
	z[a3].re = r1 - t4
	z[a1].re = r1 + t4
	z[a2].im = i0 - t6
	z[a0].im = i0 + t6
}

// transform mirrors the TRANSFORM macro (libavutil/tx_template.c:554-559
// @ d09d5afc3a).
func transform(z []complex32, a0, a1, a2, a3 int, wre, wim int32) {
	t1, t2 := cmul(z[a2].re, z[a2].im, wre, -wim)
	t5, t6 := cmul(z[a3].re, z[a3].im, wre, wim)
	butterflies(z, a0, a1, a2, a3, t1, t2, t5, t6)
}

// fft2 mirrors ff_tx_fft2_ns (libavutil/tx_template.c:631-641 @ d09d5afc3a),
// in-place (the decoder always calls the codelets with dst == src).
func fft2(z []complex32) {
	tmpRe := z[0].re - z[1].re
	d0Re := z[0].re + z[1].re
	tmpIm := z[0].im - z[1].im
	d0Im := z[0].im + z[1].im
	z[0] = complex32{d0Re, d0Im}
	z[1] = complex32{tmpRe, tmpIm}
}

// fft4 mirrors ff_tx_fft4_ns (libavutil/tx_template.c:643-658 @ d09d5afc3a).
func fft4(z []complex32) {
	s0re, s0im := z[0].re, z[0].im
	s1re, s1im := z[1].re, z[1].im
	s2re, s2im := z[2].re, z[2].im
	s3re, s3im := z[3].re, z[3].im

	t3, t1 := s0re-s1re, s0re+s1re
	t8, t6 := s3re-s2re, s3re+s2re
	z[2].re = t1 - t6
	z[0].re = t1 + t6
	t4, t2 := s0im-s1im, s0im+s1im
	t7, t5 := s2im-s3im, s2im+s3im
	z[3].im = t4 - t8
	z[1].im = t4 + t8
	z[3].re = t3 - t7
	z[1].re = t3 + t7
	z[2].im = t2 - t5
	z[0].im = t2 + t5
}

// fft8 mirrors ff_tx_fft8_ns (libavutil/tx_template.c:660-677 @ d09d5afc3a).
func fft8(z []complex32) {
	cos := srTabs[0][1] // ff_tx_tab_8[1]

	fft4(z)

	t1 := z[4].re + z[5].re
	d5re := z[4].re - z[5].re
	t2 := z[4].im + z[5].im
	d5im := z[4].im - z[5].im
	t5 := z[6].re + z[7].re
	d7re := z[6].re - z[7].re
	t6 := z[6].im + z[7].im
	d7im := z[6].im - z[7].im
	z[5] = complex32{d5re, d5im}
	z[7] = complex32{d7re, d7im}

	butterflies(z, 0, 2, 4, 6, t1, t2, t5, t6)
	transform(z, 1, 3, 5, 7, cos, cos)
}

// fft16 mirrors ff_tx_fft16_ns (libavutil/tx_template.c:679-704 @ d09d5afc3a).
func fft16(z []complex32) {
	cos := srTabs[1] // ff_tx_tab_16

	fft8(z[:8])
	fft4(z[8:12])
	fft4(z[12:16])

	t1 := z[8].re
	t2 := z[8].im
	t5 := z[12].re
	t6 := z[12].im
	butterflies(z, 0, 4, 8, 12, t1, t2, t5, t6)

	transform(z, 2, 6, 10, 14, cos[2], cos[2])
	transform(z, 1, 5, 9, 13, cos[1], cos[3])
	transform(z, 3, 7, 11, 15, cos[3], cos[1])
}

// srCombine mirrors ff_tx_fft_sr_combine (libavutil/tx_template.c:562-586
// @ d09d5afc3a). n is the C's len parameter (transform length / 8).
func srCombine(z []complex32, cos []int32, n int) {
	o1 := 2 * n
	o2 := 4 * n
	o3 := 6 * n
	// wo mirrors the C's `wim = cos + o1 - 7` exactly (ff_tx_fft_sr_combine,
	// tx_template.c:562-586 @ d09d5afc3a): the C indexes wim[7..0], all
	// non-negative, so cos[wo+7] is cos[o1]. There is no wim[-1] negative
	// indexing; do NOT change o1-7 to o1-8 (it would break the bit-exact gate).
	zo, co, wo := 0, 0, o1-7 // z, cos, wim cursors

	for i := 0; i < n; i += 4 {
		transform(z, zo+0, zo+o1+0, zo+o2+0, zo+o3+0, cos[co+0], cos[wo+7])
		transform(z, zo+2, zo+o1+2, zo+o2+2, zo+o3+2, cos[co+2], cos[wo+5])
		transform(z, zo+4, zo+o1+4, zo+o2+4, zo+o3+4, cos[co+4], cos[wo+3])
		transform(z, zo+6, zo+o1+6, zo+o2+6, zo+o3+6, cos[co+6], cos[wo+1])

		transform(z, zo+1, zo+o1+1, zo+o2+1, zo+o3+1, cos[co+1], cos[wo+6])
		transform(z, zo+3, zo+o1+3, zo+o2+3, zo+o3+3, cos[co+3], cos[wo+4])
		transform(z, zo+5, zo+o1+5, zo+o2+5, zo+o3+5, cos[co+5], cos[wo+2])
		transform(z, zo+7, zo+o1+7, zo+o2+7, zo+o3+7, cos[co+7], cos[wo+0])

		zo += 8
		co += 8
		wo -= 8
	}
}

// fftPow2 runs the in-place split-radix FFT on pre-permuted input, mirroring
// the DECL_SR_CODELET recursion (libavutil/tx_template.c:615-722
// @ d09d5afc3a): fft(n/2) on the first half, two fft(n/4) on the quarters,
// then the combine pass.
func fftPow2(z []complex32, n int) {
	switch n {
	case 2:
		fft2(z)
		return
	case 4:
		fft4(z)
		return
	case 8:
		fft8(z)
		return
	case 16:
		fft16(z)
		return
	}
	n2, n4 := n/2, n/4
	fftPow2(z[:n2], n2)
	fftPow2(z[n4*2:n4*3], n4)
	fftPow2(z[n4*3:n], n4)
	srCombine(z, srTab(n), n4>>1)
}

// splitRadixPermutation mirrors split_radix_permutation (libavutil/tx.c:125-134
// @ d09d5afc3a).
func splitRadixPermutation(i, n, inv int) int {
	n >>= 1
	if n <= 1 {
		return i & 1
	}
	if i&n == 0 {
		return splitRadixPermutation(i, n, inv) * 2
	}
	n >>= 1
	notSet := 0
	if i&n == 0 {
		notSet = 1
	}
	return splitRadixPermutation(i, n, inv)*4 + 1 - 2*(notSet^inv)
}

// ptwoRevtabGather builds the FF_TX_MAP_GATHER revtab of ff_tx_gen_ptwo_revtab
// (libavutil/tx.c:136-154 @ d09d5afc3a): map[i] = -srp(i) & (n-1). The AAC
// inverse MDCT requests the gather direction (map_dir = FF_TX_MAP_GATHER in
// ff_tx_mdct_init when inv, libavutil/tx_template.c:1231-1233).
func ptwoRevtabGather(n, inv int) []int {
	m := make([]int, n)
	for i := range m {
		m[i] = -splitRadixPermutation(i, n, inv) & (n - 1)
	}
	return m
}
