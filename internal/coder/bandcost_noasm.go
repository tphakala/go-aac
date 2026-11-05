// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm

package coder

// BandCostIsSIMD is false on the noasm build: quantizeAndEncodeBandCost runs
// only its scalar loop.
const BandCostIsSIMD = false

// bandCostKernel reports ok == false on the noasm build, so the caller runs
// the scalar loop.
func bandCostKernel(in []float32, q []int32, bits []uint8, vectors []float32, rng, off int, d cbInfo, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited, ok bool) {
	return 0, 0, 0, false, false
}
