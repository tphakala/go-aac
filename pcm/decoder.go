// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/tphakala/go-aac/internal/dec"
)

var (
	_ io.Reader   = (*Decoder)(nil)
	_ io.WriterTo = (*Decoder)(nil)
)

// Sentinel decode errors, testable with errors.Is. They wrap the internal
// dec sentinels so a caller can branch on class without importing internal
// packages.
var (
	// ErrCorruptStream reports malformed input: a missing syncword with no
	// resync, a bad header field, an overread, or a truncated frame.
	ErrCorruptStream = errors.New("go-aac/pcm: corrupt stream")
	// ErrUnsupported reports well-formed input outside the AAC-LC mono/stereo
	// scope (non-LC object types, channel configs > 2, SBR/PS, 960-sample
	// frames, mid-stream config changes).
	ErrUnsupported = errors.New("go-aac/pcm: unsupported stream")
)

// Info describes the decoded stream, populated at construction.
type Info struct {
	SampleRate int    // Hz
	Channels   int    // 1 (mono) or 2 (stereo)
	Profile    string // always "AAC-LC" in v1
}

// Decoder decodes an AAC-LC stream into interleaved little-endian S16 PCM. It
// implements io.Reader and io.WriterTo, mirroring go-flac's pcm.Decoder shape
// (NewDecoder, Info, Read, WriteTo, Reset).
//
// A Decoder is not safe for concurrent use.
type Decoder struct {
	br   *bufio.Reader
	dec  *dec.Decoder
	info Info
	raw  bool

	frame   []byte // one access unit (ADTS: header+payload), reused
	buf     []byte // packed PCM backing buffer, reused across frames
	pending []byte // unread window into buf
	done    bool
	err     error // latched terminal error; returned until Reset
}

type config struct {
	asc []byte
	raw bool
}

// Option configures a Decoder.
type Option func(*config)

// WithRawStream configures the decoder for raw AAC access units described by
// the given AudioSpecificConfig instead of ADTS. Raw streams carry no
// syncword, so this explicit opt-in is required; without it NewDecoder treats
// the input as ADTS. The raw access units are length-prefixed (2-byte
// big-endian, the go-aac framing convention); see the package docs for the
// framing contract.
func WithRawStream(asc []byte) Option {
	return func(c *config) {
		c.asc = bytes.Clone(asc)
		c.raw = true
	}
}

// NewDecoder reads enough of r to establish the stream configuration and
// returns a Decoder with Info populated. For ADTS input it resyncs past any
// leading garbage to the first valid frame header. A nil reader, a stream
// with no decodable frame, or an unsupported configuration returns an error.
func NewDecoder(r io.Reader, opts ...Option) (*Decoder, error) {
	d := &Decoder{}
	if err := d.Reset(r, opts...); err != nil {
		return nil, err
	}
	return d, nil
}

// Reset rebinds the Decoder to a new source and reconfigures it, reusing the
// internal buffers and the internal decoder's allocations so a consumer that
// decodes many clips can pool decoders with zero per-stream allocation after
// warm-up.
func (d *Decoder) Reset(r io.Reader, opts ...Option) error {
	if r == nil {
		return fmt.Errorf("go-aac/pcm: nil reader")
	}
	var c config
	for _, o := range opts {
		o(&c)
	}
	if d.br == nil {
		d.br = bufio.NewReaderSize(r, 1<<16)
	} else {
		d.br.Reset(r)
	}
	d.buf = d.buf[:0]
	d.pending = nil
	d.done = false
	d.err = nil
	d.raw = c.raw
	d.info = Info{} // cleared up front so a failed Reset leaves no stale metadata

	if c.raw {
		if err := d.resetRawDec(c.asc); err != nil {
			// Latch the failure so a pooled decoder whose caller ignored this
			// error cannot then decode against the previous stream's config;
			// Read and WriteTo return d.err until the next successful Reset.
			d.err = mapErr(err)
			return d.err
		}
		cfg := d.dec.Config()
		d.info = Info{SampleRate: cfg.SampleRate, Channels: cfg.ChanConfig, Profile: "AAC-LC"}
		return nil
	}

	d.resetADTSDec()
	h, err := d.syncADTS()
	if err != nil {
		// At construction the stream must yield at least one valid ADTS header
		// to populate Info; a clean EOF here means no syncword was ever found,
		// which is a corrupt (or empty) stream rather than a normal end. Latch
		// it so a reused decoder does not silently proceed on a bad Reset.
		if errors.Is(err, io.EOF) {
			d.err = fmt.Errorf("%w: no ADTS syncword found", ErrCorruptStream)
		} else {
			d.err = mapErr(err)
		}
		return d.err
	}
	d.info = Info{SampleRate: h.SampleRate, Channels: h.ChanConfig, Profile: "AAC-LC"}
	return nil
}

