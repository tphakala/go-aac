package quality

import (
	"math"
	"slices"
)

// Tunables of the Go-native metrics. The exported ones appear in reports so
// a reader knows which definitions the numbers were computed under.
const (
	// SNRCap is the SNR reported when the error energy is zero or below
	// epsilon times the signal energy (identical or numerically identical
	// signals).
	SNRCap = 120.0
	// BandLimitHz bounds the STFT bins BandSNR and LSD are computed over, so
	// an encoder's deliberate high-frequency lowpass at lower bitrates is not
	// counted as noise there (full-band SNR still sees it). At 48 kHz the NMR
	// coder codes to 16000 Hz and twoloop/fast to 16600 Hz, so a 16 kHz limit
	// keeps both spectral figures blind to the shelf. At 64 kbps stereo (32
	// kbps per channel) the NMR cutoff drops near 14 kHz, so BandSNR does see
	// part of the shelf there; that is accepted and identical for both encoders.
	BandLimitHz = 16000.0
	// SegSNRMin and SegSNRMax clamp per-segment SNR (the segmental-SNR
	// convention) so one silent or one destroyed segment cannot dominate
	// the mean.
	SegSNRMin = -10.0
	SegSNRMax = 35.0
	// SegSNRSegment is the segmental-SNR segment length in samples: one
	// AAC-LC frame.
	SegSNRSegment = 1024
	// BandwidthFloorDB is how far below the long-term PSD peak a bin may sit
	// and still count as "in band".
	BandwidthFloorDB = 50.0

	stftSize    = 2048
	stftHop     = 1024
	activeFloor = 1e-6 // mean-square floor for an "active" segment or frame (-60 dBFS)
	epsilon     = 1e-12

	preEchoWindowMs  = 5    // attack-detection window
	preEchoPreMs     = 20   // pre-attack window the error is integrated over
	preEchoRatio     = 10.0 // window-to-previous-window energy ratio that marks an attack
	preEchoFloor     = 1e-4 // mean-square an attack window must exceed (-40 dBFS)
	preEchoHoldoffMs = 50   // minimum spacing between counted attacks
)

// Metrics is one channel-averaged measurement of a decoded signal against
// its reference. Directions: SNR, BandSNR, SegSNR higher is better; LSD and
// PreEcho lower is better; Bandwidth is informational.
type Metrics struct {
	SNR       float64 // dB, full band, capped at SNRCap
	BandSNR   float64 // dB, STFT bins at or below BandLimitHz
	SegSNR    float64 // dB, mean per-segment SNR clamped to [SegSNRMin, SegSNRMax]; NaN when no active segment
	LSD       float64 // dB, mean log-spectral distance over active frames; NaN when no active frame
	PreEcho   float64 // dB, pre-attack error energy relative to attack energy; NaN when no channel had an attack
	PreEchoN  int     // total attacks over the attack-bearing channels (a count, not PreEcho's channel-averaging denominator); >0 iff PreEcho is finite
	Bandwidth float64 // Hz, highest long-term PSD bin within BandwidthFloorDB of the peak
}

// Compare measures deg against ref (planar channels of equal length, already
// aligned) and averages every metric over channels, except Bandwidth, which
// is the maximum over channels, and PreEchoN, which is the total. For SNR,
// BandSNR, SegSNR, and LSD a NaN channel value propagates: those NaNs mark a
// silent (no-data) reference channel, so a metric undefined on one channel is
// undefined for the pair. PreEcho is the exception: a channel with no detected
// attack (its NaN means "no transient here", not "no data") is skipped rather
// than propagated, so PreEcho is the mean over the channels that had attacks
// and PreEchoN is the total over those same channels. PreEcho is NaN with
// PreEchoN 0 only when no channel had an attack; PreEchoN > 0 iff PreEcho is
// finite.
func Compare(ref, deg [][]float64, sampleRate int) Metrics {
	var m Metrics
	if len(ref) == 0 || len(deg) != len(ref) {
		return Metrics{SNR: math.NaN(), BandSNR: math.NaN(), SegSNR: math.NaN(),
			LSD: math.NaN(), PreEcho: math.NaN()}
	}
	nch := float64(len(ref))
	// PreEcho is averaged only over the channels that actually had a detected
	// attack, not poisoned to NaN by a channel that had none: see the field
	// comment and preSum/preChans below.
	var preSum float64
	var preChans int
	for c := range ref {
		m.SNR += SNR(ref[c], deg[c]) / nch
		m.SegSNR += SegmentalSNR(ref[c], deg[c], SegSNRSegment) / nch
		// compareSpectra yields band-limited SNR, LSD, and bandwidth from a
		// single STFT pass; its results are numerically equal to SpectralMetrics
		// plus Bandwidth called separately, to within floating-point
		// reassociation (which the grid ran as four STFT passes per channel,
		// transforming deg twice).
		b, l, bw := compareSpectra(ref[c], deg[c], sampleRate)
		m.BandSNR += b / nch
		m.LSD += l / nch
		m.Bandwidth = max(m.Bandwidth, bw)
		// PreEcho returns (NaN, 0) for a channel with no attack. Skipping those
		// channels averages PreEcho over the attack-bearing channels only (the
		// denominator is preChans, the channel count, not PreEchoN), and sums
		// their attack counts into PreEchoN: PreEchoN > 0 iff PreEcho is finite.
		if p, ev := PreEcho(ref[c], deg[c], sampleRate); ev > 0 {
			preSum += p
			preChans++
			m.PreEchoN += ev
		}
	}
	if preChans > 0 {
		m.PreEcho = preSum / float64(preChans)
	} else {
		m.PreEcho = math.NaN()
	}
	return m
}

