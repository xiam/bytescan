package bench

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeSide writes the raw file of one side of case json in the format of the
// runner, in nanoseconds per operation, and returns its path.
func writeSide(t *testing.T, dir, sideName string, ns []float64) string {
	t.Helper()
	var b bytes.Buffer
	for _, v := range ns {
		fmt.Fprintf(&b, "lane: control\ngoos: linux\ngoarch: amd64\npkg: github.com/xiam/bytescan/internal/bench\n")
		fmt.Fprintf(&b, "BenchmarkCase/json-12 \t       1\t%.0f ns/op\t     100 B/op\t       3 allocs/op\nPASS\n", v)
	}
	path := compareFile(dir, "json", sideName)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// near returns n samples around v, within ± 1 percent.
func near(v float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v * (1 + float64(i%5-2)/200)
	}
	return s
}

// compareFixture runs benchstat over a fixed pair of samples of case json and
// returns the case with the status of the rerun rule.
func compareFixture(t *testing.T, base, head []float64, rerun bool) (compareCase, bool) {
	t.Helper()
	dir := t.TempDir()
	cmp, err := benchstatBy(".file", writeSide(t, dir, sideBase, base), writeSide(t, dir, sideHead, head))
	if err != nil {
		t.Fatal(err)
	}
	status, ok := rerunRule(aboveLimit(cmp.first[unitTime]) || aboveLimit(cmp.second[unitTime]), rerun)
	return compareCase{name: "json", bytes: 1000, status: status, cmp: cmp}, ok
}

// fixedReport returns a comparison report with a fixed header. The head has
// the tree of the base when sameTree is set.
func fixedReport(sameTree bool, cs ...compareCase) *compareReport {
	r := &compareReport{
		base:  side{name: sideBase, ref: "master", commit: "0123456789abcdef0123", tree: "aaaaaaaaaaaaaaaa"},
		head:  side{name: sideHead, ref: "feat/x", commit: "fedcba9876543210fedc", tree: "bbbbbbbbbbbbbbbb"},
		cases: cs,
	}
	if sameTree {
		r.head.ref, r.head.commit, r.head.tree = "master", r.base.commit, r.base.tree
	}
	r.Report = Report{
		kind: "compare", date: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC),
		host: "runner", cpu: "cpu", cpus: 12, kernel: "6.8",
		loadStart: "1.00", loadEnd: "2.00", goVersion: "go1.25.4 linux/amd64", gomaxprocs: 12,
		benchstat: checkedBenchstat, runs: 10, rerunRuns: 20,
		command: "go run ./internal/bench/cmd/benchreport -kind compare -base master -head feat/x -cases json",
		rawDir:  "2026-11-21-0123456789ab-fedcba987654/",
	}
	return r
}

