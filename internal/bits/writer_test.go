// SPDX-License-Identifier: LGPL-2.1-or-later
package bits

import (
	"bytes"
	"fmt"
	"testing"
)

// bitsToBytes turns a string of '0'/'1' into MSB-first bytes, zero-padded.
func bitsToBytes(t *testing.T, s string) []byte {
	t.Helper()
	for len(s)%8 != 0 {
		s += "0"
	}
	out := make([]byte, len(s)/8)
	for i := range len(s) {
		if s[i] == '1' {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

func TestWriterKnownPattern(t *testing.T) {
	w := NewWriter(make([]byte, 0, 16))
	w.Put(12, 0xfff)
	w.Put(1, 0)
	w.Put(2, 0)
	w.Put(1, 1)
	w.Put(2, 1)
	w.Put(4, 3)
	if got := w.Count(); got != 22 {
		t.Fatalf("Count() = %d, want 22", got)
	}
	got := w.Flush()
	want := bitsToBytes(t, "111111111111"+"0"+"00"+"1"+"01"+"0011")
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes = % x, want % x", got, want)
	}
}

func TestWriter32BitValueAfterOddOffset(t *testing.T) {
	w := NewWriter(nil)
	w.Put(3, 5)
	w.Put(32, 0xDEADBEEF)
	got := w.Flush()
	want := bitsToBytes(t, "101"+fmt.Sprintf("%032b", uint32(0xDEADBEEF)))
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes = % x, want % x", got, want)
	}
}

func TestWriterReset(t *testing.T) {
	w := NewWriter(nil)
	w.Put(8, 0xAA)
	w.Reset(nil)
	if w.Count() != 0 {
		t.Fatalf("Count() after Reset = %d, want 0", w.Count())
	}
	w.Put(4, 0xF)
	if got := w.Flush(); !bytes.Equal(got, []byte{0xF0}) {
		t.Fatalf("bytes = % x, want f0", got)
	}
}

// TestFlushCountsPadding pins put_bits_count semantics: FFmpeg computes it as
// (buf_ptr - buf) * 8 + BUF_BITS - bit_left, so once flush_put_bits has
// advanced the write pointer past the padded byte, the padding is counted.
// aacenc.c:1392 reads put_bits_count immediately after flush_put_bits to feed
// the bit reservoir, so Count must include the padding or the reservoir drifts
// low by up to 7 bits every frame.
func TestFlushCountsPadding(t *testing.T) {
	w := NewWriter(nil)
	w.Put(12, 0xfff)
	if got := w.Count(); got != 12 {
		t.Fatalf("Count() before Flush = %d, want 12", got)
	}
	buf := w.Flush()
	if got := w.Count(); got != 16 {
		t.Fatalf("Count() after Flush = %d, want 16 (12 bits + 4 padding)", got)
	}
	if got, want := w.Count(), len(buf)*8; got != want {
		t.Fatalf("after Flush, Count() = %d but len(buf)*8 = %d", got, want)
	}
}
