// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

// BandCostIsSIMD is true on the default build, recording that this package
// compiled the SIMD band-cost dispatch (bandcost_amd64.go, bandcost_arm64.go,
// and the portable stub bandcost_other.go on other architectures). The root
// package asserts it against aac.SIMDEnabled (simd_kernels_test.go).
const BandCostIsSIMD = true

// Flag bits passed to the band-cost kernels, one per codebook axis of
// quantize_and_encode_band_cost_template (aaccoder.c:75-193 @ d09d5afc3a).
const (
	bandCostPair     = 1 << iota // BT_PAIR: 2-dimensional codebook, else 4-dimensional
	bandCostUnsigned             // BT_UNSIGNED: magnitudes coded, sign bits counted
	bandCostEscape               // BT_ESC: codebook 11 escape coding
)

// bandCostFlags maps a codebook descriptor to the kernel flag bits.
func bandCostFlags(d cbInfo) int {
	flags := 0
	if d.pair {
		flags |= bandCostPair
	}
	if d.unsigned {
		flags |= bandCostUnsigned
	}
	if d.escape {
		flags |= bandCostEscape
	}
	return flags
}
