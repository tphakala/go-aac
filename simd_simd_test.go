// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package aac

import (
	"os"
	"strings"
	"testing"

	"github.com/tphakala/simd/cpu"
)

// TestSIMDEnabledDefaultBuild is the default-build half of the pair described in
// simd_noasm_test.go: -tags noasm excludes this file, so it compiles only on the
// default build, where the answer must be true.
func TestSIMDEnabledDefaultBuild(t *testing.T) {
	if !SIMDEnabled() {
		t.Fatal("SIMDEnabled() = false on the default build")
	}
}

// TestSIMDDisableEnvIsHonoured guards the non-vacuity of the CI lane that runs
// this suite with SIMD_DISABLE=all.
//
// That lane exists because the SIMD kernels' equivalence to their scalar
// counterparts is fuzz-asserted but was never golden-asserted end to end on a
// host that takes the RUNTIME fallback. -tags noasm does not cover that case: it
// compiles the SIMD dispatch out altogether, a different file, so it never
// exercises the feature check inside a SIMD build. Setting SIMD_DISABLE reaches
// the real path, because github.com/tphakala/simd masks its detected features
// from that variable (cpu/cpu_amd64.go, cpu/cpu_arm64.go).
//
// The hazard is that the lane silently stops testing anything: if the
// dependency renamed or dropped the variable, every encode would quietly go
// back to the vectorised path and the lane would keep printing ok while
// asserting nothing. So when SIMD_DISABLE names everything, this asserts the
// masking actually took effect.
//
// It is deliberately narrow. It only runs when the variable is set, so a normal
// `go test ./...` skips it, and it asserts only that features were masked, not
// which ones a given host has.
func TestSIMDDisableEnvIsHonoured(t *testing.T) {
	// The dependency parses SIMD_DISABLE as a comma-separated, case-insensitive
	// token list and treats the token "all" as "clear every feature"
	// (cpu/cpu.go applyDisable), so match it the same way rather than on an
	// exact literal: "ALL" and "all," are full masks too.
	//
	// Anything that is not a full mask skips, INCLUDING a partial mask such as
	// SIMD_DISABLE=avx2. Those are a supported per-tier A/B workflow for the
	// three SIMD kernels this repo ships, and failing them here would break a
	// developer bisecting a kernel for a reason unrelated to what they are
	// testing. What stops the CI lane silently decaying into a third copy of the
	// default pass is not this skip but the workflow step beside it, which runs
	// this test alone under SIMD_DISABLE=all and requires a PASS rather than a
	// skip; a skip there fails the step.
	if !simdDisableIsFullMask(os.Getenv("SIMD_DISABLE")) {
		t.Skip("SIMD_DISABLE is not a full mask; this guard is for the CI fallback lane")
	}

	// Assert on the structured feature sets rather than on cpu.Info(), whose
	// wording is per-arch: amd64 renders "AMD64 (scalar)" but arm64 renders
	// "ARM64 (no SIMD)" and the generic build "Generic (no SIMD)"
	// (cpu/cpu_amd64.go, cpu/cpu_arm64_info.go, cpu/cpu_other.go). Matching a
	// substring would pass on amd64 and fail everywhere else, which is the
	// wrong way round for a guard that exists to be trustworthy. Features is
	// all-bool, so the zero value IS "nothing detected". This covers amd64 and
	// arm64, the two the dependency populates; on any other architecture
	// cpu_other.go has no init at all, so both stay zero and the check is
	// trivially satisfied rather than meaningfully covering.
	//
	// cpu.Info() still goes in the failure message, because when this fires the
	// question is which features survived.
	if cpu.X86 != (cpu.Features{}) || cpu.ARM64 != (cpu.Features{}) {
		t.Fatalf("SIMD_DISABLE=all did not mask the CPU features: cpu.Info() = %q. "+
			"The CI fallback lane is running on the vectorised path and gating nothing; "+
			"check whether github.com/tphakala/simd still honours SIMD_DISABLE", cpu.Info())
	}
}

// simdDisableIsFullMask reports whether spec, the raw SIMD_DISABLE value, is one
// the dependency treats as clearing every CPU feature. It mirrors applyDisable's
// parsing (github.com/tphakala/simd cpu/cpu.go): split on commas, trim, compare
// case-insensitively, ignore empty tokens.
func simdDisableIsFullMask(spec string) bool {
	for tok := range strings.SplitSeq(spec, ",") {
		if strings.EqualFold(strings.TrimSpace(tok), "all") {
			return true
		}
	}
	return false
}
