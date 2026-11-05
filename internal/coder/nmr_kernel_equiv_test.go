// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package coder

import (
	"math"
	"math/rand"
	"testing"

	"github.com/tphakala/go-aac/internal/fmath"
)

// fillNodeCostInput fills nd with finite non-negative float32 values over
// many binades and nb with int32 bit counts, including counts above 2^24
// whose float32 conversion rounds.
func fillNodeCostInput(nd *[NMRNCand]float32, nb *[NMRNCand]int32, rng *rand.Rand) {
	for i := range nd {
		nd[i] = float32(math.Ldexp(rng.Float64(), rng.Intn(80)-40))
		switch rng.Intn(4) {
		case 0:
			nb[i] = int32(rng.Intn(1 << 12))
		case 1:
			nb[i] = int32(rng.Uint32() >> 1)
		default:
			nb[i] = int32(rng.Intn(1 << 25))
		}
	}
}

// checkNodeCost asserts the dispatched nmrNodeCost matches nmrNodeCostGo
// bit for bit for a band of n candidates.
func checkNodeCost(t *testing.T, nd *[NMRNCand]float32, nb *[NMRNCand]int32, n int, lam float32) {
	t.Helper()
	got, want := make([]float32, n), make([]float32, n)
	nmrNodeCost(got, nd, nb, lam)
	nmrNodeCostGo(want, nd, nb, lam)
	for o := range n {
		if math.Float32bits(got[o]) != math.Float32bits(want[o]) {
			t.Fatalf("n=%d lam=%v o=%d: got %#x, want %#x (nd=%v nb=%d)",
				n, lam, o, math.Float32bits(got[o]), math.Float32bits(want[o]), nd[o], nb[o])
		}
	}
}

// TestNMRNodeCostMatchesReference sweeps every band width up to NMRNCand
// (below the kernel width, exact multiples and overlapped tails) and the
// lambda range the bisection visits.
func TestNMRNodeCostMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(38))
	var nd [NMRNCand]float32
	var nb [NMRNCand]int32
	lams := []float32{0, 1, 0x1p-20, 1.0 / 3, 7.25, 1e6}
	for trial := range 200 {
		fillNodeCostInput(&nd, &nb, rng)
		lam := lams[trial%len(lams)]
		if trial >= len(lams) {
			lam = float32(math.Ldexp(rng.Float64(), rng.Intn(40)-20))
		}
		for n := range NMRNCand + 1 {
			checkNodeCost(t, &nd, &nb, n, lam)
		}
	}
}

// FuzzNMRNodeCost drives the band width, lambda and payload seed beyond the
// fixed sweep.
func FuzzNMRNodeCost(f *testing.F) {
	f.Add(96, float32(1), int64(1))
	f.Add(8, float32(0.5), int64(2))
	f.Add(13, float32(3e-4), int64(3))
	f.Fuzz(func(t *testing.T, nR int, lam float32, seed int64) {
		if math.IsNaN(float64(lam)) || math.IsInf(float64(lam), 0) {
			return
		}
		var nd [NMRNCand]float32
		var nb [NMRNCand]int32
		fillNodeCostInput(&nd, &nb, rand.New(rand.NewSource(seed)))
		checkNodeCost(t, &nd, &nb, modRange(nR, NMRNCand), lam)
	})
}

// fillReconcileInput fills node with finite costs, vals with argmin minima
// that include the MaxFloat32 incumbent, values on either side of it and the
// +Inf of a dead window, and idxs with tap indices.
func fillReconcileInput(node, vals []float32, idxs []int32, rng *rand.Rand) {
	for o := range vals {
		node[o] = (rng.Float32()*2 - 1) * 12
		switch rng.Intn(8) {
		case 0:
			vals[o] = fmath.MaxFloat32
		case 1:
			vals[o] = math.Nextafter32(fmath.MaxFloat32, 0)
		case 2:
			vals[o] = fmath.Inf32()
		case 3:
			vals[o] = float32(math.Ldexp(rng.Float64(), 120+rng.Intn(8)))
		default:
			vals[o] = (rng.Float32()*2 - 1) * 24
		}
		idxs[o] = int32(rng.Intn(2 * NMRNCand))
	}
}

// checkReconcile asserts the dispatched nmrTrellisReconcile matches
// nmrTrellisReconcileGo bit for bit on dp and bp.
func checkReconcile(t *testing.T, node, vals []float32, idxs []int32, dHi int) {
	t.Helper()
	n := len(vals)
	dpGot, dpWant := make([]float32, n), make([]float32, n)
	bpGot, bpWant := make([]uint8, n), make([]uint8, n)
	nmrTrellisReconcile(dpGot, bpGot, node, vals, idxs, dHi)
	nmrTrellisReconcileGo(dpWant, bpWant, node, vals, idxs, dHi)
	for o := range n {
		if math.Float32bits(dpGot[o]) != math.Float32bits(dpWant[o]) || bpGot[o] != bpWant[o] {
			t.Fatalf("n=%d dHi=%d o=%d: got dp=%#x bp=%d, want dp=%#x bp=%d (val=%v idx=%d)",
				n, dHi, o, math.Float32bits(dpGot[o]), bpGot[o], math.Float32bits(dpWant[o]), bpWant[o], vals[o], idxs[o])
		}
	}
}

// TestNMRTrellisReconcileMatchesReference sweeps every row count up to
// NMRNCand and back-pointer offsets dHi that make o-dHi+idx wrap below zero
// and above 255, which the uint8 conversion truncates.
func TestNMRTrellisReconcileMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(39))
	node := make([]float32, NMRNCand)
	vals := make([]float32, NMRNCand)
	idxs := make([]int32, NMRNCand)
	for range 200 {
		fillReconcileInput(node, vals, idxs, rng)
		dHi := rng.Intn(4*NMRNCand) - 2*NMRNCand
		for n := 1; n <= NMRNCand; n++ {
			checkReconcile(t, node[:n], vals[:n], idxs[:n], dHi)
		}
	}
}

// FuzzNMRTrellisReconcile drives the row count, back-pointer offset and
// payload seed beyond the fixed sweep.
func FuzzNMRTrellisReconcile(f *testing.F) {
	f.Add(96, 40, int64(1))
	f.Add(8, -3, int64(2))
	f.Add(21, 300, int64(3))
	f.Fuzz(func(t *testing.T, nR, dHi int, seed int64) {
		n := 1 + modRange(nR, NMRNCand-1)
		node := make([]float32, n)
		vals := make([]float32, n)
		idxs := make([]int32, n)
		fillReconcileInput(node, vals, idxs, rand.New(rand.NewSource(seed)))
		checkReconcile(t, node, vals, idxs, dHi%(1<<20))
	})
}
