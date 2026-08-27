// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/tphakala/go-aac/internal/enc"
)

// Channel-layout labels for the arch corpus golden keys and subtest names.
const (
	archChanMono   = "mono"   // single_channel_element (SCE)
	archChanStereo = "stereo" // channel_pair_element (CPE)
)

// archCase is one (coder, channel layout, sample rate, bitrate) cell of the
// cross-arch determinism corpus. TestEncoderArchDeterminism pins each cell's
// ADTS SHA-256; TestArchHashCorpus prints the same cells for cross-arch
// reproduction. Both drive off archCases so the gate and its repro tool cannot
// drift apart.
type archCase struct {
	coder    string
	kind     enc.CoderKind
	chLabel  string
	channels int
	rate     int
	bitrate  int
}

// archCoders is the coder axis shared by the gate and the repro diagnostic.
var archCoders = []struct {
	name string
	kind enc.CoderKind
}{
	{coderNMR, enc.CoderNMR},
	{coderTwoLoop, enc.CoderTwoLoop},
	{coderFast, enc.CoderFast},
}

// archCases enumerates the determinism corpus: the original stereo/44100 sweep
// across three bitrates, plus mono/44100, stereo/48000 and mono/48000 pinned at
// a single representative bitrate (128000, the mid value of the swept set; note
// this is not DefaultBitrate, which is 200000). The additions gate the SCE
// (mono) path and the 48 kHz tables that the original stereo/44100 goldens never
// exercised; the bitrate axis is already swept three deep on the primary
// stereo/44100 config, so the new axes need only one representative bitrate.
func archCases() []archCase {
	cases := make([]archCase, 0, len(archCoders)*6)
	for _, c := range archCoders {
		for _, br := range []int{96000, 128000, 192000} {
			cases = append(cases, archCase{c.name, c.kind, archChanStereo, 2, 44100, br})
		}
		cases = append(cases,
			archCase{c.name, c.kind, archChanMono, 1, 44100, 128000},
			archCase{c.name, c.kind, archChanStereo, 2, 48000, 128000},
			archCase{c.name, c.kind, archChanMono, 1, 48000, 128000},
		)
	}
	return cases
}

// archKey is the golden-map key and subtest name for a case:
// "<coder>_<mono|stereo>_<rate>_<bitrate>".
func archKey(c archCase) string {
	return fmt.Sprintf("%s_%s_%d_%d", c.coder, c.chLabel, c.rate, c.bitrate)
}

// archCastanetsInput builds the castanets corpus input for a channel layout and
// sample rate. Each channel is one synthCastanets render; mono is the left
// channel alone. The left channel (seed 0x0badcafe) is byte-identical to the
// channel the stereo cases use, so mono reuses already arch-verified input, and
// the 48000 Hz renders were independently verified byte-identical on amd64 and
// arm64 (raw float32 input hashes) before their goldens were pinned.
func archCastanetsInput(chLabel string, rate int) [][]float32 {
	left := synthCastanets(rate*6, rate, 0x0badcafe, 0)
	if chLabel == archChanMono {
		return [][]float32{left}
	}
	right := synthCastanets(rate*6, rate, 0x5eed1234, 137)
	return [][]float32{left, right}
}

