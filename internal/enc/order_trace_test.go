// SPDX-License-Identifier: LGPL-2.1-or-later
package enc

import (
	"math"
	"strings"
	"testing"
)

// TestPipelineOrdering proves by trace that the Go pipeline invokes the
// tools in exactly the sequence the REAL C aac_encode_frame does (measured
// with a hooked coder vtable on the archive encoder; see corder.c).
func TestPipelineOrdering(t *testing.T) {
	mk := func(ch int) [][]float32 {
		out := make([][]float32, ch)
		for c := range out {
			out[c] = make([]float32, 1024)
			for i := range out[c] {
				env := float32(0.3) * float32(math.Exp(float64(-(i-128))/300.0))
				if i < 128 {
					env = 0.9
				}
				out[c][i] = env * float32(math.Sin(0.21*float64(i)+float64(c)))
			}
		}
		return out
	}
	cases := []struct {
		name string
		cfg  Config
		ch   int
		want string
	}{
		{"nmr mono", Config{SampleRate: 48000, Bitrate: 128000, Channels: 1}, 1,
			"mark_pns search_for_tns apply_tns_filt search_for_quantizers"},
		{"nmr stereo", Config{SampleRate: 48000, Bitrate: 128000, Channels: 2}, 2,
			"search_for_tns apply_tns_filt search_for_tns apply_tns_filt mark_pns mark_pns search_for_quantizers search_for_quantizers"},
		{"nmr mono tns off", Config{SampleRate: 48000, Bitrate: 128000, Channels: 1, DisableTNS: true}, 1,
			"search_for_quantizers"},
		{"twoloop mono", Config{SampleRate: 48000, Bitrate: 128000, Channels: 1, Coder: CoderTwoLoop}, 1,
			"mark_pns search_for_quantizers search_for_tns apply_tns_filt search_for_pns search_for_is search_for_ms"},
		{"twoloop stereo", Config{SampleRate: 48000, Bitrate: 128000, Channels: 2, Coder: CoderTwoLoop}, 2,
			"mark_pns search_for_quantizers mark_pns search_for_quantizers search_for_tns apply_tns_filt search_for_pns search_for_tns apply_tns_filt search_for_pns search_for_is search_for_ms"},
		{"fast stereo", Config{SampleRate: 48000, Bitrate: 128000, Channels: 2, Coder: CoderFast}, 2,
			"mark_pns search_for_quantizers mark_pns search_for_quantizers search_for_tns apply_tns_filt search_for_pns search_for_tns apply_tns_filt search_for_pns search_for_is search_for_ms"},
	}
	for _, tc := range cases {
		e, err := New(tc.cfg)
		if err != nil {
			t.Fatal(err)
		}
		var dst []byte
		src := mk(tc.ch)
		e.trace = make([]string, 0, 32)
		dst, err = e.EncodeFrame(dst, src) // priming
		if err != nil {
			t.Fatal(err)
		}
		e.trace = e.trace[:0]
		if _, err := e.EncodeFrame(dst, src); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(e.trace, " ")
		if got != tc.want {
			t.Errorf("%s:\n got  %s\n want %s", tc.name, got, tc.want)
		} else {
			t.Logf("%s: %s", tc.name, got)
		}
	}
}