// energy returns the energy (sum of squares) of x over [lo, hi).
func energy(x []float64, lo, hi int) float64 {
	var e float64
	for _, v := range x[lo:hi] {
		e += v * v
	}
	return e
}

// energies returns the reference and error energies over [lo, hi).
func energies(ref, deg []float64, lo, hi int) (sig, errE float64) {
	for i := lo; i < hi; i++ {
		d := ref[i] - deg[i]
		sig += ref[i] * ref[i]
		errE += d * d
	}
	return sig, errE
}

// snrFrom converts energies to dB with the cap. A reference with no energy
// at all yields NaN rather than the cap: there was nothing to measure, and
// reporting the top of the scale for that would turn "no data" into "perfect"
// (the sibling metrics all return NaN for the same degenerate input).
func snrFrom(sig, errE float64) float64 {
	if sig <= 0 {
		return math.NaN()
	}
	if errE == 0 || errE <= epsilon*sig {
		return SNRCap
	}
	return min(SNRCap, 10*math.Log10(sig/errE))
}

// SNR is the full-band signal-to-noise ratio of deg against ref in dB over
// their common length.
func SNR(ref, deg []float64) float64 {
	n := min(len(ref), len(deg))
	sig, errE := energies(ref, deg, 0, n)
	return snrFrom(sig, errE)
}

