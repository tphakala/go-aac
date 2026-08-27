// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"errors"
	"testing"

	"github.com/tphakala/go-aac/internal/bits"
)

// buildASC packs a sequence of (width, value) bit fields into an
// AudioSpecificConfig byte slice, so the HE-AAC test vectors read as the
// bitstream fields they represent instead of opaque hex.
func buildASC(fields ...[2]int) []byte {
	w := bits.NewWriter(make([]byte, 32))
	for _, f := range fields {
		w.Put(f[0], uint32(f[1])) //nolint:gosec // small non-negative test constants
	}
	return w.Flush()
}

// assertSentinel checks err matches want and every sentinel in alsoMatch, and
// matches none in notMatch. It encodes the SBR/PS hierarchy: PS wraps SBR wraps
// ErrUnsupported, so a PS error also matches SBR, but an SBR error does not
// match PS.
func assertSentinel(t *testing.T, err, want error, alsoMatch, notMatch []error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want errors.Is %v", err, want)
	}
	for _, e := range alsoMatch {
		if !errors.Is(err, e) {
			t.Errorf("error %v should also match %v", err, e)
		}
	}
	for _, e := range notMatch {
		if errors.Is(err, e) {
			t.Errorf("error %v wrongly matches %v", err, e)
		}
	}
}

// TestParseASCUnsupportedSBR_PS pins the precise HE-AAC / HE-AACv2 rejection
// across BOTH raise sites in ParseASC: the explicit SBR/PS object type (AOT
// 5/29) and the implicit SBR/PS sync extension. The hierarchy is verified too:
// PS wraps SBR (so a caller testing ErrUnsupportedSBR catches the whole HE-AAC
// family), while SBR does not wrap PS, and both wrap the base ErrUnsupported.
func TestParseASCUnsupportedSBR_PS(t *testing.T) {
	// Sync-extension vectors: AAC-LC core (AOT 2, 44.1 kHz) + GASpecificConfig
	// (3 zero bits) + the 0x2b7 SBR sync extension declaring ext AOT 5, SBR
	// present, and a 48 kHz extension rate (!= core, so SBR is not reset).
	syncSBRStereo := buildASC(
		[2]int{5, 2}, [2]int{4, 4}, [2]int{4, 2}, // AOT=LC, sri=44100, chan=2
		[2]int{1, 0}, [2]int{1, 0}, [2]int{1, 0}, // frameLen, dependsOnCore, extFlag
		[2]int{11, 0x2b7}, [2]int{5, 5}, [2]int{1, 1}, [2]int{4, 3}, // SBR ext, AOT 5, present, sri=48000
	)
	// Mono with the same SBR extension: decode_ga_specific_config upgrades a
	// single-channel SBR stream to PS, so this reports ErrUnsupportedPS.
	syncPSMono := buildASC(
		[2]int{5, 2}, [2]int{4, 4}, [2]int{4, 1}, // AOT=LC, sri=44100, chan=1
		[2]int{1, 0}, [2]int{1, 0}, [2]int{1, 0},
		[2]int{11, 0x2b7}, [2]int{5, 5}, [2]int{1, 1}, [2]int{4, 3},
	)

	cases := []struct {
		name                string
		asc                 []byte
		want                error
		alsoMatch, notMatch []error
	}{
		{"explicit SBR (AOT 5)", []byte{0x2b, 0x92, 0x08, 0x00},
			ErrUnsupportedSBR, []error{ErrUnsupported}, []error{ErrUnsupportedPS}},
		{"explicit PS (AOT 29)", []byte{0xea, 0x12, 0x08},
			ErrUnsupportedPS, []error{ErrUnsupportedSBR, ErrUnsupported}, nil},
		{"sync-extension SBR (stereo)", syncSBRStereo,
			ErrUnsupportedSBR, []error{ErrUnsupported}, []error{ErrUnsupportedPS}},
		{"sync-extension PS (mono upgrade)", syncPSMono,
			ErrUnsupportedPS, []error{ErrUnsupportedSBR, ErrUnsupported}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseASC(tc.asc)
			assertSentinel(t, err, tc.want, tc.alsoMatch, tc.notMatch)
		})
	}
}

// TestSkipFillElementSBR covers the third raise site: a fill element whose
// first extension payload is EXT_SBR_DATA (13) or EXT_SBR_DATA_CRC (14) means
// the ADTS stream is HE-AAC, and skipFillElement must reject it as
// ErrUnsupportedSBR rather than skip it and decode garbage.
func TestSkipFillElementSBR(t *testing.T) {
	cases := []struct {
		name string
		b    byte // top nibble is the extension payload type
	}{
		{"EXT_SBR_DATA", 0xd0},     // 1101 ....
		{"EXT_SBR_DATA_CRC", 0xe0}, // 1110 ....
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := bits.NewReader([]byte{tc.b, 0x00})
			err := skipFillElement(r, 1)
			assertSentinel(t, err, ErrUnsupportedSBR, []error{ErrUnsupported}, nil)
		})
	}
}
