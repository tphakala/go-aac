// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
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
// A caller muxing into MP4 instead of writing a stream wants the raw access
// units: use FrameEncoder, which Encoder is built on.
//
// An Encoder is not safe for concurrent use, and must not be copied after
// first use: a copy shares the codec state and the scratch buffers with the
// original.
type Encoder struct {
	// fe holds the whole encoding pipeline: carry buffer, integer to
	// float32 conversion, framing, priming and drain. Encoder adds only
	// the ADTS framing and the sink.
	fe FrameEncoder

	w   io.Writer
	out []byte // ADTS frame scratch (header + access unit), reused
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
	// Detach from the previous stream before rebinding, so every failure path
	// below leaves the encoder unconfigured AND holding no reference to the
	// sink it was replacing, rather than pinning it until the next success.
	e.release()
	if w == nil {
		return fmt.Errorf("go-aac/pcm: nil writer")
	}
	if err := e.fe.Reset(cfg); err != nil { // Reset disarms again before validating
		return err
	}
	e.w = w
	if e.out == nil {
		e.out = make([]byte, 0, maxAUBytes+adtsHeaderLen)
	}
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
	return e.fe.encode(p, e.writeAU)
}

// writeAU wraps one access unit in an ADTS header and writes header plus
// payload to the sink in a single Write. It is the emit callback the
// FrameEncoder core calls per access unit; the samples count is implicit in
// ADTS, so it is ignored. Write and Close pass it as a method value rather
// than caching one, which costs no allocation because the core never lets the
// callback escape, and which keeps a copied Encoder from writing to the
// original's sink.
func (e *Encoder) writeAU(au []byte, _ int) error {
	var err error
	e.out, err = aac.AppendADTSHeader(e.out[:0], e.fe.cfg.SampleRate, e.fe.cfg.Channels, len(au))
	if err != nil {
		return fmt.Errorf("go-aac/pcm: %w", err)
	}
	e.out = append(e.out, au...)
	if _, err := e.w.Write(e.out); err != nil {
		return err
	}
	return nil
}

// Close encodes the final partial frame (zero-padded to a whole frame by
// the encoder), drains the one-frame encoder delay and flushes the last
// access units. The stream needs no finalization beyond this: ADTS has no
// stream-level state. Close is idempotent; Write after Close returns
// ErrEncoderClosed. It errors if buffered trailing bytes are not a whole
// number of inter-channel samples.
func (e *Encoder) Close() error {
	return e.fe.Flush(e.writeAU)
}

// AudioSpecificConfig returns the MPEG-4 AudioSpecificConfig for this
// stream (2 bytes for AAC-LC), the decoder configuration an MP4/M4A muxing
// layer needs. Valid after NewEncoder or Reset. It returns a fresh copy on
// every call; callers may retain or mutate it freely.
func (e *Encoder) AudioSpecificConfig() []byte {
	return e.fe.AudioSpecificConfig()
}

// release drops every reference to the caller's stream and disarms the
// encoder. It backs both the pooled EncodeInterleaved teardown and the head of
// Reset: an encoder that is idle in the pool, or whose Reset failed, must pin
// neither the caller's sink nor a latched error (which wraps whatever that
// sink put in it), and must not be able to encode into the released sink
// should any future path reach Write before the next Reset re-arms it.
func (e *Encoder) release() {
	e.w = nil
	e.fe.disarm()
}

// Delay reports the encoder priming delay in samples per channel, the leading
// samples a muxer trims. ADTS cannot signal it, so it matters only to a caller
// that also muxes the same audio into a container; see FrameEncoder.Delay.
func (e *Encoder) Delay() int {
	return e.fe.Delay()
}

// Stats returns a snapshot of encoder tool usage (frames, short blocks,
// PNS/MS/IS/TNS band counts, mean lambda), mirroring the report FFmpeg's
// encoder prints at uninit. Call it after Close for whole-stream numbers;
// Reset clears it.
func (e *Encoder) Stats() aac.Stats {
	return e.fe.Stats()
}