// SegmentalSNR is the mean over active seg-sample segments (reference
// mean-square above the active floor) of the per-segment SNR clamped to
// [SegSNRMin, SegSNRMax]. It returns NaN when no segment is active.
func SegmentalSNR(ref, deg []float64, seg int) float64 {
	if seg <= 0 {
		return math.NaN() // a non-positive segment length would not terminate
	}
	n := min(len(ref), len(deg))
	var sum float64
	var count int
	for lo := 0; lo+seg <= n; lo += seg {
		sig, errE := energies(ref, deg, lo, lo+seg)
		if sig/float64(seg) < activeFloor {
			continue
		}
		sum += max(SegSNRMin, min(SegSNRMax, snrFrom(sig, errE)))
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// stftWindow is the Hann window for the fixed stftSize, computed once. It is
// read-only after init, so concurrent callers (the harness scores cases in
// parallel) share it safely.
var stftWindow = hannWindow(stftSize)

// stftFrames calls fn with the Hann-windowed power spectrum (bins
// 0..stftSize/2, unnormalized FFT) of every full stftSize frame of x at hop
// stftHop, and returns the number of frames visited. The power slice is
// reused between calls; fn must copy what it keeps.
func stftFrames(x []float64, fn func(power []float64)) int {
	w := stftWindow
	re := make([]float64, stftSize)
	im := make([]float64, stftSize)
	power := make([]float64, stftSize/2+1)
	frames := 0
	for lo := 0; lo+stftSize <= len(x); lo += stftHop {
		for i := range stftSize {
			re[i] = x[lo+i] * w[i]
			im[i] = 0
		}
		fft(re, im)
		for k := range power {
			power[k] = re[k]*re[k] + im[k]*im[k]
		}
		fn(power)
		frames++
	}
	return frames
}

// bandBins returns the number of STFT bins (from DC) at or below
// BandLimitHz at sampleRate, capped at the Nyquist bin.
func bandBins(sampleRate int) int {
	return min(stftSize/2, int(BandLimitHz*stftSize/float64(sampleRate))) + 1
}

// SpectralMetrics returns the band-limited SNR (bins at or below
// BandLimitHz, all frames pooled) and the mean log-spectral distance over
// active frames (in-band reference power above the active floor), both in
// dB. LSD is NaN when no frame is active.
//
// It is the per-figure reference implementation that the fused compareSpectra
// (used in production, via Compare) is validated against; production computes
// these figures through compareSpectra, not through this function.
func SpectralMetrics(ref, deg []float64, sampleRate int) (bandSNR, lsd float64) {
	n := min(len(ref), len(deg))
	ref, deg = ref[:n], deg[:n]
	nb := bandBins(sampleRate)

	var refPow [][]float64
	stftFrames(ref, func(p []float64) {
		cp := make([]float64, nb)
		copy(cp, p[:nb])
		refPow = append(refPow, cp)
	})

	errSig := make([]float64, n)
	for i := range errSig {
		errSig[i] = ref[i] - deg[i]
	}
	if len(refPow) == 0 {
		// Shorter than one STFT frame: nothing was transformed, so neither
		// figure is defined. Returning the SNR cap here would report a
		// too-short input as a perfect match.
		return math.NaN(), math.NaN()
	}

	var sig, errE float64
	f := 0
	stftFrames(errSig, func(p []float64) {
		for k := range nb {
			sig += refPow[f][k]
			errE += p[k]
		}
		f++
	})
	bandSNR = snrFrom(sig, errE)

	// Active-frame floor in the unnormalized power domain: the in-band power
	// sum of band-limited white noise at the activeFloor mean-square, i.e.
	// activeFloor times the periodic Hann energy (3N/8) per bin (Parseval
	// spreads N*E over N bins) times the nb bins summed.
	frameFloor := activeFloor * (3.0 * stftSize / 8) * float64(nb)
	var lsdSum float64
	var active int
	f = 0
	stftFrames(deg, func(p []float64) {
		rp := refPow[f]
		f++
		var frameSig float64
		for _, v := range rp {
			frameSig += v
		}
		if frameSig < frameFloor {
			return
		}
		var acc float64
		for k := range nb {
			d := 10 * math.Log10((rp[k]+epsilon)/(p[k]+epsilon))
			acc += d * d
		}
		lsdSum += math.Sqrt(acc / float64(nb))
		active++
	})
	if active == 0 {
		return bandSNR, math.NaN()
	}
	return bandSNR, lsdSum / float64(active)
}

// compareSpectra computes, in a single STFT pass, the three spectral figures
// Compare needs from one channel pair: band-limited SNR, mean log-spectral
// distance over active frames, and the degraded signal's bandwidth. It is the
// fused equivalent of SpectralMetrics(ref, deg) plus Bandwidth(deg): each
// frame windows ref, deg, and their difference with the same stftWindow and
// transforms them with the same fft in the same frame order, so every
// accumulator sees its summands in the identical sequence and the three
// results are numerically equal to what those two functions return, to within
// floating-point reassociation (this package uses raw libm and is exempt from
// the fmath FMA-determinism boundary, so it carries no bit-exactness
// requirement; see doc.go). Fusing them drops
// the whole-signal error buffer and the per-frame reference-power slices
// SpectralMetrics allocated, and transforms deg once rather than twice.
//
// Like SpectralMetrics, it measures the common leading window
// min(len(ref), len(deg)); Compare's inputs are equal length in practice, so
// the bandwidth here matches Bandwidth(deg) too. They differ only for a caller
// that passes unequal slices, where this measures deg's common window while a
// bare Bandwidth(deg) would also pool deg's trailing frames.
func compareSpectra(ref, deg []float64, sampleRate int) (bandSNR, lsd, bandwidth float64) {
	n := min(len(ref), len(deg))
	ref, deg = ref[:n], deg[:n]
	nb := bandBins(sampleRate)

	w := stftWindow
	refRe := make([]float64, stftSize)
	refIm := make([]float64, stftSize)
	degRe := make([]float64, stftSize)
	degIm := make([]float64, stftSize)
	errRe := make([]float64, stftSize)
	errIm := make([]float64, stftSize)
	refP := make([]float64, stftSize/2+1)
	degP := make([]float64, stftSize/2+1)
	errP := make([]float64, stftSize/2+1)
	degAcc := make([]float64, stftSize/2+1) // long-term deg PSD, for bandwidth

	// The active-frame floor in the unnormalized power domain; see
	// SpectralMetrics for the derivation.
	frameFloor := activeFloor * (3.0 * stftSize / 8) * float64(nb)
	var sig, errE, lsdSum float64
	var frames, active int

	for lo := 0; lo+stftSize <= n; lo += stftHop {
		for i := range stftSize {
			refRe[i], refIm[i] = ref[lo+i]*w[i], 0
			degRe[i], degIm[i] = deg[lo+i]*w[i], 0
			// ref[lo+i]-deg[lo+i] is the same float64 SpectralMetrics formed in
			// its errSig buffer before windowing, so the transformed frame, and
			// thus errP, is identical.
			errRe[i], errIm[i] = (ref[lo+i]-deg[lo+i])*w[i], 0
		}
		fft(refRe, refIm)
		fft(degRe, degIm)
		fft(errRe, errIm)
		for k := range refP {
			refP[k] = refRe[k]*refRe[k] + refIm[k]*refIm[k]
			degP[k] = degRe[k]*degRe[k] + degIm[k]*degIm[k]
			errP[k] = errRe[k]*errRe[k] + errIm[k]*errIm[k]
		}
		frames++

		// Bandwidth pools the deg PSD over EVERY frame, active or not, so this
		// accumulation runs before the active-frame gate below: moving it past
		// the gate would silently drop silent-reference frames from bandwidth.
		for k := range degAcc {
			degAcc[k] += degP[k]
		}
		// Band-limited SNR: in-band reference and error power pooled over all
		// frames, added bin by bin in the order SpectralMetrics used.
		for k := range nb {
			sig += refP[k]
			errE += errP[k]
		}
		// LSD over active frames only (in-band reference power above the floor).
		var frameSig float64
		for k := range nb {
			frameSig += refP[k]
		}
		if frameSig < frameFloor {
			continue
		}
		var acc float64
		for k := range nb {
			d := 10 * math.Log10((refP[k]+epsilon)/(degP[k]+epsilon))
			acc += d * d
		}
		lsdSum += math.Sqrt(acc / float64(nb))
		active++
	}

	if frames == 0 {
		// Shorter than one STFT frame: SpectralMetrics returns NaN for both of
		// its figures, and Bandwidth reports 0.
		return math.NaN(), math.NaN(), 0
	}
	bandSNR = snrFrom(sig, errE)
	lsd = math.NaN()
	if active > 0 {
		lsd = lsdSum / float64(active)
	}
	return bandSNR, lsd, bandwidthFrom(degAcc, sampleRate)
}

// Bandwidth returns the frequency in Hz of the highest bin whose long-term
// average power spectrum lies within BandwidthFloorDB of the spectrum's
// peak. It reports 0 for an all-silent or too-short signal.
//
// It is the per-figure reference implementation that the fused compareSpectra
// (used in production, via Compare) is validated against; production computes
// the degraded signal's bandwidth through compareSpectra, not through this
// function.
func Bandwidth(x []float64, sampleRate int) float64 {
	acc := make([]float64, stftSize/2+1)
	frames := stftFrames(x, func(p []float64) {
		for k := range acc {
			acc[k] += p[k]
		}
	})
	if frames == 0 {
		return 0
	}
	return bandwidthFrom(acc, sampleRate)
}

// bandwidthFrom returns the rolloff frequency in Hz of a long-term power
// spectrum acc (bins 0..stftSize/2): the highest bin within BandwidthFloorDB
// of the peak, or 0 when the spectrum has no energy.
func bandwidthFrom(acc []float64, sampleRate int) float64 {
	peak := 0.0
	for _, v := range acc {
		peak = max(peak, v)
	}
	if peak <= 0 {
		return 0
	}
	floor := peak * math.Pow(10, -BandwidthFloorDB/10)
	for k, v := range slices.Backward(acc) {
		if v >= floor {
			return float64(k) * float64(sampleRate) / stftSize
		}
	}
	return 0
}

// PreEcho detects attacks in ref (a preEchoWindowMs window whose energy
// exceeds preEchoRatio times the previous window's and preEchoFloor in
// mean-square, at least preEchoHoldoffMs after the previous counted attack
// and at least preEchoPreMs into the signal) and, for each, measures the
// error energy in the preEchoPreMs before the attack window relative to the
// attack window's reference energy, in dB. It returns the mean over attacks
// and the attack count; the mean is NaN when no attack is found. Lower is
// better: a clean encoder sits far below 0 dB, audible pre-echo approaches
// it.
func PreEcho(ref, deg []float64, sampleRate int) (meanDB float64, events int) {
	n := min(len(ref), len(deg))
	win := sampleRate * preEchoWindowMs / 1000
	pre := sampleRate * preEchoPreMs / 1000
	holdoff := preEchoHoldoffMs / preEchoWindowMs
	if win == 0 || n < pre+win {
		return math.NaN(), 0
	}
	var sum float64
	prevE := 0.0
	lastAttack := -holdoff
	for k := 0; (k+1)*win <= n; k++ {
		lo := k * win
		e := energy(ref, lo, lo+win)
		isAttack := e > preEchoRatio*prevE && e/float64(win) > preEchoFloor &&
			lo >= pre && k-lastAttack >= holdoff
		prevE = e
		if !isAttack {
			continue
		}
		lastAttack = k
		_, errPre := energies(ref, deg, lo-pre, lo)
		sum += 10 * math.Log10((errPre+epsilon)/(e+epsilon))
		events++
	}
	if events == 0 {
		return math.NaN(), 0
	}
	return sum / float64(events), events
}
