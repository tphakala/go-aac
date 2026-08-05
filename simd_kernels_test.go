// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"testing"

	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/dsp"
)

// TestSIMDEnabledMatchesKernels asserts that what SIMDEnabled reports is what
// the kernel packages actually compiled.
//
// internal/simdbuild derives its answer from the build tag on its own files,
// never from the dispatch files, so nothing else stops the two from drifting.
// Moving a kernel pair to a different tag, a per-kernel tag for instance, would
// still compile and would still pass every equivalence and oracle test, because
// a scalar kernel matches the scalar reference trivially. The accessor would
// then disagree with the kernel actually in the binary. This assertion is what
// catches that.
//
// It checks which dispatch file each package compiled, not that the compiled
// dispatch reaches github.com/tphakala/simd, and it knows only the two kernels
// named below. So every dispatch pair gated on goaac_simd owes this file a
// <Kernel>IsSIMD constant and a line here; a third pair that skips both is
// invisible to it.
//
// This file carries no build tag on purpose, so the assertion runs in both
// builds rather than only in the one the tag selects. It does not subsume the
// tagged pair in simd_noasm_test.go and simd_simd_test.go: those pin the literal
// tag name, which this file never mentions.
func TestSIMDEnabledMatchesKernels(t *testing.T) {
	if dsp.AbsPow34IsSIMD != SIMDEnabled() {
		t.Errorf("dsp.AbsPow34IsSIMD = %v, SIMDEnabled() = %v",
			dsp.AbsPow34IsSIMD, SIMDEnabled())
	}
	if dsp.QuantizeBandsIsSIMD != SIMDEnabled() {
		t.Errorf("dsp.QuantizeBandsIsSIMD = %v, SIMDEnabled() = %v",
			dsp.QuantizeBandsIsSIMD, SIMDEnabled())
	}
	if coder.NMRTrellisIsSIMD != SIMDEnabled() {
		t.Errorf("coder.NMRTrellisIsSIMD = %v, SIMDEnabled() = %v",
			coder.NMRTrellisIsSIMD, SIMDEnabled())
	}
}
