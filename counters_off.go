//go:build !bench

package bytescan

// scanCounters is the empty substitute for the engine counters of a Scanner,
// and ruleCounters the one for the counters that the rules processor updates
// (docs/specs/measurement.md, "The build tag"). Both have size zero and every
// method has an empty body, so the compiler inlines each call to nothing. The
// build with the tag bench uses counters_on.go instead.
type (
	scanCounters struct{}
	ruleCounters struct{}
)

func (*scanCounters) rules() ruleCounters            { return ruleCounters{} }
func (*scanCounters) symbol()                        {}
func (*scanCounters) token(typ TokenType, long bool) {}
func (*scanCounters) abandon(err error)              {}
func (*scanCounters) stepBack(overshoot int)         {}

func (ruleCounters) step(calls, pushbacks, live int) {}
