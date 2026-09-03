// SPDX-License-Identifier: BSD-3-Clause
// Copyright 2009, 2018 The Go Authors. All rights reserved.
//
// Derived from Go 1.27.0 GOROOT/src/math/sin.go and trig_reduce.go. Vendored so
// the encoder's sine/cosine are architecture-deterministic: math.Sin/Cos are
// portable Go on both arm64 and amd64 but gc contracts their polynomials into
// FMADDD on arm64, so the same input yields different last-ulp results per arch
// (issues #59 and #79). The explicit float64(...) conversions below are rounding
// barriers forbidding contraction; a barriered copy equals amd64's stdlib
// Sin/Cos bit-for-bit, so amd64 output is unchanged and arm64 converges to it.
// trigReduce is exact integer arithmetic (its stdlib shift/mask/bias constants
// are renamed to the package-local fshift/fmask/fbias), so it needs no barrier.
// See PROVENANCE.md.
//
// The original C code and constants are from netlib's sin/cos (FreeBSD msun);
// trigReduce implements Payne-Hanek reduction (Ng et al, 1992).

package fmath

import (
	"math"
	"math/bits"
)

// sin coefficients.
var _sin = [...]float64{
	1.58962301576546568060e-10, // 0x3de5d8fd1fd19ccd
	-2.50507477628578072866e-8, // 0xbe5ae5e5a9291f5d
	2.75573136213857245213e-6,  // 0x3ec71de3567d48a1
	-1.98412698295895385996e-4, // 0xbf2a01a019bfdf03
	8.33333333332211858878e-3,  // 0x3f8111111110f7d0
	-1.66666666666666307295e-1, // 0xbfc5555555555548
}

// cos coefficients.
var _cos = [...]float64{
	-1.13585365213876817300e-11, // 0xbda8fa49a0861a9b
	2.08757008419747316778e-9,   // 0x3e21ee9d7b4e3f05
	-2.75573141792967388112e-7,  // 0xbe927e4f7eac4bc6
	2.48015872888517045348e-5,   // 0x3efa01a019c844f5
	-1.38888888888730564116e-3,  // 0xbf56c16c16c14f91
	4.16666666666665929218e-2,   // 0x3fa555555555554b
}

const (
	mPi          = 3.14159265358979311600e+00 // math.Pi
	reduceThresh = 1 << 29
	// IEEE-754 float64 layout constants (math package internals).
	fshift = 52
	fmask  = 0x7FF
	fbias  = 1023
)

// sinPoly / cosPoly evaluate the Chebyshev polynomials with per-op rounding so
// gc cannot contract them into FMAs (no cross-statement FMA).
func sinPoly(z, zz float64) float64 {
	s := float64(_sin[0]*zz) + _sin[1]
	s = float64(s*zz) + _sin[2]
	s = float64(s*zz) + _sin[3]
	s = float64(s*zz) + _sin[4]
	s = float64(s*zz) + _sin[5]
	return z + float64(z*zz*s)
}

func cosPoly(zz float64) float64 {
	c := float64(_cos[0]*zz) + _cos[1]
	c = float64(c*zz) + _cos[2]
	c = float64(c*zz) + _cos[3]
	c = float64(c*zz) + _cos[4]
	c = float64(c*zz) + _cos[5]
	return 1.0 - float64(0.5*zz) + float64(zz*zz*c)
}

// octantReduce reduces positive x into octant j (mod 8) and z in [0, Pi/4],
// barriering the extended-precision products.
func octantReduce(x float64) (j uint64, z float64) {
	const (
		PI4A = 7.85398125648498535156e-1  // Pi/4 split into three parts
		PI4B = 3.77489470793079817668e-8  //
		PI4C = 2.69515142907905952645e-15 //
	)
	if x >= reduceThresh {
		return trigReduce(x)
	}
	j = uint64(x * (4 / mPi))
	y := float64(j)
	if j&1 == 1 {
		j++
		y++
	}
	j &= 7
	// ((x - y*PI4A) - y*PI4B) - y*PI4C, each product barriered. // no cross-statement FMA
	z = ((x - float64(y*PI4A)) - float64(y*PI4B)) - float64(y*PI4C)
	return j, z
}

