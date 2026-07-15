// SPDX-License-Identifier: LGPL-2.1-or-later
package window

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func readF32(t *testing.T, path string, n int) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw) != 4*n {
		t.Fatalf("%s: %d bytes, want %d", path, len(raw), 4*n)
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

func TestKBDGolden(t *testing.T) {
	for _, c := range []struct {
		got  []float32
		path string
	}{
		{KBDLong1024, "testdata/kbd1024.f32"},
		{KBDShort128, "testdata/kbd128.f32"},
	} {
		want := readF32(t, c.path, len(c.got))
		maxErr := 0.0
		for i := range want {
			if e := math.Abs(float64(c.got[i] - want[i])); e > maxErr {
				maxErr = e
			}
			if math.Abs(float64(c.got[i]-want[i])) > 1e-7 {
				t.Fatalf("%s[%d]: got %.9f want %.9f", c.path, i, c.got[i], want[i])
			}
		}
		t.Logf("%s: max abs diff = %.3g", c.path, maxErr)
	}
}
