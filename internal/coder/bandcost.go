// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

// Flag bits passed to the band-cost kernels, one per codebook axis of
// quantize_and_encode_band_cost_template (aaccoder.c:75-193 @ d09d5afc3a).
// The assembly reads them as const_bandCost* through go_asm.h.
const (
	bandCostPair     = 1 << iota // BT_PAIR: 2-dimensional codebook, else 4-dimensional
	bandCostUnsigned             // BT_UNSIGNED: magnitudes coded, sign bits counted
	bandCostEscape               // BT_ESC: codebook 11 escape coding
)

// bandCostMaxSize is the widest band the kernels take: they keep their per-band
// results in a fixed stack frame sized for 96 coefficients, the widest
// SwbSize1024 entry.
const bandCostMaxSize = 96

// bandCostFlagTable holds the kernel flag bits of every codebook, derived once
// from cbDescriptors so the hot path indexes it instead of rebuilding the flags.
var bandCostFlagTable = func() (t [len(cbDescriptors)]int) {
	for cb, d := range cbDescriptors {
		if d.pair {
			t[cb] |= bandCostPair
		}
		if d.unsigned {
			t[cb] |= bandCostUnsigned
		}
		if d.escape {
			t[cb] |= bandCostEscape
		}
	}
	return t
}()
