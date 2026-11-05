// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import (
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/simd/cpu"
)

// bandCostKernel evaluates a cost-only quantizeAndEncodeBandCost call (no
// bitstream, no dequantized output) with NEON and reports ok == true; under
// SIMD_DISABLE it reports ok == false and the caller runs the scalar loop. The
// kernel uses separate FMUL and FADD, never FMLA, so it matches the scalar loop
// whose float32 conversions block fusion: TestBandCostKernelMatchesScalar and
// FuzzBandCostEquiv gate it.
func bandCostKernel(in []float32, q []int32, bits []uint8, vectors []float32, rng, off int, d cbInfo, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited, ok bool) {
	if !cpu.ARM64.NEON {
		return 0, 0, 0, false, false
	}
	cost, qenergy, totBits, exited = bandCostNEON(in, q, bits, vectors, &tables.Pow43, rng, off, bandCostFlags(d), iq, qs, rounding, clipEsc, lambda, uplim)
	return cost, qenergy, totBits, exited, true
}

// bandCostNEON is the NEON band-cost kernel, the same two-pass scheme as
// bandCostAVX2.
//
//go:noescape
func bandCostNEON(in []float32, q []int32, bits []uint8, vectors []float32, pow43 *[8192]float32, rng, off, flags int, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited bool)
