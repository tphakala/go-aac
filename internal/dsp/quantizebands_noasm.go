// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !goaac_simd

package dsp

// QuantizeBandsIsSIMD is false on the default build, recording that this file
// provides the scalar kernel. The root package asserts it against
// aac.SIMDEnabled (simd_kernels_test.go), so the public accessor cannot claim a
// kernel this package did not compile.
const QuantizeBandsIsSIMD = false

// QuantizeBands is the default-build dispatch: it calls the canonical scalar
// kernel directly. The goaac_simd build replaces this file with an
// f32-backed implementation (quantizebands_simd.go) that produces byte-identical
// output on encoder input. The default build links no simd code. The scalar body
// owns the length panic, so this wrapper adds no check of its own.
func QuantizeBands(out []int32, in, scaled []float32, isSigned bool, maxval int, q34, rounding float32) {
	quantizeBandsScalar(out, in, scaled, isSigned, maxval, q34, rounding)
}
