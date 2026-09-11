package main

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/go-aac/internal/quality"
)

func TestParseInts(t *testing.T) {
	got, err := parseInts(" 64, 128 ,192")
	if err != nil || len(got) != 3 || got[0] != 64 || got[1] != 128 || got[2] != 192 {
		t.Fatalf("parseInts: %v, %v", got, err)
	}
	if _, err := parseInts(","); err == nil {
		t.Fatal("empty list must error")
	}
	if _, err := parseInts("128,abc"); err == nil {
		t.Fatal("non-integer must error")
	}
}

// testRates is the set of supported rates, so a corpus fixture at either is
// accepted and the rate check is not what a test is measuring unless it says so.
var testRates = []int{44100, 48000}

func TestSelectPrograms(t *testing.T) {
	all, err := selectPrograms("", "", testRates)
	if err != nil || len(all) != len(quality.Programs()) {
		t.Fatalf("all programs: %d, %v", len(all), err)
	}
	two, err := selectPrograms("sweep, multitone", "", testRates)
	if err != nil || len(two) != 2 || two[0].Name != "sweep" || two[1].Name != "multitone" {
		t.Fatalf("filtered programs: %+v, %v", two, err)
	}
	if _, err := selectPrograms("nope", "", testRates); err == nil {
		t.Fatal("unknown program must error")
	}
	if _, err := selectPrograms("", "/nonexistent-corpus-dir", testRates); err == nil {
		t.Fatal("an unreadable corpus directory must error")
	}
}

// TestSelectProgramsDuplicateName: a corpus WAV whose basename equals a selected
// synthetic program name would produce two report rows with the same Program
// name that the tables cannot tell apart, so selectPrograms rejects it.
func TestSelectProgramsDuplicateName(t *testing.T) {
	dir := t.TempDir()
	if err := writeWAVFile(filepath.Join(dir, "multitone.wav"), 48000, [][]float64{{0.1, -0.1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := selectPrograms("multitone", dir, testRates); err == nil {
		t.Fatal("a corpus file named after a selected synthetic program must error on the name collision")
	}
	// A distinct corpus name must NOT be rejected: renaming the same file to
	// "clip" resolves the collision and the selection succeeds.
	if err := os.Rename(filepath.Join(dir, "multitone.wav"), filepath.Join(dir, "clip.wav")); err != nil {
		t.Fatal(err)
	}
	progs, err := selectPrograms("multitone", dir, testRates)
	if err != nil {
		t.Fatalf("a distinct corpus name must not be rejected: %v", err)
	}
	if len(progs) != 2 {
		t.Fatalf("got %d programs %v, want the synthetic multitone plus the clip corpus file", len(progs), names(progs))
	}
}

// TestWavProgramCorpus writes a WAV into a corpus dir and checks it becomes a
// program pinned to its own rate, that non-WAV and non-regular entries are
// skipped, and that an unparsable WAV is an error rather than a silent skip.
func TestWavProgramCorpus(t *testing.T) {
	dir := t.TempDir()
	ch := [][]float64{{0.1, -0.1, 0.2, -0.2}, {0, 0.5, 0, -0.5}}
	if err := writeWAVFile(filepath.Join(dir, "clip.wav"), 48000, ch); err != nil {
		t.Fatal(err)
	}
	// Both of these must be skipped, so the count assertion below is contingent
	// on the skip actually happening.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.wav"), 0o755); err != nil {
		t.Fatal(err)
	}

	progs, err := selectPrograms("multitone", dir, testRates)
	if err != nil || len(progs) != 2 || progs[1].Name != "clip" || progs[1].Channels != 2 {
		t.Fatalf("corpus programs: %+v, %v", progs, err)
	}
	if progs[1].SampleRate != 48000 {
		t.Fatalf("clip.SampleRate = %d, want 48000", progs[1].SampleRate)
	}
	if !progs[1].RunsAt(48000) || progs[1].RunsAt(44100) {
		t.Fatal("a corpus program must run only at its own rate")
	}
	if !progs[0].RunsAt(44100) || !progs[0].RunsAt(48000) {
		t.Fatal("a synthetic program must run at any rate")
	}
	got := progs[1].Gen(48000, 999)
	if len(got) != 2 || len(got[0]) != 4 || got[1][1] != 0.5 {
		t.Fatalf("clip at 48 kHz: %v", got)
	}

	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "bad.wav"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := selectPrograms("", bad, testRates); err == nil {
		t.Fatal("an unparsable corpus WAV must error")
	}
}

// TestWavProgramRateRejected: a corpus file whose rate is not in the effective
// -rates set is an error at load, not a program that is loaded and then skipped
// at every case. Rate 0, which a malformed fmt chunk can declare and which
// Program.SampleRate reads as "any rate", goes the same way.
func TestWavProgramRateRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.wav")
	if err := writeWAVFile(path, 48000, [][]float64{{0.1, -0.1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := wavProgram(path, []int{44100}); err == nil {
		t.Fatal("a 48 kHz corpus file must be rejected when -rates is 44100")
	}
	if _, err := wavProgram(path, testRates); err != nil {
		t.Fatalf("a 48 kHz corpus file must load when -rates includes it: %v", err)
	}

	// A zero rate in the fmt chunk, patched in place: bytes 24-27 of a canonical
	// 44-byte header.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(raw[24:], 0)
	zero := filepath.Join(dir, "zero.wav")
	if err := os.WriteFile(zero, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wavProgram(zero, testRates); err == nil {
		t.Fatal("a corpus file declaring rate 0 must be rejected")
	}
}

// TestRunExitCodes drives run() itself, which nothing else does. Every case is a
// setup error caught before the grid runs, so none needs an external binary: the
// first four fail in parseFlags, and the explicit bad -ffmpeg fails at tool
// detection regardless of any ffmpeg on PATH.
func TestRunExitCodes(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"non-positive seconds", []string{"-seconds", "0"}},
		{"unsupported rate", []string{"-rates", "12345"}},
		{"negative rate", []string{"-rates", "-1"}},
		{"unknown program", []string{"-programs", "nope"}},
		{"unknown coder", []string{"-coders", "bogus"}},
		{"missing explicit ffmpeg", []string{"-ffmpeg", "/nonexistent/ffmpeg-binary"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := run(t.Context(), c.args, io.Discard); got != exitSetup {
				t.Fatalf("run(%v) = %d, want exitSetup %d", c.args, got, exitSetup)
			}
		})
	}
}
