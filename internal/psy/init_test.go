// SPDX-License-Identifier: LGPL-2.1-or-later

package psy

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/tphakala/go-aac/internal/tables"
)

// reader walks a little-endian cpsy fixture.
type reader struct {
	d   []byte
	off int
	t   *testing.T
}

func (r *reader) i32() int32 {
	v := int32(binary.LittleEndian.Uint32(r.d[r.off:]))
	r.off += 4
	return v
}

func (r *reader) f32() float32 {
	v := math.Float32frombits(binary.LittleEndian.Uint32(r.d[r.off:]))
	r.off += 4
	return v
}

func open(t *testing.T, name string) *reader {
	t.Helper()
	d, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return &reader{d: d, t: t}
}

// relDiff is |a-b| relative to the larger magnitude, with an absolute
// floor so exact-zero fields compare cleanly.
func relDiff(a, b float32) float64 {
	// A non-finite value on either side must FAIL the comparison, not slip
	// through it. Without this guard relDiff(NaN, x) is NaN, and every caller's
	// `relDiff(...) > tol` is false for NaN, so a psychoacoustic output that went
	// NaN or Inf would pass the golden gates silently. Bit-identical operands
	// (including matching Inf) are still a zero difference.
	if a == b {
		return 0
	}
	af, bf := float64(a), float64(b)
	if math.IsNaN(af) || math.IsNaN(bf) || math.IsInf(af, 0) || math.IsInf(bf, 0) {
		return math.Inf(1)
	}
	d := math.Abs(af - bf)
	m := math.Max(math.Abs(af), math.Abs(bf))
	if m < 1e-20 {
		return 0
	}
	return d / m
}

func newFixtureContextRate(rate, srIdx, bitrate, channels int) *Context {
	bands := [2][]uint8{tables.SwbSize1024[srIdx], tables.SwbSize128[srIdx]}
	numBands := [2]int{int(tables.NumSwb1024[srIdx]), int(tables.NumSwb128[srIdx])}
	// Coding bandwidth exactly as the encoder computes it for the fast
	// coder without PNS/IS (aacenc.c:1591-1616 non-NMR branch).
	frameBr := bitrate / channels
	bw := max(3000, aacCutoffFromBitrate(frameBr, 1, rate))
	bw = min(max(bw, 8000), rate/2)
	return New(rate, bitrate, channels, bw, bands, numBands)
}

func newFixtureContext(bitrate, channels int) *Context {
	return newFixtureContextRate(fixtureRate, 4, bitrate, channels)
}

// aacCutoffFromBitrate mirrors AAC_CUTOFF_FROM_BITRATE (psymodel.h:35
// @ d09d5afc3a); duplicated here so the psy tests do not depend on
// internal/enc.
func aacCutoffFromBitrate(bitRate, channels, sampleRate int) int {
	if bitRate == 0 {
		return sampleRate / 2
	}
	return min(
		max(bitRate/channels/5, bitRate/channels*15/32-5500),
		3000+bitRate/channels/4,
		12000+bitRate/channels/16,
		22000,
		sampleRate/2)
}

//nolint:gocognit // sequential walk of the cpsy fixture format
func TestPsyInitAgainstC(t *testing.T) {
	cases := []struct {
		name     string
		fixture  string
		rate     int
		srIdx    int
		bitrate  int
		channels int
	}{
		{"mono 128k 44.1k", "cinit_m1_44.bin", 44100, 4, 128000, 1},
		{"stereo 128k 44.1k", "cinit_s2_44.bin", 44100, 4, 128000, 2},
		{"mono 128k 48k", "cinit_m1_48.bin", 48000, 3, 128000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := open(t, tc.fixture)
			ctx := newFixtureContextRate(tc.rate, tc.srIdx, tc.bitrate, tc.channels)

			if got, want := ctx.chanBitrate, int(r.i32()); got != want {
				t.Errorf("chan_bitrate %d want %d", got, want)
			}
			if got, want := ctx.frameBits, int(r.i32()); got != want {
				t.Errorf("frame_bits %d want %d", got, want)
			}
			if got, want := ctx.pe.min, r.f32(); got != want {
				t.Errorf("pe.min %g want %g", got, want)
			}
			if got, want := ctx.pe.max, r.f32(); got != want {
				t.Errorf("pe.max %g want %g", got, want)
			}
			if got, want := ctx.Bitres.Size, int(r.i32()); got != want {
				t.Errorf("bitres.size %d want %d", got, want)
			}
			if got, want := ctx.fillLevel, int(r.i32()); got != want {
				t.Errorf("fill_level %d want %d", got, want)
			}
			if got, want := ctx.pch[0].attackThreshold, r.f32(); got != want {
				t.Errorf("attack_threshold %g want %g", got, want)
			}
			var worst [5]float64
			for j := range 2 {
				nb := int(r.i32())
				if nb != ctx.NumBands[j] {
					t.Fatalf("num_bands[%d] %d want %d", j, ctx.NumBands[j], nb)
				}
				for g := range nb {
					c := &ctx.psyCoef[j][g]
					fields := []struct {
						idx  int
						name string
						got  float32
						want float32
					}{
						{0, "ath", c.ath, r.f32()},
						{1, "barks", c.barks, r.f32()},
						{2, "spread_low0", c.spreadLow[0], r.f32()},
						{2, "spread_low1", c.spreadLow[1], r.f32()},
						{3, "spread_hi0", c.spreadHi[0], r.f32()},
						{3, "spread_hi1", c.spreadHi[1], r.f32()},
						{4, "min_snr", c.minSnr, r.f32()},
					}
					for _, f := range fields {
						d := relDiff(f.got, f.want)
						worst[f.idx] = math.Max(worst[f.idx], d)
						// spread factors amplify the 1-ulp atanf difference
						// in barks through exp10; 1e-5 rel is the measured
						// envelope (rehearsed max 4.4e-6)
						tol := 1e-6
						if f.idx == 2 || f.idx == 3 {
							tol = 1e-5
						}
						if d > tol {
							t.Errorf("j%d g%d %s: got %g want %g rel %g",
								j, g, f.name, f.got, f.want, d)
						}
					}
				}
			}
			t.Logf("init max rel diffs: ath %.3g barks %.3g spread_low %.3g spread_hi %.3g min_snr %.3g",
				worst[0], worst[1], worst[2], worst[3], worst[4])
			if r.off != len(r.d) {
				t.Fatalf("fixture not fully consumed: %d of %d", r.off, len(r.d))
			}
		})
	}
}
