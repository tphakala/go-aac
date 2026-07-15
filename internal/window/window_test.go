// SPDX-License-Identifier: LGPL-2.1-or-later
package window

import (
	"math"
	"testing"
)

func check(t *testing.T, name string, got []float32, want map[int]float64) {
	t.Helper()
	for i, v := range want {
		if d := math.Abs(float64(got[i]) - v); d > 1e-6 {
			t.Errorf("%s[%d] = %.9f, want %.9f", name, i, got[i], v)
		}
	}
}

func TestKBDLong1024(t *testing.T) {
	check(t, "KBDLong1024", KBDLong1024, map[int]float64{
		0: 0.000292562, 1: 0.000429986, 255: 0.177255594,
		511: 0.706119339, 512: 0.708092846, 1023: 0.999999957,
	})
}

func TestKBDShort128(t *testing.T) {
	check(t, "KBDShort128", KBDShort128, map[int]float64{
		0: 0.000043796, 63: 0.697406466, 64: 0.716675813, 127: 0.999999999,
	})
}

func TestSine(t *testing.T) {
	check(t, "Sine1024", Sine1024, map[int]float64{
		0: 0.000766990, 1023: 0.999999706,
	})
	if len(Sine128) != 128 {
		t.Fatalf("len(Sine128) = %d, want 128", len(Sine128))
	}
}

func TestKBDMonotone(t *testing.T) {
	for i := 1; i < len(KBDLong1024); i++ {
		if KBDLong1024[i] < KBDLong1024[i-1] {
			t.Fatalf("KBDLong1024 not monotone at %d", i)
		}
	}
}
