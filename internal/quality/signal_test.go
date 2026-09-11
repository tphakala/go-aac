package quality

import (
	"testing"
	"time"
)

// TestLCGSequencePinned pins the generator's state transition and first two
// outputs against values captured independently, so any change to the
// multiplier, the increment, or the >>11 / 2^53 output extraction is caught.
func TestLCGSequencePinned(t *testing.T) {
	seed := uint64(1)
	v0 := LCG(&seed)
	if seed != 7806831264735756412 {
		t.Fatalf("state after 1 step = %d, want 7806831264735756412", seed)
	}
	if v0 != 0.42320917087271326 {
		t.Fatalf("v0 = %.17g, want 0.42320917087271326", v0)
	}
	v1 := LCG(&seed)
	if seed != 9396908728118811419 {
		t.Fatalf("state after 2 steps = %d, want 9396908728118811419", seed)
	}
	if v1 != 0.50940744288372064 {
		t.Fatalf("v1 = %.17g, want 0.50940744288372064", v1)
	}
}

// TestLCGSignedRange: LCGSigned stays in [-1, 1) and is exactly LCG*2-1 on the
// same state advance.
func TestLCGSignedRange(t *testing.T) {
	sa, sb := uint64(0xABCDEF), uint64(0xABCDEF)
	for range 1000 {
		s := LCGSigned(&sa)
		if s < -1 || s >= 1 {
			t.Fatalf("LCGSigned = %v out of [-1, 1)", s)
		}
		if want := LCG(&sb)*2 - 1; s != want {
			t.Fatalf("LCGSigned = %v, want LCG*2-1 = %v", s, want)
		}
	}
}

// TestClickTrainCadence pins that ClickTrain's internal period computation
// matches periodFrames*FrameSize: a burst of energy begins at every such
// boundary and the samples between bursts are exactly silent. A wrong internal
// multiplier in ClickTrain shifts its bursts off the boundaries the test
// computes and trips the burst- or silence-region assertion. The test derives
// its own period from the same FrameSize and ClickPeriodFrames constants
// ClickTrain reads, so a change to those shared constants moves both sides
// together and is NOT what this test guards.
func TestClickTrainCadence(t *testing.T) {
	period := ClickPeriodFrames * FrameSize
	burst := ClickBurstFrames * FrameSize
	const periods = 10
	n := periods * period
	x := ClickTrain(n, ClickPeriodFrames, ClickBurstFrames)
	for start := 0; start+period <= n; start += period {
		var burstE, silenceE float64
		for i := start; i < start+burst; i++ {
			burstE += x[i] * x[i]
		}
		for i := start + burst; i < start+period; i++ {
			silenceE += x[i] * x[i]
		}
		if burstE == 0 {
			t.Fatalf("period at %d: burst region carries no energy", start)
		}
		if silenceE != 0 {
			t.Fatalf("period at %d: the gap between bursts is not silent (energy %v)", start, silenceE)
		}
	}
}

// TestClickTrainDegenerateCadence: a periodFrames of 0 must yield silence, not
// loop forever. Run in a goroutine so a regression that loops is caught by the
// timeout here as an explicit failure rather than hanging the whole suite. The
// 4096-sample buffer is larger than one burst (ClickBurstFrames*FrameSize =
// 1024), so without the guard the period-0 loop would spin forever; the guarded
// path returns in microseconds, far under the 10s timeout.
func TestClickTrainDegenerateCadence(t *testing.T) {
	done := make(chan []float64, 1)
	go func() { done <- ClickTrain(4096, 0, ClickBurstFrames) }()
	select {
	case x := <-done:
		if len(x) != 4096 {
			t.Fatalf("returned %d samples, want 4096", len(x))
		}
		for i, v := range x {
			if v != 0 {
				t.Fatalf("degenerate cadence must be silent; x[%d] = %v", i, v)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ClickTrain with periodFrames=0 did not return within 10s: the degenerate-cadence guard is missing")
	}
}

// TestToneClickDegenerateCadence: a periodFrames of 0 must return the clamped
// tone without indexing out of range (period 0 starts the gap loop at a
// negative index). Recover so a regression is a clear failure, not a crash.
func TestToneClickDegenerateCadence(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ToneClick with periodFrames=0 panicked: %v; the degenerate-cadence guard is missing", r)
		}
	}()
	x := ToneClick(44100, 4096, 0, ClickBurstFrames)
	if len(x) != 4096 {
		t.Fatalf("returned %d samples, want 4096", len(x))
	}
}
