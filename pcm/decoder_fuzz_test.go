// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/dec"
)

// firstFrame returns the first complete ADTS frame of a corpus stream, or nil.
// Seeding the fuzzer with single frames (not whole multi-KB streams) keeps
// throughput high: a whole-stream seed decodes slowly per execution and
// collapses the exec rate (see plan-d3 REHEARSAL 6).
func firstFrame(name string) []byte {
	data, err := os.ReadFile(filepath.Join(decoderTestdata, name+".adts"))
	if err != nil {
		return nil
	}
	h, err := dec.ParseADTSHeaderBytes(data)
	if err != nil || h.FrameLength > len(data) {
		return nil
	}
	return data[:h.FrameLength]
}

// FuzzDecodeStream asserts the public ADTS decode path never panics and never
// reads out of bounds on arbitrary input: NewDecoder plus a bounded drain must
// always terminate with a value or an error.
func FuzzDecodeStream(f *testing.F) {
	for _, name := range []string{streamMono, "pulse_m48", streamStereo} {
		if fr := firstFrame(name); fr != nil {
			f.Add(fr)
		}
	}
	// A hand-built minimal ADTS frame header plus a short body, an empty input,
	// and a lone syncword prefix.
	f.Add([]byte{0xff, 0xf1, 0x4c, 0x80, 0x0d, 0x3f, 0xfc, 0x01, 0x18, 0x20, 0x07})
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xf1})
	f.Fuzz(func(t *testing.T, data []byte) {
		// Guard against a pathologically large generated input driving work
		// unrelated to the decode logic under test.
		if len(data) > 1<<20 {
			return
		}
		d, err := NewDecoder(bytes.NewReader(data))
		if err != nil {
			return
		}
		// Cap output so a crafted stream cannot drive unbounded work.
		_, _ = io.Copy(io.Discard, io.LimitReader(readerFunc(d.Read), 1<<26))
	})
}

type readerFunc func([]byte) (int, error)

func (rf readerFunc) Read(p []byte) (int, error) { return rf(p) }
