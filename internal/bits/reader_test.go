// SPDX-License-Identifier: LGPL-2.1-or-later

package bits

import (
	"math/rand/v2"
	"testing"
)

func TestReaderKnownBits(t *testing.T) {
	// 0xA5 0x3C = 1010 0101 0011 1100
	r := NewReader([]byte{0xA5, 0x3C})
	if got := r.Peek(4); got != 0xA {
		t.Fatalf("Peek(4) = %#x, want 0xA", got)
	}
	if got := r.Read(3); got != 0b101 {
		t.Fatalf("Read(3) = %#b, want 101", got)
	}
	if got := r.ReadBit(); got != 0 {
		t.Fatalf("ReadBit = %d, want 0", got)
	}
	if got := r.Read(6); got != 0b010100 {
		t.Fatalf("Read(6) = %#b, want 010100", got)
	}
	if got, want := r.Pos(), 10; got != want {
		t.Fatalf("Pos = %d, want %d", got, want)
	}
	r.Align()
	if got, want := r.Pos(), 16; got != want {
		t.Fatalf("Pos after Align = %d, want %d", got, want)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("Err = %v, want nil", err)
	}
}

func TestReaderZeroBits(t *testing.T) {
	r := NewReader([]byte{0xFF})
	if got := r.Read(0); got != 0 {
		t.Fatalf("Read(0) = %d, want 0", got)
	}
	if got := r.Pos(); got != 0 {
		t.Fatalf("Read(0) consumed %d bits", got)
	}
}

func TestReaderOverread(t *testing.T) {
	r := NewReader([]byte{0xFF})
	if got := r.Read(8); got != 0xFF {
		t.Fatalf("Read(8) = %#x", got)
	}
	// Peeking past the end is legal and returns zero bits (the C reader
	// sits inside its zero-initialized padding there).
	if got := r.Peek(16); got != 0 {
		t.Fatalf("Peek past end = %#x, want 0", got)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("peek must not latch overread: %v", err)
	}
	// Consuming past the end returns zeros and latches the error.
	if got := r.Read(4); got != 0 {
		t.Fatalf("Read past end = %#x, want 0", got)
	}
	if err := r.Err(); err == nil {
		t.Fatal("Err = nil after overread")
	}
	if got := r.Left(); got != 0 {
		t.Fatalf("Left = %d after clamped overread, want 0", got)
	}
	// The error is sticky across further reads.
	_ = r.Read(32)
	if err := r.Err(); err == nil {
		t.Fatal("overread error must be sticky")
	}
}

func TestReaderAlignAtBoundary(t *testing.T) {
	r := NewReader([]byte{0xAB, 0xCD})
	r.Skip(8)
	r.Align() // already aligned: must consume nothing
	if got := r.Pos(); got != 8 {
		t.Fatalf("Align at boundary moved to %d", got)
	}
}

// TestReaderRoundTrip writes random-width fields with the Writer and reads
// them back, covering every n in 1..32 and unaligned tails.
func TestReaderRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x1f2e3d4c, 42))
	for trial := range 100 {
		type field struct {
			n int
			v uint32
		}
		fields := make([]field, 0, 200)
		w := NewWriter(nil)
		for range 200 {
			n := 1 + rng.IntN(32)
			v := uint32(rng.Uint64()) & (1<<n - 1)
			fields = append(fields, field{n, v})
			w.Put(n, v)
		}
		buf := w.Flush()
		r := NewReader(buf)
		for i, f := range fields {
			if got := r.Read(f.n); got != f.v {
				t.Fatalf("trial %d field %d: Read(%d) = %#x, want %#x",
					trial, i, f.n, got, f.v)
			}
		}
		if err := r.Err(); err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
	}
}
