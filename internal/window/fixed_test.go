// SPDX-License-Identifier: LGPL-2.1-or-later

package window

import "testing"

// TestSineFixedBakedDelta pins the relationship between the baked oracle
// sine tables and the portable formula: the oracle libm's sinf is off
// correct rounding at exactly 14 of 1024 and 3 of 128 inputs, and each
// deviation is a single float ulp (one quantum of the scaled int at that
// magnitude). If this test starts failing after a re-bake, the oracle
// toolchain changed; re-verify against the pin before accepting.
func TestSineFixedBakedDelta(t *testing.T) {
	cases := []struct {
		name     string
		baked    []int32
		n        int
		wantOffs int
	}{
		{"sine_1024", Sine1024Fixed, 1024, 14},
		{"sine_128", Sine128Fixed, 128, 3},
	}
	for _, c := range cases {
		formula := SineFixed(c.n)
		offs := 0
		for i := range c.baked {
			d := int64(c.baked[i]) - int64(formula[i])
			if d == 0 {
				continue
			}
			offs++
			// one float ulp at this magnitude: 2^(exp-23) of the scaled value
			ulp := int64(1)
			for v := c.baked[i]; v >= 1<<24; v >>= 1 {
				ulp <<= 1
			}
			if d != ulp && d != -ulp {
				t.Errorf("%s[%d]: baked %d vs formula %d, delta %d exceeds one ulp %d",
					c.name, i, c.baked[i], formula[i], d, ulp)
			}
		}
		if offs != c.wantOffs {
			t.Errorf("%s: %d values differ from the portable formula, want %d",
				c.name, offs, c.wantOffs)
		}
	}
}

// TestKBDFixedRange sanity-checks the computed integer KBD windows: strictly
// rising to the midpoint, bounded by 2147483647, matching lengths. The
// byte-exact check against the C is TestIMDCTDump in internal/dec.
func TestKBDFixedRange(t *testing.T) {
	for _, c := range []struct {
		name string
		w    []int32
		n    int
	}{
		{"kbd_long_1024", KBDLong1024Fixed, 1024},
		{"kbd_short_128", KBDShort128Fixed, 128},
	} {
		if len(c.w) != c.n {
			t.Fatalf("%s: len %d, want %d", c.name, len(c.w), c.n)
		}
		for i := 1; i <= c.n/2; i++ {
			if c.w[i] <= c.w[i-1] {
				t.Fatalf("%s: not rising at %d (%d <= %d)", c.name, i, c.w[i], c.w[i-1])
			}
		}
		if c.w[0] <= 0 {
			t.Fatalf("%s: nonpositive first entry %d", c.name, c.w[0])
		}
	}
}
