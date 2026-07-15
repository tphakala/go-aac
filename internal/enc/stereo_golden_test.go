// SPDX-License-Identifier: LGPL-2.1-or-later

package enc

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/tables"
)

type sfxr struct {
	t   *testing.T
	buf []byte
	off int
}

func (f *sfxr) i32() int32 {
	v := int32(binary.LittleEndian.Uint32(f.buf[f.off:]))
	f.off += 4
	return v
}

func (f *sfxr) f32() float32 {
	v := math.Float32frombits(binary.LittleEndian.Uint32(f.buf[f.off:]))
	f.off += 4
	return v
}

func (f *sfxr) u8() uint8 {
	v := f.buf[f.off]
	f.off++
	return v
}

type stereoLCG uint32

func (l *stereoLCG) next() uint32 {
	*l = stereoLCG(uint32(*l)*1664525 + 1013904223)
	return uint32(*l)
}

func (l *stereoLCG) f() float32 {
	return float32(l.next()>>8) / 16777216.0
}

// stereoSynthLong mirrors cnmr.c synth_frame for the long-window stereo
// left channel (thrbase 0.0005, thrspan 0.03, no pnsmix).
func stereoSynthLong(lcg *stereoLCG, sce *coder.SingleChannelElement,
	psy *[128]coder.PsyBand, amp, noisiness float32) {
	const srIdx = 4
	sce.ICS.NumWindows = 1
	sce.ICS.GroupLen[0] = 1
	sce.ICS.SwbSizes = tables.SwbSize1024[srIdx]
	sce.ICS.SwbOffset = tables.SwbOffset1024[srIdx]
	sce.ICS.NumSwb = int(tables.NumSwb1024[srIdx])
	sce.ICS.WindowSequence[0] = coder.OnlyLongSequence

	for i := range 1024 {
		ti := float32(float32(i%1024) * 0.01)
		dec := 1.0 / (1.0 + ti)
		t2 := 2.0 * lcg.f()
		r := t2 - 1.0
		sce.Coeffs[i] = amp * dec * r
	}
	for i := range psy {
		psy[i] = coder.PsyBand{}
	}
	start := 0
	for g := range sce.ICS.NumSwb {
		b := &psy[g]
		var e float32
		for i := range int(sce.ICS.SwbSizes[g]) {
			cc := sce.Coeffs[start+i]
			t := float32(cc * cc)
			e += t
		}
		b.Energy = e
		tf := float32(0.03 * lcg.f())
		b.Threshold = e * (0.0005 + tf)
		sp := float32(noisiness * lcg.f())
		b.Spread = 0.3 + sp
		if b.Spread > 2.0 {
			b.Spread = 2.0
		}
		start += int(sce.ICS.SwbSizes[g])
	}
}