// TestEncoderArchDeterminism is the end-to-end guard for issue #59: encoding a
// fixed input must yield a byte-identical AAC stream on every architecture. It
// pins the ADTS SHA-256 of the synthetic castanets corpus across the full
// shipped config surface: all three coders and three bitrates on stereo/44100,
// plus mono (SCE) at 44100, and both stereo and mono at 48000, so the SCE path
// and the 48 kHz tables are gated rather than only the stereo/44100 cases. CI
// runs the test job on ubuntu-latest and windows-latest (amd64) and
// macos-latest (arm64), all against these goldens, so a GOARCH-dependent
// divergence trips it on an arch.
//
// The castanets corpus is used rather than the tonal one because its generated
// input PCM is EMPIRICALLY byte-identical across arm64 and amd64 (verified by
// hashing the raw float32 input; the input-hash lines in TestArchHashCorpus are
// the fast way to re-check this), whereas synthStereoNMR's is not. The cause is
// float64 FMA contraction: gc fuses a float64 a*b+c into one rounded op on arm64
// but not on default (GOAMD64=v1) amd64. In synthStereoNMR that fires in
// math.Sin's own polynomial (math.Sin is pure-Go on both arches, so gc
// contracting it to FMADDD on arm64 makes math.Sin(x) itself differ across
// arches) and in its own 0.32*Sin(a)+0.22*Sin(b) sum, so a few of its float32
// samples differ across arches. The castanets generator also uses
// transcendentals but its sample values round to identical float32 on both
// arches at both 44100 and 48000 (hence "empirically"). The mono cases reuse
// the already-verified stereo left channel. These goldens therefore also depend
// on the input generator staying arch-stable; a future Go math change could
// require regenerating them on both arches. The encoder is deterministic given
// identical input, which this test asserts.
func TestEncoderArchDeterminism(t *testing.T) {
	golden := map[string]string{
		// Original stereo/44100 sweep (values unchanged, re-keyed).
		"nmr_stereo_44100_96000":      "5119dc32e132837e99933790400ef1010d8f0c4b92fe65e142693782373679ee",
		"nmr_stereo_44100_128000":     "8e312290271b849aed93cbdfcaeb2fb55da58ce040cf6ec86b5cade61e573df6",
		"nmr_stereo_44100_192000":     "484a283cd0cc935221fcf722c653da470697feea125fa50697a9511890229f1a",
		"twoloop_stereo_44100_96000":  "12ea9b39e325444dc00b3baef65d74639d141e6798fa4a33d4f717e643a6d8d4",
		"twoloop_stereo_44100_128000": "dd1bada1f176492e4692cae711875929e036b199a3390397b238aaf873217e33",
		"twoloop_stereo_44100_192000": "6cd66c224e237c7f5407836bb8ae5cbe95507e92c421aa78d1b2d3d0b9bf4793",
		"fast_stereo_44100_96000":     "dfb56e44120d37b1685e613409915250ad1a8b8492acf48d2653cb22d1c7fc7c",
		"fast_stereo_44100_128000":    "c60e22b24d6fd6074f3a02fdfb9f69f358ac6ab2d3dfc67365da49652babe1fc",
		"fast_stereo_44100_192000":    "754c504f0a3106412de0c93c22f6be61f1686489467c1956f72ce5652587b20e",
		// Mono/44100 (SCE path), pinned at the representative 128000 bitrate.
		"nmr_mono_44100_128000":     "deb44fee6f012d36dea4ea19955b46cb2cfa2fd68eb9d4d31ad25ceceb985d76",
		"twoloop_mono_44100_128000": "3d1b784317acf9c05994a71c5f0f117401fde7c66b18092eee120266c86137f1",
		"fast_mono_44100_128000":    "e047132ea1ee25dc5bb52b392e9540f2be4d160e820e06755d66b54fdcab0ea4",
		// Stereo/48000 (48 kHz tables), pinned at the representative 128000 bitrate.
		"nmr_stereo_48000_128000":     "d5c15bf85eef9515394c2cf0832dddaf42eb6c86366658c99087ce367249a588",
		"twoloop_stereo_48000_128000": "78054da37459c27121b9797486abfe5a1c5e29d3c71245c6270dfb91e1ec851b",
		"fast_stereo_48000_128000":    "092c315015918776d81a6ce9f5e2c8c623ba06fb22933a71d1069a0cd40f3718",
		// Mono/48000 (SCE path at 48 kHz), pinned at the representative 128000 bitrate.
		"nmr_mono_48000_128000":     "643fa0d813b771f1d20a2d4510e9c1f4797ed5e59b54f5c19e90da42e559dc3c",
		"twoloop_mono_48000_128000": "6bc86506d74dfd3700e5f4674717bcd5c58055f48c77a04a4dc2ea86950cc3fe",
		"fast_mono_48000_128000":    "85d05c5af5a4c9626f0a52121aeae9d4511a9b02d329fc00334a1b05b417d5dc",
	}

	cases := archCases()
	if len(golden) != len(cases) {
		t.Fatalf("golden map has %d entries, corpus has %d cases; they must match 1:1",
			len(golden), len(cases))
	}
	for _, c := range cases {
		key := archKey(c)
		t.Run(key, func(t *testing.T) {
			want, ok := golden[key]
			if !ok {
				t.Fatalf("no golden pinned for %s", key)
			}
			stream := encodeADTSPlanar(t,
				enc.Config{SampleRate: c.rate, Bitrate: c.bitrate, Channels: c.channels, Coder: c.kind},
				archCastanetsInput(c.chLabel, c.rate))
			sum := sha256.Sum256(stream)
			got := hex.EncodeToString(sum[:])
			if got != want {
				t.Errorf("ADTS sha256 = %s, want %s (encoder output is not reproducible; "+
					"if this is an intentional encoder change, update the golden on both arm64 and amd64)",
					got, want)
			}
		})
	}
}
