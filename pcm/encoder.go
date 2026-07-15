// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"encoding/binary"
	"fmt"
	"io"

	aac "github.com/tphakala/go-aac"
)

// Encoder implements io.WriteCloser, byte-for-byte the go-flac pcm.Encoder
// and go-opus oggopus.Encoder shape.
var _ io.WriteCloser = (*Encoder)(nil)

// Encoder streams interleaved little-endian integer PCM (as []byte) to an
// AAC-LC ADTS stream on an io.Writer. It is shaped exactly like go-flac's
// pcm.Encoder and go-opus' oggopus.Encoder: a flat Config,
// NewEncoder(w, cfg), Reset(w, cfg) for pooling, Write/Close, and a
// one-shot EncodeInterleaved.
//
// ADTS is self-framing (one 7-byte header per 1024-sample frame), so the
// Encoder is correct on a plain io.Writer with no seeking, no length known
// up front and no finalization step; Close only drains buffered samples.
//
// An Encoder is not safe for concurrent use.
type Encoder struct {
	w   io.Writer
	cfg Config

	enc *aac.Encoder // nil only on a zero value or after a failed Reset

	bytesPS    int    // bytes per sample per channel (BitDepth / 8)
	stride     int    // bytes per inter-channel sample (bytesPS * channels)
	frameBytes int    // bytes in one full 1024-sample frame (stride * FrameSize)
	carry      []byte // buffered PCM bytes not yet a full frame (bounded to one frame)
	planar     [2][aac.FrameSize]float32
	frames     [2][]float32 // slice headers into planar, resliced per frame
	au         []byte       // raw access unit scratch, reused
	out        []byte       // ADTS frame scratch (header + access unit), reused

	closed bool
	err    error // latched on the first Write or Close failure; returned until Reset
}

// NewEncoder validates cfg and returns an Encoder writing an ADTS stream
// to w. Unlike go-flac, no io.WriteSeeker is ever needed: ADTS has no
// stream-level header to patch. A config error returns immediately, before
// a byte is written.
func NewEncoder(w io.Writer, cfg Config) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(w, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset rebinds the Encoder to a new sink w and reconfigures it with cfg
// so one Encoder can encode many independent streams without
// re-allocating; this is the pooling path for the many-short-clips
// workload. It re-validates cfg, discards buffered input and resets all
// per-stream state. After a successful Reset the encoder is ready for
// Write/Close as if freshly constructed; on error it must not be used.
// Reset may be called on a closed encoder, which is the usual pooling
// pattern (Reset, Write, Close, repeat).
func (e *Encoder) Reset(w io.Writer, cfg Config) error {
	e.closed = true // a partially reset encoder must refuse Write
	e.err = nil     // clear any latched terminal error from the previous stream
	if w == nil {
		return fmt.Errorf("go-aac/pcm: nil writer")
	}
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
	e.w = w
	e.cfg = cfg
	e.bytesPS = cfg.BitDepth / 8
	e.stride = e.bytesPS * cfg.Channels
	e.frameBytes = e.stride * aac.FrameSize
	e.carry = e.carry[:0]
	for ch := range cfg.Channels {
		e.frames[ch] = e.planar[ch][:]
	}
	if e.au == nil {
		e.au = make([]byte, 0, 6144/8*2)
	}
	if e.out == nil {
		e.out = make([]byte, 0, 6144/8*2+adtsHeaderLen)
	}
	e.closed = false
	return nil
}

// adtsHeaderLen is the fixed ADTS header size, for scratch buffer sizing.
const adtsHeaderLen = 7

// Write consumes interleaved little-endian PCM in arbitrary chunk sizes,
// exactly like go-flac: bytes that do not yet complete an inter-channel
// sample or a 1024-sample frame are buffered until the next Write or
// Close, so io.Copy works with any buffer size (including ones not
// divisible by the sample stride, e.g. 32 KiB chunks of 24-bit stereo).
// The produced stream depends only on the byte sequence, never on how it
// was chunked. Write returns len(p) on success; on a mid-write sink error
// it returns the number of bytes of p that were durably consumed
// (io.Writer contract).
func (e *Encoder) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if e.closed || e.enc == nil {
		return 0, aac.ErrEncoderClosed
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
		if err := e.encodeFrameBytes(e.carry); err != nil {
			return 0, e.fail(err)
		}
		e.carry = e.carry[:0]
		p = p[need:]
	}

	// 2. Encode whole frames straight from p, no copy.
	for len(p) >= e.frameBytes {
		if err := e.encodeFrameBytes(p[:e.frameBytes]); err != nil {
			return total - len(p), e.fail(err)
		}
		p = p[e.frameBytes:]
	}

	// 3. Stash the sub-frame remainder (which may include a partial
	// sample; Write is byte-oriented) for next time.
	e.carry = append(e.carry, p...)
	return total, nil
}

