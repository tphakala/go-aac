// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/dec"
)

// frameOffsets walks the ADTS frame grid of data and returns the byte offset of
// every complete frame, using the exported header parser to follow frame
// lengths.
func frameOffsets(t *testing.T, data []byte) []int {
	t.Helper()
	var offs []int
	for pos := 0; pos+dec.ADTSHeaderSize <= len(data); {
		h, err := dec.ParseADTSHeaderBytes(data[pos:])
		if err != nil || pos+h.FrameLength > len(data) {
			break
		}
		offs = append(offs, pos)
		pos += h.FrameLength
	}
	return offs
}

// falseSyncGarble returns a block of lossy-transport garbage that embeds a
// self-consistent ADTS header (a real 0xFFF syncword the field checks accept)
// whose frame length points into a run of zero bytes, so nothing sits at the
// claimed next boundary. This is exactly the false syncword #84 must not lock
// onto: it parses in isolation but does not chain to a real frame.
func falseSyncGarble(t *testing.T) []byte {
	t.Helper()
	// FrameLength = 57 + 7 = 64, well short of the trailing filler length.
	falseHdr, err := aac.AppendADTSHeader(nil, 8000, 1, 57)
	if err != nil {
		t.Fatal(err)
	}
	g := make([]byte, 0, 3+len(falseHdr)+128)
	g = append(g, 0x00, 0x00, 0x00) // leading non-sync bytes
	g = append(g, falseHdr...)
	g = append(g, make([]byte, 128)...) // filler; offset FrameLength lands here
	return g
}

// TestResyncSkipsFalseSyncword is the headline #84 regression: a false
// syncword injected into inter-frame garbage (as mid-stream byte loss on a live
// transport produces) must be skipped, and the decoder must recover to the true
// frame grid with byte-identical output. Before the chained-header confirmation,
// the framer locked onto the false header, mis-consumed its bogus frame length,
// and emitted garbage PCM.
func TestResyncSkipsFalseSyncword(t *testing.T) {
	garble := falseSyncGarble(t)
	for _, name := range []string{streamMono, streamStereo, streamCRC} {
		t.Run(name, func(t *testing.T) {
			data := loadStream(t, name)
			offs := frameOffsets(t, data)
			if len(offs) < 3 {
				t.Skipf("need >=3 frames, have %d", len(offs))
			}
			split := offs[1] // inject between frame 0 and frame 1
			lossy := make([]byte, 0, len(data)+len(garble))
			lossy = append(lossy, data[:split]...)
			lossy = append(lossy, garble...)
			lossy = append(lossy, data[split:]...)

			clean, err := decodeAll(t, data)
			if err != nil {
				t.Fatalf("clean decode: %v", err)
			}
			got, err := decodeAll(t, lossy)
			if err != nil {
				t.Fatalf("lossy decode: %v", err)
			}
			if !bytes.Equal(got, clean) {
				t.Fatalf("false-sync desynced the decode: got %d bytes, clean %d", len(got), len(clean))
			}
			t.Logf("skipped false sync, recovered true grid: %d bytes identical to clean", len(got))
		})
	}
}

// TestConfirmChainRejectsFalseSync pins confirmChain's contract directly: the
// peek-ahead sizing, the chained-header requirement, and the end-of-stream
// acceptance rule that preserves the final frame.
func TestConfirmChainRejectsFalseSync(t *testing.T) {
	// hdr with FrameLength = payload + 7. A second valid header used as the
	// chained successor.
	mkHdr := func(payload int) ([]byte, dec.ADTSHeader) {
		b, err := aac.AppendADTSHeader(nil, 8000, 1, payload)
		if err != nil {
			t.Fatal(err)
		}
		h, err := dec.ParseADTSHeaderBytes(b)
		if err != nil {
			t.Fatal(err)
		}
		return b, h
	}
	hdrA, hA := mkHdr(20) // FrameLength 27
	hdrB, _ := mkHdr(20)
	frameA := append(bytes.Clone(hdrA), make([]byte, 20)...) // 27 bytes

	cases := []struct {
		name string
		buf  []byte
		want bool
	}{
		{
			name: "chains to a valid header",
			buf:  append(append(bytes.Clone(frameA), hdrB...), 0x00, 0x00),
			want: true,
		},
		{
			name: "frame length lands on non-header",
			buf:  append(bytes.Clone(frameA), make([]byte, 10)...), // zeros where a header must be
			want: false,
		},
		{
			name: "whole final frame then clean EOF",
			buf:  bytes.Clone(frameA), // exactly FrameLength bytes, nothing after
			want: true,
		},
		{
			name: "candidate frame truncated at EOF",
			buf:  frameA[:20], // fewer than FrameLength bytes remain
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Decoder{br: bufio.NewReaderSize(bytes.NewReader(tc.buf), 1<<16)}
			ok, err := d.confirmChain(hA)
			if err != nil {
				t.Fatalf("confirmChain error: %v", err)
			}
			if ok != tc.want {
				t.Fatalf("confirmChain = %v, want %v", ok, tc.want)
			}
		})
	}
}

