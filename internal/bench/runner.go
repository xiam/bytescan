package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Main is the runner internal/bench/cmd/benchreport. It returns the exit
// status: 0, or 1 after it printed why it failed.
func Main() int {
	if err := runMain(os.Args[1:]); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil {
		fmt.Fprintln(os.Stderr, "benchreport:", err)
		return 1
	}
	return 0
}

type config struct {
	kind  string
	out   string
	runs  int
	cases []Case
	edits []editCase // the cases of -kind statelex, and of -kind compare on the statelex path
	// base and head are the refs of -kind compare.
	base, head string
}

func runMain(args []string) error {
	fs := flag.NewFlagSet("benchreport", flag.ContinueOnError)
	kind := fs.String("kind", "baseline", "the kind of the report: baseline, statelex, bottlenecks or compare")
	out := fs.String("out", filepath.Join("internal-docs", "benchmarks"), "the directory of the report, relative to the current directory")
	runs := fs.Int("runs", 10, "the runs per lane; a rerun has twice as many")
	only := fs.String("cases", "", "a comma-separated list of the cases to run (default all)")
	base := fs.String("base", "", "-kind compare: the ref of the base of the comparison")
	head := fs.String("head", "", "-kind compare: the ref of the head of the comparison")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments %q", fs.Args())
	}

	absOut, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	cfg := config{kind: *kind, out: absOut, runs: *runs, base: *base, head: *head}
	switch cfg.kind {
	case "baseline", "statelex", "bottlenecks":
		if cfg.base != "" || cfg.head != "" {
			return fmt.Errorf("-base and -head need -kind compare")
		}
	case "compare":
		if cfg.base == "" || cfg.head == "" {
			return fmt.Errorf("-kind compare needs -base and -head")
		}
	default:
		return fmt.Errorf("-kind %q: want baseline, statelex, bottlenecks or compare", cfg.kind)
	}
	if cfg.runs < 6 {
		return fmt.Errorf("-runs %d: benchstat gives no interval below 6 samples", cfg.runs)
	}
	switch cfg.kind {
	case "statelex":
		if cfg.edits, err = selectEdits(*only); err != nil {
			return err
		}
	case "compare":
		if cfg.cases, cfg.edits, err = selectCompare(*only); err != nil {
			return err
		}
	default:
		if cfg.cases, err = selectCases(*only); err != nil {
			return err
		}
	}

	if _, err := benchstatPath(); err != nil {
		return err
	}
	if err := checkMemoryLimit(); err != nil {
		return err
	}
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goCmd); err != nil {
		goCmd = "go"
	}
	switch cfg.kind {
	case "statelex":
		return reportEdits(cfg, goCmd)
	case "compare":
		return reportCompare(cfg, goCmd)
	}
	return report(cfg, goCmd)
}

