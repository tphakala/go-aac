// SPDX-License-Identifier: LGPL-2.1-or-later

// Package fdsp ports the int32 primitives of libavutil/fixed_dsp.c that the
// fixed-point AAC decoder uses (avpriv_alloc_fixed_dsp @ d09d5afc3a). D1
// needs vector_fmul_window; D2 adds the rest.
package fdsp

// VectorFmulWindow mirrors vector_fmul_window_c (libavutil/fixed_dsp.c:95-113
// @ d09d5afc3a). It overlap-adds two half-frames under a 2*n window:
//
//	dst[i]   = (src0[i]*win[2n-1-i] - src1[n-1-i]*win[i]   + 0x40000000) >> 31
//	dst[2n-1-i] = (src0[i]*win[i]  + src1[n-1-i]*win[2n-1-i] + 0x40000000) >> 31
//
// In every decoder call dst is a different buffer from src0 and src1 (Output,
// Saved or temp vs the MDCT scratch), so there is no dst/src aliasing here.
// The reslicing below is only to hoist the loop's bounds checks (the
// encoder-phase BCE discipline).
func VectorFmulWindow(dst, src0, src1, win []int32, n int) {
	// Re-slice to the touched extents so the compiler can lift the bounds
	// checks out of the loop (encoder-phase BCE discipline).
	dst = dst[:2*n]
	src0 = src0[:n]
	src1 = src1[:n]
	win = win[:2*n]
	for i, j := 0, n-1; i < n; i, j = i+1, j-1 {
		s0 := src0[i]
		s1 := src1[j]
		wi := win[i]
		wj := win[n+j]
		dst[i] = int32((int64(s0)*int64(wj) - int64(s1)*int64(wi) + 0x40000000) >> 31)
		dst[n+j] = int32((int64(s0)*int64(wi) + int64(s1)*int64(wj) + 0x40000000) >> 31)
	}
}

// ScalarproductFixed mirrors scalarproduct_fixed_c (libavutil/fixed_dsp.c:
// 126-137 @ d09d5afc3a): a 64-bit accumulator seeded with 0x40000000 so the
// final arithmetic >>31 rounds to nearest. The decoder uses it on the PNS
// noise fill to measure a band's energy.
func ScalarproductFixed(v1, v2 []int32) int32 {
	p := int64(0x40000000)
	// v2 bounds the loop; re-slice v1 to the same length so the loop body
	// carries no bounds check (encoder-phase BCE discipline).
	v1 = v1[:len(v2)]
	for i, b := range v2 {
		p += int64(v1[i]) * int64(b)
	}
	return int32(p >> 31)
}

// ButterfliesFixed mirrors butterflies_fixed_c (libavutil/fixed_dsp.c:139-149
// @ d09d5afc3a): v1[i], v2[i] = v1[i]+v2[i], v1[i]-v2[i]. The C casts v1
// through unsigned to make the add/sub wraparound defined; Go int32 arithmetic
// wraps identically, so no cast is needed. Applied to the dequantized spectra
// for mid/side stereo.
func ButterfliesFixed(v1, v2 []int32, n int) {
	v1 = v1[:n]
	v2 = v2[:n]
	for i := range v1 {
		t := v1[i] - v2[i]
		v1[i] += v2[i]
		v2[i] = t
	}
}
