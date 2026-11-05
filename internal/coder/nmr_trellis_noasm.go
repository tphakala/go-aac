// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package coder

// NMRTrellisIsSIMD is false on the noasm build, recording that this file
// provides the scalar dispatch for the trellis step and the node cost. The root package asserts it against
// aac.SIMDEnabled (simd_kernels_test.go), so the public accessor cannot claim a
// kernel this package did not compile.
const NMRTrellisIsSIMD = false

// nmrTrellisStep is the noasm dispatch: it calls the canonical scalar kernel
// directly. The default build uses the f32.MinIdxOfSumRows-backed
// implementation (nmr_trellis_simd.go) instead, which produces byte-identical
// output. The scratch argument is unused on this path.
func nmrTrellisStep(dp []float32, bp []uint8, dpp, node, lamsf []float32,
	nCur, nPrev, base, step, mdiff int, _ *nmrTrellisScratch) {
	nmrTrellisStepScalar(dp, bp, dpp, node, lamsf, nCur, nPrev, base, step, mdiff)
}

// nmrNodeCost is the noasm dispatch of the node cost: the canonical scalar
// loop. The default build uses f32.Int32ToFloat32ScaleAdd (nmr_trellis_simd.go).
func nmrNodeCost(dst []float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, lam float32) {
	nmrNodeCostScalar(dst, nd, nb, lam)
}