// selectCases returns the reference cases that a -cases list names, or every
// case for an empty list.
func selectCases(only string) ([]Case, error) {
	if only == "" {
		return cases(), nil
	}
	var cs []Case
	for _, name := range strings.Split(only, ",") {
		c, ok := findCase(name)
		if !ok {
			return nil, fmt.Errorf("-cases: no case %q", name)
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// checkMemoryLimit returns an error when no cgroup of this process, up to the
// root, has a memory limit (cgroup v2 memory.max).
func checkMemoryLimit() error {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return fmt.Errorf("cannot read the cgroup of the process: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.HasPrefix(line, "0::") {
			continue
		}
		for dir := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::")); strings.HasPrefix(dir, "/sys/fs/cgroup/"); dir = filepath.Dir(dir) {
			if max, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil && strings.TrimSpace(string(max)) != "max" {
				return nil
			}
		}
	}
	return errors.New("the process has no memory limit; run it inside systemd-run --user --scope -p MemoryMax=3G -p MemorySwapMax=0 --quiet")
}

// binaries holds the two test binaries: the normal build and the build with
// the tag bench, and dir, the directory that they run in: the directory of
// package internal/bench.
type binaries struct {
	normal, tagged string
	dir            string
}

func (b binaries) of(l Lane) string {
	if l == Instrumented {
		return b.tagged
	}
	return b.normal
}

func report(cfg config, goCmd string) error {
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

	sha12 := r.commit[:12]
	date := start.Format("2006-01-02")
	reportPath := filepath.Join(cfg.out, cfg.kind+"-"+date+".md")
	rawDir := filepath.Join(cfg.out, "raw", date+"-"+sha12)
	r.rawDir = filepath.Join("raw", date+"-"+sha12) + "/"
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
	bins := binaries{normal: filepath.Join(tmp, "normal.test"), tagged: filepath.Join(tmp, "bench.test"), dir: filepath.Join("internal", "bench")}
	if err := build(goCmd, bins.normal); err != nil {
		return err
	}
	if err := build(goCmd, bins.tagged, "-tags", "bench"); err != nil {
		return err
	}

	for _, c := range cfg.cases {
		cr, err := runCase(c, cfg.runs, bins, rawDir, r)
		if err != nil {
			return err
		}
		r.cases = append(r.cases, cr)
	}
	r.loadEnd = loadAverage()

	var b bytes.Buffer
	if err := r.WriteMarkdown(&b); err != nil {
		return err
	}
	if err := os.WriteFile(reportPath, b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s and %s\n", reportPath, rawDir)
	return nil
}

func moduleRoot(goCmd string) (string, error) {
	out, err := exec.Command(goCmd, "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == os.DevNull {
		return "", errors.New("run it inside the bytescan module")
	}
	return filepath.Dir(mod), nil
}

// describe fills the commit, the machine, the Go version and the benchstat
// version of r.
func describe(r *Report, goCmd string) error {
	var err error
	if r.commit, err = output("git", "rev-parse", "HEAD"); err != nil {
		return err
	}
	if r.branch, err = output("git", "rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return err
	}
	status, err := output("git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	r.modified = status != ""
	if r.host, err = os.Hostname(); err != nil {
		return err
	}
	r.cpu = cpuModel()
	r.cpus = runtime.NumCPU()
	if k, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		r.kernel = strings.TrimSpace(string(k))
	}
	v, err := output(goCmd, "env", "GOVERSION", "GOOS", "GOARCH")
	if err != nil {
		return err
	}
	f := strings.Fields(v)
	if len(f) != 3 {
		return fmt.Errorf("go env: %q", v)
	}
	r.goVersion = f[0] + " " + f[1] + "/" + f[2]
	r.benchstat, err = benchstatVersion(goCmd)
	return err
}

func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return "unknown"
}

// loadAverage returns the 1-minute load average of /proc/loadavg.
func loadAverage() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "unknown"
	}
	if f := strings.Fields(string(data)); len(f) > 0 {
		return f[0]
	}
	return "unknown"
}

