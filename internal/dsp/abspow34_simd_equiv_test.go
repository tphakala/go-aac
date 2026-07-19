// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build goaac_simd

package dsp

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

// This file gates the goaac_simd AbsPow34 bitwise against the canonical
// absPow34Scalar in dsp.go. It is tagged goaac_simd, unlike
// nmr_trellis_equiv_test.go, on purpose: the trellis reference is an independent
// C transcription meaningful on both builds, but our reference IS the scalar, so
// on the default build the exported wrapper IS the scalar and an untagged equiv
// test would compare a function against its own body, a tautology.
//
// All inputs are finite. NaN and the infinities are the classes where the SIMD
// primitive legitimately diverges from the scalar (the NaN payload bits are not
// guaranteed identical across backends), and the encoder never produces them: it
// rejects non-finite PCM (#18).

// absPow34Sizes span the NEON 4-lane and AVX 8-lane widths with their
// off-by-one edges, plus 20 (a multiple of 4 leaving an AVX 8-lane remainder of
// 4, the scalar tail every real scalefactor-band width actually hits, since all
// SwbSize entries are multiples of 4), the widest scalefactor band 96, and the
// full 1024-line frame.
var absPow34Sizes = []int{0, 1, 2, 7, 8, 15, 16, 17, 20, 96, 1024}

// negZero32 is -0.0: its sign bit is set but the arithmetic predicate a < 0 is
// false, so both the scalar and the primitive must emit +0.0 for it.
var negZero32 = float32(math.Copysign(0, -1))

// absPow34Pins are the edge inputs pinned in the low indices of every swept
// slice: signed zeros (a -0.0 must still emit +0.0 through both forms),
// power-of-two straddles both signs, denormals that underflow the intermediate
// to 0, tiny and near-1 magnitudes, and magnitudes large enough that the float32
// intermediate |x|*sqrt(|x|) overflows to +Inf on both forms.
var absPow34Pins = []float32{
	0, negZero32,
	1, -1, 2, -2, 4, -4, 0.5, -0.5,
	float32(math.SmallestNonzeroFloat32), -float32(math.SmallestNonzeroFloat32),
	1e-20, -1e-20,
	0.9999999, 1.0000001,
	1e30, -1e30,
	math.MaxFloat32, -math.MaxFloat32,
}

// randFinite32 draws a finite float32 across a wide dynamic range and both signs.
// Never NaN or Inf: those are the divergence classes the encoder never produces.
func randFinite32(rng *rand.Rand) float32 {
	mant := rng.Float64()*2 - 1 // [-1, 1)
	exp := rng.Intn(100) - 50   // 2^-50 .. 2^49
	return float32(mant * math.Ldexp(1, exp))
}

// buildAbsPow34Input returns a length-n slice with absPow34Pins in the low
// indices and a seeded finite spread filling the rest.
func buildAbsPow34Input(n int, rng *rand.Rand) []float32 {
	in := make([]float32, n)
	for i := range in {
		if i < len(absPow34Pins) {
			in[i] = absPow34Pins[i]
		} else {
			in[i] = randFinite32(rng)
		}
	}
	return in
}

// TestAbsPow34MatchesScalar asserts the goaac_simd AbsPow34 is bit-identical to
// absPow34Scalar across the size sweep, comparing raw float32 bits so a differing
// NaN payload or signed zero would fail (neither occurs on finite inputs; the
// pins cover the signed-zero and overflow edges).
func TestAbsPow34MatchesScalar(t *testing.T) {
	for _, n := range absPow34Sizes {
		rng := rand.New(rand.NewSource(int64(n) + 100))
		in := buildAbsPow34Input(n, rng)
		inBefore := slices.Clone(in)
		got := make([]float32, n)
		want := make([]float32, n)
		AbsPow34(got, in)
		// AbsPow34 reads in and writes only out; a kernel that mutated its
		// source would corrupt the encoder's coefficients (in is sce.Coeffs at
		// the call sites). Check it before absPow34Scalar reads in below, or a
		// mutation would corrupt want too and hide in the got==want compare.
		for i := 0; i < n; i++ {
			if math.Float32bits(in[i]) != math.Float32bits(inBefore[i]) {
				t.Fatalf("n=%d: AbsPow34 mutated its input at i=%d: %#x -> %#x",
					n, i, math.Float32bits(inBefore[i]), math.Float32bits(in[i]))
			}
		}
		absPow34Scalar(want, in)
		for i := 0; i < n; i++ {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("n=%d i=%d in=%v: got %v (%#x), want %v (%#x)",
					n, i, in[i], got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
			}
		}
	}
}

// TestAbsPow34AllocFree pins the 0-alloc invariant at the kernel level: the f32
// primitive allocates nothing. The encoder-level allocs/frame gate
// (encoder_api_test.go) runs under both tags in CI too; this attributes the
// invariant to the kernel itself.
func TestAbsPow34AllocFree(t *testing.T) {
	for _, n := range []int{96, 1024} {
		rng := rand.New(rand.NewSource(int64(n)))
		in := buildAbsPow34Input(n, rng)
		out := make([]float32, n)
		if a := testing.AllocsPerRun(100, func() { AbsPow34(out, in) }); a != 0 {
			t.Errorf("AbsPow34 n=%d allocated %v/op, want 0", n, a)
		}
	}
}

// modRange maps an arbitrary (possibly negative) fuzz int into [0, n].
func modRange(v, n int) int {
	m := v % (n + 1)
	if m < 0 {
		m += n + 1
	}
	return m
}

// FuzzAbsPow34Equiv drives the size the fixed grid only samples and asserts the
// dispatched AbsPow34 stays bit-identical to absPow34Scalar, so CI grows one
// fuzz step like FuzzNMRTrellisStep. Finite inputs only, for the reason in the
// file comment.
func FuzzAbsPow34Equiv(f *testing.F) {
	f.Add(0, int64(1))
	f.Add(1, int64(2))
	f.Add(8, int64(3))
	f.Add(96, int64(4))
	f.Add(1024, int64(5))
	f.Fuzz(func(t *testing.T, nArg int, seed int64) {
		n := modRange(nArg, 1024)
		rng := rand.New(rand.NewSource(seed))
		in := buildAbsPow34Input(n, rng)
		got := make([]float32, n)
		want := make([]float32, n)
		AbsPow34(got, in)
		absPow34Scalar(want, in)
		for i := 0; i < n; i++ {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("n=%d i=%d in=%v: got %#x, want %#x",
					n, i, in[i], math.Float32bits(got[i]), math.Float32bits(want[i]))
			}
		}
	})
}
