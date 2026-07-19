// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build goaac_simd

package coder

import (
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/simd/f32"
)

// nmrTrellisStep is the goaac_simd dispatch: it maps one Viterbi step onto
// f32.MinIdxOfSumRows (NEON on arm64, AVX2 on amd64, bit-identical pure-Go
// fallback elsewhere) and reconciles the scalar's MaxFloat32 sentinel
// byte-for-byte. Its output is identical to nmrTrellisStepScalar; the geometry
// helper nmrTrellisGeom (nmr_trellis.go) and the plan for issue #38 carry the
// correctness proof, and TestNMRTrellisStepMatchesReference gates it bitwise.
//
// The mapping uses k = the +Inf-padded dpp signal with slide = +1, so the
// transition offset d = o-op depends only on the tap index and one kernel
// a[i] = lamsf[base+mdiff+(dHi-i)*step] serves every row (step only decimates
// which lamsf entries land in a[]). step <= 0 (which nmrSolve never passes, but
// the equivalence sweep does) has no slide-+1 form and delegates to the scalar.
//
// dpp must not contain NaN: the primitive keeps a leading NaN candidate as the
// incumbent whereas the scalar skips NaN, so they would diverge. nmrSolve never
// produces NaN (every cost is a finite sum, the MaxFloat32 sentinel, or +Inf).
func nmrTrellisStep(dp []float32, bp []uint8, dpp, node, lamsf []float32,
	nCur, nPrev, base, step, mdiff int, ts *nmrTrellisScratch) {
	if step <= 0 {
		nmrTrellisStepScalar(dp, bp, dpp, node, lamsf, nCur, nPrev, base, step, mdiff)
		return
	}
	if nCur <= 0 {
		return
	}
	dp, bp, node = dp[:nCur], bp[:nCur], node[:nCur]

	g := nmrTrellisGeom(nCur, nPrev, base, step, mdiff)
	if nPrev <= 0 || g.kernLen <= 0 {
		// No row has a genuine candidate: write the sentinel for all rows,
		// exactly as the scalar's empty-window branch does.
		for o := range dp {
			bp[o] = 0
			dp[o] = fmath.MaxFloat32
		}
		return
	}
	dpp = dpp[:nPrev]

	// Materialise the shared transition kernel a[i] = lamsf[base+mdiff+(dHi-i)*step].
	// Walked with a descending lamsf index so op ascends with i, preserving the
	// scalar's lowest-op-wins tie-break.
	a := ts.kern[:g.kernLen]
	idx := base + mdiff + g.dHi*step
	for i := range a {
		a[i] = lamsf[idx]
		idx -= step
	}

	// Build the +Inf-padded dpp signal k: fill the whole window with +Inf, then
	// drop dpp into the middle. Every cell the primitive reads is written here,
	// so the struct-zeroed scratch (NMRState reset) can never leak a stale 0
	// that would win an argmin.
	inf := fmath.Inf32()
	p := ts.padk[:g.padFront+nPrev+g.padBack]
	for i := range p {
		p[i] = inf
	}
	copy(p[g.padFront:g.padFront+nPrev], dpp)

	vals, idxs := ts.vals[:nCur], ts.idxs[:nCur]
	f32.MinIdxOfSumRows(vals, idxs, a, p, g.basep, 1)

	// Sentinel reconciliation: a winner exists only when the row's minimum
	// beats the MaxFloat32 incumbent (strict, exactly the scalar's test). A
	// dead window (all MaxFloat32/+Inf) yields vals[o] >= MaxFloat32 and writes
	// the raw sentinel, never node[o]+vals[o] (which would overflow to +Inf).
	for o := range dp {
		if vals[o] < fmath.MaxFloat32 {
			bp[o] = uint8(o - g.dHi + int(idxs[o]))
			dp[o] = node[o] + vals[o]
		} else {
			bp[o] = 0
			dp[o] = fmath.MaxFloat32
		}
	}
}
