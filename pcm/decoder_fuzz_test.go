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

// firstFrames returns the first n complete ADTS frames of a corpus stream, or
// nil. A multi-frame seed gives the fuzzer a realistic frame grid to mutate,
// so byte-loss and injected-syncword derivatives can reach the resync and
// chain-confirmation path a single-frame seed never sets up.
func firstFrames(name string, n int) []byte {
	data, err := os.ReadFile(filepath.Join(decoderTestdata, name+".adts"))
	if err != nil {
		return nil
	}
	pos := 0
	for range n {
		h, err := dec.ParseADTSHeaderBytes(data[pos:])
		if err != nil || pos+h.FrameLength > len(data) {
			return nil
		}
		pos += h.FrameLength
	}
	return data[:pos]
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
	// A CRC-present frame (protection_absent == 0) and multi-frame seeds give the
	// fuzzer realistic frame grids to mutate toward the chained-header resync
	// confirmation.
	if fr := firstFrame("crc_s48"); fr != nil {
		f.Add(fr)
	}
	for _, name := range []string{streamMono, streamStereo} {
		if two := firstFrames(name, 2); two != nil {
			f.Add(two)
		}
	}
	// A hand-built minimal ADTS frame header plus a short body, an empty input,
	// and a lone syncword prefix.
	f.Add([]byte{0xff, 0xf1, 0x4c, 0x80, 0x0d, 0x3f, 0xfc, 0x01, 0x18, 0x20, 0x07})
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xf1})
	// A lone parseable header whose frame length points at no real successor:
	// seed material whose mutations exercise the resync confirmation's rejection
	// path (as a top-level seed it is accepted at offset 0 and later surfaces as
	// truncation).
	f.Add([]byte{0xff, 0xf1, 0x4c, 0x80, 0x0d, 0x3f, 0xfc})
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

// FuzzDecodeInterleaved asserts the one-shot public decode API never panics on
// arbitrary input. This drain path (buffer the whole decode, enforce the byte
// cap) differs from FuzzDecodeStream's streaming Read loop, so it is exercised
// separately. The cap assertion is a secondary guard: the limit is enforced by
// a wrapped writer that errors rather than over-emitting, so a success returning
// more than the cap would be a broken-enforcement regression.
func FuzzDecodeInterleaved(f *testing.F) {
	for _, name := range []string{streamMono, streamStereo, "crc_s48"} {
		if fr := firstFrame(name); fr != nil {
			f.Add(fr)
		}
	}
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xf1})
	const maxOut = 1 << 20
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		out, _, err := DecodeInterleavedLimit(bytes.NewReader(data), maxOut)
		if err == nil && len(out) > maxOut {
			t.Fatalf("DecodeInterleavedLimit returned %d bytes over the %d cap", len(out), maxOut)
		}
	})
}
