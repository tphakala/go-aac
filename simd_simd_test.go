// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package aac

import "testing"

// TestSIMDEnabledDefaultBuild is the default-build half of the pair described in
// simd_noasm_test.go: -tags noasm excludes this file, so it compiles only on the
// default build, where the answer must be true.
func TestSIMDEnabledDefaultBuild(t *testing.T) {
	if !SIMDEnabled() {
		t.Fatal("SIMDEnabled() = false on the default build")
	}
}
