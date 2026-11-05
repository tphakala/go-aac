// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !noasm

package psy

import (
	"encoding/binary"
	"math"
	"math/rand"
	"slices"
	"testing"
)

// This file gates the SIMD high-pass filter bitwise against psyHpFilterScalar.
// It is tagged !noasm because under -tags noasm psyHpFilter is the scalar loop
// itself. All inputs are finite and no sum overflows: the encoder rejects
// non-finite PCM (#18), and NaN payload bits are not guaranteed identical
// between the scalar and the vector forms.

// hpFirbufLen is the firbuf length psyHpFilter requires.
const hpFirbufLen = blockSizeLong + psyLameFirLen + 13

// hpPins are the edge inputs pinned in the low indices of every firbuf: signed
// zeros, denormals, power-of-two straddles and the largest magnitudes whose
// filtered and scaled sums stay finite.
var hpPins = []float32{
	0, float32(math.Copysign(0, -1)),
	1, -1, 0.5, -0.5,
	float32(math.SmallestNonzeroFloat32), -float32(math.SmallestNonzeroFloat32),
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

// TestHpFilterKernelMatchesScalar checks seeded wide-range buffers and a
// PCM-range buffer like the encoder's lookahead.
func TestHpFilterKernelMatchesScalar(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		checkHpFilter(t, buildHpFirbuf(rand.New(rand.NewSource(seed))))
	}
	rng := rand.New(rand.NewSource(99))
	pcm := make([]float32, hpFirbufLen)
	for i := range pcm {
		pcm[i] = rng.Float32()*2 - 1
	}
	checkHpFilter(t, pcm)
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
// buffer, an impulse, alternating signs, subnormal magnitudes and seeded
// wide-range buffers.
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
