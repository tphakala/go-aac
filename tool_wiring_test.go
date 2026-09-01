// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"bytes"
	"fmt"
	"slices"
	"testing"
)

// Oracle-free wiring gates for the encoder tool switches, the companion to
// TestEncoderMidSideWiring (edge_config_soak_test.go) which covers DisableMS.
// Before this, DisableTNS, DisablePNS and DisableIS were reachable only through
// TestPhase4TNSAB and TestPhase4FATEAnalogues, both of which call ffmpegBin(t)
// and skip unless GOAAC_FFMPEG points at the pinned oracle build. In a normal
// run nothing verified that those three flags reached anything at all: deleting
// every one of them from EncoderConfig.internal() left the suite green.
//
// The assertion vehicle is the public Stats API rather than byte inequality.
// Stats counters are accumulated per frame from the FINAL per-band decisions
// (internal/enc/encoder.go accumulateStats), so TNSLongFrames, PNSBands and
// ISBands name WHICH tool fired, where "the bytes changed" only says that
// something did. Bytes are asserted too, but as a corroborating check.
//
// Single-flag flips do not move the coding bandwidth, so any difference they
// produce is attributable to the tool. The 1.15 widening
// (internal/enc/encoder.go, mirroring aacenc.c:1609-1610) keys on
// !DisablePNS || !DisableIS, so it narrows only when BOTH are set, and these
// cells never set two at once.

// toolWiringRate is the sample rate for the sweep. 44100 only: the tools being
// gated here are rate-independent in their wiring (the switches are read in the
// same places for both shipped rates), and the 48 kHz path is already swept by
// TestEncoderArchDeterminism and TestEncoderEdgeConfigSoak. A bitrate sweep at
// both rates confirmed the activation thresholds cited below are the same at
// 48 kHz.
const toolWiringRate = 44100

// toolWiringSeconds is the input length, and it deliberately does NOT use
// edgeSoakSeconds the way the three edge-config tests do. Those shrink to one
// second under -race; this must not, because TNS activation is driven by
// transient density rather than by frame count, and the castanets corpus
// carries only about two transients per second. Measured on the TNS cell below:
// at two seconds the per-coder totals are comfortable (nmr 5, twoloop 7, fast
// 7 active channel-frames), but at one second they fall to 1, 3 and 3, so NMR's
// non-vacuity check would rest on a SINGLE frame. A precondition hanging by one
// frame is a spurious -race failure waiting for the next psychoacoustic tweak,
// so the length is fixed here instead.
const toolWiringSeconds = 2

// toolWiringFrames is toolWiringSeconds in whole frames, the unit
// edgeInputCache is keyed on.
const toolWiringFrames = toolWiringRate * toolWiringSeconds / FrameSize

// toolWiringCell is one (channel layout, bitrate) point of a gate's sweep. The
// layout is carried as the channel count alone; the corpus label is derived
// from it, so the two cannot disagree.
type toolWiringCell struct {
	channels int
	bitrate  int
}

func (c toolWiringCell) chLabel() string { return chLabelFor(c.channels) }

func (c toolWiringCell) name() string {
	return fmt.Sprintf("%s_%d", c.chLabel(), c.bitrate)
}

// Cells are chosen per tool rather than shared, because the tools do not all
// activate in the same region and a cell where the counter is legitimately zero
// with the switch CLEAR proves nothing about wiring. The activation thresholds
// below were measured by sweeping bitrate at both shipped sample rates, not
// assumed.
var (
	// TNS on stereo is dead below 64 kb/s for twoloop and fast (measured: 0
	// active frames at 24, 32 and 48 kb/s; 6 to 7 from 64 kb/s up). One cell is
	// enough: DisableTNS is read once into useTNS and gates both arms, and the
	// arms are selected by coder, which the coder axis already sweeps, not by
	// channel layout.
	tnsCells = []toolWiringCell{{2, edgeBitrateMid}}

	// PNS needs BOTH layouts, because the NMR arm reaches MarkPNS through two
	// separate guards, one on chans == 1 and one on chans == 2. Testing a single
	// layout would leave the other guard ungated. The mono cell has to be the
	// low bitrate: NMR mono PNS is dead from 96 kb/s up (measured at 44100 Hz:
	// 0 bands at 96k, 128k and 192k) and fires well below it (121 bands at the
	// 32 kb/s point used here).
	pnsCells = []toolWiringCell{
		{1, edgeBitrateLow},
		{2, edgeBitrateMid},
	}

	// Intensity stereo is a CPE tool, so both cells are stereo. Both bitrates
	// are kept because neither alone is comfortable for every coder: at 32 kb/s
	// fast reaches only 3 bands, and at 128 kb/s twoloop reaches only 9. Their
	// sum is what makes the precondition robust.
	isCells = []toolWiringCell{
		{2, edgeBitrateLow},
		{2, edgeBitrateMid},
	}
)