// resetADTSDec reuses the internal ADTS decoder in place when pooling, or
// allocates one on first use.
func (d *Decoder) resetADTSDec() {
	if d.dec == nil {
		d.dec = dec.NewADTS()
		return
	}
	d.dec.ResetADTS()
}

// resetRawDec reuses the internal raw decoder in place when pooling, or
// allocates one on first use.
func (d *Decoder) resetRawDec(asc []byte) error {
	if d.dec == nil {
		dc, err := dec.NewRaw(asc)
		if err != nil {
			return err
		}
		d.dec = dc
		return nil
	}
	return d.dec.ResetRaw(asc)
}

// Info returns the stream configuration. Valid after NewDecoder or Reset.
func (d *Decoder) Info() Info { return d.info }

// syncADTS advances the reader to the next position where a full ADTS header
// parses, discarding leading garbage, and returns that header WITHOUT
// consuming the frame (the caller reads the frame body afterward). It returns
// io.EOF at a clean frame boundary (nothing left) and ErrCorruptStream when
// the input ends mid-header.
func (d *Decoder) syncADTS() (dec.ADTSHeader, error) {
	for {
		hdr, err := d.br.Peek(dec.ADTSHeaderSize)
		if err != nil {
			// A genuine reader error is surfaced unchanged rather than relabeled
			// as EOF or corruption. The two end-of-input errors (io.EOF and, from
			// a reader that emits it, io.ErrUnexpectedEOF) fall through to the
			// truncation handling below, consistent with wrapTruncation.
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return dec.ADTSHeader{}, err
			}
			if len(hdr) == 0 {
				return dec.ADTSHeader{}, io.EOF // clean end
			}
			// Fewer than a full header remains at EOF. If a syncword could
			// still start in these bytes we cannot complete it: report
			// truncation rather than silently dropping a partial final frame.
			if bytesContainSyncStart(hdr) {
				return dec.ADTSHeader{}, fmt.Errorf("%w: truncated ADTS header (%d bytes)", ErrCorruptStream, len(hdr))
			}
			return dec.ADTSHeader{}, io.EOF // trailing junk shorter than a header
		}
		h, perr := dec.ParseADTSHeaderBytes(hdr)
		if perr == nil {
			return h, nil
		}
		if _, derr := d.br.Discard(1); derr != nil {
			return dec.ADTSHeader{}, fmt.Errorf("%w: no ADTS syncword found", ErrCorruptStream)
		}
	}
}

// bytesContainSyncStart reports whether buf could be the prefix of an ADTS
// syncword (0xFF followed by a byte whose high nibble is 0xF).
func bytesContainSyncStart(buf []byte) bool {
	for i := range len(buf) {
		if buf[i] != 0xff {
			continue
		}
		if i+1 >= len(buf) || buf[i+1]>>4 == 0xf {
			return true
		}
	}
	return false
}

