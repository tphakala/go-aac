// SPDX-License-Identifier: LGPL-2.1-or-later

package coder

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/tables"
)

// fixture reader helpers

type fxr struct {
	t   *testing.T
	buf []byte
	off int
}

func openFixture(t *testing.T, name string) *fxr {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return &fxr{t: t, buf: b}
}

func (f *fxr) i32() int32 {
	v := int32(binary.LittleEndian.Uint32(f.buf[f.off:]))
	f.off += 4
	return v
}

func (f *fxr) f32() float32 {
	v := math.Float32frombits(binary.LittleEndian.Uint32(f.buf[f.off:]))
	f.off += 4
	return v
}

func (f *fxr) u8() uint8 {
	v := f.buf[f.off]
	f.off++
	return v
}

func (f *fxr) done() {
	if f.off != len(f.buf) {
		f.t.Fatalf("fixture: %d bytes left (off %d of %d)", len(f.buf)-f.off, f.off, len(f.buf))
	}
}

// cnmrLCG mirrors the harness LCG.
type cnmrLCG uint32

func (l *cnmrLCG) next() uint32 {
	*l = cnmrLCG(uint32(*l)*1664525 + 1013904223)
	return uint32(*l)
}

func (l *cnmrLCG) f() float32 {
	return float32(l.next()>>8) / 16777216.0
}

func TestNMRSolveAgainstC(t *testing.T) {
	fx := openFixture(t, "nmr_trellis.bin")
	lcg := cnmrLCG(0x1f2e3d4c)
	var c Coder
	var st NMRState
	var blo, bnc, act, chosen [128]int

	// case 0: crafted all-equal costs; pure tie-breaking
	{
		nact := 4
		for b := range nact {
			blo[b] = 100
			bnc[b] = 5
			act[b] = b
			for o := range 5 {
				st.Nd[b][o] = 1.0
				st.Nb[b][o] = 40
			}
		}
		for i := range chosen {
			chosen[i] = -1
		}
		lam := c.nmrSolve(&st, blo[:], bnc[:], 1, act[:], nact, 200, chosen[:],
			1e-9, 1e4, nmrIters)
		if want := fx.f32(); lam != want {
			t.Errorf("case 0: lam = %v, want %v", lam, want)
		}
		for b := range nact {
			if want := int(fx.i32()); chosen[b] != want {
				t.Errorf("case 0: chosen[%d] = %d, want %d", b, chosen[b], want)
			}
		}
	}

	for cs := 1; cs <= 24; cs++ {
		nact := 1
		if cs%5 != 0 {
			nact = 2 + int(lcg.next()%45)
		}
		step := 8
		if cs&1 == 1 {
			step = 1
		}
		prevlo := 60 + int(lcg.next()%60)
		for b := range nact {
			act[b] = b
			bnc[b] = 1 + int(lcg.next()%NMRNCand)
			prevlo += int(lcg.next()%31) - 15
			prevlo = clip(prevlo, 0, 200)
			blo[b] = prevlo
			for o := range bnc[b] {
				st.Nd[b][o] = 0.5 * float32(lcg.next()%8)
				st.Nb[b][o] = 40 + 8*int32(lcg.next()%6)
				if o > 0 && lcg.next()%4 == 0 {
					st.Nd[b][o] = st.Nd[b][o-1]
					st.Nb[b][o] = st.Nb[b][o-1]
				}
			}
		}
		destbits := 100 + int(lcg.next()%4000)
		loL, hiL := float32(1e-9), float32(1e4)
		iters := nmrIters
		switch cs % 4 {
		case 1:
			iters = nmrCIters
		case 2:
			loL, hiL, iters = 0.5, 512.0, nmrCWarm
		case 3:
			loL, hiL, iters = 2.0, 2.0, 1
		}
		for i := range chosen {
			chosen[i] = -1
		}
		lam := c.nmrSolve(&st, blo[:], bnc[:], step, act[:], nact, destbits,
			chosen[:], loL, hiL, iters)
		if want := int(fx.i32()); nact != want {
			t.Fatalf("case %d: nact = %d, want %d (driver out of sync)", cs, nact, want)
		}
		if want := fx.f32(); lam != want {
			t.Errorf("case %d: lam = %v, want %v", cs, lam, want)
		}
		for b := range nact {
			if want := int(fx.i32()); chosen[b] != want {
				t.Errorf("case %d: chosen[%d] = %d, want %d", cs, b, chosen[b], want)
			}
		}
	}
	fx.done()
}

