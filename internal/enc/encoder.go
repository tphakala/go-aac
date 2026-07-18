// SPDX-License-Identifier: LGPL-2.1-or-later

// Package enc implements the AAC encoder frame pipeline: input buffering,
// window switching, MDCT, psychoacoustic analysis, rate control and
// raw_data_block bitstream writing. Mirrors libavcodec/aacenc.c
// @ d09d5afc3a, restricted to Phase 2 scope: mono SCE or stereo CPE, the
// full LAME window decision and 3GPP psy model, the fast coder, no
// TNS/PNS/MS/IS. The package emits raw AAC access units; ADTS framing is
// applied by the caller (the root package owns the ADTS writer).
package enc

import (
	"errors"
	"fmt"

	"github.com/tphakala/go-aac/internal/bits"
	"github.com/tphakala/go-aac/internal/coder"
	"github.com/tphakala/go-aac/internal/dsp"
	"github.com/tphakala/go-aac/internal/fmath"
	"github.com/tphakala/go-aac/internal/mdct"
	"github.com/tphakala/go-aac/internal/psy"
	"github.com/tphakala/go-aac/internal/tables"
	"github.com/tphakala/go-aac/internal/window"
)

// clipAvoidanceFactor mirrors CLIP_AVOIDANCE_FACTOR (libavcodec/aacenc.h
// @ d09d5afc3a).
const clipAvoidanceFactor = 0.95

// fltEpsilon mirrors C FLT_EPSILON, the lambda floor of the rate loop.
const fltEpsilon = 1.1920929e-07

// frameSize is the AAC frame length in samples (aacenc.c: avctx->frame_size).
const frameSize = 1024

// maxFrameBits is the AAC buffer model cap per channel per frame
// (aacenc.c @ d09d5afc3a: 6144 bits per channel).
const maxFrameBits = 6144

// ErrInvalidAudio reports non-finite input samples, mirroring the C
// encoder's 1e16 spectral coefficient guard (aacenc.c:1119-1124
// @ d09d5afc3a).
var ErrInvalidAudio = errors.New("enc: input contains (near) NaN/Inf")

// CoderKind selects the quantizer search strategy. Mirrors enum AACCoder
// (libavcodec/aacenc.h @ d09d5afc3a); the zero value is NMR, upstream's
// default.
type CoderKind int

// Coder kinds. Mirror enum AACCoder @ d09d5afc3a.
const (
	CoderNMR     CoderKind = iota // noise-to-mask ratio trellis (default)
	CoderFast                     // fast two-loop heuristic (Phases 1-2)
	CoderTwoLoop                  // ISO 13818-7 Appendix C two-loop search
)

// Config selects the encoder parameters.
type Config struct {
	SampleRate    int       // 44100 or 48000 in Phase 2
	Bitrate       int       // bits per second, total across channels
	Channels      int       // 1 (SCE) or 2 (CPE)
	Cutoff        int       // user bandwidth override in Hz; 0 = automatic (avctx->cutoff)
	StrictBitrate bool      // strict CBR (mirrors bit_rate_tolerance == 0)
	Coder         CoderKind // quantizer search; zero value is NMR
	NMRSpeed      int       // NMR speed level 0..4, 0 = slowest/best
	// Tool switches; zero values mirror the upstream defaults (all on:
	// aac_tns 1, aac_pns 1, aac_ms -1 auto, aac_is 1 @ d09d5afc3a).
	DisableTNS bool // disable temporal noise shaping
	DisablePNS bool // disable perceptual noise substitution
	DisableMS  bool // disable the mid/side auto search (non-NMR coders)
	DisableIS  bool // disable intensity stereo (non-NMR coders)
}

// Encoder is the AAC encoder state: everything is preallocated here so the
// encode path is allocation-free in steady state (docs/go-design.md
// allocation policy). Mirrors the Phase 2 subset of AACEncContext
// (libavcodec/aacenc.h @ d09d5afc3a).
type Encoder struct {
	cfg              Config
	samplerateIndex  int
	bandwidth        int // coding bandwidth in Hz, fixed at init
	lambda           float32
	frameNum         int64
	remainingSamples int // queued minus covered samples, seeded with the priming delay
	lastFramePBCount int
	// err is a sticky internal-invariant violation raised from deep inside the
	// bitstream writer, where the C would call av_assert0 and abort(). The band
	// iterators take callbacks that cannot return an error, so it is recorded
	// here and surfaced by EncodeFrame instead of panicking in the caller's
	// process. Cleared by Reset.
	err           error
	planarSamples [2][3 * frameSize]float32
	cpe           coder.ChannelElement
	windows       [2]psy.WindowInfo
	psy           *psy.Context
	coeffPtrs     [2][]float32
	cd            coder.Coder
	nmr           *coder.NMRState // NMR coder state, nil unless Coder == CoderNMR
	mdct1024      *mdct.MDCT
	mdct128       *mdct.MDCT
	pb            *bits.Writer
	pbBuf         []byte
	// Per-frame tool activity flags for the coeffs restore in the rate
	// loop (aacenc.c:1337-1345); all tools are off in Phase 2 so these
	// stay zero, but the restore seam is wired now
	// (docs/porting-guide.md pitfall 2).
	isMode, msMode, tnsMode, predMode int
	trace                             []string // tool-call ordering trace; nil (and dead) outside tests
	stats                             Stats    // tool-usage counters (aacenc.h:232-239)
}

