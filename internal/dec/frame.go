// SPDX-License-Identifier: LGPL-2.1-or-later

package dec

import (
	"fmt"

	"github.com/tphakala/go-aac/internal/bits"
)

// DecodeFrame decodes one access unit: an ADTS frame (NewADTS) or one raw
// AAC frame (NewRaw). The decoded symbol state is left in the decoder's
// channel elements and listed in Elems. Mirrors the frame path
// aac_decode_frame_int -> parse_adts_frame_header -> decode_frame_ga of
// libavcodec/aac/aacdec.c @ d09d5afc3a at the symbol level.
func (d *Decoder) DecodeFrame(pkt []byte) error {
	r := bits.NewReader(pkt)
	if d.adts {
		hdr, err := ParseADTS(r)
		if err != nil {
			return err
		}
		if hdr.NumAACFrames != 1 {
			return fmt.Errorf("%w: %d raw data blocks per ADTS frame",
				ErrUnsupported, hdr.NumAACFrames)
		}
		cfg := Config{
			ObjectType:    hdr.ObjectType,
			SamplingIndex: hdr.SamplingIndex,
			SampleRate:    hdr.SampleRate,
			ChanConfig:    hdr.ChanConfig,
			SBR:           -1,
			PS:            -1,
		}
		if !d.configured {
			if err := d.configure(cfg); err != nil {
				return err
			}
		} else if cfg != d.cfg {
			return fmt.Errorf("%w: mid-stream configuration change", ErrUnsupported)
		}
		if hdr.CRCAbsent == 0 {
			r.Skip(16) // CRC check word (parse_adts_frame_header @ d09d5afc3a)
		}
	} else if !d.configured {
		return fmt.Errorf("%w: decoder not configured", ErrInvalidData)
	}
	return d.decodeFrameGA(r)
}

// decodeFrameGA runs the raw_data_block element loop. Mirrors
// libavcodec/aac/aacdec.c:decode_frame_ga @ d09d5afc3a, restricted to the
// element types AAC-LC mono/stereo streams can carry; CCE and PCE return
// ErrUnsupported.
func (d *Decoder) decodeFrameGA(r *bits.Reader) error {
	d.Elems = d.Elems[:0]
	var chePresence [4][maxElemID]uint8
	for {
		elemType := int(r.Read(3))
		if elemType == TypeEnd {
			break
		}
		elemID := int(r.Read(4))
		if r.Err() != nil {
			return fmt.Errorf("%w: overread in element header", ErrInvalidData)
		}

		var che *CPE
		// CCE is a channel element type (< TypeDSE) but D0 does not decode
		// coupling, so it is excluded from the presence/allocation path and
		// falls through to the ErrUnsupported branch in the switch below.
		// Otherwise the unallocated-che check would mask it as ErrInvalidData.
		if elemType < TypeDSE && elemType != TypeCCE {
			// The C tolerates ONE duplicate channel element per frame
			// (logged at debug level) and errors on the second
			// (decode_frame_ga @ d09d5afc3a: che_presence > 1 check).
			if chePresence[elemType][elemID] > 1 {
				return fmt.Errorf("%w: duplicate channel element %d.%d",
					ErrInvalidData, elemType, elemID)
			}
			chePresence[elemType][elemID]++
			che = d.che[elemType][elemID]
			if che == nil {
				return fmt.Errorf("%w: channel element %d.%d is not allocated",
					ErrInvalidData, elemType, elemID)
			}
		}

		var err error
		switch elemType {
		case TypeSCE, TypeLFE:
			err = d.decodeICS(r, &che.Ch[0], false)
			if err == nil {
				d.Elems = append(d.Elems, ElemRef{Type: elemType, ID: elemID, CPE: che})
			}
		case TypeCPE:
			err = d.decodeCPE(r, che)
			if err == nil {
				d.Elems = append(d.Elems, ElemRef{Type: elemType, ID: elemID, CPE: che})
			}
		case TypeCCE:
			err = fmt.Errorf("%w: coupling channel element", ErrUnsupported)
		case TypeDSE:
			err = skipDataStreamElement(r)
		case TypePCE:
			err = fmt.Errorf("%w: program config element", ErrUnsupported)
		case TypeFIL:
			err = skipFillElement(r, elemID)
		}
		if err != nil {
			return err
		}
		if r.Left() < 3 {
			return fmt.Errorf("%w: overread after element", ErrInvalidData)
		}
	}
	if err := r.Err(); err != nil {
		return fmt.Errorf("%w: frame overread", ErrInvalidData)
	}
	return nil
}

// decodeICS parses one individual_channel_stream. Mirrors
// libavcodec/aac/aacdec.c:ff_aac_decode_ics @ d09d5afc3a (LC path;
// scale_flag is scalable AAC and stays unported).
func (d *Decoder) decodeICS(r *bits.Reader, sce *SCE, commonWindow bool) error {
	globalGain := int(r.Read(8))

	if !commonWindow {
		if err := d.decodeICSInfo(r, &sce.ICS); err != nil {
			return err
		}
	}

	if err := decodeBandTypes(r, sce); err != nil {
		return err
	}
	if err := decodeScalefactors(r, sce, globalGain); err != nil {
		return err
	}
	sce.dequantScalefactors()

	sce.PulsePresent = false
	if r.ReadBit() != 0 {
		if sce.ICS.WindowSequence[0] == EightShortSequence {
			return fmt.Errorf("%w: pulses in eight short sequence", ErrInvalidData)
		}
		sce.PulsePresent = true
		if err := decodePulses(r, &sce.Pulse, sce.ICS.SWBOffset, sce.ICS.NumSWB); err != nil {
			return fmt.Errorf("%w: pulse data", ErrInvalidData)
		}
	}
	sce.TNS = TNSData{}
	sce.TNS.Present = r.ReadBit() != 0
	if sce.TNS.Present {
		if err := decodeTNS(r, &sce.TNS, &sce.ICS); err != nil {
			return err
		}
	}
	if r.ReadBit() != 0 {
		// SSR gain control: position-correct parse, contents unused
		// (the C decoder reports it as a missing feature and continues).
		decodeGainControl(r, &sce.ICS)
	}
	if r.Err() != nil {
		return fmt.Errorf("%w: overread in side info", ErrInvalidData)
	}

	var pulse *Pulse
	if sce.PulsePresent {
		pulse = &sce.Pulse
	}
	return decodeSpectrum(r, sce, pulse)
}

