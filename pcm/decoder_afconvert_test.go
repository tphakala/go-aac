// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeTestWAV writes a canonical 16-bit PCM WAV of a low-frequency sine to
// path, so afconvert has a real signal to encode. The tone is well within the
// AAC coding bandwidth so both decoders reconstruct it identically.
func writeTestWAV(t *testing.T, path string, sampleRate, channels, samples int) {
	t.Helper()
	const bits = 16
	dataSize := samples * channels * (bits / 8)
	buf := make([]byte, 0, 44+dataSize)
	put := func(b ...byte) { buf = append(buf, b...) }
	putU32 := func(v uint32) { buf = binary.LittleEndian.AppendUint32(buf, v) }
	putU16 := func(v uint16) { buf = binary.LittleEndian.AppendUint16(buf, v) }

	put('R', 'I', 'F', 'F')
	putU32(uint32(36 + dataSize))
	put('W', 'A', 'V', 'E')
	put('f', 'm', 't', ' ')
	putU32(16)
	putU16(1) // PCM
	putU16(uint16(channels))
	putU32(uint32(sampleRate))
	putU32(uint32(sampleRate * channels * (bits / 8))) // byte rate
	putU16(uint16(channels * (bits / 8)))              // block align
	putU16(bits)
	put('d', 'a', 't', 'a')
	putU32(uint32(dataSize))

	for i := range samples {
		v := int16(0.3 * math.MaxInt16 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		for range channels {
			buf = binary.LittleEndian.AppendUint16(buf, uint16(v))
		}
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
}

// TestAfconvertParity encodes a signal to AAC-LC ADTS with Apple's afconvert,
// then asserts the public decoder reproduces the pinned ffmpeg aac_fixed decode
// byte for byte. This proves go-aac decodes Apple-encoded LC streams, not only
// FFmpeg-encoded ones. It is skipped when afconvert (macOS only) or the oracle
// is unavailable, so CI on other platforms stays hermetic.
func TestAfconvertParity(t *testing.T) {
	afconvert, err := exec.LookPath("afconvert")
	if err != nil {
		t.Skip("afconvert not available (macOS only)")
	}
	ff := ffmpegBin(t)

	dir := t.TempDir()
	for _, tc := range []struct {
		name              string
		sampleRate, chans int
	}{
		{"mono_44100", 44100, 1},
		{"stereo_48000", 48000, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wav := filepath.Join(dir, tc.name+".wav")
			aac := filepath.Join(dir, tc.name+".aac")
			writeTestWAV(t, wav, tc.sampleRate, tc.chans, tc.sampleRate) // 1 second

			// afconvert has no overwrite flag; ensure the target is absent.
			_ = os.Remove(aac)
			// -b >= 96 kbps keeps afconvert from downsampling; -d aac forces LC
			// (never HE-AAC/SBR, which the decoder does not support).
			cmd := exec.Command(afconvert, "-f", "adts", "-d", "aac", "-b", "128000", "-s", "0", wav, aac)
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				t.Fatalf("afconvert: %v\n%s", cerr, out)
			}

			raw, rerr := os.ReadFile(aac)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if len(raw) < 2 || raw[0] != 0xff || raw[1]&0xf0 != 0xf0 {
				t.Fatalf("afconvert output is not ADTS: % x", raw[:min(4, len(raw))])
			}

			got := decodeStream(t, aac)

			cmdO := exec.Command(ff, "-loglevel", "error", "-bitexact", "-c:a",
				"aac_fixed", "-i", aac, "-bitexact", "-f", "s16le", "-")
			want, werr := cmdO.Output()
			if werr != nil {
				t.Fatalf("oracle decode: %v", werr)
			}
			if len(got) != len(want) {
				t.Fatalf("length mismatch: pcm %d, oracle %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("byte %d: pcm 0x%02x oracle 0x%02x", i, got[i], want[i])
				}
			}
			t.Logf("afconvert %s: %d bytes byte-identical to oracle", tc.name, len(got))
		})
	}
}
