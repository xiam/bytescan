package bench

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file is the statelex path of the runner (-kind statelex): it runs the
// benchmark BenchmarkEdit of the test files of package statelex, which report
// the size of the change and the work amounts of one edit with
// testing.B.ReportMetric. The names, the metrics, the single lane, the
// benchtime, the raw file names and the layout of the report are the owner
// decision of 2026-11-20 in docs/specs/measurement.md, "The statelex report".

// The sets, the shapes and the sizes of BenchmarkEdit/<set>/<shape>/<size>,
// in the order of the report. They match latencySets, latencyShapes and
// latencySizes in statelex/latency_bench_test.go.
var (
	editSets   = []string{"A", "B"}
	editShapes = []string{
		"load", "key-start", "key-middle", "key-end",
		"backspace-start", "backspace-middle", "backspace-end",
		"selection-middle", "quote-start", "unquote-start", "settext-middle",
	}
	editSizes = []string{"64KiB", "512KiB", "4MiB"}
)

// editBenchtime is the -test.benchtime of each run of BenchmarkEdit.
const editBenchtime = "200ms"

// The units of the metrics of BenchmarkEdit, as benchstat reports them.
const (
	unitRemoved  = "removed-tokens/op"
	unitInserted = "inserted-tokens/op"
	unitCopied   = "copied-bytes/op"
	unitVisited  = "visited-tokens/op"
	unitRelexed  = "relexed-bytes/op"
)

// editCase is one benchmark of BenchmarkEdit.
type editCase struct {
	set, shape, size string
}

// name is the name of the case in the raw files and in -cases:
// <set>-<shape>-<size>.
func (c editCase) name() string {
	return c.set + "-" + c.shape + "-" + c.size
}

// pattern is the -test.bench pattern that selects only c.
func (c editCase) pattern() string {
	return "^BenchmarkEdit$/^" + c.set + "$/^" + regexp.QuoteMeta(c.shape) + "$/^" + c.size + "$"
}

// editCases returns every case, in the order of the report.
func editCases() []editCase {
	var cs []editCase
	for _, set := range editSets {
		for _, shape := range editShapes {
			for _, size := range editSizes {
				cs = append(cs, editCase{set, shape, size})
			}
		}
	}
	return cs
}

// selectEdits returns the cases that a -cases list names, or every case for
// an empty list.
func selectEdits(only string) ([]editCase, error) {
	all := editCases()
	if only == "" {
		return all, nil
	}
	var cs []editCase
	for _, name := range strings.Split(only, ",") {
		found := false
		for _, c := range all {
			if c.name() == name {
				cs, found = append(cs, c), true
			}
		}
		if !found {
			return nil, fmt.Errorf("-cases: no case %q of -kind statelex; a case is <set>-<shape>-<size>, such as A-key-start-64KiB", name)
		}
	}
	return cs, nil
}

// editResult is the result of one case: its status and the summary of each
// unit of its control lane.
type editResult struct {
	c      editCase
	status caseStatus
	units  map[string]stat
}

// reportEdits writes the statelex report: the header of the contract and one
// table for each set and shape, with one row for each size.
func reportEdits(cfg config, goCmd string) error {
	root, err := moduleRoot(goCmd)
	if err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	start := time.Now().UTC()
	r := &Report{
		kind:      cfg.kind,
		date:      start,
		runs:      cfg.runs,
		rerunRuns: 2 * cfg.runs,
		command:   strings.TrimSpace("go run ./internal/bench/cmd/benchreport " + strings.Join(os.Args[1:], " ")),
		loadStart: loadAverage(),
	}
	if err := describe(r, goCmd); err != nil {
		return err
	}
	date := start.Format("2006-01-02")
	reportPath := filepath.Join(cfg.out, cfg.kind+"-"+date+".md")
	rawDir := filepath.Join(cfg.out, "raw", date+"-"+r.commit[:12])
	r.rawDir = filepath.Join("raw", date+"-"+r.commit[:12]) + "/"
	for _, p := range []string{reportPath, rawDir} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s exists; move it away first", p)
		}
	}
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "benchreport-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "statelex.test")
	cmd := exec.Command(goCmd, "test", "-c", "-o", bin, "./statelex")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go test -c ./statelex: %w", err)
	}

	results, err := editRounds(cfg.edits, cfg.runs, bin, rawDir, r, false)
	if err != nil {
		return err
	}
	var again []editCase
	for _, res := range results {
		if res.status == "" {
			again = append(again, res.c)
		}
	}
	if len(again) > 0 {
		fmt.Fprintf(os.Stderr, "%d cases are above the spread limit; running them again with %d runs\n", len(again), 2*cfg.runs)
		super := filepath.Join(rawDir, "superseded")
		if err := os.MkdirAll(super, 0o755); err != nil {
			return err
		}
		for _, c := range again {
			if err := os.Rename(editRawFile(rawDir, c), editRawFile(super, c)); err != nil {
				return err
			}
		}
		rerun, err := editRounds(again, 2*cfg.runs, bin, rawDir, r, true)
		if err != nil {
			return err
		}
		for i := range results {
			for _, res := range rerun {
				if results[i].c == res.c {
					results[i] = res
				}
			}
		}
	}
	r.loadEnd = loadAverage()

	var b bytes.Buffer
	if err := writeEdits(&b, r, results); err != nil {
		return err
	}
	if err := os.WriteFile(reportPath, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s and %s\n", reportPath, rawDir)
	return nil
}

