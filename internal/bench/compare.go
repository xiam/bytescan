package bench

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// This file is the comparison of two commits (-kind compare -base <ref> -head
// <ref>): the test binary of each ref, built once in a clean worktree of that
// ref, runs the cases in the same rounds, and benchstat compares the head
// against the base. The flags and the name of the report are the owner
// decision of 2026-11-20 ("As drafted").

// Errors of a comparison. Each refusal has its own error.
var (
	// errRefUnresolved means that a ref does not resolve to a commit.
	errRefUnresolved = errors.New("the ref does not resolve to a commit")
	// errRefBuild means that the test binary of a ref does not build.
	errRefBuild = errors.New("the ref does not build")
)

// The names of the two sides of a comparison, as in the flags.
const (
	sideBase = "base"
	sideHead = "head"
)

// side is one side of a comparison: a ref, its commit and its tree, and the
// test binary that was built from it.
type side struct {
	// name is sideBase or sideHead, and ref the ref of its flag.
	name, ref string
	// commit and tree are the full hashes of the commit of ref and its tree.
	commit, tree string
	// worktree is the clean worktree of the commit, and bin the test binary
	// built in it.
	worktree, bin string
}

// selectCompare returns the cases of a comparison that a -cases list names:
// reference cases, such as json,code, or the cases of the statelex path,
// such as A-key-start-64KiB. One comparison runs the cases of one path. An
// empty list is every reference case.
func selectCompare(only string) ([]Case, []editCase, error) {
	if only == "" {
		return cases(), nil, nil
	}
	var cs []Case
	var es []editCase
	for _, name := range strings.Split(only, ",") {
		if c, ok := findCase(name); ok {
			cs = append(cs, c)
			continue
		}
		e, err := selectEdits(name)
		if err != nil {
			return nil, nil, fmt.Errorf("-cases: no case %q; a case is a reference case, such as json, or a statelex case, such as A-key-start-64KiB", name)
		}
		es = append(es, e...)
	}
	if len(cs) > 0 && len(es) > 0 {
		return nil, nil, fmt.Errorf("-cases %q: a comparison runs reference cases or statelex cases, not both", only)
	}
	return cs, es, nil
}

// resolveSide resolves the ref of one side in the git repository repo.
func resolveSide(repo, name, ref string) (side, error) {
	s := side{name: name, ref: ref}
	commit, err := gitOutput(repo, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil || commit == "" {
		return s, fmt.Errorf("-%s %q: %w", name, ref, errRefUnresolved)
	}
	tree, err := gitOutput(repo, "rev-parse", "--verify", "--quiet", commit+"^{tree}")
	if err != nil {
		return s, fmt.Errorf("-%s %q: the tree of %s: %w", name, ref, commit, err)
	}
	s.commit, s.tree = commit, tree
	return s, nil
}

// buildSide creates a clean worktree of the commit of s in dir and builds the
// test binary of package pkg in it, the normal build. It sets s.worktree once
// the worktree exists, so that removeWorktree can remove it after an error.
func buildSide(goCmd, repo, dir, pkg string, s *side) error {
	if _, err := gitOutput(repo, "worktree", "add", "--detach", "--quiet", dir, s.commit); err != nil {
		return fmt.Errorf("-%s %q: a worktree of %s: %w", s.name, s.ref, s.commit, err)
	}
	s.worktree = dir
	bin := dir + ".test"
	var out bytes.Buffer
	cmd := exec.Command(goCmd, "test", "-c", "-o", bin, "./"+pkg)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("-%s %q (%s): %w: go test -c ./%s: %v\n%s", s.name, s.ref, s.commit[:12], errRefBuild, pkg, err, out.Bytes())
	}
	s.bin = bin
	return nil
}

// removeWorktree removes the worktree of s, if any.
func removeWorktree(repo string, s *side) {
	if s.worktree == "" {
		return
	}
	if _, err := gitOutput(repo, "worktree", "remove", "--force", s.worktree); err != nil {
		fmt.Fprintf(os.Stderr, "benchreport: removing the worktree of -%s: %v\n", s.name, err)
	}
	s.worktree = ""
}

