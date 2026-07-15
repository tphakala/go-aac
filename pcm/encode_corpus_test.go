// SPDX-License-Identifier: LGPL-2.1-or-later
package pcm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// ffmpegBin returns the pinned FFmpeg CLI from GOAAC_FFMPEG, skipping the
// test when it is not set.
func ffmpegBin(t *testing.T) string {
	t.Helper()
	p := os.Getenv("GOAAC_FFMPEG")
	if p == "" {
		t.Skip("pinned ffmpeg not set; set GOAAC_FFMPEG")
	}
	return p
}

// corpusWAV locates a corpus recording under GOAAC_CORPUS_DIR, skipping the
// test when it is not set.
func corpusWAV(t *testing.T, name string) string {
	t.Helper()
	dir := os.Getenv("GOAAC_CORPUS_DIR")
	if dir == "" {
		t.Skip("corpus dir not set; set GOAAC_CORPUS_DIR")
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("corpus recording %s not found under GOAAC_CORPUS_DIR", p)
	}
	return p
}

// wavPCM reads a canonical RIFF/WAVE file and returns its raw interleaved
// little-endian PCM data plus format fields. Test-only minimal parser.
func wavPCM(t *testing.T, path string) (data []byte, rate, channels, bits int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 44 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		t.Fatalf("%s: not a RIFF/WAVE file", path)
	}
	off := 12
	for off+8 <= len(raw) {
		id := string(raw[off : off+4])
		sz := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := raw[off+8 : off+8+sz]
		switch id {
		case "fmt ":
			// Accept plain PCM (1) and WAVE_FORMAT_EXTENSIBLE (0xFFFE)
			// with the PCM subformat GUID (tawnyowl.wav is extensible).
			switch f := binary.LittleEndian.Uint16(body[0:2]); f {
			case 1:
			case 0xFFFE:
				if len(body) < 40 || binary.LittleEndian.Uint16(body[24:26]) != 1 {
					t.Fatalf("%s: extensible WAV without PCM subformat", path)
				}
			default:
				t.Fatalf("%s: not integer PCM (format %d)", path, f)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			data = body
		}
		off += 8 + sz + sz%2
	}
	if data == nil || rate == 0 {
		t.Fatalf("%s: missing fmt or data chunk", path)
	}
	return data, rate, channels, bits
}

// srcFloats converts interleaved integer PCM to per-channel float32 with
// the same scaling the encoder uses, for PSNR reference.
func srcFloats(data []byte, channels, bits int) [][]float32 {
	bytesPS := bits / 8
	n := len(data) / bytesPS / channels
	out := make([][]float32, channels)
	for c := range out {
		out[c] = make([]float32, n)
	}
	for i := range n {
		for c := range channels {
			o := (i*channels + c) * bytesPS
			var v int32
			switch bits {
			case 16:
				v = int32(int16(binary.LittleEndian.Uint16(data[o:])))
				out[c][i] = float32(v) / 32768
			case 24:
				v = int32(data[o]) | int32(data[o+1])<<8 | int32(data[o+2])<<16
				v = (v << 8) >> 8
				out[c][i] = float32(v) / 8388608
			case 32:
				v = int32(binary.LittleEndian.Uint32(data[o:]))
				out[c][i] = float32(v) / 2147483648
			}
		}
	}
	return out
}

// ffmpegDecode decodes an ADTS file to raw f32le with the pinned ffmpeg,
// failing on any decoder diagnostic.
func ffmpegDecode(t *testing.T, ffmpeg, path string, channels int) [][]float32 {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", path, "-f", "f32le", "-c:a", "pcm_f32le", "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg decode failed: %v\n%s", err, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("ffmpeg -v error reported diagnostics:\n%s", stderr.String())
	}
	raw := stdout.Bytes()
	n := len(raw) / 4 / channels
	out := make([][]float32, channels)
	for c := range out {
		out[c] = make([]float32, n)
	}
	for i := range n {
		for c := range channels {
			out[c][i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*channels+c)*4:]))
		}
	}
	return out
}

