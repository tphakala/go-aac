// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package coder

// NMRTrellisIsSIMD is false on the noasm build, recording that this file
// provides the scalar dispatch. The root package asserts it against
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

// nmrTrellisPlan is a no-op on the noasm build: the scalar kernel derives its
// offset bounds inside each step.
func nmrTrellisPlan(_ *nmrTrellisScratch, _, _, _, _, _, _ int) {}

// nmrTrellisStepPlanned is the noasm dispatch of a planned step: the
// canonical scalar kernel.
func nmrTrellisStepPlanned(dp []float32, bp []uint8, dpp, node, lamsf []float32,
	nCur, nPrev, base, step, mdiff int, _ *nmrTrellisScratch, _ int) {
	nmrTrellisStepScalar(dp, bp, dpp, node, lamsf, nCur, nPrev, base, step, mdiff)
}

// nmrNodeCost is the noasm dispatch of the node-cost kernel: the scalar
// reference.
func nmrNodeCost(dst []float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, lam float32) {
	nmrNodeCostGo(dst, nd, nb, lam)
}
