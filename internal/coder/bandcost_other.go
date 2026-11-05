// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build noasm || !(amd64 || arm64)

package coder

// Without a band-cost kernel (-tags noasm, or an architecture that has none)
// quantizeAndEncodeBandCost always runs its scalar loop; the constant lets the
// compiler drop the kernel branch.
const (
	bandCostKernelOK = false
	bandCostMinSize  = 4
)

// bandCostAsm is never called: bandCostKernelOK is false.
func bandCostAsm(in []float32, q []int32, bits []uint8, vectors []float32, pow43 *[8192]float32, rng, off, flags int, iq, qs, rounding, clipEsc, lambda, uplim float32) (cost, qenergy float32, totBits int, exited bool) {
	panic("go-aac: band-cost kernel not compiled in")
}
