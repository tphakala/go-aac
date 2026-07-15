// SPDX-License-Identifier: LGPL-2.1-or-later

// Package dsp provides the scalar DSP kernels of the AAC encoder.
package dsp

import "github.com/tphakala/go-aac/internal/fmath"

// The kernels below re-slice their inputs to the loop length up front. That is
// load-bearing, not cosmetic: it is the form the compiler can prove, so the
// per-element bounds checks leave the loop body, and a caller passing a short
// slice fails at once instead of after a partial write. Verified on go1.25
// arm64 with -gcflags=-d=ssa/check_bce/debug=1: VectorFMul carried two bounds
// checks per element and AbsPow34 one; after the re-slice the loops carry none
// and only a single check per call remains. Measured (min of 10, n=1024):
// AbsPow34 346.8 -> 242.8 ns/op, VectorFMul 269.1 -> 240.9 ns/op. Hoisting a
// bare `_ = src0[len(dst)-1]` instead does not work: it removes none of the
// in-loop checks and adds one of its own.

// VectorFMul computes dst[i] = src0[i] * src1[i].
// Mirrors AVFloatDSPContext.vector_fmul (libavutil/float_dsp.c @ d09d5afc3a).
func VectorFMul(dst, src0, src1 []float32) {
	src0 = src0[:len(dst)]
	src1 = src1[:len(dst)]
	for i := range dst {
		dst[i] = src0[i] * src1[i]
	}
}

// VectorFMulReverse computes dst[i] = src0[i] * src1[len-1-i].
// Mirrors AVFloatDSPContext.vector_fmul_reverse.
func VectorFMulReverse(dst, src0, src1 []float32) {
	n := len(dst)
	src0 = src0[:n]
	src1 = src1[:n]
	for i := range dst {
		dst[i] = src0[i] * src1[n-1-i]
	}
}

// AbsPow34 computes out[i] = |in[i]|^(3/4) via nested square roots.
// Mirrors libavcodec/aacencdsp.c:abs_pow34_v @ d09d5afc3a.
func AbsPow34(out, in []float32) {
	out = out[:len(in)]
	for i, v := range in {
		a := v
		if a < 0 {
			a = -a
		}
		out[i] = fmath.Sqrt32(a * fmath.Sqrt32(a))
	}
}

// QuantizeBands quantizes pow34-scaled coefficients.
// Mirrors libavcodec/aacencdsp.c:quantize_bands @ d09d5afc3a:
// out[i] = (int)min(scaled[i]*Q34 + rounding, maxval), sign from in[i].
// The product is rounded to float32 before the rounding constant is added,
// exactly as C's two-statement form does; without the explicit conversion Go
// may fuse mul+add into one FMA on arm64 and flip values that land on the
// truncation boundary.
func QuantizeBands(out []int32, in, scaled []float32, isSigned bool, maxval int, q34, rounding float32) {
	in = in[:len(out)]
	scaled = scaled[:len(out)]
	for i := range out {
		v := float32(scaled[i]*q34) + rounding
		if m := float32(maxval); v > m {
			v = m
		}
		tmp := int32(v)
		if isSigned && in[i] < 0 {
			tmp = -tmp
		}
		out[i] = tmp
	}
}
