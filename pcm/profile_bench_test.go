// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"testing"
)

// wavPCMTB is wavPCM taking a testing.TB, so benchmarks can read the corpus.
// Minimal canonical RIFF/WAVE parser, test-only.
func wavPCMTB(tb testing.TB, path string) (data []byte, rate, channels, bits int) {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		tb.Fatalf("%s: not a RIFF/WAVE file", path)
	}
	off := 12
	for off+8 <= len(raw) {
		id := string(raw[off : off+4])
		// A size with the high bit set casts to a negative int on a 32-bit
		// build, slipping past the bound check below and panicking the reslice.
		szU := binary.LittleEndian.Uint32(raw[off+4 : off+8])
		if szU > math.MaxInt32 {
			tb.Fatalf("%s: corrupt chunk %q size %d", path, id, szU)
		}
		sz := int(szU)
		if off+8+sz > len(raw) {
			sz = len(raw) - off - 8
		}
		body := raw[off+8 : off+8+sz]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				tb.Fatalf("%s: short fmt chunk: %d bytes", path, len(body))
			}
			switch f := binary.LittleEndian.Uint16(body[0:2]); f {
			case 1:
			case 0xFFFE:
				if len(body) < 40 || binary.LittleEndian.Uint16(body[24:26]) != 1 {
					tb.Fatalf("%s: extensible wav is not PCM subformat", path)
				}
			default:
				tb.Fatalf("%s: unsupported wav format %d", path, f)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			data = body
		}
		off += 8 + sz + sz&1
	}
	if data == nil {
		tb.Fatalf("%s: no data chunk", path)
	}
	return data, rate, channels, bits
}

// benchWAV reads GOAAC_BENCH_WAV, the real recording used as the profiling
// workload. Synthetic tones are not a substitute: a two-tone spectrum is
// sparse, so most bands quantize to zero and the rate-distortion search exits
// early, which understates exactly the code that dominates on real audio.
func benchWAV(tb testing.TB) (data []byte, rate, channels, bits int) {
	tb.Helper()
	p := os.Getenv("GOAAC_BENCH_WAV")
	if p == "" {
		tb.Skip("real-audio benchmark needs GOAAC_BENCH_WAV")
	}
	return wavPCMTB(tb, p)
}

// BenchmarkEncodeReal encodes a real recording through the public one-shot
// path. It is the profiling workload: ns/op against the clip duration gives
// the realtime multiple, and -cpuprofile over it shows where encode time
// actually goes.
func BenchmarkEncodeReal(b *testing.B) {
	data, rate, channels, bits := benchWAV(b)
	cfg := Config{SampleRate: rate, BitDepth: bits, Channels: channels, Bitrate: 128000}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		if err := EncodeInterleaved(io.Discard, cfg, data); err != nil {
			b.Fatal(err)
		}
	}
}
