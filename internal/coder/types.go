// SPDX-License-Identifier: LGPL-2.1-or-later

// Package coder implements the quantizer searches, the codebook sectioning
// trellis and the spectral quantize-and-encode core of the AAC encoder.
// Mirrors libavcodec/aaccoder.c, aaccoder_trellis.h, aacenc_quantization.h,
// aacenc_quantization_misc.h and aacenc_utils.h @ d09d5afc3a. Phase 1 ships
// the fast search only; twoloop and NMR arrive in later phases.
package coder

import "fmt"

// Scalefactor constants. Mirror libavcodec/aac.h @ d09d5afc3a.
const (
	ScaleDiv512   = 36  // sf difference corresponding to a scale change of 512x
	ScaleOnePos   = 140 // sf index corresponding to scale 1.0
	ScaleMaxPos   = 255 // sf index maximum value
	ScaleMaxDiff  = 60  // maximum sf difference allowed by the standard
	ScaleDiffZero = 60  // codebook index corresponding to a zero sf difference
)

// Noise (PNS) scalefactor coding constants. Mirror libavcodec/aac.h
// @ d09d5afc3a. Unused until PNS lands; encodeScaleFactors carries the
// NOISE_BT delta chain from day one (docs/porting-guide.md pitfall 4).
const (
	NoisePre     = 256 // preamble for NOISE_BT, sent with the first noise band
	NoisePreBits = 9   // length of the preamble
	NoiseOffset  = 90  // subtracted from global gain as the preamble offset
)

// Band types. Mirror enum BandType (libavcodec/aac.h @ d09d5afc3a).
const (
	ZeroBT       = 0  // scalefactors and spectral data are all zero
	FirstPairBT  = 5  // this and later band types code 2 values per codeword
	EscBT        = 11 // spectral data coded with an escape sequence
	ReservedBT   = 12 // band types following are encoded differently
	NoiseBT      = 13 // spectral data are scaled white noise (PNS)
	IntensityBT2 = 14 // intensity stereo positions, out of phase
	IntensityBT  = 15 // intensity stereo positions, in phase
)

// Window sequences. Mirror enum WindowSequence (libavcodec/aac.h
// @ d09d5afc3a).
const (
	OnlyLongSequence   = 0
	LongStartSequence  = 1
	EightShortSequence = 2
	LongStopSequence   = 3
)

// Raw data block types. Mirror enum RawDataBlockType (libavcodec/aac.h
// @ d09d5afc3a).
const (
	TypeSCE = 0
	TypeCPE = 1
	TypeCCE = 2
	TypeLFE = 3
	TypeDSE = 4
	TypePCE = 5
	TypeFIL = 6
	TypeEND = 7
)

// Codebook counts. Mirror CB_TOT and CB_TOT_ALL (libavcodec/aacenctab.h
// @ d09d5afc3a).
const (
	CBTot    = 12
	CBTotAll = 15
)

// Quantizer rounding constants. Mirror libavcodec/aacenc_utils.h
// @ d09d5afc3a.
const (
	RoundStandard = 0.4054
	RoundToZero   = 0.1054
	cQuant        = 0.4054
)

// IndividualChannelStream is the per-channel window and band metadata.
// Mirrors struct IndividualChannelStream (libavcodec/aacenc.h @ d09d5afc3a);
// TNS fields arrive with TNS in a later phase.
type IndividualChannelStream struct {
	MaxSfb              int      // number of scalefactor bands per group
	WindowSequence      [2]int   // current and previous window sequence
	UseKBWindow         [2]int   // 1 = KBD window, 0 = sine, current and previous
	GroupLen            [8]int   // window group lengths
	SwbOffset           []uint16 // offsets of the lowest line of each band
	SwbSizes            []uint8  // band sizes for the current window length
	NumSwb              int      // number of scalefactor window bands
	NumWindows          int
	TnsMaxBands         int     // ff_tns_max_bands_1024/_128 for the frame length
	WindowClipping      [8]bool // window is near clipping
	ClipAvoidanceFactor float32 // attenuation to avoid clipping, 1.0 = none
}

// EachBand invokes fn for every band position of the first nBands bands of
// each window group, with w the first window of the group, g the band and
// idx = w*16+g the canonical slot in the 128-entry band arrays. This is the
// single home of the band index convention (docs/architecture.md pitfall 1:
// band arrays are indexed w*16+g, groups iterate w += group_len[w]).
// GroupLen is >= 1 for every window of a valid ICS: the window decision sets it,
// and the C iterates the same way (w += ics->group_len[w]). But Go zero-values an
// array, so a partially initialized ICS would leave GroupLen[w] == 0 here and the
// loop would never advance -- a hang, in a library, from a struct that was merely
// forgotten. That is a programming error inside this package, not something a
// caller can provoke with any input, so it panics like an out-of-range index
// rather than wedging the caller's goroutine.
func (ics *IndividualChannelStream) EachBand(nBands int, fn func(w, g, idx int)) {
	for w := 0; w < ics.NumWindows; {
		for g := range nBands {
			fn(w, g, w*16+g)
		}
		step := ics.GroupLen[w]
		if step < 1 {
			panic(fmt.Sprintf("coder: ICS.GroupLen[%d] = %d, must be >= 1 "+
				"(uninitialized IndividualChannelStream)", w, step))
		}
		w += step
	}
}

// TemporalNoiseShaping is the per-channel TNS decision state. Mirrors
// struct TemporalNoiseShaping (libavcodec/aacenc.h @ d09d5afc3a).
type TemporalNoiseShaping struct {
	Present   bool
	NFilt     [8]int
	Length    [8][4]int
	Direction [8][4]int
	Order     [8][4]int
	CoefIdx   [8][4][TNSMaxOrder]int
	Coef      [8][4][TNSMaxOrder]float32
}

// SingleChannelElement is the per-channel coding state.
// Mirrors struct SingleChannelElement (libavcodec/aacenc.h @ d09d5afc3a);
// the pulse field is never used by the encoder and is not ported.
type SingleChannelElement struct {
	ICS      IndividualChannelStream
	TNS      TemporalNoiseShaping
	BandType [128]int      // band codebook per w*16+g slot
	BandAlt  [128]int      // alternative band type (PNS bookkeeping)
	SfIdx    [128]int      // scalefactor index per w*16+g slot
	Zeroes   [128]bool     // band is not coded
	CanPNS   [128]bool     // band is allowed to PNS (informative)
	IsEner   [128]float32  // intensity stereo position
	PnsEner  [128]float32  // noise energy values
	PCoeffs  [1024]float32 // MDCT output, pristine
	Coeffs   [1024]float32 // MDCT output, working copy
	RetBuf   [2048]float32 // windowed time-domain copy (TNS input later)
}

// PsyBand is the per-band psychoacoustic output consumed by the quantizer
// searches. Mirrors struct FFPsyBand (libavcodec/psymodel.h @ d09d5afc3a).
type PsyBand struct {
	Bits      int32
	Energy    float32
	Threshold float32
	Spread    float32 // energy spread over the band
}

// ChannelElement is the CPE wrapper around one or two channels. Mirrors
// struct ChannelElement (libavcodec/aacenc.h @ d09d5afc3a).
type ChannelElement struct {
	CommonWindow int       // set if the channels share the window sequence
	MsMode       int       // mid/side flags coding mode (0 none, 1 masked, 2 all)
	IsMode       bool      // any band uses intensity stereo
	MsMask       [128]bool // mid/side used per band
	IsMask       [128]bool // intensity stereo used per band
	Ch           [2]SingleChannelElement
}