// encodeFrameBytes converts one full frame of interleaved PCM bytes to
// planar float32, encodes it and writes the ADTS-framed result (if any;
// the priming frame produces none) to the sink. chunk holds exactly
// frameBytes bytes.
func (e *Encoder) encodeFrameBytes(chunk []byte) error {
	e.convert(chunk, aac.FrameSize)
	return e.encodeFrame(aac.FrameSize)
}

// encodeFrame encodes planar[:n] and writes the framed access unit.
func (e *Encoder) encodeFrame(n int) error {
	for ch := range e.cfg.Channels {
		e.frames[ch] = e.planar[ch][:n]
	}
	var err error
	e.au, err = e.enc.EncodeFrame(e.au[:0], e.frames[:e.cfg.Channels])
	if err != nil {
		return err
	}
	return e.writeAU()
}

// writeAU wraps the pending access unit in an ADTS header and writes
// header plus payload to the sink in a single Write. An empty access unit
// (the priming frame, or an exhausted drain) writes nothing.
func (e *Encoder) writeAU() error {
	if len(e.au) == 0 {
		return nil
	}
	var err error
	e.out, err = aac.AppendADTSHeader(e.out[:0], e.cfg.SampleRate, e.cfg.Channels, len(e.au))
	if err != nil {
		return fmt.Errorf("go-aac/pcm: %w", err)
	}
	e.out = append(e.out, e.au...)
	if _, err := e.w.Write(e.out); err != nil {
		return err
	}
	return nil
}

// convert deinterleaves and scales n input samples from chunk into the
// planar float32 buffers. The scale factors mirror FFmpeg's integer to
// fltp sample conversion (1/32768 for 16-bit and the 24/32-bit
// equivalents), so the low-level encoder sees exactly the values the C
// encoder would.
func (e *Encoder) convert(chunk []byte, n int) {
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

// Close encodes the final partial frame (zero-padded to a whole frame by
// the encoder), drains the one-frame encoder delay and flushes the last
// access units. The stream needs no finalization beyond this: ADTS has no
// stream-level state. Close is idempotent; Write after Close returns
// ErrEncoderClosed. It errors if buffered trailing bytes are not a whole
// number of inter-channel samples.
func (e *Encoder) Close() error {
	if e.closed {
		// Report the latched outcome on every later call, so a failure during
		// the final flush is not masked by a success on retry.
		return e.err
	}
	e.closed = true
	if e.err != nil {
		return e.err // a prior Write already broke the stream; nothing to flush
	}
	e.err = e.finish()
	return e.err
}

// fail latches err as the encoder's terminal error. encodeFrameBytes advances
// the codec state before writeAU can fail, so the failed access unit cannot be
// recovered by re-feeding PCM: every later Write and Close returns this error
// until Reset re-arms the encoder. Returns the latched error for convenience.
func (e *Encoder) fail(err error) error {
	if e.err == nil {
		e.err = err
	}
	return e.err
}

// finish flushes the trailing partial frame and drains the encoder delay. It
// backs Close, which records its result so repeat calls are idempotent.
func (e *Encoder) finish() error {
	if e.enc == nil {
		return aac.ErrEncoderClosed
	}

	if len(e.carry) > 0 {
		if len(e.carry)%e.stride != 0 {
			return fmt.Errorf("go-aac/pcm: %d trailing bytes are not a whole number of %d-byte samples", len(e.carry), e.stride)
		}
		n := len(e.carry) / e.stride
		e.convert(e.carry, n)
		if err := e.encodeFrame(n); err != nil {
			return err
		}
		e.carry = e.carry[:0]
	}

	// Drain the encoder delay: each nil call yields one access unit until
	// the queue is empty. A stream with no input at all still drains to
	// one all-silence frame, so Close always leaves a decodable stream.
	for !e.enc.Drained() {
		var err error
		e.au, err = e.enc.EncodeFrame(e.au[:0], nil)
		if err != nil {
			return err
		}
		if err := e.writeAU(); err != nil {
			return err
		}
	}
	return nil
}

// AudioSpecificConfig returns the MPEG-4 AudioSpecificConfig for this
// stream (2 bytes for AAC-LC), the decoder configuration a future MP4/M4A
// muxing layer needs. Valid after NewEncoder or Reset. It returns a fresh
// copy on every call; callers may retain or mutate it freely.
func (e *Encoder) AudioSpecificConfig() []byte {
	if e.enc == nil {
		return nil
	}
	return e.enc.AudioSpecificConfig()
}

// Stats returns a snapshot of encoder tool usage (frames, short blocks,
// PNS/MS/IS/TNS band counts, mean lambda), mirroring the report FFmpeg's
// encoder prints at uninit. Call it after Close for whole-stream numbers;
// Reset clears it.
func (e *Encoder) Stats() aac.Stats {
	if e.enc == nil {
		return aac.Stats{}
	}
	return e.enc.Stats()
}
