// SPDX-License-Identifier: LGPL-2.1-or-later

package psy

import (
	"reflect"
	"testing"

	"github.com/tphakala/go-aac/internal/tables"
)

// resetCfg is one psy model configuration. srIdx must be the MPEG-4 sample-rate
// table index for rate (3 = 48000, 4 = 44100); the two are coupled because
// bands() indexes the tables by srIdx while the model math uses rate.
type resetCfg struct {
	rate, srIdx, bitrate, channels, cutoff int
}

func (c resetCfg) bands() (bands [2][]uint8, numBands [2]int) {
	bands = [2][]uint8{tables.SwbSize1024[c.srIdx], tables.SwbSize128[c.srIdx]}
	numBands = [2]int{int(tables.NumSwb1024[c.srIdx]), int(tables.NumSwb128[c.srIdx])}
	return bands, numBands
}

func newResetCfg(c resetCfg) *Context {
	bands, numBands := c.bands()
	return New(c.rate, c.bitrate, c.channels, c.cutoff, bands, numBands)
}

// TestResetEqualsNew is the byte-exactness gate for the pooled reset path
// (issue #41): a Context reset to a target config must be indistinguishable
// from one freshly built by New for that config. It checks two things:
//   - resetting from a DIFFERENT prior config, covering both the slice
//     reslice+clear branch (shrink) and the reallocate branch (grow), and
//   - resetting a SAME-config context whose reused per-channel slices were
//     dirtied with post-Analyze-like state, which is what actually gates the
//     clear() calls: drop either clear() and the stale values survive the reset
//     and break the comparison.
func TestResetEqualsNew(t *testing.T) {
	targets := []resetCfg{
		{48000, 3, 128000, 2, 16000},
		{48000, 3, 96000, 1, 15000},
		{44100, 4, 128000, 2, 15000},
		{44100, 4, 64000, 1, 14000},
	}
	for i, tc := range targets {
		want := newResetCfg(tc)
		bands, numBands := tc.bands()

		// Arm 1: reset from a deliberately different prior config so both the
		// mono<->stereo grow (reallocate) and shrink (reslice+clear) branches run.
		prior := targets[(i+1)%len(targets)]
		fromPrior := newResetCfg(prior)
		fromPrior.Reset(tc.rate, tc.bitrate, tc.channels, tc.cutoff, bands, numBands)
		if !reflect.DeepEqual(want, fromPrior) {
			t.Errorf("target %+v reset from %+v: not byte-identical to fresh New", tc, prior)
		}

		// Arm 2: reset a same-config context whose reused slices carry stale
		// per-channel state, the same-shape path a pooled encoder actually takes.
		// This is the gate for clear(): without it the dirtied fields survive.
		dirtied := newResetCfg(tc)
		dirtyPsyState(dirtied)
		dirtied.Reset(tc.rate, tc.bitrate, tc.channels, tc.cutoff, bands, numBands)
		if !reflect.DeepEqual(want, dirtied) {
			t.Errorf("target %+v reset after dirtying reused slices: stale state survived (clear() gap?)", tc)
		}
	}
}

// dirtyPsyState scribbles non-zero sentinels into exactly the per-channel fields
// that Reset relies on clear() to scrub and does not itself overwrite: winEnergy
// and nextWindowSeq on the private channel, Entropy and PsyBands on FFChannel. A
// fresh New leaves all of these zero, so if either clear() in Reset were removed,
// TestResetEqualsNew's Arm 2 would see the sentinels survive and fail.
func dirtyPsyState(ctx *Context) {
	for i := range ctx.Ch {
		ctx.Ch[i].Entropy = 7
		ctx.Ch[i].PsyBands[0].Energy = 7
		ctx.Ch[i].PsyBands[127].Threshold = 7
	}
	for i := range ctx.pch {
		ctx.pch[i].winEnergy = 7
		ctx.pch[i].nextWindowSeq = 3
	}
}

// TestResetNoAllocSameChannels pins the allocation win: resetting to the same
// channel count reuses the retained Context and the two per-channel slices, so
// it must not allocate. This is what lets pcm.EncodeInterleaved reach 0
// allocs/op on same-shape reuse.
func TestResetNoAllocSameChannels(t *testing.T) {
	cfg := resetCfg{48000, 3, 128000, 2, 16000}
	ctx := newResetCfg(cfg)
	bands, numBands := cfg.bands()
	allocs := testing.AllocsPerRun(50, func() {
		ctx.Reset(cfg.rate, cfg.bitrate, cfg.channels, cfg.cutoff, bands, numBands)
	})
	if allocs != 0 {
		t.Errorf("Reset allocates %.2f/op, want 0", allocs)
	}
}