// psnr computes 10*log10(1/MSE) against full scale 1.0 between src and
// dec[delay:]. delay drops the 1024-sample encoder priming (the gapless
// caveat: ADTS cannot signal encoder delay, so decoders emit the priming).
func psnr(src, dec []float32, delay int) float64 {
	if len(dec) < delay+len(src) {
		// Truncated decode: fail any PSNR floor rather than score a prefix as
		// perfect. A stream that decodes short must not satisfy the gates.
		return math.Inf(-1)
	}
	var mse float64
	for i := range src {
		d := float64(src[i]) - float64(dec[delay+i])
		mse += d * d
	}
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(1/(mse/float64(len(src))))
}

// oddChunkReader hands out data in fixed odd-sized chunks, forcing
// io.Copy-style consumers through the partial-sample buffering path.
type oddChunkReader struct {
	data  []byte
	chunk int
}

func (r *oddChunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(r.chunk, len(r.data), len(p))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// TestBirdNETGoIntegration is the acceptance test from issue #8: the
// BirdNET-Go call shape from docs/api.md, run on the real recordings, must
// produce streams that ffmpeg decodes with zero diagnostics and Apple
// afconvert accepts, at the PSNR the Phase 3 gate established. Two paths
// are exercised: the one-shot EncodeInterleaved exactly as BirdNET-Go
// calls it, and streaming io.Copy with a 7919-byte buffer (which never
// divides the sample stride), asserting both produce identical bytes.
func TestBirdNETGoIntegration(t *testing.T) {
	ffmpeg := ffmpegBin(t)
	cases := []struct {
		wav     string
		bitrate int
		floorDB float64 // measured Phase 3 NMR baseline minus 0.10 tolerance
	}{
		{"soundscape.wav", 128000, 85.34},
		{"tawnyowl.wav", 128000, 63.80},
		// The exact docs/api.md BirdNET-Go snippet shape (96 kb/s mono).
		// Floor from the rehearsal measurement (83.52 dB) minus 0.10.
		{"soundscape.wav", 96000, 83.42},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_%dk", tc.wav, tc.bitrate/1000), func(t *testing.T) {
			data, rate, channels, bits := wavPCM(t, corpusWAV(t, tc.wav))
			cfg := Config{SampleRate: rate, BitDepth: bits, Channels: channels, Bitrate: tc.bitrate}

			// Path 1: the exact BirdNET-Go one-shot call shape.
			var oneShot bytes.Buffer
			if err := EncodeInterleaved(&oneShot, cfg, data); err != nil {
				t.Fatal(err)
			}

			// Path 2: streaming via io.Copy with an awkward buffer size.
			var streamed bytes.Buffer
			enc, err := NewEncoder(&streamed, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.CopyBuffer(enc, &oddChunkReader{data: data, chunk: 7919}, make([]byte, 7919)); err != nil {
				t.Fatal(err)
			}
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(oneShot.Bytes(), streamed.Bytes()) {
				t.Fatalf("io.Copy stream (%d B) differs from one-shot (%d B)", streamed.Len(), oneShot.Len())
			}

			dir := t.TempDir()
			adts := filepath.Join(dir, "out.adts")
			if err := os.WriteFile(adts, oneShot.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}

			// Gate leg 1: pinned ffmpeg decodes with zero diagnostics.
			dec := ffmpegDecode(t, ffmpeg, adts, channels)

			// Gate leg 2: PSNR against the source at the Phase 3 baseline,
			// dropping the 1024-sample priming.
			src := srcFloats(data, channels, bits)
			for c := range channels {
				p := psnr(src[c], dec[c], 1024)
				t.Logf("%s ch%d: %.2f dB (%d bytes, %.1f kbps)", tc.wav, c, p,
					oneShot.Len(), float64(oneShot.Len())*8*float64(rate)/float64(len(src[c]))/1000)
				if p < tc.floorDB {
					t.Errorf("PSNR %.2f dB below floor %.2f dB", p, tc.floorDB)
				}
			}

			// Gate leg 3: Apple's decoder (stricter than ffmpeg) accepts it.
			aacIn := filepath.Join(dir, "out.aac") // afconvert infers ADTS from .aac
			if err := os.Rename(adts, aacIn); err != nil {
				t.Fatal(err)
			}
			if _, err := exec.LookPath("afconvert"); err != nil {
				t.Skip("afconvert not available")
			}
			out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", aacIn,
				filepath.Join(dir, "apple.wav")).CombinedOutput()
			if err != nil {
				t.Fatalf("afconvert rejected the stream: %v\n%s", err, out)
			}
		})
	}
}