func build(goCmd, out string, tags ...string) error {
	args := append([]string{"test", "-c", "-o", out}, tags...)
	args = append(args, "./internal/bench")
	cmd := exec.Command(goCmd, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// runCase runs the rounds of one case, and once more with twice the runs
// when a lane is above the spread limit.
func runCase(c Case, runs int, bins binaries, rawDir string, r *Report) (caseReport, error) {
	in, err := c.input()
	if err != nil {
		return caseReport{}, err
	}
	cr := caseReport{c: c, bytes: len(in), rules: len(c.rules())}
	for rerun := false; ; rerun = true {
		n := runs
		if rerun {
			n = 2 * runs
			super := filepath.Join(rawDir, "superseded")
			if err := os.MkdirAll(super, 0o755); err != nil {
				return cr, err
			}
			for _, s := range slotsOf(c) {
				if err := os.Rename(s.file(rawDir, c), s.file(super, c)); err != nil {
					return cr, err
				}
			}
		}
		if err := rounds(c, n, bins, rawDir, &cr, r); err != nil {
			return cr, err
		}
		status, ok := decide(cr.lanes, cr.refs, rerun)
		if ok {
			cr.status = status
			return cr, nil
		}
		fmt.Fprintf(os.Stderr, "case %s: a lane or a reference is above the spread limit; running it again with %d runs\n", c.name, 2*runs)
	}
}

func rawFile(dir string, c Case, l Lane) string {
	return filepath.Join(dir, c.name+"-"+string(l)+".txt")
}

// refFile returns the raw file of the reference t of case c:
// <case>-ref-<tool>.txt.
func refFile(dir string, c Case, t tool) string {
	return filepath.Join(dir, c.name+"-ref-"+string(t)+".txt")
}

// slot is one run of a round: a lane of the case, or an outside reference
// when ref is set.
type slot struct {
	lane Lane
	ref  tool
}

// slotsOf returns the runs of one round of case c: the three lanes, then the
// outside references of a text case.
func slotsOf(c Case) []slot {
	var s []slot
	for _, l := range allLanes {
		s = append(s, slot{lane: l})
	}
	for _, t := range references(c) {
		s = append(s, slot{ref: t})
	}
	return s
}

func (s slot) String() string {
	if s.ref != "" {
		return "reference " + string(s.ref)
	}
	return "lane " + string(s.lane)
}

// file returns the raw file of the slot.
func (s slot) file(dir string, c Case) string {
	if s.ref != "" {
		return refFile(dir, c, s.ref)
	}
	return rawFile(dir, c, s.lane)
}

// rounds runs n rounds of case c, writes the raw output of each lane and
// each reference, and fills the lanes, the costs, the references and the
// counts of cr. The first run of a round rotates over the lanes and the
// references.
func rounds(c Case, n int, bins binaries, rawDir string, cr *caseReport, r *Report) error {
	slots := slotsOf(c)
	raw := map[slot]*bytes.Buffer{}
	results := map[Lane][]result{}
	refResults := map[tool][]refResult{}
	for _, s := range slots {
		raw[s] = &bytes.Buffer{}
	}
	for round := 0; round < n; round++ {
		for i := range slots {
			s := slots[(round+i)%len(slots)]
			fmt.Fprintf(os.Stderr, "case %s, round %d of %d, %s\n", c.name, round+1, n, s)
			out, err := runOnce(bins, c, s)
			if err != nil {
				return err
			}
			raw[s].Write(out)
			if s.ref != "" {
				res, procs, err := parseRefRun(out, c, s.ref)
				if err != nil {
					return err
				}
				r.gomaxprocs = procs
				refResults[s.ref] = append(refResults[s.ref], res)
				continue
			}
			res, procs, err := parseRun(out, c, s.lane)
			if err != nil {
				return err
			}
			r.gomaxprocs = procs
			if s.lane != Control {
				results[s.lane] = append(results[s.lane], res)
			}
		}
	}
	for _, s := range slots {
		if err := os.WriteFile(s.file(rawDir, c), raw[s].Bytes(), 0o644); err != nil {
			return err
		}
	}
	// The counts do not depend on timing: every run of a lane, and of a
	// reference, counts the same.
	for _, l := range []Lane{Wrapped, Instrumented} {
		for i, res := range results[l] {
			if res != results[l][0] {
				return fmt.Errorf("case %s, lane %s: run %d counted %+v, run 1 counted %+v", c.name, l, i+1, res, results[l][0])
			}
		}
	}
	cr.wrapped, cr.instrumented = results[Wrapped][0], results[Instrumented][0]
	if cr.wrapped.Counts != cr.instrumented.Counts {
		return fmt.Errorf("case %s: the wrapped lane counted %+v, the instrumented lane %+v", c.name, cr.wrapped.Counts, cr.instrumented.Counts)
	}
	for t, rs := range refResults {
		for i, res := range rs {
			if res != rs[0] {
				return fmt.Errorf("case %s, reference %s: run %d counted %+v, run 1 counted %+v", c.name, t, i+1, res, rs[0])
			}
		}
	}
	if rs := refResults[regexpLexer]; len(rs) > 0 && rs[0].Tokens != cr.wrapped.Tokens {
		return fmt.Errorf("case %s: the regexp lexer counted %d tokens, bytescan %d", c.name, rs[0].Tokens, cr.wrapped.Tokens)
	}

	wrapper, err := benchstat(rawFile(rawDir, c, Control), rawFile(rawDir, c, Wrapped))
	if err != nil {
		return err
	}
	counter, err := benchstat(rawFile(rawDir, c, Wrapped), rawFile(rawDir, c, Instrumented))
	if err != nil {
		return err
	}
	cr.lanes = map[Lane]map[string]stat{
		Control:      wrapper.first,
		Wrapped:      wrapper.second,
		Instrumented: counter.second,
	}
	cr.wrapperCost = wrapper.delta[unitTime]
	cr.counterCost = counter.delta[unitTime]
	cr.refs, cr.refResults = nil, nil
	for _, t := range references(c) {
		sum, err := summary(refFile(rawDir, c, t))
		if err != nil {
			return err
		}
		if cr.refs == nil {
			cr.refs, cr.refResults = map[tool]map[string]stat{}, map[tool]refResult{}
		}
		cr.refs[t], cr.refResults[t] = sum, refResults[t][0]
	}
	return nil
}

// runOnce runs one slot of case c once, in its own process: a lane, with
// BenchmarkCase in the binary of the lane, or a reference, with
// BenchmarkReference in the normal binary.
func runOnce(bins binaries, c Case, s slot) ([]byte, error) {
	bin := bins.normal
	bench := "^BenchmarkReference$/^" + regexp.QuoteMeta(c.name) + "$/^" + regexp.QuoteMeta(string(s.ref)) + "$"
	var lane []string
	if s.ref == "" {
		bin = bins.of(s.lane)
		bench = "^BenchmarkCase$/^" + regexp.QuoteMeta(c.name) + "$"
		lane = []string{"-lane", string(s.lane)}
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, append([]string{
		"-test.run", "^$",
		"-test.bench", bench,
		"-test.benchtime", "1x",
		"-test.count", "1",
		"-test.timeout", "30m"}, lane...)...)
	cmd.Dir = bins.dir
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("case %s, %s failed: %w\n%s%s", c.name, s, err, stdout.Bytes(), stderr.Bytes())
	}
	return stdout.Bytes(), nil
}

var benchLine = regexp.MustCompile(`^BenchmarkCase/([^\s-]+)(?:-(\d+))?\s+(\d+)\s+(\d+) ns/op`)

// parseRun reads the output of one run: one benchmark line of the case, with
// its GOMAXPROCS suffix, and in the wrapped and the instrumented lanes the
// line "result {...}" that BenchmarkCase logs.
func parseRun(out []byte, c Case, l Lane) (result, int, error) {
	var res result
	procs, lines, results := 0, 0, 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := benchLine.FindStringSubmatch(line); m != nil {
			if m[1] != c.name || m[3] != "1" {
				return res, 0, fmt.Errorf("case %s, lane %s: unexpected line %q", c.name, l, line)
			}
			procs = 1
			if m[2] != "" {
				procs, _ = strconv.Atoi(m[2])
			}
			lines++
		}
		if i := strings.Index(line, ": result {"); i >= 0 {
			if err := json.Unmarshal([]byte(line[i+len(": result "):]), &res); err != nil {
				return res, 0, fmt.Errorf("case %s, lane %s: %w", c.name, l, err)
			}
			results++
		}
	}
	wantResults := 1
	if l == Control {
		wantResults = 0
	}
	if lines != 1 || results != wantResults || !bytes.Contains(out, []byte("lane: "+string(l)+"\n")) {
		return res, 0, fmt.Errorf("case %s, lane %s: the output has %d benchmark lines and %d results, want 1 and %d:\n%s",
			c.name, l, lines, results, wantResults, out)
	}
	return res, procs, nil
}

