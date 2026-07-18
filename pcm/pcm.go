// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"fmt"

	aac "github.com/tphakala/go-aac"
)

// Config controls encoder output. It is a flat struct mirroring go-flac's
// pcm.Config and go-opus' oggopus.Config: every field's zero value is
// documented, so a literal with only SampleRate, BitDepth and Channels set
// is a complete, valid configuration.
type Config struct {
	// SampleRate is the input sample rate in Hz: 44100 or 48000 in v1.
	// Required; there is no zero default.
	SampleRate int
	// BitDepth is the input PCM bit depth: 16, 24 or 32 (signed integer,
	// little-endian, interleaved). Required; there is no zero default.
	BitDepth int
	// Channels is 1 or 2. Required; there is no zero default.
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

	// Coder selects the quantizer search strategy. The zero value is
	// aac.CoderNMR, the noise-to-mask-ratio trellis search that gives the
	// best quality per bit (the current behaviour). aac.CoderTwoLoop and
	// aac.CoderFast trade some quality for speed, with CoderFast the
	// fastest.
	Coder aac.Coder
}

// validate reports the first config problem, or nil. The checks match the
// low-level encoder's (which re-validates its own EncoderConfig) but carry
// this package's error messages, exactly as go-opus' oggopus validates its
// own Config rather than surfacing opus-prefixed errors.
func (c Config) validate() error {
	switch c.SampleRate {
	case 44100, 48000:
	default:
		return fmt.Errorf("go-aac/pcm: unsupported sample rate %d (supported: 44100, 48000)", c.SampleRate)
	}
	switch c.BitDepth {
	case 16, 24, 32:
	default:
		return fmt.Errorf("go-aac/pcm: unsupported bit depth %d (supported: 16, 24, 32)", c.BitDepth)
	}
	if c.Channels < 1 || c.Channels > 2 {
		return fmt.Errorf("go-aac/pcm: unsupported channel count %d (supported: 1, 2)", c.Channels)
	}
	if c.Bitrate < 0 {
		return fmt.Errorf("go-aac/pcm: negative bitrate %d", c.Bitrate)
	}
	if c.Cutoff < 0 || c.Cutoff > c.SampleRate/2 {
		return fmt.Errorf("go-aac/pcm: cutoff %d Hz outside 0..%d (half the sample rate)", c.Cutoff, c.SampleRate/2)
	}
	switch c.Coder {
	case aac.CoderNMR, aac.CoderTwoLoop, aac.CoderFast:
	default:
		return fmt.Errorf("go-aac/pcm: unknown coder %d", c.Coder)
	}
	return nil
}

// encoderConfig maps a pcm Config onto the low-level encoder config. The
// coder comes from Config.Coder (zero value aac.CoderNMR, upstream's
// default and the best quality per bit); every tool stays enabled, as the
// individual tool switches are a low-level aac option not exposed here.
func (c Config) encoderConfig() aac.EncoderConfig {
	return aac.EncoderConfig{
		SampleRate: c.SampleRate,
		Channels:   c.Channels,
		Bitrate:    c.Bitrate,
		Cutoff:     c.Cutoff,
		Coder:      c.Coder,
	}
}
