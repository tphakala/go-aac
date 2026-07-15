// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/enc"
)

// encodeADTSPlanar runs the encoder over planar src and returns an ADTS
// stream (Phase 2: mono or stereo).
func encodeADTSPlanar(t *testing.T, cfg enc.Config, src [][]float32) []byte {
	t.Helper()
	e, err := enc.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srIdx, ok := sampleRateIndex(cfg.SampleRate)
	if !ok {
		t.Fatalf("no samplerate index for %d", cfg.SampleRate)
	}
	var stream []byte
	au := make([]byte, 0, 1536*cfg.Channels)
	wrap := func(payload []byte) {
		if len(payload) == 0 {
			return
		}
		stream = appendADTSHeader(stream, srIdx, cfg.Channels, len(payload))
		stream = append(stream, payload...)
	}
	frame := make([][]float32, cfg.Channels)
	for off := 0; off < len(src[0]); off += 1024 {
		for ch := range cfg.Channels {
			frame[ch] = src[ch][off:min(off+1024, len(src[ch]))]
		}
		au, err = e.EncodeFrame(au[:0], frame)
		if err != nil {
			t.Fatal(err)
		}
		wrap(au)
	}
	for !e.Drained() {
		au, err = e.EncodeFrame(au[:0], nil)
		if err != nil {
			t.Fatal(err)
		}
		wrap(au)
	}
	return stream
}

// TestWriteADTSArtifact2 encodes a raw planar f32le file to ADTS for the
// external gate legs. Rehearsal helper driven by environment variables:
// GOAAC2_IN, GOAAC2_OUT, GOAAC2_CH, GOAAC2_RATE, GOAAC2_BR.
func TestWriteADTSArtifact2(t *testing.T) {
	in := os.Getenv("GOAAC2_IN")
	out := os.Getenv("GOAAC2_OUT")
	if in == "" || out == "" {
		t.Skip("set GOAAC2_IN/GOAAC2_OUT")
	}
	ch, err := strconv.Atoi(os.Getenv("GOAAC2_CH"))
	if err != nil || ch < 1 || ch > 2 {
		t.Fatalf("GOAAC2_CH must be 1 or 2, got %q", os.Getenv("GOAAC2_CH"))
	}
	rate, _ := strconv.Atoi(os.Getenv("GOAAC2_RATE"))
	br, _ := strconv.Atoi(os.Getenv("GOAAC2_BR"))
	raw, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	n := len(raw) / 4 / ch
	src := make([][]float32, ch)
	for c := range ch {
		src[c] = make([]float32, n)
		for i := range n {
			bitsv := uint32(raw[(c*n+i)*4]) | uint32(raw[(c*n+i)*4+1])<<8 |
				uint32(raw[(c*n+i)*4+2])<<16 | uint32(raw[(c*n+i)*4+3])<<24
			src[c][i] = f32frombits(bitsv)
		}
	}
	stream := encodeADTSPlanar(t,
		enc.Config{SampleRate: rate, Bitrate: br, Channels: ch, Coder: enc.CoderFast, DisableTNS: true, DisablePNS: true}, src)
	if err := os.WriteFile(out, stream, 0o644); err != nil {
		t.Fatal(err)
	}
}

func f32frombits(b uint32) float32 { return math.Float32frombits(b) }

// synthCastanets renders the transient gate signal: a quiet tonal bed with
// a sharp decaying noise click every 0.45 s (castanet-like), the material
// that must drive EIGHT_SHORT.
func synthCastanets(n, rate int, seed uint32, clickOff int) []float32 {
	src := make([]float32, n)
	for i := range n {
		ts := float64(i) / float64(rate)
		src[i] = float32(0.05 * math.Sin(2*math.Pi*330*ts))
	}
	state := seed
	step := int(0.45 * float64(rate))
	for at := rate/4 + clickOff; at < n-1000; at += step {
		for k := range 900 {
			state = state*1664525 + 1013904223
			r := float64(int32(state)) / 2147483648.0
			src[at+k] += float32(0.8 * math.Exp(-float64(k)/140.0) * r)
		}
	}
	return src
}

// adtsFrames splits an ADTS stream into raw_data_block payloads.
func adtsFrames(t *testing.T, d []byte) [][]byte {
	t.Helper()
	var out [][]byte
	off := 0
	for off+7 <= len(d) {
		if d[off] != 0xFF || d[off+1]&0xF0 != 0xF0 {
			t.Fatalf("ADTS sync lost at %d", off)
		}
		flen := int(d[off+3]&0x03)<<11 | int(d[off+4])<<3 | int(d[off+5])>>5
		out = append(out, d[off+7:off+flen])
		off += flen
	}
	return out
}

// bitReader is a minimal MSB-first reader for the window-sequence parse.
type bitReader struct {
	b   []byte
	pos int
}

func (r *bitReader) get(n int) uint32 {
	var v uint32
	for range n {
		byteIdx := r.pos >> 3
		bit := (r.b[byteIdx] >> (7 - (r.pos & 7))) & 1
		v = v<<1 | uint32(bit)
		r.pos++
	}
	return v
}

// windowSeqSCE extracts the window_sequence of the first SCE in each frame
// payload, skipping FIL elements (the C encoder emits an ident FIL on
// frame 1 unless bitexact).
func windowSeqSCE(t *testing.T, frames [][]byte) []int {
	t.Helper()
	out := make([]int, 0, len(frames))
	for _, p := range frames {
		r := &bitReader{b: p}
		id := r.get(3)
		for id == coder.TypeFIL {
			cnt := r.get(4)
			if cnt == 15 {
				cnt += r.get(8) - 1
			}
			r.pos += int(cnt) * 8
			id = r.get(3)
		}
		if id != coder.TypeSCE {
			t.Fatalf("unexpected element id %d", id)
		}
		r.get(4) // instance
		r.get(8) // global_gain
		r.get(1) // ics_reserved
		out = append(out, int(r.get(2)))
	}
	return out
}

