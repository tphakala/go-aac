// SPDX-License-Identifier: LGPL-2.1-or-later

package psy

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/tphakala/go-aac/internal/tables"
)

// This file gates the dispatched psyHpFilter bitwise against psyHpFilterScalar.
// It carries no build tag: under -tags noasm psyHpFilter is the scalar itself
// and the comparison is trivially true, so the default and SIMD_DISABLE lanes
// are the ones that test the SIMD composition. All inputs are finite and no sum
// overflows: the encoder rejects non-finite PCM, and NaN payload bits are not
// part of the contract.

// hpFirbufLen is the firbuf length psyHpFilter requires.
const hpFirbufLen = blockSizeLong + psyLameFirLen + 13

var negZero = float32(math.Copysign(0, -1))

// hpPins are the edge inputs pinned in the low indices of the seeded firbufs:
// signed zeros, denormals, power-of-two straddles and the largest magnitudes
// whose filtered and scaled sums stay finite.
var hpPins = []float32{
	0, negZero,
	1, -1, 0.5, -0.5,
	math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
	1e-38, -1e-38,
	0.9999999, 1.0000001,
	1e30, -1e30,
}

// buildHpFirbuf returns a firbuf with hpPins at the start and a seeded finite
// spread over 2^-50..2^49 and both signs filling the rest.
func buildHpFirbuf(rng *rand.Rand) []float32 {
	buf := make([]float32, hpFirbufLen)
	for i := range buf {
		if i < len(hpPins) {
			buf[i] = hpPins[i]
		} else {
			buf[i] = float32((rng.Float64()*2 - 1) * math.Ldexp(1, rng.Intn(100)-50))
		}
	}
	return buf
}

// hpSignedZeroOffsets are the firbuf offsets, relative to output index k, of
// the taps that the signed-zero case sets to -0: the centre tap k+10 and the
// odd-tap pairs (k+3, k+18) and (k+7, k+14). Output k then sums only zeros, and
// its sign depends on whether sum2 starts at +0 (lame.go) or at its first
// product.
var hpSignedZeroOffsets = []int{10, 3, 18, 7, 14}

// buildHpSignedZero returns an all +0 firbuf with the signed-zero pattern
// anchored at output index k. Each case needs its own buffer: two shifted
// patterns in one buffer can turn a +0 tap pair into -0 + -0 and destroy the
// discriminator.
func buildHpSignedZero(k int) []float32 {
	buf := make([]float32, hpFirbufLen)
	for _, o := range hpSignedZeroOffsets {
		buf[k+o] = negZero
	}
	return buf
}

// hpSignedZeroIndices are the output indices the signed-zero case is anchored
// at: the first lane, lanes inside the first vector, the first lane of the
// second vector, and the last lane of the final full vector.
var hpSignedZeroIndices = []int{0, 1, 7, 8, blockSizeLong - 1}

// hpPCMBuffer returns a PCM-range buffer like the encoder's lookahead.
func hpPCMBuffer() []float32 {
	rng := rand.New(rand.NewSource(99))
	pcm := make([]float32, hpFirbufLen)
	for i := range pcm {
		pcm[i] = rng.Float32()*2 - 1
	}
	return pcm
}

// hpCorpus is every input TestHpFilterMatchesScalar compares and every input
// the mutant check may use to tell a wrong composition from the scalar.
func hpCorpus() map[string][]float32 {
	c := map[string][]float32{"pcm": hpPCMBuffer()}
	for seed := int64(1); seed <= 20; seed++ {
		c[fmt.Sprintf("seeded%d", seed)] = buildHpFirbuf(rand.New(rand.NewSource(seed)))
	}
	for _, k := range hpSignedZeroIndices {
		c[fmt.Sprintf("signedzero%d", k)] = buildHpSignedZero(k)
	}
	return c
}

