// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import "fmt"

const adtsHeaderSize = 7

// maxADTSPayload is the largest access unit the 13-bit ADTS frame length
// field can carry (8191 bytes including the 7-byte header).
const maxADTSPayload = 8191 - adtsHeaderSize

// AppendADTSHeader appends the 7-byte ADTS header framing one raw AAC-LC
// access unit of payloadLen bytes, returning the extended slice. Callers
// of the low-level Encoder use it to build a streamable ADTS stream: for
// each access unit, append the header, then the access unit itself (the
// pcm package does exactly this). channels is the ADTS channel
// configuration (1..7). Byte-identical to FFmpeg's ADTS muxer output
// (libavformat/adtsenc.c:adts_write_frame_header @ d09d5afc3a).
func AppendADTSHeader(dst []byte, sampleRate, channels, payloadLen int) ([]byte, error) {
	idx, ok := sampleRateIndex(sampleRate)
	if !ok {
		return dst, fmt.Errorf("go-aac: no ADTS sample rate index for %d Hz", sampleRate)
	}
	if channels < 1 || channels > 7 {
		return dst, fmt.Errorf("go-aac: ADTS channel configuration %d outside 1..7", channels)
	}
	if payloadLen < 0 || payloadLen > maxADTSPayload {
		return dst, fmt.Errorf("go-aac: ADTS payload of %d bytes outside 0..%d", payloadLen, maxADTSPayload)
	}
	return appendADTSHeader(dst, idx, channels, payloadLen), nil
}

// appendADTSHeader appends a 7-byte ADTS header for one raw AAC access unit
// of payloadLen bytes. Mirrors libavformat/adtsenc.c:adts_write_frame_header
// @ d09d5afc3a: MPEG-4 ID, layer 0, no CRC, profile AAC-LC (object type 2,
// coded as 1), buffer fullness 0x7ff (VBR sentinel), one raw data block.
// The 13-bit frame length field caps payloadLen at 8184; the encoder's
// 6144-bits-per-channel bound keeps real frames far below that.
func appendADTSHeader(dst []byte, srIndex, chanConfig, payloadLen int) []byte {
	frameLen := payloadLen + adtsHeaderSize
	return append(dst,
		0xff,
		0xf1,
		byte(1<<6|srIndex<<2|chanConfig>>2),
		byte(chanConfig<<6|frameLen>>11),
		byte(frameLen>>3),
		byte(frameLen<<5|0x1f),
		0xfc,
	)
}
