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
// then disagree with the kernel actually in the binary, over-claiming under one
// tag and under-claiming under the other. This assertion is what catches that,
// in either direction.
//
// This file carries no build tag on purpose, so the assertion runs in both
// builds rather than only in the one the tag selects.
func TestSIMDEnabledMatchesKernels(t *testing.T) {
	if dsp.AbsPow34IsSIMD != SIMDEnabled() {
		t.Errorf("dsp.AbsPow34IsSIMD = %v, SIMDEnabled() = %v",
			dsp.AbsPow34IsSIMD, SIMDEnabled())
	}
	if coder.NMRTrellisIsSIMD != SIMDEnabled() {
		t.Errorf("coder.NMRTrellisIsSIMD = %v, SIMDEnabled() = %v",
			coder.NMRTrellisIsSIMD, SIMDEnabled())
	}
}
