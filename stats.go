// SPDX-License-Identifier: LGPL-2.1-or-later

package aac

import (
	"fmt"

	"github.com/tphakala/go-aac/internal/enc"
)

// Stats reports encoder tool usage, accumulated per encoded frame over the
// final per-band decisions. It mirrors the counters FFmpeg's encoder prints
// once at uninit (libavcodec/aacenc.h:232-239, aacenc.c:1352-1386
// @ d09d5afc3a); this library never logs, so the report is exposed as data
// instead. All counters reset on Encoder.Reset.
type Stats struct {
	Frames         int64   // access units produced
	ChannelFrames  int64   // coded channel-frames (Frames x channels)
	ShortFrames    int64   // channel-frames coded with eight short windows
	TNSLongFrames  int64   // TNS-active channel-frames among long blocks
	TNSShortFrames int64   // TNS-active channel-frames among short blocks
	Bands          int64   // coded channel-bands
	PNSBands       int64   // of which perceptual noise substitution
	PairBands      int64   // coded channel-pair bands (stereo only)
	MSBands        int64   // of which mid/side coded
	ISBands        int64   // of which intensity coded
	MeanLambda     float64 // mean per-frame operating lambda (the C's Qavg)
}

// String formats the stats like the report FFmpeg's encoder logs at uninit
// (aacenc.c:1437-1445 @ d09d5afc3a).
func (s Stats) String() string {
	pct := func(a, b int64) float64 {
		if b == 0 {
			return 0
		}
		return 100 * float64(a) / float64(b)
	}
	return fmt.Sprintf(
		"Qavg: %.3f  Tr: %.1f%%  TNS(L): %.1f%%  TNS(S): %.1f%%  M/S: %.1f%%  I/S: %.1f%%  PNS: %.1f%%",
		s.MeanLambda,
		pct(s.ShortFrames, s.ChannelFrames),
		pct(s.TNSLongFrames, s.ChannelFrames-s.ShortFrames),
		pct(s.TNSShortFrames, s.ShortFrames),
		pct(s.MSBands, s.PairBands),
		pct(s.ISBands, s.PairBands),
		pct(s.PNSBands, s.Bands),
	)
}

// statsFromInternal converts the internal counters to the public shape.
// The pointer receiver-style parameter avoids copying the 88-byte struct
// (gocritic hugeParam); the callee never mutates it.
func statsFromInternal(st *enc.Stats) Stats {
	mean := 0.0
	if st.LambdaCount > 0 {
		mean = st.LambdaSum / float64(st.LambdaCount)
	}
	return Stats{
		Frames:         st.LambdaCount,
		ChannelFrames:  st.Chans,
		ShortFrames:    st.Short,
		TNSLongFrames:  st.TNSLong,
		TNSShortFrames: st.TNSShort,
		Bands:          st.ChBands,
		PNSBands:       st.PNS,
		PairBands:      st.CPEBands,
		MSBands:        st.MS,
		ISBands:        st.IS,
		MeanLambda:     mean,
	}
}
