// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The FMA-contraction guards in this file cross-compile the whole module with
// the assembly listing enabled (-S) and fail on any compiler-emitted fused
// multiply-add in code that must match the C reference. The AAC reference is an
// FFmpeg build with -ffp-contract=off, which never fuses, so a contracted a*b+c
// is both a fidelity mismatch and a GOARCH-dependent output split. Every such
// site carries an explicit float32() or float64() rounding barrier.
//
// Two architectures fuse a*b+c today and both are checked as subtests: arm64
// fuses by default, and amd64 fuses under GOAMD64=v3. The barrier is a plain
// float32()/float64() conversion, so it holds regardless of architecture; the
// amd64 subtest exists because amd64 spells and schedules FMA differently, so a
// missing barrier could surface there even when arm64 happens not to fuse it.
// The scan reads the emitted assembly and never runs it, so it checks both
// targets from any host. It is skipped under -short and when the go toolchain is
// not on PATH.

const fmaGuardBuildTimeout = 5 * time.Minute

// Architecture labels reused across both guards. arm64 doubles as the GOARCH and
// the human label; amd64's label names the GOAMD64 level that enables its FMA.
const (
	archARM64   = "arm64"
	archAMD64v3 = "amd64 GOAMD64=v3"
)

// Compiled once at package load rather than per scan. stextRe pulls the symbol
// name out of an -S function header; the four opcode patterns match a scalar
// fused multiply-add in the tab-delimited opcode column (so data or symbol lines
// cannot false-match). arm64 uses FMADD/FMSUB and their negated forms
// (F[N]M{ADD,SUB}{S,D}); amd64 uses the VEX FMA3 forms
// VF[N]M{ADD,SUB}{132,213,231}{SS,SD}. The capturing group is the full opcode,
// which appears verbatim in the violation message.
var (
	stextRe  = regexp.MustCompile(`^(\S+)\s+STEXT`)
	fma32ARM = regexp.MustCompile(`\t(FMADDS|FMSUBS|FNMADDS|FNMSUBS)\t`)
	fma32AMD = regexp.MustCompile(`\t(VFN?M(?:ADD|SUB)(?:132|213|231)SS)\t`)
	fma64ARM = regexp.MustCompile(`\t(FMADDD|FMSUBD|FNMADDD|FNMSUBD)\t`)
	fma64AMD = regexp.MustCompile(`\t(VFN?M(?:ADD|SUB)(?:132|213|231)SD)\t`)
)

// Package prefixes used as scope substrings and as canaries. The trailing dot
// stops a prefix from matching a sibling package (internal/dsputil.) or a
// subpackage.
const (
	pkgFmath = "internal/fmath."
	pkgMdct  = "internal/mdct."
	pkgDsp   = "internal/dsp."
	pkgPsy   = "internal/psy."
)

// determinismCriticalFloat64Scope is the float64 guard's allowlist: the packages
// whose float64 values become spectral coefficients, TNS coefficients and psy
// thresholds that are later cast to float32, so a contracted FMA in them would
// shift the float32 result. It is a hand-maintained allowlist rather than "every
// package" because some float64 FMA elsewhere is legitimate (see the guard's
// doc). The per-scope positive control in runFMAGuard fails if any entry here
// stops appearing in the listing, so a typo cannot silently drop coverage; a new
// package that computes determinism-critical float64 values must be added here.
var determinismCriticalFloat64Scope = []string{pkgFmath, pkgMdct, pkgDsp, pkgPsy}

// fmaGuardCase is one architecture's guard: the target to cross-compile for, the
// opcode class that spells FMA there, the packages in scope, a canary substring
// that proves the listing is non-empty when the scope is "every package", and
// the value kind named in the barrier hint.
type fmaGuardCase struct {
	arch    string // label for failure messages, e.g. "arm64" or "amd64 GOAMD64=v3"
	goarch  string
	goamd64 string // GOAMD64 level, empty for the default
	op      *regexp.Regexp
	scope   []string // package substrings in scope; empty means every package
	canary  string   // non-empty proof used only when scope is empty
	kind    string   // "float32" or "float64", used to name the barrier to add
}

// inScope reports whether a symbol is in this guard's scope. An empty scope
// means every package (the float32 guard: every contracting site is barriered).
func (c *fmaGuardCase) inScope(sym string) bool {
	if len(c.scope) == 0 {
		return true
	}
	for _, s := range c.scope {
		if strings.Contains(sym, s) {
			return true
		}
	}
	return false
}

// required returns the substrings that must each appear in the listing for the
// scan to be trusted. For a scoped guard that is every scope entry, so a missing
// or mistyped package is caught; for the every-package guard it is the single
// canary, which only proves the listing is non-empty (a warm cache prints none).
func (c *fmaGuardCase) required() []string {
	if len(c.scope) > 0 {
		return c.scope
	}
	return []string{c.canary}
}

