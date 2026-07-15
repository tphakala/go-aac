// SPDX-License-Identifier: LGPL-2.1-or-later

package vlc

import (
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/tables"
)

// TestSpectralExhaustive feeds every codeword of every spectral codebook
// through Decode and requires the exact packed symbol from
// ff_aac_codebook_vector_idx plus the exact consumed bit count.
func TestSpectralExhaustive(t *testing.T) {
	for cb := range 11 {
		codes := tables.SpectralCodes[cb]
		lens := tables.SpectralBits[cb]
		syms := tables.CodebookVectorIdx[cb]
		if len(codes) != len(lens) || len(codes) != len(syms) {
			t.Fatalf("cb%d: table length mismatch %d/%d/%d",
				cb+1, len(codes), len(lens), len(syms))
		}
		for j := range codes {
			w := bits.NewWriter(nil)
			w.Put(int(lens[j]), uint32(codes[j]))
			w.Put(7, 0x2a) // trailing bits must be left unconsumed
			r := bits.NewReader(w.Flush())
			sym, ok := Spectral[cb].Decode(r)
			if !ok {
				t.Fatalf("cb%d code %d: not decoded", cb+1, j)
			}
			if sym != uint32(syms[j]) {
				t.Fatalf("cb%d code %d: sym %#x, want %#x", cb+1, j, sym, syms[j])
			}
			if r.Pos() != int(lens[j]) {
				t.Fatalf("cb%d code %d: consumed %d bits, want %d",
					cb+1, j, r.Pos(), lens[j])
			}
		}
	}
}

// TestScalefactorExhaustive checks all 121 scalefactor codewords decode to
// their index (FFmpeg's implicit-symbol convention) with the exact length,
// including the 19-bit codes that exercise the two-level subtable path.
func TestScalefactorExhaustive(t *testing.T) {
	sawLong := false
	for j := range tables.ScalefactorCode {
		n := int(tables.ScalefactorBits[j])
		if n > 8 {
			sawLong = true
		}
		w := bits.NewWriter(nil)
		w.Put(n, tables.ScalefactorCode[j])
		w.Put(3, 5)
		r := bits.NewReader(w.Flush())
		sym, ok := Scalefactor.Decode(r)
		if !ok {
			t.Fatalf("scf code %d: not decoded", j)
		}
		if sym != uint32(j) {
			t.Fatalf("scf code %d: sym %d, want %d", j, sym, j)
		}
		if r.Pos() != n {
			t.Fatalf("scf code %d: consumed %d bits, want %d", j, r.Pos(), n)
		}
	}
	if !sawLong {
		t.Fatal("no scalefactor code longer than the root table width")
	}
}

// TestCodebooksComplete pins a property the error handling relies on:
// every AAC codebook at this pin is a COMPLETE prefix code (Kraft sum
// exactly 1), so Decode yields a symbol for every bit pattern and the
// not-ok branch is defensive only, exactly like the C's get_vlc2 which
// cannot fail on these books. It also pins the maximum code lengths the
// two-level table layout depends on (spectral max 16 bits, scalefactor 19;
// both fit rootBits + 16).
func TestCodebooksComplete(t *testing.T) {
	kraft := func(lens []uint8) (sum float64, maxn int) {
		for _, n := range lens {
			if int(n) > maxn {
				maxn = int(n)
			}
			sum += 1 / float64(uint64(1)<<n)
		}
		return sum, maxn
	}
	wantMax := [11]int{11, 9, 16, 12, 13, 11, 12, 10, 15, 12, 12}
	for cb := range 11 {
		sum, maxn := kraft(tables.SpectralBits[cb])
		if sum != 1 {
			t.Errorf("cb%d: Kraft sum %v, want exactly 1", cb+1, sum)
		}
		if maxn != wantMax[cb] {
			t.Errorf("cb%d: max code length %d, want %d", cb+1, maxn, wantMax[cb])
		}
	}
	sum, maxn := kraft(tables.ScalefactorBits[:])
	if sum != 1 {
		t.Errorf("scalefactors: Kraft sum %v, want exactly 1", sum)
	}
	if maxn != 19 {
		t.Errorf("scalefactors: max code length %d, want 19", maxn)
	}
}
