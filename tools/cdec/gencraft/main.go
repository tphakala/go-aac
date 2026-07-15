// Crafts corpus streams for decode paths no encoder emits:
//  1. pulse_m48.adts   - SCE frames carrying pulse_data
//  2. crc_s48.adts     - ADTS frames with protection_absent=0 (CRC present)
package main

import (
	"fmt"
	"os"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/tables"
)

// findSpectral returns the Huffman code and length for a packed spectral
// vector. It is only ever called for codebook 1 (cb index 0), whose low byte
// uniquely identifies the signed 4-tuple, so the low-byte match is exact.
// A full uint16 compare would NOT work here: book-1 CodebookVectorIdx entries
// carry a nonzero high byte (0x8140, ...) while the caller passes the low-byte
// vector index. The low-byte aliasing that affects book 11 cannot occur
// because this helper is never invoked for book 11.
func findSpectral(cb int, packed uint16) (code uint32, n int) {
	for j, s := range tables.CodebookVectorIdx[cb] {
		if s&0xff == packed {
			return uint32(tables.SpectralCodes[cb][j]), int(tables.SpectralBits[cb][j])
		}
	}
	panic(fmt.Sprintf("cb%d: packed %#x not found", cb+1, packed))
}

func pulseFrame() []byte {
	w := bits.NewWriter(nil)
	w.Put(3, 0)   // TYPE_SCE
	w.Put(4, 0)   // element_instance_tag
	w.Put(8, 100) // global_gain
	// ics_info: long window
	w.Put(1, 0) // ics_reserved
	w.Put(2, 0) // ONLY_LONG_SEQUENCE
	w.Put(1, 1) // KBD
	w.Put(6, 1) // max_sfb = 1
	w.Put(1, 0) // predictor_data_present
	// section_data: one section, codebook 1, run 1
	w.Put(4, 1)
	w.Put(5, 1)
	// scalefactor: delta 0 -> code index 60
	w.Put(int(tables.ScalefactorBits[60]), tables.ScalefactorCode[60])
	// pulse_data: 2 pulses in band 0 (48 kHz band 0 is coeffs 0..3)
	w.Put(1, 1) // pulse_data_present
	w.Put(2, 1) // number_pulse-1 = 1 -> 2 pulses
	w.Put(6, 0) // pulse_start_sfb
	w.Put(5, 1) // offset -> pos 1
	w.Put(4, 5) // amp 5
	w.Put(5, 2) // offset -> pos 3
	w.Put(4, 3) // amp 3
	w.Put(1, 0) // tns_data_present
	w.Put(1, 0) // gain_control_data_present
	// spectral: band 0 (width 4), cb1: vector (1,0,-1,1) -> packed 134
	code, n := findSpectral(0, 134)
	w.Put(n, code)
	w.Put(3, 7) // TYPE_END
	return w.Flush()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gencraft <output-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	// pulseFrame is deterministic, so every frame is the same bytes: build the
	// access unit and its header once and repeat them.
	au := pulseFrame()
	hdr, err := aac.AppendADTSHeader(nil, 48000, 1, len(au))
	if err != nil {
		panic(err)
	}
	adts := make([]byte, 0, (len(hdr)+len(au))*5)
	for range 5 {
		adts = append(adts, hdr...)
		adts = append(adts, au...)
	}
	if err := os.WriteFile(dir+"/pulse_m48.adts", adts, 0o644); err != nil {
		panic(err)
	}

	// CRC-present variant of an encoded stream: protection_absent=0,
	// 16-bit CRC word (unverified by the decoder, which just skips it),
	// frame_length grows by 2.
	src, err := os.ReadFile(dir + "/tonal_s48_128k.adts")
	if err != nil {
		panic(err)
	}
	var out []byte
	for pos := 0; pos < len(src); {
		if len(src)-pos < 7 {
			panic(fmt.Sprintf("truncated ADTS header at offset %d", pos))
		}
		flen := int(src[pos+3]&3)<<11 | int(src[pos+4])<<3 | int(src[pos+5])>>5
		if flen < 7 || flen > len(src)-pos {
			panic(fmt.Sprintf("invalid ADTS frame length %d at offset %d", flen, pos))
		}
		if src[pos+1]&1 == 0 {
			panic(fmt.Sprintf("source frame at offset %d already carries a CRC", pos))
		}
		if flen+2 > 0x1fff {
			panic(fmt.Sprintf("CRC frame exceeds the 13-bit ADTS length limit at offset %d", pos))
		}
		frame := src[pos : pos+flen]
		hdr := make([]byte, 7, 9)
		copy(hdr, frame[:7])
		hdr[1] &^= 1 // protection_absent = 0
		nlen := flen + 2
		hdr[3] = hdr[3]&^3 | byte(nlen>>11)
		hdr[4] = byte(nlen >> 3)
		hdr[5] = hdr[5]&0x1f | byte(nlen&7)<<5
		hdr = append(hdr, 0xde, 0xad) // dummy CRC, decoder skips it
		out = append(out, hdr...)
		out = append(out, frame[7:]...)
		pos += flen
	}
	if err := os.WriteFile(dir+"/crc_s48.adts", out, 0o644); err != nil {
		panic(err)
	}
}
