package bench

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// The units of benchstat that the report reads.
const (
	unitTime   = "sec/op"
	unitBytes  = "B/op"
	unitAllocs = "allocs/op"
)

// stat is benchstat's summary of the samples of one lane in one unit.
type stat struct {
	// Median is the median of the samples, in the unit.
	Median float64
	// Spread is benchstat's 95 percent confidence interval of the median, as
	// a percentage of the median. It is meaningful only when Bounded is true.
	Spread float64
	// Bounded is false when benchstat gives no interval (∞), for example for
	// fewer than 6 samples.
	Bounded bool
}

// delta is benchstat's comparison of the medians of two lanes in one unit.
type delta struct {
	// Percent is the change of the median of the second lane against the
	// first. It is meaningful only when Significant is true.
	Percent float64
	// Significant is false when benchstat reports the change as "~": not
	// significant.
	Significant bool
	// P is the p-value of the comparison.
	P float64
	// N holds the sample counts of the first and the second lane.
	N [2]int
}

// comparison is benchstat's output for two lanes of one case: the summary of
// each lane and the comparison of the second lane against the first, by unit.
type comparison struct {
	first, second map[string]stat
	delta         map[string]delta
}

// errNoBenchstat means that the benchstat command is not on the PATH.
var errNoBenchstat = errors.New("bench: benchstat is not on the PATH; install it with go install golang.org/x/perf/cmd/benchstat@latest")

// benchstatPath returns the path of the benchstat command, or errNoBenchstat.
func benchstatPath() (string, error) {
	p, err := exec.LookPath("benchstat")
	if err != nil {
		return "", errNoBenchstat
	}
	return p, nil
}

