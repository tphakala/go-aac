// SPDX-License-Identifier: LGPL-2.1-or-later
package tables

import (
	"encoding/binary"
	"math"
)

// Names of the two runtime-computed records; the golden test treats them
// tolerantly while everything else must be byte-identical.
const (
	pow2SFName  = "Pow2SF"
	pow34SFName = "Pow34SF"
)

// The functions below serialize every table into the canonical byte form
// shared with tools/gentables bin mode (little-endian elements; nested
// tables carry a uint32 length prefix per row). The checksum test hashes
// this form and the golden test compares it byte for byte against the
// fixture dumped from the pinned C tree.

func serU8(dst []byte, v []uint8) []byte { return append(dst, v...) }

func serU16(dst []byte, v []uint16) []byte {
	for _, x := range v {
		dst = binary.LittleEndian.AppendUint16(dst, x)
	}
	return dst
}

func serU32(dst []byte, v []uint32) []byte {
	for _, x := range v {
		dst = binary.LittleEndian.AppendUint32(dst, x)
	}
	return dst
}

func serF32(dst []byte, v []float32) []byte {
	for _, x := range v {
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(x))
	}
	return dst
}

func serRowsU8(dst []byte, rows [][]uint8) []byte {
	for _, r := range rows {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(r)))
		dst = serU8(dst, r)
	}
	return dst
}

func serRowsU16(dst []byte, rows [][]uint16) []byte {
	for _, r := range rows {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(r)))
		dst = serU16(dst, r)
	}
	return dst
}

func serRowsF32(dst []byte, rows [][]float32) []byte {
	for _, r := range rows {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(r)))
		dst = serF32(dst, r)
	}
	return dst
}

func serPsyMap(dst []byte, v []PsyLamePreset) []byte {
	for _, p := range v {
		dst = binary.LittleEndian.AppendUint32(dst, uint32(p.Quality))
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(p.StLrm))
	}
	return dst
}

// serializedTables returns every table in canonical serialized form, keyed
// by the record names used in testdata/ctables.bin.
func serializedTables() map[string][]byte {
	m := map[string][]byte{}
	m["ScalefactorCode"] = serU32(nil, ScalefactorCode[:])
	m["ScalefactorBits"] = serU8(nil, ScalefactorBits[:])
	m["SpectralCodes"] = serRowsU16(nil, SpectralCodes[:])
	m["SpectralBits"] = serRowsU8(nil, SpectralBits[:])
	m["CodebookVectors"] = serRowsF32(nil, CodebookVectors[:])
	m["CodebookVectorVals"] = serRowsF32(nil, CodebookVectorVals[:])
	m["CodebookVectorIdx"] = serRowsU16(nil, CodebookVectorIdx[:])
	m["NumSwb1024"] = serU8(nil, NumSwb1024[:])
	m["NumSwb128"] = serU8(nil, NumSwb128[:])
	m["SwbOffset1024"] = serRowsU16(nil, SwbOffset1024[:])
	m["SwbOffset128"] = serRowsU16(nil, SwbOffset128[:])
	m["TNSMaxBands1024"] = serU8(nil, TNSMaxBands1024[:])
	m["TNSMaxBands128"] = serU8(nil, TNSMaxBands128[:])
	m["TNSTmp2Map"] = serRowsF32(nil, TNSTmp2Map[:])
	m["SwbSize1024"] = serRowsU8(nil, SwbSize1024[:])
	m["SwbSize128"] = serRowsU8(nil, SwbSize128[:])
	var cc []byte
	for i := range ChanConfigs {
		cc = serU8(cc, ChanConfigs[i][:])
	}
	m["ChanConfigs"] = cc
	var cm []byte
	for i := range ChanMaps {
		cm = serU8(cm, ChanMaps[i][:])
	}
	m["ChanMaps"] = cm
	m["RunValueBitsLong"] = serU8(nil, RunValueBitsLong[:])
	m["RunValueBitsShort"] = serU8(nil, RunValueBitsShort[:])
	m["TNSMinSfbLong"] = serU8(nil, TNSMinSfbLong[:])
	m["TNSMinSfbShort"] = serU8(nil, TNSMinSfbShort[:])
	m["CBOutMap"] = serU8(nil, CBOutMap[:])
	m["CBInMap"] = serU8(nil, CBInMap[:])
	m["CBRange"] = serU8(nil, CBRange[:])
	m["CBMaxval"] = serU8(nil, CBMaxval[:])
	m["MaxvalCB"] = serU8(nil, MaxvalCB[:])
	m["PsyABRMap"] = serPsyMap(nil, PsyABRMap[:])
	m["PsyVBRMap"] = serPsyMap(nil, PsyVBRMap[:])
	m["PsyFirCoeffs"] = serF32(nil, PsyFirCoeffs[:])
	m["WindowGrouping"] = serU8(nil, WindowGrouping[:])
	m[pow2SFName] = serF32(nil, Pow2SF[:])
	m[pow34SFName] = serF32(nil, Pow34SF[:])
	return m
}
