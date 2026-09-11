package quality

import "math"

// alignSamples bounds how many reference samples AlignLag correlates over:
// plenty for codec delays, cheap enough for a few-thousand-lag search.
const alignSamples = 65536

// AlignLag returns the lag, in [minLag, maxLag], that maximizes the
// normalized cross-correlation between ref and deg, where a positive lag
// means deg is delayed relative to ref (deg[i+lag] tracks ref[i]). It
// returns 0 when nothing correlates positively, so a silent or unrelated
// pair is left unshifted rather than trimmed at an arbitrary lag.
//
// The correlation is normalized by the energy of BOTH windows, not just by
// their length. That is what makes the result trustworthy on a periodic
// program: a bare sum, or a mean product, is not bounded by the aligned
// value, so a lag one signal period away whose own window happens to carry
// more energy can outscore the true delay. The harness measured a 440 Hz
// program at 3262 samples instead of 1057, exactly 22 periods late, and then
// scored every metric on misaligned audio and reported it as valid. Cauchy
// and Schwarz bound the normalized form by 1, reached only at true alignment.
//
// The correlation runs over the first alignSamples reference samples (or
// fewer when the inputs are shorter). Lags whose overlap with deg is under
// half that window are skipped: a short overlap leaves too few samples for
// the ratio to mean anything, and it can reach 1 by coincidence.
func AlignLag(ref, deg []float64, minLag, maxLag int) int {
	refN := min(len(ref), alignSamples)
	if refN == 0 || len(deg) == 0 {
		return 0
	}
	minOverlap := min(refN, len(deg)) / 2
	if minOverlap == 0 {
		return 0
	}

	// Prefix sums of the squares, so each lag's two window energies are O(1)
	// instead of O(n): without them the normalization would triple the cost
	// of a several-thousand-lag search.
	refSq := make([]float64, refN+1)
	for i := range refN {
		refSq[i+1] = refSq[i] + ref[i]*ref[i]
	}
	degSq := make([]float64, len(deg)+1)
	for i, v := range deg {
		degSq[i+1] = degSq[i] + v*v
	}

	bestLag, bestC := 0, 0.0
	for lag := minLag; lag <= maxLag; lag++ {
		lo := max(0, -lag)
		hi := min(refN, len(deg)-lag)
		if hi-lo < minOverlap {
			continue
		}
		energy := (refSq[hi] - refSq[lo]) * (degSq[hi+lag] - degSq[lo+lag])
		if energy <= 0 {
			continue // one window is silent: the ratio is undefined
		}
		var c float64
		for i := lo; i < hi; i++ {
			c += ref[i] * deg[i+lag]
		}
		if c /= math.Sqrt(energy); c > bestC {
			bestC, bestLag = c, lag
		}
	}
	return bestLag // 0 when nothing scored a positive correlation
}
