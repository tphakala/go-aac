package quality

import "math"

// FrameSize is the AAC-LC frame size in samples per channel, and
// ClickPeriodFrames and ClickBurstFrames are the click cadence the
// tools/quality corpus drives an encoder's attack detector with. These
// generators are used only by the offline quality harness and its tests, so
// they may call libm transcendentals and are not golden-pinned.
const (
	FrameSize         = 1024
	ClickPeriodFrames = 4
	ClickBurstFrames  = 1
)

// LCG advances the PCG-style linear congruential generator at *seed and
// returns a pseudo-random float64 in the unit interval [0, 1).
func LCG(seed *uint64) float64 {
	*seed = *seed*6364136223846793005 + 1442695040888963407
	return float64(*seed>>11) / float64(1<<53)
}

// LCGSigned advances the same generator as LCG but returns a pseudo-random
// float64 in the signed interval [-1, 1).
func LCGSigned(seed *uint64) float64 {
	return LCG(seed)*2 - 1
}

// MultiTone returns nSamples samples of a deterministic multi-tone program:
// a 440 Hz fundamental plus overtones at -6 dB (880 Hz) and -12 dB (1320
// Hz), scaled so |x[i]| <= peak for every i regardless of phase alignment
// (dividing by the SUM of the three weights bounds the signal pointwise,
// since |sum of sines| <= sum of amplitudes always; the actual peak,
// reached only where the three tones align in phase, is <= this bound).
// chPhase offsets the signal's phase so a second, decorrelated channel can
// be built by calling this with a different chPhase, rather than
// duplicating the same samples across channels.
func MultiTone(sampleRate, nSamples int, chPhase, peak float64) []float64 {
	const f0 = 440.0
	const w1, w2, w3 = 1.0, 0.5011872336272722, 0.251188643150958 // 0, -6, -12 dB
	scale := peak / (w1 + w2 + w3)

	x := make([]float64, nSamples)
	for i := range x {
		t := float64(i) / float64(sampleRate)
		v := w1*math.Sin(2*math.Pi*f0*t+chPhase) +
			w2*math.Sin(2*math.Pi*2*f0*t+chPhase*1.3) +
			w3*math.Sin(2*math.Pi*3*f0*t+chPhase*1.7)
		x[i] = scale * v
	}
	return x
}

// ClickTrain returns nSamples of mono click-train content: silence with a
// loud LCG-noise burst (amplitude 0.8, seed 0xC1CC7A31) every
// periodFrames*FrameSize samples, each burst burstFrames*FrameSize samples
// long, repeating for the whole duration, the first burst at sample 0. It
// drives an encoder's attack detector repeatedly.
func ClickTrain(nSamples, periodFrames, burstFrames int) []float64 {
	x := make([]float64, nSamples)
	seed := uint64(0xC1CC7A31)
	period := periodFrames * FrameSize
	burst := burstFrames * FrameSize
	for start := 0; start+burst <= nSamples; start += period {
		for i := range burst {
			x[start+i] = LCGSigned(&seed) * 0.8
		}
	}
	return x
}

// ToneClick returns nSamples of mono tone+click content: a steady MultiTone
// (peak 0.5) interrupted every periodFrames frames by a genuine onset (a half
// frame of silence, then an LCG-noise burst of burstFrames frames, seed
// 0x5A17C1CC, amplitude 0.9), clamped to [-1, 1]. The silence-then-burst
// shape is what a sub-block energy-ratio attack detector actually fires on: a
// click merely summed onto a loud tone never clears the ratio. The first
// burst starts one period in, a true mid-stream onset rather than a
// stream-start frame.
func ToneClick(sampleRate, nSamples, periodFrames, burstFrames int) []float64 {
	tone := MultiTone(sampleRate, nSamples, 0, 0.5)
	seed := uint64(0x5A17C1CC)
	period := periodFrames * FrameSize
	burst := burstFrames * FrameSize
	const gap = FrameSize / 2
	for start := period; start+burst <= nSamples; start += period {
		for i := start - gap; i < start; i++ {
			tone[i] = 0
		}
		for i := range burst {
			tone[start+i] = LCGSigned(&seed) * 0.9
		}
	}
	x := make([]float64, nSamples)
	for i, v := range tone {
		x[i] = max(-1, min(1, v))
	}
	return x
}
