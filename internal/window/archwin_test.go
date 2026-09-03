// SPDX-License-Identifier: LGPL-2.1-or-later

package window

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"
)

// hashTable folds the raw float32 bits of a window table into one FNV-64a
// digest so a single-arch CI run pins the table exactly.
func hashTable(xs []float32) uint64 {
	h := fnv.New64a()
	var b [4]byte
	for _, x := range xs {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(x))
		h.Write(b[:])
	}
	return h.Sum64()
}

// TestWindowTablesGolden pins the four analysis half-windows generated at init
// (KBD long/short and sine long/short). The generators route through the
// vendored architecture-deterministic fmath.Sin/Cos and fmath.BesselI0 (issue
// #79), so these digests must be identical on every architecture; the pins were
// captured on arm64 and confirmed on amd64. A failure here means the window
// tables changed: an arm64-vs-amd64 split is a lost float64 barrier, otherwise a
// generator or a constant changed.
func TestWindowTablesGolden(t *testing.T) {
	cases := []struct {
		name string
		tab  []float32
		want uint64
	}{
		{"KBDLong1024", KBDLong1024, 0xb18fe8dd14ea7a98},
		{"KBDShort128", KBDShort128, 0xccd5ed37bfb153b2},
		{"Sine1024", Sine1024, 0xae25807832ee77b1},
		{"Sine128", Sine128, 0xe0708e3fb55cf4fe},
	}
	for _, c := range cases {
		if got := hashTable(c.tab); got != c.want {
			t.Errorf("%s digest = %#016x, want %#016x (window table changed)", c.name, got, c.want)
		}
	}
}
