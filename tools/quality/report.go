package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/tphakala/go-aac/internal/quality"
)

// Metric column names, shared by the report tables, the summary, and the
// win-direction table.
const (
	mSNR       = "SNR"
	mBandSNR   = "BandSNR"
	mSegSNR    = "SegSNR"
	mLSD       = "LSD"
	mPreEcho   = "PreEcho"
	mBandwidth = "Bandwidth"
	mMOS       = "MOS"
	mODG       = "ODG"
)

// metric describes one report column: how to read it from a result, and which
// direction counts as better. scored is false for a column that is reported
// but not judged, so it contributes a delta and no win.
//
// One table rather than three parallel ones on purpose: with a separate name
// slice, getter switch and direction map, adding a metric to one and not the
// others compiles and runs, and the failure is silent (an always-n/a column,
// or an inverted win count).
type metric struct {
	name   string
	get    func(*encoderResult) float64
	better int // +1 higher is better, -1 lower is better
	scored bool
}

var metrics = []metric{
	{mSNR, func(r *encoderResult) float64 { return r.Metrics.SNR }, +1, true},
	{mBandSNR, func(r *encoderResult) float64 { return r.Metrics.BandSNR }, +1, true},
	{mSegSNR, func(r *encoderResult) float64 { return r.Metrics.SegSNR }, +1, true},
	{mLSD, func(r *encoderResult) float64 { return r.Metrics.LSD }, -1, true},
	{mPreEcho, func(r *encoderResult) float64 { return r.Metrics.PreEcho }, -1, true},
	// Bandwidth is informational: an encoder's lowpass is a deliberate bitrate
	// dependent choice, so "wider" is not "better" and scoring it would award
	// points for declining to lowpass.
	{mBandwidth, func(r *encoderResult) float64 { return r.Metrics.Bandwidth / 1000 }, +1, false},
	{mMOS, func(r *encoderResult) float64 { return r.MOS }, +1, true},
	{mODG, func(r *encoderResult) float64 { return r.ODG }, +1, true},
}

// reportSchemaVersion is the JSON report's layout version. Bump it on any
// breaking change to the field set so a consumer can tell one layout from the
// next; it is emitted as the first field of every report.
const reportSchemaVersion = 1

// report is the whole run: provenance header plus every case.
type report struct {
	SchemaVersion int          `json:"schema_version"`
	GeneratedUTC  string       `json:"generated_utc"`
	GoAACRev      string       `json:"goaac_rev"`
	FFmpegVersion string       `json:"ffmpeg_version"`
	LibFDK        bool         `json:"libfdk"`
	Tools         []string     `json:"tools"`
	Seconds       int          `json:"seconds"`
	Attempted     int          `json:"attempted"`
	Failed        int          `json:"failed"`
	Cases         []caseResult `json:"cases"`
}

// summaryKey groups the summary by sample rate, bitrate, AND coder. Keying on
// fewer fields would silently merge rows the detail tables keep split, so a
// coder that regressed would be averaged into the ones that did not.
type summaryKey struct {
	SampleRate int
	Kbps       int
	Coder      string
}

// summaryRow aggregates one (rate, bitrate, coder) across programs.
type summaryRow struct {
	summaryKey
	Programs int
	// MeanDelta is the mean of (go-aac minus ffmpeg aac) per metric over the
	// programs where both values are finite; NaN when none are.
	MeanDelta map[string]float64
	// Compared counts those programs, and Wins how many of them go-aac won.
	// Wins is printed over Compared, never over Programs: a metric no case
	// could measure would otherwise read as a clean sweep of losses.
	Compared map[string]int
	Wins     map[string]int
}

// summarize aggregates go-aac minus ffmpeg aac deltas per (rate, bitrate,
// coder). The native aac encoder is the scored reference; libfdk_aac, when
// present, is reported in its own tables but never summarized here.
func summarize(cases []caseResult) []summaryRow {
	rows := map[summaryKey]*summaryRow{}
	for i := range cases {
		c := &cases[i]
		key := summaryKey{c.SampleRate, c.Kbps, c.Coder}
		row := rows[key]
		if row == nil {
			row = &summaryRow{summaryKey: key, MeanDelta: map[string]float64{},
				Compared: map[string]int{}, Wins: map[string]int{}}
			rows[key] = row
		}
		row.Programs++
		for _, m := range metrics {
			g, l := m.get(&c.GoAAC), m.get(&c.FFmpegAAC)
			if !finite(g) || !finite(l) {
				continue
			}
			row.MeanDelta[m.name] += g - l
			row.Compared[m.name]++
			if m.scored && (g-l)*float64(m.better) > 0 {
				row.Wins[m.name]++
			}
		}
	}
	out := make([]summaryRow, 0, len(rows))
	for _, row := range rows {
		for _, m := range metrics {
			if n := row.Compared[m.name]; n > 0 {
				row.MeanDelta[m.name] /= float64(n)
			} else {
				row.MeanDelta[m.name] = math.NaN()
			}
		}
		out = append(out, *row)
	}
	slices.SortFunc(out, func(a, b summaryRow) int {
		if a.SampleRate != b.SampleRate {
			return a.SampleRate - b.SampleRate
		}
		if a.Kbps != b.Kbps {
			return a.Kbps - b.Kbps
		}
		return strings.Compare(a.Coder, b.Coder)
	})
	return out
}

