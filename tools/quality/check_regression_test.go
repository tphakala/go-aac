package main

import "testing"

// TestCheckRegression pins the pure gate decision (the logic behind the gate)
// across the directions and edge cases, table-driven so each row names the one
// change and the number of gate messages it must produce. The gate ratchets:
// it trips on a regression AND on a significant improvement (which demands a
// baseline refresh), but not on a change within tolerance in either direction.
func TestCheckRegression(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	// A healthy baseline case: lag at the encoder delay, finite metrics.
	newBase := func() baselineCase {
		return baselineCase{Lag: 1024, SNR: f(40), BandSNR: f(50), SegSNR: f(30), LSD: f(2), PreEcho: f(-30)}
	}
	cases := []struct {
		name    string
		mutBase func(*baselineCase)
		mutCur  func(*baselineCase)
		wantN   int
	}{
		{"identical", nil, nil, 0},
		{"snr within tolerance", nil, func(c *baselineCase) { c.SNR = f(39.6) }, 0},                   // -0.4 dB, inside 0.5
		{"snr small improvement within tolerance", nil, func(c *baselineCase) { c.SNR = f(40.4) }, 0}, // +0.4 dB, inside 0.5: no trip
		{"snr improved past tolerance trips", nil, func(c *baselineCase) { c.SNR = f(50) }, 1},        // +10 dB: ratchet demands a refresh
		{"snr regressed", nil, func(c *baselineCase) { c.SNR = f(39.0) }, 1},                          // -1.0 dB, past 0.5
		{"lsd within tolerance", nil, func(c *baselineCase) { c.LSD = f(2.4) }, 0},                    // +0.4 dB, inside 0.5
		{"lsd small improvement within tolerance", nil, func(c *baselineCase) { c.LSD = f(1.6) }, 0},  // -0.4 dB, inside 0.5: no trip
		{"lsd regressed", nil, func(c *baselineCase) { c.LSD = f(2.6) }, 1},                           // +0.6 dB, past 0.5
		{"lsd improved past tolerance trips", nil, func(c *baselineCase) { c.LSD = f(1) }, 1},         // -1.0 dB: ratchet demands a refresh
		// Exact regression boundaries: the check is strict (< / >), so a value
		// exactly at base-tol (higher-is-better) or base+tol (lower-is-better) is
		// NOT a regression; a hair past it is. These catch a < -> <= mutation,
		// distinct from the >tol rows above.
		{"snr exactly at lower boundary not a regression", nil, func(c *baselineCase) { c.SNR = f(39.5) }, 0}, // == 40-0.5 (higher is better)
		{"snr just past lower boundary regresses", nil, func(c *baselineCase) { c.SNR = f(39.49) }, 1},        // past 40-0.5
		{"lsd exactly at upper boundary not a regression", nil, func(c *baselineCase) { c.LSD = f(2.5) }, 0},  // == 2+0.5 (lower is better)
		{"lag changed", nil, func(c *baselineCase) { c.Lag = 1025 }, 1},                                       // zero tolerance
		{"snr turned undefined", nil, func(c *baselineCase) { c.SNR = nil }, 1},                               // finite baseline, nil now
		{"baseline lsd null skipped", func(c *baselineCase) { c.LSD = nil }, func(c *baselineCase) { c.LSD = f(99) }, 0},
		{"baseline snr null is corrupt", func(c *baselineCase) { c.SNR = nil }, nil, 1},
		{"two regressions", nil, func(c *baselineCase) { c.SNR = f(38); c.LSD = f(3) }, 2},
	}
	for _, c := range cases {
		base := newBase()
		cur := newBase()
		if c.mutBase != nil {
			c.mutBase(&base)
		}
		if c.mutCur != nil {
			c.mutCur(&cur)
		}
		if got := regressions(&base, &cur); len(got) != c.wantN {
			t.Errorf("%s: got %d gate messages %v, want %d", c.name, len(got), got, c.wantN)
		}
	}
}