func gitOutput(dir string, args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// compareReport is the report of a comparison. Report holds the fields of
// the header that every report has; its commit and branch are not used.
type compareReport struct {
	Report
	base, head side
	// statelex is set for the statelex path.
	statelex bool
	cases    []compareCase
}

// sameTree reports whether the two sides have the same tree: the run is a
// same-tree control.
func (r *compareReport) sameTree() bool {
	return r.base.tree == r.head.tree
}

// compareCase is the result of one case of a comparison.
type compareCase struct {
	name string
	// bytes is the size of the input of a reference case, and 0 on the
	// statelex path.
	bytes int
	// status is the status of the case after the rerun rule.
	status caseStatus
	// cmp is benchstat's comparison: first is the base, second the head.
	cmp comparison
}

// runSide runs one case once on one side and returns the raw output of the
// run, which starts with the line "lane: control", and its GOMAXPROCS.
type runSide func(s *side) ([]byte, int, error)

// reportCompare writes the report of a comparison.
func reportCompare(cfg config, goCmd string) error {
	root, err := moduleRoot(goCmd)
	if err != nil {
		return err
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	r := &compareReport{statelex: len(cfg.edits) > 0}
	r.kind, r.runs, r.rerunRuns = cfg.kind, cfg.runs, 2*cfg.runs
	r.date = time.Now().UTC()
	r.command = strings.TrimSpace("go run ./internal/bench/cmd/benchreport " + strings.Join(os.Args[1:], " "))
	if r.base, err = resolveSide(root, sideBase, cfg.base); err != nil {
		return err
	}
	if r.head, err = resolveSide(root, sideHead, cfg.head); err != nil {
		return err
	}
	if err := describe(&r.Report, goCmd); err != nil {
		return err
	}

	stem := r.date.Format("2006-01-02") + "-" + r.base.commit[:12] + "-" + r.head.commit[:12]
	dir := filepath.Join(cfg.out, "compare")
	reportPath := filepath.Join(dir, stem+".md")
	rawDir := filepath.Join(dir, stem)
	r.rawDir = stem + "/"
	for _, p := range []string{reportPath, rawDir} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s exists; move it away first", p)
		}
	}

	tmp, err := os.MkdirTemp("", "benchreport-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	pkg := filepath.Join("internal", "bench")
	if r.statelex {
		pkg = "statelex"
	}
	sides := []*side{&r.base, &r.head}
	defer func() {
		for _, s := range sides {
			removeWorktree(root, s)
		}
	}()
	for _, s := range sides {
		fmt.Fprintf(os.Stderr, "building -%s %s (%s)\n", s.name, s.ref, s.commit[:12])
		if err := buildSide(goCmd, root, filepath.Join(tmp, s.name), filepath.ToSlash(pkg), s); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}

	r.loadStart = loadAverage()
	for _, c := range cfg.cases {
		in, err := c.input()
		if err != nil {
			return err
		}
		run := func(s *side) ([]byte, int, error) {
			out, err := runOnce(binaries{normal: s.bin, dir: filepath.Join(s.worktree, "internal", "bench")}, c, slot{lane: Control})
			if err != nil {
				return nil, 0, fmt.Errorf("-%s: %w", s.name, err)
			}
			_, procs, err := parseRun(out, c, Control)
			return out, procs, err
		}
		cc, err := compareRuns(c.name, cfg.runs, sides, run, rawDir, &r.Report)
		if err != nil {
			return err
		}
		cc.bytes = len(in)
		r.cases = append(r.cases, cc)
	}
	for _, e := range cfg.edits {
		run := func(s *side) ([]byte, int, error) {
			out, procs, err := runEdit(s.bin, filepath.Join(s.worktree, "statelex"), e)
			if err != nil {
				return nil, 0, fmt.Errorf("-%s: %w", s.name, err)
			}
			return append([]byte("lane: "+string(Control)+"\n"), out...), procs, nil
		}
		cc, err := compareRuns(e.name(), cfg.runs, sides, run, rawDir, &r.Report)
		if err != nil {
			return err
		}
		r.cases = append(r.cases, cc)
	}
	r.loadEnd = loadAverage()

	var b bytes.Buffer
	if err := writeCompare(&b, r); err != nil {
		return err
	}
	if err := os.WriteFile(reportPath, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s and %s\n", reportPath, rawDir)
	return nil
}

// compareFile returns the raw file of one side of a case:
// <case>-<side>.txt.
func compareFile(dir, name, sideName string) string {
	return filepath.Join(dir, name+"-"+sideName+".txt")
}

// compareRuns runs the rounds of one case on the two sides, and once more
// with twice the runs when a side is above the spread limit.
func compareRuns(name string, runs int, sides []*side, run runSide, rawDir string, r *Report) (compareCase, error) {
	cc := compareCase{name: name}
	for rerun := false; ; rerun = true {
		n := runs
		if rerun {
			n = 2 * runs
			super := filepath.Join(rawDir, "superseded")
			if err := os.MkdirAll(super, 0o755); err != nil {
				return cc, err
			}
			for _, s := range sides {
				if err := os.Rename(compareFile(rawDir, name, s.name), compareFile(super, name, s.name)); err != nil {
					return cc, err
				}
			}
		}
		cmp, err := compareRounds(name, n, sides, run, rawDir, r)
		if err != nil {
			return cc, err
		}
		cc.cmp = cmp
		status, ok := rerunRule(aboveLimit(cmp.first[unitTime]) || aboveLimit(cmp.second[unitTime]), rerun)
		if ok {
			cc.status = status
			return cc, nil
		}
		fmt.Fprintf(os.Stderr, "case %s: a side is above the spread limit; running it again with %d runs\n", name, 2*runs)
	}
}

// compareRounds runs n rounds of one case. A round runs the case once on
// each side, and the first side alternates: base then head in the first
// round, head then base in the second, and so on. It writes the raw file of
// each side and returns benchstat's comparison of the head against the base.
func compareRounds(name string, n int, sides []*side, run runSide, rawDir string, r *Report) (comparison, error) {
	raw := make([]bytes.Buffer, len(sides))
	for round := 0; round < n; round++ {
		for i := range sides {
			k := (round + i) % len(sides)
			s := sides[k]
			fmt.Fprintf(os.Stderr, "case %s, round %d of %d, %s\n", name, round+1, n, s.name)
			out, procs, err := run(s)
			if err != nil {
				return comparison{}, err
			}
			r.gomaxprocs = procs
			raw[k].Write(out)
		}
	}
	for i, s := range sides {
		if err := os.WriteFile(compareFile(rawDir, name, s.name), raw[i].Bytes(), 0o644); err != nil {
			return comparison{}, err
		}
	}
	return benchstatBy(".file", compareFile(rawDir, name, sides[0].name), compareFile(rawDir, name, sides[1].name))
}

// writeCompare writes the report of a comparison.
func writeCompare(w io.Writer, r *compareReport) error {
	p := &printer{w: w}
	p.f("# %s, %s", title(r.kind), r.date.Format("2006-01-02"))
	if r.sameTree() {
		p.f(": same-tree control")
	}
	p.f("\n\n")
	if r.sameTree() {
		p.f("**Same-tree control.** The base and the head have the same tree, `%s`, so both sides run the same code. ", r.base.tree[:min(12, len(r.base.tree))])
		p.f("This run measures the bias of the procedure, not a gain. ")
		p.f("With the same code on both sides, `benchstat` should report not significant. ")
		p.f("A significant difference is a finding about the runner or the harness, not a gain.\n\n")
	}
	p.f("| Field | Value |\n| --- | --- |\n")
	for _, s := range []side{r.base, r.head} {
		p.f("| %s | `%s` at `%s` |\n", strings.ToUpper(s.name[:1])+s.name[1:], s.ref, s.commit[:min(12, len(s.commit))])
	}
	p.f("| Machine | shared runner `%s`, `%s`, %d logical CPUs, Linux `%s` |\n", r.host, r.cpu, r.cpus, r.kernel)
	p.f("| Load | %s at the start, %s at the end |\n", r.loadStart, r.loadEnd)
	p.f("| Go | `%s`, GOMAXPROCS %d |\n", r.goVersion, r.gomaxprocs)
	p.f("| benchstat | `%s` |\n", r.benchstat)
	if r.statelex {
		p.f("| Method | %d runs per side, base and head alternated in each round, one lane (control); `-test.benchtime %s`; spread limit %s percent; rerun once with %d |\n",
			r.runs, editBenchtime, trimFloat(spreadLimit), r.rerunRuns)
	} else {
		p.f("| Method | %d runs per side, base and head alternated in each round, one lane (control); spread limit %s percent; rerun once with %d |\n",
			r.runs, trimFloat(spreadLimit), r.rerunRuns)
	}
	p.f("| Command | `%s` |\n", r.command)
	p.f("| Raw samples | `%s` |\n\n", r.rawDir)

	p.f("Each side is the test binary of its commit, the normal build, built once in a clean worktree of the commit. ")
	if r.statelex {
		p.f("Each run is one process that runs one benchmark `BenchmarkEdit/<set>/<shape>/<size>` of `statelex/latency_bench_test.go` with `-test.benchtime %s`; one operation is one edit. ", editBenchtime)
	} else {
		p.f("Each run is one process that lexes one case once in the control lane (`BenchmarkCase` with `-test.benchtime 1x`). ")
	}
	p.f("A round runs the case once on each side, and the first side alternates: ")
	p.f("base then head in the first round, head then base in the second, and so on. So both sides see the same load. ")
	p.f("The estimator is the median of the samples, as `benchstat` reports it. ")
	p.f("The spread is the larger of `benchstat`'s 95 percent confidence interval of the median (CI), as a percentage of the median, and %s percent. ", trimFloat(spreadFloor))
	p.f("When the CI of a side is above %s percent, the case runs again with twice the runs; if a CI is still above it, the case is inconclusive. ", trimFloat(spreadLimit))
	p.f("The change is `benchstat`'s comparison of the head against the base, with its p-value; a negative change of the time is a faster head. ")
	p.f("A change that `benchstat` reports as not significant is \"not significant\": never zero and never a gain. ")
	p.f("A significant change of the time under %s percent is under the resolution floor of the method.\n", trimFloat(spreadFloor))

	for i := range r.cases {
		r.writeCase(p, &r.cases[i])
	}
	return p.err
}

// compareUnits returns the units of the change table of a case, in order.
func (r *compareReport) compareUnits() []string {
	if r.statelex {
		return []string{unitTime, unitRemoved, unitInserted, unitCopied, unitVisited, unitRelexed, unitAllocs, unitBytes}
	}
	return []string{unitTime, unitAllocs, unitBytes}
}

func (r *compareReport) writeCase(p *printer, c *compareCase) {
	inconclusive := c.status == inconclusive
	p.f("\n## Case %s\n\n", c.name)
	if c.bytes > 0 {
		p.f("Input %s bytes. ", group(uint64(c.bytes)))
	}
	p.f("Status: **%s**.\n\n", c.status)

	sides := []struct {
		name string
		u    map[string]stat
	}{{sideBase, c.cmp.first}, {sideHead, c.cmp.second}}
	if r.statelex {
		p.f("| Side | Median time/op | Spread (95%% CI of the median, at least 5 %%) | Change (removed / inserted tokens) | Copied bytes | Visited tokens | Re-lexed bytes | allocs/op | B/op |\n")
		p.f("| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
		for _, s := range sides {
			u := s.u
			p.f("| %s | %s | %s | %s / %s | %s | %s | %s | %s | %s |\n", s.name, formatTime(u[unitTime].Median), formatSpread(u[unitTime]),
				metric(u[unitRemoved]), metric(u[unitInserted]), metric(u[unitCopied]), metric(u[unitVisited]), metric(u[unitRelexed]),
				metric(u[unitAllocs]), metric(u[unitBytes]))
		}
	} else {
		p.f("| Side | Median time/op | Spread (95%% CI of the median, at least 5 %%) | Rate from the median | allocs/op | B/op |\n")
		p.f("| --- | --- | --- | --- | --- | --- |\n")
		for _, s := range sides {
			u := s.u
			rate := "inconclusive"
			if !inconclusive {
				rate = fmt.Sprintf("%.2f MB/s", rateOf(c.bytes, u[unitTime]))
			}
			p.f("| %s | %s | %s | %s | %s | %s |\n", s.name, formatTime(u[unitTime].Median), formatSpread(u[unitTime]), rate,
				group(uint64(u[unitAllocs].Median+0.5)), group(uint64(u[unitBytes].Median+0.5)))
		}
	}

	p.f("\n| Change (head against base) | Delta of the medians | p-value | Samples |\n| --- | --- | --- | --- |\n")
	for _, u := range r.compareUnits() {
		d, ok := c.cmp.delta[u]
		if !ok {
			p.err = fmt.Errorf("bench: case %s: benchstat gave no comparison of %s", c.name, u)
			return
		}
		label := u
		if u == unitTime {
			label = "time/op"
		}
		p.f("| %s | %s | %.3f | %d, %d |\n", label, formatDelta(d), d.P, d.N[0], d.N[1])
	}
	p.f("\n%s\n", r.result(c))
}

// result returns the sentence that states the result of the time of a case.
func (r *compareReport) result(c *compareCase) string {
	d := c.cmp.delta[unitTime]
	switch {
	case c.status == inconclusive:
		return "Result: inconclusive. A side is above the spread limit after the rerun, so the change of the time is not a result."
	case !d.Significant:
		return fmt.Sprintf("Result: not significant (p=%.3f).", d.P)
	case r.sameTree():
		return fmt.Sprintf("Result: a significant difference of %s (p=%.3f) between two sides of the same tree. "+
			"This is a measurement-bias finding about the runner or the harness, not a gain.", formatDelta(d), d.P)
	}
	dir := "faster"
	if d.Percent > 0 {
		dir = "slower"
	}
	floor := "at or above"
	if math.Abs(d.Percent) < spreadFloor {
		floor = "under"
	}
	return fmt.Sprintf("Result: the head is %s than the base, %s (p=%.3f), significant, %s the %s percent floor.",
		dir, formatDelta(d), d.P, floor, trimFloat(spreadFloor))
}
