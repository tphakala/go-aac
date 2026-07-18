// SPDX-License-Identifier: LGPL-2.1-or-later
package fmath

import (
	"math"
	"testing"
)

func TestBesselI0(t *testing.T) {
	cases := []struct{ x, want float64 }{
		{0, 1.0},
		{1, 1.266065877752},
		{4, 11.301921952136},
	}
	for _, c := range cases {
		if got := BesselI0(c.x); math.Abs(got-c.want) > 1e-9*c.want {
			t.Errorf("BesselI0(%v) = %.12f, want %.12f", c.x, got, c.want)
		}
	}
}

func TestSqrt32(t *testing.T) {
	if got := Sqrt32(9); got != 3 {
		t.Errorf("Sqrt32(9) = %v, want 3", got)
	}
}

// TestBesselI0Guards pins the guards added after review: without them a NaN or
// overflowing argument spins forever, because the series terminates on
// term < 1e-17*sum, which is false for NaN and for Inf/Inf.
func TestBesselI0Guards(t *testing.T) {
	if got := BesselI0(math.NaN()); !math.IsNaN(got) {
		t.Errorf("BesselI0(NaN) = %v, want NaN", got)
	}
	if got := BesselI0(math.Inf(1)); !math.IsInf(got, 1) {
		t.Errorf("BesselI0(+Inf) = %v, want +Inf", got)
	}
	if got := BesselI0(1e9); !math.IsInf(got, 1) {
		t.Errorf("BesselI0(1e9) = %v, want +Inf (I0 overflows float64 near 713)", got)
	}
	if BesselI0(-4) != BesselI0(4) {
		t.Error("BesselI0 must be even")
	}
}

func TestCbrt32(t *testing.T) {
	if got := Cbrt32(27); got != 3 {
		t.Errorf("Cbrt32(27) = %v, want 3", got)
	}
	if got := Cbrt32(0); got != 0 {
		t.Errorf("Cbrt32(0) = %v, want 0", got)
	}
}

func TestLog232(t *testing.T) {
	if got := Log232(8); got != 3 {
		t.Errorf("Log232(8) = %v, want 3", got)
	}
	if got := Log232(1); got != 0 {
		t.Errorf("Log232(1) = %v, want 0", got)
	}
}

func TestInf32(t *testing.T) {
	inf := Inf32()
	if !(inf > math.MaxFloat32) {
		t.Errorf("Inf32() = %v, want +Inf", inf)
	}
	if got := min(inf, 5); got != 5 {
		t.Errorf("min(Inf32(), 5) = %v, want 5", got)
	}
}

func TestPsyWrappers(t *testing.T) {
	// Atan32/Exp232 are float64 stdlib computations rounded once; pin a few
	// reference values (computed with math.Atan/math.Exp2 directly).
	if got, want := Atan32(1.0), float32(math.Pi/4); got != want {
		t.Errorf("Atan32(1) = %g, want %g", got, want)
	}
	if got := Exp232(3.0); got != 8.0 {
		t.Errorf("Exp232(3) = %g, want 8", got)
	}
	if got := Exp2(10); got != 1024 {
		t.Errorf("Exp2(10) = %g, want 1024", got)
	}
	// ff_exp10 is exp2(M_LOG2_10 * x), NOT math.Pow(10, x); at x = 3 the
	// two agree exactly, at other points they may differ in the last ulp.
	if got := Exp10(3); got != 1000 {
		t.Errorf("Exp10(3) = %g, want 1000", got)
	}
	if got := Pow(2, 0.5); got != math.Sqrt2 {
		t.Errorf("Pow(2, 0.5) = %g, want %g", got, math.Sqrt2)
	}
	if got := Exp(0); got != 1 {
		t.Errorf("Exp(0) = %g, want 1", got)
	}
	if !math.IsNaN(float64(NaN32())) {
		t.Error("NaN32 is not NaN")
	}
}

func TestNMRWrappers(t *testing.T) {
	if got := Exp32(0); got != 1 {
		t.Errorf("Exp32(0) = %g, want 1", got)
	}
	if got := Log32(1); got != 0 {
		t.Errorf("Log32(1) = %g, want 0", got)
	}
	if got := Pow32(2, 10); got != 1024 {
		t.Errorf("Pow32(2, 10) = %g, want 1024", got)
	}
	if got := Ceil32(1.25); got != 2 {
		t.Errorf("Ceil32(1.25) = %g, want 2", got)
	}
	if got := Round32(-2.5); got != -3 {
		t.Errorf("Round32(-2.5) = %g, want -3 (half away from zero)", got)
	}
	if got := float32(MaxFloat32); got != math.MaxFloat32 {
		t.Errorf("MaxFloat32 = %g", got)
	}
}

func TestAbsf(t *testing.T) {
	cases := []struct{ x, want float32 }{
		{3, 3},
		{-3, 3},
		{0, 0},
		{math.MaxFloat32, math.MaxFloat32},
		{-math.MaxFloat32, math.MaxFloat32},
	}
	for _, c := range cases {
		if got := Absf(c.x); got != c.want {
			t.Errorf("Absf(%g) = %g, want %g", c.x, got, c.want)
		}
	}
	// Absf normalizes -0.0 to +0.0: max(-0.0, +0.0) is +0.0.
	if got := Absf(float32(math.Copysign(0, -1))); got != 0 || math.Signbit(float64(got)) {
		t.Errorf("Absf(-0.0) = %g (signbit %v), want +0.0", got, math.Signbit(float64(got)))
	}
	// Absf is max(x, -x), which propagates NaN, so Absf(NaN) is NaN.
	if got := Absf(float32(math.NaN())); !math.IsNaN(float64(got)) {
		t.Errorf("Absf(NaN) = %g, want NaN", got)
	}
}

func TestClipf(t *testing.T) {
	cases := []struct {
		v, lo, hi, want float32
	}{
		{-1, 0, 10, 0},  // below lo
		{5, 0, 10, 5},   // within range
		{20, 0, 10, 10}, // above hi
		{0, 0, 10, 0},   // at lo
		{10, 0, 10, 10}, // at hi
	}
	for _, c := range cases {
		if got := Clipf(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("Clipf(%g, %g, %g) = %g, want %g", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestClipi(t *testing.T) {
	cases := []struct {
		v, lo, hi, want int
	}{
		{-1, 0, 10, 0},  // below lo
		{5, 0, 10, 5},   // within range
		{20, 0, 10, 10}, // above hi
		{0, 0, 10, 0},   // at lo
		{10, 0, 10, 10}, // at hi
	}
	for _, c := range cases {
		if got := Clipi(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("Clipi(%d, %d, %d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}
