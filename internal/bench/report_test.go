package bench

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkedBenchstat is the benchstat version that the expectations of
// TestSkewedSample were checked against.
const checkedBenchstat = "golang.org/x/perf v0.0.0-20261009192801-be2c69fb417e"

// writeSamples writes a file of samples of one lane of BenchmarkCase/json,
// in nanoseconds per operation.
func writeSamples(t *testing.T, dir string, l Lane, ns []float64) string {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "goos: linux\ngoarch: amd64\npkg: github.com/xiam/bytescan/internal/bench\nlane: %s\n", l)
	for _, v := range ns {
		fmt.Fprintf(&b, "BenchmarkCase/json-12 \t       1\t%.0f ns/op\t     100 B/op\t       3 allocs/op\n", v)
	}
	path := filepath.Join(dir, string(l)+".txt")
	{
		err := os.WriteFile(path, b.Bytes(), 0o644)
		require.NoError(t, err)
	}
	return path
}

func repeat(v float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

func benchstatOrSkip(t *testing.T) {
	t.Helper()
	if _, err := benchstatPath(); errors.Is(err, errNoBenchstat) {
		t.Skip(err)
	}
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	v, err := benchstatVersion(goCmd)
	require.NoError(t, err)
	t.Logf("benchstat %s; the expectations were checked against %s", v, checkedBenchstat)
}

// TestSkewedSample feeds the report code a fixed, skewed sample: nineteen
// samples of 1,000 ns and one of 100,000 ns. Its arithmetic mean is 5,950 ns
// and its median is 1,000 ns. The report gives the median, the interval of
// the median and no mean, and a lane within the spread limit needs no rerun.
// A second, wide sample is above the limit: it asks for a rerun, and after
// the rerun the case is inconclusive and has no rate.
func TestSkewedSample(t *testing.T) {
	benchstatOrSkip(t)
	dir := t.TempDir()

	skewed := append(repeat(1000, 19), 100000)
	var sum float64
	for _, v := range skewed {
		sum += v
	}
	{
		mean := sum / float64(len(skewed))
		require.Truef(t, mean == 5950, "the mean of the fixture is %v, want 5950", mean)
	}
	cmp, err := benchstat(writeSamples(t, dir, Control, skewed), writeSamples(t, dir, Wrapped, repeat(1000, 20)))
	require.NoError(t, err)
	got := cmp.first[unitTime]
	assert.Falsef(t, math.Abs(got.Median-1000e-9) > 1e-15, "median = %v s, want 1e-06 s (the median, not the mean of 5.95e-06 s)", got.Median)
	assert.Falsef(t, !got.Bounded || got.Spread != 0, "spread = %+v, want a bounded interval of 0 percent", got)
	{
		r := reportedSpread(got)
		assert.Truef(t, r == 5, "reported spread = %v, want the floor of 5 percent", r)
	}
	{
		s := formatSpread(got)
		assert.Truef(t, s == "± 5 % (CI ± 0 %)", "formatSpread = %q, want %q", s, "± 5 % (CI ± 0 %)")
	}
	{
		d := cmp.delta[unitTime]
		assert.Falsef(t, d.Significant || d.N != [2]int{20, 20}, "delta = %+v, want not significant with 20 and 20 samples", d)
	}
	{
		s := formatDelta(cmp.delta[unitTime])
		assert.Truef(t, s == "not significant", "formatDelta = %q, want not significant", s)
	}
	lanes := map[Lane]map[string]stat{Control: cmp.first, Wrapped: cmp.second, Instrumented: cmp.second}
	{
		status, ok := decide(lanes, nil, false)
		assert.Falsef(t, !ok || status != conclusive, "decide = %q, %v, want conclusive", status, ok)
	}

	md := render(t, lanes, cmp.delta[unitTime], conclusive)
	for _, want := range []string{
		"| Lane | Median time/op | Spread (95% CI of the median, at least 5 %) |",
		"| control | 1 µs | ± 5 % (CI ± 0 %) | **4880944.00 MB/s** |",
		"| wrapper (wrapped against control) | not significant | 1.000 | 20, 20 |",
		"The report gives no arithmetic mean.",
		"This is a check of the procedure, not a statistical guarantee.",
	} {
		assert.Truef(t, strings.Contains(md, want), "the report has no %q:\n%s", want, md)
	}
	assert.Falsef(t, strings.Contains(strings.ReplaceAll(md, "The report gives no arithmetic mean.", ""), "mean"), "the report names a mean:\n%s", md)

	// A wide sample: the spread is above the limit.
	wide := []float64{800, 900, 1000, 1100, 1200, 1300, 700, 1400, 950, 1050}
	cmp, err = benchstat(writeSamples(t, dir, Control, wide), writeSamples(t, dir, Wrapped, repeat(1000, 10)))
	require.NoError(t, err)
	{
		s := cmp.first[unitTime]
		assert.Falsef(t, !s.Bounded || s.Spread <= spreadLimit || !aboveLimit(s), "spread of the wide sample = %+v, want above %v percent", s, spreadLimit)
	}
	lanes = map[Lane]map[string]stat{Control: cmp.first, Wrapped: cmp.second, Instrumented: cmp.second}
	{
		_, ok := decide(lanes, nil, false)
		assert.Falsef(t, ok, "decide before the rerun: ok, want a rerun")
	}
	status, ok := decide(lanes, nil, true)
	assert.Falsef(t, !ok || status != inconclusive, "decide after the rerun = %q, %v, want inconclusive", status, ok)
	md = render(t, lanes, cmp.delta[unitTime], status)
	assert.Falsef(t, !strings.Contains(md, "Headline: inconclusive") || strings.Contains(md, "MB/s"), "an inconclusive case shows a rate:\n%s", md)
}

// render writes a report of one json case with the summaries of lanes. Each
// outside reference has the summary of the control lane.
func render(t *testing.T, lanes map[Lane]map[string]stat, cost delta, status caseStatus) string {
	t.Helper()
	c, _ := findCase("json")
	r := Report{
		kind: "baseline", date: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		commit: "0123456789abcdef", branch: "master", runs: 10, rerunRuns: 20,
		command: "go run ./internal/bench/cmd/benchreport",
		cases: []caseReport{{
			c: c, bytes: 4880944, status: status, lanes: lanes, rules: 12,
			wrapperCost: cost, counterCost: cost, wrapped: result{Tokens: 10},
			refs:       map[tool]map[string]stat{textScanner: lanes[Control], regexpLexer: lanes[Control], jsonDecoder: lanes[Control]},
			refResults: map[tool]refResult{textScanner: {Tokens: 8, Errors: 2}, regexpLexer: {Tokens: 10}, jsonDecoder: {Tokens: 6}},
		}},
	}
	r.cases[0].wrapped.Counts.Tokens = 10
	r.cases[0].wrapped.Counts.Bytes = 4880944
	r.cases[0].wrapped.Counts.Reads = 100
	var b bytes.Buffer
	{
		err := r.WriteMarkdown(&b)
		require.NoError(t, err)
	}
	return b.String()
}

// TestDeltaNeverZero checks that a cost that benchstat reports as not
// significant is "not significant", never zero.
func TestDeltaNeverZero(t *testing.T) {
	{
		s := formatDelta(delta{})
		assert.Truef(t, s == "not significant", "formatDelta(not significant) = %q", s)
	}
	{
		s := formatDelta(delta{Percent: 1.5, Significant: true})
		assert.Truef(t, s == "+1.50 %", "formatDelta(+1.5) = %q", s)
	}
}

// TestReportHasEveryFieldOfTheFormat checks the report against the filled
// example of docs/specs/measurement.md, "A filled example": the header row
// of every table of the example is in the report, and so is the first cell
// of every row.
func TestReportHasEveryFieldOfTheFormat(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "specs", "measurement.md"))
	require.NoError(t, err)
	_, example, ok := strings.Cut(string(spec), "### A filled example")
	require.True(t, ok, "the contract has no filled example")
	_, example, _ = strings.Cut(example, "```markdown\n")
	example, _, _ = strings.Cut(example, "```")

	stats := map[string]stat{unitTime: {Median: 1, Spread: 1, Bounded: true}, unitBytes: {Median: 1, Bounded: true}, unitAllocs: {Median: 1, Bounded: true}}
	lanes := map[Lane]map[string]stat{Control: stats, Wrapped: stats, Instrumented: stats}
	md := render(t, lanes, delta{Percent: 1, Significant: true, P: 0.01, N: [2]int{10, 10}}, conclusive)

	var rows int
	for _, line := range strings.Split(example, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| ---") {
			continue
		}
		rows++
		cells := strings.Split(line, "|")
		first := "| " + strings.TrimSpace(cells[1]) + " |"
		assert.Truef(t, strings.Contains(md, "\n"+first), "the report has no row %q", first)
		assert.Falsef(t, !strings.Contains(line, "‹") && !strings.Contains(md, line), "the report has no line %q", line)
	}
	require.Falsef(t, rows < 33, "read %d rows of the example, want the five tables", rows)
}