// New returns an encoder for cfg. Mirrors the Phase 2 subset of
// aacenc.c:aac_encode_init @ d09d5afc3a: samplerate index lookup, bitrate
// clamping, lambda default, coding bandwidth computation and psy init.
func New(cfg Config) (*Encoder, error) {
	e := &Encoder{}
	if err := e.Reset(cfg); err != nil {
		return nil, err
	}
	return e, nil
}

// Reset re-arms the encoder for a new, independent stream with cfg, reusing
// every retained heap allocation (MDCT contexts, NMR state, bitstream
// buffer) so pooled encoders stay allocation-light. Encoding after Reset
// produces the same bytes as encoding with a fresh New(cfg) encoder. On
// error the encoder must not be used.
func (e *Encoder) Reset(cfg Config) error {
	var idx int
	switch cfg.SampleRate {
	case 48000:
		idx = 3
	case 44100:
		idx = 4
	default:
		return fmt.Errorf("enc: unsupported sample rate %d (Phase 2 supports 44100 and 48000)", cfg.SampleRate)
	}
	if cfg.Channels < 1 || cfg.Channels > 2 {
		return fmt.Errorf("enc: unsupported channel count %d (1 or 2)", cfg.Channels)
	}
	if cfg.Bitrate <= 0 {
		return fmt.Errorf("enc: invalid bitrate %d", cfg.Bitrate)
	}
	if cfg.Coder < CoderNMR || cfg.Coder > CoderTwoLoop {
		return fmt.Errorf("enc: unsupported coder kind %d", cfg.Coder)
	}
	// Bitrate limiting (aacenc.c:1560-1566).
	cfg.Bitrate = min(cfg.Bitrate,
		int(float64(maxFrameBits*cfg.Channels)/1024.0*float64(cfg.SampleRate)))

	// Retain the reusable heap objects across the wipe. Zeroing the whole
	// struct (about 650 KiB of inline arrays) is what guarantees a Reset
	// encoder is indistinguishable from a fresh one.
	mdct1024, mdct128, nmr, pb, pbBuf := e.mdct1024, e.mdct128, e.nmr, e.pb, e.pbBuf
	*e = Encoder{
		cfg:             cfg,
		samplerateIndex: idx,
		lambda:          120,
		// ff_af_queue_init seeds remaining_samples with initial_padding
		// (audio_frame_queue.c @ d09d5afc3a); the priming frame emits no
		// packet and the flush drain emits one extra packet to cover it.
		remainingSamples: frameSize,
	}
	if mdct1024 == nil {
		mdct1024 = mdct.New(1024, 32768.0)
		mdct128 = mdct.New(128, 32768.0)
	}
	e.mdct1024, e.mdct128 = mdct1024, mdct128
	if pbBuf == nil {
		pbBuf = make([]byte, 0, 8192*2)
		pb = bits.NewWriter(pbBuf)
	}
	e.pb, e.pbBuf = pb, pbBuf

	// Coding bandwidth, fixed at init (aacenc.c:1591-1616). A user cutoff
	// wins verbatim (aacenc.c:1591-1592); for NMR the rate to bandwidth
	// conversion was tuned upstream over a variable cutoff x bitrate combo
	// (aacenc.c:1598-1607).
	if cfg.Cutoff > 0 {
		e.bandwidth = cfg.Cutoff
	} else {
		frameBr := cfg.Bitrate / cfg.Channels
		if cfg.Coder == CoderNMR && frameBr >= 32000 {
			rates := [5]int{32000, 48000, 64000, 96000, 192000}
			bws := [5]int{14000, 15000, 16000, 18000, 20000}
			bwI := 0
			for bwI < 3 && frameBr > rates[bwI+1] {
				bwI++
			}
			e.bandwidth = bws[bwI] + int(int64(bws[bwI+1]-bws[bwI])*
				int64(frameBr-rates[bwI])/int64(rates[bwI+1]-rates[bwI]))
			e.bandwidth = min(e.bandwidth, 22000, cfg.SampleRate/2)
		} else {
			if cfg.Coder == CoderNMR {
				// PNS and I/S are on by default with NMR (aacenc.c:1609)
				frameBr = int(float32(frameBr) * 1.15)
			}
			e.bandwidth = max(3000, aacCutoffFromBitrate(frameBr, 1, cfg.SampleRate))
		}
		e.bandwidth = min(max(e.bandwidth, 8000), cfg.SampleRate/2)
	}

	if cfg.Coder == CoderNMR {
		if nmr == nil {
			nmr = &coder.NMRState{} // ~96 KiB, allocated once (pitfall 12)
		} else {
			*nmr = coder.NMRState{}
		}
		e.nmr = nmr
	}
	e.cd.RandomState = 0x1f2e3d4c // PNS LFSR seed (aacenc.c:1640)

	// Psy model init (ff_psy_init call site, aacenc.c:1630-1638). The
	// context is small (~4 KiB); rebuilding it keeps Reset simple and
	// exactly equivalent to a fresh init.
	bands := [2][]uint8{tables.SwbSize1024[idx], tables.SwbSize128[idx]}
	numBands := [2]int{int(tables.NumSwb1024[idx]), int(tables.NumSwb128[idx])}
	e.psy = psy.New(cfg.SampleRate, cfg.Bitrate, cfg.Channels, e.bandwidth,
		bands, numBands)

	for ch := range cfg.Channels {
		e.coeffPtrs[ch] = e.cpe.Ch[ch].Coeffs[:]
	}
	e.err = nil // a reset encoder is usable again
	return nil
}

