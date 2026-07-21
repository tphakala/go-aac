// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import "testing"

// BenchmarkConvert isolates the interleaved-PCM to planar-float32 conversion,
// which the full-encode profile attributes ~10% of encode time to. pprof folds
// an inlined callee's cost into the caller's line, so measure it directly
// rather than trust that attribution.
func BenchmarkConvert(b *testing.B) {
	const n = 1024
	for _, ch := range []int{1, 2} {
		for _, depth := range []int{16, 32} {
			name := map[int]string{1: "mono", 2: "stereo"}[ch]
			b.Run(name+"_s"+map[int]string{16: "16", 32: "32"}[depth], func(b *testing.B) {
				e := &FrameEncoder{cfg: Config{SampleRate: 48000, BitDepth: depth, Channels: ch}}
				chunk := make([]byte, n*ch*depth/8)
				for i := range chunk {
					chunk[i] = byte(i * 7)
				}
				b.SetBytes(int64(len(chunk)))
				b.ResetTimer()
				for b.Loop() {
					e.convert(chunk, n)
				}
				// ns/op divided by (n*ch) gives ns per sample.
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n*ch), "ns/sample")
			})
		}
	}
}
