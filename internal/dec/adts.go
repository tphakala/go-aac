// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"fmt"

	"github.com/tphakala/go-aac/internal/bits"
)

// adtsHeaderSize is AV_AAC_ADTS_HEADER_SIZE (libavcodec/adts_parser.h
// @ d09d5afc3a).
const adtsHeaderSize = 7

// mpeg4SampleRates mirrors ff_mpeg4audio_sample_rates
// (libavcodec/mpeg4audio.c @ d09d5afc3a). Entries 13..15 are zero:
// reserved indices that the ADTS parser rejects.
var mpeg4SampleRates = [16]int{
	96000, 88200, 64000, 48000, 44100, 32000,
	24000, 22050, 16000, 12000, 11025, 8000, 7350, 0, 0, 0,
}

// ADTSHeader carries the parsed fixed and variable ADTS header fields.
// Mirrors AACADTSHeaderInfo (libavcodec/adts_header.h @ d09d5afc3a).
type ADTSHeader struct {
	ObjectType    int // profile_objecttype + 1
	ChanConfig    int
	CRCAbsent     int
	NumAACFrames  int
	SamplingIndex int
	SampleRate    int
	Samples       int
	FrameLength   int
}

// ParseADTS parses one ADTS frame header from r. Mirrors
// libavcodec/adts_header.c:ff_adts_header_parse @ d09d5afc3a, including
// its exact field order and validation.
func ParseADTS(r *bits.Reader) (ADTSHeader, error) {
	var h ADTSHeader
	if r.Read(12) != 0xfff {
		return h, ErrSync
	}
	r.Skip(1)             // id
	r.Skip(2)             // layer
	crcAbs := r.ReadBit() // protection_absent
	aot := r.Read(2)      // profile_objecttype
	sr := r.Read(4)       // sample_frequency_index
	if mpeg4SampleRates[sr] == 0 {
		return h, fmt.Errorf("%w: bad ADTS sample rate index %d", ErrInvalidData, sr)
	}
	r.Skip(1)          // private_bit
	ch := r.Read(3)    // channel_configuration
	r.Skip(1)          // original/copy
	r.Skip(1)          // home
	r.Skip(1)          // copyright_identification_bit
	r.Skip(1)          // copyright_identification_start
	size := r.Read(13) // aac_frame_length
	if int(size) < adtsHeaderSize {
		return h, fmt.Errorf("%w: ADTS frame length %d", ErrInvalidData, size)
	}
	r.Skip(11)       // adts_buffer_fullness
	rdb := r.Read(2) // number_of_raw_data_blocks_in_frame
	if err := r.Err(); err != nil {
		return h, fmt.Errorf("%w: truncated ADTS header", ErrInvalidData)
	}
	h.ObjectType = int(aot) + 1
	h.ChanConfig = int(ch)
	h.CRCAbsent = int(crcAbs)
	h.NumAACFrames = int(rdb) + 1
	h.SamplingIndex = int(sr)
	h.SampleRate = mpeg4SampleRates[sr]
	h.Samples = (int(rdb) + 1) * 1024
	h.FrameLength = int(size)
	return h, nil
}

// FindSync returns the offset of the next plausible ADTS frame at or after
// pos: a 12-bit syncword whose header parses. Returns -1 if none is found.
// Mirrors the resynchronization scan of the ADTS parser layer.
func FindSync(buf []byte, pos int) int {
	if pos < 0 {
		pos = 0 // "at or after pos" from before the buffer means from the start
	}
	for ; pos+adtsHeaderSize <= len(buf); pos++ {
		// 12-bit syncword pre-filter only; ff_adts_header_parse checks
		// nothing else before the field reads (it skips id and layer), so
		// masking layer bits here would reject headers the C accepts.
		if buf[pos] != 0xff || buf[pos+1]>>4 != 0xf {
			continue
		}
		if _, err := ParseADTS(bits.NewReader(buf[pos:])); err == nil {
			return pos
		}
	}
	return -1
}