// benchstatVersion returns the module version of the benchstat command, as go
// version -m reports it, for example "golang.org/x/perf v0.0.0-...".
func benchstatVersion(goCmd string) (string, error) {
	p, err := benchstatPath()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(goCmd, "version", "-m", p).Output()
	if err != nil {
		return "", fmt.Errorf("bench: go version -m %s: %w", p, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "mod" {
			return f[1] + " " + f[2], nil
		}
	}
	return "", fmt.Errorf("bench: go version -m %s names no module", p)
}

// benchstat runs benchstat over two files of samples, each of one lane of
// one benchmark, with the lanes as columns (-col lane). The first file is the
// base of the comparison.
func benchstat(first, second string) (comparison, error) {
	return benchstatBy("lane", first, second)
}

// benchstatBy runs benchstat over two files of samples of one benchmark with
// the columns of col (benchstat -col): "lane" for two lanes, or ".file" for
// two files of the same lane, as in a comparison of two commits. The first
// file is the base of the comparison.
func benchstatBy(col, first, second string) (comparison, error) {
	p, err := benchstatPath()
	if err != nil {
		return comparison{}, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(p, "-format", "csv", "-col", col, first, second)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return comparison{}, fmt.Errorf("bench: benchstat %s %s: %w\n%s", first, second, err, stderr.Bytes())
	}
	return parseBenchstat(stdout.Bytes())
}

// configLine matches a configuration line of benchstat's output, such as
// "goos: linux".
var configLine = regexp.MustCompile(`^[^,\s]+: `)

var pField = regexp.MustCompile(`^p=([0-9.]+) n=([0-9]+)(?:\+([0-9]+))?$`)

// parseBenchstat reads the CSV output of benchstat -format csv for two
// columns and one benchmark. benchstat prints a table for each unit, after
// the configuration lines ("key: value"), with blank lines between them.
func parseBenchstat(out []byte) (comparison, error) {
	cmp := comparison{first: map[string]stat{}, second: map[string]stat{}, delta: map[string]delta{}}
	for _, block := range strings.Split(string(out), "\n\n") {
		var lines []string
		for _, l := range strings.Split(block, "\n") {
			if l != "" && !configLine.MatchString(l) {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			continue
		}
		rows, err := csv.NewReader(strings.NewReader(strings.Join(lines, "\n"))).ReadAll()
		if err != nil && !errors.Is(err, io.EOF) {
			return comparison{}, fmt.Errorf("bench: benchstat output: %w", err)
		}
		// rows[0]: lanes; rows[1]: unit, CI, unit, CI, vs base, P; then one
		// row for the benchmark and one for the geomean.
		var data [][]string
		for _, r := range rows[min(2, len(rows)):] {
			if len(r) > 0 && r[0] != "geomean" {
				data = append(data, r)
			}
		}
		if len(rows) < 3 || len(rows[1]) < 7 || len(data) != 1 || len(data[0]) < 7 {
			return comparison{}, fmt.Errorf("bench: benchstat output: want two lanes and one benchmark, got %q", block)
		}
		unit, r := rows[1][1], data[0]
		a, err := parseStat(r[1], r[2])
		if err != nil {
			return comparison{}, err
		}
		b, err := parseStat(r[3], r[4])
		if err != nil {
			return comparison{}, err
		}
		d, err := parseDelta(r[5], r[6])
		if err != nil {
			return comparison{}, err
		}
		cmp.first[unit], cmp.second[unit], cmp.delta[unit] = a, b, d
	}
	if _, ok := cmp.first[unitTime]; !ok {
		return comparison{}, fmt.Errorf("bench: benchstat output has no %s table:\n%s", unitTime, out)
	}
	return cmp, nil
}

func parseStat(center, ci string) (stat, error) {
	m, err := strconv.ParseFloat(center, 64)
	if err != nil {
		return stat{}, fmt.Errorf("bench: benchstat median %q: %w", center, err)
	}
	s := stat{Median: m}
	if ci == "∞" {
		return s, nil
	}
	s.Spread, err = strconv.ParseFloat(strings.TrimSuffix(ci, "%"), 64)
	if err != nil || !strings.HasSuffix(ci, "%") {
		return stat{}, fmt.Errorf("bench: benchstat interval %q", ci)
	}
	s.Bounded = true
	return s, nil
}

func parseDelta(vs, p string) (delta, error) {
	m := pField.FindStringSubmatch(p)
	if m == nil {
		return delta{}, fmt.Errorf("bench: benchstat comparison %q", p)
	}
	var d delta
	d.P, _ = strconv.ParseFloat(m[1], 64)
	d.N[0], _ = strconv.Atoi(m[2])
	d.N[1] = d.N[0]
	if m[3] != "" {
		d.N[1], _ = strconv.Atoi(m[3])
	}
	if vs == "~" {
		return d, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(vs, "%"), 64)
	if err != nil || !strings.HasSuffix(vs, "%") {
		return delta{}, fmt.Errorf("bench: benchstat change %q", vs)
	}
	d.Percent, d.Significant = v, true
	return d, nil
}

// summary runs benchstat over one file of samples of one benchmark, such as
// the raw file of a reference, and returns its summary by unit.
func summary(file string) (map[string]stat, error) {
	p, err := benchstatPath()
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(p, "-format", "csv", file)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bench: benchstat %s: %w\n%s", file, err, stderr.Bytes())
	}
	return parseSummary(stdout.Bytes())
}

// parseSummary reads the CSV output of benchstat -format csv for one column
// and one benchmark: a table for each unit, after the configuration lines.
func parseSummary(out []byte) (map[string]stat, error) {
	sum := map[string]stat{}
	for _, block := range strings.Split(string(out), "\n\n") {
		var lines []string
		for _, l := range strings.Split(block, "\n") {
			if l != "" && !configLine.MatchString(l) {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			continue
		}
		rows, err := csv.NewReader(strings.NewReader(strings.Join(lines, "\n"))).ReadAll()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("bench: benchstat output: %w", err)
		}
		// rows[0]: the file; rows[1]: unit, CI; then one row for the
		// benchmark and one for the geomean.
		var data [][]string
		for _, r := range rows[min(2, len(rows)):] {
			if len(r) > 0 && r[0] != "geomean" {
				data = append(data, r)
			}
		}
		if len(rows) < 3 || len(rows[1]) < 3 || len(data) != 1 || len(data[0]) < 3 {
			return nil, fmt.Errorf("bench: benchstat output: want one column and one benchmark, got %q", block)
		}
		s, err := parseStat(data[0][1], data[0][2])
		if err != nil {
			return nil, err
		}
		sum[rows[1][1]] = s
	}
	if _, ok := sum[unitTime]; !ok {
		return nil, fmt.Errorf("bench: benchstat output has no %s table:\n%s", unitTime, out)
	}
	return sum, nil
}
