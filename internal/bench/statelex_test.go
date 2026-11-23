package bench

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEditCasesMatchTheBenchmarks checks that every set, shape and size of
// the runner is a name of BenchmarkEdit in statelex/latency_bench_test.go,
// and that the runner has 66 cases.
func TestEditCasesMatchTheBenchmarks(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "statelex", "latency_bench_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{editSets, editShapes, editSizes} {
		for _, name := range names {
			if !bytes.Contains(src, []byte(`"`+name+`"`)) {
				t.Errorf("statelex/latency_bench_test.go names no %q", name)
			}
		}
	}
	if n := len(editCases()); n != 66 {
		t.Errorf("%d cases, want 66", n)
	}
}

func TestSelectEdits(t *testing.T) {
	cs, err := selectEdits("A-key-start-64KiB,B-quote-start-4MiB")
	if err != nil {
		t.Fatal(err)
	}
	want := []editCase{{"A", "key-start", "64KiB"}, {"B", "quote-start", "4MiB"}}
	if fmt.Sprint(cs) != fmt.Sprint(want) {
		t.Errorf("selectEdits = %v, want %v", cs, want)
	}
	if c := cs[0]; c.pattern() != "^BenchmarkEdit$/^A$/^key-start$/^64KiB$" {
		t.Errorf("pattern = %q", c.pattern())
	}
	if _, err := selectEdits("json"); err == nil {
		t.Error("selectEdits accepted a case of the baseline")
	}
}

// writeEditSamples writes a raw file of case A-key-start-64KiB in the format
// of the runner: each run starts with "lane: control".
func writeEditSamples(t *testing.T, dir string, ns []float64) string {
	t.Helper()
	var b bytes.Buffer
	for _, v := range ns {
		fmt.Fprintf(&b, "lane: control\ngoos: linux\ngoarch: amd64\npkg: github.com/xiam/bytescan/statelex\n")
		fmt.Fprintf(&b, "BenchmarkEdit/A/key-start/64KiB-12 \t    1000\t%.0f ns/op\t 2.000 copied-bytes/op\t 1.000 inserted-tokens/op\t 3.000 relexed-bytes/op\t 1.000 removed-tokens/op\t 56.50 visited-tokens/op\t 5256 B/op\t 82 allocs/op\n", v)
	}
	path := filepath.Join(dir, "A-key-start-64KiB-control.txt")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStatelexReportHasEveryField runs benchstat over a fixed raw file of one
// case and checks that the report has the header of the contract and every
// column of the statelex table, with the median of each metric.
func TestStatelexReportHasEveryField(t *testing.T) {
	benchstatOrSkip(t)
	units, err := benchstatOne(writeEditSamples(t, t.TempDir(), append(repeat(30000, 9), 90000)))
	if err != nil {
		t.Fatal(err)
	}
	r := &Report{
		kind: "statelex", date: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		commit: "0123456789abcdef", branch: "master", host: "runner", cpu: "cpu", cpus: 12, kernel: "6.8",
		loadStart: "1.00", loadEnd: "2.00", goVersion: "go1.25.4 linux/amd64", gomaxprocs: 12,
		benchstat: checkedBenchstat, runs: 10, rerunRuns: 20,
		command: "go run ./internal/bench/cmd/benchreport -kind statelex", rawDir: "raw/2026-11-20-0123456789ab/",
	}
	var b bytes.Buffer
	res := editResult{c: editCase{"A", "key-start", "64KiB"}, status: conclusive, units: units}
	if err := writeEdits(&b, r, []editResult{res}); err != nil {
		t.Fatal(err)
	}
	md := b.String()
	for _, want := range []string{
		"# Statelex report, 2026-11-20",
		"| Commit | `0123456789ab` (`master`) |",
		"| Machine | shared runner `runner`, `cpu`, 12 logical CPUs, Linux `6.8` |",
		"| Load | 1.00 at the start, 2.00 at the end |",
		"| Go | `go1.25.4 linux/amd64`, GOMAXPROCS 12 |",
		"| benchstat | `" + checkedBenchstat + "` |",
		"| Method | 10 runs per case, one lane (control), cases interleaved; `-test.benchtime 200ms`; spread limit 5 percent; rerun once with 20 |",
		"| Command | `go run ./internal/bench/cmd/benchreport -kind statelex` |",
		"| Raw samples | `raw/2026-11-20-0123456789ab/` |",
		"## Rule set A, key-start",
		"| Size | Median time/op | Spread (95% CI of the median, at least 5 %) | Status | Change (removed / inserted tokens) | Copied bytes | Visited tokens | Re-lexed bytes | allocs/op | B/op |",
		"| 64KiB | 30 µs | ± 5 % (CI ± 0 %) | conclusive | 1 / 1 | 2 | 56.5 | 3 | 82 | 5,256 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the report has no %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "MB/s") {
		t.Errorf("the report gives a rate:\n%s", md)
	}
}