func writeReport(t *testing.T, r *compareReport) string {
	t.Helper()
	var b bytes.Buffer
	if err := writeCompare(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func wantLines(t *testing.T, md string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(md, w) {
			t.Errorf("the report has no %q:\n%s", w, md)
		}
	}
}

// TestCompareFaster checks a head that is faster than the base beyond the
// interval, and that the report carries every field of a comparison: the two
// refs, the machine, the load, the Go version, the run count, and for each
// side the median with its spread, the rate, allocs/op and B/op, and
// benchstat's change with its p-value.
func TestCompareFaster(t *testing.T) {
	benchstatOrSkip(t)
	c, ok := compareFixture(t, near(1000, 10), near(700, 10), false)
	if !ok || c.status != conclusive {
		t.Fatalf("rerun rule = %q, %v, want conclusive", c.status, ok)
	}
	if d := c.cmp.delta[unitTime]; !d.Significant || d.Percent > -25 || d.N != [2]int{10, 10} {
		t.Fatalf("delta = %+v, want a significant change of about -30 percent with 10 and 10 samples", d)
	}
	md := writeReport(t, fixedReport(false, c))
	wantLines(t, md,
		"# Compare report, 2026-11-21\n",
		"| Base | `master` at `0123456789ab` |",
		"| Head | `feat/x` at `fedcba987654` |",
		"| Machine | shared runner `runner`, `cpu`, 12 logical CPUs, Linux `6.8` |",
		"| Load | 1.00 at the start, 2.00 at the end |",
		"| Go | `go1.25.4 linux/amd64`, GOMAXPROCS 12 |",
		"| benchstat | `"+checkedBenchstat+"` |",
		"| Method | 10 runs per side, base and head alternated in each round, one lane (control); spread limit 5 percent; rerun once with 20 |",
		"| Command | `go run ./internal/bench/cmd/benchreport -kind compare -base master -head feat/x -cases json` |",
		"| Raw samples | `2026-11-21-0123456789ab-fedcba987654/` |",
		"## Case json\n\nInput 1,000 bytes. Status: **conclusive**.",
		"| Side | Median time/op | Spread (95% CI of the median, at least 5 %) | Rate from the median | allocs/op | B/op |",
		"| base | 1 µs | ± 5 % (CI ± ",
		" | 1000.00 MB/s | 3 | 100 |",
		"| head | 700 ns | ± 5 % (CI ± ",
		" | 1428.57 MB/s | 3 | 100 |",
		"| Change (head against base) | Delta of the medians | p-value | Samples |",
		"| time/op | -30.00 % | 0.000 | 10, 10 |",
		"| allocs/op | not significant | 1.000 | 10, 10 |",
		"| B/op | not significant | 1.000 | 10, 10 |",
		"Result: the head is faster than the base, -30.00 % (p=0.000), significant, at or above the 5 percent floor.",
	)
	if strings.Contains(md, "same-tree control") {
		t.Errorf("a comparison of two trees is labelled a control:\n%s", md)
	}
}

// TestCompareNotSignificant checks a head inside the interval of the base:
// the change is "not significant", never zero and never a gain.
func TestCompareNotSignificant(t *testing.T) {
	benchstatOrSkip(t)
	c, ok := compareFixture(t, near(1000, 10), near(1001, 10), false)
	if !ok || c.status != conclusive {
		t.Fatalf("rerun rule = %q, %v, want conclusive", c.status, ok)
	}
	if d := c.cmp.delta[unitTime]; d.Significant {
		t.Fatalf("delta = %+v, want not significant", d)
	}
	md := writeReport(t, fixedReport(false, c))
	wantLines(t, md, "| time/op | not significant | ", "Result: not significant (p=")
	if strings.Contains(md, "Result: the head is") || strings.Contains(md, "+0.00 %") || strings.Contains(md, "-0.00 %") {
		t.Errorf("a not-significant change is written as a change:\n%s", md)
	}
}

// TestCompareRerun checks the rerun rule of a comparison: a side above the
// spread limit asks for a rerun, and after the rerun the case is
// inconclusive and has no rate.
func TestCompareRerun(t *testing.T) {
	benchstatOrSkip(t)
	wide := []float64{800, 900, 1000, 1100, 1200, 1300, 700, 1400, 950, 1050}
	if _, ok := compareFixture(t, near(1000, 10), wide, false); ok {
		t.Error("a wide head before the rerun: no rerun")
	}
	if _, ok := compareFixture(t, wide, near(1000, 10), false); ok {
		t.Error("a wide base before the rerun: no rerun")
	}
	c, ok := compareFixture(t, near(1000, 10), wide, true)
	if !ok || c.status != inconclusive {
		t.Fatalf("rerun rule after the rerun = %q, %v, want inconclusive", c.status, ok)
	}
	md := writeReport(t, fixedReport(false, c))
	wantLines(t, md, "Status: **inconclusive**.", "Result: inconclusive.")
	if strings.Contains(md, "MB/s") {
		t.Errorf("an inconclusive case shows a rate:\n%s", md)
	}
	if c, ok := compareFixture(t, near(1000, 10), near(1000, 10), true); !ok || c.status != conclusiveAfterRerun {
		t.Errorf("rerun rule after a good rerun = %q, %v, want conclusive after a rerun", c.status, ok)
	}
}

// TestCompareSameTreeControl checks the header of a same-tree control, and
// that a significant difference in a control is a measurement-bias finding,
// not a gain.
func TestCompareSameTreeControl(t *testing.T) {
	benchstatOrSkip(t)
	same, _ := compareFixture(t, near(1000, 10), near(1001, 10), false)
	md := writeReport(t, fixedReport(true, same))
	wantLines(t, md,
		"# Compare report, 2026-11-21: same-tree control\n",
		"**Same-tree control.** The base and the head have the same tree, `aaaaaaaaaaaa`",
		"| Head | `master` at `0123456789ab` |",
		"Result: not significant (p=",
	)
	biased, _ := compareFixture(t, near(1000, 10), near(900, 10), false)
	md = writeReport(t, fixedReport(true, biased))
	wantLines(t, md, "Result: a significant difference of -10.00 % (p=0.000) between two sides of the same tree. This is a measurement-bias finding about the runner or the harness, not a gain.")
	if strings.Contains(md, "Result: the head is") {
		t.Errorf("a control calls a difference a change of the head:\n%s", md)
	}
}

// TestCompareUnderFloor checks a significant change under the 5 percent floor.
func TestCompareUnderFloor(t *testing.T) {
	r := fixedReport(false)
	c := compareCase{status: conclusive, cmp: comparison{delta: map[string]delta{unitTime: {Percent: 3, Significant: true, P: 0.01}}}}
	if got, want := r.result(&c), "Result: the head is slower than the base, +3.00 % (p=0.010), significant, under the 5 percent floor."; got != want {
		t.Errorf("result = %q, want %q", got, want)
	}
}

// TestCompareStatelex checks the report of the statelex path: no rate, and
// the work amounts of each side with their changes.
func TestCompareStatelex(t *testing.T) {
	benchstatOrSkip(t)
	dir := t.TempDir()
	base := writeEditSamples(t, dir, near(30000, 10))
	other := filepath.Join(dir, "head")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	head := writeEditSamples(t, other, near(20000, 10))
	cmp, err := benchstatBy(".file", base, head)
	if err != nil {
		t.Fatal(err)
	}
	r := fixedReport(false, compareCase{name: "A-key-start-64KiB", status: conclusive, cmp: cmp})
	r.statelex = true
	md := writeReport(t, r)
	wantLines(t, md,
		"| Method | 10 runs per side, base and head alternated in each round, one lane (control); `-test.benchtime 200ms`; spread limit 5 percent; rerun once with 20 |",
		"## Case A-key-start-64KiB\n\nStatus: **conclusive**.",
		"| Side | Median time/op | Spread (95% CI of the median, at least 5 %) | Change (removed / inserted tokens) | Copied bytes | Visited tokens | Re-lexed bytes | allocs/op | B/op |",
		"| base | 30 µs | ± 5 % (CI ± ",
		" | 1 / 1 | 2 | 56.5 | 3 | 82 | 5,256 |",
		"| time/op | -33.33 % | 0.000 | 10, 10 |",
		"| copied-bytes/op | not significant | 1.000 | 10, 10 |",
		"Result: the head is faster than the base",
	)
	if strings.Contains(md, "MB/s") {
		t.Errorf("the statelex path gives a rate:\n%s", md)
	}
}

// TestSelectCompare checks the cases of a comparison: reference cases or
// statelex cases, not both.
func TestSelectCompare(t *testing.T) {
	cs, es, err := selectCompare("")
	if err != nil || len(cs) != len(cases()) || es != nil {
		t.Errorf("selectCompare(\"\") = %d, %d, %v, want every reference case", len(cs), len(es), err)
	}
	cs, es, err = selectCompare("json,code")
	if err != nil || len(cs) != 2 || cs[1].name != "code" || es != nil {
		t.Errorf("selectCompare(json,code) = %v, %v, %v", cs, es, err)
	}
	cs, es, err = selectCompare("A-key-start-64KiB,B-load-4MiB")
	if err != nil || cs != nil || len(es) != 2 || es[1] != (editCase{"B", "load", "4MiB"}) {
		t.Errorf("selectCompare(statelex) = %v, %v, %v", cs, es, err)
	}
	if _, _, err := selectCompare("json,A-key-start-64KiB"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("selectCompare(mixed) = %v, want a refusal", err)
	}
	if _, _, err := selectCompare("nope"); err == nil || !strings.Contains(err.Error(), `no case "nope"`) {
		t.Errorf("selectCompare(nope) = %v, want a refusal", err)
	}
}

// TestCompareFlags checks the flags of a comparison.
func TestCompareFlags(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-kind", "compare", "-base", "master"}, "-kind compare needs -base and -head"},
		{[]string{"-kind", "compare", "-head", "master"}, "-kind compare needs -base and -head"},
		{[]string{"-base", "master", "-head", "master"}, "-base and -head need -kind compare"},
		{[]string{"-kind", "compare", "-base", "a", "-head", "b", "-cases", "json,A-load-4MiB"}, "not both"},
	} {
		if err := runMain(c.args); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("runMain(%q) = %v, want %q", c.args, err, c.want)
		}
	}
}

