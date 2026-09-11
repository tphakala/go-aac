package quality

import "math"

// hannWindow returns the periodic Hann window of length n:
// w[i] = 0.5 - 0.5*cos(2*pi*i/n).
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

// fft computes the in-place forward radix-2 complex FFT of (re, im), whose
// length must be a power of two. Both production callers (stftFrames and
// compareSpectra) pass the fixed stftSize, so there is no padding step. No
// scaling is applied:
// X[k] = sum_i x[i] * exp(-2*pi*j*k*i/N).
func fft(re, im []float64) {
	n := len(re)
	if n != len(im) || n == 0 || n&(n-1) != 0 {
		panic("quality: fft length must be a power of two and re/im equal length")
	}
	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	// Iterative Cooley-Tukey butterflies. The twiddle loop is the OUTER of the
	// two, so each factor is reused across every block of its stage. For the
	// fixed stftSize, the only length stftFrames and compareSpectra transform,
	// the factors are read from the precomputed stftTwiddles table (the cos/sin
	// calls dominate the FFT cost); any other length computes them inline. The
	// factors, the butterflies, and their operand order are identical either
	// way, so the output is bit-for-bit the same.
	useTable := n == stftSize
	for stage, size := 0, 2; size <= n; stage, size = stage+1, size<<1 {
		half := size >> 1
		for k := range half {
			var wr, wi float64
			if useTable {
				t := stftTwiddles[stage][k]
				wr, wi = t.re, t.im
			} else {
				step := -2 * math.Pi / float64(size)
				wr, wi = math.Cos(step*float64(k)), math.Sin(step*float64(k))
			}
			for start := 0; start < n; start += size {
				a, b := start+k, start+k+half
				tr := re[b]*wr - im[b]*wi
				ti := re[b]*wi + im[b]*wr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}

// twiddle is one FFT twiddle factor, the complex root cos(theta) + i*sin(theta).
type twiddle struct{ re, im float64 }

// stftTwiddles caches the per-stage twiddle factors for the fixed stftSize,
// computed once at init. Each factor uses the exact same expression fft's
// inline path uses, so a transform of that length reads bit-identical factors
// from the table instead of recomputing thousands of cos/sin per call.
var stftTwiddles = computeTwiddles(stftSize)

// computeTwiddles returns the Cooley-Tukey twiddle factors for a transform of
// length n, one slice per stage (lengths 2, 4, ... n), laid out to match fft's
// stage loop so stage s and index k address stftTwiddles[s][k] directly.
func computeTwiddles(n int) [][]twiddle {
	var stages [][]twiddle
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := -2 * math.Pi / float64(size)
		st := make([]twiddle, half)
		for k := range half {
			st[k] = twiddle{math.Cos(step * float64(k)), math.Sin(step * float64(k))}
		}
		stages = append(stages, st)
	}
	return stages
}
