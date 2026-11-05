// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

// Under -tags noasm both sides of every comparison below are the scalar loop,
// so these tests are built only where a kernel can be compiled in, as
// internal/dsp/*_simd_equiv_test.go are.

package coder

import (
	"math"
	"math/rand"
	"runtime"
	"testing"

	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/simd/cpu"
)

// bandCostRounding returns the rounding mode of a test case.
func bandCostRounding(roundToZero bool) float32 {
	if roundToZero {
		return RoundToZero
	}
	return RoundStandard
}

// fillBandCostInput fills in with a seeded spectrum that covers every branch
// of the band-cost loop: exact zeros and negative zeros (no sign bit), small
// values (low codebook indices), and values up to 1e4 times the band scale,
// which push codebook 11 into escapes and past clippedEscape at the low
// scalefactors.
func fillBandCostInput(in []float32, rng *rand.Rand) {
	scale := float32(math.Pow(10, float64(rng.Intn(9))-2))
	for i := range in {
		v := float32(rng.NormFloat64()) * scale
		switch rng.Intn(12) {
		case 0:
			v = 0
		case 1:
			v = float32(math.Copysign(0, -1))
		case 2:
			v *= 1e4
		}
		in[i] = v
	}
}

// checkBandCost runs one cost-only quantizeAndEncodeBandCost call (out == nil,
// which takes bandCostAsm where the CPU has it) against the same call with
// a dequantized-output buffer (out != nil, which always runs the scalar loop
// and leaves cost, bits and energy unchanged) and requires bit-identical cost,
// bit count, quantized energy and early exit.
func checkBandCost(t *testing.T, c *Coder, in []float32, scaleIdx, cb int, lambda, uplim, rounding float32) {
	t.Helper()
	dequant := make([]float32, len(in))
	gotBits, wantBits := -1, -1
	gotEnergy, wantEnergy := float32(-1), float32(-1)
	got := c.quantizeAndEncodeBandCost(nil, in, nil, nil, scaleIdx, cb, lambda, uplim, &gotBits, &gotEnergy, rounding)
	want := c.quantizeAndEncodeBandCost(nil, in, dequant, nil, scaleIdx, cb, lambda, uplim, &wantBits, &wantEnergy, rounding)
	if math.Float32bits(got) != math.Float32bits(want) || gotBits != wantBits || math.Float32bits(gotEnergy) != math.Float32bits(wantEnergy) {
		t.Fatalf("cb %d sf %d size %d lambda %v uplim %v rounding %v: got (%v, %d, %v) want (%v, %d, %v)",
			cb, scaleIdx, len(in), lambda, uplim, rounding, got, gotBits, gotEnergy, want, wantBits, wantEnergy)
	}
}

// TestBandCostKernelMatchesScalar sweeps every codebook, scalefactor, both
// rounding modes, four lambdas and both an infinite and a finite uplim (the
// finite one exercises the early exit) over band widths 4..96, and requires the
// band-cost kernel to match the scalar loop bit for bit. Band widths below
// bandCostMinSize take the scalar loop, so the sweep covers both sides of the
// dispatch on arm64.
func TestBandCostKernelMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(20261105))
	var c Coder
	in := make([]float32, 96)
	for trial := range 3000 {
		size := 4 * (1 + rng.Intn(24))
		fillBandCostInput(in[:size], rng)
		rounding := bandCostRounding(trial%3 == 0)
		uplim := fmath.Inf32()
		if trial%5 == 0 {
			uplim = float32(rng.Intn(400))
		}
		checkBandCost(t, &c, in[:size], rng.Intn(ScaleMaxPos+1), 1+rng.Intn(11), []float32{0, 1, 0.3, 7.5}[trial%4], uplim, rounding)
	}
}

// FuzzBandCostEquiv drives codebook, scalefactor, band width, lambda, uplim
// and rounding beyond the fixed sweep, with the spectrum drawn from a seeded
// PRNG so every payload is finite. The seeds are pre-images of the
// normalization below, so each lands on the codebook, scalefactor and band
// width its comment names.
func FuzzBandCostEquiv(f *testing.F) {
	f.Add(10, 120, 22, float32(1), float32(-1), false, int64(1))  // cb 11, sf 120, 96 wide: escapes, clipped escapes, zeros
	f.Add(10, 100, 0, float32(1), float32(30), true, int64(2))    // cb 11, sf 100, 8 wide: escapes, early exit, round to zero
	f.Add(0, 120, 6, float32(7.5), float32(-1), false, int64(3))  // cb 1, sf 120, 32 wide: signed quads
	f.Add(2, 100, 2, float32(0.3), float32(-1), true, int64(5))   // cb 3, sf 100, 16 wide: unsigned quads
	f.Add(4, 60, 10, float32(0.3), float32(100), false, int64(4)) // cb 5, sf 60, 48 wide: signed pairs, early exit
	f.Add(8, 120, 14, float32(1), float32(-1), false, int64(6))   // cb 9, sf 120, 64 wide: unsigned pairs
	f.Fuzz(func(t *testing.T, cbR, scaleR, sizeR int, lambda, uplimR float32, rtz bool, seed int64) {
		if math.IsNaN(float64(lambda)) || math.IsInf(float64(lambda), 0) || math.IsNaN(float64(uplimR)) {
			return
		}
		cb := 1 + modRange(cbR, 10)
		scaleIdx := modRange(scaleR, ScaleMaxPos)
		size := 4 * (2 + modRange(sizeR, 22))
		uplim := uplimR
		if uplim < 0 {
			uplim = fmath.Inf32()
		}
		rounding := bandCostRounding(rtz)
		in := make([]float32, size)
		fillBandCostInput(in, rand.New(rand.NewSource(seed)))
		var c Coder
		checkBandCost(t, &c, in, scaleIdx, cb, lambda, uplim, rounding)
	})
}

