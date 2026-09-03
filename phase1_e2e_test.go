// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"fmt"
	"math"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
	"github.com/tphakala/go-aac/internal/oracletest"
)

// synthTonal generates n samples of a deterministic three-tone mix, the
// tonal Phase 1 gate signal (issue #3: tonal corpus).
func synthTonal(n, rate int) []float32 {
	src := make([]float32, n)
	for i := range n {
		ts := float64(i) / float64(rate)
		v := 0.35*math.Sin(2*math.Pi*220*ts) +
			0.25*math.Sin(2*math.Pi*997*ts) +
			0.10*math.Sin(2*math.Pi*3800*ts)
		src[i] = float32(v)
	}
	return src
}

// encodeADTS runs the encoder over mono src and returns an ADTS stream. It is
// encodeADTSPlanar (phase2_e2e_test.go) with the single channel wrapped, so the
// feed-frame-and-drain loop lives in one place.
func encodeADTS(t *testing.T, cfg enc.Config, src []float32) []byte {
	t.Helper()
	return encodeADTSPlanar(t, cfg, [][]float32{src})
}

func TestPhase1Gate(t *testing.T) {
	ffmpeg := oracletest.FFmpegBin(t)
	for _, rate := range []int{44100, 48000} {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			n := rate * 5
			src := synthTonal(n, rate)
			stream := encodeADTS(t, enc.Config{SampleRate: rate, Bitrate: 128000, Channels: 1, Coder: enc.CoderFast, DisableTNS: true, DisablePNS: true}, src)

			// Mono, so the one decoded channel is the signal; the issue #3
			// gate demands zero decoder diagnostics at -v error, which
			// oracletest.DecodeStream enforces.
			dec := oracletest.DecodeStream(t, ffmpeg, stream, 1, EncoderDelay)[0]
			if len(dec) < n {
				t.Fatalf("decoded %d samples, want >= %d", len(dec), n)
			}
			got := oracletest.PSNRPrefix(src, dec, EncoderDelay)
			t.Logf("rate %d: %d bytes ADTS, decoded %d samples, PSNR %.2f dB",
				rate, len(stream), len(dec), got)
			if got < 30 {
				t.Errorf("PSNR %.2f dB below the 30 dB Phase 1 gate", got)
			}
		})
	}
}