func TestNMRBandCurveAgainstC(t *testing.T) {
	fx := openFixture(t, "nmr_curve.bin")
	lcg := cnmrLCG(0x2b7e1516)
	var c Coder
	var sce SingleChannelElement
	var ndRow [NMRNCand]float32
	var nbRow [NMRNCand]int32
	const srIdx = 4

	sce.ICS.NumWindows = 1
	sce.ICS.GroupLen[0] = 1
	sce.ICS.SwbSizes = tables.SwbSize1024[srIdx]
	sce.ICS.SwbOffset = tables.SwbOffset1024[srIdx]
	sce.ICS.NumSwb = int(tables.NumSwb1024[srIdx])

	amps := [6]float32{0.4, 3.0, 12.0, 90.0, 500.0, 9000.0}
	for _, amp := range amps {
		for i := range 1024 {
			t2 := 2.0 * lcg.f()
			r := t2 - 1.0
			sce.Coeffs[i] = amp * r
		}
		dsp.AbsPow34(c.scoefs[:], sce.Coeffs[:])
		for gsel := range 3 {
			g := [3]int{2, 20, 38}[gsel]
			start := int(sce.ICS.SwbOffset[g])
			maxval := FindMaxVal(1, int(sce.ICS.SwbSizes[g]), c.scoefs[start:])
			if maxval <= 0 {
				if want := fx.i32(); want != -1 {
					t.Fatalf("amp %v g %d: maxval 0 but fixture %d", amp, g, want)
				}
				continue
			}
			lo := clip(int(Coef2MinSF(maxval)), 0, ScaleMaxPos)
			tf := float32(0.05 * lcg.f())
			thrf := 0.001 + tf
			var esum float32
			for i := range int(sce.ICS.SwbSizes[g]) {
				cc := float32(sce.Coeffs[start+i] * sce.Coeffs[start+i])
				esum += cc
			}
			invthr := 1.0 / max(esum*thrf, 1e-9)
			if want := fx.f32(); thrf != want {
				t.Errorf("amp %v g %d: thrf = %v, want %v", amp, g, thrf, want)
			}
			if want := fx.f32(); esum != want {
				t.Errorf("amp %v g %d: esum = %v, want %v (diff %g)", amp, g, esum, want, float64(esum-want))
			}
			for _, step := range [2]int{8, 1} {
				c.CacheInit()
				ncand := c.nmrBandCurve(&sce, 0, g, start, lo, step, NMRNCand,
					invthr, maxval, &ndRow, &nbRow)
				checkCurve(t, fx, ncand, lo, invthr, maxval, &ndRow, &nbRow)
			}
		}
	}

	// grouped short-window case
	{
		var ssce SingleChannelElement
		ssce.ICS.NumWindows = 8
		gl := [8]int{1, 3, 0, 0, 3, 0, 0, 1}
		for w := range 8 {
			ssce.ICS.GroupLen[w] = gl[w]
		}
		ssce.ICS.SwbSizes = tables.SwbSize128[srIdx]
		ssce.ICS.SwbOffset = tables.SwbOffset128[srIdx]
		ssce.ICS.NumSwb = int(tables.NumSwb128[srIdx])
		for i := range 1024 {
			t2 := 2.0 * lcg.f()
			r := t2 - 1.0
			ssce.Coeffs[i] = 25.0 * r
		}
		dsp.AbsPow34(c.scoefs[:], ssce.Coeffs[:])
		w, g := 1, 3
		start := w*128 + int(ssce.ICS.SwbOffset[g])
		maxval := FindMaxVal(3, int(ssce.ICS.SwbSizes[g]), c.scoefs[start:])
		lo := clip(int(Coef2MinSF(maxval)), 0, ScaleMaxPos)
		invthr := float32(1.0) / 3.7
		c.CacheInit()
		ncand := c.nmrBandCurve(&ssce, w, g, start, lo, 1, NMRNCand,
			invthr, maxval, &ndRow, &nbRow)
		checkCurve(t, fx, ncand, lo, invthr, maxval, &ndRow, &nbRow)
	}
	fx.done()
}

