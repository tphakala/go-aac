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

// plantTrellisSentinels seeds dpp with the sentinel shapes the goaac_simd
// differential must reconcile byte-for-byte with the scalar, which writes a
// RAW MaxFloat32 (never node[o]+vals[o], which would overflow to +Inf) whenever
// no candidate beats the MaxFloat32 incumbent: a lone MaxFloat32 (saturates the
// one-wide windows the dense base sweep produces at the mdiff edges), a
// contiguous run long enough to saturate whole windows at the small n values,
// and a genuine +Inf that must never win (matching the primitive's +Inf pad).
func plantTrellisSentinels(dpp []float32, rng *rand.Rand) {
	dpp[rng.Intn(len(dpp))] = fmath.MaxFloat32
	runStart := rng.Intn(len(dpp))
	for j := 0; j < 18 && runStart+j < len(dpp); j++ {
		dpp[runStart+j] = fmath.MaxFloat32
	}
	dpp[rng.Intn(len(dpp))] = fmath.Inf32()
}

// TestNMRTrellisStepMatchesReference sweeps the kernel's parameter space and
// asserts bit-identical dp and bp against the straight transcription of the
// C. The sweep is a cross product of sampled shapes, not an exhaustive proof:
// it is dense in base, the axis the bounds derivation turns on, and sampled in
// step, nCur, nPrev and mdiff; the step list covers both signs, zero, and
// values where the floor/ceil rounding bites.
//
// The optimisation only reshapes which op values are visited, so the cases
// that matter are the window edges (base beyond +/-mdiff, so the range clips
// or empties), step > 1 (where the floor/ceil rounding of the bounds is
// load-bearing), and ties in c (where the strict < tie-break has to keep
// picking the lowest op).
func TestNMRTrellisStepMatchesReference(t *testing.T) {
	const maxN = NMRNCand
	rng := rand.New(rand.NewSource(1))

	// Reused across the whole sweep on purpose: a stale value left in the
	// scratch by one call must never leak into the next (the goaac_simd wrapper
	// must write every cell it reads), which the encoder relies on after the
	// NMRState struct-zeroing reset.
	var ts nmrTrellisScratch

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
		plantTrellisSentinels(dpp, rng)
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
								nCur, nPrev, base, step, mdiff, &ts)
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
	var ts nmrTrellisScratch
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
		nmrTrellisStep(dpGot, bpGot, dpp, node, lamsf, maxN, maxN, base, 0, ScaleMaxDiff, &ts)
		for o := range maxN {
			if math.Float32bits(dpGot[o]) != math.Float32bits(dpWant[o]) || bpGot[o] != bpWant[o] {
				t.Fatalf("step=0 base=%d o=%d: got dp=%v bp=%d, want dp=%v bp=%d",
					base, o, dpGot[o], bpGot[o], dpWant[o], bpWant[o])
			}
		}
	}
}

// modRange maps an arbitrary (possibly negative) fuzz int into [0, n].
func modRange(v, n int) int {
	m := v % (n + 1)
	if m < 0 {
		m += n + 1
	}
	return m
}

// FuzzNMRTrellisStep drives the shape parameters (nCur, nPrev, base, step,
// mdiff) the fixed grid only samples, and asserts the dispatched kernel stays
// bit-identical to the naive C-transcription reference and never panics. The
// float payloads come from a seeded PRNG through plantTrellisSentinels, so they
// stay finite or the MaxFloat32/+Inf sentinels and NEVER NaN: a NaN in dpp is
// the one input class where the SIMD path legitimately diverges from the scalar
// (the primitive keeps a leading-NaN incumbent, the scalar skips it), and
// nmrSolve provably never produces one, so feeding it here would be a false
// divergence, not a real defect.
func FuzzNMRTrellisStep(f *testing.F) {
	f.Add(11, 11, 63, 0, 60, int64(1))  // fine shape, in-window base, step 1
	f.Add(17, 17, 179, 3, 60, int64(2)) // coarse shape (step index 3 -> 8)
	f.Add(11, 11, 200, 0, 60, int64(3)) // base past mdiff -> globally empty
	f.Add(2, 96, -70, 4, 7, int64(4))   // asymmetric shape, mdiff 7, step 0 delegate
	f.Add(96, 1, 5, 1, 0, int64(5))     // mdiff 0, single-candidate windows
	f.Fuzz(func(t *testing.T, nCurR, nPrevR, baseR, stepR, mdiffR int, seed int64) {
		const maxN = NMRNCand
		nCur := modRange(nCurR, maxN)
		nPrev := modRange(nPrevR, maxN)
		mdiff := modRange(mdiffR, ScaleMaxDiff)
		// base swept well past +/-mdiff on both sides so the window clips and
		// empties; every step value the call sites and the sweep use.
		base := modRange(baseR, 4*ScaleMaxDiff) - 2*ScaleMaxDiff
		steps := []int{1, 2, 3, 8, 0, -1, -8}
		step := steps[modRange(stepR, len(steps)-1)]

		rng := rand.New(rand.NewSource(seed))
		lamsf := make([]float32, 2*ScaleMaxDiff+1)
		dpp := make([]float32, maxN)
		node := make([]float32, maxN)
		for i := range lamsf {
			lamsf[i] = float32(rng.Intn(8)) * 0.5 // finite, non-negative, like lam*ScalefactorBits
		}
		for i := range dpp {
			dpp[i] = (rng.Float32()*2 - 1) * 12
			node[i] = (rng.Float32()*2 - 1) * 12
		}
		plantTrellisSentinels(dpp, rng)

		dpGot, dpWant := make([]float32, maxN), make([]float32, maxN)
		bpGot, bpWant := make([]uint8, maxN), make([]uint8, maxN)
		var ts nmrTrellisScratch
		nmrTrellisStepRef(dpWant, bpWant, dpp, node, lamsf, nCur, nPrev, base, step, mdiff)
		nmrTrellisStep(dpGot, bpGot, dpp, node, lamsf, nCur, nPrev, base, step, mdiff, &ts)
		for o := range nCur {
			if math.Float32bits(dpGot[o]) != math.Float32bits(dpWant[o]) || bpGot[o] != bpWant[o] {
				t.Fatalf("nCur=%d nPrev=%d base=%d step=%d mdiff=%d o=%d: got dp=%#x bp=%d, want dp=%#x bp=%d",
					nCur, nPrev, base, step, mdiff, o,
					math.Float32bits(dpGot[o]), bpGot[o], math.Float32bits(dpWant[o]), bpWant[o])
			}
		}
	})
}
