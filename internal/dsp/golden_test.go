// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func readF64(t *testing.T, path string, n int) []float64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(raw) != 8*n {
		t.Fatalf("%s: %d bytes, want %d", path, len(raw), 8*n)
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[8*i:]))
	}
	return out
}

// lcgNoise1024 regenerates the exact input cdump fed to
// ff_lpc_calc_ref_coefs_f: LCG noise scaled to +-1024.
func lcgNoise1024() []float32 {
	samples := make([]float32, 1024)
	l := LCGSeed
	for i := range samples {
		samples[i] = 1024 * (float32(l.Next()) / (1 << 31))
	}
	return samples
}

func TestLPCGolden(t *testing.T) {
	for _, c := range []struct {
		path        string
		order       int
		applyWindow bool
	}{
		{"testdata/lpc_o2_rect.f64", 2, false},
		{"testdata/lpc_o12_hann.f64", 12, true},
	} {
		want := readF64(t, c.path, 1+c.order)
		samples := lcgNoise1024()
		ref := make([]float64, c.order)
		scratch := make([]float64, len(samples))
		gain := CalcRefCoefs(samples, c.order, ref, c.applyWindow, scratch)
		if d := math.Abs(gain-want[0]) / want[0]; d > 1e-9 {
			t.Errorf("%s: gain = %.15g, want %.15g (rel %.3g)", c.path, gain, want[0], d)
		}
		maxErr := 0.0
		for i := range ref {
			d := math.Abs(ref[i] - want[1+i])
			if d > maxErr {
				maxErr = d
			}
			if d > 1e-6 {
				t.Errorf("%s: ref[%d] = %.15g, want %.15g", c.path, i, ref[i], want[1+i])
			}
		}
		t.Logf("%s: gain=%g max |go-c| ref diff = %.3g", c.path, gain, maxErr)
	}
}
