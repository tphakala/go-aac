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
	channels int
	rate     int
	bitrate  int
}

// chLabel derives the corpus channel-layout label from the channel count, so a
// cell cannot carry a label that disagrees with its channels. Mirrors
// toolWiringCell.chLabel (tool_wiring_test.go) and edgeCase.chLabel.
func (c archCase) chLabel() string { return chLabelFor(c.channels) }

// testCoders is THE coder axis for this package: the determinism gate and its
// repro diagnostic, the edge-config soak, the mid/side gate and the tool-wiring
// gate all sweep it. Listing it once is the whole point. It used to be restated
// per test, which meant a fourth coder could be added to one sweep and silently
// missed by another.
//
// It carries the label explicitly because Coder is a bare int with no String
// method, and it carries the PUBLIC Coder rather than the internal
// enc.CoderKind because Coder.kind() maps public onto internal and no reverse
// mapping exists; archCoderKind projects it for the corpus cells that need the
// internal type.
var testCoders = []struct {
	name  string
	coder Coder
}{
	{coderNMR, CoderNMR},
	{coderTwoLoop, CoderTwoLoop},
	{coderFast, CoderFast},
}

// archCoderKind projects a testCoders entry onto the internal kind the
// determinism corpus stores. A coder this package lists but Coder.kind()
// rejects is a bug in this file rather than a caller error, and it must not be
// swallowed: enc.CoderKind's zero value is enc.CoderNMR, so taking it silently
// would build three identical NMR cells that still pass the gate. archCases has
// no *testing.T in scope, so this panics, which fails the test binary just as
// loudly and points at the cause.
func archCoderKind(c Coder) enc.CoderKind {
	kind, ok := c.kind()
	if !ok {
		panic(fmt.Sprintf("testCoders holds coder %d, which Coder.kind() rejects", c))
	}
	return kind
}

// archCases enumerates the determinism corpus: the stereo/44100 bitrate sweep,
// plus mono/44100, stereo/48000 and mono/48000 pinned at a single
// representative bitrate (128000, the mid value of the swept set; note this is
// not DefaultBitrate, which is 200000). The mono and 48 kHz cells gate the SCE
// path and the 48 kHz tables that the original stereo/44100 goldens never
// exercised.
//
// The bitrate axis is swept only on stereo/44100, and it is swept at the
// BOUNDARIES rather than uniformly. Sweeping every axis against every bitrate
// would multiply the gate's cost for no coverage: the divergence this gate
// guards is float32 FMA contraction, which is bitrate-independent, so extra
// mid-range points re-run the same arithmetic. What the mid-range points do NOT
// reach is the two bitrate-dependent branches:
//
//   - 32000 is 16 kb/s per channel on a stereo stream, under the 32 kb/s
//     per-channel threshold at which CoderNMR switches to its rate-to-bandwidth
//     table (internal/enc/encoder.go, aacenc.c:1598-1607). Every in-range cell
//     is at or above that threshold, so this is the only stereo cell where NMR
//     takes the aacCutoffFromBitrate formula branch instead. For twoloop and
//     fast, which always take the formula branch, it is simply the lowest rate
//     point in the corpus. internal/enc/bandwidth_test.go already unit-pins that
//     branch; what this cell adds is end-to-end golden coverage of it.
//   - 1000000 is above the AAC buffer model ceiling for this config, so Reset
//     clamps it to 6144*channels/1024*rate (mirroring aacenc.c:1560-1566) before
//     anything downstream sees it. That leaves 264600 bps per channel, which is
//     past the top row of the NMR rate-to-bandwidth table (192000), so bwI pins
//     at 3 and the interpolation EXTRAPOLATES above the table rather than
//     interpolating within it, giving 21512 Hz. Note the trailing
//     min(bw, 22000, SampleRate/2) does not bind here; extrapolation past the
//     top row is what makes the cell distinct. Rate control then runs against
//     the ceiling. No in-range target reaches either branch.
//
// Both branches are arch-sensitive in the same way as the rest of the encoder,
// so they are worth pinning; the points between them are not. These three cells
// are also, empirically, the gate for the Reset clamp itself: deleting the clamp
// leaves the edge-config soak green and turns exactly these three red.
func archCases() []archCase {
	cases := make([]archCase, 0, len(testCoders)*8)
	for _, c := range testCoders {
		kind := archCoderKind(c.coder)
		for _, br := range []int{32000, 96000, 128000, 192000, 1000000} {
			cases = append(cases, archCase{c.name, kind, 2, 44100, br})
		}
		cases = append(cases,
			archCase{c.name, kind, 1, 44100, 128000},
			archCase{c.name, kind, 2, 48000, 128000},
			archCase{c.name, kind, 1, 48000, 128000},
		)
	}
	return cases
}

// archKey is the golden-map key and subtest name for a case:
// "<coder>_<mono|stereo>_<rate>_<bitrate>".
func archKey(c archCase) string {
	return fmt.Sprintf("%s_%s_%d_%d", c.coder, c.chLabel(), c.rate, c.bitrate)
}

