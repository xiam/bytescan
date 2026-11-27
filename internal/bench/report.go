package bench

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/xiam/bytescan/internal/counters"
	"github.com/xiam/bytescan/internal/meter"
)

// spreadLimit is the spread limit of the method, in percent of the median.
const spreadLimit = 5.0

// caseStatus is the status of a case in a report.
type caseStatus string

// The statuses of a case.
const (
	conclusive           caseStatus = "conclusive"
	conclusiveAfterRerun caseStatus = "conclusive after a rerun"
	inconclusive         caseStatus = "inconclusive"
)

// aboveLimit reports whether the spread of s is above the spread limit, or
// benchstat gave no interval for it. A lane above the limit makes its case
// rerun once, and then inconclusive.
func aboveLimit(s stat) bool {
	return !s.Bounded || s.Spread > spreadLimit
}

// Report is a report in the format of docs/specs/measurement.md, "The report
// format".
type Report struct {
	// kind is the kind of the report: baseline, statelex or bottlenecks.
	kind string
	// date is the date of the start of the runs, in UTC.
	date time.Time
	// commit is the full hash of the commit, branch its branch, and modified
	// reports a working tree with changes to tracked files.
	commit, branch string
	modified       bool
	// host, cpu, cpus and kernel describe the machine.
	host, cpu string
	cpus      int
	kernel    string
	// loadStart and loadEnd are the 1-minute load averages at the start and
	// at the end.
	loadStart, loadEnd string
	// goVersion is the Go version and platform, gomaxprocs that of the runs.
	goVersion  string
	gomaxprocs int
	// benchstat is the module version of benchstat.
	benchstat string
	// runs is the number of runs per lane, and rerunRuns that of a rerun.
	runs, rerunRuns int
	// command is the command that wrote the report, and rawDir the directory
	// of the raw samples, relative to the report.
	command, rawDir string
	// cases holds the results of the cases.
	cases []caseReport
}

// caseReport is the result of one case.
type caseReport struct {
	c Case
	// bytes is the size of the input.
	bytes int
	// status is the status of the case after the rerun rule.
	status caseStatus
	// lanes holds the summary of each lane, by unit.
	lanes map[Lane]map[string]stat
	// wrapperCost is wrapped against control, counterCost instrumented
	// against wrapped, both in time per operation.
	wrapperCost, counterCost delta
	// wrapped is the result of a run in the wrapped lane, and instrumented of
	// a run in the instrumented lane. Every run of a lane gives the same.
	wrapped, instrumented result
	// rules is the number of rules of the rule set.
	rules int
	// refs holds the summary of each outside reference, by unit, and
	// refResults what one run of each counted. Both are nil for a case that
	// is not a text case.
	refs       map[tool]map[string]stat
	refResults map[tool]refResult
}

// decide returns the status of a case from the summaries of its lanes and
// of its outside references: conclusive when none is above the spread limit;
// otherwise, after the rerun, inconclusive, and before it, ok false, which
// asks for the rerun. The references follow the rule of the lanes (owner
// decision, 2026-11-20, "Approve as proposed").
func decide(lanes map[Lane]map[string]stat, refs map[tool]map[string]stat, rerun bool) (status caseStatus, ok bool) {
	above := false
	for _, l := range allLanes {
		if aboveLimit(lanes[l][unitTime]) {
			above = true
		}
	}
	for _, t := range allTools {
		if s, ok := refs[t]; ok && aboveLimit(s[unitTime]) {
			above = true
		}
	}
	return rerunRule(above, rerun)
}

// rerunRule is the rerun rule of the method. above reports whether a run of
// the case is above the spread limit, and rerun whether the samples are those
// of the rerun. It returns conclusive when none is above the limit;
// otherwise, after the rerun, inconclusive, and before it, ok false, which
// asks for the rerun.
func rerunRule(above, rerun bool) (status caseStatus, ok bool) {
	switch {
	case !above && !rerun:
		return conclusive, true
	case !above:
		return conclusiveAfterRerun, true
	case rerun:
		return inconclusive, true
	}
	return "", false
}

// rateOf returns the rate of a case in MB/s (10^6 bytes per second): the bytes
// of its input divided by the median time per operation of a lane.
func rateOf(bytes int, time stat) float64 {
	return float64(bytes) / time.Median / 1e6
}

// spreadFloor is the resolution floor of the reported spread, in percent of
// the median (owner decision, 2026-11-20, "5% resolution floor").
const spreadFloor = 5.0

