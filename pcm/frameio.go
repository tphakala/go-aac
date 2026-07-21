// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"encoding/binary"
	"errors"
	"fmt"

	aac "github.com/tphakala/go-aac"
)

// emitFunc is the access-unit callback. It is an alias, so the public
// signatures spell the type out and godoc shows no indirection.
type emitFunc = func(au []byte, samples int) error

// errNilEmit reports a nil access-unit callback. It is a package-level value
// so reporting the misuse allocates nothing.
var errNilEmit = errors.New("go-aac/pcm: nil emit callback")

// FrameEncoder streams interleaved little-endian integer PCM (as []byte) to
// RAW AAC-LC access units, reporting each unit through a callback instead of
// writing a framed stream. It is the muxing counterpart of Encoder: where
// Encoder wraps every access unit in an ADTS header and writes it to an
// io.Writer, FrameEncoder hands the bare unit to the caller, who carries it in
// a container (an MP4 or fragmented-MP4 mp4a track, Matroska, and the like)
// that describes the boundaries out of band. AudioSpecificConfig supplies the
// esds DecoderSpecificInfo and Delay the edit-list priming count.
//
// The emit callback has the same shape as go-flac's pcm.FrameEncoder, so a
// muxer's per-unit path is shared between the two codecs. The lifecycle
// differs: AAC-LC needs a Flush to drain the priming frame, where the FLAC
// frame encoder is one-shot.
//
// FrameEncoder shares its whole implementation with Encoder: the same carry
// buffer, the same integer-to-float32 conversion, the same 1024-sample
// framing, priming and drain. The access units are therefore byte-identical
// to the payloads of the ADTS stream Encoder would write for the same input
// and Config.
//
// A FrameEncoder is not safe for concurrent use, and must not be copied after
// first use: a copy shares the codec state and the scratch buffers with the
// original.
type FrameEncoder struct {
	cfg Config

	enc *aac.Encoder // nil only on a zero value or after a failed Reset

	stride     int    // bytes per inter-channel sample (BitDepth/8 * channels)
	frameBytes int    // bytes in one full 1024-sample frame (stride * FrameSize)
	carry      []byte // buffered PCM bytes not yet a full frame (bounded to one frame)
	planar     [2][aac.FrameSize]float32
	frames     [2][]float32 // slice headers into planar, resliced per frame
	au         []byte       // raw access unit scratch, reused across emits

	// configured records that a Reset completed, so the encoder describes a
	// live stream. A zero value and a failed Reset are both unconfigured, and
	// every method reports that rather than acting on a stale stream: after a
	// rejected Reset the previous stream's aac.Encoder is still allocated (it
	// is the 650 KiB the next Reset reuses), so enc != nil does not mean the
	// encoder is usable.
	configured bool
	flushed    bool
	err        error // latched on the first failure; returned until Reset
}

