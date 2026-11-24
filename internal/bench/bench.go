// Package bench is the benchmark harness of docs/specs/measurement.md. It is
// internal to this module: it is a measurement tool, not API.
//
// The package holds the five reference cases (Case, cases), the three lanes
// that each case runs in (Lane), one timed run of a case in a lane (lex), the
// outside references of the text cases (refLex), the reading of benchstat's
// results (benchstat), and the report (Report). The benchmark BenchmarkCase
// runs one case in one lane, and BenchmarkReference one outside reference. The runner
// internal/bench/cmd/benchreport runs the lanes interleaved and writes the
// report.
//
// The harness owns the clock. lex starts its interval before the first Next
// and ends it after the last Next returns, so the tokens that the scanner
// emits after its last read are inside the interval. The input is in memory
// before the interval starts.
package bench

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/counters"
	"github.com/xiam/bytescan/internal/meter"
)

// Lane is one of the three ways a case runs. The lanes differ only in the
// instruments.
type Lane string

// The lanes of docs/specs/measurement.md, "allLanes".
const (
	// Control is the normal build, with no wrapper.
	Control Lane = "control"
	// Wrapped is the normal build, with meter.Reader and meter.Source.
	Wrapped Lane = "wrapped"
	// Instrumented is the build with the tag bench, with meter.Reader and
	// meter.Source.
	Instrumented Lane = "instrumented"
)

// allLanes lists the lanes in the order of the report.
var allLanes = []Lane{Control, Wrapped, Instrumented}

// parseLane returns the lane named s.
func parseLane(s string) (Lane, error) {
	for _, l := range allLanes {
		if string(l) == s {
			return l, nil
		}
	}
	return "", fmt.Errorf("bench: unknown lane %q, want control, wrapped or instrumented", s)
}

// rule is one rule of a rule set, with its token type.
type rule struct {
	typ  bytescan.TokenType
	rule bytescan.Rule
}

// Case is a reference case: a rule set, the way the scanner reads, and an
// input.
type Case struct {
	// name is the name of the case in the contract: json, code, slip, stream
	// or large.
	name string
	// ruleSet and source describe the rule set and the input for the report.
	ruleSet, source string
	// rules returns a new copy of the rule set, in the order of registration.
	rules func() []rule
	// newCursor returns the cursor that the scanner reads r through.
	newCursor func(r io.Reader) *cursor.Reader
	// input returns the input of the case. It is called before the interval.
	input func() ([]byte, error)
}

// findCase returns the case named name: a reference case, a cold case or a
// case of unicodeCases.
func findCase(name string) (Case, bool) {
	for _, c := range namedCases() {
		if c.name == name {
			return c, true
		}
	}
	return Case{}, false
}

// namedCases returns the reference cases, then the cases that a comparison
// runs only when -cases names them: the cold cases and unicodeCases.
func namedCases() []Case {
	return append(append(cases(), coldCases()...), unicodeCases()...)
}

// result is what one run of a case counted.
type result struct {
	// Tokens is the number of tokens that the run consumed.
	Tokens uint64
	// Counts is the snapshot of the wrappers after the last Next. It is zero
	// in the control lane.
	Counts meter.Counts
	// Cursor and Scanner are the snapshots of the engine counters after the
	// last Next. They are zero outside the instrumented lane.
	Cursor  counters.Cursor
	Scanner counters.Scanner
}

// Errors of lex.
var (
	// errLaneBuild means that the lane does not match the build: the
	// instrumented lane needs the tag bench, and the other lanes need the
	// normal build.
	errLaneBuild = errors.New("bench: the lane does not match the build tags")
	// errCounts means that the wrappers did not count the whole input.
	errCounts = errors.New("bench: the wrappers did not count the whole input")
)

// sink keeps the compiler from dropping the tokens.
var sink *bytescan.Token

// lex lexes in once, in lane l. It calls start before the first Next and stop
// after the last Next returns, so a timer that start and stop drive, such as
// testing.B.StartTimer and testing.B.StopTimer, measures the interval of the
// harness. The setup of the
// scanner and its rules, and the snapshots after the run, are outside the
// interval. Every lane consumes the tokens through the same bytescan.Source
// interface. A token of type bytescan.TokenTypeUnknown is an error: no
// reference case has one.
func lex(c Case, l Lane, in []byte, start, stop func()) (result, error) {
	var counts meter.Counts
	var r io.Reader = bytes.NewReader(in)
	if l != Control {
		r = meter.NewReader(r, &counts)
	}
	cur := c.newCursor(r)
	sc := bytescan.NewWithCursor(cur)
	for _, rr := range c.rules() {
		sc.MustAddRule(rr.typ, rr.rule)
	}
	if _, tagged := counters.OfScanner(sc); tagged != (l == Instrumented) {
		return result{}, fmt.Errorf("%w: lane %s", errLaneBuild, l)
	}
	var src bytescan.Source = sc
	if l != Control {
		src = meter.NewSource(sc, &counts)
	}

	var res result
	var err error
	start()
	for {
		var tok *bytescan.Token
		tok, err = src.Next()
		if err != nil {
			break
		}
		if tok.Type() == bytescan.TokenTypeUnknown {
			err = fmt.Errorf("bench: case %s: an unknown token at byte %d", c.name, tok.Offset())
			break
		}
		res.Tokens++
		sink = tok
	}
	stop()
	if err != io.EOF {
		return result{}, fmt.Errorf("bench: case %s, lane %s: %w", c.name, l, err)
	}

	res.Counts = counts
	if l != Control && (counts.Bytes != uint64(len(in)) || counts.TokenBytes != uint64(len(in)) || counts.Tokens != res.Tokens) {
		return result{}, fmt.Errorf("%w: case %s, lane %s: %d bytes, %d token bytes and %d tokens for %d bytes and %d tokens",
			errCounts, c.name, l, counts.Bytes, counts.TokenBytes, counts.Tokens, len(in), res.Tokens)
	}
	if l == Instrumented {
		res.Cursor, _ = counters.OfCursor(cur)
		res.Scanner, _ = counters.OfScanner(sc)
	}
	return res, nil
}
