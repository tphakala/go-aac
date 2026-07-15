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
// # Concurrency
//
// An Encoder is not safe for concurrent use; use one per goroutine.
// EncodeInterleaved is safe for concurrent use.
package pcm
