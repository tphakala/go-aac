// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"errors"
	"fmt"

	"github.com/tphakala/go-aac/internal/enc"
)

// FrameSize is the number of samples per channel consumed by one
// Encoder.EncodeFrame call and covered by one AAC access unit.
const FrameSize = 1024

// defaultBitrate is the target selected by EncoderConfig.Bitrate == 0.
// Mirrors AV_CODEC_DEFAULT_BITRATE, the bit_rate FFmpeg's encoder receives
// when the caller sets none (libavcodec/options_table.h:47 @ d09d5afc3a):
// 200 kb/s total, regardless of channel count.
const defaultBitrate = 200_000

// Coder selects the quantizer search strategy. The zero value is CoderNMR,
// upstream's default. Mirrors enum AACCoder (libavcodec/aacenc.h
// @ d09d5afc3a).
type Coder int

// Quantizer search strategies, from best quality to fastest.
const (
	// CoderNMR is the noise-to-mask-ratio trellis search, the upstream
	// default and the best quality per bit.
	CoderNMR Coder = iota
	// CoderTwoLoop is the ISO 13818-7 Appendix C two-loop search.
	CoderTwoLoop
	// CoderFast is the constrained two-loop heuristic, the fastest.
	CoderFast
)

// kind maps the public enum to the internal one. The two are kept separate
// so the internal ordering can never leak into the public API contract.
func (c Coder) kind() (enc.CoderKind, bool) {
	switch c {
	case CoderNMR:
		return enc.CoderNMR, true
	case CoderTwoLoop:
		return enc.CoderTwoLoop, true
	case CoderFast:
		return enc.CoderFast, true
	default:
		return 0, false
	}
}

// EncoderConfig configures a low-level Encoder. It is a flat struct,
// mirroring opus.EncoderConfig in go-opus: every field's zero value is
// documented, so a literal with only SampleRate and Channels set is a
// complete, valid configuration.
type EncoderConfig struct {
	// SampleRate is the input sample rate in Hz: 44100 or 48000 in v1.
	// Required; there is no zero default.
	SampleRate int
	// Channels is 1 (mono SCE) or 2 (stereo CPE). Required; there is no
	// zero default.
	Channels int

	// Bitrate is the ABR target in bits per second for the whole stream
	// (all channels), e.g. 128000. Zero selects FFmpeg's default of
	// 200 kb/s total. Targets above the AAC buffer model ceiling (6144
	// bits per channel per 1024-sample frame) are clamped, exactly as the
	// C encoder clamps them; negative targets are rejected.
	Bitrate int

	// Cutoff, when > 0, overrides the automatic coding bandwidth in Hz
	// (FFmpeg -cutoff equivalent). It must not exceed SampleRate/2. Leave
	// it 0 for the tuned rate-dependent default.
	Cutoff int

	// Coder selects the quantizer search. The zero value is CoderNMR,
	// upstream's default and the recommended choice.
	Coder Coder

	// Tool switches are negative (Disable*) so the zero value enables
	// every tool, matching FFmpeg's defaults: TNS, PNS, M/S and I/S on.
	DisableTNS bool // disable temporal noise shaping
	DisablePNS bool // disable perceptual noise substitution
	DisableMS  bool // disable the mid/side stereo search
	DisableIS  bool // disable intensity stereo
}

// validate reports the first config problem, or nil.
func (c EncoderConfig) validate() error {
	switch c.SampleRate {
	case 44100, 48000:
	default:
		return fmt.Errorf("go-aac: unsupported sample rate %d (supported: 44100, 48000)", c.SampleRate)
	}
	if c.Channels < 1 || c.Channels > 2 {
		return fmt.Errorf("go-aac: unsupported channel count %d (supported: 1, 2)", c.Channels)
	}
	if c.Bitrate < 0 {
		return fmt.Errorf("go-aac: negative bitrate %d", c.Bitrate)
	}
	if c.Cutoff < 0 || c.Cutoff > c.SampleRate/2 {
		return fmt.Errorf("go-aac: cutoff %d Hz outside 0..%d (half the sample rate)", c.Cutoff, c.SampleRate/2)
	}
	if _, ok := c.Coder.kind(); !ok {
		return fmt.Errorf("go-aac: unknown coder %d", c.Coder)
	}
	return nil
}

// internal converts a validated config to the internal encoder config.
func (c EncoderConfig) internal() enc.Config {
	kind, _ := c.Coder.kind()
	bitrate := c.Bitrate
	if bitrate == 0 {
		bitrate = defaultBitrate
	}
	return enc.Config{
		SampleRate: c.SampleRate,
		Bitrate:    bitrate,
		Channels:   c.Channels,
		Cutoff:     c.Cutoff,
		Coder:      kind,
		DisableTNS: c.DisableTNS,
		DisablePNS: c.DisablePNS,
		DisableMS:  c.DisableMS,
		DisableIS:  c.DisableIS,
	}
}

