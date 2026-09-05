// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"

	aacpcm "github.com/tphakala/go-aac/pcm"
)

// countADTSFrames walks the self-framing ADTS stream by its 13-bit frame
// length fields.
func countADTSFrames(stream []byte) int {
	n := 0
	for len(stream) >= 7 {
		if stream[0] != 0xFF || stream[1]&0xF0 != 0xF0 {
			return -1
		}
		frameLen := int(stream[3]&0x03)<<11 | int(stream[4])<<3 | int(stream[5])>>5
		if frameLen < 7 || frameLen > len(stream) {
			return -1
		}
		stream = stream[frameLen:]
		n++
	}
	return n
}

// ExampleEncodeInterleaved encodes a complete in-memory PCM buffer in one
// call, the BirdNET-Go pattern. The import is aliased because the package
// name deliberately matches go-flac's pcm package.
func ExampleEncodeInterleaved() {
	// One second of silent 16-bit mono PCM at 48 kHz.
	pcm := make([]byte, 48000*2)

	var buf bytes.Buffer
	err := aacpcm.EncodeInterleaved(&buf, aacpcm.Config{
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Bitrate:    96000,
	}, pcm)
	if err != nil {
		log.Fatal(err)
	}
	// 47 input frames (the last one padded) plus one frame covering the
	// encoder delay.
	fmt.Println(countADTSFrames(buf.Bytes()), "ADTS frames")
	// Output: 48 ADTS frames
}

// ExampleNewFrameEncoder collects raw access units for a muxer instead of
// writing a framed stream: the shape a fragmented-MP4 (CMAF) segmenter
// consumes for live HLS. The access unit is borrowed, so the segment
// buffer appends it rather than retaining the slice.
func ExampleNewFrameEncoder() {
	fe, err := aacpcm.NewFrameEncoder(aacpcm.Config{
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Bitrate:    96000,
	})
	if err != nil {
		log.Fatal(err)
	}

	// The init segment's esds DecoderSpecificInfo and the edit list priming
	// count are both available before any audio is encoded.
	asc := fe.AudioSpecificConfig()

	var segment []byte // one CMAF segment's mdat payload
	var sizes []int    // per-sample sizes for the trun box
	emit := func(au []byte, samples int) error {
		segment = append(segment, au...) // copy: au is only valid until this returns
		sizes = append(sizes, len(au))
		// samples is the decoded length in PCM samples per channel, always
		// 1024 for AAC-LC; scale it to the track timescale for trun.
		_ = samples
		return nil
	}

	pcm := make([]byte, 48000*2) // one second of silent 16-bit mono PCM
	if err := fe.EncodeInterleaved(pcm, emit); err != nil {
		log.Fatal(err)
	}
	if err := fe.Flush(emit); err != nil {
		log.Fatal(err)
	}

	// The encoded size is deliberately not printed: it tracks rate-control
	// decisions, and pinning it here would make this example churn on any
	// bitstream tweak. TestFrameEncoderGoldenAccessUnits is the bitstream anchor.
	fmt.Printf("ASC %x, delay %d samples, %d access units\n",
		asc, fe.Delay(), len(sizes))
	// Output: ASC 1188, delay 1024 samples, 48 access units
}

// ExampleNewEncoder streams PCM of unknown length to any io.Writer; no
// seeking and no finalization beyond Close are ever needed.
func ExampleNewEncoder() {
	var out bytes.Buffer
	enc, err := aacpcm.NewEncoder(&out, aacpcm.Config{
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   2,
		Bitrate:    128000,
	})
	if err != nil {
		log.Fatal(err)
	}
	chunk := make([]byte, 4096) // any chunk size works, even odd ones
	for i := range 100 {
		binary.LittleEndian.PutUint16(chunk, uint16(i)) // not real audio
		if _, err := enc.Write(chunk); err != nil {
			log.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Println(countADTSFrames(out.Bytes()), "ADTS frames")
	// Output: 101 ADTS frames
}

// ExampleNewRawDecoder decodes raw AAC-LC access units one at a time, the
// push-style OnFrame(au) shape a network transport (RTSP, HLS) drives, where
// the AudioSpecificConfig arrives out of band. Here the units are produced by a
// FrameEncoder to keep the example self-contained; in a real ingest they come
// from the transport. Passing pcm[:0] reuses one buffer, so steady-state decode
// does not allocate.
func ExampleNewRawDecoder() {
	cfg := aacpcm.Config{SampleRate: 48000, BitDepth: 16, Channels: 1, Bitrate: 96000}
	fe, err := aacpcm.NewFrameEncoder(cfg)
	if err != nil {
		log.Fatal(err)
	}
	asc := fe.AudioSpecificConfig() // delivered to the decoder out of band
	var aus [][]byte
	collect := func(au []byte, _ int) error {
		aus = append(aus, bytes.Clone(au))
		return nil
	}
	if err := fe.EncodeInterleaved(make([]byte, 48000*2), collect); err != nil {
		log.Fatal(err)
	}
	if err := fe.Flush(collect); err != nil {
		log.Fatal(err)
	}

	d, err := aacpcm.NewRawDecoder(asc)
	if err != nil {
		log.Fatal(err)
	}
	var pcm []byte
	total := 0
	for _, au := range aus { // the OnFrame(au) callback body
		var n int
		if pcm, n, err = d.DecodeFrame(pcm[:0], au); err != nil {
			log.Fatal(err)
		}
		total += n
	}
	fmt.Printf("%d Hz, %d ch, %d samples/ch decoded\n", d.SampleRate(), d.Channels(), total)
	// Output: 48000 Hz, 1 ch, 49152 samples/ch decoded
}

// ExampleParseASC probes an AudioSpecificConfig before decoding, so an
// unsupported codec is named up front instead of failing at the first frame.
func ExampleParseASC() {
	// A stereo AAC-LC config (an MP4 esds DecoderSpecificInfo).
	info, err := aacpcm.ParseASC([]byte{0x12, 0x10})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("AAC-LC=%v, %d Hz, %d ch, SBR=%v PS=%v\n",
		info.ObjectType == 2, info.SampleRate, info.Channels, info.SBR, info.PS)

	// HE-AAC (SBR) surfaces as a typed error the caller can branch on.
	if _, err := aacpcm.ParseASC([]byte{0x2b, 0x92, 0x08, 0x00}); errors.Is(err, aacpcm.ErrUnsupportedSBR) {
		fmt.Println("second stream is HE-AAC, unsupported")
	}
	// Output:
	// AAC-LC=true, 44100 Hz, 2 ch, SBR=false PS=false
	// second stream is HE-AAC, unsupported
}
