package quality

import (
	"math"
	"testing"
)

// TestFFTSingleBin checks a pure cosine at bin k lands its energy in bins k
// and N-k only, with magnitude N/2 each.
func TestFFTSingleBin(t *testing.T) {
	const n, k = 64, 5
	re := make([]float64, n)
	im := make([]float64, n)
	for i := range re {
		re[i] = math.Cos(2 * math.Pi * float64(k*i) / n)
	}
	fft(re, im)
	for b := range n {
		mag := math.Hypot(re[b], im[b])
		want := 0.0
		if b == k || b == n-k {
			want = n / 2
		}
		if math.Abs(mag-want) > 1e-9 {
			t.Fatalf("bin %d: |X| = %v, want %v", b, mag, want)
		}
	}
}

// TestFFTMatchesDFT compares the radix-2 FFT against a direct O(N^2) DFT on a
// pseudo-random input.
func TestFFTMatchesDFT(t *testing.T) {
	const n = 128
	re := make([]float64, n)
	im := make([]float64, n)
	copy(re, genNoise(n, 1, 12345))
	wantRe := make([]float64, n)
	wantIm := make([]float64, n)
	for k := range n {
		for i := range n {
			a := -2 * math.Pi * float64(k*i) / n
			wantRe[k] += re[i] * math.Cos(a)
			wantIm[k] += re[i] * math.Sin(a)
		}
	}
	fft(re, im)
	for k := range n {
		if math.Abs(re[k]-wantRe[k]) > 1e-9 || math.Abs(im[k]-wantIm[k]) > 1e-9 {
			t.Fatalf("bin %d: got (%v, %v), want (%v, %v)", k, re[k], im[k], wantRe[k], wantIm[k])
		}
	}
}

func TestFFTRejectsNonPowerOfTwo(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("fft on length 6 must panic")
		}
	}()
	fft(make([]float64, 6), make([]float64, 6))
}

func TestHannWindowEndpoints(t *testing.T) {
	w := hannWindow(8)
	if w[0] != 0 {
		t.Fatalf("w[0] = %v, want 0 (periodic Hann)", w[0])
	}
	if math.Abs(w[4]-1) > 1e-12 {
		t.Fatalf("w[n/2] = %v, want 1", w[4])
	}
	// Periodic Hann energy: sum of w^2 over a period is 3N/8 exactly.
	var e float64
	for _, v := range w {
		e += v * v
	}
	if math.Abs(e-3.0*8/8) > 1e-12 {
		t.Fatalf("sum w^2 = %v, want 3N/8 = 3", e)
	}
}

// TestFFTRejectsMismatchedLengths covers the other two arms of fft's guard,
// which the non-power-of-two case leaves untouched.
func TestFFTRejectsMismatchedLengths(t *testing.T) {
	for _, c := range []struct{ nRe, nIm int }{{8, 4}, {0, 0}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("fft(%d, %d) must panic", c.nRe, c.nIm)
				}
			}()
			fft(make([]float64, c.nRe), make([]float64, c.nIm))
		}()
	}
}

// TestStftTwiddlesMatchInline pins that the precomputed stftTwiddles are
// bit-for-bit the cos/sin values fft's inline path computes for the same stage
// and index. That equality is what makes the table-driven transform of
// stftSize produce identical output to the inline one.
func TestStftTwiddlesMatchInline(t *testing.T) {
	for stage, size := 0, 2; size <= stftSize; stage, size = stage+1, size<<1 {
		half := size >> 1
		if got := len(stftTwiddles[stage]); got != half {
			t.Fatalf("stage %d (size %d): table has %d factors, want %d", stage, size, got, half)
		}
		step := -2 * math.Pi / float64(size)
		for k := range half {
			wantRe, wantIm := math.Cos(step*float64(k)), math.Sin(step*float64(k))
			if got := stftTwiddles[stage][k]; got.re != wantRe || got.im != wantIm {
				t.Fatalf("stage %d k %d: table (%v, %v), inline (%v, %v)", stage, k, got.re, got.im, wantRe, wantIm)
			}
		}
	}
}
