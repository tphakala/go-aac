// SPDX-License-Identifier: LGPL-2.1-or-later
package aac_test

import (
	"fmt"
	"log"

	aac "github.com/tphakala/go-aac"
)

// Example_lowLevel encodes planar float32 frames with the low-level
// Encoder and frames each raw access unit in ADTS by hand. Callers with
// interleaved integer PCM should use the pcm package instead, which does
// all of this internally.
func Example_lowLevel() {
	enc, err := aac.NewEncoder(aac.EncoderConfig{
		SampleRate: 48000,
		Channels:   1,
		Bitrate:    96000,
	})
	if err != nil {
		log.Fatal(err)
	}

	frame := make([]float32, aac.FrameSize) // silence; real callers fill [-1, 1] samples
	var stream, au []byte
	writeAU := func() {
		if len(au) == 0 {
			return // encoder priming: the first call emits nothing
		}
		// A raw access unit is NOT self-framing: it must be wrapped in an
		// ADTS header (or muxed into MP4 using AudioSpecificConfig).
		stream, err = aac.AppendADTSHeader(stream, 48000, 1, len(au))
		if err != nil {
			log.Fatal(err)
		}
		stream = append(stream, au...)
	}
	for range 10 {
		if au, err = enc.EncodeFrame(au[:0], [][]float32{frame}); err != nil {
			log.Fatal(err)
		}
		writeAU()
	}
	for !enc.Drained() {
		if au, err = enc.EncodeFrame(au[:0], nil); err != nil {
			log.Fatal(err)
		}
		writeAU()
	}
	fmt.Printf("ASC % x, stream starts % x\n", enc.AudioSpecificConfig(), stream[:2])
	// Output: ASC 11 88, stream starts ff f1
}