// aacCutoffFromBitrate mirrors AAC_CUTOFF_FROM_BITRATE
// (libavcodec/psymodel.h:35 @ d09d5afc3a).
func aacCutoffFromBitrate(bitRate, channels, sampleRate int) int {
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

// Bandwidth returns the coding bandwidth in Hz fixed at init.
func (e *Encoder) Bandwidth() int { return e.bandwidth }

// SampleRateIndex returns the MPEG-4 samplerate table index.
func (e *Encoder) SampleRateIndex() int { return e.samplerateIndex }

// Channels returns the configured channel count.
func (e *Encoder) Channels() int { return e.cfg.Channels }

// EncodeFrame encodes the next 1024-sample frame of planar float32 PCM in
// [-1, 1] (one slice per channel) and appends one raw AAC access unit to
// dst. Pass nil samples to flush; a shorter final frame is zero-padded.
// Returns dst unchanged during priming (the first call) and once the flush
// has drained all queued samples. Mirrors the Phase 2 subset of
// aacenc.c:aac_encode_frame @ d09d5afc3a.
func (e *Encoder) EncodeFrame(dst []byte, samples [][]float32) ([]byte, error) {
	if samples != nil {
		if len(samples) != e.cfg.Channels {
			return dst, fmt.Errorf("enc: %d channel slices, want %d", len(samples), e.cfg.Channels)
		}
		for ch := 1; ch < len(samples); ch++ {
			if len(samples[ch]) != len(samples[0]) {
				return dst, errors.New("enc: channel slices differ in length")
			}
		}
		// A non-nil but empty frame queues no audio yet still shifts the overlap
		// buffer and advances the priming/drain bookkeeping below, which silently
		// injects a blank frame and desynchronizes draining. nil is the only flush
		// signal; an empty slice is a caller mistake, not a flush.
		if len(samples[0]) == 0 {
			return dst, errors.New("enc: empty frame; pass nil to flush")
		}
		if len(samples[0]) > frameSize {
			return dst, fmt.Errorf("enc: frame of %d samples exceeds %d", len(samples[0]), frameSize)
		}
		// Reject non-finite input at ingest, before any sample can reach a
		// downstream int(float) conversion. int32(NaN) is 0 on arm64 and
		// -2^31 on amd64, so a NaN reaching the quantizer would make the
		// emitted bytes GOARCH-dependent. A NaN in the lookahead region taints
		// the current frame's psy analysis one frame before the post-MDCT
		// guard (below) fires, so that guard alone cannot close this hole. The
		// !(|v| <= FLT_MAX) form is true for NaN and for +/-Inf; it matches
		// the documented ErrInvalidAudio contract.
		for ch := range samples {
			for _, v := range samples[ch] {
				if !(absf(v) <= fmath.MaxFloat32) {
					return dst, ErrInvalidAudio
				}
			}
		}
		e.remainingSamples += len(samples[0])
	} else if e.remainingSamples <= 0 {
		return dst, nil // drained (mirrors the afq empty check, aacenc.c:1031)
	}

	e.copyInputSamples(samples)

	if e.frameNum == 0 { // priming: no packet for the first frame
		e.frameNum++
		return dst, nil
	}

	flush := samples == nil
	chans := e.cfg.Channels
	for ch := range chans {
		if err := e.analyzeWindowAndTransform(ch, flush); err != nil {
			return dst, err
		}
	}

	e.encodeFrameRateLoop(chans)

	// An unachievable strict bitrate, or an invariant violation recorded by the
	// bitstream writer, means the frame just built is garbage: surface it rather
	// than emitting it.
	if e.err != nil {
		return dst, e.err
	}

	e.accumulateStats(chans)

	e.pb.Put(3, coder.TypeEND)
	payload := e.pb.Flush()
	e.lastFramePBCount = e.pb.Count()

	// NMR rate accounting: how many bits the frame really took beyond
	// what the trellis counted (aacenc.c:1394-1409).
	if e.nmr != nil {
		counted := 0
		for i := range e.cfg.Channels {
			counted += e.nmr.Counted[i]
		}
		if counted > 0 {
			side := float32(e.lastFramePBCount) - float32(counted)
			if e.nmr.SideInited {
				e.nmr.SideEMA += 0.125 * (side - e.nmr.SideEMA)
			} else {
				e.nmr.SideEMA = side
				e.nmr.SideInited = true
			}
		}
	}

	// Qavg accounting (aacenc.c:1412-1413): the NMR servo's operating
	// lambda when it is live, the rate-loop lambda otherwise.
	if e.nmr != nil && e.nmr.LamRC > 0.0 {
		e.stats.LambdaSum += float64(e.nmr.LamRC)
	} else {
		e.stats.LambdaSum += float64(e.lambda)
	}
	e.stats.LambdaCount++

	e.frameNum++
	e.remainingSamples -= frameSize
	return append(dst, payload...), nil
}

// accumulateStats folds this frame's final per-band decisions into the
// tool-usage counters. Mirrors the stats block of aac_encode_frame
// (aacenc.c:1352-1386 @ d09d5afc3a), restricted to the single SCE/CPE
// element of the v1 channel maps.
func (e *Encoder) accumulateStats(chans int) {
	cpe := &e.cpe
	for ch := range chans {
		isShort := cpe.Ch[ch].ICS.WindowSequence[0] == coder.EightShortSequence
		e.stats.Chans++
		if isShort {
			e.stats.Short++
		}
		if cpe.Ch[ch].TNS.Present {
			if isShort {
				e.stats.TNSShort++
			} else {
				e.stats.TNSLong++
			}
		}
	}
	ics := &cpe.Ch[0].ICS
	for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
		for g := range ics.NumSwb {
			idx := w*16 + g
			coded := false
			for ch := range chans {
				sce := &cpe.Ch[ch]
				if sce.Zeroes[idx] && sce.BandType[idx] == 0 {
					continue
				}
				e.stats.ChBands++
				if sce.BandType[idx] == coder.NoiseBT {
					e.stats.PNS++
				}
				coded = true
			}
			if chans == 2 && coded {
				e.stats.CPEBands++
				if cpe.MsMask[idx] {
					e.stats.MS++
				}
				if cpe.IsMask[idx] {
					e.stats.IS++
				}
			}
		}
	}
}