// checkHpFilter asserts the dispatched psyHpFilter leaves firbuf unchanged and
// matches psyHpFilterScalar bit for bit on every output sample.
func checkHpFilter(t *testing.T, firbuf []float32) {
	t.Helper()
	before := slices.Clone(firbuf)
	var got, want [blockSizeLong]float32
	psyHpFilter(firbuf, &got)
	for i := range firbuf {
		if math.Float32bits(firbuf[i]) != math.Float32bits(before[i]) {
			t.Fatalf("psyHpFilter mutated firbuf[%d]: %#x -> %#x", i, math.Float32bits(before[i]), math.Float32bits(firbuf[i]))
		}
	}
	psyHpFilterScalar(firbuf, &want)
	for i := range blockSizeLong {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample %d: got %v (%#x), want %v (%#x)", i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
		}
	}
}

// TestHpFilterMatchesScalar compares the dispatched filter with the scalar on
// the seeded, PCM-range and signed-zero corpus.
func TestHpFilterMatchesScalar(t *testing.T) {
	for name, buf := range hpCorpus() {
		t.Run(name, func(t *testing.T) { checkHpFilter(t, buf) })
	}
	// The signed-zero case must really sit on the +0 side of the scalar,
	// otherwise it discriminates nothing.
	for _, k := range hpSignedZeroIndices {
		var want [blockSizeLong]float32
		psyHpFilterScalar(buildHpSignedZero(k), &want)
		if bits := math.Float32bits(want[k]); bits != 0 {
			t.Fatalf("signed-zero case k=%d: scalar output bits %#x, want 0", k, bits)
		}
	}
}

// hpMutants are the realistic mistakes in composing the filter from ten
// passes, each written in plain Go in the scalar's shape. A change to the
// corpus that stops distinguishing them from psyHpFilterScalar would let the
// same mistake through the dispatched filter unnoticed.
var hpMutants = map[string]func(firbuf []float32, out *[blockSizeLong]float32){
	// one accumulator for both tap parities
	"merged accumulators": func(fb []float32, out *[blockSizeLong]float32) {
		for i := range blockSizeLong {
			sum := fb[i+(psyLameFirLen-1)/2]
			for j := 0; j < ((psyLameFirLen-1)/2)-1; j += 2 {
				sum += float32(tables.PsyFirCoeffs[j] * (fb[i+j] + fb[i+psyLameFirLen-j]))
				sum += float32(tables.PsyFirCoeffs[j+1] * (fb[i+j+1] + fb[i+psyLameFirLen-j-1]))
			}
			out[i] = sum * 32768.0
		}
	},
	// taps accumulated in descending j
	"descending taps": func(fb []float32, out *[blockSizeLong]float32) {
		for i := range blockSizeLong {
			sum1 := fb[i+(psyLameFirLen-1)/2]
			var sum2 float32
			for j := ((psyLameFirLen - 1) / 2) - 2; j >= 0; j -= 2 {
				sum1 += float32(tables.PsyFirCoeffs[j] * (fb[i+j] + fb[i+psyLameFirLen-j]))
				sum2 += float32(tables.PsyFirCoeffs[j+1] * (fb[i+j+1] + fb[i+psyLameFirLen-j-1]))
			}
			out[i] = (sum1 + sum2) * 32768.0
		}
	},
	// sum2 seeded with its first product instead of +0
	"product-seeded sum2": func(fb []float32, out *[blockSizeLong]float32) {
		for i := range blockSizeLong {
			sum1 := fb[i+(psyLameFirLen-1)/2]
			sum2 := float32(tables.PsyFirCoeffs[1] * (fb[i+1] + fb[i+psyLameFirLen-1]))
			sum1 += float32(tables.PsyFirCoeffs[0] * (fb[i] + fb[i+psyLameFirLen]))
			for j := 2; j < ((psyLameFirLen-1)/2)-1; j += 2 {
				sum1 += float32(tables.PsyFirCoeffs[j] * (fb[i+j] + fb[i+psyLameFirLen-j]))
				sum2 += float32(tables.PsyFirCoeffs[j+1] * (fb[i+j+1] + fb[i+psyLameFirLen-j-1]))
			}
			out[i] = (sum1 + sum2) * 32768.0
		}
	},
	// multiply-add fused into one rounding
	"fused multiply-add": func(fb []float32, out *[blockSizeLong]float32) {
		fma := func(c, x, acc float32) float32 {
			return float32(math.FMA(float64(c), float64(x), float64(acc)))
		}
		for i := range blockSizeLong {
			sum1 := fb[i+(psyLameFirLen-1)/2]
			var sum2 float32
			for j := 0; j < ((psyLameFirLen-1)/2)-1; j += 2 {
				sum1 = fma(tables.PsyFirCoeffs[j], fb[i+j]+fb[i+psyLameFirLen-j], sum1)
				sum2 = fma(tables.PsyFirCoeffs[j+1], fb[i+j+1]+fb[i+psyLameFirLen-j-1], sum2)
			}
			out[i] = (sum1 + sum2) * 32768.0
		}
	},
}