func checkCurve(t *testing.T, fx *fxr, ncand, lo int, invthr, maxval float32,
	ndRow *[NMRNCand]float32, nbRow *[NMRNCand]int32) {
	t.Helper()
	if want := int(fx.i32()); ncand != want {
		t.Fatalf("ncand = %d, want %d", ncand, want)
	}
	if want := int(fx.i32()); lo != want {
		t.Fatalf("lo = %d, want %d", lo, want)
	}
	if want := fx.f32(); invthr != want {
		t.Fatalf("invthr = %v, want %v", invthr, want)
	}
	if want := fx.f32(); maxval != want {
		t.Fatalf("maxval = %v, want %v", maxval, want)
	}
	for o := range ncand {
		if want := fx.f32(); ndRow[o] != want {
			t.Errorf("nd[%d] = %v, want %v (diff %g)", o, ndRow[o], want,
				float64(ndRow[o]-want))
		}
		if want := fx.i32(); nbRow[o] != want {
			t.Errorf("nb[%d] = %d, want %d", o, nbRow[o], want)
		}
	}
}

// nmrSynthFrame mirrors cnmr.c synth_frame exactly (same LCG call order).
func nmrSynthFrame(lcg *cnmrLCG, sce *SingleChannelElement, psy *[128]PsyBand,
	shortFrame bool, amp, noisiness, thrbase, thrspan float32, pnsmix bool) {
	const srIdx = 4
	if shortFrame {
		gl := [8]int{1, 3, 0, 0, 3, 0, 0, 1}
		sce.ICS.NumWindows = 8
		for w := range 8 {
			sce.ICS.GroupLen[w] = gl[w]
		}
		sce.ICS.SwbSizes = tables.SwbSize128[srIdx]
		sce.ICS.SwbOffset = tables.SwbOffset128[srIdx]
		sce.ICS.NumSwb = int(tables.NumSwb128[srIdx])
		sce.ICS.WindowSequence[0] = EightShortSequence
	} else {
		sce.ICS.NumWindows = 1
		sce.ICS.GroupLen[0] = 1
		sce.ICS.SwbSizes = tables.SwbSize1024[srIdx]
		sce.ICS.SwbOffset = tables.SwbOffset1024[srIdx]
		sce.ICS.NumSwb = int(tables.NumSwb1024[srIdx])
		sce.ICS.WindowSequence[0] = OnlyLongSequence
	}

	m := 1024
	if shortFrame {
		m = 128
	}
	for i := range 1024 {
		ti := float32(float32(i%m) * 0.01)
		dec := 1.0 / (1.0 + ti)
		t2 := 2.0 * lcg.f()
		r := t2 - 1.0
		sce.Coeffs[i] = amp * dec * r
	}
	for i := range psy {
		psy[i] = PsyBand{}
	}
	for w := range sce.ICS.NumWindows {
		start := 0
		for g := range sce.ICS.NumSwb {
			b := &psy[w*16+g]
			var e float32
			for i := range int(sce.ICS.SwbSizes[g]) {
				cc := sce.Coeffs[w*128+start+i]
				t := float32(cc * cc)
				e += t
			}
			b.Energy = e
			tf := float32(thrspan * lcg.f())
			b.Threshold = e * (thrbase + tf)
			sp := float32(noisiness * lcg.f())
			b.Spread = 0.3 + sp
			if pnsmix && g >= 24 && g%4 >= 2 {
				t1 := float32(0.1 * lcg.f())
				b.Threshold = e * (0.15 + t1)
				t2 := float32(0.4 * lcg.f())
				b.Spread = 1.6 + t2
			}
			if b.Spread > 2.0 {
				b.Spread = 2.0
			}
			start += int(sce.ICS.SwbSizes[g])
		}
	}
}

