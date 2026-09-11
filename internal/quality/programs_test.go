package quality

import (
	"math"
	"testing"
)

// TestProgramsWellFormed checks every program produces the declared channel
// count, the requested length, samples inside [-1, 1], non-silence, and is
// deterministic (two calls are identical).
func TestProgramsWellFormed(t *testing.T) {
	const sr, n = 44100, 44100
	seen := map[string]bool{}
	for _, p := range Programs() {
		if seen[p.Name] {
			t.Fatalf("duplicate program name %q", p.Name)
		}
		seen[p.Name] = true
		a := p.Gen(sr, n)
		b := p.Gen(sr, n)
		if len(a) != p.Channels {
			t.Fatalf("%s: %d channels, want %d", p.Name, len(a), p.Channels)
		}
		for c := range a {
			if len(a[c]) != n {
				t.Fatalf("%s ch%d: len %d, want %d", p.Name, c, len(a[c]), n)
			}
			var e float64
			for i, v := range a[c] {
				if v > 1 || v < -1 || math.IsNaN(v) {
					t.Fatalf("%s ch%d[%d] = %v out of range", p.Name, c, i, v)
				}
				if v != b[c][i] {
					t.Fatalf("%s ch%d[%d]: not deterministic", p.Name, c, i)
				}
				e += v * v
			}
			if e/float64(n) < 1e-4 {
				t.Fatalf("%s ch%d: mean-square %v, program is nearly silent", p.Name, c, e/float64(n))
			}
		}
	}
	if len(seen) != 12 {
		t.Fatalf("%d programs, want 12", len(seen))
	}
}

func TestProgramByName(t *testing.T) {
	if _, ok := ProgramByName("bird-chirps"); !ok {
		t.Fatal("bird-chirps missing")
	}
	if _, ok := ProgramByName("band-noise"); !ok {
		t.Fatal("band-noise missing")
	}
	if _, ok := ProgramByName("nope"); ok {
		t.Fatal("unknown name must not resolve")
	}
}

// TestRunsAt pins the two halves of the rate contract: a synthetic program
// (SampleRate 0) runs at every rate, a corpus program pinned to one rate runs
// only there.
func TestRunsAt(t *testing.T) {
	synthetic := Program{SampleRate: 0}
	for _, sr := range []int{44100, 48000} {
		if !synthetic.RunsAt(sr) {
			t.Fatalf("a SampleRate-0 program must run at %d Hz", sr)
		}
	}
	pinned := Program{SampleRate: 44100}
	if !pinned.RunsAt(44100) {
		t.Fatal("a pinned program must run at its own rate")
	}
	if pinned.RunsAt(48000) {
		t.Fatal("a pinned program must not run at a different rate")
	}
}

// TestTransientProgramsHaveAttacks: the pre-echo metric must find attacks in
// the programs designed to carry them, and none in the steady ones.
func TestTransientProgramsHaveAttacks(t *testing.T) {
	for _, name := range []string{"click-train", "tone-click", "bird-chirps", "stereo-wide"} {
		p, ok := ProgramByName(name)
		if !ok {
			t.Fatalf("program %q missing", name)
		}
		x := p.Gen(44100, 3*44100)
		if _, events := PreEcho(x[0], x[0], 44100); events == 0 {
			t.Fatalf("%s: PreEcho found no attacks", name)
		}
	}
	for _, name := range []string{"multitone", "sweep"} {
		p, ok := ProgramByName(name)
		if !ok {
			t.Fatalf("program %q missing", name)
		}
		x := p.Gen(44100, 3*44100)
		if _, events := PreEcho(x[0], x[0], 44100); events != 0 {
			t.Fatalf("%s: PreEcho found %d attacks in a steady program", name, events)
		}
	}
}

// TestProgramsAtEveryRate: both supported rates generate cleanly at the right
// shape.
func TestProgramsAtEveryRate(t *testing.T) {
	for _, sr := range []int{44100, 48000} {
		for _, p := range Programs() {
			x := p.Gen(sr, sr/2)
			if len(x) != p.Channels || len(x[0]) != sr/2 {
				t.Fatalf("%s at %d Hz: shape %dx%d", p.Name, sr, len(x), len(x[0]))
			}
		}
	}
}
