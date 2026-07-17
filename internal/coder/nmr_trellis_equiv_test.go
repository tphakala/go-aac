// SPDX-License-Identifier: LGPL-2.1-or-later
package coder

import (
	"math"
	"math/rand"
	"testing"

	"github.com/tphakala/go-aac/internal/fmath"
)

// nmrTrellisStepRef is nmr_trellis_step_c @ d09d5afc3a transcribed
// statement for statement, and is the form nmrTrellisStep carried before the
// window was turned into computed loop bounds. It exists so the optimised
// kernel has something to be differentially checked against; keep it in
// lockstep with the C, never with nmrTrellisStep.
func nmrTrellisStepRef(dp []float32, bp []uint8, dpp, node, lamsf []float32,
	nCur, nPrev, base, step, mdiff int) {
	for o := range nCur {
		best := -1
		bestc := float32(fmath.MaxFloat32)
		for op := range nPrev {
			d := base + (o-op)*step
			if d < -mdiff || d > mdiff {
				continue
			}
			c := dpp[op] + lamsf[d+mdiff]
			if c < bestc {
				bestc = c
				best = op
			}
		}
		if best < 0 {
			bp[o] = 0
			dp[o] = fmath.MaxFloat32
		} else {
			bp[o] = uint8(best)
			dp[o] = node[o] + bestc
		}
	}
}

// TestNMRTrellisStepMatchesReference sweeps the kernel's parameter space and
// asserts bit-identical dp and bp against the straight transcription of the
// C. The sweep is a cross product of sampled shapes, not an exhaustive proof:
// it is dense in base, the axis the bounds derivation turns on, and sampled in
// step, nCur, nPrev and mdiff; the step list covers both signs, zero, and
// values where the floor/ceil rounding bites. The optimisation only reshapes which op are
// visited, so the cases that matter are the window edges (base beyond
// +/-mdiff, so the range clips or empties), step > 1 (where the floor/ceil
// rounding of the bounds is load-bearing), and ties in c (where the strict
// < tie-break has to keep picking the lowest op).
func TestNMRTrellisStepMatchesReference(t *testing.T) {
	const maxN = NMRNCand
	rng := rand.New(rand.NewSource(1))

	lamsf := make([]float32, 2*ScaleMaxDiff+1)
	dpp := make([]float32, maxN)
	node := make([]float32, maxN)

	dpGot, dpWant := make([]float32, maxN), make([]float32, maxN)
	bpGot, bpWant := make([]uint8, maxN), make([]uint8, maxN)

	// A coarse value set makes exact ties common, which is what the
	// tie-break needs to be exercised by; the random fill covers the rest.
	fill := func(quantised bool) {
		for i := range lamsf {
			if quantised {
				lamsf[i] = float32(rng.Intn(4)) * 0.5
			} else {
				lamsf[i] = rng.Float32() * 10
			}
		}
		for i := range dpp {
			if quantised {
				dpp[i] = float32(rng.Intn(4)) * 0.5
				node[i] = float32(rng.Intn(4)) * 0.25
			} else {
				dpp[i] = rng.Float32() * 10
				node[i] = rng.Float32() * 10
			}
		}
		// FLT_MAX in dpp reaches the best < 0 sentinel path through a
		// non-empty window, which is distinct from an empty one.
		dpp[rng.Intn(len(dpp))] = fmath.MaxFloat32
	}

	// The call sites only ever pass nmrStep (1) or nmrCoarse (8). 0 and the
	// negative steps are unreachable there, but the computed bounds divide
	// by step where the C did not, so every sign is swept rather than
	// reasoned about: a wrong rounding or an unflipped inequality would
	// otherwise be a silently wrong answer, not a panic.
	steps := []int{1, 2, 3, 8, 0, -1, -2, -8}
	mdiffs := []int{0, 1, 7, ScaleMaxDiff}
	ns := []int{0, 1, 2, 11, 16, 17, 96}

	cases := 0
	for _, quantised := range []bool{true, false} {
		for _, step := range steps {
			// refill per step rather than once per fill mode, so the sweep
			// does not rest its whole float domain on two fixed data sets
			fill(quantised)
			for _, mdiff := range mdiffs {
				for _, nCur := range ns {
					for _, nPrev := range ns {
						// base spans well past +/-mdiff on both sides so the
						// window clips at each end and empties beyond them.
						// Walked contiguously on purpose: a stride pins
						// -mdiff-base to one residue class mod gcd(stride,
						// step), silently dropping window-edge cases. Which
						// ones depends on the alignment; stride 2 with step 8
						// never divides exactly, so the no-rounding edge stops
						// being tested. Contiguous keeps every residue live for
						// whatever step a future maintainer adds.
						for base := -2*ScaleMaxDiff - 3; base <= 2*ScaleMaxDiff+3; base++ {
							clear(dpGot)
							clear(dpWant)
							clear(bpGot)
							clear(bpWant)

							nmrTrellisStepRef(dpWant, bpWant, dpp, node, lamsf,
								nCur, nPrev, base, step, mdiff)
							nmrTrellisStep(dpGot, bpGot, dpp, node, lamsf,
								nCur, nPrev, base, step, mdiff)
							cases++

							for o := range nCur {
								if math.Float32bits(dpGot[o]) != math.Float32bits(dpWant[o]) ||
									bpGot[o] != bpWant[o] {
									t.Fatalf("quantised=%v step=%d mdiff=%d nCur=%d nPrev=%d base=%d o=%d:\n got dp=%v (%#x) bp=%d\nwant dp=%v (%#x) bp=%d",
										quantised, step, mdiff, nCur, nPrev, base, o,
										dpGot[o], math.Float32bits(dpGot[o]), bpGot[o],
										dpWant[o], math.Float32bits(dpWant[o]), bpWant[o])
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("%d parameter combinations compared", cases)
}

// TestNMRTrellisStepZeroStep pins the step == 0 case as a named regression:
// it is the one step value the bounds cannot be divided through for, so it
// takes its own switch arm. The sweep above covers it too; this spells out
// the admits-every-op and admits-none halves against a readable base list.
func TestNMRTrellisStepZeroStep(t *testing.T) {
	const maxN = 17
	lamsf := make([]float32, 2*ScaleMaxDiff+1)
	for i := range lamsf {
		lamsf[i] = float32(i) * 0.125
	}
	dpp := make([]float32, maxN)
	node := make([]float32, maxN)
	for i := range dpp {
		dpp[i] = float32(i%5) * 0.5
		node[i] = float32(i%3) * 0.25
	}

	dpGot, dpWant := make([]float32, maxN), make([]float32, maxN)
	bpGot, bpWant := make([]uint8, maxN), make([]uint8, maxN)

	// base inside the window admits every op; base outside admits none
	for _, base := range []int{0, 7, -7, ScaleMaxDiff, -ScaleMaxDiff, ScaleMaxDiff + 1, -ScaleMaxDiff - 1} {
		nmrTrellisStepRef(dpWant, bpWant, dpp, node, lamsf, maxN, maxN, base, 0, ScaleMaxDiff)
		nmrTrellisStep(dpGot, bpGot, dpp, node, lamsf, maxN, maxN, base, 0, ScaleMaxDiff)
		for o := range maxN {
			if math.Float32bits(dpGot[o]) != math.Float32bits(dpWant[o]) || bpGot[o] != bpWant[o] {
				t.Fatalf("step=0 base=%d o=%d: got dp=%v bp=%d, want dp=%v bp=%d",
					base, o, dpGot[o], bpGot[o], dpWant[o], bpWant[o])
			}
		}
	}
}
