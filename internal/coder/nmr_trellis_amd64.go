// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import "github.com/tphakala/simd/cpu"

// nmrNodeCost evaluates nmrNodeCostGo with AVX2 for a band of at least eight
// candidates when the CPU has AVX2 (and SIMD_DISABLE does not mask it), else
// with nmrNodeCostGo. Each lane is one candidate: the same int32 to float32
// conversion, float32 product and float32 sum, so the result is bit-identical.
func nmrNodeCost(dst []float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, lam float32) {
	if cpu.X86.AVX2 && len(dst) >= 8 {
		nmrNodeCostAVX2(&dst[0], nd, nb, len(dst), lam)
		return
	}
	nmrNodeCostGo(dst, nd, nb, lam)
}

// nmrTrellisReconcile evaluates nmrTrellisReconcileGo with AVX2 for at least
// eight rows when the CPU has AVX2, else with nmrTrellisReconcileGo. Each lane
// is one row: the same strict compare against MaxFloat32, float32 sum and
// back-pointer, so the result is bit-identical.
func nmrTrellisReconcile(dp []float32, bp []uint8, node, vals []float32, idxs []int32, dHi int) {
	if cpu.X86.AVX2 && len(dp) >= 8 {
		nmrTrellisReconcileAVX2(&dp[0], &bp[0], &node[0], &vals[0], &idxs[0], len(dp), dHi)
		return
	}
	nmrTrellisReconcileGo(dp, bp, node, vals, idxs, dHi)
}

// nmrNodeCostAVX2 computes eight candidates per iteration, the last one
// overlapping the previous; n must be at least 8.
//
//go:noescape
func nmrNodeCostAVX2(dst *float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, n int, lam float32)

// nmrTrellisReconcileAVX2 reconciles eight rows per iteration, the last one
// overlapping the previous; n must be at least 8 and dp, bp, node, vals and
// idxs must each hold n entries.
//
//go:noescape
func nmrTrellisReconcileAVX2(dp *float32, bp *uint8, node, vals *float32, idxs *int32, n, dHi int)