// Encoder is the low-level AAC-LC encoder: planar float32 frames in, raw
// access units out, one frame per call. The output is NOT self-framing;
// see EncodeFrame. For interleaved integer PCM in and a streamable ADTS
// stream out, use the pcm package instead.
//
// An Encoder is stateful (MDCT overlap, psychoacoustic history, bit
// reservoir all carry across frames) and is not safe for concurrent use;
// use one Encoder per goroutine.
type Encoder struct {
	enc        *enc.Encoder
	cfg        EncoderConfig
	srIndex    int
	channels   int
	shortFrame bool // a sub-FrameSize frame was submitted; only the final frame may be short
}

// NewEncoder returns an Encoder for cfg. Mirrors aacenc.c:aac_encode_init
// @ d09d5afc3a: bitrate clamping, coding bandwidth selection and
// psychoacoustic model setup all happen here, fixed for the stream.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset re-arms the encoder for a new, independent stream with cfg,
// reusing all internal buffers (the encoder state is about 650 KiB, so
// pooled reuse matters; this is what backs pcm.EncodeInterleaved).
// Encoding after Reset produces the same bytes as a fresh NewEncoder. On
// error the encoder must not be used.
func (e *Encoder) Reset(cfg EncoderConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if e.enc == nil {
		ie, err := enc.New(cfg.internal())
		if err != nil {
			return err
		}
		e.enc = ie
	} else if err := e.enc.Reset(cfg.internal()); err != nil {
		return err
	}
	e.cfg = cfg
	e.srIndex = e.enc.SampleRateIndex()
	e.channels = cfg.Channels
	e.shortFrame = false
	return nil
}

// EncodeFrame encodes the next frame of planar float32 PCM in [-1, 1] (one
// slice per channel, up to FrameSize samples each; only the final frame of
// a stream may be shorter, and it is zero-padded) and appends the encoded
// access unit to dst, returning the extended slice. It is append-style
// like strconv.AppendInt: allocation-free when dst has capacity.
//
// The returned bytes are one RAW access unit, not a self-framing stream:
// concatenating raw access units produces a byte stream no decoder
// accepts. Wrap each access unit in an ADTS header (AppendADTSHeader), or
// mux the access units into a container (MP4/M4A) using
// AudioSpecificConfig for the decoder configuration. The pcm package does
// the ADTS framing automatically and is the right layer for almost all
// callers.
//
// The encoder delays output by one frame (encoder priming): the first call
// appends nothing. Pass nil samples to drain; each nil call appends one
// remaining access unit until Drained reports true:
//
//	for !e.Drained() {
//	    dst, err = e.EncodeFrame(dst, nil)
//	}
//
// Input containing NaN or Inf returns an error satisfying
// errors.Is(err, ErrInvalidAudio) and appends nothing. The samples are
// checked at ingest, so the error surfaces on the same call that carried
// the bad samples. After ErrInvalidAudio the stream is unusable; Reset the
// encoder.
func (e *Encoder) EncodeFrame(dst []byte, samples [][]float32) ([]byte, error) {
	if e.enc == nil {
		return dst, ErrEncoderClosed // zero value or a failed Reset
	}
	if samples != nil {
		if len(samples) != e.channels {
			return dst, fmt.Errorf("go-aac: %d channel slices, want %d", len(samples), e.channels)
		}
		for ch := 1; ch < len(samples); ch++ {
			if len(samples[ch]) != len(samples[0]) {
				return dst, errors.New("go-aac: channel slices differ in length")
			}
		}
		if len(samples[0]) == 0 {
			return dst, errors.New("go-aac: empty frame; pass nil to flush")
		}
		if len(samples[0]) > FrameSize {
			return dst, fmt.Errorf("go-aac: frame of %d samples exceeds FrameSize (%d)", len(samples[0]), FrameSize)
		}
		if e.shortFrame {
			return dst, errors.New("go-aac: audio submitted after a short final frame; Reset to start a new stream")
		}
		if len(samples[0]) < FrameSize {
			e.shortFrame = true // only the final frame of a stream may be short
		}
	}
	out, err := e.enc.EncodeFrame(dst, samples)
	if err != nil {
		if errors.Is(err, enc.ErrInvalidAudio) {
			return dst, ErrInvalidAudio
		}
		return dst, err
	}
	return out, nil
}

// Drained reports whether a drain (EncodeFrame with nil samples) has
// flushed all buffered audio; once true, further nil calls append nothing.
func (e *Encoder) Drained() bool {
	if e.enc == nil {
		return true // an uninitialized encoder has nothing to drain
	}
	return e.enc.Drained()
}

// AudioSpecificConfig returns the MPEG-4 AudioSpecificConfig for this
// stream (2 bytes for AAC-LC), as carried in an MP4 esds box; raw-access-
// unit consumers hand it to their muxer or decoder. It returns a fresh
// copy on every call; callers may retain or mutate it freely. Mirrors
// aacenc.c:put_audio_specific_config @ d09d5afc3a.
func (e *Encoder) AudioSpecificConfig() []byte {
	if e.enc == nil {
		return nil // uninitialized: no config to report
	}
	return appendAudioSpecificConfig(nil, e.srIndex, e.channels)
}

// Stats returns a snapshot of the encoder's tool-usage counters,
// accumulated since NewEncoder or Reset. Call it after draining for
// whole-stream numbers.
func (e *Encoder) Stats() Stats {
	if e.enc == nil {
		return Stats{}
	}
	st := e.enc.Stats()
	return statsFromInternal(&st)
}