// TestSpreadFloor checks the resolution floor of the reported spread (owner
// decision, 2026-11-20, "5% resolution floor"): the reported spread is the
// larger of the interval and 5 percent of the median, and the rerun rule
// still uses the interval itself.
func TestSpreadFloor(t *testing.T) {
	cases := []struct {
		ci       float64
		reported float64
		label    string
		above    bool
	}{
		{0, 5, "± 5 % (CI ± 0 %)", false},
		{2, 5, "± 5 % (CI ± 2 %)", false},
		{5, 5, "± 5 %", false},
		{7, 7, "± 7 %", true},
	}
	for _, c := range cases {
		s := stat{Median: 1, Spread: c.ci, Bounded: true}
		{
			got := reportedSpread(s)
			assert.Truef(t, got == c.reported, "CI %v: reported spread %v, want %v", c.ci, got, c.reported)
		}
		{
			got := formatSpread(s)
			assert.Truef(t, got == c.label, "CI %v: label %q, want %q", c.ci, got, c.label)
		}
		{
			got := aboveLimit(s)
			assert.Truef(t, got == c.above, "CI %v: above the limit %v, want %v", c.ci, got, c.above)
		}
	}
	{
		got := formatSpread(stat{Median: 1})
		assert.Truef(t, got == "no interval (∞)", "no interval: label %q", got)
	}
}

