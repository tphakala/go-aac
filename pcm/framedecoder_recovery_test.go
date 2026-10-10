// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	aac "github.com/tphakala/go-aac"
)

// shapeFlipUnit returns a copy of an access unit whose first element's
// window_shape bit is flipped, cut to hdr+3 bytes so the parse fails after
// decodeICSInfo has already shifted the window history. hdr is the ADTS header
// length (7) or 0 for a raw unit. The element type, common_window flag and
// protection_absent are decoded from the unit here rather than assumed, so a
// unit this helper cannot describe is reported instead of silently corrupting
// the wrong bit.
func shapeFlipUnit(au []byte, hdr int) ([]byte, error) {
	if len(au) < hdr+3 {
		return nil, fmt.Errorf("unit of %d bytes is too short to corrupt", len(au))
	}
	if hdr > 0 && au[1]&1 != 1 {
		return nil, errors.New("ADTS frame has a CRC word; the offsets assume protection_absent=1")
	}
	p := au[hdr:]
	var bit int
	switch elem := int(p[0] >> 5); elem {
	case 0: // SCE: 3 type + 4 tag + 8 global_gain + 1 reserved + 2 window_sequence
		bit = 18
	case 1: // CPE
		if p[0]&1 == 1 { // common_window is payload bit 7
			bit = 11 // 3 + 4 + 1 + 1 reserved + 2 window_sequence
		} else {
			bit = 19 // 3 + 4 + 1 + 8 global_gain + 1 reserved + 2 window_sequence
		}
	default:
		return nil, fmt.Errorf("first element type %d is neither SCE nor CPE", elem)
	}
	bad := bytes.Clone(au[:hdr+3])
	bad[hdr+bit/8] ^= 0x80 >> (bit % 8)
	return bad, nil
}

// corruptKind names a way of damaging an access unit.
type corruptKind struct {
	name string
	// mustError is set when every unit it produces has to fail to decode. The
	// shape flip plus 3-byte truncation always overreads inside the element, so
	// it must. The plain truncations can leave a parseable unit (state then
	// legitimately advances), so the lockstep reference is fed those too.
	mustError bool
	build     func(t *testing.T, au []byte, hdr int) []byte
}

var corruptKinds = []corruptKind{
	{"shape_flip_trunc3", true, func(t *testing.T, au []byte, hdr int) []byte {
		t.Helper()
		bad, err := shapeFlipUnit(au, hdr)
		if err != nil {
			t.Fatal(err)
		}
		return bad
	}},
	{"trunc_half", false, func(t *testing.T, au []byte, hdr int) []byte {
		t.Helper()
		return bytes.Clone(au[:hdr+(len(au)-hdr)/2])
	}},
	{"trunc_last_byte", false, func(t *testing.T, au []byte, hdr int) []byte {
		t.Helper()
		return bytes.Clone(au[:len(au)-1])
	}},
}