// toolGate describes one tool switch and how to observe it.
type toolGate struct {
	name string
	// disable sets this gate's switch on a config.
	disable func(*EncoderConfig)
	// used reads this tool's usage counter out of the public Stats.
	used func(Stats) int64
	// cells is where this tool is known to engage; see the cell vars above.
	cells []toolWiringCell
	// cellsAreDistinctBranches says the cells exist to cover SEPARATE code
	// paths rather than to pool margin, so each one must fire on its own.
	// Without it the non-vacuity sum below would let a cell go dead while a
	// sibling kept the total positive, silently ungating that cell's branch.
	// Set it whenever a cell was added for a structural reason; leave it clear
	// when the cells are the same branch sampled twice.
	cellsAreDistinctBranches bool
	// wiring cites where in the encoder the switch is read, so a failure points
	// at the code to look at rather than only at the counter that moved.
	wiring string
}

var toolGates = []toolGate{
	{
		name:    "tns",
		disable: func(c *EncoderConfig) { c.DisableTNS = true },
		used:    func(s Stats) int64 { return s.TNSLongFrames + s.TNSShortFrames },
		cells:   tnsCells,
		wiring: "internal/enc/encoder.go reads it once into useTNS and gates SearchForTNS " +
			"on both arms: pre-quantizer for NMR (tnsFirst) and post-quantizer for twoloop and fast",
	},
	{
		name:    "pns",
		disable: func(c *EncoderConfig) { c.DisablePNS = true },
		used:    func(s Stats) int64 { return s.PNSBands },
		cells:   pnsCells,
		// The mono and stereo cells reach the two separately-guarded MarkPNS
		// paths, so each must fire on its own; pooling them would let either
		// go dead unnoticed.
		cellsAreDistinctBranches: true,
		wiring: "internal/enc/encoder.go gates MarkPNS on the two NMR arms (mono and stereo), " +
			"which is load-bearing there because CanPNS drives the NMR trellis; MarkPNS on the " +
			"non-NMR arm, load-bearing because twoloop reads CanPNS ungated by the pns argument; " +
			"and SearchForPNS on the non-NMR arm, load-bearing because SearchForPNS sets NoiseBT " +
			"from the psy model without consulting CanPNS at all",
	},
	{
		name:    "is",
		disable: func(c *EncoderConfig) { c.DisableIS = true },
		used:    func(s Stats) int64 { return s.ISBands },
		cells:   isCells,
		// Reaches every coder, but through two different mechanisms, which is
		// why this gate reads a counter rather than bytes. On the non-NMR arm
		// the switch skips the post-quantizer search; on the NMR arm it is
		// threaded into nmrDecideStereo, which decides I/S from the psy model
		// before quantization. Until issue #92 the NMR arm was built with
		// intensityStereo hardcoded true and the switch did nothing there.
		wiring: "internal/enc/encoder.go gates SearchForIS and applyIntensityStereo on the " +
			"non-NMR arm, and feeds intensityStereo into nmrDecideStereo on the NMR arm, " +
			"which decides I/S pre-quantizer",
	},
}