// sin returns the sine of the radian argument x. Barriered copy of math.sin.
//
//go:noinline
func sin(x float64) float64 {
	switch {
	case x == 0 || math.IsNaN(x):
		return x // return +-0 || NaN()
	case math.IsInf(x, 0):
		return math.NaN()
	}

	sign := false
	if x < 0 {
		x = -x
		sign = true
	}

	j, z := octantReduce(x)
	if j > 3 {
		sign = !sign
		j -= 4
	}
	zz := z * z
	var y float64
	if j == 1 || j == 2 {
		y = cosPoly(zz)
	} else {
		y = sinPoly(z, zz)
	}
	if sign {
		y = -y
	}
	return y
}

// cos returns the cosine of the radian argument x. Barriered copy of math.cos.
//
//go:noinline
func cos(x float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsInf(x, 0):
		return math.NaN()
	}

	sign := false
	if x < 0 {
		x = -x
	}

	j, z := octantReduce(x)
	if j > 3 {
		j -= 4
		sign = !sign
	}
	if j > 1 {
		sign = !sign
	}
	zz := z * z
	var y float64
	if j == 1 || j == 2 {
		y = sinPoly(z, zz)
	} else {
		y = cosPoly(zz)
	}
	if sign {
		y = -y
	}
	return y
}

// trigReduce implements Payne-Hanek range reduction by Pi/4 for x >= Pi/4.
// Exact integer arithmetic, copied verbatim from Go's math.trigReduce, so it is
// architecture-deterministic without barriers.
func trigReduce(x float64) (j uint64, z float64) {
	const PI4 = mPi / 4
	if x < PI4 {
		return 0, x
	}
	ix := math.Float64bits(x)
	exp := int(ix>>fshift&fmask) - fbias - fshift
	ix &^= fmask << fshift
	ix |= 1 << fshift
	digit, bitshift := uint(exp+61)/64, uint(exp+61)%64
	z0 := (mPi4[digit] << bitshift) | (mPi4[digit+1] >> (64 - bitshift))
	z1 := (mPi4[digit+1] << bitshift) | (mPi4[digit+2] >> (64 - bitshift))
	z2 := (mPi4[digit+2] << bitshift) | (mPi4[digit+3] >> (64 - bitshift))
	z2hi, _ := bits.Mul64(z2, ix)
	z1hi, z1lo := bits.Mul64(z1, ix)
	z0lo := z0 * ix
	lo, c := bits.Add64(z1lo, z2hi, 0)
	hi, _ := bits.Add64(z0lo, z1hi, c)
	j = hi >> 61
	hi = hi<<3 | lo>>61
	lz := uint(bits.LeadingZeros64(hi))
	e := uint64(fbias - (lz + 1))
	hi = (hi << (lz + 1)) | (lo >> (64 - (lz + 1)))
	hi >>= 64 - fshift
	hi |= e << fshift
	z = math.Float64frombits(hi)
	if j&1 == 1 {
		j++
		j &= 7
		z--
	}
	return j, z * PI4
}

// mPi4 is the binary digits of 4/pi as a uint64 array (4/pi = Sum mPi4[i]*2^(-64*i)).
var mPi4 = [...]uint64{
	0x0000000000000001,
	0x45f306dc9c882a53,
	0xf84eafa3ea69bb81,
	0xb6c52b3278872083,
	0xfca2c757bd778ac3,
	0x6e48dc74849ba5c0,
	0x0c925dd413a32439,
	0xfc3bd63962534e7d,
	0xd1046bea5d768909,
	0xd338e04d68befc82,
	0x7323ac7306a673e9,
	0x3908bf177bf25076,
	0x3ff12fffbc0b301f,
	0xde5e2316b414da3e,
	0xda6cfd9e4f96136e,
	0x9e8c7ecd3cbfd45a,
	0xea4f758fd7cbe2f6,
	0x7a0e73ef14a525d4,
	0xd7f6bf623f1aba10,
	0xac06608df8f6d757,
}
