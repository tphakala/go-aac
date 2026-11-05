// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package psy

// HpFilterIsSIMD is false on the noasm build, recording that this file
// provides the scalar dispatch for the high-pass filter. The root package
// asserts it against aac.SIMDEnabled (simd_kernels_test.go), so the public
// accessor cannot claim a kernel this package did not compile.
const HpFilterIsSIMD = false

// psyHpFilter is the noasm dispatch: it calls the canonical scalar filter
// directly. The default build uses the f32.PairSumScaleAdd composition
// (hpfilter_simd.go) instead, which produces byte-identical output.
func psyHpFilter(firbuf []float32, hpfsmpl *[blockSizeLong]float32) {
	psyHpFilterScalar(firbuf, hpfsmpl)
}
