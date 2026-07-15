// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

// appendAudioSpecificConfig appends the 2-byte MPEG-4 AudioSpecificConfig
// for AAC-LC: 5-bit audio object type (2), 4-bit sample rate index, 4-bit
// channel configuration, then GASpecificConfig bits 000 (1024 frame length,
// no dependsOnCoreCoder, no extension). Mirrors the standard-configuration
// path of libavcodec/aacenc.c:put_audio_specific_config @ d09d5afc3a.
func appendAudioSpecificConfig(dst []byte, srIndex, chanConfig int) []byte {
	v := 2<<11 | srIndex<<7 | chanConfig<<3
	return append(dst, byte(v>>8), byte(v))
}
