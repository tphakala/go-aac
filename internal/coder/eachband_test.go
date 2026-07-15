// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"strings"
	"testing"
)

// EachBand advances by GroupLen[w], exactly as the C does
// (w += ics->group_len[w]). Go zero-values arrays, so an ICS that was never
// initialized leaves GroupLen[w] == 0 and the loop would never advance -- a hang.
// It must fail fast instead. This is a programming error inside the package, not
// anything a caller can provoke, so a panic is the right shape (an out-of-range
// index behaves the same way); what matters is that it cannot wedge a goroutine.
func TestEachBandRejectsZeroGroupLen(t *testing.T) {
	var ics IndividualChannelStream
	ics.NumWindows = 8 // GroupLen left at its zero value

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("EachBand did not panic on GroupLen == 0; it would have looped forever")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "GroupLen") {
			t.Fatalf("panic did not name the invariant: %v", r)
		}
	}()

	ics.EachBand(16, func(_, _, _ int) {})
}

// A valid ICS must iterate exactly as before: the guard may not change the
// traversal of any well-formed stream.
func TestEachBandValidTraversal(t *testing.T) {
	var ics IndividualChannelStream

	// Long block: one window, one group.
	ics.NumWindows = 1
	ics.GroupLen[0] = 1
	var got []int
	ics.EachBand(3, func(w, g, idx int) { got = append(got, w, g, idx) })
	want := []int{0, 0, 0, 0, 1, 1, 0, 2, 2}
	if len(got) != len(want) {
		t.Fatalf("long block: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("long block: got %v, want %v", got, want)
		}
	}

	// Eight short windows in two groups of 4: w visits 0 and 4 only, and the
	// band index is w*16+g, not w*nBands+g.
	ics = IndividualChannelStream{}
	ics.NumWindows = 8
	ics.GroupLen[0] = 4
	ics.GroupLen[4] = 4
	var ws []int
	ics.EachBand(2, func(w, g, idx int) {
		if g == 0 {
			ws = append(ws, w)
		}
		if idx != w*16+g {
			t.Fatalf("idx = %d, want w*16+g = %d", idx, w*16+g)
		}
	})
	if len(ws) != 2 || ws[0] != 0 || ws[1] != 4 {
		t.Fatalf("group starts = %v, want [0 4]", ws)
	}
}
