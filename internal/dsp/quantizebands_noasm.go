// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package dsp

// QuantizeBandsIsSIMD is false on the noasm build, recording that this file
// provides the scalar kernel. The root package asserts it against
// aac.SIMDEnabled (simd_kernels_test.go), so the public accessor cannot claim a
// kernel this package did not compile.
const QuantizeBandsIsSIMD = false

// QuantizeBands is the noasm dispatch: it calls the canonical scalar kernel
// directly. The default build uses the f32-backed implementation
// (quantizebands_simd.go) instead, which produces byte-identical output on
// encoder input; this file compiles only under -tags noasm and links no simd
// code. The scalar body owns the length panic, so this wrapper adds no check of
// its own.
func QuantizeBands(out []int32, in, scaled []float32, isSigned bool, maxval int, q34, rounding float32) {
	quantizeBandsScalar(out, in, scaled, isSigned, maxval, q34, rounding)
}
