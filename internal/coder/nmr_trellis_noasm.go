// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !goaac_simd

package coder

// nmrTrellisStep is the default-build dispatch: it calls the canonical scalar
// kernel directly. The goaac_simd build replaces this file with an
// f32.MinIdxOfSumRows-backed implementation (nmr_trellis_simd.go) that produces
// byte-identical output. The scratch argument is unused on this path.
func nmrTrellisStep(dp []float32, bp []uint8, dpp, node, lamsf []float32,
	nCur, nPrev, base, step, mdiff int, _ *nmrTrellisScratch) {
	nmrTrellisStepScalar(dp, bp, dpp, node, lamsf, nCur, nPrev, base, step, mdiff)
}
