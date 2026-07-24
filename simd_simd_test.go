// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build goaac_simd

package aac

import "testing"

// TestSIMDEnabledTaggedBuild is the goaac_simd half of the pair described in
// simd_noasm_test.go: this file exists only under the tag, so the answer must be
// true.
func TestSIMDEnabledTaggedBuild(t *testing.T) {
	if !SIMDEnabled() {
		t.Fatal("SIMDEnabled() = false on a build with -tags goaac_simd")
	}
}