// TestHpFilterCorpusSeparatesMutants is the sensitivity check of the corpus
// above: every mutant must differ from psyHpFilterScalar on at least one output
// of at least one corpus buffer, and the product-seeded sum2 must produce -0
// where the scalar gives +0 at every anchor of the signed-zero case (random
// PCM never shows that difference, so only the crafted buffers catch it).
func TestHpFilterCorpusSeparatesMutants(t *testing.T) {
	corpus := hpCorpus()
	for name, mutant := range hpMutants {
		t.Run(name, func(t *testing.T) {
			for _, buf := range corpus {
				var got, want [blockSizeLong]float32
				mutant(buf, &got)
				psyHpFilterScalar(buf, &want)
				if !hpEqualBits(&got, &want) {
					return
				}
			}
			t.Fatalf("no corpus buffer distinguishes the %q mutant from psyHpFilterScalar", name)
		})
	}
	for _, k := range hpSignedZeroIndices {
		var got [blockSizeLong]float32
		hpMutants["product-seeded sum2"](buildHpSignedZero(k), &got)
		if bits := math.Float32bits(got[k]); bits != 0x80000000 {
			t.Fatalf("product-seeded sum2 at k=%d: output bits %#x, want 0x80000000", k, bits)
		}
	}
}

func hpEqualBits(a, b *[blockSizeLong]float32) bool {
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

// hpFuzzMax is the largest sample magnitude FuzzHpFilterEquiv keeps, the 1e30
// of hpPins whose filtered and scaled sums stay finite.
const hpFuzzMax = 1e30

// hpFuzzSeed encodes firbuf samples as the little-endian float32 bytes
// FuzzHpFilterEquiv reads.
func hpFuzzSeed(samples []float32) []byte {
	b := make([]byte, 4*len(samples))
	for i, v := range samples {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	return b
}

// FuzzHpFilterEquiv reads the fuzz input as little-endian float32 firbuf
// samples (missing samples are zero, and a non-finite sample or one above
// hpFuzzMax in magnitude reads as zero) and asserts the dispatched psyHpFilter
// stays bit-identical to psyHpFilterScalar on it. The seeds are an all-zero
// buffer, an impulse, alternating signs, subnormal magnitudes, the signed-zero
// buffers and seeded wide-range buffers.
func FuzzHpFilterEquiv(f *testing.F) {
	zero := make([]float32, hpFirbufLen)
	impulse := make([]float32, hpFirbufLen)
	impulse[hpFirbufLen/2] = 1
	alternating := make([]float32, hpFirbufLen)
	small := make([]float32, hpFirbufLen)
	for i := range hpFirbufLen {
		alternating[i] = float32(1 - 2*(i&1))
		small[i] = float32(i%7-3) * math.SmallestNonzeroFloat32
	}
	for _, s := range [][]float32{zero, impulse, alternating, small} {
		f.Add(hpFuzzSeed(s))
	}
	for _, k := range hpSignedZeroIndices {
		f.Add(hpFuzzSeed(buildHpSignedZero(k)))
	}
	for seed := int64(1); seed <= 3; seed++ {
		f.Add(hpFuzzSeed(buildHpFirbuf(rand.New(rand.NewSource(seed)))))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		firbuf := make([]float32, hpFirbufLen)
		for i := range min(hpFirbufLen, len(data)/4) {
			v := math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
			if math.Abs(float64(v)) <= hpFuzzMax {
				firbuf[i] = v
			}
		}
		checkHpFilter(t, firbuf)
	})
}
