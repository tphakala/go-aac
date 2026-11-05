// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm && !amd64 && !arm64

package coder

// bandCostKernel reports ok == false on architectures without a band-cost
// kernel, so the caller runs the scalar loop.
func bandCostKernel(in []float32, q []int32, bits []uint8, vectors []float32, rng, off int, d cbInfo, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited, ok bool) {
	return 0, 0, 0, false, false
}
