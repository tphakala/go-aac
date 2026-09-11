package quality

import "math"

// Program is one deterministic test signal of the synthetic corpus. Names
// are stable identifiers used in reports and -programs filters.
type Program struct {
	Name     string
	Channels int
	// SampleRate pins a program to one rate, and is 0 for the synthetic
	// programs, which generate at any rate. A corpus WAV is only meaningful
	// at its own rate, and resampling it would change what is measured, so
	// the caller skips the rates it does not serve rather than failing them.
	SampleRate int
	// Gen returns Channels planar float64 channels of nSamples samples each,
	// every sample in [-1, 1], identical on every call. A program with a
	// pinned SampleRate ignores both arguments and returns its own samples.
	Gen func(sampleRate, nSamples int) [][]float64
}

// RunsAt reports whether the program has data at sampleRate.
func (p Program) RunsAt(sampleRate int) bool {
	return p.SampleRate == 0 || p.SampleRate == sampleRate
}

// Programs returns the synthetic corpus in report order: nine mono programs
// spanning tonal, noisy, band-limited-noise, transient, swept, bird-like, and
// speech-like content, then three stereo programs exercising decorrelated,
// near-identical (M/S), and fully independent channels.
func Programs() []Program {
	mono := func(name string, gen func(sr, n int) []float64) Program {
		return Program{Name: name, Channels: 1, Gen: func(sr, n int) [][]float64 { return [][]float64{gen(sr, n)} }}
	}
	return []Program{
		mono("multitone", func(sr, n int) []float64 {
			return withSlowEnvelope(MultiTone(sr, n, 0, 0.7), sr)
		}),
		mono("harmonic-vibrato", genHarmonicVibrato),
		mono("pink-noise", genPinkNoise),
		mono("band-noise", genBandNoise),
		mono("click-train", func(_, n int) []float64 {
			return ClickTrain(n, ClickPeriodFrames, ClickBurstFrames)
		}),
		mono("tone-click", func(sr, n int) []float64 {
			return ToneClick(sr, n, ClickPeriodFrames, ClickBurstFrames)
		}),
		mono("sweep", genSweep),
		mono("bird-chirps", genBirdChirps),
		mono("speech-like", genSpeechLike),
		{Name: "stereo-decorrelated", Channels: 2, Gen: func(sr, n int) [][]float64 {
			return [][]float64{
				withSlowEnvelope(MultiTone(sr, n, 0, 0.7), sr),
				withSlowEnvelope(MultiTone(sr, n, 0.37, 0.7), sr),
			}
		}},
		{Name: "stereo-near-identical", Channels: 2, Gen: func(sr, n int) [][]float64 {
			l := genHarmonicVibrato(sr, n)
			r := make([]float64, n)
			seed := uint64(0x51DE)
			for i := range r {
				r[i] = clamp1(l[i] + 0.01*LCGSigned(&seed))
			}
			return [][]float64{l, r}
		}},
		{Name: "stereo-wide", Channels: 2, Gen: func(sr, n int) [][]float64 {
			return [][]float64{genBirdChirps(sr, n), genSweep(sr, n)}
		}},
	}
}

// ProgramByName looks a program up by its report name.
func ProgramByName(name string) (Program, bool) {
	for _, p := range Programs() {
		if p.Name == name {
			return p, true
		}
	}
	return Program{}, false
}

func clamp1(v float64) float64 { return max(-1, min(1, v)) }

// envelopeHz is the amplitude-modulation rate withSlowEnvelope applies. It is
// slow enough not to disturb what a tonal program measures, and fast enough
// that a whole-period misalignment is visible within one correlation window.
const envelopeHz = 0.7

// withSlowEnvelope multiplies x in place by a slow amplitude modulation
// between 0.45 and 1.0, and returns it.
//
// This exists for the aligner, not for the audio. A perfectly stationary
// periodic program correlates almost identically at every lag one signal
// period apart, so its true delay is not recoverable from cross-correlation:
// the harness measured a 440 Hz program at 3262 samples instead of 1057
// (exactly 22 periods late) and then scored every metric on misaligned audio,
// reporting 27.66 dB where the true alignment gives 67.43 dB. An envelope
// makes the correlation peak unique. Every other program in the corpus
// already carries such a feature (vibrato, transients, a sweep, or noise) and
// aligns correctly without help; these two were the only stationary ones.
func withSlowEnvelope(x []float64, sr int) []float64 {
	for i := range x {
		t := float64(i) / float64(sr)
		x[i] *= 0.725 + 0.275*math.Sin(2*math.Pi*envelopeHz*t)
	}
	return x
}

// genHarmonicVibrato: 220 Hz with 12 harmonics at 1/k, 5.5 Hz vibrato of
// +/-1 %, 0.5 Hz amplitude modulation between 0.3 and 1.0, peak 0.6.
func genHarmonicVibrato(sr, n int) []float64 {
	x := make([]float64, n)
	var norm float64
	for k := 1; k <= 12; k++ {
		norm += 1 / float64(k)
	}
	phase := 0.0
	for i := range x {
		t := float64(i) / float64(sr)
		f0 := 220 * (1 + 0.01*math.Sin(2*math.Pi*5.5*t))
		phase += 2 * math.Pi * f0 / float64(sr)
		var v float64
		for k := 1; k <= 12; k++ {
			v += math.Sin(float64(k)*phase) / float64(k)
		}
		am := 0.65 + 0.35*math.Sin(2*math.Pi*0.5*t)
		x[i] = 0.6 * am * v / norm
	}
	return x
}