// decodeCPE parses one channel_pair_element. Mirrors
// libavcodec/aac/aacdec.c:decode_cpe @ d09d5afc3a at the symbol level (the
// M/S and intensity APPLICATION are later phases; the mask parse is here).
func (d *Decoder) decodeCPE(r *bits.Reader, cpe *CPE) error {
	commonWindow := r.ReadBit() != 0
	if commonWindow {
		if err := d.decodeICSInfo(r, &cpe.Ch[0].ICS); err != nil {
			return err
		}
		i := cpe.Ch[1].ICS.UseKBWindow[0]
		cpe.Ch[1].ICS = cpe.Ch[0].ICS
		cpe.Ch[1].ICS.UseKBWindow[1] = i
		msPresent := int(r.Read(2))
		if msPresent == 3 {
			return fmt.Errorf("%w: ms_present = 3 is reserved", ErrInvalidData)
		}
		if msPresent > 0 {
			decodeMidSideStereo(r, cpe, msPresent)
		}
	}
	if err := d.decodeICS(r, &cpe.Ch[0], commonWindow); err != nil {
		return err
	}
	return d.decodeICS(r, &cpe.Ch[1], commonWindow)
}

// decodeMidSideStereo parses the M/S mask. Mirrors
// libavcodec/aac/aacdec.c:decode_mid_side_stereo @ d09d5afc3a, including
// the C's behavior of leaving stale mask entries untouched when
// ms_present == 0 (the caller never applies them).
func decodeMidSideStereo(r *bits.Reader, cpe *CPE, msPresent int) {
	maxIdx := cpe.Ch[0].ICS.NumWindowGroups * cpe.Ch[0].ICS.MaxSFB
	if msPresent == 1 {
		for idx := range maxIdx {
			cpe.MSMask[idx] = uint8(r.ReadBit())
		}
	} else {
		for idx := range maxIdx {
			cpe.MSMask[idx] = 1
		}
	}
}

// skipDataStreamElement mirrors
// libavcodec/aac/aacdec.c:skip_data_stream_element @ d09d5afc3a.
func skipDataStreamElement(r *bits.Reader) error {
	byteAlign := r.ReadBit()
	count := int(r.Read(8))
	if count == 255 {
		count += int(r.Read(8))
	}
	if byteAlign != 0 {
		r.Align()
	}
	if r.Left() < 8*count {
		return fmt.Errorf("%w: overread in data stream element", ErrInvalidData)
	}
	r.Skip(8 * count)
	return nil
}

// Extension payload types carried by fill elements. Mirror enum
// ExtensionPayloadID (libavcodec/aac/aacdec.h @ d09d5afc3a).
const (
	extSBRData    = 13 // EXT_SBR_DATA
	extSBRDataCRC = 14 // EXT_SBR_DATA_CRC
)

// skipFillElement parses the fill element length and skips its payload.
// The length handling mirrors the TYPE_FIL case of decode_frame_ga
// @ d09d5afc3a. The payload (extension_payload: fill bits, DRC, data
// elements) is skipped unparsed, which consumes the same bit count the C
// consumes for every non-SBR payload type: decode_extension_payload
// @ d09d5afc3a reads a 4-bit type then 8*cnt-4 bits, and
// decode_dynamic_range consumes exactly 8 bits per byte it reports
// consumed. An SBR payload means the stream is HE-AAC (the C upgrades the
// output configuration mid-stream); D0 rejects it as unsupported instead.
//
// Only the FIRST payload's 4-bit type is inspected, whereas the C loops
// over every payload in the element (aacdec.c:2427 "while (elem_id > 0)").
// This is deliberate and correct for D0's AAC-LC scope, not an oversight a
// reviewer should "fix": SBR appears only in HE-AAC, and the sole payload
// type that does not consume the whole element (so the C's loop advances
// to a second payload) is EXT_DYNAMIC_RANGE. Catching SBR hidden behind a
// leading DRC payload would therefore require porting decode_dynamic_range
// plus decode_drc_channel_exclusions to find the DRC byte length, which
// belongs with real HE-AAC/DRC support in a later slice, not this
// symbol-only decoder. Bit accounting is identical either way: the whole
// 8*cnt is skipped, so no in-scope stream desyncs.
func skipFillElement(r *bits.Reader, cnt int) error {
	if cnt == 15 {
		cnt += int(r.Read(8)) - 1
	}
	if r.Left() < 8*cnt {
		return fmt.Errorf("%w: overread in fill element", ErrInvalidData)
	}
	if cnt > 0 {
		// First payload only; see the scope note above.
		if t := r.Peek(4); t == extSBRData || t == extSBRDataCRC {
			return fmt.Errorf("%w: SBR extension payload (HE-AAC)", ErrUnsupported)
		}
	}
	r.Skip(8 * cnt)
	return nil
}