// TestWindowSequenceVsC is the Phase 2 window-decision gate: the Go
// encoder's per-frame window sequences on transient (castanet-like) and
// tonal material must match the pinned C encoder's (fast coder, tools
// off, bitexact) on more than 99% of frames, and EIGHT_SHORT must
// actually trigger on the transient material.
func TestWindowSequenceVsC(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	for _, tc := range []struct {
		name string
		src  []float32
	}{
		{"castanets", synthCastanets(44100*6, 44100, 0x0badcafe, 0)},
		{"tonal", synthTonal(44100*5, 44100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rawPath := filepath.Join(dir, "src.f32")
			raw := make([]byte, 4*len(tc.src))
			for i, v := range tc.src {
				binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(v))
			}
			if err := os.WriteFile(rawPath, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			cPath := filepath.Join(dir, "c.adts")
			cmd := exec.Command(ffmpeg, "-v", "error", "-f", "f32le", "-ar", "44100",
				"-ac", "1", "-i", rawPath, "-c:a", "aac", "-aac_coder", "fast",
				"-aac_tns", "0", "-aac_pns", "0", "-aac_is", "0", "-aac_ms", "0",
				"-b:a", "128k", "-flags", "+bitexact", "-f", "adts", cPath, "-y")
			if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
				t.Fatalf("C encode: %v %q", err, out)
			}
			cStream, err := os.ReadFile(cPath)
			if err != nil {
				t.Fatal(err)
			}
			goStream := encodeADTSPlanar(t,
				enc.Config{SampleRate: 44100, Bitrate: 128000, Channels: 1, Coder: enc.CoderFast, DisableTNS: true, DisablePNS: true},
				[][]float32{tc.src})

			goSeq := windowSeqSCE(t, adtsFrames(t, goStream))
			cSeq := windowSeqSCE(t, adtsFrames(t, cStream))
			if len(goSeq) != len(cSeq) {
				t.Fatalf("frame counts differ: go %d c %d", len(goSeq), len(cSeq))
			}
			same, shortsGo, shortsC := 0, 0, 0
			for i := range goSeq {
				if goSeq[i] == cSeq[i] {
					same++
				}
				if goSeq[i] == coder.EightShortSequence {
					shortsGo++
				}
				if cSeq[i] == coder.EightShortSequence {
					shortsC++
				}
			}
			pct := 100 * float64(same) / float64(len(goSeq))
			t.Logf("%s: %d/%d identical (%.1f%%), EIGHT_SHORT go=%d c=%d",
				tc.name, same, len(goSeq), pct, shortsGo, shortsC)
			if pct <= 99 {
				t.Errorf("window sequences match on %.1f%%, gate demands > 99%%", pct)
			}
			if tc.name == "castanets" && (shortsGo == 0 || shortsC == 0) {
				t.Errorf("transient material did not trigger EIGHT_SHORT (go %d, c %d)",
					shortsGo, shortsC)
			}
		})
	}
}

// TestPhase2DecodeGate encodes the Phase 2 gate material (transient mono,
// decorrelated stereo, stereo with common-window short blocks), decodes
// with the pinned ffmpeg at -v error and asserts the rehearsed PSNR
// floors, ~1 dB under the rehearsed worst-channel measurements (tonal
// mono 70.89 dB, castanets mono 60.64 dB, stereo decorrelated 37.55 dB,
// stereo castanets 37.47 dB; the C fast coder with tools off measures the
// identical 37.47 dB on the stereo castanets case, so the lower stereo
// numbers are the algorithm at 64 kbps/channel, not a port defect).
func TestPhase2DecodeGate(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	cast := synthCastanets(44100*6, 44100, 0x0badcafe, 0)
	tonal := synthTonal(44100*5, 44100)
	castR := synthCastanets(44100*5, 44100, 0x5eed1234, 137)
	for _, tc := range []struct {
		name  string
		src   [][]float32
		floor float64
	}{
		{"tonal mono", [][]float32{tonal}, 69.0},
		{"castanets mono", [][]float32{cast}, 59.0},
		{"stereo decorrelated", [][]float32{tonal, castR[:len(tonal)]}, 36.5},
		{"stereo common short", [][]float32{cast, cast}, 36.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := len(tc.src)
			stream := encodeADTSPlanar(t,
				enc.Config{SampleRate: 44100, Bitrate: 128000, Channels: ch, Coder: enc.CoderFast, DisableTNS: true, DisablePNS: true}, tc.src)
			dir := t.TempDir()
			adts := filepath.Join(dir, "out.adts")
			if err := os.WriteFile(adts, stream, 0o644); err != nil {
				t.Fatal(err)
			}
			dec := ffmpegDecode(t, ffmpeg, adts) // interleaved for stereo
			const delay = 1024
			worst := math.Inf(1)
			for c := range ch {
				dc := make([]float32, len(dec)/ch)
				for i := range dc {
					dc[i] = dec[i*ch+c]
				}
				p := psnr(tc.src[c], dc, delay)
				t.Logf("%s ch %d PSNR %.2f dB", tc.name, c, p)
				worst = math.Min(worst, p)
			}
			if worst < tc.floor {
				t.Errorf("PSNR %.2f dB below the %.1f dB floor", worst, tc.floor)
			}
		})
	}
}