// Stats returns a snapshot of the tool-usage counters accumulated since
// New or Reset. Mirrors the AACEncContext stat_* fields and Qavg
// accounting (aacenc.h:232-239 @ d09d5afc3a).
func (e *Encoder) Stats() Stats { return e.stats }

// analyzeWindowAndTransform runs the per-channel window decision, ICS
// setup, clipping evaluation, windowing + MDCT and the NaN guard for one
// channel. Mirrors the per-channel head of aac_encode_frame
// (aacenc.c:1046-1126) @ d09d5afc3a.
func (e *Encoder) analyzeWindowAndTransform(ch int, flush bool) error {
	sce := &e.cpe.Ch[ch]
	ics := &sce.ICS
	overlap := e.planarSamples[ch][:]

	var la []float32
	if !flush {
		la = overlap[frameSize+448+64:]
	}
	wi := &e.windows[ch]
	*wi = e.psy.Window(la, ch, ics.WindowSequence[0])

	ics.WindowSequence[1] = ics.WindowSequence[0]
	ics.WindowSequence[0] = wi.WindowType[0]
	ics.UseKBWindow[1] = ics.UseKBWindow[0]
	ics.UseKBWindow[0] = wi.WindowShape
	ics.NumWindows = wi.NumWindows
	short := b2i(ics.NumWindows == 8)
	if short == 1 {
		ics.SwbSizes = tables.SwbSize128[e.samplerateIndex]
		ics.SwbOffset = tables.SwbOffset128[e.samplerateIndex]
		ics.TnsMaxBands = int(tables.TNSMaxBands128[e.samplerateIndex])
	} else {
		ics.SwbSizes = tables.SwbSize1024[e.samplerateIndex]
		ics.SwbOffset = tables.SwbOffset1024[e.samplerateIndex]
		ics.TnsMaxBands = int(tables.TNSMaxBands1024[e.samplerateIndex])
	}
	ics.NumSwb = e.psy.NumBands[short]
	ics.MaxSfb = min(ics.MaxSfb, ics.NumSwb)
	for w := range ics.NumWindows {
		ics.GroupLen[w] = wi.Grouping[w]
	}

	// Input sample maximums and clipping risk (aacenc.c:1091-1115).
	var clipAvoidance float32
	wlen := 2048 / ics.NumWindows
	for w := range ics.NumWindows {
		wbuf := overlap[w*128:]
		var maxAbs float32
		for j := range wlen {
			maxAbs = max(maxAbs, absf(wbuf[j]))
		}
		if maxAbs > clipAvoidanceFactor {
			ics.WindowClipping[w] = true
			clipAvoidance = max(clipAvoidance, maxAbs)
		} else {
			ics.WindowClipping[w] = false
		}
	}
	if clipAvoidance > clipAvoidanceFactor {
		ics.ClipAvoidanceFactor = clipAvoidanceFactor / clipAvoidance
	} else {
		ics.ClipAvoidanceFactor = 1.0
	}

	e.applyWindowAndMDCT(ch)

	// NaN/Inf guard (aacenc.c:1119-1124): the !(x < 1e16) form catches NaN.
	for k := range 1024 {
		v := sce.Coeffs[k]
		if v < 0 {
			v = -v
		}
		if !(v < 1e16) {
			return ErrInvalidAudio
		}
	}
	e.avoidClipping(sce)
	return nil
}

