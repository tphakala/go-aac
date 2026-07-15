// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import "testing"

type bandVisit struct{ w, g, idx int }

// TestEachBandLong pins the long-frame traversal: one window group, slots
// 0..nBands-1.
func TestEachBandLong(t *testing.T) {
	ics := &IndividualChannelStream{NumWindows: 1}
	ics.GroupLen[0] = 1
	var got []bandVisit
	ics.EachBand(3, func(w, g, idx int) { got = append(got, bandVisit{w, g, idx}) })
	want := []bandVisit{{0, 0, 0}, {0, 1, 1}, {0, 2, 2}}
	assertVisits(t, got, want)
}

// TestEachBandGrouped pins the short-frame convention that breaks silently
// if the slot stride is num_swb instead of 16 (docs/architecture.md pitfall
// 1): 8 windows in groups of 2+1+5, slots w*16+g with w the group's first
// window.
func TestEachBandGrouped(t *testing.T) {
	ics := &IndividualChannelStream{NumWindows: 8}
	ics.GroupLen[0] = 2
	ics.GroupLen[2] = 1
	ics.GroupLen[3] = 5
	var got []bandVisit
	ics.EachBand(2, func(w, g, idx int) { got = append(got, bandVisit{w, g, idx}) })
	want := []bandVisit{
		{0, 0, 0}, {0, 1, 1},
		{2, 0, 32}, {2, 1, 33},
		{3, 0, 48}, {3, 1, 49},
	}
	assertVisits(t, got, want)
}

func assertVisits(t *testing.T, got, want []bandVisit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("visited %d bands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("visit %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
