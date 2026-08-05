// SPDX-License-Identifier: LGPL-2.1-or-later
package dsp

import (
	"fmt"
	"testing"
)

// Like abspow34_bench_test.go this file is untagged, so it compiles into BOTH
// builds and calls the exported (dispatched) QuantizeBands. benchstat then A/Bs
// the default and -tags noasm builds on identical binary shapes.
// b.ReportAllocs keeps the 0-alloc invariant visible on both. Inputs come from
// the package LCG (lcg.go) so both builds see the same deterministic bytes.

// BenchmarkQuantizeBands sweeps the band widths, the widest SwbSize1024 entry
// (96), the 128-lane shape and the full 1024-line frame, for both the signed and
// unsigned dispatch (they hit different f32 kernels:
// Float32ToInt32ScaleClampSigned vs Float32ToInt32ScaleClamp). n=4 (the most
// common band, below the SIMD AVX-activation width so the default build routes
// it to the scalar guard) and n=8 (that width) are included so the small-n
// dispatch crossover is visible; 20 shows the AVX 8-lane remainder (a 4-element
// scalar tail). scaled is run through AbsPow34 so it is a non-negative magnitude,
// as at the call site; in stays signed to drive the sign path.
func BenchmarkQuantizeBands(b *testing.B) {
	for _, isSigned := range []bool{true, false} {
		for _, n := range []int{4, 8, 16, 20, 96, 128, 1024} {
			in := benchFloats(n, 1)
			scaled := make([]float32, n)
			AbsPow34(scaled, benchFloats(n, 2))
			out := make([]int32, n)
			b.Run(fmt.Sprintf("signed=%v/n=%d", isSigned, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					QuantizeBands(out, in, scaled, isSigned, 12, 0.37, 0.4054)
				}
			})
		}
	}
}
