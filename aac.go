// SPDX-License-Identifier: LGPL-2.1-or-later

// Package aac implements an AAC-LC encoder ported from FFmpeg's native
// encoder, pinned at FFmpeg commit d09d5afc3a. See PROVENANCE.md for the
// origin and licensing of the ported code; architecture, porting and
// validation documentation is maintained in the internal planning
// repository.
//
// The library has two layers, mirroring go-flac (flac + pcm) and go-opus
// (opus + oggopus):
//
//   - This package is the low-level codec: planar float32 frames in, raw
//     AAC access units out (Encoder, EncodeFrame), plus the framing
//     helpers raw-AU consumers need (AppendADTSHeader,
//     Encoder.AudioSpecificConfig). Raw access units are not
//     self-framing; see EncodeFrame.
//   - Package pcm is the streaming layer and the right entry point for
//     almost all callers: interleaved little-endian integer PCM in via
//     io.Writer, a self-framing ADTS stream out, or raw access units
//     through a callback for muxing (pcm.FrameEncoder).
//
// An Encoder is not safe for concurrent use; use one per goroutine. The
// package has no mutable global state, so any number of encoders run in
// parallel.
//
// By default three hot kernels are SIMD versions, backed by
// github.com/tphakala/simd, which picks the widest path the CPU supports and
// falls back to pure Go. Building with -tags noasm swaps in their scalar
// counterparts and links no simd code. The output is byte-identical either way,
// so the choice only affects speed, and SIMDEnabled reports which set a binary
// was built with.
package aac

// SampleRates is the MPEG-4 audio sample rate table; the position of a rate
// is its 4-bit index in ADTS and AudioSpecificConfig.
// Mirrors ff_mpeg4audio_sample_rates (libavcodec/mpeg4audio.c @ d09d5afc3a).
var SampleRates = [13]int{96000, 88200, 64000, 48000, 44100, 32000,
	24000, 22050, 16000, 12000, 11025, 8000, 7350}

// sampleRateIndex returns the MPEG-4 table index for rate.
func sampleRateIndex(rate int) (int, bool) {
	for i, r := range SampleRates {
		if r == rate {
			return i, true
		}
	}
	return 0, false
}
