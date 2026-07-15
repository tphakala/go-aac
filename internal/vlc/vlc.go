// SPDX-License-Identifier: LGPL-2.1-or-later

// Package vlc implements a table-driven decoder for the variable-length
// codes of the AAC bitstream. The codebook DATA (codes, lengths, symbols)
// comes verbatim from internal/tables, which pins it byte-for-byte against
// the pinned FFmpeg tree; the lookup structure here is deliberately NOT a
// copy of FFmpeg's vlc.c multi-level tables. docs/decoder-analysis.md
// requires symbol-stream correctness, not structural fidelity, and the
// decoder below is validated by exhaustive codeword round-trips plus the
// tools/cdec symbol-stream gate.
package vlc

import (
	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/tables"
)

// rootBits is the first-level table width. All AAC codebooks have maximum
// code lengths <= rootBits+16, so two levels always suffice (spectral books
// max 16 bits at this pin, cb3; scalefactor book 19 bits; pinned by
// TestCodebooksComplete).
const rootBits = 8

type entry struct {
	// n > 0: valid codeword of n bits, sym is the decoded symbol.
	// n < 0: escape to the subtable of width -n at base sym.
	// n == 0: invalid prefix (not a codeword of this book).
	n   int8
	sym uint32
}

// Table is an immutable two-level VLC lookup table.
type Table struct {
	entries []entry
}

// New builds a decoding table. Entry j maps the codeword codes[j] of
// lengths[j] bits to symbol syms[j]; syms may be nil, in which case the
// symbol is the index j (FFmpeg's implicit-symbol convention, used by the
// scalefactor book). Mirrors the (codes, bits, symbols) triples FFmpeg
// feeds ff_vlc_init_tables_sparse in libavcodec/aac/aacdec_tab.c:757-771
// @ d09d5afc3a.
func New(lengths []uint8, codes []uint32, syms []uint16) *Table {
	t := &Table{entries: make([]entry, 1<<rootBits)}

	// First pass: short codes fill the root table directly; long codes
	// allocate one subtable per distinct root prefix, sized by the longest
	// suffix under that prefix.
	subWidth := map[uint32]int{}
	for j, n := range lengths {
		if int(n) > rootBits {
			p := codes[j] >> (n - rootBits)
			if w := int(n) - rootBits; w > subWidth[p] {
				subWidth[p] = w
			}
		}
	}
	subBase := map[uint32]int{}
	for j, n := range lengths {
		sym := uint32(j)
		if syms != nil {
			sym = uint32(syms[j])
		}
		if int(n) <= rootBits {
			base := codes[j] << (rootBits - int(n))
			for k := range 1 << (rootBits - int(n)) {
				t.entries[base+uint32(k)] = entry{n: int8(n), sym: sym}
			}
			continue
		}
		p := codes[j] >> (n - rootBits)
		base, ok := subBase[p]
		if !ok {
			base = len(t.entries)
			subBase[p] = base
			w := subWidth[p]
			t.entries = append(t.entries, make([]entry, 1<<w)...)
			t.entries[p] = entry{n: int8(-w), sym: uint32(base)}
		}
		w := subWidth[p]
		suffix := codes[j] & (1<<(n-rootBits) - 1)
		lo := suffix << (uint(w) - (uint(n) - rootBits))
		for k := range 1 << (uint(w) - (uint(n) - rootBits)) {
			t.entries[base+int(lo)+k] = entry{n: int8(int(n) - rootBits), sym: sym}
		}
	}
	return t
}

// Decode reads one codeword from r and returns its symbol. ok is false if
// the peeked bits are not a codeword of this book (possible only on corrupt
// input, or past the end of the buffer where peeks return zeros); the
// caller turns that into an invalid-data error. Overreads latch r.Err.
func (t *Table) Decode(r *bits.Reader) (sym uint32, ok bool) {
	e := t.entries[r.Peek(rootBits)]
	if e.n > 0 {
		r.Skip(int(e.n))
		return e.sym, true
	}
	if e.n == 0 {
		return 0, false
	}
	r.Skip(rootBits)
	e = t.entries[e.sym+r.Peek(int(-e.n))]
	if e.n <= 0 {
		return 0, false
	}
	r.Skip(int(e.n))
	return e.sym, true
}

// Spectral holds the decoding tables for the 11 spectral codebooks,
// built from the same arrays FFmpeg passes to ff_vlc_init_tables_sparse
// (ff_aac_spectral_codes/_bits with ff_aac_codebook_vector_idx as symbols,
// libavcodec/aac/aacdec_tab.c:751-762 @ d09d5afc3a). Symbols are the packed
// vector descriptors documented at aactab.c:1040-1046.
var Spectral [11]*Table

// Scalefactor is the decoding table for the scalefactor codebook
// (ff_vlc_scalefactors, aacdec_tab.c:764-771 @ d09d5afc3a); symbols are the
// codeword indices 0..120.
var Scalefactor *Table

func init() {
	for cb := range 11 {
		codes := make([]uint32, len(tables.SpectralCodes[cb]))
		for j, c := range tables.SpectralCodes[cb] {
			codes[j] = uint32(c)
		}
		Spectral[cb] = New(tables.SpectralBits[cb], codes, tables.CodebookVectorIdx[cb])
	}
	Scalefactor = New(tables.ScalefactorBits[:], tables.ScalefactorCode[:], nil)
}
