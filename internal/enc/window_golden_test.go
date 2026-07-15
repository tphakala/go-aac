// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/mdct"
)

// TestEightShortWindowMDCTMatchesC feeds the cquant short fixture's input
// samples through applyWindow(EIGHT_SHORT, sine, sine) + 8x mdct128 and
// compares against the C's windowed short MDCT output
// (internal/coder/testdata/short_in_coeffs.f32, produced with
// apply_eight_short_window semantics and AV_TX_FLOAT_MDCT).
func TestEightShortWindowMDCTMatchesC(t *testing.T) {
	load := func(name string) []float32 {
		b, err := os.ReadFile("../coder/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		}
		return out
	}
	samples := load("short_in_samples.f32")
	want := load("short_in_coeffs.f32")

	// Pin the fixture shape. Without this a truncated fixture only compares a
	// prefix, and an empty one makes peak == 0 so rel is NaN and the rel > 1e-4
	// check below passes vacuously.
	if len(samples) != 2048 || len(want) != 1024 {
		t.Fatalf("fixture shape: len(samples)=%d want 2048, len(coeffs)=%d want 1024",
			len(samples), len(want))
	}

	out := make([]float32, 2048)
	applyWindow(coder.EightShortSequence, 0, 0, out, samples)
	got := make([]float32, 1024)
	m128 := mdct.New(128, 32768.0)
	for i := 0; i < 1024; i += 128 {
		m128.Transform(got[i:i+128], out[i*2:i*2+256])
	}

	var peak, maxDiff float64
	for i := range want {
		peak = math.Max(peak, math.Abs(float64(want[i])))
		maxDiff = math.Max(maxDiff, math.Abs(float64(got[i])-float64(want[i])))
	}
	if peak == 0 {
		t.Fatal("golden coeffs are all zero; fixture is degenerate")
	}
	rel := maxDiff / peak
	t.Logf("eight-short windowed MDCT: peak %.4g, max abs diff %.4g, rel %.3g", peak, maxDiff, rel)
	if rel > 1e-4 {
		t.Errorf("max diff relative to peak %g exceeds 1e-4", rel)
	}
}
