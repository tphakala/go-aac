// SPDX-License-Identifier: LGPL-2.1-or-later
package aac

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// benchPlanar loads GOAAC_BENCH_WAV and pre-converts it to planar float32,
// so the benchmark below times the codec alone. Input conversion is measured
// separately (it is ~0.2% of encode) and only muddies the profile here.
func benchPlanar(tb testing.TB) (frames [][]float32, rate, channels int) {
	tb.Helper()
	p := os.Getenv("GOAAC_BENCH_WAV")
	if p == "" {
		tb.Skip("real-audio benchmark needs GOAAC_BENCH_WAV")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		tb.Fatal(err)
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		tb.Fatalf("%s: not a RIFF/WAVE file", p)
	}
	var data []byte
	var bits int
	off := 12
	for off+8 <= len(raw) {
		id := string(raw[off : off+4])
		// A size with the high bit set casts to a negative int on a 32-bit
		// build, slipping past the bound check below and panicking the reslice.
		szU := binary.LittleEndian.Uint32(raw[off+4 : off+8])
		if szU > math.MaxInt32 {
			tb.Fatalf("%s: corrupt chunk %q size %d", p, id, szU)
		}
		sz := int(szU)
		if off+8+sz > len(raw) {
			sz = len(raw) - off - 8
		}
		body := raw[off+8 : off+8+sz]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				tb.Fatalf("%s: short fmt chunk: %d bytes", p, len(body))
			}
			switch f := binary.LittleEndian.Uint16(body[0:2]); f {
			case 1:
			case 0xFFFE:
				if len(body) < 40 || binary.LittleEndian.Uint16(body[24:26]) != 1 {
					tb.Fatalf("%s: extensible wav is not PCM subformat", p)
				}
			default:
				tb.Fatalf("%s: unsupported wav format %d", p, f)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			data = body
		}
		off += 8 + sz + sz&1
	}
	// Guard the divisor: bits < 8 or channels 0 would make the stride zero.
	if bits != 16 && bits != 32 {
		tb.Fatalf("%s: unsupported bit depth %d (must be 16 or 32)", p, bits)
	}
	if channels < 1 || channels > 2 {
		tb.Fatalf("%s: unsupported channel count %d", p, channels)
	}
	if rate <= 0 {
		tb.Fatalf("%s: invalid sample rate %d", p, rate)
	}
	n := len(data) / (channels * bits / 8)
	frames = make([][]float32, channels)
	for c := range frames {
		frames[c] = make([]float32, n)
	}
	for i := range n {
		for c := range channels {
			switch bits {
			case 16:
				v := int16(binary.LittleEndian.Uint16(data[(i*channels+c)*2:]))
				frames[c][i] = float32(v) / (1 << 15)
			case 32:
				v := int32(binary.LittleEndian.Uint32(data[(i*channels+c)*4:]))
				frames[c][i] = float32(v) / (1 << 31)
			default:
				tb.Fatalf("unsupported bit depth %d", bits)
			}
		}
	}
	return frames, rate, channels
}

// Coder names, held as constants so the benchmark table does not add another
// occurrence of literals the other test files already carry.
const (
	coderNMR     = "nmr"
	coderTwoLoop = "twoloop"
	coderFast    = "fast"
)

// BenchmarkEncodeFrames drives the low-level encoder over a real recording,
// one 1024-sample frame at a time. This is the profiling target: no PCM
// conversion, no ADTS framing, no sink, just the codec.
func BenchmarkEncodeFrames(b *testing.B) {
	planar, rate, channels := benchPlanar(b)
	for _, coder := range []struct {
		name string
		cfg  EncoderConfig
	}{
		{coderNMR, EncoderConfig{SampleRate: rate, Channels: channels, Bitrate: 128000, Coder: CoderNMR}},
		{coderTwoLoop, EncoderConfig{SampleRate: rate, Channels: channels, Bitrate: 128000, Coder: CoderTwoLoop}},
		{coderFast, EncoderConfig{SampleRate: rate, Channels: channels, Bitrate: 128000, Coder: CoderFast}},
		// Tools off isolates what the coder search costs versus what the
		// psychoacoustic model, windowing and MDCT cost underneath it.
		{coderNMR + "_notools", EncoderConfig{
			SampleRate: rate, Channels: channels, Bitrate: 128000, Coder: CoderNMR,
			DisableTNS: true, DisablePNS: true, DisableMS: true, DisableIS: true,
		}},
		{coderFast + "_notools", EncoderConfig{
			SampleRate: rate, Channels: channels, Bitrate: 128000, Coder: CoderFast,
			DisableTNS: true, DisablePNS: true, DisableMS: true, DisableIS: true,
		}},
	} {
		b.Run(coder.name, func(b *testing.B) {
			e, err := NewEncoder(coder.cfg)
			if err != nil {
				b.Fatal(err)
			}
			nFrames := len(planar[0]) / FrameSize
			frame := make([][]float32, channels)
			var au []byte
			b.SetBytes(int64(nFrames * FrameSize * channels * 4))
			b.ResetTimer()
			for b.Loop() {
				for f := range nFrames {
					for c := range channels {
						frame[c] = planar[c][f*FrameSize : (f+1)*FrameSize]
					}
					au, err = e.EncodeFrame(au[:0], frame)
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
