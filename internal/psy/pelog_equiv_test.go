// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

import (
	"math"
	"math/rand"
	"testing"

	"github.com/tphakala/go-aac/internal/fmath"
)

// This file gates the NEON logarithm pair bitwise against fmath.Log232. It is
// tagged !noasm because under -tags noasm, as on architectures other than
// arm64, peLog2 is fmath.Log232 itself. The kernel reproduces every special
// case of the vendored log, so the inputs cover the whole float32 bit space,
// NaN payloads included.

// peLogPins are the edge inputs: both zeros, the smallest subnormal, the
// largest subnormal and the smallest normal, powers of two (log2's exact
// branch) and their neighbours, both sides of the sqrt(2)/2 split of the
// fraction, the largest finite value, +Inf, -Inf, a negative value and NaNs.
var peLogPins = []uint32{
	0x00000000, 0x80000000,
	0x00000001, 0x007fffff, 0x00800000,
	0x3f800000, 0x3f7fffff, 0x3f800001, 0x40000000, 0x3f000000,
	0x3f3504f3, 0x3f3504f4, 0x3f3504f2,
	0x7f7fffff, 0x7f800000, 0xff800000, 0xbf800000,
	0x7fc00000, 0x7f800001, 0xffc12345,
}

// checkPeLog2 requires both results of peLog2 to match fmath.Log232 bit for
// bit.
func checkPeLog2(t *testing.T, eb, tb uint32) {
	t.Helper()
	e, th := math.Float32frombits(eb), math.Float32frombits(tb)
	ge, gt := peLog2(e, th)
	we, wt := fmath.Log232(e), fmath.Log232(th)
	if math.Float32bits(ge) != math.Float32bits(we) || math.Float32bits(gt) != math.Float32bits(wt) {
		t.Fatalf("peLog2(%#08x, %#08x) = (%#08x, %#08x), want (%#08x, %#08x)",
			eb, tb, math.Float32bits(ge), math.Float32bits(gt), math.Float32bits(we), math.Float32bits(wt))
	}
}

// TestPeLog2MatchesScalar sweeps every pin pair, every float32 exponent with
// mantissas at both ends and in between, and random bit patterns.
func TestPeLog2MatchesScalar(t *testing.T) {
	for _, e := range peLogPins {
		for _, th := range peLogPins {
			checkPeLog2(t, e, th)
		}
	}
	rng := rand.New(rand.NewSource(1))
	for exp := range uint32(256) {
		for _, mant := range []uint32{0, 1, 0x3504f3, 0x3504f4, 0x400000, 0x7fffff, rng.Uint32() & 0x7fffff} {
			v := exp<<23 | mant
			checkPeLog2(t, v, v|0x80000000)
			checkPeLog2(t, v, rng.Uint32())
		}
	}
	for range 200000 {
		checkPeLog2(t, rng.Uint32(), rng.Uint32())
	}
}

// FuzzPeLog2Equiv drives both arguments over arbitrary float32 bit patterns.
func FuzzPeLog2Equiv(f *testing.F) {
	f.Add(uint32(0x3f800000), uint32(0x3f3504f3))
	f.Add(uint32(0x00000001), uint32(0x7f7fffff))
	f.Add(uint32(0x47c35000), uint32(0x3a83126f))
	f.Add(uint32(0x80000000), uint32(0x7fc00000))
	f.Fuzz(func(t *testing.T, e, th uint32) {
		checkPeLog2(t, e, th)
	})
}
