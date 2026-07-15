// SPDX-License-Identifier: LGPL-2.1-or-later

// Package pcm is the high-level PCM streaming API for go-aac: interleaved
// little-endian integer PCM in via io.Writer, an AAC-LC ADTS stream out.
//
// It is shaped exactly like go-flac's pcm package and go-opus' oggopus
// package, so a consumer can switch between the three encoders with the
// same call shape. The package name deliberately collides with
// go-flac/pcm; import it with an alias:
//
//	import aacpcm "github.com/tphakala/go-aac/pcm"
//
// # Encoding
//
// NewEncoder accepts any io.Writer and a Config. ADTS frames each access
// unit individually, so no io.WriteSeeker is ever needed and Close
// performs no finalization beyond draining:
//
//	enc, err := aacpcm.NewEncoder(f, aacpcm.Config{
//	    SampleRate: 48000,
//	    BitDepth:   16,
//	    Channels:   1,
//	    Bitrate:    96000,
//	})
//
// Write accepts arbitrary chunk sizes: bytes that do not complete an
// inter-channel sample or a 1024-sample frame are buffered internally, so
// io.Copy works with any buffer size, including ones not divisible by the
// sample stride. The int-to-float conversion, 1024-sample framing, encoder
// priming and final-frame padding all happen internally.
//
// # Reuse and one-shot encoding
//
// Encoder.Reset rebinds an existing encoder to a new sink and Config,
// reusing all internal buffers (about 650 KiB), so a producer that encodes
// many independent clips can pool encoders. EncodeInterleaved is the
// one-shot helper for a complete in-memory buffer; it draws from an
// internal pool and is safe for concurrent use:
//
//	err := aacpcm.EncodeInterleaved(w, cfg, pcmBytes)
//
// # Gapless playback
//
// ADTS cannot signal the encoder delay (1024 priming samples) or the
// final-frame padding, so decoders emit roughly 1024 extra leading samples
// and up to 1023 trailing ones. Every AAC-in-ADTS stream in the world
// behaves this way; players compensate or ignore it. Compute clip
// durations from the source PCM, not from decoded AAC length. For
// sample-accurate trimming a container with an edit list (MP4) is
// required, which is out of scope for v1; the low-level aac package plus
// an external muxer is the escape hatch.
//
// # Decoding
//
// NewDecoder wraps an io.Reader and decodes an AAC-LC stream to interleaved
// little-endian S16 PCM. It mirrors go-flac's pcm.Decoder: NewDecoder, Info,
// Read (io.Reader), WriteTo (io.WriterTo) and Reset for pooling.
//
//	d, err := aacpcm.NewDecoder(r)
//	if err != nil {
//	    // classify with errors.Is against aacpcm.ErrCorruptStream or aacpcm.ErrUnsupported
//	}
//	info := d.Info() // SampleRate, Channels, Profile
//	_, err = io.Copy(w, d)
//
// ADTS is the default: the framer locates the 0xFFF syncword, resyncs past
// leading or mid-stream garbage, reads each frame by its ADTS length and
// reconstructs it. Info is valid immediately after NewDecoder, which peeks the
// first header. Raw access units described by an AudioSpecificConfig are opt in
// through WithRawStream; raw carries no syncword, so the units are
// length-prefixed (2-byte big-endian, a go-aac framing convention).
//
// The S16 output is the decoder's native S32P samples shifted right by 16,
// which is byte-identical to "ffmpeg -c:a aac_fixed -f s16le" under bitexact,
// including Apple afconvert LC streams. Malformed input never panics: the API
// is recover-free and returns wrapped ErrCorruptStream or ErrUnsupported
// sentinels (testable with errors.Is). A truncated final frame delivers every
// complete frame and then reports ErrCorruptStream. A mid-stream sample-rate or
// channel change is reported as ErrUnsupported after the first configuration's
// frames, because a single interleaved s16le stream cannot signal the change
// (the C decoder re-initializes instead, which a future segmented API could
// match). A pooled Decoder decodes at zero allocations per frame in steady
// state.
//
// # Concurrency
//
// An Encoder is not safe for concurrent use; use one per goroutine.
// EncodeInterleaved is safe for concurrent use. A Decoder is likewise not safe
// for concurrent use.
package pcm
