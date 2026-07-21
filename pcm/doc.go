// SPDX-License-Identifier: LGPL-2.1-or-later

// Package pcm is the high-level PCM streaming API for go-aac: interleaved
// little-endian integer PCM in, an AAC-LC ADTS stream out via io.Writer
// (Encoder), or raw access units out through a callback for muxing into MP4
// (FrameEncoder). It also decodes AAC-LC back to PCM (Decoder).
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
// # Muxing into MP4
//
// A caller putting AAC-LC into MP4 or fragmented MP4 (CMAF) needs the
// opposite of ADTS: raw access units, with their boundaries reported out of
// band. FrameEncoder is that path, and it is the same pipeline as Encoder,
// so the access units are byte-identical to the ADTS stream's payloads:
//
//	fe, err := aacpcm.NewFrameEncoder(cfg)
//	emit := func(au []byte, samples int) error {
//	    segment = append(segment, au...) // au is borrowed; copy or append
//	    return nil
//	}
//	err = fe.EncodeInterleaved(pcm, emit)
//	err = fe.Flush(emit) // drains the priming frame; without it the tail is lost
//
// FrameEncoder.AudioSpecificConfig is the esds DecoderSpecificInfo, valid
// before any audio is encoded so an init segment can be built up front, and
// FrameEncoder.Delay is the priming count for the edit list media_time. The
// emit callback has the same shape as go-flac's pcm.FrameEncoder, so a muxer's
// per-unit path is shared between the two codecs; the lifecycle differs, since
// AAC-LC needs a Flush to drain the priming frame where the FLAC frame encoder
// is one-shot.
//
// Note the two spellings of EncodeInterleaved: the package-level function is
// the one-shot ADTS helper below, while FrameEncoder.EncodeInterleaved is the
// streaming raw-access-unit method used here.
//
// Every access unit decodes to aac.FrameSize samples, which is what a trun
// sample_duration carries. That is also why sample-accurate trimming needs
// one number the encoder does not have: the caller feeding the PCM tracks the
// true input sample count and gives it to the muxer as the edit list segment
// duration, since the reported per-unit count cannot distinguish the padding
// in a short final frame.
//
// # Gapless playback
//
// ADTS cannot signal the encoder delay (aac.EncoderDelay priming samples) or
// the final-frame padding, so decoders emit roughly aac.EncoderDelay extra
// leading samples and up to 1023 trailing ones. Every AAC-in-ADTS stream in
// the world behaves this way; players compensate or ignore it. Compute clip
// durations from the source PCM, not from decoded AAC length. Sample-accurate
// trimming needs a container with an edit list: use FrameEncoder with an MP4
// muxer such as go-m4a.
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
// An Encoder is not safe for concurrent use; use one per goroutine, and the
// same holds for a FrameEncoder and a Decoder. The package-level
// EncodeInterleaved function is safe for concurrent use.
package pcm
