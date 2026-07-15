// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"fmt"

	"github.com/tphakala/go-aac/internal/bits"
)

// Audio object types the parser distinguishes. Mirror enum AudioObjectType
// (libavcodec/mpeg4audio.h @ d09d5afc3a).
const (
	aotAACLC  = 2
	aotSBR    = 5
	aotPS     = 29
	aotEscape = 31
)

// getObjectType mirrors libavcodec/mpeg4audio.c:get_object_type
// @ d09d5afc3a.
func getObjectType(r *bits.Reader) int {
	t := int(r.Read(5))
	if t == aotEscape {
		t = 32 + int(r.Read(6))
	}
	return t
}

// getSampleRate mirrors libavcodec/mpeg4audio.c:get_sample_rate
// @ d09d5afc3a: 4-bit index, escape 0xf followed by a 24-bit explicit rate.
func getSampleRate(r *bits.Reader) (rate, index int) {
	index = int(r.Read(4))
	if index == 0xf {
		return int(r.Read(24)), index
	}
	return mpeg4SampleRates[index], index
}

// ParseASC parses an MPEG-4 AudioSpecificConfig for AAC-LC. It ports the
// AAC path of libavcodec/mpeg4audio.c:ff_mpeg4audio_get_config_gb plus the
// LC arm of libavcodec/aac/aacdec.c:decode_ga_specific_config @ d09d5afc3a,
// keeping the C's two-pass structure: the common header, one
// frame-length bit and the trailing SBR/PS sync-extension scan run on a
// copy of the reader; GASpecificConfig is then parsed from the
// specific-config position. Non-LC object types, chan_config 0
// (PCE-configured), chan_config > 2 and 960-sample frames return
// ErrUnsupported naming the offending value.
func ParseASC(asc []byte) (Config, error) {
	var c Config
	r := bits.NewReader(asc)

	// Pass 1: ff_mpeg4audio_get_config_gb on a reader copy.
	c.ObjectType = getObjectType(r)
	c.SampleRate, c.SamplingIndex = getSampleRate(r)
	c.ChanConfig = int(r.Read(4))
	// Report a truncated header as ErrInvalidData before the semantic
	// ErrUnsupported early returns below, so a short buffer whose garbage
	// bits happen to decode to an unsupported chan_config or object type is
	// still classified as truncation rather than "unsupported". The pass-two
	// reader (r2) applies the same guard for the specific-config tail.
	if err := r.Err(); err != nil {
		return c, fmt.Errorf("%w: truncated AudioSpecificConfig", ErrInvalidData)
	}
	if c.ChanConfig > 2 {
		return c, fmt.Errorf("%w: channel configuration %d", ErrUnsupported,
			c.ChanConfig)
	}
	c.SBR, c.PS = -1, -1
	if c.ObjectType == aotSBR || c.ObjectType == aotPS {
		// Explicit SBR/PS signalling wraps the real object type. Parsed
		// for a precise error; D0 decodes plain LC only.
		wrapper := c.ObjectType
		if wrapper == aotPS {
			c.PS = 1
		}
		c.SBR = 1
		_, _ = getSampleRate(r) // extension sample rate
		c.ObjectType = getObjectType(r)
		name := "SBR"
		if wrapper == aotPS {
			name = "PS"
		}
		return c, fmt.Errorf("%w: explicitly signalled %s (object type %d over %d)",
			ErrUnsupported, name, wrapper, c.ObjectType)
	}
	if c.ObjectType != aotAACLC {
		return c, fmt.Errorf("%w: audio object type %d (only AAC-LC is supported)",
			ErrUnsupported, c.ObjectType)
	}
	if c.SamplingIndex > 12 {
		return c, fmt.Errorf("%w: sampling index %d", ErrInvalidData,
			c.SamplingIndex)
	}
	specificConfigPos := r.Pos()
	c.FrameLenShort = r.ReadBit() != 0

	// Implicit SBR/PS sync extension: scanned from one bit into the
	// specific config, exactly where the C's copy reader stands
	// (ff_mpeg4audio_get_config_gb tail @ d09d5afc3a). The C guards the
	// scan with ext_object_type != AOT_SBR; the explicit-SBR branch above
	// always returns, so the guard is vacuously true here.
	for r.Left() > 15 {
		if r.Peek(11) == 0x2b7 {
			r.Skip(11)
			if getObjectType(r) == aotSBR {
				if c.SBR = int(r.ReadBit()); c.SBR == 1 {
					rate, _ := getSampleRate(r)
					if rate == c.SampleRate {
						c.SBR = -1
					}
				}
			}
			if r.Left() > 11 && r.Read(11) == 0x548 {
				c.PS = int(r.ReadBit())
			}
			break
		}
		r.Skip(1)
	}

	// Pass 2: GASpecificConfig from the specific-config position
	// (decode_ga_specific_config @ d09d5afc3a).
	r2 := bits.NewReader(asc)
	r2.Skip(specificConfigPos)
	c.FrameLenShort = r2.ReadBit() != 0
	if r2.ReadBit() != 0 { // dependsOnCoreCoder
		r2.Skip(14) // coreCoderDelay
	}
	extensionFlag := r2.ReadBit()
	if c.ChanConfig == 0 {
		return c, fmt.Errorf("%w: chan_config 0 (PCE-configured stream)",
			ErrUnsupported)
	}
	// count_channels(layout_map) > 1 clears PS; a single channel with
	// signalled SBR upgrades to PS (decode_ga_specific_config
	// @ d09d5afc3a). chan_config 1 is one channel, 2 is two.
	if c.ChanConfig > 1 {
		c.PS = 0
	} else if c.SBR == 1 && c.PS == -1 {
		c.PS = 1
	}
	if extensionFlag != 0 {
		r2.Skip(1) // extensionFlag3
	}
	if err := r2.Err(); err != nil {
		return c, fmt.Errorf("%w: truncated AudioSpecificConfig", ErrInvalidData)
	}
	if c.SBR == 1 {
		return c, fmt.Errorf("%w: sync-extension signalled SBR", ErrUnsupported)
	}
	if c.FrameLenShort {
		return c, fmt.Errorf("%w: 960-sample frames", ErrUnsupported)
	}
	return c, nil
}
