// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package dsp

// Vendoring boundary: github.com/tphakala/simd holds only truly generic math
// primitives. Here the magnitude quantizer maps onto f32.Float32ToInt32ScaleClamp
// and its sign-transfer sibling f32.Float32ToInt32ScaleClampSigned (the fusion of
// the generic ScaleClamp and CopySign primitives). The AAC-specific composition,
// the isSigned dispatch, and the destination-length panic that
// TestKernelLengthContract pins stay vendored in this package; nothing
// codec-specific migrates upstream.

import "github.com/tphakala/simd/f32"

// QuantizeBandsIsSIMD is true on the default build, recording that this file
// provides the SIMD kernel. The root package asserts it against aac.SIMDEnabled
// (simd_kernels_test.go), so the public accessor cannot claim a kernel this
// package did not compile.
const QuantizeBandsIsSIMD = true

// avxMinLen is the f32 primitives' AVX-activation width on amd64 (simd's
// minAVXElements). At or above it the SIMD path vectorizes and wins; below it the
// primitives run their portable Go loop reached through non-inlined cross-package
// frames plus a CPUID test, which loses to the scalar the default build inlines,
// so QuantizeBands routes sub-width bands to quantizeBandsScalar. arm64 NEON is
// 4-wide, but measured on a Cortex-A76 the SIMD path only pulls clearly ahead from
// n=16 (n=8 is within noise), so routing n<8 to the scalar there costs only the
// wrapper's non-inlined frame (about +2.6% at n=4, dwarfed by the n>=16 wins), not
// a real NEON win.
const avxMinLen = 8

// QuantizeBands is the default dispatch. It maps the pow34-scaled magnitude
// quantizer onto the generic f32 primitives:
//
//	signed:   out[i] = copysign(int32(clamp(scaled[i]*q34 + rounding, 0, maxval)), in[i])
//	unsigned: out[i] =          int32(clamp(scaled[i]*q34 + rounding, 0, maxval))
//
// which is quantizeBandsScalar's body. Bit-identity on encoder input rests on
// three facts the primitive's contract guarantees, matching the scalar exactly:
//   - The product rounds to float32 before rounding is added and is never
//     contracted into an FMA, the same as the scalar's explicit float32() cast.
//     This is the hard no-FMA constraint from the kernel doc.
//   - Conversion truncates toward zero, the same as int32(v).
//   - Out-of-range clamps rather than wraps: +Inf -> maxval matches the scalar's
//     min(+Inf, m) = m, and the int32 convert collapses both.
//
// The minV = 0 floor is inert here: scaled is AbsPow34 output, so scaled >= 0,
// and q34 > 0 with rounding >= 0, so scaled[i]*q34 + rounding is never negative
// and the floor never fires. It is passed only because the primitive requires a
// lower bound; a negative magnitude (which the encoder never produces, since
// scaled is AbsPow34 output and |x|^(3/4) is non-negative) would diverge from the
// unclamped scalar, and TestQuantizeBandsMatchesScalar keeps the sweep inside the
// non-negative domain.
//
// Signed uses copysign on bit 31 of in[i] where the scalar uses in[i] < 0. The
// two differ only for a -0.0 sign, and only when the magnitude is nonzero; but a
// -0.0 coefficient has magnitude 0, which quantizes to int32 0 where the sign is
// inert, so there is no divergence on encoder input (rounding is always < 1, so a
// zero magnitude cannot round up to a nonzero value). The equiv test pins the
// -0.0-on-zero-magnitude case for both isSigned values.
//
// Both sources are length-checked up front regardless of isSigned: the scalar
// panics on a short in even when unsigned, and the contract must not weaken per
// branch. The panic must live here because the f32 primitives silently process
// min(len(...)) instead of panicking, so without it the destination-length
// contract that TestKernelLengthContract pins would quietly weaken on the default
// build. quantizebands_simd_equiv_test.go gates the output bitwise against
// quantizeBandsScalar.
//
// Bands narrower than avxMinLen route to quantizeBandsScalar instead of the
// primitive: below the AVX-activation width the f32 kernels fall back to a
// portable Go loop reached through non-inlined cross-package frames, which loses
// to the inlined scalar, and n=4 is the most common scalefactor-band width. This
// keeps the vectorized win on the wide bands without a dispatch regression on the
// common narrow ones. The routed scalar owns its own length panic, so the
// destination-length contract holds on both paths.
func QuantizeBands(out []int32, in, scaled []float32, isSigned bool, maxval int, q34, rounding float32) {
	if len(out) < avxMinLen {
		quantizeBandsScalar(out, in, scaled, isSigned, maxval, q34, rounding)
		return
	}
	if len(in) < len(out) || len(scaled) < len(out) {
		panic("dsp: QuantizeBands: source shorter than out")
	}
	in = in[:len(out)]
	scaled = scaled[:len(out)]
	m := float32(maxval)
	if isSigned {
		f32.Float32ToInt32ScaleClampSigned(out, scaled, in, q34, rounding, 0, m)
	} else {
		f32.Float32ToInt32ScaleClamp(out, scaled, q34, rounding, 0, m)
	}
}