// nmrBandwidth mirrors the NMR rate-to-bandwidth law (aacenc.c:1600-1607).
func nmrBandwidth(frameBr, rate int) int {
	rates := [5]int{32000, 48000, 64000, 96000, 192000}
	bws := [5]int{14000, 15000, 16000, 18000, 20000}
	var bandwidth int
	if frameBr >= 32000 {
		bwI := 0
		for bwI < 3 && frameBr > rates[bwI+1] {
			bwI++
		}
		bandwidth = bws[bwI] + int(int64(bws[bwI+1]-bws[bwI])*
			int64(frameBr-rates[bwI])/int64(rates[bwI+1]-rates[bwI]))
		bandwidth = min(bandwidth, 22000, rate/2)
	} else {
		bandwidth = max(3000, aacCutoffFromBitrateTest(frameBr, 1, rate))
	}
	return min(max(bandwidth, 8000), rate/2)
}

func aacCutoffFromBitrateTest(bitRate, channels, sampleRate int) int {
	if bitRate == 0 {
		return sampleRate / 2
	}
	return min(
		max(bitRate/channels/5, bitRate/channels*15/32-5500),
		3000+bitRate/channels/4,
		12000+bitRate/channels/16,
		22000,
		sampleRate/2)
}

// The complexity waiver covers a sequential dump-format walk; splitting it
// would obscure the record layout being verified.
//
//nolint:gocognit,gocyclo // sequential fixture-format walk, see above
func runNMRSearchSequence(t *testing.T, name string, seed uint32, bitrate, speed int,
	noisiness, thrbase, thrspan float32, pnsmix bool) {
	t.Helper()
	fx := openFixture(t, name)
	lcg := cnmrLCG(seed)
	var c Coder
	var st NMRState
	var sce SingleChannelElement
	var psy [128]PsyBand
	const rate = 44100

	bandwidth := nmrBandwidth(bitrate, rate)
	if want := int(fx.i32()); bandwidth != want {
		t.Fatalf("bandwidth = %d, want %d", bandwidth, want)
	}

	lastPB := 0
	rateFrame := int(float64(bitrate) * 1024.0 / float64(rate))
	for f := range 30 {
		shortFrame := f == 18 || f == 19 || f == 24
		af := float32(60.0 * lcg.f())
		amp := 20.0 + af // separate rounding, no FMA
		if f >= 8 && f <= 10 {
			amp *= 4.0
		}
		if f == 12 {
			amp *= 0.05
		}
		nmrSynthFrame(&lcg, &sce, &psy, shortFrame, amp, noisiness,
			thrbase, thrspan, pnsmix)

		alloc := -1
		if f >= 2 {
			alloc = rateFrame + int(lcg.next()%uint32(2*rateFrame/4)) - rateFrame/4
		}

		c.MarkPNS(rate, bandwidth, &sce, &psy, 120.0)
		in := &NMRInput{
			BitRate: bitrate, SampleRate: rate, Channels: 1,
			FrameNum: int64(f), BitresAlloc: alloc, Bandwidth: bandwidth,
			CurChannel: 0, Speed: speed, RateControlOK: true,
			QScaleChannels: 1, LastFramePBCount: lastPB,
		}
		c.SearchForQuantizersNMR(in, &st, &sce, &psy, 120.0)

		// side accounting with synthetic side bits (mirrors the harness)
		side := 120 + int(lcg.next()%90)
		counted := st.Counted[0]
		lastPB = counted + side
		if counted > 0 {
			sd := float32(lastPB) - float32(counted)
			if st.SideInited {
				st.SideEMA += 0.125 * (sd - st.SideEMA)
			} else {
				st.SideEMA = sd
				st.SideInited = true
			}
		}

		if got, want := f, int(fx.i32()); got != want {
			t.Fatalf("frame counter desync: %d vs %d", got, want)
		}
		if got, want := shortFrame, fx.i32() != 0; got != want {
			t.Fatalf("frame %d: short flag desync", f)
		}
		mism := 0
		for i := range 128 {
			if want := int(fx.i32()); sce.SfIdx[i] != want {
				t.Errorf("frame %d: sf_idx[%d] = %d, want %d", f, i, sce.SfIdx[i], want)
				mism++
			}
		}
		for i := range 128 {
			if want := int(fx.i32()); sce.BandType[i] != want {
				t.Errorf("frame %d: band_type[%d] = %d, want %d", f, i, sce.BandType[i], want)
				mism++
			}
		}
		for i := range 128 {
			if want := fx.u8() != 0; sce.Zeroes[i] != want {
				t.Errorf("frame %d: zeroes[%d] = %v, want %v", f, i, sce.Zeroes[i], want)
				mism++
			}
		}
		for i := range 128 {
			if want := fx.u8() != 0; sce.CanPNS[i] != want {
				t.Errorf("frame %d: can_pns[%d] = %v, want %v", f, i, sce.CanPNS[i], want)
				mism++
			}
		}
		for i := range 128 {
			if want := fx.f32(); sce.PnsEner[i] != want {
				t.Errorf("frame %d: pns_ener[%d] = %v, want %v", f, i, sce.PnsEner[i], want)
				mism++
			}
		}
		if want := fx.f32(); st.Lam[0] != want {
			t.Errorf("frame %d: lam[0] = %v, want %v", f, st.Lam[0], want)
		}
		if want := fx.f32(); st.Lam[1] != want {
			t.Errorf("frame %d: lam[1] = %v, want %v", f, st.Lam[1], want)
		}
		if want := int(fx.i32()); st.Counted[0] != want {
			t.Errorf("frame %d: counted[0] = %d, want %d", f, st.Counted[0], want)
		}
		if want := int(fx.i32()); st.Counted[1] != want {
			t.Errorf("frame %d: counted[1] = %d, want %d", f, st.Counted[1], want)
		}
		if want := fx.f32(); st.SideEMA != want {
			t.Errorf("frame %d: side_ema = %v, want %v", f, st.SideEMA, want)
		}
		if want := fx.i32() != 0; st.SideInited != want {
			t.Errorf("frame %d: side_inited = %v, want %v", f, st.SideInited, want)
		}
		if want := int64(fx.i32()); st.RCFrameNum != want {
			t.Errorf("frame %d: rc_frame_num = %d, want %d", f, st.RCFrameNum, want)
		}
		if want := fx.f32(); st.LamRC != want {
			t.Errorf("frame %d: lam_rc = %v, want %v (diff %g)", f, st.LamRC, want,
				float64(st.LamRC-want))
		}
		if want := int(fx.i32()); st.RCFill != want {
			t.Errorf("frame %d: rc_fill = %d, want %d", f, st.RCFill, want)
		}
		if want := int(fx.i32()); st.FramesSinceShort != want {
			t.Errorf("frame %d: frames_since_short = %d, want %d", f, st.FramesSinceShort, want)
		}
		if want := fx.i32() != 0; st.PrevWasShort != want {
			t.Errorf("frame %d: prev_was_short = %v, want %v", f, st.PrevWasShort, want)
		}
		if want := fx.f32(); st.RunBurst != want {
			t.Errorf("frame %d: run_burst = %v, want %v", f, st.RunBurst, want)
		}
		if want := int(fx.i32()); lastPB != want {
			t.Errorf("frame %d: last_frame_pb_count = %d, want %d", f, lastPB, want)
		}
		if mism > 12 {
			t.Fatalf("frame %d: too many mismatches, aborting", f)
		}
	}
	fx.done()
}

func TestNMRSearchHiAgainstC(t *testing.T) {
	runNMRSearchSequence(t, "nmr_search_hi.bin", 0x3c6ef372, 128000, 0,
		1.2, 0.0005, 0.03, false)
}

func TestNMRSearchLoAgainstC(t *testing.T) {
	runNMRSearchSequence(t, "nmr_search_lo.bin", 0x510e527f, 32000, 0,
		1.9, 2e-5, 5e-4, true)
}

func TestNMRSearchSpeed3AgainstC(t *testing.T) {
	runNMRSearchSequence(t, "nmr_search_sp3.bin", 0x9b05688c, 96000, 3,
		1.2, 0.0005, 0.03, false)
}
