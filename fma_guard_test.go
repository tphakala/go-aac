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
// float64 FMA (FMADDD, ...) is deliberately NOT flagged: those sites (MDCT,
// LPC, window init) were measured to round away in the float32 pipeline and are
// tracked separately by the transcendental-determinism follow-up. This runs by
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