// The complexity waiver covers a sequential dump-format walk; splitting it
// would obscure the record layout being verified.
//
//nolint:gocognit,gocyclo // sequential fixture-format walk, see above
func TestNMRDecideStereoAgainstC(t *testing.T) {
	buf, err := os.ReadFile("testdata/nmr_stereo.bin")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	fx := &sfxr{t: t, buf: buf}
	lcg := stereoLCG(0x6a09e667)

	rate, bitrate := 44100, 96000
	var c coder.Coder
	var st coder.NMRState
	var cpe coder.ChannelElement
	var psy [2][128]coder.PsyBand

	bandwidth := nmrCoderBandwidth(bitrate/2, rate)
	if want := int(fx.i32()); bandwidth != want {
		t.Fatalf("bandwidth = %d, want %d", bandwidth, want)
	}

	lastPB := 0
	rateFrame := int(float64(bitrate) * 1024.0 / float64(rate))
	for f := range 8 {
		s0, s1 := &cpe.Ch[0], &cpe.Ch[1]
		for i := range 128 {
			s0.BandType[i] = 0
			s1.BandType[i] = 0
			cpe.MsMask[i] = false
			cpe.IsMask[i] = false
		}

		stereoSynthLong(&lcg, s0, &psy[0], 45.0, 1.7)
		s1.ICS = s0.ICS
		start := 0
		for g := range s0.ICS.NumSwb {
			for i := range int(s0.ICS.SwbSizes[g]) {
				l := s0.Coeffs[start+i]
				var r float32
				switch g % 3 {
				case 0:
					t2 := 2.0 * lcg.f()
					tt := float32(0.002 * (t2 - 1.0))
					r = l * (1.0 + tt)
				case 1:
					t2 := 2.0 * lcg.f()
					tn := float32(0.01 * l * (t2 - 1.0))
					tl := float32(0.7 * l)
					r = tl + tn
				default:
					t2 := 2.0 * lcg.f()
					r = (t2 - 1.0) * 40.0 / (1.0 + float32(g))
				}
				s1.Coeffs[start+i] = r
			}
			start += int(s0.ICS.SwbSizes[g])
		}
		// lift the I/S bands' masking so the image test can pass
		for g := range s0.ICS.NumSwb {
			if g%3 == 1 {
				b := &psy[0][g]
				tf := float32(0.05 * lcg.f())
				b.Threshold = b.Energy * (0.02 + tf)
			}
		}
		// right-channel psy from its own coeffs
		start = 0
		for g := range s1.ICS.NumSwb {
			b := &psy[1][g]
			var e float32
			for i := range int(s1.ICS.SwbSizes[g]) {
				cc := s1.Coeffs[start+i]
				tt := float32(cc * cc)
				e += tt
			}
			b.Energy = e
			tf := float32(0.03 * lcg.f())
			b.Threshold = e * (0.0005 + tf)
			sp := float32(1.7 * lcg.f())
			b.Spread = 0.3 + sp
			if b.Spread > 2.0 {
				b.Spread = 2.0
			}
			start += int(s1.ICS.SwbSizes[g])
		}

		alloc := rateFrame + int(lcg.next()%uint32(rateFrame/2)) - rateFrame/4
		if f == 4 {
			st.RCFill = -1800 // exercise the I/S deficit bonus
		}

		// stereo PNS intersection (aacenc.c:1203-1212)
		c.MarkPNS(rate, bandwidth, s0, &psy[0], 120.0)
		c.MarkPNS(rate, bandwidth, s1, &psy[1], 120.0)
		for b := range 128 {
			if !s0.CanPNS[b] || !s1.CanPNS[b] {
				s0.CanPNS[b] = false
				s1.CanPNS[b] = false
			}
		}

		nmrDecideStereo(stereoInput{
			sampleRate: rate, bitRate: bitrate, channels: 2,
			midSide: -1, intensityStereo: true,
			rcFill: st.RCFill, haveNMR: true,
		}, &cpe, &psy[0], &psy[1])

		for i := range 128 {
			if want := fx.u8() != 0; cpe.MsMask[i] != want {
				t.Errorf("frame %d: ms_mask[%d] = %v, want %v", f, i, cpe.MsMask[i], want)
			}
		}
		for i := range 128 {
			if want := fx.u8() != 0; cpe.IsMask[i] != want {
				t.Errorf("frame %d: is_mask[%d] = %v, want %v", f, i, cpe.IsMask[i], want)
			}
		}
		if want := fx.u8() != 0; cpe.IsMode != want {
			t.Errorf("frame %d: is_mode = %v, want %v", f, cpe.IsMode, want)
		}
		for i := range 128 {
			if want := int(fx.i32()); s1.BandType[i] != want {
				t.Errorf("frame %d: ch1 band_type[%d] = %d, want %d", f, i, s1.BandType[i], want)
			}
		}
		for i := range 128 {
			if want := fx.f32(); s0.IsEner[i] != want {
				t.Errorf("frame %d: ch0 is_ener[%d] = %v, want %v", f, i, s0.IsEner[i], want)
			}
			if want := fx.f32(); s1.IsEner[i] != want {
				t.Errorf("frame %d: ch1 is_ener[%d] = %v, want %v", f, i, s1.IsEner[i], want)
			}
		}
		for i := range 128 {
			for ch := range 2 {
				if want := fx.f32(); psy[ch][i].Energy != want {
					t.Errorf("frame %d: psy%d energy[%d] = %v, want %v", f, ch, i, psy[ch][i].Energy, want)
				}
				if want := fx.f32(); psy[ch][i].Threshold != want {
					t.Errorf("frame %d: psy%d threshold[%d] = %v, want %v", f, ch, i, psy[ch][i].Threshold, want)
				}
			}
		}
		for i := range 128 {
			if want := fx.u8() != 0; s0.CanPNS[i] != want {
				t.Errorf("frame %d: can_pns[%d] = %v, want %v", f, i, s0.CanPNS[i], want)
			}
		}

		// both-channel search + special band scalefactors
		for ch := range 2 {
			in := &coder.NMRInput{
				BitRate: bitrate, SampleRate: rate, Channels: 2,
				FrameNum: int64(f), BitresAlloc: alloc, Bandwidth: bandwidth,
				CurChannel: ch, Speed: 0, RateControlOK: true,
				QScaleChannels: 2, LastFramePBCount: lastPB,
			}
			c.SearchForQuantizersNMR(in, &st, &cpe.Ch[ch], &psy[ch], 120.0)
		}
		coder.SetSpecialBandScalefactors(s0)
		coder.SetSpecialBandScalefactors(s1)

		for ch := range 2 {
			sce := &cpe.Ch[ch]
			for i := range 128 {
				if want := int(fx.i32()); sce.SfIdx[i] != want {
					t.Errorf("frame %d ch %d: sf_idx[%d] = %d, want %d", f, ch, i, sce.SfIdx[i], want)
				}
			}
			for i := range 128 {
				if want := int(fx.i32()); sce.BandType[i] != want {
					t.Errorf("frame %d ch %d: band_type[%d] = %d, want %d", f, ch, i, sce.BandType[i], want)
				}
			}
			for i := range 128 {
				if want := fx.u8() != 0; sce.Zeroes[i] != want {
					t.Errorf("frame %d ch %d: zeroes[%d] = %v, want %v", f, ch, i, sce.Zeroes[i], want)
				}
			}
			for i := range 128 {
				if want := fx.u8() != 0; sce.CanPNS[i] != want {
					t.Errorf("frame %d ch %d: can_pns[%d] = %v, want %v", f, ch, i, sce.CanPNS[i], want)
				}
			}
			for i := range 128 {
				if want := fx.f32(); sce.PnsEner[i] != want {
					t.Errorf("frame %d ch %d: pns_ener[%d] = %v, want %v", f, ch, i, sce.PnsEner[i], want)
				}
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
			t.Errorf("frame %d: lam_rc = %v, want %v", f, st.LamRC, want)
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

		// side accounting with synthetic side bits
		side := 260 + int(lcg.next()%120)
		counted := st.Counted[0] + st.Counted[1]
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
		if want := int(fx.i32()); lastPB != want {
			t.Errorf("frame %d: last_frame_pb_count = %d, want %d", f, lastPB, want)
		}
		if t.Failed() {
			t.Fatalf("frame %d: mismatches, aborting", f)
		}
	}
	if fx.off != len(fx.buf) {
		t.Fatalf("fixture: %d bytes left", len(fx.buf)-fx.off)
	}
}

// nmrCoderBandwidth mirrors the NMR rate-to-bandwidth law
// (aacenc.c:1600-1607 @ d09d5afc3a) for frame_br >= 32000.
func nmrCoderBandwidth(frameBr, rate int) int {
	rates := [5]int{32000, 48000, 64000, 96000, 192000}
	bws := [5]int{14000, 15000, 16000, 18000, 20000}
	bwI := 0
	for bwI < 3 && frameBr > rates[bwI+1] {
		bwI++
	}
	bandwidth := bws[bwI] + int(int64(bws[bwI+1]-bws[bwI])*
		int64(frameBr-rates[bwI])/int64(rates[bwI+1]-rates[bwI]))
	bandwidth = min(bandwidth, 22000, rate/2)
	return min(max(bandwidth, 8000), rate/2)
}