var refLine = regexp.MustCompile(`^BenchmarkReference/([^/\s]+)/([^\s-]+)(?:-(\d+))?\s+(\d+)\s+(\d+) ns/op`)

// parseRefRun reads the output of one run of a reference: one benchmark
// line of the case and the tool, with its GOMAXPROCS suffix, and the line
// "reference {...}" that BenchmarkReference logs.
func parseRefRun(out []byte, c Case, t tool) (refResult, int, error) {
	var res refResult
	procs, lines, results := 0, 0, 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := refLine.FindStringSubmatch(line); m != nil {
			if m[1] != c.name || m[2] != string(t) || m[4] != "1" {
				return res, 0, fmt.Errorf("case %s, reference %s: unexpected line %q", c.name, t, line)
			}
			procs = 1
			if m[3] != "" {
				procs, _ = strconv.Atoi(m[3])
			}
			lines++
		}
		if i := strings.Index(line, ": reference {"); i >= 0 {
			if err := json.Unmarshal([]byte(line[i+len(": reference "):]), &res); err != nil {
				return res, 0, fmt.Errorf("case %s, reference %s: %w", c.name, t, err)
			}
			results++
		}
	}
	if lines != 1 || results != 1 {
		return res, 0, fmt.Errorf("case %s, reference %s: the output has %d benchmark lines and %d results, want 1 and 1:\n%s",
			c.name, t, lines, results, out)
	}
	return res, procs, nil
}
