// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

// Fixed-range clamps for clip_output; math.MinInt32/MaxInt32 without importing
// the fenced math package.
const (
	minI32 = -1 << 31
	maxI32 = 1<<31 - 1
)

// reconTaps carries optional stage snapshots for the differential gate. They
// are the Go analogue of the C harness's vtable hooks (decode_spectrum_and_
// dequant, imdct_and_windowing, clip_output): production callers pass nil.
type reconTaps struct {
	afterDequant func(t, id, ch int, coeffs []int32) // post dequant, pre stereo
	preIMDCT     func(t, id, ch int, coeffs []int32) // at imdct entry, post TNS
	postClip     func(t, id int, isCPE bool, ch0, ch1 []int32)
}

// reconstruct turns the parsed symbol state into time-domain PCM. It is the
// internal D2 driver (the public decode-to-PCM API is D3's); the gate calls it
// after DecodeFrame. It mirrors the reconstruction half of the C decoder:
// decode_spectrum_and_dequant's fixed dequant + PNS (aacdec_proc_template.c),
// the decode_cpe stereo tools (aacdec.c:1907-1916), then spectral_to_sample's
// TNS + IMDCT + clip (aacdec.c:2123-2187).
//
// Dequant runs in bitstream (Elems) order so the shared PNS random_state
// threads through noise bands exactly as it does across the C's ff_aac_decode_
// ics calls. spectral_to_sample then runs in type-descending order like the C.
func (d *Decoder) reconstruct(taps *reconTaps) {
	d.dsp.init()

	// Stage 1+2: dequant every channel, then the per-pair stereo tools.
	for _, e := range d.Elems {
		che := e.CPE
		isCPE := e.Type == TypeCPE

		d.dequant(&che.Ch[0])
		if taps != nil && taps.afterDequant != nil {
			taps.afterDequant(e.Type, e.ID, 0, che.Ch[0].Coeffs[:])
		}
		if isCPE {
			d.dequant(&che.Ch[1])
			if taps != nil && taps.afterDequant != nil {
				taps.afterDequant(e.Type, e.ID, 1, che.Ch[1].Coeffs[:])
			}
			if che.commonWindow && che.msPresent != 0 {
				applyMidSideStereo(che)
			}
			applyIntensityStereo(che, che.msPresent)
		}
	}

	// Stage 3: TNS, IMDCT/windowing, clip, in type-descending element order.
	for t := 3; t >= 0; t-- {
		for id := range maxElemID {
			che := d.che[t][id]
			if che == nil || !che.present {
				continue
			}
			isCPE := t == TypeCPE

			if che.Ch[0].TNS.Present {
				applyTNS(che.Ch[0].Coeffs[:], &che.Ch[0].TNS, &che.Ch[0].ICS)
			}
			if che.Ch[1].TNS.Present {
				applyTNS(che.Ch[1].Coeffs[:], &che.Ch[1].TNS, &che.Ch[1].ICS)
			}

			if taps != nil && taps.preIMDCT != nil {
				taps.preIMDCT(t, id, 0, che.Ch[0].Coeffs[:])
			}
			d.dsp.imdctAndWindowing(&che.Ch[0])
			if isCPE {
				if taps != nil && taps.preIMDCT != nil {
					taps.preIMDCT(t, id, 1, che.Ch[1].Coeffs[:])
				}
				d.dsp.imdctAndWindowing(&che.Ch[1])
			}

			clipOutput(che, isCPE, 1024)
			if taps != nil && taps.postClip != nil {
				taps.postClip(t, id, isCPE, che.Ch[0].Output[:], che.Ch[1].Output[:])
			}
			che.present = false
		}
	}
}

// clipOutput scales the reconstructed time samples into the decoder's S32P
// output range and adds the resampler bias. Mirrors clip_output
// (aacdec_dsp_template.c:604-617, USE_FIXED branch @ d09d5afc3a):
// out = av_clip64(out*128, INT32_MIN, INT32_MAX-0x8000) + 0x8000. The +0x8000
// is a round-to-nearest bias for the eventual S32->S16 conversion (a plain
// arithmetic >>16, verified byte-identical against ffmpeg -f s16le). ch1 is
// clipped only for a channel pair (PS, an SCE-with-second-channel case, is
// HE-AAC and out of LC scope).
func clipOutput(che *CPE, isCPE bool, samples int) {
	clip := func(out []int32) {
		out = out[:samples]
		for j := range out {
			v := int64(out[j]) * 128
			if v < minI32 {
				v = minI32
			} else if v > maxI32-0x8000 {
				v = maxI32 - 0x8000
			}
			out[j] = int32(v + 0x8000)
		}
	}
	clip(che.Ch[0].Output[:])
	if isCPE {
		clip(che.Ch[1].Output[:])
	}
}
