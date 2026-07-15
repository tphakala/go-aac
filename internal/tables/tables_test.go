// SPDX-License-Identifier: LGPL-2.1-or-later
package tables

import (
	"math"
	"testing"
)

// spectralSizes mirrors ff_aac_spectral_sizes (libavcodec/aactab.c
// @ d09d5afc3a), transcribed from the source text as an independent pin on
// the generator (the generator derives the same numbers from the C).
var spectralSizes = [11]int{81, 81, 81, 81, 81, 81, 64, 64, 169, 169, 289}

func TestSpectralBookShapes(t *testing.T) {
	for i, want := range spectralSizes {
		if len(SpectralCodes[i]) != want || len(SpectralBits[i]) != want {
			t.Errorf("book %d: len codes/bits = %d/%d, want %d",
				i+1, len(SpectralCodes[i]), len(SpectralBits[i]), want)
		}
		dim := 2
		if i < 4 {
			dim = 4
		}
		if len(CodebookVectors[i]) != want*dim {
			t.Errorf("book %d: len vectors = %d, want %d", i+1, len(CodebookVectors[i]), want*dim)
		}
		if len(CodebookVectorIdx[i]) != want {
			t.Errorf("book %d: len idx = %d, want %d", i+1, len(CodebookVectorIdx[i]), want)
		}
	}
}

// checkPrefixFree asserts that no codeword is a prefix of another, i.e. the
// table forms a decodable Huffman code (what the future internal/vlc builds
// its lookup tables from).
func checkPrefixFree(t *testing.T, name string, codes []uint32, bits []uint8) {
	t.Helper()
	for i := range codes {
		for j := range codes {
			if i == j || bits[i] > bits[j] {
				continue
			}
			if codes[j]>>(bits[j]-bits[i]) == codes[i] {
				t.Fatalf("%s: code %d (%d bits) is a prefix of code %d (%d bits)",
					name, i, bits[i], j, bits[j])
			}
		}
	}
}

func TestHuffmanPrefixFree(t *testing.T) {
	checkPrefixFree(t, "scalefactor", ScalefactorCode[:], ScalefactorBits[:])
	for i := range SpectralCodes {
		codes := make([]uint32, len(SpectralCodes[i]))
		for j, c := range SpectralCodes[i] {
			codes[j] = uint32(c)
		}
		checkPrefixFree(t, "spectral", codes, SpectralBits[i])
	}
}

func TestSwbTablesConsistent(t *testing.T) {
	for i := range 13 {
		if len(SwbOffset1024[i]) != int(NumSwb1024[i])+1 {
			t.Errorf("SwbOffset1024[%d]: len %d, want num_swb+1 = %d",
				i, len(SwbOffset1024[i]), NumSwb1024[i]+1)
		}
		if len(SwbOffset128[i]) != int(NumSwb128[i])+1 {
			t.Errorf("SwbOffset128[%d]: len %d, want num_swb+1 = %d",
				i, len(SwbOffset128[i]), NumSwb128[i]+1)
		}
		if got := SwbOffset1024[i][NumSwb1024[i]]; got != 1024 {
			t.Errorf("SwbOffset1024[%d] sentinel = %d, want 1024", i, got)
		}
		if got := SwbOffset128[i][NumSwb128[i]]; got != 128 {
			t.Errorf("SwbOffset128[%d] sentinel = %d, want 128", i, got)
		}
		for g := 1; g < len(SwbOffset1024[i]); g++ {
			if SwbOffset1024[i][g] <= SwbOffset1024[i][g-1] {
				t.Fatalf("SwbOffset1024[%d] not strictly increasing at %d", i, g)
			}
		}
		sum := 0
		for _, w := range SwbSize1024[i] {
			sum += int(w)
		}
		if sum != 1024 || len(SwbSize1024[i]) != int(NumSwb1024[i]) {
			t.Errorf("SwbSize1024[%d]: sum %d len %d, want 1024 and %d",
				i, sum, len(SwbSize1024[i]), NumSwb1024[i])
		}
		sum = 0
		for _, w := range SwbSize128[i] {
			sum += int(w)
		}
		if sum != 128 || len(SwbSize128[i]) != int(NumSwb128[i]) {
			t.Errorf("SwbSize128[%d]: sum %d len %d, want 128 and %d",
				i, sum, len(SwbSize128[i]), NumSwb128[i])
		}
	}
}