// encodeFrameRateLoop runs the do/while rate-control loop of
// aac_encode_frame (aacenc.c:1131-1351) @ d09d5afc3a for one SCE or CPE
// element: psy analysis, quantizer search, bitstream write and the lambda
// feedback (ABR with sqrt damping, or strict CBR). The complexity waiver
// covers a faithful port of the C rate loop; splitting it would break the
// line-by-line mapping to the pinned source (docs/porting-guide.md ground
// rule 1).
//
//nolint:gocognit,gocyclo // faithful port of one C loop, see doc comment
func (e *Encoder) encodeFrameRateLoop(chans int) {
	cpe := &e.cpe
	tag := coder.TypeSCE
	if chans == 2 {
		tag = coder.TypeCPE
	}
	e.isMode, e.msMode, e.tnsMode, e.predMode = 0, 0, 0, 0

	its := 0
	for {
		e.pb.Reset(e.pbBuf)
		// put_bitstream_info is skipped: BITEXACT behavior, always valid
		// (docs/architecture.md bitrate/quality notes).
		targetBits := 0
		cpe.CommonWindow = 0
		for i := range 128 {
			cpe.MsMask[i] = false
			cpe.IsMask[i] = false
		}
		e.pb.Put(3, uint32(tag))
		e.pb.Put(4, 0) // element_instance_tag

		for ch := range chans {
			sce := &cpe.Ch[ch]
			sce.TNS = coder.TemporalNoiseShaping{} // aacenc.c:1154 memset
			for w := range 128 {
				if sce.BandType[w] > coder.ReservedBT {
					sce.BandType[w] = 0
				}
			}
		}
		e.psy.Bitres.Alloc = -1
		e.psy.Bitres.Bits = e.lastFramePBCount / e.cfg.Channels
		e.psy.Analyze(e.frameNum, 0, e.coeffPtrs[:chans], e.windows[:chans])
		if e.psy.Bitres.Alloc > 0 {
			// Lambda unused here on purpose; the psy's unscaled allocation
			// is wanted (aacenc.c:1162-1167).
			targetBits += int(float32(e.psy.Bitres.Alloc) * (e.lambda / 120))
			e.psy.Bitres.Alloc /= chans
		}
		if chans > 1 &&
			e.windows[0].WindowType[0] == e.windows[1].WindowType[0] &&
			e.windows[0].WindowShape == e.windows[1].WindowShape {
			cpe.CommonWindow = 1
			for w := range e.windows[0].NumWindows {
				if e.windows[0].Grouping[w] != e.windows[1].Grouping[w] {
					cpe.CommonWindow = 0
					break
				}
			}
		}

		useTNS := !e.cfg.DisableTNS

		// The NMR coder rate-controls itself and never re-quantizes, so
		// TNS must run BEFORE the quantizer; with twoloop/fast it runs
		// after (aacenc.c:1185-1201, docs/porting-guide.md pitfall 3).
		tnsFirst := e.cfg.Coder == CoderNMR
		if tnsFirst && useTNS {
			for ch := range chans {
				sce := &cpe.Ch[ch]
				// mono: mark_pns before TNS so the region cap sees PNS
				// bands. Stereo PNS is marked below after the stereo
				// decision (aacenc.c:1192-1195).
				if chans == 1 && !e.cfg.DisablePNS {
					if e.trace != nil {
						e.trace = append(e.trace, "mark_pns")
					}
					e.cd.MarkPNS(e.cfg.SampleRate, e.bandwidth, sce,
						&e.psy.Ch[ch].PsyBands, e.lambda)
				}
				if e.trace != nil {
					e.trace = append(e.trace, "search_for_tns")
				}
				e.cd.SearchForTNS(e.samplerateIndex, sce, &e.psy.Ch[ch].PsyBands)
				if e.trace != nil {
					e.trace = append(e.trace, "apply_tns_filt")
				}
				coder.ApplyTNS(sce)
				if sce.TNS.Present {
					e.tnsMode = 1
				}
			}
		}

		// NMR stereo PNS (imaging-safe): mark each channel's noise-like
		// bands on the original L/R psy, then keep PNS only where BOTH
		// channels are noise-like (aacenc.c:1203-1212).
		if chans == 2 && cpe.CommonWindow != 0 && tnsFirst && !e.cfg.DisablePNS {
			if e.trace != nil {
				e.trace = append(e.trace, "mark_pns", "mark_pns")
			}
			e.cd.MarkPNS(e.cfg.SampleRate, e.bandwidth, &cpe.Ch[0],
				&e.psy.Ch[0].PsyBands, e.lambda)
			e.cd.MarkPNS(e.cfg.SampleRate, e.bandwidth, &cpe.Ch[1],
				&e.psy.Ch[1].PsyBands, e.lambda)
			for b := range 128 {
				if !cpe.Ch[0].CanPNS[b] || !cpe.Ch[1].CanPNS[b] {
					cpe.Ch[0].CanPNS[b] = false
					cpe.Ch[1].CanPNS[b] = false
				}
			}
		}

		// The NMR coder decides I/S and M/S BEFORE quantization, from the
		// psy model (aacenc.c:1214-1220).
		if chans == 2 && cpe.CommonWindow != 0 && e.cfg.Coder == CoderNMR {
			nmrDecideStereo(stereoInput{
				sampleRate:      e.cfg.SampleRate,
				bitRate:         e.cfg.Bitrate,
				channels:        e.cfg.Channels,
				midSide:         -1, // upstream default (auto)
				intensityStereo: true,
				rcFill:          e.nmr.RCFill,
				haveNMR:         true,
			}, cpe, &e.psy.Ch[0].PsyBands, &e.psy.Ch[1].PsyBands)
		}

		for ch := range chans {
			// non-NMR coders mark PNS candidacy just before the search
			// (aacenc.c:1223-1225); the NMR path marked it above.
			if !e.cfg.DisablePNS && !tnsFirst {
				if e.trace != nil {
					e.trace = append(e.trace, "mark_pns")
				}
				e.cd.MarkPNS(e.cfg.SampleRate, e.bandwidth, &cpe.Ch[ch],
					&e.psy.Ch[ch].PsyBands, e.lambda)
			}
			if e.trace != nil {
				e.trace = append(e.trace, "search_for_quantizers")
			}
			switch e.cfg.Coder {
			case CoderNMR:
				in := &coder.NMRInput{
					BitRate:          e.cfg.Bitrate,
					SampleRate:       e.cfg.SampleRate,
					Channels:         e.cfg.Channels,
					FrameNum:         e.frameNum, // == C avctx->frame_num: 1 on the first packet (both prime frame 0)
					BitresAlloc:      e.psy.Bitres.Alloc,
					Bandwidth:        e.bandwidth,
					CurChannel:       ch,
					Speed:            e.cfg.NMRSpeed,
					RateControlOK:    !e.cfg.StrictBitrate,
					QScaleChannels:   e.cfg.Channels,
					LastFramePBCount: e.lastFramePBCount,
				}
				e.cd.SearchForQuantizersNMR(in, e.nmr, &cpe.Ch[ch],
					&e.psy.Ch[ch].PsyBands, e.lambda)
			case CoderTwoLoop:
				e.cd.SearchForQuantizersTwoLoop(e.cfg.Bitrate, e.cfg.SampleRate,
					e.cfg.Channels, e.psy.Bitres.Alloc, e.bandwidth,
					!e.cfg.DisablePNS, &cpe.Ch[ch], &e.psy.Ch[ch].PsyBands, e.lambda)
			case CoderFast:
				e.cd.SearchForQuantizersFast(e.cfg.Bitrate, e.cfg.SampleRate, chans,
					&cpe.Ch[ch], &e.psy.Ch[ch].PsyBands, e.lambda)
			default:
				// Unreachable: Reset rejects any coder outside the enum. Guard the
				// invariant instead of silently quantizing with the fast path.
				e.fail(fmt.Errorf("enc: unsupported coder kind %d", e.cfg.Coder))
				return
			}
		}
		for ch := range chans { // TNS (non-NMR) and PNS (aacenc.c:1228-1239)
			sce := &cpe.Ch[ch]
			if !tnsFirst && useTNS {
				if e.trace != nil {
					e.trace = append(e.trace, "search_for_tns")
				}
				e.cd.SearchForTNS(e.samplerateIndex, sce, &e.psy.Ch[ch].PsyBands)
				if e.trace != nil {
					e.trace = append(e.trace, "apply_tns_filt")
				}
				coder.ApplyTNS(sce)
				if sce.TNS.Present {
					e.tnsMode = 1
				}
			}
			// search_for_pns is NULL for the NMR coder (PNS is decided in
			// the trellis); twoloop and fast run it here.
			if !e.cfg.DisablePNS && e.cfg.Coder != CoderNMR {
				if e.trace != nil {
					e.trace = append(e.trace, "search_for_pns")
				}
				e.cd.SearchForPNS(e.cfg.SampleRate, e.bandwidth, sce,
					&e.psy.Ch[ch].PsyBands, e.lambda)
			}
		}
		// Intensity stereo (aacenc.c:1241-1248): the NMR path decided it
		// pre-search in nmrDecideStereo.
		if !e.cfg.DisableIS {
			if e.cfg.Coder != CoderNMR {
				if e.trace != nil {
					e.trace = append(e.trace, "search_for_is")
				}
				e.cd.SearchForIS(e.cfg.SampleRate, cpe,
					&e.psy.Ch[0].PsyBands, &e.psy.Ch[min(chans-1, 1)].PsyBands, e.lambda)
				applyIntensityStereo(cpe)
			}
			if cpe.IsMode {
				e.isMode = 1
			}
		}
		// Mid/side stereo (aacenc.c:1249-1255): mid_side is -1 (auto) so
		// the search runs; the forced-all mode (aac_ms 1) is unreachable.
		if !e.cfg.DisableMS && e.cfg.Coder != CoderNMR {
			if e.trace != nil {
				e.trace = append(e.trace, "search_for_ms")
			}
			e.cd.SearchForMS(cpe, &e.psy.Ch[0].PsyBands,
				&e.psy.Ch[min(chans-1, 1)].PsyBands, e.lambda)
			applyMidSideStereo(cpe)
		}
		e.adjustFrameInformation(cpe, chans)
		if chans == 2 {
			e.pb.Put(1, uint32(cpe.CommonWindow))
			if cpe.CommonWindow != 0 {
				e.putIcsInfo(&cpe.Ch[0].ICS)
				e.encodeMSInfo(cpe)
				if cpe.MsMode != 0 {
					e.msMode = 1
				}
			}
		}
		for ch := range chans {
			e.encodeIndividualChannel(&cpe.Ch[ch], cpe.CommonWindow != 0)
		}

		frameBits := e.pb.Count()

		// The NMR coder rate-controls itself (global-lambda reservoir
		// servo): skip the lambda rate loop and only intervene on a hard
		// overflow (aacenc.c:1279-1284).
		if e.cfg.Coder == CoderNMR && !e.cfg.StrictBitrate &&
			frameBits < maxFrameBits*chans-3 {
			return
		}

		// Rate control (aacenc.c:1286-1350): allow between the nominal
		// bitrate and what psy's bit reservoir says to target, but drift
		// towards the nominal bitrate always.
		rateBits := e.cfg.Bitrate * 1024 / e.cfg.SampleRate
		rateBits = min(rateBits, maxFrameBits*chans-3)
		tooManyBits := max(targetBits, rateBits)
		tooManyBits = min(tooManyBits, maxFrameBits*chans-3)
		tooFewBits := min(max(rateBits-rateBits/4, targetBits), tooManyBits)

		if e.cfg.StrictBitrate { // bit_rate_tolerance == 0 (aacenc.c:1297)
			if rateBits < frameBits {
				// The C spins here forever when the target is unachievable:
				// aacenc.c:1297-1305 sits inside `do { ... } while (1)` with no
				// lambda floor and no iteration cap, and its `its++` lives in the
				// ABR branch, not this one. Once lambda underflows, the quantizer
				// cannot shrink the frame any further, rate_bits < frame_bits stays
				// true, and the encoder hangs. Reproduced here before fixing: at
				// 1000 bps, 44100 Hz, StrictBitrate, EncodeFrame never returns.
				//
				// Reproducing an upstream *output* quirk is the rule in this port;
				// reproducing an upstream *hang* is not, so this terminates instead.
				// The deviation is output-preserving: lambda only reaches the floor
				// on inputs where the C never terminates, so every bitstream the C
				// can actually produce is unchanged.
				if e.lambda <= fltEpsilon {
					e.fail(fmt.Errorf("enc: strict bitrate of %d bps unachievable at %d Hz: "+
						"budget is %d bits/frame but the smallest frame the quantizer reaches is %d",
						e.cfg.Bitrate, e.cfg.SampleRate, rateBits, frameBits))
					return
				}
				ratio := float32(rateBits) / float32(frameBits)
				e.lambda *= min(0.9, ratio)
				continue
			}
			e.lambda = 120 // reset lambda when solution is found
			return
		}

		// ABR: be strict, but only for increasing (aacenc.c:1309).
		tooFewBits -= tooFewBits / 8
		tooManyBits += tooManyBits / 2

		if its == 0 || // for steady-state Q-scale tracking
			(its < 5 && (frameBits < tooFewBits || frameBits > tooManyBits)) ||
			frameBits >= maxFrameBits*chans-3 {
			ratio := float32(rateBits) / float32(frameBits)
			if frameBits >= tooFewBits && frameBits <= tooManyBits {
				// steady state: adjust lambda slowly
				ratio = sqrtf(sqrtf(ratio))
				ratio = min(max(ratio, 0.9), 1.1)
			} else {
				ratio = sqrtf(ratio)
			}
			prevLambda := e.lambda
			e.lambda = min(max(e.lambda*ratio, fltEpsilon), 65536.0)
			if ratio > 0.9 && ratio < 1.1 {
				return
			}
			// Restore coeffs from pcoeffs when tools modified them
			// (aacenc.c:1337-1345); no tool is active in Phase 2 so the
			// flags stay zero, but the seam is the C's.
			if e.isMode != 0 || e.msMode != 0 || e.tnsMode != 0 || e.predMode != 0 {
				for ch := range chans {
					cpe.Ch[ch].Coeffs = cpe.Ch[ch].PCoeffs
				}
			}
			// Only the `frameBits >= maxFrameBits*chans-3` arm can retry without
			// bound: the other two are capped by its<5. So the loop can only spin
			// forever while the frame is stuck at the buffer ceiling AND lambda has
			// stopped moving -- pinned at one of its clamps, so the next pass would
			// quantize identically, land on the identical frameBits, take the
			// identical branch, and pin lambda again. The C spins there
			// (aacenc.c:1309-1350, inside do{}while(1)).
			//
			// BOTH halves of the condition are load-bearing. A pinned lambda alone is
			// routine: at a comfortable bitrate the frame comes in well under budget,
			// ratio > 1 drives lambda to the 65536 ceiling, and it sits there while
			// its<5 ends the loop. Guarding on the pin alone rejects ordinary 128 kbps
			// encodes -- the C differential gate caught exactly that.
			if e.lambda == prevLambda && frameBits >= maxFrameBits*chans-3 {
				e.fail(fmt.Errorf("enc: rate control cannot converge at %d bps, %d Hz: "+
					"lambda is pinned at %g and the frame stays at %d bits, at the %d-bit ceiling",
					e.cfg.Bitrate, e.cfg.SampleRate, e.lambda, frameBits, maxFrameBits*chans-3))
				return
			}
			its++
		} else {
			return
		}
	}
}

