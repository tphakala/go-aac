// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

// nmrTrellisScratch is per-encoder scratch for the goaac_simd trellis kernel
// (internal/coder/nmr_trellis_simd.go). It is sized for the worst case after
// the offset window is clipped to what any valid (o, op) pair can reach (see
// nmrTrellisGeom): the shared kernel spans at most 2*NMRNCand-1 lamsf entries
// and each +Inf pad is at most NMRNCand-1 wide, so the padded signal fits in
// 3*NMRNCand-2. The zero value is valid: the wrapper writes every cell it reads
// within a single call, so the struct-zeroing reset of NMRState
// (internal/enc/encoder.go) cannot leave a stale 0 that would win an argmin.
// The default (scalar) build never touches these fields.
type nmrTrellisScratch struct {
	vals [NMRNCand]float32       // per-row argmin cost from MinIdxOfSumRows
	idxs [NMRNCand]int32         // per-row argmin index from MinIdxOfSumRows
	kern [2*NMRNCand - 1]float32 // materialised transition kernel a[i]
	padk [3*NMRNCand - 2]float32 // +Inf-padded dpp signal (the k argument)
}

// trellisGeom is the clipped window geometry the goaac_simd trellis wrapper
// maps onto f32.MinIdxOfSumRows.
type trellisGeom struct {
	dLo, dHi          int // clipped offset range, d = o - op in [dLo, dHi]
	kernLen           int // kernel length, dHi - dLo + 1
	padFront, padBack int // count of +Inf pad entries before/after dpp in k
	basep             int // the base argument passed to f32.MinIdxOfSumRows
}

// nmrTrellisGeom returns the clipped Viterbi window geometry the goaac_simd
// trellis wrapper maps onto f32.MinIdxOfSumRows, for step > 0 only (the wrapper
// delegates step <= 0 to the scalar kernel). It reproduces
// nmrTrellisStepScalar's per-call offset bounds, then clips them to
// [-(nPrev-1), nCur-1], the widest d = o-op any valid (o, op) pair can produce.
// Clipping drops only offsets no row could use (proved in the plan and pinned
// by the geometry test), so it changes no result while bounding the kernel by
// 2*NMRNCand-1 and each pad by NMRNCand-1. kernLen <= 0 means no row has a
// genuine candidate; every row then takes the MaxFloat32 sentinel path.
func nmrTrellisGeom(nCur, nPrev, base, step, mdiff int) trellisGeom {
	loOff := ceilDiv(-mdiff-base, step)
	hiOff := floorDiv(mdiff-base, step)
	dHi := min(hiOff, nCur-1)
	dLo := max(loOff, -(nPrev - 1))
	padFront := max(0, dHi)
	return trellisGeom{
		dLo:      dLo,
		dHi:      dHi,
		kernLen:  dHi - dLo + 1,
		padFront: padFront,
		padBack:  max(0, nCur-nPrev-dLo),
		basep:    padFront - dHi,
	}
}