func editRawFile(dir string, c editCase) string {
	return filepath.Join(dir, c.name()+"-"+string(Control)+".txt")
}

// editRounds runs n rounds of the cases. A round runs each case once, each
// run in its own process, and the first case of a round rotates. It writes
// the raw file of each case and returns the summary of each case, with an
// empty status for a case above the spread limit before the rerun.
func editRounds(cs []editCase, n int, bin, rawDir string, r *Report, rerun bool) ([]editResult, error) {
	raw := make([]bytes.Buffer, len(cs))
	for round := 0; round < n; round++ {
		for i := range cs {
			k := (round + i) % len(cs)
			c := cs[k]
			fmt.Fprintf(os.Stderr, "case %s, round %d of %d\n", c.name(), round+1, n)
			out, procs, err := runEdit(bin, "statelex", c)
			if err != nil {
				return nil, err
			}
			r.gomaxprocs = procs
			fmt.Fprintf(&raw[k], "lane: %s\n", Control)
			raw[k].Write(out)
		}
	}
	results := make([]editResult, len(cs))
	for i, c := range cs {
		path := editRawFile(rawDir, c)
		if err := os.WriteFile(path, raw[i].Bytes(), 0o644); err != nil {
			return nil, err
		}
		units, err := benchstatOne(path)
		if err != nil {
			return nil, err
		}
		status, _ := rerunRule(aboveLimit(units[unitTime]), rerun)
		results[i] = editResult{c: c, status: status, units: units}
	}
	return results, nil
}

var editLine = regexp.MustCompile(`^BenchmarkEdit/([A-Z])/([a-z-]+)/([0-9A-Za-z]+?)(?:-(\d+))?\s+\d+\s+[0-9.]+ ns/op`)

// runEdit runs case c once, in its own process in directory dir, the
// directory of package statelex, and returns the output of the run and its
// GOMAXPROCS. The output has exactly one benchmark line, of c, with every
// metric of BenchmarkEdit.
func runEdit(bin, dir string, c editCase) ([]byte, int, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin,
		"-test.run", "^$",
		"-test.bench", c.pattern(),
		"-test.benchtime", editBenchtime,
		"-test.count", "1",
		"-test.timeout", "30m")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("case %s failed: %w\n%s%s", c.name(), err, stdout.Bytes(), stderr.Bytes())
	}
	out := stdout.Bytes()
	procs, lines := 0, 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := editLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		if (editCase{m[1], m[2], m[3]}) != c {
			return nil, 0, fmt.Errorf("case %s: unexpected line %q", c.name(), sc.Text())
		}
		for _, u := range []string{unitRemoved, unitInserted, unitCopied, unitVisited, unitRelexed, unitBytes, unitAllocs} {
			if !strings.Contains(sc.Text(), " "+u) {
				return nil, 0, fmt.Errorf("case %s: the line has no %s: %q", c.name(), u, sc.Text())
			}
		}
		procs = 1
		if m[4] != "" {
			procs, _ = strconv.Atoi(m[4])
		}
		lines++
	}
	if lines != 1 {
		return nil, 0, fmt.Errorf("case %s: the output has %d benchmark lines, want 1:\n%s", c.name(), lines, out)
	}
	return out, procs, nil
}

// benchstatOne runs benchstat over one raw file of one case and returns the
// summary of each unit.
func benchstatOne(path string) (map[string]stat, error) {
	p, err := benchstatPath()
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(p, "-format", "csv", "-col", "lane", path)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bench: benchstat %s: %w\n%s", path, err, stderr.Bytes())
	}
	return parseBenchstatOne(stdout.Bytes())
}

