// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import (
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/simd/cpu"
)

// bandCostKernel evaluates a cost-only quantizeAndEncodeBandCost call (no
// bitstream, no dequantized output) with AVX2 and reports ok == true; without
// AVX2 (or under SIMD_DISABLE) it reports ok == false and the caller runs the
// scalar loop. The result is bit-identical to that loop:
// TestBandCostKernelMatchesScalar and FuzzBandCostEquiv gate it.
func bandCostKernel(in []float32, q []int32, bits []uint8, vectors []float32, rng, off int, d cbInfo, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited, ok bool) {
	if !cpu.X86.AVX2 {
		return 0, 0, 0, false, false
	}
	cost, qenergy, totBits, exited = bandCostAVX2(in, q, bits, vectors, &tables.Pow43, rng, off, bandCostFlags(d), iq, qs, rounding, clipEsc, lambda, uplim)
	return cost, qenergy, totBits, exited, true
}

// bandCostAVX2 is the AVX2 band-cost kernel: one coefficient per lane for the
// products, separately rounded as in the scalar loop, and an in-order second
// pass for the cost, bit and energy sums.
//
//go:noescape
func bandCostAVX2(in []float32, q []int32, bits []uint8, vectors []float32, pow43 *[8192]float32, rng, off, flags int, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited bool)
