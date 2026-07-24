// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !goaac_simd

package aac

import "testing"

// TestSIMDEnabledDefaultBuild pins the accessor to the build it was compiled
// under. This file exists only in the default build, so the answer must be
// false; simd_simd_test.go asserts the other half. What the pair catches is
// internal/simdbuild's constants being inverted, or its tag drifting from the
// name the build actually uses; a tag typo inside that package is already a
// compile error in one of the two builds. Neither test observes a kernel, so
// agreement with the kernels is asserted separately in simd_kernels_test.go.
func TestSIMDEnabledDefaultBuild(t *testing.T) {
	if SIMDEnabled() {
		t.Fatal("SIMDEnabled() = true on a build without -tags goaac_simd")
	}
}
