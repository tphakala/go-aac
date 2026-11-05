// SPDX-License-Identifier: LGPL-2.1-or-later
package coder

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

// checkNodeCostEquiv asserts that the dispatched nmrNodeCost writes the same
// bits as nmrNodeCostScalar for dst[:n] and leaves dst[n:] untouched. On the
// noasm build both sides are the scalar, so only the default and SIMD_DISABLE
// lanes test the dispatch.
func checkNodeCostEquiv(t *testing.T, n int, lam float32, rng *rand.Rand) {
	t.Helper()
	var nd [NMRNCand]float32
	var nb [NMRNCand]int32
	for i := range nd {
		nd[i] = (rng.Float32()*2 - 1) * 1e3
		nb[i] = int32(rng.Intn(4000))
	}
	// Magnitudes above 2^24 make float32(nb) round, so a path that scales in
	// integer or double precision first would diverge.
	big := []int32{1<<24 + 1, 1<<24 + 3, -(1<<24 + 1), math.MaxInt32, math.MinInt32}
	for i, v := range big {
		nb[(i*7+1)%NMRNCand] = v
	}

	var got, want [NMRNCand]float32
	for i := range got {
		got[i] = float32(i) + 0.5 // sentinel for "untouched"
		want[i] = got[i]
	}
	nmrNodeCost(got[:n], &nd, &nb, lam)
	nmrNodeCostScalar(want[:n], &nd, &nb, lam)
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("n=%d lam=%g: dst[%d] = %08x, want %08x", n, lam, i,
				math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// TestNMRNodeCostMatchesScalar covers every length 0..NMRNCand, so below, at
// and above the AVX (8) and NEON (4) thresholds and every tail length, with
// the lambda drawn over several decades.
func TestNMRNodeCostMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for n := 0; n <= NMRNCand; n++ {
		for range 20 {
			lam := float32(math.Exp(rng.NormFloat64() * 8))
			checkNodeCostEquiv(t, n, lam, rng)
		}
	}
}

func FuzzNMRNodeCostEquiv(f *testing.F) {
	f.Add(11, math.Float32bits(1), int64(1))
	f.Add(17, math.Float32bits(0.0123), int64(2))
	f.Add(NMRNCand, math.Float32bits(3e5), int64(3))
	f.Add(7, math.Float32bits(0), int64(4))
	f.Fuzz(func(t *testing.T, nR int, lamBits uint32, seed int64) {
		lam := math.Float32frombits(lamBits)
		if math.IsNaN(float64(lam)) || math.IsInf(float64(lam), 0) {
			t.Skip("lambda is always finite")
		}
		n := modRange(nR, NMRNCand)
		checkNodeCostEquiv(t, n, lam, rand.New(rand.NewSource(seed)))
	})
}

// BenchmarkNMRNodeCost compares the dispatched node cost with the scalar loop
// at the two real candidate counts (11 and 17) and at the cap.
func BenchmarkNMRNodeCost(b *testing.B) {
	var nd [NMRNCand]float32
	var nb [NMRNCand]int32
	var dst [NMRNCand]float32
	for i := range nd {
		nd[i] = float32(i%13) * 0.5
		nb[i] = int32(i*37%900 + 3)
	}
	for _, n := range []int{11, 17, NMRNCand} {
		b.Run("scalar/"+strconv.Itoa(n), func(b *testing.B) {
			for b.Loop() {
				nmrNodeCostScalar(dst[:n], &nd, &nb, 0.75)
			}
		})
		b.Run("dispatched/"+strconv.Itoa(n), func(b *testing.B) {
			for b.Loop() {
				nmrNodeCost(dst[:n], &nd, &nb, 0.75)
			}
		})
	}
}