// NewFrameEncoder validates cfg and returns a FrameEncoder ready to encode.
// A config error returns immediately, before any state is built.
func NewFrameEncoder(cfg Config) (*FrameEncoder, error) {
	e := &FrameEncoder{}
	if err := e.Reset(cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset rebinds the FrameEncoder to cfg so one encoder can encode many
// independent streams without re-allocating the roughly 650 KiB of codec
// state, and is the way to re-arm an encoder after a latched error or a
// Flush. It re-validates cfg, discards buffered input and resets all
// per-stream state. After a successful Reset the encoder is ready to encode
// as if freshly constructed; on error it is left unconfigured, and every
// method reports that until a later Reset succeeds.
func (e *FrameEncoder) Reset(cfg Config) error {
	e.disarm()
	if err := cfg.validate(); err != nil {
		return err
	}
	if e.enc == nil {
		enc, err := aac.NewEncoder(cfg.encoderConfig())
		if err != nil {
			return err
		}
		e.enc = enc
	} else if err := e.enc.Reset(cfg.encoderConfig()); err != nil {
		e.enc = nil
		return err
	}
	e.cfg = cfg
	e.stride = cfg.BitDepth / 8 * cfg.Channels
	e.frameBytes = e.stride * aac.FrameSize
	e.carry = e.carry[:0]
	if e.au == nil {
		e.au = make([]byte, 0, maxAUBytes)
	}
	e.flushed = false
	e.configured = true
	return nil
}

// maxAUBytes pre-sizes the access-unit scratch to the encoder's rate-control
// target of 6144 bits per channel per frame, at the maximum two channels. It
// is a sizing hint, not a bound: the ABR loop steers toward that ceiling
// rather than truncating at it and can return with a frame slightly over, so
// a very high stereo bitrate grows the buffer once (over 1550 bytes at 44.1
// kHz stereo, 640 kbps; the exact figure is signal-dependent). The enforced
// limit is the ADTS 13-bit length field, 8184 bytes; see
// aac.AppendADTSHeader.
const maxAUBytes = 6144 / 8 * 2

// disarm marks the encoder unconfigured and clears any latched error, so a
// zero value and a partially reset encoder both refuse to encode until a
// Reset completes.
func (e *FrameEncoder) disarm() {
	e.configured = false
	e.flushed = true
	e.err = nil
}

// EncodeInterleaved consumes interleaved little-endian PCM in arbitrary chunk
// sizes and calls emit once per complete access unit. Bytes that do not
// complete an inter-channel sample or a 1024-sample frame are buffered until
// the next call or Flush, so the emitted sequence depends only on the byte
// sequence, never on how it was chunked. It may be called any number of times;
// Flush ends the stream.
//
// The au slice is BORROWED: it aliases an internal scratch buffer that the
// next emit overwrites, so a muxer copies it or appends it to its own segment
// buffer before returning. samples is the access unit's decoded length, always
// aac.FrameSize for AAC-LC including the zero-padded final frame; it carries
// no information about how much of that final frame was real input. It is
// passed for signature parity with go-flac's pcm.FrameEncoder, whose block
// sizes genuinely vary.
//
// The encoder delays output by one frame (priming), so the first complete
// frame emits nothing; Flush drains it. An error from emit is returned
// unchanged and latched: no further access unit is emitted and every later
// call returns it until Reset.
func (e *FrameEncoder) EncodeInterleaved(pcm []byte, emit func(au []byte, samples int) error) error {
	_, err := e.encode(pcm, emit)
	return err
}

// encode is the shared core behind EncodeInterleaved and Encoder.Write. It
// returns the number of bytes of p durably consumed, which Encoder.Write needs
// to honour the io.Writer contract on a mid-write sink error.
func (e *FrameEncoder) encode(p []byte, emit emitFunc) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if !e.configured || e.flushed {
		return 0, aac.ErrEncoderClosed
	}
	if emit == nil {
		return 0, errNilEmit
	}
	total := len(p)

	// 1. Top off the carry to a full frame from the front of p.
	if len(e.carry) > 0 {
		need := e.frameBytes - len(e.carry) // >= 1: carry is always < one frame
		if len(p) < need {                  // still short of a full frame
			e.carry = append(e.carry, p...)
			return total, nil
		}
		e.carry = append(e.carry, p[:need]...)
		if err := e.encodeFrameBytes(e.carry, emit); err != nil {
			return 0, e.fail(err)
		}
		e.carry = e.carry[:0]
		p = p[need:]
	}

	// 2. Encode whole frames straight from p, no copy.
	for len(p) >= e.frameBytes {
		if err := e.encodeFrameBytes(p[:e.frameBytes], emit); err != nil {
			return total - len(p), e.fail(err)
		}
		p = p[e.frameBytes:]
	}

	// 3. Stash the sub-frame remainder (which may include a partial sample;
	// the input is byte-oriented) for next time.
	e.carry = append(e.carry, p...)
	return total, nil
}

// Flush encodes the trailing partial frame (zero-padded to a whole frame by
// the encoder) and drains the one-frame encoder lookahead, so the final
// samples reach the caller. It ends the stream: Flush is idempotent and
// reports the same outcome on every later call, and EncodeInterleaved
// afterwards returns aac.ErrEncoderClosed. Reset re-arms the encoder for
// another stream.
//
// A FrameEncoder that saw no input at all still flushes one all-silence
// access unit, so a muxed track is never empty. It errors if buffered
// trailing bytes are not a whole number of inter-channel samples, and reports
// aac.ErrEncoderClosed on a zero value or after a failed Reset. A nil emit
// callback is rejected without ending the stream, so the buffered tail
// survives for a Flush that supplies a real one.
func (e *FrameEncoder) Flush(emit func(au []byte, samples int) error) error {
	if !e.configured {
		return aac.ErrEncoderClosed // a zero value, or a Reset that failed
	}
	if e.flushed {
		// Report the latched outcome on every later call, so a failure during
		// the final drain is not masked by a success on retry.
		return e.err
	}
	if e.err != nil {
		e.flushed = true
		return e.err // a prior encode already broke the stream; nothing to flush
	}
	if emit == nil {
		// Reject before consuming the terminal transition, so a caller that
		// passes a nil callback by mistake can still flush the buffered tail
		// once it supplies a real one. EncodeInterleaved treats the same
		// misuse the same way.
		return errNilEmit
	}
	e.flushed = true
	e.err = e.finish(emit)
	return e.err
}

// finish flushes the trailing partial frame and drains the encoder delay. It
// backs Flush, which screens out the unconfigured cases and records the result
// so repeat calls are idempotent.
func (e *FrameEncoder) finish(emit emitFunc) error {
	if len(e.carry) > 0 {
		if len(e.carry)%e.stride != 0 {
			return fmt.Errorf("go-aac/pcm: %d trailing bytes are not a whole number of %d-byte samples", len(e.carry), e.stride)
		}
		n := len(e.carry) / e.stride
		e.convert(e.carry, n)
		if err := e.encodeFrame(n, emit); err != nil {
			return err
		}
		e.carry = e.carry[:0]
	}

	// Drain the encoder delay: each nil call yields one access unit until the
	// queue is empty. A stream with no input at all still drains to one
	// all-silence frame, so a flushed encoder always leaves a decodable track.
	for !e.enc.Drained() {
		var err error
		e.au, err = e.enc.EncodeFrame(e.au[:0], nil)
		if err != nil {
			return err
		}
		if err := e.emitAU(emit); err != nil {
			return err
		}
	}
	return nil
}

// fail latches err as the encoder's terminal error. encodeFrameBytes advances
// the codec state before emit can fail, so the failed access unit cannot be
// recovered by re-feeding PCM: every later call returns this error until Reset
// re-arms the encoder. Returns the latched error for convenience.
func (e *FrameEncoder) fail(err error) error {
	if e.err == nil {
		e.err = err
	}
	return e.err
}

// encodeFrameBytes converts one full frame of interleaved PCM bytes to planar
// float32, encodes it and reports the resulting access unit (if any; the
// priming frame produces none). chunk holds exactly frameBytes bytes.
func (e *FrameEncoder) encodeFrameBytes(chunk []byte, emit emitFunc) error {
	e.convert(chunk, aac.FrameSize)
	return e.encodeFrame(aac.FrameSize, emit)
}

// encodeFrame encodes planar[:n] and reports the access unit.
func (e *FrameEncoder) encodeFrame(n int, emit emitFunc) error {
	for ch := range e.cfg.Channels {
		e.frames[ch] = e.planar[ch][:n]
	}
	var err error
	e.au, err = e.enc.EncodeFrame(e.au[:0], e.frames[:e.cfg.Channels])
	if err != nil {
		return err
	}
	return e.emitAU(emit)
}

// convert deinterleaves and scales n input samples from chunk into the
// planar float32 buffers. The scale factors mirror FFmpeg's integer to
// fltp sample conversion (1/32768 for 16-bit and the 24/32-bit
// equivalents), so the low-level encoder sees exactly the values the C
// encoder would.
func (e *FrameEncoder) convert(chunk []byte, n int) {
	ch := e.cfg.Channels
	switch e.cfg.BitDepth {
	case 16:
		const scale = 1.0 / (1 << 15)
		for c := range ch {
			dst := e.planar[c][:n]
			for i := range n {
				v := int16(binary.LittleEndian.Uint16(chunk[(i*ch+c)*2:]))
				dst[i] = float32(v) * scale
			}
		}
	case 24:
		const scale = 1.0 / (1 << 23)
		for c := range ch {
			dst := e.planar[c][:n]
			for i := range n {
				o := (i*ch + c) * 3
				v := int32(chunk[o]) | int32(chunk[o+1])<<8 | int32(chunk[o+2])<<16
				v = (v << 8) >> 8 // sign-extend 24 bits
				dst[i] = float32(v) * scale
			}
		}
	case 32:
		const scale = 1.0 / (1 << 31)
		for c := range ch {
			dst := e.planar[c][:n]
			for i := range n {
				v := int32(binary.LittleEndian.Uint32(chunk[(i*ch+c)*4:]))
				dst[i] = float32(v) * scale
			}
		}
	}
}

// emitAU hands the pending access unit to emit. An empty access unit (the
// priming frame, or an exhausted drain) reports nothing. Every access unit
// carries aac.FrameSize samples per channel: a short final frame is zero-padded
// before encoding, so the unit still decodes to a full frame.
func (e *FrameEncoder) emitAU(emit emitFunc) error {
	if len(e.au) == 0 {
		return nil
	}
	return emit(e.au, aac.FrameSize)
}

// AudioSpecificConfig returns the MPEG-4 AudioSpecificConfig for this stream
// (2 bytes for AAC-LC), the DecoderSpecificInfo payload of an MP4 esds box.
// Valid immediately after NewFrameEncoder or a successful Reset, before any
// audio is encoded, so an init segment can be built up front; it is nil on a
// zero value or after a failed Reset, rather than describing the stream that
// Reset replaced. It returns a fresh copy on every call; callers may retain or
// mutate it freely.
func (e *FrameEncoder) AudioSpecificConfig() []byte {
	if !e.configured {
		return nil
	}
	return e.enc.AudioSpecificConfig()
}

// Delay reports the encoder priming delay in samples per channel: the leading
// samples a muxer trims, as an MP4 edit list media_time (scaled to the track
// timescale, which for audio is usually the sample rate) or an iTunSMPB
// priming field. It reads the delay from the underlying encoder, so a muxer
// holding a FrameEncoder never has to know the constant, and reports
// aac.EncoderDelay when there is no encoder to ask. Both branches return the
// same number today; the guard keeps aac.Encoder.Delay's freedom to
// dereference its receiver from becoming load-bearing here.
func (e *FrameEncoder) Delay() int {
	if !e.configured {
		return aac.EncoderDelay
	}
	return e.enc.Delay()
}

// Stats returns a snapshot of encoder tool usage (frames, short blocks,
// PNS/MS/IS/TNS band counts, mean lambda), mirroring the report FFmpeg's
// encoder prints at uninit. Call it after Flush for whole-stream numbers;
// Reset clears it.
func (e *FrameEncoder) Stats() aac.Stats {
	if !e.configured {
		return aac.Stats{}
	}
	return e.enc.Stats()
}
