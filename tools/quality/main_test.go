package main

import (
	"context"
	"io"
	"slices"
	"testing"

	aac "github.com/tphakala/go-aac"
	"github.com/tphakala/go-aac/internal/quality"
)

func TestDedupInts(t *testing.T) {
	cases := []struct {
		in, want []int
	}{
		{[]int{64, 96, 128, 192}, []int{64, 96, 128, 192}},
		{[]int{128, 128}, []int{128}},
		{[]int{192, 64, 192, 96, 64}, []int{192, 64, 96}}, // first-seen order
		{[]int{44100}, []int{44100}},
	}
	for _, c := range cases {
		got := dedupInts(c.in)
		if !slices.Equal(got, c.want) {
			t.Errorf("dedupInts(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSelectProgramsNone(t *testing.T) {
	rates := []int{44100}
	// -programs none with no corpus has nothing to compare: an error, not an
	// empty run that writes a header and no rows.
	if _, err := selectPrograms("none", "", rates); err == nil {
		t.Fatal("selectPrograms(none, no corpus) = nil error, want a no-programs error")
	}
	// Default still yields the full synthetic corpus.
	all, err := selectPrograms("", "", rates)
	if err != nil {
		t.Fatalf("selectPrograms(all): %v", err)
	}
	if len(all) != len(quality.Programs()) {
		t.Fatalf("selectPrograms(all) returned %d programs, want %d", len(all), len(quality.Programs()))
	}
	// An unknown name is still rejected (none is the only special token).
	if _, err := selectPrograms("does-not-exist", "", rates); err == nil {
		t.Fatal("selectPrograms(unknown) = nil error, want unknown-program error")
	}
}

// progMultitone and progToneClick name synthetic programs reused across tests.
const (
	progMultitone = "multitone"
	progToneClick = "tone-click"
)

func TestSelectProgramsDedup(t *testing.T) {
	// A repeated -programs name is collapsed to one, first-seen order kept, so
	// it is not run (and summary-weighted) twice, matching -rates/-bitrates.
	progs, err := selectPrograms("pink-noise,"+progMultitone+",pink-noise", "", []int{44100})
	if err != nil {
		t.Fatal(err)
	}
	if len(progs) != 2 || progs[0].Name != "pink-noise" || progs[1].Name != progMultitone {
		t.Fatalf("dedup produced %d programs %v, want [pink-noise multitone]", len(progs), names(progs))
	}
}

func names(progs []quality.Program) []string {
	out := make([]string, len(progs))
	for i, p := range progs {
		out[i] = p.Name
	}
	return out
}

// TestBuildJobsGridOrderAndSharedRef pins the full (program, bitrate, coder)
// grid expansion: program-major, then bitrate, then coder; dense 1-based
// indices; and a single reference shared read-only across every case of one
// program so parallel workers can read it safely.
func TestBuildJobsGridOrderAndSharedRef(t *testing.T) {
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128, 192},
		coders:   []aac.Coder{aac.CoderNMR, aac.CoderTwoLoop},
		programs: []quality.Program{
			{Name: "a", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
			{Name: "b", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
		},
		seconds: 1,
	}
	jobs := buildJobs(t.Context(), o, io.Discard)
	if len(jobs) != 8 {
		t.Fatalf("built %d jobs, want 8 (2 programs x 2 bitrates x 2 coders)", len(jobs))
	}
	want := []struct {
		idx   int
		name  string
		kbps  int
		coder aac.Coder
	}{
		{1, "a", 128, aac.CoderNMR}, {2, "a", 128, aac.CoderTwoLoop},
		{3, "a", 192, aac.CoderNMR}, {4, "a", 192, aac.CoderTwoLoop},
		{5, "b", 128, aac.CoderNMR}, {6, "b", 128, aac.CoderTwoLoop},
		{7, "b", 192, aac.CoderNMR}, {8, "b", 192, aac.CoderTwoLoop},
	}
	for i, w := range want {
		j := jobs[i]
		if j.idx != w.idx || j.spec.Program.Name != w.name || j.spec.Kbps != w.kbps || j.spec.Coder != w.coder {
			t.Errorf("job %d = {idx %d %s %d %s}, want {idx %d %s %d %s}",
				i, j.idx, j.spec.Program.Name, j.spec.Kbps, coderName(j.spec.Coder),
				w.idx, w.name, w.kbps, coderName(w.coder))
		}
	}
	// Program a's four cases share one reference (generated once per (rate,
	// program)); program b's is a distinct backing array.
	if &jobs[0].ref[0][0] != &jobs[3].ref[0][0] {
		t.Error("program a's cases do not share the same reference backing array")
	}
	if &jobs[0].ref[0][0] == &jobs[4].ref[0][0] {
		t.Error("programs a and b unexpectedly share a reference")
	}
}

func TestBuildJobsCancelled(t *testing.T) {
	// An already-cancelled context stops buildJobs before it generates any
	// reference, so a Ctrl-C during a long -seconds generation phase is prompt.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		coders:   []aac.Coder{aac.CoderNMR},
		seconds:  1,
		programs: []quality.Program{{Name: "a", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }}},
	}
	if jobs := buildJobs(ctx, o, io.Discard); len(jobs) != 0 {
		t.Fatalf("cancelled buildJobs built %d jobs, want 0", len(jobs))
	}
}

func TestBuildJobsSkipsRateMismatchAndEmpty(t *testing.T) {
	o := &options{
		rates:    []int{44100},
		bitrates: []int{128},
		coders:   []aac.Coder{aac.CoderNMR},
		programs: []quality.Program{
			// Pinned to a different rate: skipped, no case.
			{Name: "pinned", Channels: 1, SampleRate: 48000, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
			// Empty at this rate: skipped, no case.
			{Name: "empty", Channels: 1, Gen: func(_, _ int) [][]float64 { return [][]float64{{}} }},
			// Real one.
			{Name: "ok", Channels: 1, Gen: func(_, n int) [][]float64 { return [][]float64{make([]float64, n)} }},
		},
		seconds: 1,
	}
	jobs := buildJobs(t.Context(), o, io.Discard)
	if len(jobs) != 1 || jobs[0].spec.Program.Name != "ok" || jobs[0].idx != 1 {
		t.Fatalf("buildJobs skipped incorrectly: got %d jobs %+v", len(jobs), jobs)
	}
}

// TestParseFlagsRejects covers the setup-time validation: each row names an
// input that must be rejected before any case runs.
func TestParseFlagsRejects(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"rate 32000", []string{"-rates", "32000"}},
		{"bitrate 0", []string{"-bitrates", "0"}},
		{"bitrate over ceiling", []string{"-bitrates", "900"}},
		{"unknown coder", []string{"-coders", "bogus"}},
		{"seconds 0", []string{"-seconds", "0"}},
		{"jobs 0", []string{"-jobs", "0"}},
		{"empty coders", []string{"-coders", ""}},
	}
	for _, c := range cases {
		if _, err := parseFlags(c.args); err == nil {
			t.Errorf("%s: parseFlags(%v) = nil error, want rejection", c.name, c.args)
		}
	}
	// A legal command line parses, with the coder axis resolved.
	o, err := parseFlags([]string{"-bitrates", "64,320", "-rates", "44100,48000", "-coders", "nmr,fast"})
	if err != nil {
		t.Fatalf("legal flags rejected: %v", err)
	}
	if len(o.bitrates) != 2 || len(o.rates) != 2 || len(o.coders) != 2 {
		t.Fatalf("parsed %d bitrates, %d rates, %d coders, want 2/2/2", len(o.bitrates), len(o.rates), len(o.coders))
	}
}