// checkToolWiringCell encodes one cell twice, once with the gate's switch clear
// and once with it set, and makes every per-cell assertion. It returns the
// counter observed with the switch CLEAR so the caller can fold it into the
// per-coder non-vacuity total.
//
// It is a function rather than an inline closure so that TestEncoderToolWiring
// stays a plain three-level sweep; the assertions below are the substance and
// they read better away from the loop bookkeeping.
//
// gate is taken by pointer only to avoid copying the 88-byte struct (gocritic
// hugeParam), matching statsFromInternal in stats.go; the callee never mutates
// it.
//
// It deliberately does NOT call t.Helper(): it makes three distinct assertions,
// and marking it a helper reports all of them at the single call site instead of
// at the line that actually failed.
//
//nolint:thelper // this is the test body for one cell, not an assertion wrapper
func checkToolWiringCell(t *testing.T, gate *toolGate, coder Coder, cell toolWiringCell, src [][]float32) int64 {
	on := EncoderConfig{
		SampleRate: toolWiringRate,
		Channels:   cell.channels,
		Bitrate:    cell.bitrate,
		Coder:      coder,
	}
	off := on
	gate.disable(&off)

	onAUs, onASC, onStats := encodeCollectStats(t, on, src)
	offAUs, offASC, offStats := encodeCollectStats(t, off, src)
	onUsed, offUsed := gate.used(onStats), gate.used(offStats)

	// Both directions must produce a usable stream, not merely a different one.
	assertDecodable(t, "on", onASC, onAUs, src)
	assertDecodable(t, "off", offASC, offAUs, src)

	// Where the cells cover separate code paths, each must carry its own
	// weight; see cellsAreDistinctBranches.
	if gate.cellsAreDistinctBranches && onUsed == 0 {
		t.Errorf("%s never fired on this cell even with the switch clear, so this cell's "+
			"assertions are vacuous. It is not interchangeable with the other cells: it is "+
			"here to cover a separate code path (%s)", gate.name, gate.wiring)
	}

	if offUsed != 0 {
		t.Errorf("%s still used on %d bands/frames with the switch set, want 0; the switch "+
			"is not reaching the search (%s)", gate.name, offUsed, gate.wiring)
		// The defect is established. The byte check below would add a second,
		// differently-worded error for the same cause, so stop here.
		return onUsed
	}

	// Where the tool actually fired with the switch clear, the bitstream must
	// differ too. Corroboration rather than the gate itself: it catches a
	// counter that moves without the output following.
	if onUsed > 0 && slices.EqualFunc(onAUs, offAUs, bytes.Equal) {
		t.Errorf("%s fired on %d bands/frames yet the bitstream is byte-identical with the "+
			"switch set; the counter moved but the output did not", gate.name, onUsed)
	}
	return onUsed
}

// TestEncoderToolWiring gates that DisableTNS, DisablePNS and DisableIS reach
// the encoder, without an external oracle.
//
// Each gate runs two directions per cell. The DISABLED direction is the actual
// gate and is asserted per cell: the tool's usage counter must be exactly zero,
// which nothing but the switch reaching the search can produce. The ENABLED
// direction is a non-vacuity precondition, asserted as a SUM over the cells:
// the counter must be nonzero somewhere. Summing is deliberate where the cells
// exist only to pool margin, because asserting per cell that the tool fires
// would pin psychoacoustic outcomes this test has no business pinning, and a
// retune moving one cell to zero would then fail here spuriously. Either way the
// test cannot silently degrade into asserting 0 == 0.
//
// The exception is a gate marked cellsAreDistinctBranches, where each cell must
// fire on its own; see that field. For such a gate the per-cell check subsumes
// the sum, which can then never fire, so the sum is the rule only for gates
// whose cells pool margin.
func TestEncoderToolWiring(t *testing.T) {
	if toolWiringSkipRace {
		t.Skip("skipped under -race: single-goroutine and build-independent, and too " +
			"costly to repeat across six race lanes; see edge_soak_race_test.go")
	}
	inputs := edgeInputCache{}
	for _, gate := range toolGates {
		t.Run(gate.name, func(t *testing.T) {
			// Without this a gate declared with no cells runs no subtests, the
			// per-coder verdict below is suppressed by ran == 0, and the whole
			// gate passes while asserting nothing. That is the same silent-pass
			// this test exists to prevent, so it is checked rather than assumed.
			if len(gate.cells) == 0 {
				t.Fatalf("%s declares no cells, so it asserts nothing", gate.name)
			}
			for _, tc := range testCoders {
				t.Run(tc.name, func(t *testing.T) {
					// ran counts cells that got far enough to contribute a
					// measurement, and allPassed records whether they all
					// succeeded. The verdict below needs both: a cell that
					// aborted via t.Fatal never reached the accumulator, and a
					// -run filter selecting none of this gate's cells leaves it
					// at zero. Without these guards the vacuity check fires on
					// either, blaming the tool for not firing when the truth is
					// that nothing measured it.
					var usedWithTool int64
					ran, allPassed := 0, true
					for _, cell := range gate.cells {
						src := inputs.get(cell.chLabel(), toolWiringRate, toolWiringFrames)
						ok := t.Run(cell.name(), func(t *testing.T) {
							usedWithTool += checkToolWiringCell(t, &gate, tc.coder, cell, src)
							ran++
						})
						allPassed = allPassed && ok
					}
					if ran > 0 && allPassed && usedWithTool == 0 {
						t.Errorf("%s never fired for %s on any of the %d cells that ran, even with the "+
							"switch clear, so the disabled-direction assertions above are vacuous; pick "+
							"cells where the tool engages (see tnsCells, pnsCells and isCells)",
							gate.name, tc.name, ran)
					}
				})
			}
		})
	}
}

