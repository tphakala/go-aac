// SPDX-License-Identifier: LGPL-2.1-or-later

// Package dsp provides the scalar DSP kernels of the AAC encoder. The exported
// AbsPow34 is a per-build dispatch point: the default build calls the canonical
// absPow34Scalar defined here, while the goaac_simd build swaps in an f32-backed
// version producing byte-identical output. The scalar stays canonical.
package dsp

import "github.com/tphakala/go-aac/internal/fmath"

// Length contract: every kernel here is destination-governed. The destination
// slice (dst or out) fixes the element count, matching Go's copy convention and
// the three-of-four majority these kernels already followed. Each source must
// be at least as long as the destination. A shorter source is a caller bug, so
// every kernel checks its sources up front and panics rather than reading past
// their length. Without that check the re-slice below validates capacity, not
// length, so a short source with spare capacity would be silently extended and
// the loop would read stale data past the source's end.
//
// After the check the kernels re-slice their sources to the destination length.
// That re-slice is load-bearing, not cosmetic: it is the form the compiler can
// prove, so the per-element bounds checks leave the loop body. Verified on
// go1.25 arm64 with -gcflags=-d=ssa/check_bce/debug=1: VectorFMul carried two
// bounds checks per element and absPow34Scalar one; after the re-slice the loops carry
// none and only a single check per call remains. Measured (min of 10, n=1024):
// AbsPow34 346.8 -> 242.8 ns/op, VectorFMul 269.1 -> 240.9 ns/op. Hoisting a
// bare `_ = src0[len(dst)-1]` instead does not work: it removes none of the
// in-loop checks, adds one of its own, and panics wrongly when the destination
// is empty.

// VectorFMul computes dst[i] = src0[i] * src1[i].
// Mirrors AVFloatDSPContext.vector_fmul (libavutil/float_dsp.c @ d09d5afc3a).
func VectorFMul(dst, src0, src1 []float32) {
	if len(src0) < len(dst) || len(src1) < len(dst) {
		panic("dsp: VectorFMul: source shorter than dst")
	}
	src0 = src0[:len(dst)]
	src1 = src1[:len(dst)]
	for i := range dst {
		dst[i] = src0[i] * src1[i]
	}
}

// VectorFMulReverse computes dst[i] = src0[i] * src1[len-1-i].
// Mirrors AVFloatDSPContext.vector_fmul_reverse.
func VectorFMulReverse(dst, src0, src1 []float32) {
	if len(src0) < len(dst) || len(src1) < len(dst) {
		panic("dsp: VectorFMulReverse: source shorter than dst")
	}
	n := len(dst)
	src0 = src0[:n]
	src1 = src1[:n]
	for i := range dst {
		dst[i] = src0[i] * src1[n-1-i]
	}
}

// absPow34Scalar computes out[i] = |in[i]|^(3/4) via nested square roots.
// Mirrors libavcodec/aacencdsp.c:abs_pow34_v @ d09d5afc3a. This is the canonical
// scalar kernel; the exported AbsPow34 is a per-build dispatch (abspow34_noasm.go
// and abspow34_simd.go), and abspow34_simd_equiv_test.go gates the tagged build
// bitwise against it.
func absPow34Scalar(out, in []float32) {
	if len(in) < len(out) {
		panic("dsp: AbsPow34: source shorter than out")
	}
	in = in[:len(out)]
	for i := range out {
		a := in[i]
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
//
// The clamp is min(), not a transliteration of the C's FFMIN (aacencdsp.c:41),
// which expands to "a > b ? b : a" (macros.h:49). They are not equivalent in
// general: min() quiets a signaling NaN, and Go makes min(x, NaN) the NaN and
// min(+0.0, -0.0) the -0.0, where FFMIN keeps x, x and +0.0. They agree here
// on two properties this call site guarantees: v is always the output of
// "+ rounding", so IEEE arithmetic has already quieted any signaling NaN, and
// m converts from an int (tables.CBMaxval, 0 to 16), so it is never NaN and
// never -0.0. TestQuantizeBandsMatchesFFMIN pins both, and the FMA guard above
// with them. Do not copy this to a kernel whose bound can be NaN, or whose
// input reaches the clamp unquieted.
//
// Only out is equivalent, not v: on amd64 min() lowers to MINSS/MINSS/POR, and
// at maxval 7 the POR leaves a NaN payload arm64's FMINS does not. int32()
// collapses both, which hides it, so do not lift v out of this function.
//
// min() also drops the clamp's branch, which is data dependent because it fires
// only on the coefficients that saturate: worth 5.5% on Cortex-A76 and 6.7% on
// x86_64 over a full encode. Hoisting float32(maxval) mirrors clang; hoisting
// it while keeping the branch measured slower than either form, so the two
// belong together.
func QuantizeBands(out []int32, in, scaled []float32, isSigned bool, maxval int, q34, rounding float32) {
	if len(in) < len(out) || len(scaled) < len(out) {
		panic("dsp: QuantizeBands: source shorter than out")
	}
	in = in[:len(out)]
	scaled = scaled[:len(out)]
	m := float32(maxval)
	for i := range out {
		v := min(float32(scaled[i]*q34)+rounding, m)
		tmp := int32(v)
		// Nested rather than "isSigned && in[i] < 0": the && form's merge block
		// has three predecessors, and branchelim only rewrites two-predecessor
		// diamonds, so the inner test stays a branch. Split, it if-converts to
		// CSNEG on arm64 and MOVL/NEGL/CMOVL on amd64, which spills a register
		// to do it and still comes out ahead. This works only because tmp is an
		// integer: branchelim builds a CondSelect for integer and pointer types
		// alone, so the same shape over a float32, as at quantize.go:199, gains
		// nothing from nesting.
		//
		// It is the loop's only data-dependent branch, but it is not a coin
		// flip: the rate loop replays these same coefficients once per
		// scalefactor candidate, so the sign sequence is mostly predicted and
		// only the residual is on the table. Worth about 2% of a full encode on
		// x86_64 and under 1% on Cortex-A76, on go1.26.5; hardware and method
		// are in #14. TestQuantizeBandsMatchesFFMIN pins the equivalence, its
		// reference deliberately keeping the && form.
		if isSigned {
			if in[i] < 0 {
				tmp = -tmp
			}
		}
		out[i] = tmp
	}
}
