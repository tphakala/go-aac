// SPDX-License-Identifier: LGPL-2.1-or-later

package bits

import "errors"

// ErrOverread reports that a Reader consumed bits past the end of its
// buffer. Mirrors the condition get_bits_left(gb) < 0 of FFmpeg's safe
// bitstream reader (libavcodec/get_bits.h @ d09d5afc3a, with
// CONFIG_SAFE_BITSTREAM_READER, the default), which clamps the position and
// returns zero bits; the Reader does the same but also latches this sticky
// error so a parser cannot mistake padding zeros for payload.
var ErrOverread = errors.New("bits: read past end of input")

// Reader reads bit fields MSB-first from a byte slice, mirroring the
// semantics of FFmpeg's GetBitContext (libavcodec/get_bits.h @ d09d5afc3a).
// Peeks past the end return zero bits without error, exactly like the C
// reader running inside its zero-initialized padding; consuming past the
// end clamps the position at the end and latches ErrOverread. Callers check
// Err at the same points the C checks get_bits_left, and once more when a
// frame is complete.
type Reader struct {
	buf      []byte
	pos      int // next bit to read
	size     int // total bits
	overread bool
}

// NewReader returns a Reader over buf. The caller must not mutate buf while
// reading.
func NewReader(buf []byte) *Reader {
	r := &Reader{}
	r.Reset(buf)
	return r
}

// Reset re-arms the Reader over buf, clearing position and error state.
func (r *Reader) Reset(buf []byte) {
	r.buf = buf
	r.pos = 0
	r.size = len(buf) * 8
	r.overread = false
}

// Peek returns the next n bits (0 <= n <= 32) without consuming them,
// zero-padded past the end of the buffer. Mirrors show_bits.
func (r *Reader) Peek(n int) uint32 {
	if n == 0 {
		return 0
	}
	i := r.pos >> 3
	var w uint64
	if i+8 <= len(r.buf) {
		w = uint64(r.buf[i])<<56 | uint64(r.buf[i+1])<<48 |
			uint64(r.buf[i+2])<<40 | uint64(r.buf[i+3])<<32 |
			uint64(r.buf[i+4])<<24 | uint64(r.buf[i+5])<<16 |
			uint64(r.buf[i+6])<<8 | uint64(r.buf[i+7])
	} else {
		for k := range 8 {
			w <<= 8
			if j := i + k; j < len(r.buf) {
				w |= uint64(r.buf[j])
			}
		}
	}
	w <<= uint(r.pos & 7)
	return uint32(w >> uint(64-n))
}

// Skip consumes n bits (n >= 0). Consuming past the end clamps at the end
// and latches ErrOverread. Mirrors skip_bits / skip_bits_long.
func (r *Reader) Skip(n int) {
	r.pos += n
	if r.pos > r.size {
		r.pos = r.size
		r.overread = true
	}
}

// Read consumes and returns the next n bits (0 <= n <= 32). Past the end it
// returns zero bits and latches ErrOverread. Mirrors get_bits (and
// get_bits_long: n = 0 is legal and returns 0).
func (r *Reader) Read(n int) uint32 {
	v := r.Peek(n)
	r.Skip(n)
	return v
}

// ReadBit consumes and returns one bit. Mirrors get_bits1.
func (r *Reader) ReadBit() uint32 {
	return r.Read(1)
}

// Align advances to the next byte boundary. Mirrors align_get_bits.
func (r *Reader) Align() {
	r.Skip(-r.pos & 7)
}

// Pos returns the number of bits consumed. Mirrors get_bits_count.
func (r *Reader) Pos() int { return r.pos }

// Left returns the number of bits remaining. It never goes negative; the
// overread condition C code detects via get_bits_left(gb) < 0 is reported
// by Err instead.
func (r *Reader) Left() int { return r.size - r.pos }

// Err returns ErrOverread once any read or skip has consumed bits past the
// end of the buffer, nil otherwise.
func (r *Reader) Err() error {
	if r.overread {
		return ErrOverread
	}
	return nil
}
