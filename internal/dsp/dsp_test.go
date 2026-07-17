// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import (
	"math"
	"testing"
)

func TestAbsPow34(t *testing.T) {
	in := []float32{0, 1, -1, 0.5, -2.25, 100}
	out := make([]float32, len(in))
	AbsPow34(out, in)
	for i, v := range in {
		want := math.Pow(math.Abs(float64(v)), 0.75)
		if math.Abs(float64(out[i])-want) > 1e-5*(want+1e-10) {
			t.Errorf("AbsPow34(%v) = %v, want %v", v, out[i], want)
		}
	}
}

func TestQuantizeBands(t *testing.T) {
	in := []float32{1, -1, 1, 1}
	scaled := []float32{0.9, 0.9, 5.0, 0.2}
	out := make([]int32, 4)
	// Q34 = 1, rounding = 0.4054, maxval = 3:
	// 0.9+0.4054 -> 1; signed negative -> -1; min(5.4054, 3) -> 3; 0.6054 -> 0
	QuantizeBands(out, in, scaled, true, 3, 1, 0.4054)
	want := []int32{1, -1, 3, 0}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("out[%d] = %d, want %d", i, out[i], want[i])
		}
	}
}

// refQuantizeBandsFFMIN transliterates quantize_bands (aacencdsp.c:35-47 @
// d09d5afc3a) with FFMIN written out as the ternary it expands to. It is the
// form QuantizeBands has to stay equivalent to.
func refQuantizeBandsFFMIN(out []int32, in, scaled []float32, isSigned bool, maxval int, q34, rounding float32) {
	for i := range out {
		v := float32(scaled[i]*q34) + rounding
		if m := float32(maxval); v > m {
			v = m
		}
		tmp := int32(v)
		if isSigned && in[i] < 0 {
			tmp = -tmp
		}
		out[i] = tmp
	}
}

// TestQuantizeBandsMatchesFFMIN locks QuantizeBands to the C form. It pins two
// separate things, both of which the C-differential oracle misses because real
// audio never produces the inputs that discriminate:
//
//   - The clamp. QuantizeBands spells it min(), which is not FFMIN in general
//     (see the comment there), so NaN, the infinities, signed zero and a v
//     landing exactly on the bound all have to agree.
//   - The anti-FMA conversion. Both forms wrap the product in float32() to stop
//     Go fusing mul+add. Dropping it from either side alone must fail here, so
//     q34/rounding include a pair that lands on the truncation boundary where
//     fused and split results differ.
//
// Comparing against the reference rather than against literals is required, not
// merely tidy: int32(NaN) is GOARCH-dependent (0 on arm64, MinInt32 on amd64),
// so no literal is writable, and both sides take that hit equally.
func TestQuantizeBandsMatchesFFMIN(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	negZero := float32(math.Copysign(0, -1))
	denormal := float32(math.SmallestNonzeroFloat32)
	scaled := []float32{
		nan, inf, -inf, negZero, 0, denormal,
		7, 7.0000005, 6.9999995, -7, // straddle a real CBMaxval bound
		1e30, -1e30, // overflow to +/-Inf
		9.999999, // with q34=0.05, rounding=0.5: fused and split disagree
	}
	got := make([]int32, len(scaled))
	want := make([]int32, len(scaled))
	in := make([]float32, len(scaled))
	fails := 0
	// The distinct values of tables.CBMaxval. Referencing it directly would be
	// an import cycle (coder -> dsp). maxval=7 matters beyond coverage: it is
	// the only one whose bits escape a quiet NaN's, so on amd64 min()'s POR
	// leaves a different payload than the ternary does. int32() hides it.
	for _, maxval := range []int{0, 1, 2, 4, 7, 12, 16} {
		for _, q34 := range []float32{1, 0.37, 0.05, 1e30} {
			// A -0.0 rounding is what lets v itself reach -0.0.
			for _, rounding := range []float32{0, 0.4054, 0.5, negZero} {
				for _, isSigned := range []bool{true, false} {
					for _, sign := range []float32{1, -1} {
						for i := range in {
							in[i] = sign
						}
						QuantizeBands(got, in, scaled, isSigned, maxval, q34, rounding)
						refQuantizeBandsFFMIN(want, in, scaled, isSigned, maxval, q34, rounding)
						for i := range want {
							if got[i] == want[i] {
								continue
							}
							fails++
							if fails > 20 {
								t.Fatalf("more than 20 mismatches, stopping")
							}
							t.Errorf("i=%d scaled=%v maxval=%d q34=%v rounding=%v isSigned=%v in=%v: got %d, want %d",
								i, scaled[i], maxval, q34, rounding, isSigned, sign, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

func TestLCGSequence(t *testing.T) {
	// First five values from the encoder seed 0x1f2e3d4c, computed during
	// planning from lcg_random (aacenc_utils.h @ d09d5afc3a).
	want := []int32{983586875, -1216483234, 885496869, -1301962944, 1447791007}
	l := LCGSeed
	for i, w := range want {
		if got := l.Next(); got != w {
			t.Fatalf("Next() #%d = %d, want %d", i, got, w)
		}
	}
}

func TestVectorFMulReverse(t *testing.T) {
	dst := make([]float32, 3)
	VectorFMulReverse(dst, []float32{1, 2, 3}, []float32{10, 20, 30})
	for i, w := range []float32{30, 40, 30} {
		if dst[i] != w {
			t.Errorf("dst[%d] = %v, want %v", i, dst[i], w)
		}
	}
}
