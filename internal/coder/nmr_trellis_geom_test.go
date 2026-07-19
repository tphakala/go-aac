// SPDX-License-Identifier: LGPL-2.1-or-later
package coder

import "testing"

// TestNMRTrellisGeomExamples pins the four worked geometry examples from the
// SIMD trellis plan (issue #38), including the lamsf indices the kernel
// materialisation walks, so a future change to nmrTrellisGeom or the wrapper's
// index algebra fails here with a readable diff rather than only as a byte
// mismatch in the encoder output.
func TestNMRTrellisGeomExamples(t *testing.T) {
	cases := []struct {
		name                           string
		nCur, nPrev, base, step, mdiff int
		want                           trellisGeom
		// wantIdx maps a kernel position i to its expected lamsf index
		// a[i] = lamsf[base+mdiff+(dHi-i)*step].
		wantIdx map[int]int
	}{
		{
			name: "ex1_step1_11x11_base3",
			nCur: 11, nPrev: 11, base: 3, step: 1, mdiff: 60,
			want:    trellisGeom{dLo: -10, dHi: 10, kernLen: 21, padFront: 10, padBack: 10, basep: 0},
			wantIdx: map[int]int{0: 73, 5: 68, 10: 63, 20: 53},
		},
		{
			name: "ex2_step8_17x17_baseNeg11",
			nCur: 17, nPrev: 17, base: -11, step: 8, mdiff: 60,
			want:    trellisGeom{dLo: -6, dHi: 8, kernLen: 15, padFront: 8, padBack: 6, basep: 0},
			wantIdx: map[int]int{0: 113, 14: 1},
		},
		{
			name: "ex3_edge_step1_11x11_base70",
			nCur: 11, nPrev: 11, base: 70, step: 1, mdiff: 60,
			want:    trellisGeom{dLo: -10, dHi: -10, kernLen: 1, padFront: 0, padBack: 10, basep: 10},
			wantIdx: map[int]int{0: 120},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := nmrTrellisGeom(c.nCur, c.nPrev, c.base, c.step, c.mdiff)
			if g != c.want {
				t.Fatalf("geom = %+v, want %+v", g, c.want)
			}
			for i, want := range c.wantIdx {
				if got := c.base + c.mdiff + (g.dHi-i)*c.step; got != want {
					t.Errorf("a[%d] lamsf index = %d, want %d", i, got, want)
				}
			}
		})
	}

	// Globally empty window (base far past mdiff): every row takes the
	// sentinel path, signalled by kernLen <= 0. The wrapper must not call the
	// primitive in this case.
	if g := nmrTrellisGeom(11, 11, 200, 1, 60); g.kernLen > 0 {
		t.Errorf("globally-empty window: kernLen = %d, want <= 0", g.kernLen)
	}
}

// assertClipExact proves clipping the offset range to [-(nPrev-1), nCur-1]
// leaves every row's genuine [opLo, opHi] window identical to the unclipped
// scalar window, so the shared kernel drops or adds no candidate.
func assertClipExact(t *testing.T, step, mdiff, nCur, nPrev, base int, g trellisGeom) {
	t.Helper()
	loOff := ceilDiv(-mdiff-base, step)
	hiOff := floorDiv(mdiff-base, step)
	for o := range nCur {
		scLo, scHi := max(0, o-hiOff), min(nPrev-1, o-loOff)
		clLo, clHi := max(0, o-g.dHi), min(nPrev-1, o-g.dLo)
		if scLo != clLo || scHi != clHi {
			t.Fatalf("step=%d mdiff=%d nCur=%d nPrev=%d base=%d o=%d: clipped window [%d,%d] != scalar [%d,%d]",
				step, mdiff, nCur, nPrev, base, o, clLo, clHi, scLo, scHi)
		}
	}
}

// assertNoPanicBounds proves every padded-buffer index the primitive reads,
// basep + o + i for o in [0,nCur) and i in [0,kernLen), lies inside
// [0, padFront+nPrev+padBack), and that the geometry fits the fixed NMRState
// scratch, so f32.MinIdxOfSumRows never panics on an out-of-range window.
func assertNoPanicBounds(t *testing.T, step, mdiff, nCur, nPrev, base int, g trellisGeom) {
	t.Helper()
	if g.basep < 0 {
		t.Fatalf("step=%d mdiff=%d nCur=%d nPrev=%d base=%d: basep=%d < 0",
			step, mdiff, nCur, nPrev, base, g.basep)
	}
	lenP := g.padFront + nPrev + g.padBack
	if hi := g.basep + (nCur - 1) + (g.kernLen - 1); nCur > 0 && (hi < 0 || hi >= lenP) {
		t.Fatalf("step=%d mdiff=%d nCur=%d nPrev=%d base=%d: highest P index %d out of [0,%d)",
			step, mdiff, nCur, nPrev, base, hi, lenP)
	}
	if g.kernLen > 2*NMRNCand-1 || lenP > 3*NMRNCand-2 {
		t.Fatalf("step=%d mdiff=%d nCur=%d nPrev=%d base=%d: kernLen=%d lenP=%d exceed scratch capacity",
			step, mdiff, nCur, nPrev, base, g.kernLen, lenP)
	}
}

// TestNMRTrellisGeomClippingExact sweeps the same parameter grid the
// equivalence test uses (step > 0 only, the SIMD path) and proves both the
// clipping-exactness invariant and the no-panic-tightness invariant across it.
func TestNMRTrellisGeomClippingExact(t *testing.T) {
	steps := []int{1, 2, 3, 8}
	mdiffs := []int{0, 1, 7, ScaleMaxDiff}
	ns := []int{0, 1, 2, 11, 16, 17, 96}
	checked := 0
	for _, step := range steps {
		for _, mdiff := range mdiffs {
			for _, nCur := range ns {
				for _, nPrev := range ns {
					for base := -2*ScaleMaxDiff - 3; base <= 2*ScaleMaxDiff+3; base++ {
						g := nmrTrellisGeom(nCur, nPrev, base, step, mdiff)
						if nCur > 0 && nPrev > 0 {
							assertClipExact(t, step, mdiff, nCur, nPrev, base, g)
						}
						if nPrev > 0 && g.kernLen > 0 {
							assertNoPanicBounds(t, step, mdiff, nCur, nPrev, base, g)
						}
						checked++
					}
				}
			}
		}
	}
	t.Logf("%d geometry parameter combinations checked", checked)
}
