// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import "github.com/tphakala/go-aac/internal/bits"

// ADTSHeaderSize is the fixed ADTS header length in bytes
// (AV_AAC_ADTS_HEADER_SIZE). The streaming framer peeks this many bytes to
// read aac_frame_length before pulling the whole frame.
const ADTSHeaderSize = adtsHeaderSize

// ParseADTSHeaderBytes parses one ADTS frame header from the front of buf. It
// is the byte-slice entry point the pcm framer uses so it never imports the
// internal bits reader. buf must be at least ADTSHeaderSize bytes; a shorter
// buffer returns ErrInvalidData (truncated header).
func ParseADTSHeaderBytes(buf []byte) (ADTSHeader, error) {
	return ParseADTS(bits.NewReader(buf))
}