// wrapTruncation classifies an io.ReadFull error raised while pulling a frame
// body or its length prefix. A short read (io.EOF or io.ErrUnexpectedEOF) means
// the stream was truncated: it is wrapped as ErrCorruptStream with the
// underlying error preserved for errors.Is. Any other error is a genuine
// transport failure of the underlying reader and is surfaced unchanged so the
// caller can inspect its real cause instead of seeing it relabeled as
// corruption.
func wrapTruncation(what string, got, want int, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: truncated %s (%d of %d bytes): %w", ErrCorruptStream, what, got, want, err)
	}
	return err
}

// nextFrameADTS resyncs to the next header and reads the whole frame into
// d.frame.
func (d *Decoder) nextFrameADTS() ([]byte, error) {
	h, err := d.syncADTS()
	if err != nil {
		return nil, err
	}
	flen := h.FrameLength
	if cap(d.frame) < flen {
		d.frame = make([]byte, flen)
	}
	d.frame = d.frame[:flen]
	n, rerr := io.ReadFull(d.br, d.frame)
	if rerr != nil {
		return nil, wrapTruncation("final frame", n, flen, rerr)
	}
	return d.frame, nil
}

// nextFrameRaw reads one length-prefixed raw access unit into d.frame.
func (d *Decoder) nextFrameRaw() ([]byte, error) {
	var lp [2]byte
	n, err := io.ReadFull(d.br, lp[:])
	if err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return nil, io.EOF // clean end between access units
		}
		return nil, wrapTruncation("raw length prefix", n, len(lp), err)
	}
	flen := int(lp[0])<<8 | int(lp[1])
	if cap(d.frame) < flen {
		d.frame = make([]byte, flen)
	}
	d.frame = d.frame[:flen]
	if bn, berr := io.ReadFull(d.br, d.frame); berr != nil {
		return nil, wrapTruncation("raw access unit", bn, flen, berr)
	}
	return d.frame, nil
}

// decodeNextFrame decodes one frame, packing its PCM into d.buf; d.pending is
// the window Read/WriteTo drain. Returns io.EOF at a clean stream end.
func (d *Decoder) decodeNextFrame() error {
	if d.done {
		return io.EOF
	}
	var (
		frame []byte
		err   error
	)
	if d.raw {
		frame, err = d.nextFrameRaw()
	} else {
		frame, err = d.nextFrameADTS()
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			d.done = true
			return io.EOF
		}
		d.err = err
		return err
	}
	d.buf, _, err = d.dec.AppendS16(d.buf[:0], frame)
	if err != nil {
		d.err = mapErr(err)
		return d.err
	}
	d.pending = d.buf
	return nil
}

// Read fills p with interleaved little-endian S16 PCM. It returns (0, io.EOF)
// at a clean stream end and returns any latched terminal error on every later
// call.
func (d *Decoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.err != nil {
		return 0, d.err
	}
	for len(d.pending) == 0 {
		if err := d.decodeNextFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.pending)
	d.pending = d.pending[n:]
	return n, nil
}

// WriteTo drains all decoded PCM into w. It implements io.WriterTo so
// io.Copy(w, decoder) streams the whole decode with one call.
func (d *Decoder) WriteTo(w io.Writer) (int64, error) {
	if d.err != nil {
		return 0, d.err
	}
	var total int64
	if len(d.pending) > 0 {
		n, err := w.Write(d.pending)
		total += int64(n)
		d.pending = d.pending[n:]
		if err != nil {
			return total, err
		}
	}
	for {
		err := d.decodeNextFrame()
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		n, werr := w.Write(d.pending)
		total += int64(n)
		d.pending = d.pending[n:]
		if werr != nil {
			return total, werr
		}
	}
}

// mapErr wraps an internal dec error in the matching pcm sentinel so callers
// branch on ErrCorruptStream / ErrUnsupported with errors.Is.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, dec.ErrUnsupported):
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	case errors.Is(err, dec.ErrSync), errors.Is(err, dec.ErrInvalidData):
		return fmt.Errorf("%w: %w", ErrCorruptStream, err)
	default:
		return err
	}
}