// TestCompareNoBenchstat checks that a comparison is refused when benchstat
// is not on the PATH, before any ref is resolved or built.
func TestCompareNoBenchstat(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := runMain([]string{"-kind", "compare", "-base", "no-such-ref", "-head", "no-such-ref"})
	if !errors.Is(err, errNoBenchstat) {
		t.Errorf("runMain without benchstat = %v, want %v", err, errNoBenchstat)
	}
}

// gitRepo creates a git repository in a temporary directory with one module
// whose package internal/bench does not build in the first commit and builds
// in the second, tagged good. It returns the directory.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed:", err)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=bench", "-c", "user.email=bench@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "--quiet")
	write("go.mod", "module example.com/compare\n\ngo 1.21\n")
	write("internal/bench/bench_test.go", "package bench\n\nfunc broken( {\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "broken")
	git("tag", "broken")
	write("internal/bench/bench_test.go", "package bench\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	git("commit", "--quiet", "-a", "-m", "good")
	git("tag", "good")
	return dir
}

// TestCompareRefusals checks the refusals of the refs, each with its own
// error: a ref that does not resolve, and a ref that does not build. A ref
// that builds is not refused, and two refs of the same tree are a control,
// not a refusal.
func TestCompareRefusals(t *testing.T) {
	repo := gitRepo(t)
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goCmd); err != nil {
		goCmd = "go"
	}

	_, err := resolveSide(repo, sideBase, "no-such-ref")
	if !errors.Is(err, errRefUnresolved) || errors.Is(err, errRefBuild) || !strings.Contains(err.Error(), `-base "no-such-ref": the ref does not resolve to a commit`) {
		t.Errorf("an unresolvable ref: %v", err)
	}
	if _, err := resolveSide(repo, sideHead, "--help"); !errors.Is(err, errRefUnresolved) {
		t.Errorf("a ref that is an option: %v", err)
	}

	work := t.TempDir()
	broken, err := resolveSide(repo, sideHead, "broken")
	if err != nil {
		t.Fatal(err)
	}
	err = buildSide(goCmd, repo, filepath.Join(work, "head"), "internal/bench", &broken)
	if !errors.Is(err, errRefBuild) || errors.Is(err, errRefUnresolved) || !strings.Contains(err.Error(), `-head "broken" (`+broken.commit[:12]+"): the ref does not build: go test -c ./internal/bench") {
		t.Errorf("an unbuildable ref: %v", err)
	}
	if broken.worktree == "" {
		t.Fatal("the worktree of the unbuildable ref is not recorded, so it cannot be removed")
	}
	removeWorktree(repo, &broken)
	if _, err := os.Stat(filepath.Join(work, "head")); !os.IsNotExist(err) {
		t.Errorf("the worktree of the unbuildable ref is still there: %v", err)
	}

	good, err := resolveSide(repo, sideBase, "good")
	if err != nil {
		t.Fatal(err)
	}
	same, err := resolveSide(repo, sideHead, good.commit)
	if err != nil {
		t.Fatal(err)
	}
	if same.tree != good.tree || same.tree == broken.tree {
		t.Errorf("trees: good %s, its commit %s, broken %s", good.tree, same.tree, broken.tree)
	}
	if r := (&compareReport{base: good, head: same}); !r.sameTree() {
		t.Error("two refs of one tree are not a same-tree control")
	}
	if r := (&compareReport{base: good, head: broken}); r.sameTree() {
		t.Error("two trees are a same-tree control")
	}
	if err := buildSide(goCmd, repo, filepath.Join(work, "base"), "internal/bench", &good); err != nil {
		t.Fatalf("a ref that builds: %v", err)
	}
	defer removeWorktree(repo, &good)
	if _, err := os.Stat(good.bin); err != nil {
		t.Errorf("the test binary: %v", err)
	}
	if out, err := gitOutput(good.worktree, "status", "--porcelain"); err != nil || out != "" {
		t.Errorf("the worktree is not clean: %q, %v", out, err)
	}
}
