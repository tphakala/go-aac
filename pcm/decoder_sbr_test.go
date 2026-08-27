// SPDX-License-Identifier: LGPL-2.1-or-later

package pcm

import (
	"bytes"
	"errors"
	"testing"
)

// TestDecodeRawUnsupportedSBRPS proves the public decoder surfaces the precise
// HE-AAC / HE-AACv2 rejection through NewDecoder + WithRawStream. It also pins
// the sentinel hierarchy that lets a caller ingesting camera/RTSP audio detect
// the whole HE-AAC family with one check: ErrUnsupportedPS wraps
// ErrUnsupportedSBR (PS implies SBR), so errors.Is(err, ErrUnsupportedSBR)
// catches both an SBR and an SBR+PS stream, while ErrUnsupportedPS still
// distinguishes HE-AACv2; both satisfy the base ErrUnsupported.
func TestDecodeRawUnsupportedSBRPS(t *testing.T) {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDecoder(bytes.NewReader(nil), WithRawStream(tc.asc))
			if err == nil {
				t.Fatal("NewDecoder accepted an HE-AAC config, want an unsupported error")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is %v", err, tc.want)
			}
			for _, e := range tc.alsoMatch {
				if !errors.Is(err, e) {
					t.Errorf("error %v should also match %v", err, e)
				}
			}
			for _, e := range tc.notMatch {
				if errors.Is(err, e) {
					t.Errorf("error %v wrongly matches %v", err, e)
				}
			}
		})
	}
}
