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
