// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"fmt"
	"testing"
)

// bandwidthCase is one row of the C parity grid: the coding bandwidth in Hz
// the pinned encoder fixes at init for a per-channel bitrate, with the PNS
// and intensity stereo tools off and on.
type bandwidthCase struct {
	brPerCh int
	wantOff int // DisablePNS and DisableIS both set
	wantOn  int // tool defaults, both enabled
}

// TestBandwidthVsC pins the coding bandwidth fixed at init
// (aacenc.c:1590-1616 @ d09d5afc3a) against the C encoder for every coder,
// both branches of the 15% widening, and both sides of the NMR rate-table
// cut at 32 kb/s per channel.
//
// Every want below was read out of AACEncContext->bandwidth of the real
// FFmpeg encoder through tools/ccutoff, so the numbers are the C encoder's
// own rather than a transcription of the branch.
//
// The widening at aacenc.c:1609-1610 keys on the pns and intensity_stereo
// option flags, never on the coder, which is what this test exists to hold
// (issue #49): keying it on the coder instead cost CoderTwoLoop and
// CoderFast close to a fifth of their bandwidth at the worst bitrates (the
// 32000 row below: 9500 Hz where the C gives 11750), and gave CoderNMR a
// widening the C skips. It applies to mono as well, because aac_is is an
// option flag that is not cleared for a single-channel stream.
//
// The bitrates are per channel; the wants hold for 44100 and 48000 alike,
// as the sample_rate/2 clamp never binds (22050 is already above the 22000
// ceiling) and every other term is a function of the per-channel rate only.
func TestBandwidthVsC(t *testing.T) {
	// The NMR rate-to-bandwidth table takes over at 32 kb/s per channel and
	// ignores the tools; below the cut NMR shares the cutoff formula with
	// the other coders, so 30000 is the row that exercises its widening.
	nmr := []bandwidthCase{
		{16000, 8000, 8000}, // both masked by the 8000 Hz floor
		{30000, 8562, 10671},
		{32000, 14000, 14000}, // table branch from here up: tools do not matter
		{48000, 15000, 15000},
		{64000, 16000, 16000},
		{96000, 18000, 18000},
		{128000, 18666, 18666},
		{192000, 20000, 20000},
		{256000, 21333, 21333},
	}
	nonNMR := []bandwidthCase{
		{16000, 8000, 8000}, // both masked by the 8000 Hz floor
		{30000, 8562, 10671},
		{32000, 9500, 11750},
		{48000, 15000, 15450},
		{64000, 16000, 16600},
		{96000, 18000, 18900},
		{128000, 20000, 21200},
		{192000, 22000, 22000}, // both clamped to the 22000 Hz ceiling
		{256000, 22000, 22000},
	}
	coders := []struct {
		name  string
		kind  CoderKind
		cases []bandwidthCase
	}{
		{"nmr", CoderNMR, nmr},
		{"twoloop", CoderTwoLoop, nonNMR},
		{"fast", CoderFast, nonNMR},
	}
	for _, c := range coders {
		for _, rate := range []int{44100, 48000} {
			for _, ch := range []int{1, 2} {
				for _, tc := range c.cases {
					for _, tools := range []bool{false, true} {
						want := tc.wantOff
						if tools {
							want = tc.wantOn
						}
						name := fmt.Sprintf("%s/%d/%dch/%dk/tools=%t",
							c.name, rate, ch, tc.brPerCh/1000, tools)
						t.Run(name, func(t *testing.T) {
							e, err := New(Config{
								SampleRate: rate,
								Channels:   ch,
								Bitrate:    tc.brPerCh * ch,
								Coder:      c.kind,
								DisablePNS: !tools,
								DisableIS:  !tools,
							})
							if err != nil {
								t.Fatal(err)
							}
							if got := e.Bandwidth(); got != want {
								t.Errorf("bandwidth %d Hz, C gives %d", got, want)
							}
						})
					}
				}
			}
		}
	}
}

// TestBandwidthWideningIsDisjunction pins the shape of the condition, which
// a test toggling both tools together cannot see: the C widens when EITHER
// pns or intensity_stereo is set (aacenc.c:1609), so only disabling the two
// together narrows the bandwidth. Wants read from the C encoder through
// tools/ccutoff at 48 kHz mono.
func TestBandwidthWideningIsDisjunction(t *testing.T) {
	combos := []struct {
		disablePNS bool
		disableIS  bool
		widened    bool
	}{
		{false, false, true},
		{true, false, true},
		{false, true, true},
		{true, true, false},
	}
	cases := []struct {
		name       string
		coder      CoderKind
		brPerCh    int
		wantNarrow int
		wantWide   int
	}{
		// Non-NMR coders take the cutoff formula at every bitrate.
		{"twoloop 64k", CoderTwoLoop, 64000, 16000, 16600},
		// NMR reaches the formula only below 32 kb/s per channel.
		{"nmr 30k", CoderNMR, 30000, 8562, 10671},
	}
	for _, tc := range cases {
		for _, cb := range combos {
			want := tc.wantNarrow
			if cb.widened {
				want = tc.wantWide
			}
			name := fmt.Sprintf("%s/pns=%t/is=%t", tc.name, !cb.disablePNS, !cb.disableIS)
			t.Run(name, func(t *testing.T) {
				e, err := New(Config{
					SampleRate: 48000,
					Channels:   1,
					Bitrate:    tc.brPerCh,
					Coder:      tc.coder,
					DisablePNS: cb.disablePNS,
					DisableIS:  cb.disableIS,
				})
				if err != nil {
					t.Fatal(err)
				}
				if got := e.Bandwidth(); got != want {
					t.Errorf("bandwidth %d Hz, C gives %d", got, want)
				}
			})
		}
	}
}

// TestBandwidthNMRCeiling pins the 22000 Hz ceiling of the NMR table branch
// (aacenc.c:1605-1607), which the grid above never reaches. The table keeps
// interpolating above 192 kb/s per channel, so the ceiling only binds at the
// bitrate cap: 48 kHz mono is limited to 6144 bits per 1024-sample frame,
// which is 288000 bits/s, and the C encoder returns exactly 22000 there.
func TestBandwidthNMRCeiling(t *testing.T) {
	e, err := New(Config{SampleRate: 48000, Channels: 1, Bitrate: 288000})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Bandwidth(); got != 22000 {
		t.Errorf("bandwidth %d Hz, C gives 22000", got)
	}
}

// TestBandwidthCutoffOverridesTools pins aacenc.c:1591-1592: a user cutoff
// is taken verbatim, so the tool switches cannot move it.
func TestBandwidthCutoffOverridesTools(t *testing.T) {
	for _, tools := range []bool{false, true} {
		e, err := New(Config{
			SampleRate: 48000,
			Channels:   1,
			Bitrate:    64000,
			Coder:      CoderTwoLoop,
			Cutoff:     13000,
			DisablePNS: !tools,
			DisableIS:  !tools,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := e.Bandwidth(); got != 13000 {
			t.Errorf("tools=%t: bandwidth %d Hz, want the verbatim cutoff 13000",
				tools, got)
		}
	}
}