// reportedSpread returns the reported spread of s: the larger of benchstat's
// 95 percent interval and the floor. The report compares medians, and reports,
// with it. The rerun rule (aboveLimit) uses the interval itself.
func reportedSpread(s stat) float64 {
	return max(s.Spread, spreadFloor)
}

// formatSpread returns the reported spread of s for the report: "± 7 %" when
// the interval is above the floor, "± 5 % (CI ± 2 %)" when the floor applies,
// or "no interval (∞)" when benchstat gave none.
func formatSpread(s stat) string {
	if !s.Bounded {
		return "no interval (∞)"
	}
	if s.Spread < spreadFloor {
		return fmt.Sprintf("± %s %% (CI ± %s %%)", trimFloat(reportedSpread(s)), trimFloat(s.Spread))
	}
	return fmt.Sprintf("± %s %%", trimFloat(s.Spread))
}

// formatDelta returns a cost for the report: the change of the median, or
// "not significant". It never returns a zero for a change that benchstat
// reports as not significant.
func formatDelta(d delta) string {
	if !d.Significant {
		return "not significant"
	}
	return fmt.Sprintf("%+.2f %%", d.Percent)
}

func trimFloat(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

// formatTime returns a time per operation in seconds as benchstat's text
// output would: in s, ms, µs or ns, with four significant digits.
func formatTime(sec float64) string {
	switch {
	case sec >= 1:
		return fmt.Sprintf("%.4g s", sec)
	case sec >= 1e-3:
		return fmt.Sprintf("%.4g ms", sec*1e3)
	case sec >= 1e-6:
		return fmt.Sprintf("%.4g µs", sec*1e6)
	}
	return fmt.Sprintf("%.4g ns", sec*1e9)
}

func title(kind string) string {
	if kind == "" {
		return "Report"
	}
	return strings.ToUpper(kind[:1]) + kind[1:] + " report"
}

// WriteMarkdown writes the report in the format of the contract.
func (r *Report) WriteMarkdown(w io.Writer) error {
	p := &printer{w: w}
	commit := r.commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	modified := ""
	if r.modified {
		modified = ", with changes to tracked files"
	}
	p.f("# %s, %s\n\n", title(r.kind), r.date.Format("2006-01-02"))
	p.f("| Field | Value |\n| --- | --- |\n")
	p.f("| Commit | `%s` (`%s`%s) |\n", commit, r.branch, modified)
	p.f("| Machine | shared runner `%s`, `%s`, %d logical CPUs, Linux `%s` |\n", r.host, r.cpu, r.cpus, r.kernel)
	p.f("| Load | %s at the start, %s at the end |\n", r.loadStart, r.loadEnd)
	p.f("| Go | `%s`, GOMAXPROCS %d |\n", r.goVersion, r.gomaxprocs)
	p.f("| benchstat | `%s` |\n", r.benchstat)
	p.f("| Method | %d runs per lane, lanes interleaved; spread limit %s percent; rerun once with %d |\n",
		r.runs, trimFloat(spreadLimit), r.rerunRuns)
	p.f("| Command | `%s` |\n", r.command)
	p.f("| Raw samples | `%s` |\n\n", r.rawDir)

	p.f("The estimator is the median of the samples, as `benchstat` reports it. ")
	p.f("The spread is the larger of `benchstat`'s 95 percent confidence interval of the median (CI), as a percentage of the median, and %s percent: ", trimFloat(spreadFloor))
	p.f("no difference under %s percent is claimed between reports. The rerun rule uses the CI. ", trimFloat(spreadFloor))
	p.f("The report gives no arithmetic mean. ")
	p.f("Each run is one process that lexes one case once in one lane. ")
	p.f("The first round runs control, wrapped and instrumented, followed by the case's outside references. ")
	p.f("Text cases run textscanner and regexp. The json case adds jsondecoder after regexp. ")
	p.f("Each subsequent round starts one position later in this order and wraps around. ")
	p.f("The spread limit and the rerun apply to the references as to the lanes. ")
	p.f("Two reports of the same commit on the same machine should have control medians that differ by less than their reported spread, ")
	p.f("or the case is marked inconclusive. This is a check of the procedure, not a statistical guarantee.\n")

	for i := range r.cases {
		r.cases[i].write(p)
	}
	return p.err
}

func (c *caseReport) write(p *printer) {
	inconclusive := c.status == inconclusive
	tokens := c.wrapped.Counts.Tokens
	p.f("\n## Case %s\n\n", c.c.name)
	p.f("Rule set %s; input %s, %s bytes, %s tokens\n", c.c.ruleSet, c.c.source, group(uint64(c.bytes)), group(tokens))
	p.f("(%s bytes per token). Status: **%s**.\n\n", ratio(c.wrapped.Counts.TokenBytes, tokens), c.status)

	p.f("| Lane | Median time/op | Spread (95%% CI of the median, at least 5 %%) | Rate from the median | allocs/op | B/op |\n")
	p.f("| --- | --- | --- | --- | --- | --- |\n")
	for _, l := range allLanes {
		s := c.lanes[l]
		rate := "inconclusive"
		if !inconclusive {
			rate = fmt.Sprintf("%.2f MB/s", rateOf(c.bytes, s[unitTime]))
			if l == Control {
				rate = "**" + rate + "**"
			}
		}
		p.f("| %s | %s | %s | %s | %s | %s |\n", l, formatTime(s[unitTime].Median), formatSpread(s[unitTime]), rate,
			group(uint64(s[unitAllocs].Median+0.5)), group(uint64(s[unitBytes].Median+0.5)))
	}
	ctl := c.lanes[Control][unitTime]
	if inconclusive {
		p.f("\nHeadline: inconclusive, control lane %s.\n\n", formatSpread(ctl))
	} else {
		p.f("\nHeadline: %.2f MB/s, %s, control lane.\n\n", rateOf(c.bytes, ctl), formatSpread(ctl))
	}

	p.f("| Cost | Delta of the medians | p-value | Samples |\n| --- | --- | --- | --- |\n")
	p.f("| wrapper (wrapped against control) | %s | %.3f | %d, %d |\n", formatDelta(c.wrapperCost), c.wrapperCost.P, c.wrapperCost.N[0], c.wrapperCost.N[1])
	p.f("| counters (instrumented against wrapped) | %s | %.3f | %d, %d |\n\n", formatDelta(c.counterCost), c.counterCost.P, c.counterCost.N[0], c.counterCost.N[1])

	wc := c.wrapped.Counts
	if inconclusive {
		p.f("Wrapped lane, over the harness interval: intake and token rate inconclusive, ")
	} else {
		elapsed := time.Duration(c.lanes[Wrapped][unitTime].Median * 1e9)
		t, err := meter.Rates(wc, elapsed)
		if err != nil {
			p.err = fmt.Errorf("bench: case %s: %w", c.c.name, err)
			return
		}
		p.f("Wrapped lane, over the harness interval: intake %.2f MB/s, %s tokens per second, ",
			t.BytesPerSecond/1e6, group(uint64(t.TokensPerSecond+0.5)))
	}
	p.f("%s `Read` calls of %s bytes on average.\n\n", group(wc.Reads), ratio(wc.Bytes, wc.Reads))

	sc, cu := c.instrumented.Scanner, c.instrumented.Cursor
	a := sc.Abandons
	p.f("Counters, instrumented lane (the same in every run):\n\n")
	p.f("| Counter | Value | Derived |\n| --- | --- | --- |\n")
	p.f("| Symbols | %s | — |\n", group(sc.Symbols))
	p.f("| RuleCalls | %s | %s rule calls per symbol |\n", group(sc.RuleCalls), ratio(sc.RuleCalls, sc.Symbols))
	p.f("| Pushbacks | %s | %s active rules per step |\n", group(sc.Pushbacks), ratio(sc.RuleCalls-sc.Pushbacks, sc.Symbols))
	p.f("| MaxLive | %s | — |\n", group(sc.MaxLive))
	p.f("| Tokens / LongTokens | %s / %s | %s symbols per token |\n", group(sc.Tokens), group(sc.LongTokens), ratio(sc.Symbols, sc.Tokens))
	p.f("| Unknown | %s | — |\n", group(sc.Unknown))
	p.f("| Abandons (end of input / ErrMoreInput / error) | %s / %s / %s | — |\n", group(a.EndOfInput), group(a.MoreInput), group(a.Error))
	p.f("| StepBacks / StepBackBytes | %s / %s | %s re-read share |\n", group(sc.StepBacks), group(sc.StepBackBytes), ratio(sc.StepBackBytes, uint64(c.bytes)))
	p.f("| Fills / FillBytes | %s / %s | %s bytes per fill |\n", group(cu.Fills), group(cu.FillBytes), ratio(cu.FillBytes, cu.Fills))
	p.f("| Compactions / CompactBytes | %s / %s | — |\n", group(cu.Compactions), group(cu.CompactBytes))
	p.f("| Grows / MaxBuffer | %s / %s | — |\n", group(cu.Grows), group(cu.MaxBuffer))
	p.f("| Replays / ReplayBytes | %s / %s | — |\n", group(cu.Replays), group(cu.ReplayBytes))
	p.f("| Locks | %s | %s locks per symbol |\n", group(cu.Locks), ratio(cu.Locks, sc.Symbols))
	p.f("| FloorScans | %s | — |\n", group(cu.FloorScans))
	p.f("| Map lookups by token type | (derived) | %s |\n", group(mapLookups))
	p.f("| Reset walks | (derived) | %s, each over %d rules |\n", group(resetWalks(sc)), c.rules)
	c.writeReferences(p)
}

// referenceNotes is the fixed paragraph of the outside references: what
// each tool does and does not do, so that the reader does not take their
// numbers as the same work as bytescan's.
const referenceNotes = "The outside references do not do the same work as bytescan. " +
	"bytescan reads the input through `cursor.Reader` from an `io.Reader`, " +
	"returns every token as a `*bytescan.Token` with its text and its position (byte, rune, line and column), " +
	"and accepts input that arrives in parts (`bytescan.ErrMoreInput`). " +
	"**textscanner** is `text/scanner` of the standard library with its default mode. " +
	"It reads the input from an `io.Reader` and keeps the line and the column. " +
	"Its token classes are fixed: the tokens of Go. " +
	"It skips white space and comments and returns no token for them. " +
	"The run does not ask for the text of a token. " +
	"It reports a byte sequence that is not a Go token as an error, counts it, and goes on, " +
	"so its tokens are not bytescan's tokens. " +
	"**regexp** is one regular expression of the standard library `regexp`: " +
	"an anchored alternation of the token classes of the case, with leftmost-longest matching (`Regexp.Longest`), " +
	"matched again at the end of each token. " +
	"Its tokens are bytescan's tokens: the same types at the same offsets (test `TestReferencesLex`). " +
	"It needs the whole input in one byte slice, gives the class and the end of each token, " +
	"and does not copy the text or count lines.\n"

// writeReferences writes the outside references of a text case.
func (c *caseReport) writeReferences(p *printer) {
	if c.refs == nil {
		return
	}
	p.f("\nOutside references, on the same input in the same rounds:\n\n")
	p.f("| Tool | Median time/op | Spread (95%% CI of the median, at least 5 %%) | Rate from the median | allocs/op | B/op | Tokens | Errors |\n")
	p.f("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, t := range allTools {
		s, ok := c.refs[t]
		if !ok {
			continue
		}
		rate := "inconclusive"
		if c.status != inconclusive {
			rate = fmt.Sprintf("%.2f MB/s", rateOf(c.bytes, s[unitTime]))
		}
		res := c.refResults[t]
		p.f("| %s | %s | %s | %s | %s | %s | %s | %s |\n", t, formatTime(s[unitTime].Median), formatSpread(s[unitTime]), rate,
			group(uint64(s[unitAllocs].Median+0.5)), group(uint64(s[unitBytes].Median+0.5)), group(res.Tokens), group(res.Errors))
	}
	p.f("\n%s", referenceNotes)
	if _, ok := c.refs[jsonDecoder]; ok {
		p.f("\n**jsondecoder** calls the standard library's `encoding/json.Decoder.Token` through EOF. ")
		p.f("It reads both JSON documents in sequence, checks delimiter nesting as it reads, and decodes scalar values. ")
		p.f("The Tokens column counts returned decoder events. The stream omits whitespace, commas and colons, and the events carry no Bytescan spans.\n")
	}
}

// mapLookups is the derived count of keyed lookups by token type of
// docs/specs/measurement.md, "Derived quantities". The processor keeps the
// rule states in a slice indexed by rule number (rulesProcessor.states), so
// it looks up no state by token type: the count is 0 for every scanner.
const mapLookups uint64 = 0

// resetWalks is the derived count of reset walks: Tokens + Abandons − 1, or
// 0 for a scanner that was never called.
func resetWalks(sc counters.Scanner) uint64 {
	n := sc.Tokens + sc.Abandons.EndOfInput + sc.Abandons.MoreInput + sc.Abandons.Error
	if n == 0 {
		return 0
	}
	return n - 1
}

// ratio returns a / b with three significant digits, or with no fraction
// from 1,000 up, or "—" when b is 0.
func ratio(a, b uint64) string {
	if b == 0 {
		return "—"
	}
	v := float64(a) / float64(b)
	if v >= 1000 {
		return group(uint64(v + 0.5))
	}
	return strconv.FormatFloat(v, 'g', 3, 64)
}

// group returns n with commas between groups of three digits.
func group(n uint64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

type printer struct {
	w   io.Writer
	err error
}

func (p *printer) f(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}