// Channel seeds and click offsets for the castanets corpus. They live here, in
// one place, because the arch-determinism goldens are pinned against the exact
// samples they produce: a second copy elsewhere could drift and silently
// decouple another test from the gated corpus.
const (
	castanetsLeftSeed   = 0x0badcafe
	castanetsRightSeed  = 0x5eed1234
	castanetsLeftClick  = 0
	castanetsRightClick = 137
)

// castanetsChannels renders n samples per channel of the castanets corpus for a
// channel layout; mono is the left channel alone. It is the single generator
// for that corpus, shared by the arch-determinism gate and the edge-config
// soak, so both are guaranteed to drive the encoder with the same material
// rather than promising to in prose.
func castanetsChannels(chLabel string, rate, n int) [][]float32 {
	left := synthCastanets(n, rate, castanetsLeftSeed, castanetsLeftClick)
	if chLabel == archChanMono {
		return [][]float32{left}
	}
	return [][]float32{left, synthCastanets(n, rate, castanetsRightSeed, castanetsRightClick)}
}

// archCastanetsInput builds the six-second castanets corpus input the
// determinism goldens are pinned against. The left channel is byte-identical to
// the channel the stereo cases use, so mono reuses already arch-verified input,
// and the 48000 Hz renders were independently verified byte-identical on amd64
// and arm64 (raw float32 input hashes) before their goldens were pinned.
func archCastanetsInput(chLabel string, rate int) [][]float32 {
	return castanetsChannels(chLabel, rate, rate*6)
}

// TestEncoderArchDeterminism is the end-to-end guard for issue #59: encoding a
// fixed input must yield a byte-identical AAC stream on every architecture. It
// pins the ADTS SHA-256 of the synthetic castanets corpus across the full
// shipped config surface: all three coders and five bitrates on stereo/44100
// (spanning the low-rate bandwidth branch and the clamped-bitrate branch as
// well as the normal range), plus mono (SCE) at 44100, and both stereo and mono
// at 48000, so the SCE path and the 48 kHz tables are gated rather than only the
// stereo/44100 cases. CI runs the test job on ubuntu-latest and windows-latest
// (amd64) and macos-latest (arm64), all against these goldens, so a
// GOARCH-dependent divergence trips it on an arch.
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
		// Stereo/44100 low-rate boundary: 16 kb/s per channel takes the
		// non-table coding-bandwidth branch.
		"nmr_stereo_44100_32000":     "b452ab4ad996cf77b07d5045919d343566caae3581d15f664aeb1743659a17c9",
		"twoloop_stereo_44100_32000": "51cd2fcf0bf7a644acee5d5e75ed6e1e617f228994b789760b51dcd12352ff45",
		"fast_stereo_44100_32000":    "34fb2c5127ce4bc77373e73025a932e2facaf325657ef03e7e56294f2e824c14",
		// Stereo/44100 in-range sweep (values unchanged, re-keyed).
		"nmr_stereo_44100_96000":      "5119dc32e132837e99933790400ef1010d8f0c4b92fe65e142693782373679ee",
		"nmr_stereo_44100_128000":     "8e312290271b849aed93cbdfcaeb2fb55da58ce040cf6ec86b5cade61e573df6",
		"nmr_stereo_44100_192000":     "484a283cd0cc935221fcf722c653da470697feea125fa50697a9511890229f1a",
		"twoloop_stereo_44100_96000":  "12ea9b39e325444dc00b3baef65d74639d141e6798fa4a33d4f717e643a6d8d4",
		"twoloop_stereo_44100_128000": "dd1bada1f176492e4692cae711875929e036b199a3390397b238aaf873217e33",
		"twoloop_stereo_44100_192000": "6cd66c224e237c7f5407836bb8ae5cbe95507e92c421aa78d1b2d3d0b9bf4793",
		"fast_stereo_44100_96000":     "dfb56e44120d37b1685e613409915250ad1a8b8492acf48d2653cb22d1c7fc7c",
		"fast_stereo_44100_128000":    "c60e22b24d6fd6074f3a02fdfb9f69f358ac6ab2d3dfc67365da49652babe1fc",
		"fast_stereo_44100_192000":    "754c504f0a3106412de0c93c22f6be61f1686489467c1956f72ce5652587b20e",
		// Stereo/44100 clamped boundary: 1000000 is above the buffer model
		// ceiling, so Reset clamps it and rate control runs at the ceiling.
		"nmr_stereo_44100_1000000":     "e79f50ca0a3adabfa4d10822e86462c090b753061e99027a9572a6a01f3cb8b5",
		"twoloop_stereo_44100_1000000": "5c91b4b5ebc1b5f1c38a376ab0d72f0ff28769b2335817d65448e04e49670157",
		"fast_stereo_44100_1000000":    "40d3f13f1cbb3a51b5b320894251684348164ce60ec0301ee83c5e1a4f007b3d",
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
				archCastanetsInput(c.chLabel(), c.rate))
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
