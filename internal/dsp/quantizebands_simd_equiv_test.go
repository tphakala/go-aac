// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build goaac_simd

package dsp

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

// This file gates the goaac_simd QuantizeBands bitwise against the canonical
// quantizeBandsScalar in dsp.go, on the finite, non-negative-magnitude domain
// the encoder actually produces. Like abspow34_simd_equiv_test.go it is tagged
// goaac_simd on purpose: our reference IS the scalar, so on the default build the
// exported wrapper IS the scalar and an untagged equiv test would compare a
// function against its own body, a tautology.
//
// Input domain, and why it is what it is:
//   - scaled >= 0 and finite. scaled is AbsPow34 output at every call site
//     (quantize.go), so it is a non-negative magnitude, never NaN or Inf. A
//     negative magnitude would hit the kernel's minV=0 floor the scalar lacks,
//     and a NaN would convert arch-dependently; the encoder produces neither: the
//     magnitude is non-negative because scaled is AbsPow34 output, and finite
//     because the encoder rejects non-finite PCM (#18).
//     TestQuantizeBandsMatchesFFMIN separately pins the scalar itself against the
//     C form over those out-of-domain classes.
//   - in is finite, both signs. It supplies only the sign.
//   - rounding in [0, 1). The encoder uses RoundStandard 0.4054 and RoundToZero
//     0.1054 (coder/types.go). This bound is load-bearing: the one input where
//     copysign (kernel) and "in[i] < 0" (scalar) genuinely diverge is a -0.0
//     sign on a zero magnitude that a rounding >= 1 would quantize to a nonzero
//     value. Below 1 a zero magnitude stays int32 0, where the sign is inert, so
//     the i=0 pin (scaled 0, in -0.0) is identical through both forms.
//
// The size sweep (absPow34Sizes) and randFinite32 / modRange helpers are shared
// with abspow34_simd_equiv_test.go (same package, same tag).

// quantizeBandsMaxvals are the distinct values of tables.CBMaxval (0 to 16).
// Referencing the table directly would be an import cycle (coder -> dsp), so the
// set is listed literally, as dsp_test.go already does. maxval 0 is the zero
// codebook: both forms must emit all zeros.
var quantizeBandsMaxvals = []int{0, 1, 2, 4, 7, 12, 16}

// quantizeBandsQ34 spans a unit scale, two sub-unit scales that land values off
// the truncation boundary, and 1e30 to force the product to overflow to +Inf on
// the large-magnitude pin (scalar min(+Inf, m) = m; kernel +Inf -> maxV; the
// int32 convert collapses both to maxval).
var quantizeBandsQ34 = []float32{1, 0.37, 0.05, 1e30}

// quantizeBandsRoundings stay inside [0, 1): the encoder's two constants, the
// endpoints (the largest kept just under 1 so a zero magnitude cannot round up,
// see the -0.0 note in the file comment), and 0.5, which paired with the 9.999999
// scaled pin at q34=0.05 is the triple that discriminates an FMA (see the pin
// comment).
var quantizeBandsRoundings = []float32{0, 0.1054, 0.4054, 0.5, 0.9999999}

// quantizeBandsScaledPins are non-negative magnitude edges in the low indices of
// every swept scaled slice: two zeros (paired with the -0.0 and +0.0 sign pins),
// a denormal that underflows the product, values straddling the CBMaxval=7 bound
// at q34=1, MaxFloat32 which overflows the product to +Inf at q34=1e30, and
// 9.999999 which at q34=0.05, rounding=0.5 lands the split-rounded product one ULP
// off the fused (FMA) result (split -> int32 1, fused -> 0), so an FMA in the
// kernel flips the output and TestQuantizeBandsMatchesScalar catches it. Both the
// scalar's explicit float32() cast and the primitive's no-FMA contract round the
// product before the add, so on the real kernels they agree.
var quantizeBandsScaledPins = []float32{
	0, 0,
	float32(math.SmallestNonzeroFloat32),
	6.9999995, 7, 7.0000005,
	0.5, 1, 2.5,
	math.MaxFloat32,
	9.999999,
}

// quantizeBandsSignPins are the sign sources aligned with the scaled pins. Index
// 0 is -0.0 on a zero magnitude, the case where copysign and "in[i] < 0" differ
// in isolation but not in result; index 1 is +0.0; the rest exercise both signs.
// The last entry pairs a positive sign with the 9.999999 FMA-discriminating pin.
var quantizeBandsSignPins = []float32{
	negZero32, 0,
	-1,
	1, -1, 1,
	-1, 1, -1,
	-1,
	1,
}

// buildQuantizeBandsInputs returns length-n scaled (non-negative) and sign slices
// with the pins in the low indices and a seeded finite spread filling the rest.
func buildQuantizeBandsInputs(n int, rng *rand.Rand) (scaled, sign []float32) {
	scaled = make([]float32, n)
	sign = make([]float32, n)
	for i := range scaled {
		if i < len(quantizeBandsScaledPins) {
			scaled[i] = quantizeBandsScaledPins[i]
			sign[i] = quantizeBandsSignPins[i]
			continue
		}
		// abs() keeps scaled a non-negative magnitude, as AbsPow34 output is.
		v := randFinite32(rng)
		scaled[i] = float32(math.Abs(float64(v)))
		sign[i] = randFinite32(rng)
	}
	return scaled, sign
}