// runRecovery feeds f0, then for each later frame `repeat` corrupt units
// followed by the frame itself, through the decoder under test. A reference
// decoder sees exactly the units the decoder under test accepted, so every
// valid frame must decode byte-identically to it: a failed unit may leave no
// trace. It returns how many corrupt units errored.
func runRecovery(t *testing.T, mk func() *FrameDecoder, frames [][]byte, hdr int, kind corruptKind, repeat int) int {
	t.Helper()
	d, ref := mk(), mk()
	var got, want []byte
	errored := 0
	for k, f := range frames {
		if k > 0 {
			bad := kind.build(t, f, hdr)
			for range repeat {
				_, _, err := d.DecodeFrame(nil, bad)
				if err != nil {
					errored++
					continue
				}
				if kind.mustError {
					t.Fatalf("frame %d: %s unit decoded without error; the error path was not exercised", k, kind.name)
				}
				// The unit parsed, so decoder state legitimately advanced.
				if _, _, err := ref.DecodeFrame(nil, bad); err != nil {
					t.Fatalf("frame %d: reference rejected a unit the decoder accepted: %v", k, err)
				}
			}
		}
		var err error
		if got, _, err = d.DecodeFrame(got[:0], f); err != nil {
			t.Fatalf("frame %d: decode after corrupt input: %v", k, err)
		}
		if want, _, err = ref.DecodeFrame(want[:0], f); err != nil {
			t.Fatalf("frame %d: reference decode: %v", k, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d (%s, %d corrupt units before it): output differs from a decoder that never saw the corrupt units", k, kind.name, repeat)
		}
	}
	return errored
}

// recoveryStream is one stream of access units for the recovery tests.
type recoveryStream struct {
	name   string
	frames [][]byte
	hdr    int // bytes of per-unit header (7 for ADTS, 0 for raw)
	mk     func() *FrameDecoder
}

func recoveryStreams(t *testing.T) []recoveryStream {
	t.Helper()
	var out []recoveryStream
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"16bit_mono_48k", frameDecoderConfigs[0].cfg},
		{"16bit_stereo_48k", frameDecoderConfigs[2].cfg},
	} {
		pcm := testPCM(4500, tc.cfg)
		out = append(out, recoveryStream{
			name:   "adts_" + tc.name,
			frames: splitADTSFrames(t, encodeADTS(t, tc.cfg, pcm)),
			hdr:    adtsHeaderLen,
			mk:     NewADTSDecoder,
		})
		aus, _ := collectAUs(t, tc.cfg, pcm, 0)
		fe, err := NewFrameEncoder(tc.cfg)
		if err != nil {
			t.Fatalf("NewFrameEncoder: %v", err)
		}
		asc := fe.AudioSpecificConfig()
		out = append(out, recoveryStream{
			name:   "raw_" + tc.name,
			frames: aus,
			mk: func() *FrameDecoder {
				d, err := NewRawDecoder(asc)
				if err != nil {
					t.Fatalf("NewRawDecoder: %v", err)
				}
				return d
			},
		})
	}
	// Corpus streams with real long/short window transitions and PNS.
	for _, name := range []string{"click_s44_128k", "pns_m48_24k"} {
		out = append(out, recoveryStream{
			name:   "adts_" + name,
			frames: splitADTSFrames(t, loadStream(t, name)),
			hdr:    adtsHeaderLen,
			mk:     NewADTSDecoder,
		})
	}
	return out
}

// TestFrameDecoderErrorRecoveryByteExact pins the contract that a unit that
// returns an error consumes no decoder state: after any number of consecutive
// failed units the next valid unit decodes byte-identically to a decoder that
// never saw them. decodeICSInfo shifts the window history in place while it
// parses, so without a rollback a unit that fails after its window_shape bit
// would change how the following frame overlaps.
func TestFrameDecoderErrorRecoveryByteExact(t *testing.T) {
	for _, s := range recoveryStreams(t) {
		if len(s.frames) < 3 {
			t.Fatalf("%s: only %d frames", s.name, len(s.frames))
		}
		for _, kind := range corruptKinds {
			for _, repeat := range []int{1, 2} {
				t.Run(s.name+"/"+kind.name+"/x"+string(rune('0'+repeat)), func(t *testing.T) {
					n := runRecovery(t, s.mk, s.frames, s.hdr, kind, repeat)
					if n == 0 {
						t.Fatal("no corrupt unit produced an error; the error path was not exercised")
					}
				})
			}
		}
	}
}

