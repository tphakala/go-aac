// SPDX-License-Identifier: LGPL-2.1-or-later
package coder

import "testing"

// BenchmarkNMRTrellisStepShapes measures nmrTrellisStep at the shapes the real
// encode drives it with. Two leaf attributions in the full-encode profile have
// already proven to be phantoms (off by 47x and 260x), so no leaf cost is
// believed here until it is measured directly and reconciled against the call
// count.
func BenchmarkNMRTrellisStepShapes(b *testing.B) {
	const mdiff = ScaleMaxDiff
	dp := make([]float32, NMRNCand)
	bp := make([]uint8, NMRNCand)
	dpp := make([]float32, NMRNCand)
	node := make([]float32, NMRNCand)
	lamsf := make([]float32, 2*ScaleMaxDiff+1)
	for i := range dpp {
		dpp[i] = float32(i%17) * 0.5
		node[i] = float32(i%7) * 0.25
	}
	for i := range lamsf {
		lamsf[i] = float32(i) * 0.125
	}
	for _, shape := range []struct {
		name             string
		nCur, nPrev, stp int
		base             int
	}{
		{"96x96_step1", NMRNCand, NMRNCand, 1, 0},
		{"96x96_step8", NMRNCand, NMRNCand, 8, 0},
		{"96x96_step8_base45", NMRNCand, NMRNCand, 8, 45},
		{"16x16_step1", 16, 16, 1, 0},
	} {
		b.Run(shape.name, func(b *testing.B) {
			for b.Loop() {
				nmrTrellisStep(dp, bp, dpp, node, lamsf,
					shape.nCur, shape.nPrev, shape.base, shape.stp, mdiff)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/call")
		})
	}
}