// fmaListingCache memoizes the fresh-GOCACHE `-S` cross-compile per target
// (GOARCH plus GOAMD64 level) so the float32 and float64 scans over the same
// build share a single cold compile. The build target is always ./..., so the
// float32 scan (every package) and the float64 scan (the scoped subset) both
// read the same listing. The cache is process-local: `go test -count=N` reuses
// the listing for the scans but rebuilds once per process, and a transient build
// error is cached, so both guards for that target report it rather than each
// paying the timeout again.
var (
	fmaListingMu    sync.Mutex
	fmaListingOnce  = map[string]*sync.Once{}
	fmaListingCache = map[string]fmaListing{}
)

type fmaListing struct {
	out string
	err error
}

// fmaGuardListing returns the assembly listing for the given target, building it
// at most once per target across all guard subtests.
func fmaGuardListing(t *testing.T, goarch, goamd64 string) (string, error) {
	t.Helper()
	key := goarch + "/" + goamd64

	fmaListingMu.Lock()
	once, ok := fmaListingOnce[key]
	if !ok {
		once = &sync.Once{}
		fmaListingOnce[key] = once
	}
	fmaListingMu.Unlock()

	once.Do(func() {
		res := buildFMAListing(t, goarch, goamd64)
		fmaListingMu.Lock()
		fmaListingCache[key] = res
		fmaListingMu.Unlock()
	})

	fmaListingMu.Lock()
	res := fmaListingCache[key]
	fmaListingMu.Unlock()
	return res.out, res.err
}

// buildFMAListing runs one fresh-GOCACHE `go build -gcflags=...=-S` cross-compile
// and returns the combined assembly listing. A fresh GOCACHE is load-bearing:
// with a warm cache the compile is elided and -S prints nothing, silently
// greening the guard. GOFLAGS and GOEXPERIMENT are pinned empty so an ambient
// value (for example GOFLAGS=-tags=noasm in a shell or CI) cannot make the guard
// scan a different build variant than the default SIMD one it documents.
func buildFMAListing(t *testing.T, goarch, goamd64 string) fmaListing {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmaListing{err: fmt.Errorf("go toolchain not found on PATH: %w", err)}
	}
	// t.TempDir is safe as the shared GOCACHE: the build runs synchronously here
	// and only the listing string is cached, so the directory is no longer needed
	// once buildFMAListing returns, well before this subtest's cleanup fires.
	gocache := t.TempDir()

	ctx, cancel := context.WithTimeout(t.Context(), fmaGuardBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "build",
		"-gcflags=github.com/tphakala/go-aac/...=-S", "./...")
	env := append(cmd.Environ(),
		"GOARCH="+goarch, "GOOS=linux", "CGO_ENABLED=0", "GOCACHE="+gocache,
		"GOFLAGS=", "GOEXPERIMENT=")
	if goamd64 != "" {
		env = append(env, "GOAMD64="+goamd64)
	}
	cmd.Env = env

	out, buildErr := cmd.CombinedOutput()
	res := fmaListing{out: string(out)}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		res.err = fmt.Errorf("go build -gcflags=-S timed out after %s", fmaGuardBuildTimeout)
	case buildErr != nil:
		res.err = fmt.Errorf("go build -gcflags=-S failed: %w", buildErr)
	}
	return res
}

// fmaScan walks an -S listing and returns every in-scope symbol that emits an
// FMA opcode matched by op, plus the required substrings that never appeared.
// A non-empty missing set means the scan cannot be trusted (a warm cache elided
// the compile, or a scope substring is stale), which the caller treats as a
// failure rather than a silent green.
func fmaScan(listing string, op *regexp.Regexp, inScope func(string) bool, required []string) (violations, missing []string) {
	seen := make([]bool, len(required))
	var fn string
	for line := range strings.Lines(listing) {
		if m := stextRe.FindStringSubmatch(line); m != nil {
			fn = m[1]
			for i, r := range required {
				if !seen[i] && strings.Contains(fn, r) {
					seen[i] = true
				}
			}
			continue
		}
		if !inScope(fn) {
			continue
		}
		if m := op.FindStringSubmatch(line); m != nil {
			violations = append(violations, fn+" "+m[1]+"\n    "+strings.TrimSpace(line))
		}
	}
	for i, r := range required {
		if !seen[i] {
			missing = append(missing, r)
		}
	}
	return violations, missing
}

// runFMAGuard fetches the cached listing for c.arch, fails if any required
// package produced no symbols (warm cache or a stale scope entry), and fails if
// any in-scope symbol emits an FMA opcode.
func runFMAGuard(t *testing.T, c *fmaGuardCase) {
	t.Helper()
	listing, err := fmaGuardListing(t, c.goarch, c.goamd64)
	if err != nil {
		t.Fatalf("%v\n%.4000s", err, listing)
	}
	violations, missing := fmaScan(listing, c.op, c.inScope, c.required())
	if len(missing) > 0 {
		t.Fatalf("%s %s guard saw no compiled code for %s "+
			"(warm cache, or a stale scope substring?), so it cannot see FMA contraction.\n%.4000s",
			c.arch, c.kind, strings.Join(missing, ", "), listing)
	}
	if len(violations) > 0 {
		t.Fatalf("%s %s FMA contraction found (%d sites); add a %s() barrier at each:\n%s",
			c.arch, c.kind, len(violations), c.kind, strings.Join(violations, "\n"))
	}
}

