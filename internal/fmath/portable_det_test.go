// SPDX-License-Identifier: LGPL-2.1-or-later

package fmath

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"
)

// detCorpus feeds each vendored transcendental a deterministic sweep of inputs
// (no FMA in the generator: division and addition only) covering the encoder's
// operating range plus specials, and folds the raw bits of every output into
// one hash. Because the vendored implementations are fully barriered, this hash
// MUST be identical on every architecture, so CI running it on both arm64 and
// amd64 catches any future divergence: an accidental edit or a compiler
// regression moves the hash on either arch, while a lost float64 barrier moves
// it only on arm64 (amd64 does not fuse at GOAMD64=v1), so the arm64 job is what
// catches that. The constants below were produced by running this test and are
// cross-checked on arm64 and amd64.
func detCorpus(f func(float64) float64) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	add := func(v float64) {
		// Canonicalize NaN: a computed NaN (for example atan(NaN)) can carry an
		// arch-dependent payload or sign, which would make this hash diverge
		// spuriously. Only finite outputs need to match bit for bit.
		if math.IsNaN(v) {
			v = math.NaN()
		}
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
		h.Write(buf[:])
	}
	for i := range 300000 {
		x := float64(i)/5000.0 - 30.0 // ~[-30, 30), division then add: no FMA
		add(f(x))
	}
	for _, s := range []float64{0, math.Copysign(0, -1), 1, -1, 0.5, math.SmallestNonzeroFloat64,
		math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		add(f(s))
	}
	return h.Sum64()
}

func detCorpus2(f func(float64, float64) float64, y float64) uint64 {
	return detCorpus(func(x float64) float64 { return f(x, y) })
}

// TestPortableDeterminismGolden pins the arch-independent output hash of every
// vendored transcendental. If this fails on one arch it is an edit bug; if it
// disagrees between arm64 and amd64 the vendored code lost architecture
// determinism (the whole point of the #79 transcendental vendoring).
func TestPortableDeterminismGolden(t *testing.T) {
	cases := []struct {
		name string
		got  uint64
		want uint64
	}{
		{"atan", detCorpus(atan), 0x0b94458d2b35c011},
		{"sin", detCorpus(sin), 0x20af00b08c7fdd27},
		{"cos", detCorpus(cos), 0x3610fc4a3ad37262},
		{"exp", detCorpus(exp), 0x02900f719d39bb97},
		{"exp2", detCorpus(exp2), 0x49b65e9699505d06},
		{"log", detCorpus(log), 0x7e862ed949dd7299},
		{"log2", detCorpus(log2), 0x3d0b71fe2ecba274},
		{"pow0.75", detCorpus2(pow, 0.75), 0x0907f5e1cbeb97a3},
		{"pow-1.3", detCorpus2(pow, -1.3), 0x829d4aa66efff12e},
		// Integer exponents drive the barriered squaring loop through multiple
		// iterations (yi=3 runs it twice, yi=10 four times), which the
		// fractional exponents above (yi=1, loop dead) never reach.
		{"pow3", detCorpus2(pow, 3), 0x85a1e375f7bf1a76},
		{"pow10", detCorpus2(pow, 10), 0xa335f2ef0c2bdee8},
	}
	for _, c := range cases {
		t.Logf("%-8s %#016x", c.name, c.got)
	}
	for _, c := range cases {
		if c.want == 0 {
			t.Errorf("%s has no pinned golden hash (got %#016x); every vendored transcendental must be pinned so a lost barrier cannot pass silently", c.name, c.got)
			continue
		}
		if c.got != c.want {
			t.Errorf("%s golden hash = %#016x, want %#016x (arch determinism lost)", c.name, c.got, c.want)
		}
	}
}
