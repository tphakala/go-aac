// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"fmt"
	"testing"

	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
)

// BenchmarkQuantizeBandCost measures quantizeBandCost, which drives
// quantizeAndEncodeBandCost, the hottest function in the encoder, in its
// search configuration (pb nil, out nil, scaled precomputed, resBits and
// energy non-nil). Coefficients use the same deterministic LCG and
// per-codebook amplitude table as TestQuantizeAndEncodeBandMatchesC in
// quantize_golden_test.go, so the rate-distortion search runs its full cost
// instead of hitting a trivial early exit. Shapes cross the scalefactor
// band sizes seen in real streams (4 up to 96, the widest SwbSize1024
// entry) with one representative codebook from each coding family: SQUAD
// (signed quad), UQUAD (unsigned quad), UPAIR (unsigned pair) and ESC
// (unsigned pair with escape codes).
func BenchmarkQuantizeBandCost(b *testing.B) {
	sizes := []int{4, 16, 32, 48, 96}
	codebooks := []struct {
		name string
		cb   int
	}{
		{"cb1", 1},   // SQUAD
		{"cb4", 4},   // UQUAD
		{"cb10", 10}, // UPAIR
		{"cb11", 11}, // ESC
	}

	for _, size := range sizes {
		for _, cbc := range codebooks {
			b.Run(fmt.Sprintf("sz%d_%s", size, cbc.name), func(b *testing.B) {
				state := uint32(0x1f2e3d4c)
				in := quantizeSearchCoeffs(&state, cbc.cb, size)
				scaled := make([]float32, size)
				dsp.AbsPow34(scaled, in)

				var c Coder
				var sink float32
				var resBits int
				var energy float32
				for b.Loop() {
					sink += c.quantizeBandCost(in, scaled, ScaleOnePos, cbc.cb,
						1.0, fmath.Inf32(), &resBits, &energy)
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/call")
				_ = sink
			})
		}
	}
}
