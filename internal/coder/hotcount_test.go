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
	var ts nmrTrellisScratch
	for i := range dpp {
		dpp[i] = float32(i%17) * 0.5
		node[i] = float32(i%7) * 0.25
	}
	for i := range lamsf {
		lamsf[i] = float32(i) * 0.125
	}
	// Cycle base each iteration so neither the scalar branch predictor nor the
	// SIMD lane count trains on one fixed geometry: real solves feed small
	// band-offset differences straddling 0 and occasionally the mdiff edge.
	bases := []int{0, 3, -3, 8, -8, 45, -45}
	// The real shapes are 11x11 (coarse, step 8) and 17x17 (fine, step 1); the
	// 96x96 rows are the candidate cap, not a real shape, so they gate nothing
	// and stay only as a diagnostic. See issue #38.
	for _, shape := range []struct {
		name             string
		nCur, nPrev, stp int
	}{
		{"17x17_step1", 17, 17, 1},
		{"11x11_step8", 11, 11, 8},
		{"11x11_step1", 11, 11, 1},
		{"17x17_step8", 17, 17, 8},
		{"96x96_step1_diag", NMRNCand, NMRNCand, 1},
		{"96x96_step8_diag", NMRNCand, NMRNCand, 8},
	} {
		b.Run(shape.name, func(b *testing.B) {
			i := 0
			for b.Loop() {
				nmrTrellisStep(dp, bp, dpp, node, lamsf,
					shape.nCur, shape.nPrev, bases[i%len(bases)], shape.stp, mdiff, &ts)
				i++
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/call")
		})
	}

	// mix_real weights the two real shapes ~50/50 to approximate the encode's
	// call distribution (measured 50.7% 11-cand, 47.7% 17-cand); this is the
	// case the spike's GO/NO-GO leans on, not the single-shape rows.
	b.Run("mix_real", func(b *testing.B) {
		i := 0
		for b.Loop() {
			nCur, nPrev, stp := 17, 17, 1
			if i%2 == 1 {
				nCur, nPrev, stp = 11, 11, 8
			}
			nmrTrellisStep(dp, bp, dpp, node, lamsf,
				nCur, nPrev, bases[i%len(bases)], stp, mdiff, &ts)
			i++
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/call")
	})
}
