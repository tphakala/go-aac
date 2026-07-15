// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"github.com/tphakala/go-aac/internal/fdsp"
	"github.com/tphakala/go-aac/internal/tx"
	"github.com/tphakala/go-aac/internal/window"
)

// dspState carries the per-decoder IMDCT scratch of AACDecContext:
// buf_mdct[1024] and temp[128] (libavcodec/aac/aacdec.h:527-528
// @ d09d5afc3a) plus the two transform contexts init_dsp creates
// (libavcodec/aac/aacdec.c:1263-1296: scale = 1.0/len * 128.0f).
type dspState struct {
	mdct1024 *tx.IMDCT
	mdct128  *tx.IMDCT
	bufMDCT  [1024]int32
	temp     [128]int32
}

func (d *dspState) init() {
	// Both contexts are created together on first use, so guarding on
	// mdct1024 alone also covers mdct128; they are never partially set.
	if d.mdct1024 == nil {
		d.mdct1024 = tx.NewIMDCT(1024, 1.0/1024*128.0)
		d.mdct128 = tx.NewIMDCT(128, 1.0/128*128.0)
	}
}

// imdctAndWindowing reconstructs one frame of time samples from the
// spectrum in sce.Coeffs: inverse MDCT, windowed overlap-add into
// sce.Output, and the overlap buffer update into sce.Saved. Mirrors the
// USE_FIXED instantiation of imdct_and_windowing
// (libavcodec/aac/aacdec_dsp_template.c:325-384 @ d09d5afc3a), including
// its collapse of all "meaningless" transitions onto the short-short path.
func (d *dspState) imdctAndWindowing(sce *SCE) {
	d.init()
	ics := &sce.ICS
	in := sce.Coeffs[:]
	out := sce.Output[:]
	saved := sce.Saved[:]
	buf := d.bufMDCT[:]
	temp := d.temp[:]

	swindow := window.Sine128Fixed
	if ics.UseKBWindow[0] != 0 {
		swindow = window.KBDShort128Fixed
	}
	lwindowPrev := window.Sine1024Fixed
	swindowPrev := window.Sine128Fixed
	if ics.UseKBWindow[1] != 0 {
		lwindowPrev = window.KBDLong1024Fixed
		swindowPrev = window.KBDShort128Fixed
	}

	// imdct
	if ics.WindowSequence[0] == EightShortSequence {
		for i := 0; i < 1024; i += 128 {
			d.mdct128.Transform(buf[i:i+128], in[i:i+128])
		}
	} else {
		d.mdct1024.Transform(buf, in)
	}

	// Window overlapping. Like the C: long-to-long is one full-length
	// overlap; everything else copies the untouched 448 head samples and
	// runs short overlaps, with the extra 8x sub-window chain for
	// EIGHT_SHORT.
	if (ics.WindowSequence[1] == OnlyLongSequence || ics.WindowSequence[1] == LongStopSequence) &&
		(ics.WindowSequence[0] == OnlyLongSequence || ics.WindowSequence[0] == LongStartSequence) {
		fdsp.VectorFmulWindow(out, saved, buf, lwindowPrev, 512)
	} else {
		copy(out[:448], saved[:448])

		if ics.WindowSequence[0] == EightShortSequence {
			fdsp.VectorFmulWindow(out[448+0*128:], saved[448:], buf[0*128:], swindowPrev, 64)
			fdsp.VectorFmulWindow(out[448+1*128:], buf[0*128+64:], buf[1*128:], swindow, 64)
			fdsp.VectorFmulWindow(out[448+2*128:], buf[1*128+64:], buf[2*128:], swindow, 64)
			fdsp.VectorFmulWindow(out[448+3*128:], buf[2*128+64:], buf[3*128:], swindow, 64)
			fdsp.VectorFmulWindow(temp, buf[3*128+64:], buf[4*128:], swindow, 64)
			copy(out[448+4*128:448+4*128+64], temp[:64])
		} else {
			fdsp.VectorFmulWindow(out[448:], saved[448:], buf, swindowPrev, 64)
			copy(out[576:1024], buf[64:64+448])
		}
	}

	// buffer update
	switch ics.WindowSequence[0] {
	case EightShortSequence:
		copy(saved[:64], temp[64:128])
		fdsp.VectorFmulWindow(saved[64:], buf[4*128+64:], buf[5*128:], swindow, 64)
		fdsp.VectorFmulWindow(saved[192:], buf[5*128+64:], buf[6*128:], swindow, 64)
		fdsp.VectorFmulWindow(saved[320:], buf[6*128+64:], buf[7*128:], swindow, 64)
		copy(saved[448:512], buf[7*128+64:7*128+128])
	case LongStartSequence:
		copy(saved[:448], buf[512:512+448])
		copy(saved[448:512], buf[7*128+64:7*128+128])
	default: // LONG_STOP or ONLY_LONG
		copy(saved[:512], buf[512:1024])
	}
}