// genPinkNoise: LCG white noise through the Paul Kellet 3-pole pinking
// filter (a widely published -3 dB/octave approximation), peak-normalized
// to 0.5.
func genPinkNoise(_, n int) []float64 {
	x := make([]float64, n)
	seed := uint64(0x9A11C0DE)
	var b0, b1, b2 float64
	peak := 0.0
	for i := range x {
		w := LCGSigned(&seed)
		b0 = 0.99765*b0 + w*0.0990460
		b1 = 0.96300*b1 + w*0.2965164
		b2 = 0.57000*b2 + w*1.0526913
		x[i] = b0 + b1 + b2 + w*0.1848
		peak = max(peak, math.Abs(x[i]))
	}
	if peak == 0 {
		return x
	}
	for i := range x {
		x[i] = 0.5 * x[i] / peak
	}
	return x
}

// genBandNoise: the issue's band-limited noise, built without filter design so
// the reference itself is exactly band-limited and cannot alias. It sums 200
// sinusoids at LCG-random frequencies uniform in [300, 6000] Hz with random
// phases (seed 0xBA4D01), peak-normalizes to 0.5, then applies the slow
// envelope so the aligner has a unique correlation peak.
func genBandNoise(sr, n int) []float64 {
	const (
		tones = 200
		loHz  = 300.0
		hiHz  = 6000.0
	)
	x := make([]float64, n)
	if sr <= 0 || n <= 0 {
		return x
	}
	seed := uint64(0xBA4D01)
	for range tones {
		f := loHz + (hiHz-loHz)*LCG(&seed)
		phase := 2 * math.Pi * LCG(&seed)
		w := 2 * math.Pi * f / float64(sr)
		for i := range x {
			x[i] += math.Sin(w*float64(i) + phase)
		}
	}
	peak := 0.0
	for _, v := range x {
		peak = max(peak, math.Abs(v))
	}
	if peak == 0 {
		return x
	}
	for i := range x {
		x[i] = 0.5 * x[i] / peak
	}
	return withSlowEnvelope(x, sr)
}

// genSweep: logarithmic sine sweep 50 Hz to 18 kHz over the duration,
// amplitude 0.5.
func genSweep(sr, n int) []float64 {
	x := make([]float64, n)
	if sr <= 0 || n <= 0 {
		return x
	}
	// The top of the sweep is held below Nyquist so the REFERENCE program
	// itself never aliases and skews both encoders' bandwidth and band-limited
	// SNR; at 44.1 kHz the 18 kHz end sits comfortably below Nyquist.
	const f0 = 50.0
	f1 := min(18000.0, 0.45*float64(sr))
	dur := float64(n) / float64(sr)
	k := math.Log(f1 / f0)
	amp := 2 * math.Pi * f0 * dur / k
	for i := range x {
		t := float64(i) / float64(sr)
		x[i] = 0.5 * math.Sin(amp*(math.Exp(k*t/dur)-1))
	}
	return x
}

// genBirdChirps: an FM chirp rising 2 kHz to 6 kHz over 60 ms with a 3 ms
// attack and a 40 ms exponential decay, one every 250 ms, peak 0.6, silence
// between chirps. The short attack is what makes it a pre-echo probe.
func genBirdChirps(sr, n int) []float64 {
	x := make([]float64, n)
	period := sr / 4
	length := sr * 60 / 1000
	attack := sr * 3 / 1000
	if period <= 0 || length <= 0 || attack <= 0 {
		return x // a rate too low to carry a chirp; the loop would not advance
	}
	decay := float64(sr) * 0.040
	for start := 0; start+length <= n; start += period {
		phase := 0.0
		for i := range length {
			u := float64(i) / float64(length)
			f := 2000 + 4000*u
			phase += 2 * math.Pi * f / float64(sr)
			// if/else, not compute-then-overwrite: the Exp is real work and
			// the attack ramp discards it for the first attack samples.
			var env float64
			if i < attack {
				env = float64(i) / float64(attack)
			} else {
				env = math.Exp(-float64(i-attack) / decay)
			}
			x[start+i] = 0.6 * env * math.Sin(phase)
		}
	}
	return x
}

// genSpeechLike: a 120 Hz pulse train (harmonics 1..40 at 1/k) shaped by
// two moving formant resonances, a 4 Hz syllabic amplitude envelope, and a
// 30 ms noise burst every 500 ms, peak-normalized to 0.6.
func genSpeechLike(sr, n int) []float64 {
	x := make([]float64, n)
	seed := uint64(0x5EEC4)
	peak := 0.0
	for i := range x {
		t := float64(i) / float64(sr)
		f1 := 700 + 200*math.Sin(2*math.Pi*3*t)
		f2 := 1800 + 600*math.Sin(2*math.Pi*3*t+1)
		var v float64
		for k := 1; k <= 40; k++ {
			fk := 120 * float64(k)
			g := math.Exp(-((fk-f1)*(fk-f1))/(2*150*150)) + 0.5*math.Exp(-((fk-f2)*(fk-f2))/(2*250*250))
			v += g * math.Sin(2*math.Pi*fk*t) / float64(k)
		}
		v *= 0.55 + 0.45*math.Sin(2*math.Pi*4*t)
		if (i*1000/sr)%500 < 30 {
			v += 0.25 * LCGSigned(&seed)
		}
		x[i] = v
		peak = max(peak, math.Abs(v))
	}
	if peak == 0 {
		return x
	}
	for i := range x {
		x[i] = 0.6 * x[i] / peak
	}
	return x
}
