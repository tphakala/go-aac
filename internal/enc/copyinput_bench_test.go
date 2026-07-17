// SPDX-License-Identifier: LGPL-2.1-or-later
package enc

import "testing"

// BenchmarkCopyInputSamples isolates the input shift. The full-encode profile
// attributes ~12% of encode time to the memmove underneath it, which the
// arithmetic disputes: this moves ~8 KB per frame, so ~45 MB across a 120 s
// clip, which is milliseconds of bandwidth and not hundreds of them.
func BenchmarkCopyInputSamples(b *testing.B) {
	for _, ch := range []int{1, 2} {
		name := map[int]string{1: "mono", 2: "stereo"}[ch]
		b.Run(name, func(b *testing.B) {
			e := &Encoder{}
			e.cfg.Channels = ch
			samples := make([][]float32, ch)
			for c := range samples {
				samples[c] = make([]float32, 1024)
			}
			b.ResetTimer()
			for b.Loop() {
				e.copyInputSamples(samples)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/frame")
		})
	}
}