// TestNMRStereoGuardWiring is the oracle-free gate for the pre-quantization
// stereo guard added for issue #92: the (midSide != 0 || intensityStereo) term
// that decides whether nmrDecideStereo runs at all (internal/enc/encoder.go).
// Two separate one-line mutations of that guard, `||` -> `&&` and deleting the
// whole term, each survive the entire rest of the NON-oracle suite, so a plain
// `go test` proves nothing about the guard. Measured by running the full oracle
// lane under each: the `||` -> `&&` half IS caught there, by
// TestPhase4NMRStereoSwitchesVsC/nois at +65% stream size, but the deleted-term
// half is NOT. That gate's `neither` cell passes under it at -0.06% size, well
// inside the 3% bound. So this test is the only coverage anywhere for the
// deleted-term mutation, and the only oracle-free coverage for either: do not
// delete it as redundant with the oracle gate. Each assertion below kills one
// mutation, reading the public Stats counters rather than an external reference.
//
// Both assertions pin RELATIONS, never magnitudes, so a psychoacoustic retune
// cannot fail them spuriously. The measured counts that motivate the cells are
// cited for the record only, not asserted.
func TestNMRStereoGuardWiring(t *testing.T) {
	if toolWiringSkipRace {
		t.Skip("skipped under -race: single-goroutine and build-independent, and " +
			"too costly to repeat across six race lanes; see edge_soak_race_test.go")
	}
	src := edgeSoakInput(archChanStereo, toolWiringRate, toolWiringSeconds)

	// (a) kills `||` -> `&&`. With DisableIS the ONLY switch set, midSide is
	// still -1 (auto), so the correct guard (midSide != 0 || intensityStereo)
	// is true, nmrDecideStereo runs, and it codes bands mid/side. Under `&&`
	// the guard also needs intensityStereo, which DisableIS just cleared, so
	// the call is skipped and no band is coded mid/side. Measured NMR stereo
	// 44100 castanets, DisableIS only: MSBands 2069 at 32 kb/s and 2138 at
	// 128 kb/s in the correct build, 0 under the mutant. Asserting > 0 kills it
	// without pinning the magnitude.
	isOnly := EncoderConfig{SampleRate: toolWiringRate, Channels: 2,
		Bitrate: edgeBitrateMid, Coder: CoderNMR, DisableIS: true}
	_, _, isStats := encodeCollectStats(t, isOnly, src)
	if isStats.MSBands <= 0 {
		t.Errorf("CoderNMR with DisableIS only coded %d mid/side bands, want > 0: the "+
			"(midSide != 0 || intensityStereo) guard is not letting nmrDecideStereo run "+
			"for mid/side, so `||` may have degraded to `&&` (issue #92)", isStats.MSBands)
	}

	// (b) kills deletion of the whole (midSide != 0 || intensityStereo) term.
	// With BOTH DisableMS and DisableIS set the correct guard is false, so
	// nmrDecideStereo is skipped and its PNS-stereo reservation never runs,
	// leaving the CanPNS bands from the two-channel intersection intact. Delete
	// the term and the call runs unconditionally: the reservation clears CanPNS
	// on every band that is not clearly decorrelated, so fewer bands survive as
	// PNS. With only DisableMS set (I/S still on) the correct guard is already
	// true, so that reservation runs in BOTH the correct and mutant builds and
	// gives the reference count. Measured NMR stereo 44100 castanets 2 s at
	// 32 kb/s: PNSBands 306 with both off vs 42 with only MS off in the correct
	// build; both collapse to 42 under the mutant. Asserting bothOff > onlyMSOff
	// kills it.
	bothOff := EncoderConfig{SampleRate: toolWiringRate, Channels: 2,
		Bitrate: edgeBitrateLow, Coder: CoderNMR, DisableMS: true, DisableIS: true}
	onlyMSOff := bothOff
	onlyMSOff.DisableIS = false
	_, _, bothStats := encodeCollectStats(t, bothOff, src)
	_, _, msStats := encodeCollectStats(t, onlyMSOff, src)
	if bothStats.PNSBands <= msStats.PNSBands {
		t.Errorf("CoderNMR PNSBands with DisableMS+DisableIS (%d) not greater than with "+
			"DisableMS alone (%d): the (midSide != 0 || intensityStereo) guard is not "+
			"skipping nmrDecideStereo's PNS-stereo reservation when both stereo tools are "+
			"off, so the term may have been deleted (issue #92)",
			bothStats.PNSBands, msStats.PNSBands)
	}
}
