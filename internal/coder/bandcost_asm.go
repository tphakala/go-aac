// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm && (amd64 || arm64)

package coder

// bandCostAsm evaluates a cost-only quantizeAndEncodeBandCost call (no
// bitstream, no dequantized output): AVX2 in bandcost_amd64.s, NEON in
// bandcost_arm64.s. It computes one coefficient per lane for the products,
// each rounded separately as in the scalar loop, then sums cost, bit count and
// energy in coefficient order in a second pass, so the result and the early
// exit at cost >= uplim match the scalar loop bit for bit.
// TestBandCostKernelMatchesScalar and FuzzBandCostEquiv gate it.
//
// The band must be non-empty, at most 96 coefficients (the kernels keep their
// per-band results in a fixed stack frame) and a multiple of four wide. flags
// is bandCostFlagTable[cb].
//
//go:noescape
func bandCostAsm(in []float32, q []int32, bits []uint8, vectors []float32, pow43 *[8192]float32, rng, off, flags int, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited bool)