// fail records the first internal-invariant violation seen while writing the
// bitstream. Mirrors the av_assert0 sites in aacenc.c, which abort() the
// process; a library returns an error instead.
func (e *Encoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// Drained reports whether a flush has consumed all queued samples; once
// true, EncodeFrame(dst, nil) returns dst unchanged.
func (e *Encoder) Drained() bool { return e.remainingSamples <= 0 }

// copyInputSamples shifts the 3x1024 planar buffers one frame left and
// loads the new samples into the lookahead third, zero-padding short and
// flush frames. Mirrors aacenc.c:copy_input_samples @ d09d5afc3a (the
// mono/stereo reorder map is the identity).
func (e *Encoder) copyInputSamples(samples [][]float32) {
	for ch := range e.cfg.Channels {
		end := 2048
		copy(e.planarSamples[ch][1024:2048], e.planarSamples[ch][2048:3072])
		if samples != nil {
			end += len(samples[ch])
			copy(e.planarSamples[ch][2048:end], samples[ch])
		}
		clear(e.planarSamples[ch][end:])
	}
}

// applyWindowAndMDCT windows planarSamples[ch][0:2048] with the sequence
// selected by the window decision and transforms into sce.Coeffs (one
// mdct1024 or eight mdct128), retaining the windowed time signal in
// RetBuf and the pristine spectrum in PCoeffs. Mirrors
// aacenc.c:apply_window_and_mdct (aacenc.c:447-462) @ d09d5afc3a.
func (e *Encoder) applyWindowAndMDCT(ch int) {
	sce := &e.cpe.Ch[ch]
	audio := e.planarSamples[ch][:]
	out := sce.RetBuf[:]

	applyWindow(sce.ICS.WindowSequence[0], sce.ICS.UseKBWindow[0],
		sce.ICS.UseKBWindow[1], out, audio)

	if sce.ICS.WindowSequence[0] != coder.EightShortSequence {
		e.mdct1024.Transform(sce.Coeffs[:], out)
	} else {
		for i := 0; i < 1024; i += 128 {
			e.mdct128.Transform(sce.Coeffs[i:i+128], out[i*2:i*2+256])
		}
	}
	copy(audio[0:1024], audio[1024:2048])
	sce.PCoeffs = sce.Coeffs
}

// applyWindow applies the per-sequence analysis window pair to 2048 input
// samples. Mirrors the four apply_*_window variants (aacenc.c:382-436)
// @ d09d5afc3a, including the eight_short choice that window 0's left
// half uses the current shape while windows 1-7 use the previous shape.
func applyWindow(seq, kb0, kb1 int, out, audio []float32) {
	switch seq {
	case coder.OnlyLongSequence:
		dsp.VectorFMul(out[:1024], audio[:1024], pickWindow(kb0, window.KBDLong1024, window.Sine1024))
		dsp.VectorFMulReverse(out[1024:2048], audio[1024:2048], pickWindow(kb1, window.KBDLong1024, window.Sine1024))
	case coder.LongStartSequence:
		dsp.VectorFMul(out[:1024], audio[:1024], pickWindow(kb1, window.KBDLong1024, window.Sine1024))
		copy(out[1024:1472], audio[1024:1472])
		dsp.VectorFMulReverse(out[1472:1600], audio[1472:1600], pickWindow(kb0, window.KBDShort128, window.Sine128))
		clear(out[1600:2048])
	case coder.LongStopSequence:
		clear(out[:448])
		dsp.VectorFMul(out[448:576], audio[448:576], pickWindow(kb1, window.KBDShort128, window.Sine128))
		copy(out[576:1024], audio[576:1024])
		dsp.VectorFMulReverse(out[1024:2048], audio[1024:2048], pickWindow(kb0, window.KBDLong1024, window.Sine1024))
	case coder.EightShortSequence:
		swindow := pickWindow(kb0, window.KBDShort128, window.Sine128)
		pwindow := pickWindow(kb1, window.KBDShort128, window.Sine128)
		for w := range 8 {
			in := audio[448+w*128:]
			o := out[2*w*128:]
			first := swindow
			if w != 0 {
				first = pwindow
			}
			dsp.VectorFMul(o[:128], in[:128], first)
			dsp.VectorFMulReverse(o[128:256], in[128:256], swindow)
		}
	}
}

func pickWindow(kb int, k, s []float32) []float32 {
	if kb != 0 {
		return k
	}
	return s
}

// avoidClipping downscales the spectrum of near-clipping windows.
// Mirrors aacenc.c:avoid_clipping (aacenc.c:928-943) @ d09d5afc3a.
func (e *Encoder) avoidClipping(sce *coder.SingleChannelElement) {
	ics := &sce.ICS
	if ics.ClipAvoidanceFactor < 1.0 {
		for w := range ics.NumWindows {
			start := 0
			for i := range ics.MaxSfb {
				swbCoeffs := sce.Coeffs[start+w*128:]
				for j := range int(ics.SwbSizes[i]) {
					swbCoeffs[j] *= ics.ClipAvoidanceFactor
				}
				start += int(ics.SwbSizes[i])
			}
		}
	}
}

// adjustFrameInformation derives max_sfb from the zero flags, reconciles
// zero flags across window groups and, for a common-window pair, aligns
// max_sfb across the two channels. Mirrors
// aacenc.c:adjust_frame_information (aacenc.c:503-549) @ d09d5afc3a.
func (e *Encoder) adjustFrameInformation(cpe *coder.ChannelElement, chans int) {
	for ch := range chans {
		sce := &cpe.Ch[ch]
		ics := &sce.ICS
		maxsfb := 0
		for w := 0; w < ics.NumWindows; w += ics.GroupLen[w] {
			cmaxsfb := ics.NumSwb
			for cmaxsfb > 0 && sce.Zeroes[w*16+cmaxsfb-1] {
				cmaxsfb--
			}
			maxsfb = max(maxsfb, cmaxsfb)
		}
		ics.MaxSfb = maxsfb

		// adjust zero bands for window groups
		ics.EachBand(ics.MaxSfb, func(w, g, idx int) {
			allZero := true
			for w2 := w; w2 < w+ics.GroupLen[w]; w2++ {
				if !sce.Zeroes[w2*16+g] {
					allZero = false
					break
				}
			}
			sce.Zeroes[idx] = allZero
		})
	}

	if chans > 1 && cpe.CommonWindow != 0 {
		ics0 := &cpe.Ch[0].ICS
		ics1 := &cpe.Ch[1].ICS
		msc := 0
		ics0.MaxSfb = max(ics0.MaxSfb, ics1.MaxSfb)
		ics1.MaxSfb = ics0.MaxSfb
		for w := 0; w < ics0.NumWindows*16; w += 16 {
			for i := range ics0.MaxSfb {
				if cpe.MsMask[w+i] {
					msc++
				}
			}
		}
		switch {
		case msc == 0 || ics0.MaxSfb == 0:
			cpe.MsMode = 0
		case msc < ics0.MaxSfb*ics0.NumWindows:
			cpe.MsMode = 1
		default:
			cpe.MsMode = 2
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func absf(x float32) float32 { return max(x, -x) }

// sqrtf keeps the rate loop reading like the C source it mirrors.
func sqrtf(x float32) float32 { return fmath.Sqrt32(x) }
