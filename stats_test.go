// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import "testing"

// TestStatsString pins the report format against hand-computed percentages,
// including the two places it can go wrong: the divide guard when a
// denominator is zero, and the ChannelFrames-ShortFrames denominator of the
// long-block TNS rate, which reaches zero when every channel-frame is short.
// The populated case's six percentages are pairwise distinct (20, 50, 25, 30,
// 10, 3), so a transposed argument or a swapped label inside String cannot
// render the same string.
func TestStatsString(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   Stats
		want string
	}{
		{
			name: "populated",
			st: Stats{
				Frames: 10, ChannelFrames: 20, ShortFrames: 4,
				TNSLongFrames: 8, TNSShortFrames: 1,
				Bands: 1000, PNSBands: 30,
				PairBands: 400, MSBands: 120, ISBands: 40,
				MeanLambda: 118.5,
			},
			want: "Qavg: 118.500  Tr: 20.0%  TNS(L): 50.0%  TNS(S): 25.0%  M/S: 30.0%  I/S: 10.0%  PNS: 3.0%",
		},
		{
			name: "zero value",
			st:   Stats{},
			want: "Qavg: 0.000  Tr: 0.0%  TNS(L): 0.0%  TNS(S): 0.0%  M/S: 0.0%  I/S: 0.0%  PNS: 0.0%",
		},
		{
			name: "all short",
			st:   Stats{ChannelFrames: 5, ShortFrames: 5, TNSShortFrames: 5},
			want: "Qavg: 0.000  Tr: 100.0%  TNS(L): 0.0%  TNS(S): 100.0%  M/S: 0.0%  I/S: 0.0%  PNS: 0.0%",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.String(); got != tc.want {
				t.Errorf("Stats.String() = %q\nwant %q", got, tc.want)
			}
		})
	}
}
