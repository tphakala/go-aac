// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
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

// recoveryFrames returns the first n ADTS frames of a corpus stream, or nil.
func recoveryFrames(name string, n int) [][]byte {
	data := firstFrames(name, n)
	if data == nil {
		return nil
	}
	var frames [][]byte
	for len(data) > 0 {
		l := int(data[3]&0x03)<<11 | int(data[4])<<3 | int(data[5])>>5
		frames = append(frames, data[:l])
		data = data[l:]
	}
	return frames
}

// rawASC builds the 2-byte AudioSpecificConfig (AAC-LC, the ADTS stream's rate
// index and channel config) for the raw twin of an ADTS corpus stream.
func rawASC(frame []byte) []byte {
	sfi := int(frame[2]>>2) & 0x0f
	ch := int(frame[2]&1)<<2 | int(frame[3]>>6)
	v := 2<<11 | sfi<<7 | ch<<3
	return []byte{byte(v >> 8), byte(v)}
}

// recoverySeeds builds the corpus-independent seed units for a stream: the
// fourth frame shape-flipped and truncated (fails to parse after the window
// history shifted), truncated at half, empty, header only, and intact.
func recoverySeeds(frames [][]byte, hdr int) [][]byte {
	seeds := [][]byte{nil, frames[3][:hdr], frames[3]}
	if bad, err := shapeFlipUnit(frames[3], hdr); err == nil {
		seeds = append(seeds, bad)
	}
	return append(seeds, frames[3][:hdr+(len(frames[3])-hdr)/2])
}

// fuzzRecovery runs the recovery property: decode f0..f2, feed one arbitrary
// unit, then decode f3 and f4. When the arbitrary unit returned an error, f3
// and f4 must be byte-identical to a reference decoder that never saw it. When
// it decoded, state legitimately advanced and nothing more is asserted.
func fuzzRecovery(t *testing.T, mk func() *FrameDecoder, frames [][]byte, bad []byte) {
	t.Helper()
	d, ref := mk(), mk()
	for i := range 3 {
		if _, _, err := d.DecodeFrame(nil, frames[i]); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if _, _, err := ref.DecodeFrame(nil, frames[i]); err != nil {
			t.Fatalf("reference frame %d: %v", i, err)
		}
	}
	_, _, badErr := d.DecodeFrame(nil, bad)
	for i := 3; i < 5; i++ {
		got, _, err := d.DecodeFrame(nil, frames[i])
		if err != nil {
			t.Fatalf("frame %d after the arbitrary unit: %v", i, err)
		}
		want, _, err := ref.DecodeFrame(nil, frames[i])
		if err != nil {
			t.Fatalf("reference frame %d: %v", i, err)
		}
		if badErr != nil && !bytes.Equal(got, want) {
			t.Fatalf("frame %d differs from a decoder that never saw the failed unit (%v)", i, badErr)
		}
	}
}

// FuzzFrameDecoderRecovery asserts that after an ADTS unit that returns an
// error, later valid frames decode byte-identically to a decoder that never
// saw it. data[0]&1 selects the stream (stereo with window transitions, or
// mono); data[1:] is the unit.
func FuzzFrameDecoderRecovery(f *testing.F) {
	streams := [2][][]byte{recoveryFrames("click_s44_128k", 6), recoveryFrames("sine_m8_24k", 6)}
	for sel, frames := range streams {
		if len(frames) < 5 {
			f.Fatalf("stream %d: only %d frames", sel, len(frames))
		}
		for _, seed := range recoverySeeds(frames, adtsHeaderLen) {
			f.Add(append([]byte{byte(sel)}, seed...))
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 1 || len(data) > 1<<16 {
			return
		}
		fuzzRecovery(t, NewADTSDecoder, streams[data[0]&1], data[1:])
	})
}

// FuzzFrameDecoderRawRecovery is the raw-access-unit twin of
// FuzzFrameDecoderRecovery: the same streams with their ADTS headers removed
// and the AudioSpecificConfig derived from the stream's header.
func FuzzFrameDecoderRawRecovery(f *testing.F) {
	var streams [2][][]byte
	var ascs [2][]byte
	for sel, name := range []string{"click_s44_128k", "sine_m8_24k"} {
		adts := recoveryFrames(name, 6)
		if len(adts) < 5 {
			f.Fatalf("%s: only %d frames", name, len(adts))
		}
		ascs[sel] = rawASC(adts[0])
		if _, err := ParseASC(ascs[sel]); err != nil {
			f.Fatalf("%s: derived ASC %x rejected: %v", name, ascs[sel], err)
		}
		for _, fr := range adts {
			streams[sel] = append(streams[sel], fr[adtsHeaderLen:])
		}
		for _, seed := range recoverySeeds(streams[sel], 0) {
			f.Add(append([]byte{byte(sel)}, seed...))
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 1 || len(data) > 1<<16 {
			return
		}
		sel := data[0] & 1
		mk := func() *FrameDecoder {
			d, err := NewRawDecoder(ascs[sel])
			if err != nil {
				t.Fatalf("NewRawDecoder: %v", err)
			}
			return d
		}
		fuzzRecovery(t, mk, streams[sel], data[1:])
	})
}
