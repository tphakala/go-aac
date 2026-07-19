// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import (
	"fmt"
	"testing"
)

// This file is untagged, so it compiles into BOTH builds and calls the exported
// (dispatched) AbsPow34. benchstat can then A/B the default and -tags goaac_simd
// builds on identical binary shapes. b.ReportAllocs keeps the 0-alloc invariant
// visible on both. Inputs come from the package LCG (lcg.go) so both builds see
// the same deterministic bytes.

// benchFloats fills n deterministic float32 values spanning both signs, scaled
// down from the LCG's int32 output. salt varies the stream between call sites.
func benchFloats(n int, salt uint32) []float32 {
	l := LCGSeed + LCG(salt)
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(l.Next()) / 1e6
	}
	return s
}

// BenchmarkAbsPow34 sweeps the band widths, the widest SwbSize1024 entry (96),
// the 128-lane shape, and the full-frame 1024 that fast.go, nmr.go, twoloop.go
// and trellis.go run. 20 is included so the AVX 8-lane remainder (a 4-element
// scalar tail) shows up in benchstat, not just the lane-aligned sizes.
func BenchmarkAbsPow34(b *testing.B) {
	for _, n := range []int{16, 20, 96, 128, 1024} {
		in := benchFloats(n, 1)
		out := make([]float32, n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				AbsPow34(out, in)
			}
		})
	}
}