// TestFrameDecoderResetAfterError checks that Reset after a failed unit yields
// a decoder equivalent to a fresh one, including when an ADTS decoder is then
// fed a stream of a different configuration.
func TestFrameDecoderResetAfterError(t *testing.T) {
	stereoCfg := frameDecoderConfigs[2].cfg
	monoCfg := frameDecoderConfigs[0].cfg
	stereo := splitADTSFrames(t, encodeADTS(t, stereoCfg, testPCM(4500, stereoCfg)))
	mono := splitADTSFrames(t, encodeADTS(t, monoCfg, testPCM(4500, monoCfg)))

	decodeAllFrames := func(d *FrameDecoder, frames [][]byte) []byte {
		var out []byte
		for i, f := range frames {
			var err error
			if out, _, err = d.DecodeFrame(out, f); err != nil {
				t.Fatalf("frame %d: %v", i, err)
			}
		}
		return out
	}
	bad := corruptKinds[0].build(t, stereo[3], adtsHeaderLen)

	t.Run("adts_other_config", func(t *testing.T) {
		d := NewADTSDecoder()
		for _, f := range stereo[:3] {
			if _, _, err := d.DecodeFrame(nil, f); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := d.DecodeFrame(nil, bad); err == nil {
			t.Fatal("corrupt unit decoded without error; the error path was not exercised")
		}
		if err := d.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		got := decodeAllFrames(d, mono)
		want := decodeAllFrames(NewADTSDecoder(), mono)
		if !bytes.Equal(got, want) {
			t.Fatal("Reset after an error then a different config differs from a fresh decoder")
		}
	})

	t.Run("raw_same_stream", func(t *testing.T) {
		aus, _ := collectAUs(t, stereoCfg, testPCM(4500, stereoCfg), 0)
		fe, err := NewFrameEncoder(stereoCfg)
		if err != nil {
			t.Fatal(err)
		}
		asc := fe.AudioSpecificConfig()
		d, err := NewRawDecoder(asc)
		if err != nil {
			t.Fatal(err)
		}
		for _, au := range aus[:3] {
			if _, _, err := d.DecodeFrame(nil, au); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := d.DecodeFrame(nil, corruptKinds[0].build(t, aus[3], 0)); err == nil {
			t.Fatal("corrupt unit decoded without error; the error path was not exercised")
		}
		if err := d.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		fresh, err := NewRawDecoder(asc)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decodeAllFrames(d, aus), decodeAllFrames(fresh, aus)) {
			t.Fatal("raw Reset after an error differs from a fresh decoder")
		}
	})
}

// TestFrameDecoderADTSSteadyStateAllocs is the allocation gate for the ADTS
// FrameDecoder path, mirroring the raw gate: a warmed decoder cycling through a
// stream's frames (window transitions included) must not allocate.
func TestFrameDecoderADTSSteadyStateAllocs(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"mono", frameDecoderConfigs[0].cfg},
		{"stereo", frameDecoderConfigs[2].cfg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := splitADTSFrames(t, encodeADTS(t, tc.cfg, testPCM(4500, tc.cfg)))
			if len(frames) < 4 {
				t.Fatalf("only %d frames", len(frames))
			}
			d := NewADTSDecoder()
			dst := make([]byte, 0, aac.FrameSize*tc.cfg.Channels*2)
			var err error
			for i := range 2 * len(frames) { // warm up configure and any growth paths
				if dst, _, err = d.DecodeFrame(dst[:0], frames[i%len(frames)]); err != nil {
					t.Fatal(err)
				}
			}
			i := 0
			allocs := testing.AllocsPerRun(50, func() {
				if dst, _, err = d.DecodeFrame(dst[:0], frames[i%len(frames)]); err != nil {
					t.Fatal(err)
				}
				i++
			})
			t.Logf("%.2f allocs/DecodeFrame", allocs)
			if allocs > 0 {
				t.Errorf("steady-state ADTS DecodeFrame allocates %.2f/op, want 0", allocs)
			}
		})
	}
}

// TestFrameDecoderIndependentInstancesConcurrent pins that distinct
// FrameDecoders share no mutable state: eight goroutines, each with its own
// ADTS and raw decoder, must reproduce the serial result. Run under -race it
// catches a shared global; it never shares one decoder across goroutines, which
// the type forbids.
func TestFrameDecoderIndependentInstancesConcurrent(t *testing.T) {
	var streams []recoveryStream
	for _, s := range recoveryStreams(t) {
		if s.name == "adts_16bit_stereo_48k" || s.name == "raw_16bit_mono_48k" {
			streams = append(streams, s)
		}
	}
	if len(streams) != 2 {
		t.Fatalf("selected %d streams, want 2", len(streams))
	}
	decode := func(s recoveryStream) ([]byte, error) {
		d := s.mk()
		var out []byte
		for _, f := range s.frames {
			var err error
			if out, _, err = d.DecodeFrame(out, f); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	want := make([][]byte, len(streams))
	for i, s := range streams {
		var err error
		if want[i], err = decode(s); err != nil {
			t.Fatalf("%s: serial decode: %v", s.name, err)
		}
	}
	var wg sync.WaitGroup
	for g := range 8 {
		for i, s := range streams {
			wg.Go(func() {
				got, err := decode(s)
				if err != nil {
					t.Errorf("goroutine %d %s: %v", g, s.name, err)
					return
				}
				if !bytes.Equal(got, want[i]) {
					t.Errorf("goroutine %d %s: output differs from the serial decode", g, s.name)
				}
			})
		}
	}
	wg.Wait()
}