// TestReferenceSummary checks the outside references in the report: the
// summary of one raw file by benchstat, the rerun rule, and the table with
// the fixed paragraph. A reference above the spread limit asks for a rerun,
// as a lane does.
func TestReferenceSummary(t *testing.T) {
	benchstatOrSkip(t)
	dir := t.TempDir()
	var b bytes.Buffer
	b.WriteString("goos: linux\ngoarch: amd64\npkg: github.com/xiam/bytescan/internal/bench\nlane: control\n")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, "BenchmarkReference/json/regexp-12 \t       1\t%d ns/op\t     200 B/op\t       4 allocs/op\n", 2000)
	}
	path := filepath.Join(dir, "json-ref-regexp.txt")
	{
		err := os.WriteFile(path, b.Bytes(), 0o644)
		require.NoError(t, err)
	}
	sum, err := summary(path)
	require.NoError(t, err)
	{
		got := sum[unitTime]
		assert.Falsef(t, math.Abs(got.Median-2000e-9) > 1e-15 || !got.Bounded || got.Spread != 0, "time = %+v, want a median of 2 µs and an interval of 0 percent", got)
	}
	if sum[unitBytes].Median != 200 || sum[unitAllocs].Median != 4 {
		assert.Failf(t, "unexpected test state", "B/op %v, allocs/op %v, want 200 and 4", sum[unitBytes].Median, sum[unitAllocs].Median)
	}

	ok := map[string]stat{unitTime: {Median: 1, Spread: 1, Bounded: true}}
	wide := map[string]stat{unitTime: {Median: 1, Spread: 9, Bounded: true}}
	lanes := map[Lane]map[string]stat{Control: ok, Wrapped: ok, Instrumented: ok}
	{
		_, done := decide(lanes, map[tool]map[string]stat{textScanner: ok, regexpLexer: wide}, false)
		assert.Falsef(t, done, "decide with a wide reference: done, want a rerun")
	}
	{
		status, _ := decide(lanes, map[tool]map[string]stat{regexpLexer: wide}, true)
		assert.Truef(t, status == inconclusive, "decide after the rerun = %q, want inconclusive", status)
	}

	md := render(t, map[Lane]map[string]stat{Control: sum, Wrapped: sum, Instrumented: sum}, delta{}, conclusive)
	for _, want := range []string{
		"| textscanner | 2 µs | ± 5 % (CI ± 0 %) | 2440472.00 MB/s | 4 | 200 | 8 | 2 |",
		"| regexp | 2 µs | ± 5 % (CI ± 0 %) | 2440472.00 MB/s | 4 | 200 | 10 | 0 |",
		"| jsondecoder | 2 µs | ± 5 % (CI ± 0 %) | 2440472.00 MB/s | 4 | 200 | 6 | 0 |",
		referenceNotes,
		"The Tokens column counts returned decoder events.",
	} {
		assert.Truef(t, strings.Contains(md, want), "the report has no %q:\n%s", want, md)
	}
}
