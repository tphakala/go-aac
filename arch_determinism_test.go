// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// TestEncoderArchDeterminism is the end-to-end guard for issue #59: encoding a
// fixed input must yield a byte-identical AAC stream on every architecture. It
// pins the ADTS SHA-256 of the synthetic castanets corpus across all three
// coders and three bitrates against committed goldens. CI runs the test job on
// ubuntu-latest and windows-latest (amd64) and macos-latest (arm64), all
// against this one golden, so a GOARCH-dependent divergence trips it on an arch.
//
// The castanets corpus is used rather than the tonal one because its generated
// input PCM is EMPIRICALLY byte-identical across arm64 and amd64 (verified by
// hashing), whereas synthStereoNMR's is not. The cause is float64 FMA
// contraction: gc fuses a float64 a*b+c into one rounded op on arm64 but not on
// default (GOAMD64=v1) amd64. In synthStereoNMR that fires in math.Sin's own
// polynomial (math.Sin is pure-Go on both arches, so gc contracting it to FMADDD
// on arm64 makes math.Sin(x) itself differ across arches) and in its own
// 0.32*Sin(a)+0.22*Sin(b) sum, so a few of its float32 samples differ across
// arches. The castanets generator also uses transcendentals but its sample
// values round to identical float32 on both arches (hence "empirically"). The
// tonal INPUT is thus not reproducible independent of the encoder, which is what
// read as an encoder GOARCH split in issue #59. This
// golden therefore also depends on the input generator staying arch-stable; a
// future Go math change could require regenerating it on both arches. The
// encoder is deterministic given identical input, which this test asserts.
func TestEncoderArchDeterminism(t *testing.T) {
	casta := synthCastanets(44100*6, 44100, 0x0badcafe, 0)
	castaR := synthCastanets(44100*6, 44100, 0x5eed1234, 137)
	src := [][]float32{casta, castaR}

	golden := map[string]string{
		"fast_96000":     "dfb56e44120d37b1685e613409915250ad1a8b8492acf48d2653cb22d1c7fc7c",
		"fast_128000":    "c60e22b24d6fd6074f3a02fdfb9f69f358ac6ab2d3dfc67365da49652babe1fc",
		"fast_192000":    "754c504f0a3106412de0c93c22f6be61f1686489467c1956f72ce5652587b20e",
		"nmr_96000":      "5119dc32e132837e99933790400ef1010d8f0c4b92fe65e142693782373679ee",
		"nmr_128000":     "8e312290271b849aed93cbdfcaeb2fb55da58ce040cf6ec86b5cade61e573df6",
		"nmr_192000":     "484a283cd0cc935221fcf722c653da470697feea125fa50697a9511890229f1a",
		"twoloop_96000":  "12ea9b39e325444dc00b3baef65d74639d141e6798fa4a33d4f717e643a6d8d4",
		"twoloop_128000": "dd1bada1f176492e4692cae711875929e036b199a3390397b238aaf873217e33",
		"twoloop_192000": "6cd66c224e237c7f5407836bb8ae5cbe95507e92c421aa78d1b2d3d0b9bf4793",
	}

	coders := []struct {
		name string
		kind enc.CoderKind
	}{
		{coderNMR, enc.CoderNMR},
		{coderTwoLoop, enc.CoderTwoLoop},
		{coderFast, enc.CoderFast},
	}
	for _, c := range coders {
		for _, br := range []int{96000, 128000, 192000} {
			key := fmt.Sprintf("%s_%d", c.name, br)
			t.Run(key, func(t *testing.T) {
				stream := encodeADTSPlanar(t,
					enc.Config{SampleRate: 44100, Bitrate: br, Channels: 2, Coder: c.kind},
					src)
				sum := sha256.Sum256(stream)
				got := hex.EncodeToString(sum[:])
				if got != golden[key] {
					t.Errorf("ADTS sha256 = %s, want %s (encoder output is not reproducible; "+
						"if this is an intentional encoder change, update the golden on both arm64 and amd64)",
						got, golden[key])
				}
			})
		}
	}
}
