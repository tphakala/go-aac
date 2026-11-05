// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm && !amd64 && !arm64

package coder

// nmrNodeCost and nmrTrellisReconcile use the scalar references on
// architectures without a kernel.
func nmrNodeCost(dst []float32, nd *[NMRNCand]float32, nb *[NMRNCand]int32, lam float32) {
	nmrNodeCostGo(dst, nd, nb, lam)
}

func nmrTrellisReconcile(dp []float32, bp []uint8, node, vals []float32, idxs []int32, dHi int) {
	nmrTrellisReconcileGo(dp, bp, node, vals, idxs, dHi)
}