// fmaGuardPrereqs skips a guard when its toolchain build cannot run.
func fmaGuardPrereqs(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping toolchain build under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found on PATH")
	}
}

// TestNoFloat32FMAContraction is the standing guard for issue #59: it builds
// every package in this module with the assembly listing enabled and fails on
// any compiler-emitted float32 fused multiply-add. It checks the two targets
// that fuse a*b+c: arm64 (FMADDS/FMSUBS/FNMADDS/FNMSUBS, fused by default) and
// amd64 under GOAMD64=v3 (VFMADD.../VFMSUB...SS). A contracted float32 a*b+c is
// both a fidelity mismatch against the -ffp-contract=off C reference and a
// GOARCH-dependent output split, so every such site carries an explicit
// float32() rounding barrier. The scope is every package (no allow-list): a
// contracting float32 site anywhere is a violation.
//
// float64 FMA is guarded separately, in the determinism-critical paths, by
// TestNoFloat64FMAInDeterministicMath below.
//
// This scans the default (SIMD, !noasm) build; buildFMAListing pins GOFLAGS and
// GOEXPERIMENT empty so an ambient tag cannot switch the variant. The *_noasm.go
// scalar kernel fallbacks are not built here; their float32 FMA is instead
// backstopped by TestEncoderArchDeterminism, which CI also runs under -tags
// noasm on both arches, so a noasm arm64-vs-amd64 split trips the golden.
func TestNoFloat32FMAContraction(t *testing.T) {
	fmaGuardPrereqs(t)
	for _, c := range []fmaGuardCase{
		{arch: archARM64, goarch: archARM64, op: fma32ARM, canary: pkgPsy, kind: "float32"},
		{arch: archAMD64v3, goarch: "amd64", goamd64: "v3", op: fma32AMD, canary: pkgPsy, kind: "float32"},
	} {
		t.Run(c.goarch+c.goamd64, func(t *testing.T) { runFMAGuard(t, &c) })
	}
}

// TestNoFloat64FMAInDeterministicMath is the standing guard for issue #79: the
// determinism-critical float64 paths must contain no compiler-emitted float64
// fused multiply-add. It checks arm64 (FMADDD/FMSUBD/FNMADDD/FNMSUBD, fused by
// default) and amd64 under GOAMD64=v3 (VFMADD.../VFMSUB...SD). The scoped
// packages (determinismCriticalFloat64Scope) are internal/fmath (vendored
// transcendentals), internal/mdct (MDCT init and transform), internal/dsp (TNS
// autocorrelation and Levinson recursion) and internal/psy (the psychoacoustic
// model): the float64 values they compute become spectral coefficients, TNS
// coefficients and psy thresholds that are then cast to float32, so a contracted
// a*b+c would reintroduce the sub-ULP GOARCH split that issue #59 removed. Every
// such site carries an explicit float64() rounding barrier.
//
// Scope is a deliberate allow-list, not "every package". It targets the packages
// where determinism-critical float64 math is concentrated, so a contraction is
// caught here at its exact site. internal/window's float64 FMAs are exact
// power-of-two multiplies and value-neutral; the decoder (internal/tx,
// internal/tables/cbrt_fixed.go) and test-only or tool code never feed encoder
// output. internal/psy is scoped even though the depguard rule in .golangci.yaml
// bars it (like internal/coder and internal/enc) from importing math, because it
// combines fmath results in float64 (for example the ATH curve). Code outside the
// scope, including the float64 rate-control and stereo math in internal/coder and
// internal/enc and the *_noasm.go scalar kernels, is backstopped by
// TestEncoderArchDeterminism, which CI runs on both arches, so an architecture
// split there still trips the golden. The per-scope positive control in
// runFMAGuard fails if a scoped package stops appearing, so a typo in a scope
// substring cannot silently drop that package's coverage; a site inlined into a
// non-scoped caller is attributed to the caller and left to the golden. The
// encoder window tables are pinned cross-arch by internal/window's
// TestWindowTablesGolden and the decoder's int32 tables by internal/dec's
// TestIMDCTDump.
//
// The scan reads the whole-module ./... listing and filters to the scoped
// packages, sharing the cold compile with TestNoFloat32FMAContraction.
func TestNoFloat64FMAInDeterministicMath(t *testing.T) {
	fmaGuardPrereqs(t)
	for _, c := range []fmaGuardCase{
		{arch: archARM64, goarch: archARM64, op: fma64ARM, scope: determinismCriticalFloat64Scope, kind: "float64"},
		{arch: archAMD64v3, goarch: "amd64", goamd64: "v3", op: fma64AMD, scope: determinismCriticalFloat64Scope, kind: "float64"},
	} {
		t.Run(c.goarch+c.goamd64, func(t *testing.T) { runFMAGuard(t, &c) })
	}
}
