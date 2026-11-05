// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import "github.com/tphakala/simd/cpu"

// nmrNodeCost evaluates nmrNodeCostGo with NEON for a band of at least eight
// candidates (unless SIMD_DISABLE masks NEON), else with nmrNodeCostGo. Each
// lane is one candidate: the same int32 to float32 conversion, float32 product
// and float32 sum, so the result is bit-identical.
func nmrNodeCost(dst []float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, lam float32) {
	if cpu.ARM64.NEON && len(dst) >= 8 {
		nmrNodeCostNEON(&dst[0], nd, nb, len(dst), lam)
		return
	}
	nmrNodeCostGo(dst, nd, nb, lam)
}

// nmrTrellisReconcile evaluates nmrTrellisReconcileGo with NEON for at least
// eight rows, else with nmrTrellisReconcileGo. Each lane is one row: the same
// strict compare against MaxFloat32, float32 sum and back-pointer, so the
// result is bit-identical.
func nmrTrellisReconcile(dp []float32, bp []uint8, node, vals []float32, idxs []int32, dHi int) {
	if cpu.ARM64.NEON && len(dp) >= 8 {
		nmrTrellisReconcileNEON(&dp[0], &bp[0], &node[0], &vals[0], &idxs[0], len(dp), dHi)
		return
	}
	nmrTrellisReconcileGo(dp, bp, node, vals, idxs, dHi)
}

// nmrNodeCostNEON computes eight candidates per iteration, the last one
// overlapping the previous; n must be at least 8.
//
//go:noescape
func nmrNodeCostNEON(dst *float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, n int, lam float32)

// nmrTrellisReconcileNEON reconciles eight rows per iteration, the last one
// overlapping the previous; n must be at least 8 and dp, bp, node, vals and
// idxs must each hold n entries.
//
//go:noescape
func nmrTrellisReconcileNEON(dp *float32, bp *uint8, node, vals *float32, idxs *int32, n, dHi int)
