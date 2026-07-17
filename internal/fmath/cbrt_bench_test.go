package fmath

import "testing"

// BenchmarkCbrt32 measures the escape-path cube root, which the profile puts
// at ~6% of encode. Inputs are integers in [0,8191], the clip range the
// caller enforces.
func BenchmarkCbrt32(b *testing.B) {
	var sink float32
	i := 0
	for b.Loop() {
		sink += Cbrt32(float32(i & 8191))
		i++
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/call")
	_ = sink
}
