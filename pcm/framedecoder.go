// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/tphakala/go-aac/internal/dec"
)

// errNotInitialised reports use of a zero-value FrameDecoder. A FrameDecoder
// must come from NewADTSDecoder or NewRawDecoder; the zero value has no codec
// state to decode with. It is a package-level value so reporting the misuse
// allocates nothing.
var errNotInitialised = errors.New("go-aac/pcm: uninitialised FrameDecoder; use NewADTSDecoder or NewRawDecoder")

// FrameDecoder decodes AAC-LC access units one at a time to interleaved
// little-endian S16 PCM, for a caller driven by a push-style OnFrame(au []byte)
// callback rather than an io.Reader. It is the demuxing counterpart of
// FrameEncoder: where the reader Decoder pulls a framed stream through Read and
// WriteTo, FrameDecoder takes the discrete access units a transport (RTSP, HLS,
// a container demuxer) already carved out and returns the PCM for each, with no
// io.Pipe or reader goroutine in between.
//
// Two stream shapes are supported. NewADTSDecoder consumes self-describing ADTS
// frames (a 7-byte header, 9 with CRC, plus payload). NewRawDecoder consumes bare access units
// described out of band by an AudioSpecificConfig, the raw-stream case where the
// ASC arrives separately (an MP4 esds box, an RTSP OnCodecUpdate). Both decode
// through the same internal path as the reader Decoder, so the PCM is
// byte-identical to what Decoder produces for the same access units.
//
// A FrameDecoder is not safe for concurrent use, and must not be copied after
// first use: a copy shares the codec state and the scratch buffers with the
// original.
type FrameDecoder struct {
	dec *dec.Decoder
	asc []byte // retained (cloned) for a raw stream so Reset can re-derive the config; nil for ADTS
	raw bool
}

// NewADTSDecoder returns a FrameDecoder for a stream of self-describing ADTS
// access units. The configuration (sample rate, channel count) is learned from
// the first frame, so SampleRate and Channels report zero until the first
// DecodeFrame; a mid-stream configuration change is then rejected as
// ErrUnsupported.
func NewADTSDecoder() *FrameDecoder {
	return &FrameDecoder{dec: dec.NewADTS()}
}

// NewRawDecoder returns a FrameDecoder for raw access units described by asc, an
// MPEG-4 AudioSpecificConfig (the DecoderSpecificInfo of an MP4 esds box, or the
// config an RTSP/HLS session signals out of band). It parses asc up front, so an
// unsupported stream is reported here rather than at the first frame: it returns
// the typed ErrUnsupported, ErrUnsupportedSBR or ErrUnsupportedPS for a non-LC
// object type, a channel config above two, or HE-AAC (SBR/PS). asc is copied, so
// the caller may reuse or mutate the buffer afterwards.
func NewRawDecoder(asc []byte) (*FrameDecoder, error) {
	d, err := dec.NewRaw(asc)
	if err != nil {
		return nil, mapErr(err)
	}
	return &FrameDecoder{dec: d, asc: bytes.Clone(asc), raw: true}, nil
}

// DecodeFrame decodes one access unit and appends the interleaved little-endian
// S16 PCM to dst, returning the grown slice and the per-channel sample count
// (always aac.FrameSize, 1024, for AAC-LC). Passing dst[:0] of a reused buffer
// decodes in place: once dst has grown to a frame's worth of PCM
// (aac.FrameSize * channels * 2 bytes) no further call allocates on the common
// path, so a steady-state consumer decodes allocation-free.
//
// au is read-only and not retained. On error the returned slice is dst
// unchanged and the sample count is zero, and a decode error is one of the
// package sentinels (ErrCorruptStream for malformed input, including an access
// unit that carries no audio; ErrUnsupported and its SBR/PS refinements for a
// valid stream outside the AAC-LC scope), testable with errors.Is. A decode
// error does not consume decoder state: the failed unit leaves the
// configuration, overlap-add and PNS state untouched, so a caller may skip a
// corrupt access unit and decode the next, or Reset for a clean session.
// Calling DecodeFrame on a zero-value or nil FrameDecoder (one not built by a
// constructor) instead returns a non-sentinel initialisation error, which does
// not match those sentinels via errors.Is.
func (d *FrameDecoder) DecodeFrame(dst, au []byte) (out []byte, samples int, err error) {
	if d == nil || d.dec == nil {
		return dst, 0, errNotInitialised
	}
	// Whether the decoder already knew its configuration before this call. An
	// ADTS decoder learns it from a frame's header inside AppendS16, before the
	// audio elements are parsed, so a first frame that turns out to carry no
	// audio still leaves the decoder configured unless the rejection rolls back.
	configured := d.dec.Channels() != 0
	out, samples, err = d.dec.AppendS16(dst, au)
	if err != nil {
		// A first ADTS frame configures the decoder from its header before its
		// payload is decoded, so a payload error would otherwise leave that
		// header-derived config latched, for the same reason the no-element path
		// below rolls back. Undo it so a failed first unit consumes no state.
		if !d.raw && !configured {
			d.dec.ResetADTS()
		}
		return dst, 0, mapErr(err)
	}
	// An access unit with no audio channel element (only DSE/FIL, or a bare
	// TypeEnd) reconstructs to no audio, yet the internal decode still reports a
	// full frame of samples. Reject it so the caller is never handed a phantom
	// frame whose reported sample count does not match the PCM appended; no valid
	// AAC-LC audio frame omits its SCE/CPE. Guarding here rather than in the
	// shared internal decoder keeps the io.Reader Decoder's behaviour unchanged.
	if len(out)-len(dst) != samples*d.dec.Channels()*2 {
		// A first ADTS frame that carried no audio still configured the decoder
		// from its header. Undo that so the rejected unit consumes no state, as
		// the doc promises: otherwise a later valid frame with a different
		// configuration would be wrongly rejected as a mid-stream change. A raw
		// decoder's configuration comes from the ASC, not the frame, so it is
		// never rolled back.
		if !d.raw && !configured {
			d.dec.ResetADTS()
		}
		return dst, 0, fmt.Errorf("%w: no audio channel element in access unit", ErrCorruptStream)
	}
	return out, samples, nil
}

