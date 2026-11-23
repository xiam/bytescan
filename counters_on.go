//go:build bench

package bytescan

import (
	"io"

	"github.com/xiam/bytescan/internal/counters"
)

// scanCounters holds the engine counters of a Scanner in the build with the
// tag bench (docs/specs/measurement.md, "The engine counters"). The scanner's
// lock guards it: every method is called by nextToken, which runs with sc.mu
// held.
type scanCounters struct {
	c counters.Scanner
}

// ruleCounters points the rules processor at the counters of its scanner. A
// processor built outside a scanner has a nil pointer and counts nothing.
type ruleCounters struct {
	sc *scanCounters
}

func init() {
	counters.ScannerHook = scannerSnapshot
}

// scannerSnapshot returns a copy of the counters of v, a *Scanner.
func scannerSnapshot(v any) (counters.Scanner, bool) {
	sc, ok := v.(*Scanner)
	if !ok || sc == nil {
		return counters.Scanner{}, false
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	return sc.ctr.c, true
}

// rules returns the counters that the rules processor of the scanner updates.
func (s *scanCounters) rules() ruleCounters {
	return ruleCounters{sc: s}
}

// symbol counts one symbol passed to the rules processor.
func (s *scanCounters) symbol() {
	s.c.Symbols++
}

// token counts one committed token of type typ. long reports that its text
// has its own array.
func (s *scanCounters) token(typ TokenType, long bool) {
	s.c.Tokens++
	if long {
		s.c.LongTokens++
	}
	if typ == TokenTypeUnknown {
		s.c.Unknown++
	}
}

// abandon counts one call that commits no token and returns err. Next returns
// io.EOF and ErrMoreInput unwrapped, so they are compared directly.
func (s *scanCounters) abandon(err error) {
	switch {
	case err == io.EOF:
		s.c.Abandons.EndOfInput++
	case err == ErrMoreInput:
		s.c.Abandons.MoreInput++
	default:
		s.c.Abandons.Error++
	}
}

// stepBack counts one commit that seeks back over overshoot bytes.
func (s *scanCounters) stepBack(overshoot int) {
	s.c.StepBacks++
	s.c.StepBackBytes += uint64(overshoot)
}

// step counts one step of the rules processor: the rule calls, the pushback
// calls among them, and the rules still active after the step.
func (r ruleCounters) step(calls, pushbacks, live int) {
	if r.sc == nil {
		return
	}
	c := &r.sc.c
	c.RuleCalls += uint64(calls)
	c.Pushbacks += uint64(pushbacks)
	c.MaxLive = max(c.MaxLive, uint64(live))
}