// TestRejectFalseSyncOnlyStream feeds a stream whose only syncword is a
// non-chaining false header followed by non-terminal filler. NewDecoder must
// reject it as a corrupt stream, never lock onto the false header, never panic.
func TestRejectFalseSyncOnlyStream(t *testing.T) {
	data := falseSyncGarble(t)
	data = append(data, make([]byte, 128)...) // ample filler so the chain check has full lookahead
	_, err := NewDecoder(bytes.NewReader(data))
	if !errors.Is(err, ErrCorruptStream) {
		t.Fatalf("want ErrCorruptStream for a false-sync-only stream, got %v", err)
	}
	t.Logf("false-sync-only NewDecoder error: %v", err)
}

// erringReader yields its data once, then returns a fixed non-EOF error on
// every subsequent Read, modelling a transport reset mid-stream.
type erringReader struct {
	data []byte
	err  error
}

func (r *erringReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// TestResyncReaderErrorDuringConfirm drives a non-EOF transport error into
// confirmChain's lookahead peek: one leading garbage byte forces syncADTS to
// scan, the false header then parses, and the confirmation peek runs into the
// error. It must propagate unchanged (not be swallowed as a clean end), so a
// caller sees the real transport failure rather than a spurious ErrCorruptStream.
func TestResyncReaderErrorDuringConfirm(t *testing.T) {
	falseHdr, err := aac.AppendADTSHeader(nil, 8000, 1, 57) // FrameLength 64
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte{0x00}, falseHdr...) // 1 garbage byte forces scanning
	wantErr := errors.New("transport reset")
	d := &Decoder{br: bufio.NewReaderSize(&erringReader{data: data, err: wantErr}, 1<<16)}
	if _, gerr := d.syncADTS(); !errors.Is(gerr, wantErr) {
		t.Fatalf("syncADTS = %v, want the raw transport error", gerr)
	}
}

// TestResyncByteLoss deletes a run of bytes from inside an interior frame, the
// shape of mid-stream loss on a lossy transport. The decoder must terminate
// without panic, surface only an accepted terminal error, and never emit a
// partial or garbage-length frame (output stays a whole multiple of the frame
// size and never exceeds the clean decode).
func TestResyncByteLoss(t *testing.T) {
	for _, name := range []string{streamMono, streamStereo} {
		t.Run(name, func(t *testing.T) {
			data := loadStream(t, name)
			offs := frameOffsets(t, data)
			if len(offs) < 4 {
				t.Skipf("need >=4 frames, have %d", len(offs))
			}
			clean, err := decodeAll(t, data)
			if err != nil {
				t.Fatalf("clean decode: %v", err)
			}
			d0, err := NewDecoder(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			frameBytes := 1024 * 2 * d0.Info().Channels

			// Drop a 40-byte run from the middle of frame 2's payload.
			mid := offs[2] + dec.ADTSHeaderSize + 8
			lossy := make([]byte, 0, len(data))
			lossy = append(lossy, data[:mid]...)
			lossy = append(lossy, data[mid+40:]...)

			got, gerr := decodeAll(t, lossy)
			if gerr != nil && !errors.Is(gerr, ErrCorruptStream) && !errors.Is(gerr, io.EOF) {
				t.Fatalf("unexpected terminal error: %v", gerr)
			}
			if len(got)%frameBytes != 0 {
				t.Fatalf("emitted %d bytes, not a whole multiple of the %d-byte frame", len(got), frameBytes)
			}
			if len(got) > len(clean) {
				t.Fatalf("emitted %d bytes, more than the clean decode's %d", len(got), len(clean))
			}
			// The frames before the corruption point decode from untouched bytes,
			// so whatever is emitted must be a byte-identical prefix of the clean
			// decode: no garbage-but-correctly-sized PCM slips through.
			if !bytes.Equal(got, clean[:len(got)]) {
				t.Fatalf("emitted %d bytes that differ from the clean decode prefix", len(got))
			}
			t.Logf("byte-loss decode: emitted %d bytes (%d frames), terminal err = %v",
				len(got), len(got)/frameBytes, gerr)
		})
	}
}