// SampleRate reports the stream sample rate in Hz, or zero before it is known.
// A raw decoder knows it from construction; an ADTS decoder learns it from the
// first decoded frame.
func (d *FrameDecoder) SampleRate() int {
	if d == nil || d.dec == nil {
		return 0
	}
	return d.dec.Config().SampleRate
}

// Channels reports the channel count (1 or 2), or zero before it is known. As
// with SampleRate, a raw decoder knows it from construction and an ADTS decoder
// from the first decoded frame.
func (d *FrameDecoder) Channels() int {
	if d == nil || d.dec == nil {
		return 0
	}
	return d.dec.Channels()
}

// Reset re-arms the decoder for a fresh session, reusing the codec allocations
// so a consumer that decodes many sessions can pool decoders. An ADTS decoder is
// returned to its pre-first-frame state, ready to learn the configuration of the
// next stream (not necessarily the same one, since ADTS is self-describing); a
// raw decoder is reconfigured from the retained ASC, so it stays bound to that
// configuration. A raw Reset can only fail if the retained ASC is unsupported,
// which NewRawDecoder already rejected, so in practice it returns nil.
func (d *FrameDecoder) Reset() error {
	if d == nil || d.dec == nil {
		return errNotInitialised
	}
	if d.raw {
		return mapErr(d.dec.ResetRaw(d.asc))
	}
	d.dec.ResetADTS()
	return nil
}

// ASCInfo describes an MPEG-4 AudioSpecificConfig: the audio object type, the
// sample rate in Hz, the channel count, and whether SBR or PS is signalled.
type ASCInfo struct {
	ObjectType int
	SampleRate int
	Channels   int
	SBR, PS    bool
}

// ParseASC parses an AudioSpecificConfig for a probe or codec-support decision
// before decoding, so a consumer can name the exact codec in an "unsupported"
// message instead of failing at the first frame. For AAC-LC it returns the
// object type, sample rate and channel count with a nil error. For a
// configuration this decoder cannot handle it returns a typed error, testable
// with errors.Is: ErrUnsupportedSBR for HE-AAC, ErrUnsupportedPS for HE-AACv2,
// and ErrUnsupported for a non-LC object type, a channel config above two, a
// PCE-configured stream (channel config zero), or 960-sample frames; malformed
// or truncated input returns ErrCorruptStream. On an ErrUnsupported result the
// returned ASCInfo is populated on a best-effort basis, so a caller may inspect
// the object type and the SBR/PS flags of a rejected config. On ErrCorruptStream
// it is the zero value, because a truncated header parses only overread garbage
// (an empty buffer would otherwise report sample-rate index 0, 96 kHz).
func ParseASC(asc []byte) (ASCInfo, error) {
	cfg, err := dec.ParseASC(asc)
	mapped := mapErr(err)
	if errors.Is(mapped, ErrCorruptStream) {
		return ASCInfo{}, mapped
	}
	info := ASCInfo{
		ObjectType: cfg.ObjectType,
		SampleRate: cfg.SampleRate,
		Channels:   cfg.ChanConfig,
		// PS implies SBR: a sync extension can reset SBR to -1 (extension rate
		// equals the core rate) while PS stays signalled, so derive SBR from
		// either flag rather than report a contradictory SBR=false, PS=true.
		SBR: cfg.SBR == 1 || cfg.PS == 1,
		PS:  cfg.PS == 1,
	}
	return info, mapped
}
