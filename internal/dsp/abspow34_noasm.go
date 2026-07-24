// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !goaac_simd

package dsp

// AbsPow34IsSIMD is false on the default build, recording that this file
// provides the scalar kernel. The root package asserts it against
// aac.SIMDEnabled (simd_kernels_test.go), so the public accessor cannot claim a
// kernel this package did not compile.
const AbsPow34IsSIMD = false

// AbsPow34 is the default-build dispatch: it calls the canonical scalar kernel
// directly. The goaac_simd build replaces this file with an f32.AbsPow34-backed
// implementation (abspow34_simd.go) that produces byte-identical output. The
// default build links no simd code. The scalar body owns the length panic, so
// this wrapper adds no check of its own.
func AbsPow34(out, in []float32) {
	absPow34Scalar(out, in)
}
