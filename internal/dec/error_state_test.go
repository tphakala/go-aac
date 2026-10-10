// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// windowHistorySnapshot records the per-channel window history of every
// allocated channel element, the cross-frame parse state decodeICSInfo shifts
// in place and imdctAndWindowing reads back at index 1.
func windowHistorySnapshot(d *Decoder) []windowHist {
	var out []windowHist
	for t := range d.che {
		for _, che := range d.che[t] {
			if che == nil {
				continue
			}
			for c := range che.Ch {
				out = append(out, windowHist{
					seq: che.Ch[c].ICS.WindowSequence,
					kb:  che.Ch[c].ICS.UseKBWindow,
				})
			}
		}
	}
	return out
}

// shapeFlipTruncated returns a copy of an ADTS frame whose first element's
// window_shape bit is flipped, cut to the header plus 3 payload bytes so the
// parse fails after decodeICSInfo has already shifted the history. The bit
// offsets are verified against the frame (decoded here, not assumed): a frame
// that does not match is a test setup error.
func shapeFlipTruncated(t *testing.T, f []byte) []byte {
	t.Helper()
	if len(f) < adtsHeaderSize+3 {
		t.Fatalf("frame of %d bytes is too short", len(f))
	}
	if f[1]&1 != 1 {
		t.Fatal("ADTS frame has a CRC word; the payload offsets here assume protection_absent=1")
	}
	p := f[adtsHeaderSize:]
	var bit int
	switch elem := int(p[0] >> 5); elem {
	case TypeSCE:
		bit = 18 // 3 type + 4 tag + 8 global_gain + 1 reserved + 2 window_sequence
	case TypeCPE:
		if p[0]&1 == 1 { // common_window is payload bit 7, the low bit of byte 0
			bit = 11 // 3 + 4 + 1 + 1 reserved + 2 window_sequence
		} else {
			bit = 19 // 3 + 4 + 1 + 8 global_gain + 1 reserved + 2 window_sequence
		}
	default:
		t.Fatalf("first element type %d is neither SCE nor CPE", elem)
	}
	bad := bytes.Clone(f[:adtsHeaderSize+3])
	bad[adtsHeaderSize+bit/8] ^= 0x80 >> (bit % 8)
	return bad
}

// TestDecodeFrameErrorRestoresWindowHistory pins the mechanism behind the
// "a failed unit consumes no state" contract: a raw_data_block that fails to
// parse after decodeICSInfo shifted the window history must leave the history
// exactly as it was before the unit.
func TestDecodeFrameErrorRestoresWindowHistory(t *testing.T) {
	for _, name := range []string{"sine_m8_24k", "tonal_s48_128k"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name+".adts"))
			if err != nil {
				t.Fatal(err)
			}
			frames := splitADTSFrames(t, data)
			if len(frames) < 8 {
				t.Fatalf("only %d frames", len(frames))
			}
			d := NewADTS()
			dst := make([]byte, 0, 1<<14)
			for i, f := range frames[:6] {
				if dst, _, err = d.AppendS16(dst[:0], f); err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
			}
			before := windowHistorySnapshot(d)
			if len(before) == 0 {
				t.Fatal("no allocated channel element to snapshot")
			}
			bad := shapeFlipTruncated(t, frames[6])
			for range 2 { // a second consecutive failure must restore as well
				if _, _, err = d.AppendS16(dst[:0], bad); err == nil {
					t.Fatal("corrupt unit decoded without error; the error path was not exercised")
				}
				after := windowHistorySnapshot(d)
				if len(after) != len(before) {
					t.Fatalf("snapshot size %d, want %d", len(after), len(before))
				}
				for i := range before {
					if after[i] != before[i] {
						t.Errorf("channel %d window history %+v after a failed unit, want %+v", i, after[i], before[i])
					}
				}
			}
		})
	}
}

// TestDecodeFrameErrorRestoresNonCommonWindowCPE covers a CPE with
// common_window=0, where each channel runs its own decodeICSInfo and channel 1
// shifts its history only after channel 0 parsed completely. The frame is cut at
// every length, so some cuts fail after channel 1's shift; each failing cut must
// leave the history as it was.
func TestDecodeFrameErrorRestoresNonCommonWindowCPE(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "goenc_s48_128k.adts"))
	if err != nil {
		t.Fatal(err)
	}
	frames := splitADTSFrames(t, data)
	k := -1
	for i, f := range frames {
		if i >= 2 && f[1]&1 == 1 && int(f[adtsHeaderSize]>>5) == TypeCPE && f[adtsHeaderSize]&1 == 0 {
			k = i
			break
		}
	}
	if k < 0 {
		t.Fatal("no non-common-window CPE frame in the stream")
	}
	failed := 0
	dst := make([]byte, 0, 1<<14)
	for n := adtsHeaderSize + 3; n < len(frames[k]); n++ {
		d := NewADTS()
		for i, f := range frames[:k] {
			if dst, _, err = d.AppendS16(dst[:0], f); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
		}
		// Give every channel a history that no parse can reproduce, so a
		// restore of any one field is observable: a shift of index 0 into
		// index 1 changes each of them.
		cpe := d.che[TypeCPE][0]
		for c := range cpe.Ch {
			cpe.Ch[c].ICS.WindowSequence = [2]int{c + 1, c + 2}
			cpe.Ch[c].ICS.UseKBWindow = [2]int{c, 1 - c}
		}
		before := windowHistorySnapshot(d)
		if _, _, err = d.AppendS16(dst[:0], frames[k][:n]); err == nil {
			continue
		}
		failed++
		after := windowHistorySnapshot(d)
		for i := range before {
			if after[i] != before[i] {
				t.Fatalf("cut at %d: channel %d window history %+v after a failed unit, want %+v", n, i, after[i], before[i])
			}
		}
	}
	if failed == 0 {
		t.Fatal("no cut of the frame failed to decode; the error path was not exercised")
	}
}
