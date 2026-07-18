// SPDX-License-Identifier: LGPL-2.1-or-later

// Package fmath centralizes the math primitives used by encoder code paths
// (docs/go-design.md: only this package imports math in per-frame code). It
// also holds shared scalar helpers (Absf, Clipf, Clipi) that use only Go
// builtins and add no math import, so the package fence stays intact.
package fmath

import "math"

// BesselI0 is the zeroth-order modified Bessel function of the first kind,
// power-series form. Replaces libavutil/mathematics.c:av_bessel_i0's minimax
// rational approximation @ d09d5afc3a. For the KBD window argument range
// (x <= 13) the two agree to about 1e-13, far below float32 window
// resolution, so generated windows match FFmpeg's at float32.
// The series terminates on term < 1e-17*sum, a comparison that is false for
// NaN and for Inf/Inf, so the guards below are load-bearing: without them a
// NaN or overflowing argument spins forever. I0 overflows float64 near
// x = 713, and I0 is even, so folding the sign and saturating above 700 is
// exact for every argument the caller can reach. KBD window generation passes
// x <= 13, so none of these paths are taken in practice.
func BesselI0(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return math.NaN()
	case math.IsInf(x, 0):
		return math.Inf(1)
	}
	if x < 0 {
		x = -x // I0 is even
	}
	if x > 700 {
		return math.Inf(1)
	}
	sum, term := 1.0, 1.0
	for k := 1; ; k++ {
		h := x / (2 * float64(k))
		term *= h * h
		sum += term
		if term < 1e-17*sum {
			return sum
		}
	}
}

// Sqrt32 is float32 square root.
func Sqrt32(x float32) float32 { return float32(math.Sqrt(float64(x))) }

// Cbrt32 is float32 cube root, replacing C cbrtf. Computed in float64 and
// rounded once to float32; may differ from cbrtf by 1 ulp, which only feeds
// distortion costs, never emitted bits.
func Cbrt32(x float32) float32 { return float32(math.Cbrt(float64(x))) }

// Log232 is float32 base-2 logarithm, replacing C log2f. Computed in
// float64 and rounded once to float32.
func Log232(x float32) float32 { return float32(math.Log2(float64(x))) }

// Inf32 is the positive float32 infinity.
func Inf32() float32 { return float32(math.Inf(1)) }

// Atan32 is float32 arctangent, replacing C atanf. Computed in float64 and
// rounded once to float32.
func Atan32(x float32) float32 { return float32(math.Atan(float64(x))) }

// Exp232 is float32 base-2 exponential, replacing C exp2f. Computed in
// float64 and rounded once to float32.
func Exp232(x float32) float32 { return float32(math.Exp2(float64(x))) }

// NaN32 is a float32 quiet NaN, replacing C NAN.
func NaN32() float32 { return float32(math.NaN()) }

// mLog210 mirrors M_LOG2_10 (libavutil/mathematics.h @ d09d5afc3a).
const mLog210 = 3.32192809488736234787031942948939

// Exp10 computes 10**x exactly like libavutil/ffmath.h:ff_exp10
// @ d09d5afc3a: exp2(M_LOG2_10 * x). Used by the psy model init.
func Exp10(x float64) float64 { return math.Exp2(mLog210 * x) }

// Pow is a float64 pass-through for init-time code outside the depguard
// math fence (the psy ATH curve runs once per encoder init).
func Pow(x, y float64) float64 { return math.Pow(x, y) }

// Exp is a float64 pass-through for init-time code (psy ATH curve).
func Exp(x float64) float64 { return math.Exp(x) }

// Exp2 is a float64 pass-through for init-time code (psy min_snr).
func Exp2(x float64) float64 { return math.Exp2(x) }

// Exp32 is float32 natural exponential, replacing C expf. Computed in
// float64 and rounded once to float32.
func Exp32(x float32) float32 { return float32(math.Exp(float64(x))) }

// Log32 is float32 natural logarithm, replacing C logf. Computed in
// float64 and rounded once to float32.
func Log32(x float32) float32 { return float32(math.Log(float64(x))) }

// Pow32 is float32 power, replacing C powf. Computed in float64 and
// rounded once to float32.
func Pow32(x, y float32) float32 { return float32(math.Pow(float64(x), float64(y))) }

// Ceil32 is float32 ceiling, replacing C ceilf.
func Ceil32(x float32) float32 { return float32(math.Ceil(float64(x))) }

// Round32 is float32 round-half-away-from-zero, replacing C roundf.
func Round32(x float32) float32 { return float32(math.Round(float64(x))) }

// MaxFloat32 mirrors C FLT_MAX.
const MaxFloat32 = math.MaxFloat32

// Sqrt64 is float64 square root, replacing C double sqrt where the C
// promotes float operands (the intensity stereo downmix scale).
func Sqrt64(x float64) float64 { return math.Sqrt(x) }

// Absf is the float32 absolute value, expressed as max(x, -x) so it inlines
// with no math import. It propagates NaN: max(NaN, -NaN) is NaN, so
// Absf(NaN) is NaN. It also normalizes -0.0 to +0.0.
func Absf(x float32) float32 { return max(x, -x) }

// Clipf clamps v to the inclusive range [lo, hi] for float32 operands.
func Clipf(v, lo, hi float32) float32 { return min(max(v, lo), hi) }

// Clipi clamps v to the inclusive range [lo, hi] for int operands.
func Clipi(v, lo, hi int) int { return min(max(v, lo), hi) }