// parseBenchstatOne reads the CSV output of benchstat -format csv for one
// column and one benchmark with parseSummary, and checks that it has every
// unit of the statelex report.
func parseBenchstatOne(out []byte) (map[string]stat, error) {
	units, err := parseSummary(out)
	if err != nil {
		return nil, err
	}
	for _, u := range []string{unitTime, unitRemoved, unitInserted, unitCopied, unitVisited, unitRelexed, unitBytes, unitAllocs} {
		if _, ok := units[u]; !ok {
			return nil, fmt.Errorf("bench: benchstat output has no %s table:\n%s", u, out)
		}
	}
	return units, nil
}

// writeEdits writes the statelex report.
func writeEdits(w io.Writer, r *Report, results []editResult) error {
	p := &printer{w: w}
	modified := ""
	if r.modified {
		modified = ", with changes to tracked files"
	}
	p.f("# %s, %s\n\n", title(r.kind), r.date.Format("2006-01-02"))
	p.f("| Field | Value |\n| --- | --- |\n")
	p.f("| Commit | `%s` (`%s`%s) |\n", r.commit[:min(12, len(r.commit))], r.branch, modified)
	p.f("| Machine | shared runner `%s`, `%s`, %d logical CPUs, Linux `%s` |\n", r.host, r.cpu, r.cpus, r.kernel)
	p.f("| Load | %s at the start, %s at the end |\n", r.loadStart, r.loadEnd)
	p.f("| Go | `%s`, GOMAXPROCS %d |\n", r.goVersion, r.gomaxprocs)
	p.f("| benchstat | `%s` |\n", r.benchstat)
	p.f("| Method | %d runs per case, one lane (control), cases interleaved; `-test.benchtime %s`; spread limit %s percent; rerun once with %d |\n",
		r.runs, editBenchtime, trimFloat(spreadLimit), r.rerunRuns)
	p.f("| Command | `%s` |\n", r.command)
	p.f("| Raw samples | `%s` |\n\n", r.rawDir)

	p.f("Each case is one benchmark `BenchmarkEdit/<set>/<shape>/<size>` of `statelex/latency_bench_test.go`. ")
	p.f("One operation is one edit: the timer covers only the call of `Apply` or `SetText`, and the edit that puts the text back runs with the timer stopped. ")
	p.f("Each run is one process with `-test.benchtime %s`; within a run, `go test` divides the timed interval and each metric by the operations of the run. ", editBenchtime)
	p.f("A round runs every case once, and the first case of a round rotates. ")
	p.f("The estimator is the median of the runs, as `benchstat` reports it. ")
	p.f("The spread is the larger of `benchstat`'s 95 percent confidence interval of the median (CI), as a percentage of the median, and %s percent. ", trimFloat(spreadFloor))
	p.f("The rerun rule uses the CI of the time. ")
	p.f("The `statelex` work counters are not under the build tag, so the report has one lane, control, and no wrapper cost or counter cost.\n\n")
	p.f("The columns: the change in tokens (removed / inserted), the bytes of the held text that were copied, the held tokens that were visited, ")
	p.f("and the length in bytes of the run that was lexed again. Each is the median of the runs.\n")

	for _, set := range editSets {
		for _, shape := range editShapes {
			var rows []editResult
			for _, res := range results {
				if res.c.set == set && res.c.shape == shape {
					rows = append(rows, res)
				}
			}
			if len(rows) == 0 {
				continue
			}
			p.f("\n## Rule set %s, %s\n\n", set, shape)
			p.f("| Size | Median time/op | Spread (95%% CI of the median, at least 5 %%) | Status | Change (removed / inserted tokens) | Copied bytes | Visited tokens | Re-lexed bytes | allocs/op | B/op |\n")
			p.f("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
			for _, res := range rows {
				u := res.units
				t := formatTime(u[unitTime].Median)
				if res.status == inconclusive {
					t = "inconclusive (" + t + ")"
				}
				p.f("| %s | %s | %s | %s | %s / %s | %s | %s | %s | %s | %s |\n",
					res.c.size, t, formatSpread(u[unitTime]), res.status,
					metric(u[unitRemoved]), metric(u[unitInserted]), metric(u[unitCopied]),
					metric(u[unitVisited]), metric(u[unitRelexed]), metric(u[unitAllocs]), metric(u[unitBytes]))
			}
		}
	}
	return p.err
}

// metric returns the median of a count for the report: with one decimal when
// it is under 1,000 and not whole, and otherwise rounded, with commas between
// groups of three digits.
func metric(s stat) string {
	if v := s.Median; v < 1000 && v != float64(uint64(v)) {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return group(uint64(s.Median + 0.5))
}
