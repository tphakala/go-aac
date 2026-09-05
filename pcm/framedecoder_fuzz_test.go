// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"testing"
)

// rawAU returns the bare access unit (payload without the ADTS header) of the
// first frame of a corpus stream, the shape a raw FrameDecoder consumes.
func rawAU(name string) []byte {
	fr := firstFrame(name)
	if len(fr) <= adtsHeaderLen {
		return nil
	}
	return fr[adtsHeaderLen:]
}

// FuzzFrameDecoderADTS asserts the ADTS frame decode path never panics on
// arbitrary access units. Unlike FuzzDecodeStream it feeds one unit per call
// straight to DecodeFrame, so it exercises the frame wrapper without the
// stream-level resync in front of it.
func FuzzFrameDecoderADTS(f *testing.F) {
	for _, name := range []string{streamMono, streamStereo, streamCRC} {
		if fr := firstFrame(name); fr != nil {
			f.Add(fr)
		}
	}
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xf1})
	f.Add([]byte{0xff, 0xf1, 0x4c, 0x80, 0x0d, 0x3f, 0xfc})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		d := NewADTSDecoder()
		// Decode the same unit twice through one decoder so the second call also
		// exercises an already-configured decoder (the configured-path parse and
		// overlap-add carry), not only a fresh one. On success the emitted byte
		// count must equal the reported per-channel sample count times the stride.
		for range 2 {
			out, samples, err := d.DecodeFrame(nil, data)
			if err == nil && len(out) != samples*d.Channels()*2 {
				t.Fatalf("claimed %d samples/ch but emitted %d bytes at %d channels", samples, len(out), d.Channels())
			}
		}
	})
}

// FuzzFrameDecoderRaw asserts the raw frame decode path never panics on
// arbitrary access units, given a fixed valid AudioSpecificConfig (AAC-LC,
// 44.1 kHz, stereo). The raw path has no syncword or header framing, so a
// hostile unit reaches the spectral decode directly.
func FuzzFrameDecoderRaw(f *testing.F) {
	for _, name := range []string{streamMono, streamStereo} {
		if au := rawAU(name); au != nil {
			f.Add(au)
		}
	}
	f.Add([]byte{})
	f.Add([]byte{0x21, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		d, err := NewRawDecoder([]byte{0x12, 0x10}) // AAC-LC, 44.1 kHz, stereo
		if err != nil {
			return
		}
		for range 2 {
			out, samples, decErr := d.DecodeFrame(nil, data)
			if decErr == nil && len(out) != samples*d.Channels()*2 {
				t.Fatalf("claimed %d samples/ch but emitted %d bytes at %d channels", samples, len(out), d.Channels())
			}
		}
	})
}

// FuzzParseASC asserts the AudioSpecificConfig probe never panics on arbitrary
// input: it must always classify a buffer as a config or a typed error.
func FuzzParseASC(f *testing.F) {
	f.Add([]byte{0x12, 0x10})             // AAC-LC 44.1 kHz stereo
	f.Add([]byte{0x0a, 0x10})             // AOT 1 (non-LC)
	f.Add([]byte{0x2b, 0x92, 0x08, 0x00}) // explicit SBR
	f.Add([]byte{0xea, 0x12, 0x08})       // explicit PS
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Fuzz(func(_ *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		_, _ = ParseASC(data)
	})
}
