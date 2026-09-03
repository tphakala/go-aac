// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestNoFloat32FMAContraction is the standing guard for issue #59: it builds
// every package in this module for arm64 with the assembly listing enabled and
// fails on any compiler-emitted float32 fused multiply-add (FMADDS/FMSUBS/
// FNMADDS/FNMSUBS). The AAC reference is an FFmpeg build with -ffp-contract=off,
// which never fuses, so a contracted float32 a*b+c is both a fidelity mismatch
// and a GOARCH-dependent output split (gc contracts on arm64 but not amd64).
// Every such site must carry an explicit float32() rounding barrier.
//
// float64 FMA (FMADDD, ...) is guarded separately, in the determinism-critical
// paths, by TestNoFloat64FMAInDeterministicMath below: the vendored
// transcendentals (internal/fmath), the MDCT (internal/mdct), the TNS LPC
// (internal/dsp) and the psy ATH curve (internal/psy) are barriered
// float64-FMA-free. The float64 FMAs left unguarded are all in internal/window
// (the KBD and KBDFixed scale accumulations and the SineFixed rescale), each an
// exact power-of-two multiply and therefore value-neutral. This runs by
// cross-compilation, so it checks arm64 from any host; it is skipped under
// -short and when the go toolchain is not on PATH.
//
// This scans the default (SIMD, !noasm) build. The *_noasm.go scalar kernel
// fallbacks are not built here; their float32 FMA is instead backstopped by
// TestEncoderArchDeterminism, which CI also runs under -tags noasm on both
// arches, so a noasm arm64-vs-amd64 split trips the golden.
func TestNoFloat32FMAContraction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping toolchain build under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not found on PATH")
	}

	// A fresh GOCACHE is load-bearing: with a warm cache the compile is elided
	// and -S prints nothing, silently greening the test.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "build",
		"-gcflags=github.com/tphakala/go-aac/...=-S", "./...")
	cmd.Env = append(cmd.Environ(),
		"GOARCH=arm64", "GOOS=linux", "CGO_ENABLED=0", "GOCACHE="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("go build -gcflags=-S timed out after 5m; output so far:\n%.4000s", out)
	}
	if err != nil {
		t.Fatalf("go build -gcflags=-S failed: %v\n%s", err, out)
	}

	stext := regexp.MustCompile(`^(\S+)\s+STEXT`)
	// Tab-delimited opcode column, so data/symbol lines cannot false-match.
	fma := regexp.MustCompile(`\t(FMADDS|FMSUBS|FNMADDS|FNMSUBS)\t`)
	// Empty by design: every contracting float32 site is barriered.
	allow := map[string]bool{}

	var fn string
	var sawPsy bool // canary: -S output must actually contain our code
	var violations []string
	for line := range strings.Lines(string(out)) {
		if m := stext.FindStringSubmatch(line); m != nil {
			fn = m[1]
			if strings.Contains(fn, "internal/psy.") {
				sawPsy = true
			}
			continue
		}
		if m := fma.FindStringSubmatch(line); m != nil {
			key := fn + " " + m[1]
			if !allow[key] {
				violations = append(violations, key+"\n    "+strings.TrimSpace(line))
			}
		}
	}
	if !sawPsy {
		t.Fatalf("no STEXT output for internal/psy: the -S scan produced nothing "+
			"(warm cache?), so this guard cannot see float32 FMA.\n%.4000s", out)
	}
	if len(violations) > 0 {
		t.Fatalf("float32 FMA contraction found (%d sites); add a float32() barrier at each:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestNoFloat64FMAInDeterministicMath is the standing guard for issue #79: the
// determinism-critical float64 paths must contain no compiler-emitted float64
// fused multiply-add (FMADDD/FMSUBD/FNMADDD/FNMSUBD) on arm64. These are the
// vendored transcendentals (internal/fmath), the MDCT init and transform
// (internal/mdct), the TNS autocorrelation and Levinson recursion (internal/dsp),
// and the psychoacoustic ATH curve (internal/psy). Every such site is barriered
// so it matches the -ffp-contract=off C reference and stays identical on arm64
// and amd64; a contracted float64 a*b+c would reintroduce the sub-ULP GOARCH
// split that issue #59 removed, because these float64 values become spectral
// coefficients, TNS coefficients and psy thresholds that are then cast to
// float32. Every site carries an explicit float64() rounding barrier.
//
// These four packages are the encoder's determinism-critical float64 surface:
// internal/coder and internal/enc are float64-FMA-free (their per-frame
// arithmetic is float32, guarded by TestNoFloat32FMAContraction above), and per
// the depguard rule (.golangci.yaml) coder and psy cannot import math, so their
// transcendentals route through internal/fmath. The only other math-importing
// packages are internal/window (whose float64 FMAs are the value-neutral
// power-of-two multiplies below) and the decoder-only internal/tx and
// internal/tables/cbrt_fixed.go, so this build needs no allow-list. The
// deliberately unguarded float64 FMAs are all in internal/window (the KBD and
// KBDFixed scale accumulations and the SineFixed rescale), each an exact
// power-of-two multiply and therefore value-neutral; the encoder window tables
// are pinned cross-arch by internal/window's TestWindowTablesGolden and the
// decoder's int32 tables by internal/dec's TestIMDCTDump.
func TestNoFloat64FMAInDeterministicMath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping toolchain build under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not found on PATH")
	}

	// A fresh GOCACHE is load-bearing: with a warm cache the compile is elided
	// and -S prints nothing, silently greening the test.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "build",
		"-gcflags=github.com/tphakala/go-aac/...=-S",
		"./internal/fmath/", "./internal/mdct/", "./internal/dsp/", "./internal/psy/")
	cmd.Env = append(cmd.Environ(),
		"GOARCH=arm64", "GOOS=linux", "CGO_ENABLED=0", "GOCACHE="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("go build -gcflags=-S timed out after 5m; output so far:\n%.4000s", out)
	}
	if err != nil {
		t.Fatalf("go build -gcflags=-S failed: %v\n%s", err, out)
	}

	stext := regexp.MustCompile(`^(\S+)\s+STEXT`)
	// Tab-delimited opcode column, so data/symbol lines cannot false-match.
	fma := regexp.MustCompile(`\t(FMADDD|FMSUBD|FNMADDD|FNMSUBD)\t`)
	inScope := func(sym string) bool {
		return strings.Contains(sym, "internal/fmath.") ||
			strings.Contains(sym, "internal/mdct.") ||
			strings.Contains(sym, "internal/dsp.") ||
			strings.Contains(sym, "internal/psy.")
	}

	var fn string
	var sawFmath bool // canary: -S output must actually contain our code
	var violations []string
	for line := range strings.Lines(string(out)) {
		if m := stext.FindStringSubmatch(line); m != nil {
			fn = m[1]
			if strings.Contains(fn, "internal/fmath.") {
				sawFmath = true
			}
			continue
		}
		if !inScope(fn) {
			continue
		}
		if m := fma.FindStringSubmatch(line); m != nil {
			violations = append(violations, fn+" "+m[1]+"\n    "+strings.TrimSpace(line))
		}
	}
	if !sawFmath {
		t.Fatalf("no STEXT output for internal/fmath: the -S scan produced nothing "+
			"(warm cache?), so this guard cannot see float64 FMA.\n%.4000s", out)
	}
	if len(violations) > 0 {
		t.Fatalf("float64 FMA contraction found (%d sites); add a float64() barrier at each:\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
