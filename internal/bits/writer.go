// SPDX-License-Identifier: LGPL-2.1-or-later

// Package bits implements an MSB-first bit writer with the semantics of
// FFmpeg's PutBitContext (libavcodec/put_bits.h @ d09d5afc3a).
package bits

// Writer writes bit fields MSB-first into a byte slice. It appends, growing
// the slice if needed; callers that preallocate capacity (the encoder bounds
// every AAC frame at 6144 bits per channel) get zero allocations.
type Writer struct {
	buf  []byte
	bits int    // total bits written
	acc  uint64 // holds n unflushed low bits
	n    int    // valid bits in acc (0..7 between calls)
}

// NewWriter returns a Writer that appends to buf, which may be nil.
func NewWriter(buf []byte) *Writer {
	w := &Writer{}
	w.Reset(buf)
	return w
}

// Reset re-arms the writer over buf (which may be nil), discarding state.
func (w *Writer) Reset(buf []byte) {
	w.buf = buf[:0]
	w.bits = 0
	w.acc = 0
	w.n = 0
}

// Put writes the low n bits of v, MSB first. n must be in 0..32.
func (w *Writer) Put(n int, v uint32) {
	if n == 0 {
		return
	}
	w.acc = w.acc<<uint(n) | uint64(v)&(uint64(1)<<uint(n)-1)
	w.n += n
	w.bits += n
	for w.n >= 8 {
		w.n -= 8
		w.buf = append(w.buf, byte(w.acc>>uint(w.n)))
	}
}

// Count returns the number of bits written so far, including unflushed bits
// and, once Flush has run, the zero padding it added.
// Mirrors put_bits_count (libavcodec/put_bits.h @ d09d5afc3a), which computes
// (buf_ptr - buf) * 8 + BUF_BITS - bit_left: after a flush the write pointer
// has advanced past the padded byte, so the padding is counted. The encoder
// relies on this: aacenc.c:1392 reads put_bits_count immediately after
// flush_put_bits to feed the bit reservoir and the NMR side-bits EMA.
func (w *Writer) Count() int { return w.bits }

// Flush zero-pads to a byte boundary and returns the written bytes.
// Mirrors flush_put_bits.
func (w *Writer) Flush() []byte {
	if w.n > 0 {
		pad := 8 - w.n
		w.buf = append(w.buf, byte(w.acc<<uint(pad)))
		w.bits += pad
		w.n = 0
		w.acc = 0
	}
	return w.buf
}