// finite reports whether v is a real measurement rather than a NaN or an
// infinity. The Markdown and the JSON both treat the two the same way.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// notMeasured is the cell text for a figure that was not measured (the
// external tool was absent) or is undefined for that program.
const notMeasured = "n/a"

// fmtMetric renders a metric cell: notMeasured for anything not finite, two
// decimals otherwise.
func fmtMetric(v float64) string {
	if !finite(v) {
		return notMeasured
	}
	return fmt.Sprintf("%.2f", v)
}

// cell escapes a value for a Markdown table cell. Program names can come from
// a corpus file name, and a pipe or a newline there would split the cell or
// forge a whole row.
func cell(s string) string {
	// Escape backslash FIRST so a literal backslash before a pipe cannot forge a
	// live cell: strings.NewReplacer applies the longest match at each position
	// and does not re-scan its own output, so ordering "\\" -> "\\\\" ahead of
	// "|" -> "\\|" doubles the backslash and then escapes the pipe (a raw "\|"
	// would otherwise render as an escaped backslash followed by a live pipe).
	return strings.NewReplacer("\\", "\\\\", "|", "\\|", "\n", " ", "\r", " ").Replace(s)
}

// writeMarkdown renders the full report: header, one table per sample rate
// against ffmpeg's native aac (and, when present, against libfdk_aac), then
// the per-(rate, bitrate, coder) summary.
func writeMarkdown(w io.Writer, r *report) error {
	var b strings.Builder
	b.WriteString("# go-aac versus ffmpeg quality report\n\n")
	libfdk := "absent in this ffmpeg build"
	if r.LibFDK {
		libfdk = "present"
	}
	fmt.Fprintf(&b, "- Generated: %s\n- go-aac: %s\n- Reference encoder: ffmpeg aac (%s)\n- libfdk_aac: %s\n- External metrics: %s\n- Program length: %d s\n- Cases: %d attempted, %d failed\n\n",
		r.GeneratedUTC, r.GoAACRev, r.FFmpegVersion, libfdk, orNone(r.Tools), r.Seconds, r.Attempted, r.Failed)
	fmt.Fprintf(&b, "Metrics: SNR, BandSNR (bins at or below %.0f kHz), SegSNR, MOS (ViSQOL MOS-LQO), ODG (PEAQ basic): higher is better. LSD, PreEcho: lower is better. Bandwidth (kHz) is informational and is not scored. delta is go-aac minus the reference in that table. Lag is the measured alignment in samples: %d for go-aac and ffmpeg's aac encoder, about %d for libfdk_aac. n/a means the figure was not measured (the external tool was absent) or is undefined for that program (no attack detected, no active frame).\n\n",
		quality.BandLimitHz/1000, goaacDelay, 2*goaacDelay)

	sortedRates := caseRates(r.Cases)
	b.WriteString("## Versus ffmpeg aac\n\n")
	for _, sr := range sortedRates {
		writeComparisonTable(&b, r.Cases, sr, "ffmpeg", func(c *caseResult) *encoderResult { return &c.FFmpegAAC })
	}
	if r.LibFDK {
		b.WriteString("## Versus libfdk_aac\n\n")
		for _, sr := range sortedRates {
			writeComparisonTable(&b, r.Cases, sr, "libfdk", func(c *caseResult) *encoderResult { return c.LibFDK })
		}
	}

	b.WriteString("## Summary\n\nMean delta (go-aac minus ffmpeg aac) per sample rate, bitrate, and coder across programs, and how many of the programs that could be compared go-aac won.\n\n")
	writeHeader(&b, "| Hz | kbps | coder | programs |", "|---|---|---|---|", []string{"mean delta", "wins"})
	for _, row := range summarize(r.Cases) {
		fmt.Fprintf(&b, "| %d | %d | %s | %d |", row.SampleRate, row.Kbps, cell(row.Coder), row.Programs)
		for _, m := range metrics {
			wins := notMeasured
			if n := row.Compared[m.name]; n > 0 && m.scored {
				wins = fmt.Sprintf("%d/%d", row.Wins[m.name], n)
			}
			fmt.Fprintf(&b, " %s | %s |", fmtMetric(row.MeanDelta[m.name]), wins)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// caseRates returns the sorted distinct sample rates present in cases.
func caseRates(cases []caseResult) []int {
	rates := map[int]bool{}
	for i := range cases {
		rates[cases[i].SampleRate] = true
	}
	sorted := slices.Collect(maps.Keys(rates))
	slices.Sort(sorted)
	return sorted
}

// writeComparisonTable renders one sample rate's detail table comparing go-aac
// against the encoder pick returns. A case whose pick is nil (libfdk absent
// for that case) is skipped, so the libfdk tables omit exactly the cases that
// have no libfdk result.
func writeComparisonTable(b *strings.Builder, cases []caseResult, sr int, other string, pick func(*caseResult) *encoderResult) {
	fmt.Fprintf(b, "### %d Hz\n\n", sr)
	writeHeader(b, fmt.Sprintf("| Program | kbps | coder | ch | go lag | %s lag |", other),
		"|---|---|---|---|---|---|", []string{"go", other, "delta"})
	for i := range cases {
		c := &cases[i]
		if c.SampleRate != sr {
			continue
		}
		o := pick(c)
		if o == nil {
			continue
		}
		fmt.Fprintf(b, "| %s | %d | %s | %d | %d | %d |", cell(c.Program), c.Kbps, cell(c.Coder), c.Channels, c.GoAAC.Lag, o.Lag)
		for _, m := range metrics {
			g, l := m.get(&c.GoAAC), m.get(o)
			fmt.Fprintf(b, " %s | %s | %s |", fmtMetric(g), fmtMetric(l), fmtMetric(g-l))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

// writeHeader emits a Markdown table header: the fixed leading cells, then one
// "<metric> <suffix>" cell per metric per suffix, then the separator row.
func writeHeader(b *strings.Builder, lead, leadSep string, suffixes []string) {
	b.WriteString(lead)
	for _, m := range metrics {
		for _, s := range suffixes {
			fmt.Fprintf(b, " %s %s |", m.name, s)
		}
	}
	b.WriteByte('\n')
	b.WriteString(leadSep)
	for range metrics {
		for range suffixes {
			b.WriteString("---|")
		}
	}
	b.WriteString("\n")
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// jsonEncoderResult mirrors encoderResult with nullable floats, because
// encoding/json rejects NaN.
type jsonEncoderResult struct {
	Name      string   `json:"name"`
	Lag       int      `json:"lag"`
	Bytes     int      `json:"bytes"`
	SNR       *float64 `json:"snr"`
	BandSNR   *float64 `json:"band_snr"`
	SegSNR    *float64 `json:"seg_snr"`
	LSD       *float64 `json:"lsd"`
	PreEcho   *float64 `json:"pre_echo"`
	PreEchoN  int      `json:"pre_echo_events"`
	Bandwidth *float64 `json:"bandwidth_hz"`
	MOS       *float64 `json:"mos"`
	ODG       *float64 `json:"odg"`
}

// nullable renders a non-finite measurement as JSON null, matching what
// fmtMetric renders as n/a.
func nullable(v float64) *float64 {
	if !finite(v) {
		return nil
	}
	return &v
}

// toJSONResult converts one encoder's result to its nullable-float mirror.
func toJSONResult(r *encoderResult) jsonEncoderResult {
	m := &r.Metrics
	return jsonEncoderResult{
		Name: r.Name, Lag: r.Lag, Bytes: r.Bytes,
		SNR: nullable(m.SNR), BandSNR: nullable(m.BandSNR), SegSNR: nullable(m.SegSNR), LSD: nullable(m.LSD),
		PreEcho: nullable(m.PreEcho), PreEchoN: m.PreEchoN, Bandwidth: nullable(m.Bandwidth),
		MOS: nullable(r.MOS), ODG: nullable(r.ODG),
	}
}

// MarshalJSON renders a caseResult with nullable metric floats and a nullable
// libfdk block. The pointer receiver is honored by encoding/json for the
// addressable elements of report.Cases.
func (c *caseResult) MarshalJSON() ([]byte, error) {
	type jc struct {
		Program    string             `json:"program"`
		Channels   int                `json:"channels"`
		SampleRate int                `json:"sample_rate"`
		Kbps       int                `json:"kbps"`
		Coder      string             `json:"coder"`
		GoAAC      jsonEncoderResult  `json:"goaac"`
		FFmpegAAC  jsonEncoderResult  `json:"ffmpeg_aac"`
		LibFDK     *jsonEncoderResult `json:"libfdk_aac"`
	}
	var fdk *jsonEncoderResult
	if c.LibFDK != nil {
		j := toJSONResult(c.LibFDK)
		fdk = &j
	}
	return json.Marshal(jc{c.Program, c.Channels, c.SampleRate, c.Kbps, c.Coder, toJSONResult(&c.GoAAC), toJSONResult(&c.FFmpegAAC), fdk})
}

// writeJSON writes the report as indented JSON.
func writeJSON(w io.Writer, r *report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