// TestQuantizeBandsMatchesScalar asserts the goaac_simd QuantizeBands is
// bit-identical to quantizeBandsScalar across the size sweep and the full
// parameter grid, for both isSigned values, comparing int32 outputs exactly.
func TestQuantizeBandsMatchesScalar(t *testing.T) {
	for _, n := range absPow34Sizes {
		rng := rand.New(rand.NewSource(int64(n) + 200))
		scaled, sign := buildQuantizeBandsInputs(n, rng)
		scaledBefore := slices.Clone(scaled)
		signBefore := slices.Clone(sign)
		got := make([]int32, n)
		want := make([]int32, n)
		for _, isSigned := range []bool{true, false} {
			for _, maxval := range quantizeBandsMaxvals {
				for _, q34 := range quantizeBandsQ34 {
					for _, rounding := range quantizeBandsRoundings {
						QuantizeBands(got, sign, scaled, isSigned, maxval, q34, rounding)
						// QuantizeBands reads in and scaled and writes only out; a
						// kernel that mutated a source would corrupt the encoder's
						// coefficients. Check before the scalar reads them below.
						for i := range n {
							if math.Float32bits(scaled[i]) != math.Float32bits(scaledBefore[i]) {
								t.Fatalf("n=%d: QuantizeBands mutated scaled at i=%d", n, i)
							}
							if math.Float32bits(sign[i]) != math.Float32bits(signBefore[i]) {
								t.Fatalf("n=%d: QuantizeBands mutated in at i=%d", n, i)
							}
						}
						quantizeBandsScalar(want, sign, scaled, isSigned, maxval, q34, rounding)
						for i := range n {
							if got[i] != want[i] {
								t.Fatalf("n=%d i=%d scaled=%v in=%v isSigned=%v maxval=%d q34=%v rounding=%v: got %d, want %d",
									n, i, scaled[i], sign[i], isSigned, maxval, q34, rounding, got[i], want[i])
							}
						}
					}
				}
			}
		}
	}
}

// TestQuantizeBandsAllocFree pins the 0-alloc invariant at the kernel level for
// both the signed and unsigned dispatch, mirroring TestAbsPow34AllocFree.
func TestQuantizeBandsAllocFree(t *testing.T) {
	for _, n := range []int{96, 1024} {
		rng := rand.New(rand.NewSource(int64(n)))
		scaled, sign := buildQuantizeBandsInputs(n, rng)
		out := make([]int32, n)
		for _, isSigned := range []bool{true, false} {
			a := testing.AllocsPerRun(100, func() {
				QuantizeBands(out, sign, scaled, isSigned, 12, 0.37, 0.4054)
			})
			if a != 0 {
				t.Errorf("QuantizeBands n=%d isSigned=%v allocated %v/op, want 0", n, isSigned, a)
			}
		}
	}
}

// FuzzQuantizeBandsEquiv drives sizes and parameters the fixed grid only samples
// and asserts the dispatched QuantizeBands stays bit-identical to
// quantizeBandsScalar, growing CI one fuzz step like FuzzAbsPow34Equiv. Inputs
// are kept inside the documented domain: scaled non-negative, rounding in [0, 1).
func FuzzQuantizeBandsEquiv(f *testing.F) {
	f.Add(0, int64(1), true, 0, 0)
	f.Add(8, int64(2), false, 3, -4)
	f.Add(96, int64(3), true, 4, 2)
	f.Add(1024, int64(4), false, 6, 5)
	f.Fuzz(func(t *testing.T, nArg int, seed int64, isSigned bool, maxvalIdx, q34Exp int) {
		n := modRange(nArg, 1024)
		rng := rand.New(rand.NewSource(seed))
		scaled, sign := buildQuantizeBandsInputs(n, rng)
		maxval := quantizeBandsMaxvals[modRange(maxvalIdx, len(quantizeBandsMaxvals)-1)]
		// q34 > 0 across a wide exponent range; rounding drawn deterministically
		// inside [0, 1) from the seed so the -0.0 divergence never opens.
		q34 := float32(math.Ldexp(1, (q34Exp%40)-20))
		rounding := rng.Float32() // [0, 1)
		got := make([]int32, n)
		want := make([]int32, n)
		QuantizeBands(got, sign, scaled, isSigned, maxval, q34, rounding)
		quantizeBandsScalar(want, sign, scaled, isSigned, maxval, q34, rounding)
		for i := range n {
			if got[i] != want[i] {
				t.Fatalf("n=%d i=%d scaled=%v in=%v isSigned=%v maxval=%d q34=%v rounding=%v: got %d, want %d",
					n, i, scaled[i], sign[i], isSigned, maxval, q34, rounding, got[i], want[i])
			}
		}
	})
}
