// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"io"
	"testing"
)

// BenchmarkEncodeInterleaved measures the one-shot path on one second of
// 48 kHz 16-bit audio; b.SetBytes reports throughput, and ns/op against
// the one-second duration gives the realtime multiple.
func BenchmarkEncodeInterleaved(b *testing.B) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"48k_mono_96k", Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}},
		{"48k_stereo_128k", Config{SampleRate: 48000, BitDepth: 16, Channels: 2, Bitrate: 128000}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			pcm := genPCM16(48000, tc.cfg.Channels)
			b.SetBytes(int64(len(pcm)))
			b.ResetTimer()
			for b.Loop() {
				if err := EncodeInterleaved(io.Discard, tc.cfg, pcm); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