// TestBandCostKernelEscapeBoundaries pins codebook 11 coefficients at and next
// to clippedEscape (165140 * iq) and across the 8191 escape clamp, values the
// seeded sweep almost never produces. A kernel that compares t > clippedEscape
// instead of t >= clippedEscape, or clamps the escape index at anything but
// 8191, fails here.
func TestBandCostKernelEscapeBoundaries(t *testing.T) {
	var c Coder
	in := make([]float32, 16)
	for scaleIdx := 60; scaleIdx <= ScaleMaxPos; scaleIdx += 7 {
		// iq and clippedEscape repeat quantizeAndEncodeBandCost's own
		// expressions (quantize.go: iq := tables.Pow2SF[...], clippedEscape :=
		// 165140.0 * iq); keep them in step.
		iq := tables.Pow2SF[tables.PowSF2Zero+scaleIdx-ScaleOnePos+ScaleDiv512]
		clipped := float32(165140.0 * iq)
		below := math.Nextafter32(clipped, 0)
		above := math.Nextafter32(clipped, fmath.Inf32())
		// Magnitudes whose escape index lands on 8190 and 8191 (the table
		// entries the loop dequantizes) and on 8192 (a=165135 in the scaled
		// domain, inside [8191.5946^(4/3), 165140) so it stays below
		// clippedEscape yet quantizes past the 8191 clamp with standard
		// rounding). Indices above 8192 are unreachable below clippedEscape.
		escapes := []float32{
			tables.Pow43[8190] * iq,
			tables.Pow43[8191] * iq,
			165135 * iq,
			165129.6 * iq,
		}
		// Fail loudly if the magnitudes stop landing where this comment says.
		qs := tables.Pow2SF[tables.PowSF2Zero-scaleIdx+ScaleOnePos-ScaleDiv512]
		for i, want := range []int{8190, 8191, 8192, 8192} {
			if got := quant(escapes[i], qs, RoundStandard); got != want || escapes[i] >= clipped {
				t.Fatalf("sf %d: escapes[%d] quantizes to %d (clipped %v), want %d below clippedEscape", scaleIdx, i, got, escapes[i] >= clipped, want)
			}
		}
		for i := range in {
			var v float32
			switch i % 4 {
			case 0:
				v = clipped
			case 1:
				v = below
			case 2:
				v = above
			default:
				v = escapes[(i/4)%len(escapes)]
			}
			if i%2 == 1 {
				v = -v
			}
			in[i] = v
		}
		for _, rtz := range []bool{false, true} {
			checkBandCost(t, &c, in, scaleIdx, 11, 1, fmath.Inf32(), bandCostRounding(rtz))
		}
	}
}

// TestBandCostKernelUplimEquality sets uplim to the exact cost the scalar loop
// reaches after the last group, so the early exit (cost >= uplim) fires on the
// final group. A kernel that tests cost > uplim runs to completion instead and
// reports bits and energy the scalar loop never writes.
func TestBandCostKernelUplimEquality(t *testing.T) {
	rng := rand.New(rand.NewSource(20261106))
	var c Coder
	in := make([]float32, 96)
	dequant := make([]float32, 96)
	for trial := range 600 {
		size := 8 + 4*rng.Intn(23)
		fillBandCostInput(in[:size], rng)
		cb := 1 + rng.Intn(11)
		scaleIdx := rng.Intn(ScaleMaxPos + 1)
		lambda := []float32{1, 0.3, 7.5}[trial%3]
		full := c.quantizeAndEncodeBandCost(nil, in[:size], dequant[:size], nil, scaleIdx, cb, lambda, fmath.Inf32(), nil, nil, RoundStandard)
		checkBandCost(t, &c, in[:size], scaleIdx, cb, lambda, full, RoundStandard)
	}
}

// TestBandCostKernelRuns asserts bandCostKernelOK is set wherever a kernel is
// compiled in and the CPU has it (AVX2 and POPCNT on amd64, NEON on arm64,
// never under SIMD_DISABLE), so the equivalence tests above are not comparing
// the scalar loop with itself on a host that should run the kernel.
func TestBandCostKernelRuns(t *testing.T) {
	want := ((runtime.GOARCH == "amd64" && cpu.X86.AVX2 && cpu.X86.POPCNT) || (runtime.GOARCH == "arm64" && cpu.ARM64.NEON))
	if bandCostKernelOK != want {
		t.Fatalf("bandCostKernelOK = %v, want %v (GOARCH %s)", bandCostKernelOK, want, runtime.GOARCH)
	}
}