// Literal spot checks transcribed by eye from the pinned C source text; an
// independent pin on the generator output (the generator never sees these).
func TestSourceTextSpotChecks(t *testing.T) {
	wantNumSwb1024 := [13]uint8{41, 41, 47, 49, 49, 51, 47, 47, 43, 43, 43, 40, 40}
	if NumSwb1024 != wantNumSwb1024 {
		t.Errorf("NumSwb1024 = %v, want %v", NumSwb1024, wantNumSwb1024)
	}
	wantTNSMax1024 := [13]uint8{31, 31, 34, 40, 42, 51, 46, 46, 42, 42, 42, 39, 39}
	if TNSMaxBands1024 != wantTNSMax1024 {
		t.Errorf("TNSMaxBands1024 = %v, want %v", TNSMaxBands1024, wantTNSMax1024)
	}
	wantGrouping := [9]uint8{0xB6, 0x6C, 0xD8, 0xB2, 0x66, 0xC6, 0x96, 0x36, 0x36}
	if WindowGrouping != wantGrouping {
		t.Errorf("WindowGrouping = %#v, want %#v", WindowGrouping, wantGrouping)
	}
	wantMaxvalCB := [14]uint8{0, 1, 3, 5, 5, 7, 7, 7, 9, 9, 9, 9, 9, 11}
	if MaxvalCB != wantMaxvalCB {
		t.Errorf("MaxvalCB = %v, want %v", MaxvalCB, wantMaxvalCB)
	}
	// The zero scalefactor delta (index 60) codes as a single 0 bit.
	if ScalefactorBits[60] != 1 || ScalefactorCode[60] != 0 {
		t.Errorf("scalefactor delta 0: code %#x bits %d, want 0x0 and 1",
			ScalefactorCode[60], ScalefactorBits[60])
	}
	// Mono is one SCE (type 0), stereo one CPE (type 1) (aacenctab.h).
	if ChanConfigs[0] != [6]uint8{1, 0, 0, 0, 0, 0} || ChanConfigs[1] != [6]uint8{1, 1, 0, 0, 0, 0} {
		t.Errorf("ChanConfigs rows 0-1 = %v %v", ChanConfigs[0], ChanConfigs[1])
	}
	// LAME ABR presets: first and last rows (aacpsy.c psy_abr_map).
	if PsyABRMap[0] != (PsyLamePreset{8, 7.60}) || PsyABRMap[12] != (PsyLamePreset{160, 6.20}) {
		t.Errorf("PsyABRMap ends = %v %v", PsyABRMap[0], PsyABRMap[12])
	}
}

func TestPowTables(t *testing.T) {
	if Pow2SF[PowSF2Zero] != 1.0 {
		t.Errorf("Pow2SF[%d] = %v, want 1", PowSF2Zero, Pow2SF[PowSF2Zero])
	}
	maxRel := 0.0
	for i := range Pow2SF {
		want := math.Pow(2, float64(i-PowSF2Zero)/4)
		if d := math.Abs(float64(Pow2SF[i])-want) / want; d > maxRel {
			maxRel = d
		}
		want34 := math.Pow(want, 3.0/4)
		if d := math.Abs(float64(Pow34SF[i])-want34) / want34; d > 1e-6 {
			t.Fatalf("Pow34SF[%d] = %g, want %g", i, Pow34SF[i], want34)
		}
	}
	if maxRel > 1e-6 {
		t.Errorf("Pow2SF: max relative diff vs math.Pow = %g", maxRel)
	}
	t.Logf("Pow2SF vs math.Pow: max relative diff %.3g", maxRel)
}
