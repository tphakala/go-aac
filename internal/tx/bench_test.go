// SPDX-License-Identifier: LGPL-2.1-or-later

package tx

import "testing"

func benchIMDCT(b *testing.B, n int, scale float32) {
	b.Helper()
	m := NewIMDCT(n, scale)
	in := make([]int32, n)
	out := make([]int32, n)
	for i := range in {
		// uint32 intermediate: the constant exceeds int32, so a bare
		// int multiply would not compile on 32-bit targets.
		in[i] = int32(uint32(i)*2654435761 + 12345)
	}
	b.ReportAllocs()
	for b.Loop() {
		m.Transform(out, in)
	}
}

func BenchmarkIMDCT1024(b *testing.B) { benchIMDCT(b, 1024, 1.0/1024*128.0) }
func BenchmarkIMDCT128(b *testing.B)  { benchIMDCT(b, 128, 1.0) }
