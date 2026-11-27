// LLM Programming Directive:
//
// Objective:
// The tests/scanner_*_test.go files are a comprehensive, self-contained, and robust test suite for the
// `bytescan` package. Their primary purpose is to validate the correctness and resilience of the
// scanner's state machine. This file holds the hand-written rules that they share; tests/README.md says
// which file covers which behaviour and where a new test goes.
//
// Core Abstractions for These Files:
// - `Rule` (bytescan.Rule): A function `func(s bytescan.Symbol) (Rule, State)` representing a state in a state machine.
// - `Symbol` (bytescan.Symbol): A rune enriched with contextual flags (e.g., IsBOL, IsEOF).
// - `State` (bytescan.State): An enum (`StateAccept`, `StateContinue`, `StateReject`, `StatePushBack`)
//   that signals the scanner's next action.
// - `TokenType`: A string identifier for token types (e.g., "INT", "FLOAT", "KEYWORD").
// - `Token`: A token with Type, Text, Offset, and Length.
//
// Key Test Philosophies:
// 1.  Self-Contained: All lexical rules are implemented locally as test helpers (e.g., `newLiteralRule`,
//     `newUnsignedFloatRule`) to eliminate external dependencies and keep tests readable and focused.
// 2.  Adversarial Focus: A significant portion of the tests are designed to be adversarial, probing
//     pathological behaviors and edge cases (e.g., zero-length matches, infinite loops, deep
//     backtracking, rule panics) to ensure the core processing loop is exceptionally robust.
// 3.  Deterministic Verification: Most tests use exact token sequence matching to catch regressions.
//
// Common Patterns and Helpers:
// - `newLiteralRule(lit)`: A rule for an exact, non-empty literal.
// - `newLiteralAlternationRule(lits...)`: A rule for the alternation of the given literals.
// - `excludeLast(n, state)`: The verdict that excludes the last n symbols with n pushbacks,
//   then ends the chain with the given state.
// - Table-driven tests in `TestScannerProcessor` for basic functionality.
// - Adversarial sub-tests in `TestScannerPathologicalRules`.
//
// Known Constraints:
// - Each `TokenType` can only have one rule associated with it.
// - A rule that is still active at EOF is retired; only its recorded accepts count.
// - The scanner uses longest-match semantics with rule priority as a tiebreaker.

package tests

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
)

// Shared rule helpers.
//
// Every rule in this package follows docs/rule-authoring.md. A constructor
// newXxxRule returns the start state and its comment gives the regular
// expression and the behaviour at end of input. States are named. Each
// scanning state handles the EOF symbol first, decides with named predicates,
// and ends with an explicit reject. Every pushback chain comes from
// excludeLast. A rule keeps its state only in the Rule values it returns and
// in immutable constructor parameters, so one Rule value can be registered on
// any number of scanners.

// excludeLast returns the verdict of a rule that excludes the last n symbols
// (n >= 1) from its match and then ends the pushback chain with (nil, s). It
// issues n StatePushBack verdicts in total: the returned one, and n-1 more from
// the Rule it returns. The first pushback removes the symbol that was just read
// (at end of input, the EOF symbol). It is the only way a rule in this package
// builds a pushback chain. See "Rule states and pushback" in
// docs/specs/rule-vm-contract.md and practice 5 in docs/rule-authoring.md.
func excludeLast(n int, s bytescan.State) (bytescan.Rule, bytescan.State) {
	if n < 1 {
		panic("excludeLast: n must be at least 1")
	}
	return pushBackChain(n-1, s), bytescan.StatePushBack
}

// pushBackChain returns the pushback-chain callback that issues n more
// StatePushBack verdicts and then returns (nil, s). It ignores the symbol it
// is shown, which at end of input can be the EOF symbol: that symbol is
// already excluded from the match. Entered through one StatePushBack, it
// excludes n+1 symbols; excludeLast states the total count directly.
func pushBackChain(n int, s bytescan.State) bytescan.Rule {
	return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if n > 0 {
			return pushBackChain(n-1, s), bytescan.StatePushBack
		}
		return nil, s
	}
}

// boundedRule wraps r so that the test fails once r, and every Rule reached
// from it, has been invoked more than budget times in total. It bounds a test
// by work instead of by wall-clock time: a scanner that stops terminating keeps
// invoking its rules, and the invocation that exceeds the budget fails the test.
//
// Engine probe: boundedRule counts calls in a captured variable and fails the
// test from inside a rule on purpose (breaks practices 4 and 6). It is test
// instrumentation around a rule, not a rule.
//
// The wrapped rule runs inside Next, so the test must call Next on the test
// goroutine; require is then legal there. callRule recovers panics but not
// runtime.Goexit, so the failure stops the test instead of retiring the rule.
// A defect that loops without invoking any rule is not caught here; go test
// -timeout remains the backstop for it.
func boundedRule(t testing.TB, budget int, r bytescan.Rule) bytescan.Rule {
	calls := 0
	var wrap func(bytescan.Rule) bytescan.Rule
	wrap = func(r bytescan.Rule) bytescan.Rule {
		return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			calls++
			if calls > budget {
				require.FailNowf(t, "rule invocation budget exceeded", "rule invoked more than %d times: the scanner does not terminate", budget)
			}
			next, state := r(s)
			if next != nil {
				next = wrap(next)
			}
			return next, state
		}
	}
	return wrap(r)
}

// Rune predicates. The names say whether a class is ASCII-only or Unicode.

// isASCIIDigit reports whether r is in [0-9].
func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// isASCIILetter reports whether r is in [A-Za-z].
func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isASCIILowercase reports whether r is in [a-z].
func isASCIILowercase(r rune) bool { return r >= 'a' && r <= 'z' }

// isAOrB reports whether r is in [ab].
func isAOrB(r rune) bool { return r == 'a' || r == 'b' }

// isASCIIWord reports whether r is in \w, which is ASCII-only: [0-9A-Za-z_].
func isASCIIWord(r rune) bool {
	return r == '_' || isASCIIDigit(r) || isASCIILetter(r)
}

// isASCIIWhitespace reports whether r is in [ \t\n\r].
func isASCIIWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// isASCIISymbol reports whether r is in [-+*/=;()].
func isASCIISymbol(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '=', ';', '(', ')':
		return true
	}
	return false
}

// isNotNewline reports whether r is in . without the s flag: any rune but '\n'.
func isNotNewline(r rune) bool { return r != '\n' }

// isAnyRune reports whether r is in . with the s flag: every rune.
func isAnyRune(rune) bool { return true }

// newRunePlusRule returns a rule for X+, where inClass reports whether a rune
// is in X. It accepts after each rune of X. At end of input it rejects the EOF
// symbol, so the run read so far stays the match.
func newRunePlusRule(inClass func(rune) bool) bytescan.Rule {
	var loop bytescan.Rule
	loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if inClass(s.Rune()) {
			return loop, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return loop
}

// newLiteralRule returns a rule for the literal lit, which must not be empty.
// It accepts at the last rune of lit and stops. At end of input before that
// rune it rejects the EOF symbol, so it matches nothing.
func newLiteralRule(lit string) bytescan.Rule {
	if lit == "" {
		panic("newLiteralRule: lit must not be empty")
	}
	return literalAt([]rune(lit), 0)
}

// literalAt is the state of newLiteralRule that reads rune i of lit.
func literalAt(lit []rune, i int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == lit[i] {
			if i == len(lit)-1 {
				return nil, bytescan.StateAccept
			}
			return literalAt(lit, i+1), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// newLiteralAlternationRule returns a rule for the alternation s1|s2|... of
// the given literals, matched rune by rune; empty literals are ignored. A
// literal that is a prefix of a longer one accepts at its own last rune, and
// the rule continues only while a longer literal can still match. At end of
// input it rejects the EOF symbol, so the longest literal read so far stays
// the match.
func newLiteralAlternationRule(literals ...string) bytescan.Rule {
	var candidates [][]rune
	for _, lit := range literals {
		if lit != "" {
			candidates = append(candidates, []rune(lit))
		}
	}
	return literalAlternationAt(candidates, 0)
}

// literalAlternationAt is the state of newLiteralAlternationRule that reads
// rune i of each candidate. A candidate that ends at rune i accepts; a longer
// candidate continues in the next state.
func literalAlternationAt(candidates [][]rune, i int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}

		endsHere := false
		var longer [][]rune
		for _, c := range candidates {
			if c[i] != s.Rune() {
				continue
			}
			if len(c) == i+1 {
				endsHere = true
			} else {
				longer = append(longer, c)
			}
		}

		var next bytescan.Rule
		if len(longer) > 0 {
			next = literalAlternationAt(longer, i+1)
		}

		switch {
		case endsHere:
			return next, bytescan.StateAccept
		case next != nil:
			return next, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// newLiteralThenRule returns a rule fragment that continues over the runes of
// lit without accepting and hands the symbol after them to then. Any other
// symbol, and the EOF symbol, rejects. With an empty lit it returns then.
func newLiteralThenRule(lit string, then bytescan.Rule) bytescan.Rule {
	return literalThenAt([]rune(lit), 0, then)
}

// literalThenAt is the state of newLiteralThenRule that reads rune i of lit.
func literalThenAt(lit []rune, i int, then bytescan.Rule) bytescan.Rule {
	if i == len(lit) {
		return then
	}
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == lit[i] {
			return literalThenAt(lit, i+1, then), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// newKeywordRule returns a rule for keyword(?!W), in Perl notation, where
// continuesWord reports whether a rune is in W: the keyword matches only when
// the symbol after it cannot continue a word. It decides by one symbol of
// lookahead and never accepts before it has decided. At end of input after
// the keyword it excludes the EOF symbol and accepts the keyword.
func newKeywordRule(keyword string, continuesWord func(rune) bool) bytescan.Rule {
	return newLiteralThenRule(keyword, keywordEnd(continuesWord))
}

// keywordEnd is the state of newKeywordRule after the keyword.
func keywordEnd(continuesWord func(rune) bool) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept) // the EOF symbol
		}
		if continuesWord(s.Rune()) {
			return nil, bytescan.StateReject
		}
		return excludeLast(1, bytescan.StateAccept) // the symbol after the keyword
	}
}

// newWhitespaceRule returns a rule for [ \t\n\r]+. It accepts after each
// whitespace rune. At end of input it rejects the EOF symbol, so the run read
// so far stays the match.
func newWhitespaceRule() bytescan.Rule {
	return newRunePlusRule(isASCIIWhitespace)
}

// newUnsignedIntegerRule returns a rule for [0-9]+. It accepts after each
// digit. At end of input it rejects the EOF symbol, so the digits read so far
// stay the match.
func newUnsignedIntegerRule() bytescan.Rule {
	return newRunePlusRule(isASCIIDigit)
}

// newASCIILettersRule returns a rule for [A-Za-z]+. It accepts after each
// letter. At end of input it rejects the EOF symbol, so the letters read so far
// stay the match.
func newASCIILettersRule() bytescan.Rule {
	return newRunePlusRule(isASCIILetter)
}

// newIdentifierRule returns a rule for [A-Za-z_][A-Za-z0-9_]*. It accepts after
// each rune. At end of input it rejects the EOF symbol, so the identifier read
// so far stays the match.
func newIdentifierRule() bytescan.Rule {
	var start, rest bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if r := s.Rune(); isASCIILetter(r) || r == '_' {
			return rest, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	rest = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIWord(s.Rune()) {
			return rest, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newUnsignedFloatRule returns a rule for [0-9]+\.(?!\.)[0-9]*|\.[0-9]+, in
// Perl notation: "1.5", "1." and ".5", but not the "1." of the range "1..5".
// It decides by lookahead: it returns StateContinue until it reads the first
// symbol that cannot extend the float, then excludes that one symbol and
// accepts. It never accepts before that, because a rule's recorded match only
// grows within a token, so a pushback could not shorten an earlier accept. At
// end of input after the radix point or in the fraction, it excludes the EOF
// symbol and accepts; in any other state it rejects the EOF symbol.
func newUnsignedFloatRule() bytescan.Rule {
	var start, integerPart, afterInitialRadix, afterIntegerRadix, fractionalPart bytescan.Rule

	// start reads the first rune: a digit or a radix point.
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return integerPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			return afterInitialRadix, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	// integerPart reads the integer part. It is not a float yet, and neither
	// is the integer part followed by '.': afterIntegerRadix decides.
	integerPart = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return integerPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			return afterIntegerRadix, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	// afterIntegerRadix follows a radix point after an integer part ("123.").
	afterIntegerRadix = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept) // the EOF symbol
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			// A second radix point: "1.." starts a range, so "1." is not a float.
			return nil, bytescan.StateReject
		}
		return excludeLast(1, bytescan.StateAccept) // the symbol after the radix point
	}

	// afterInitialRadix follows a radix point that was the first rune. A
	// fractional part is required.
	afterInitialRadix = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	// fractionalPart reads the digits after the radix point.
	fractionalPart = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept) // the EOF symbol
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		return excludeLast(1, bytescan.StateAccept) // the symbol after the fraction
	}

	return start
}

// newSignedIntegerRule returns a rule for [+-]?[0-9]+. It accepts after each
// digit. At end of input it rejects the EOF symbol, so after a sign alone it
// matches nothing, and after digits the digits read so far stay the match.
func newSignedIntegerRule() bytescan.Rule {
	var start, digits bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if r := s.Rune(); r == '+' || r == '-' {
			// A sign must be followed by at least one digit.
			return digits, bytescan.StateContinue
		}
		if isASCIIDigit(s.Rune()) {
			return digits, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	digits = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return digits, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newSymbolRule returns a rule for [-+*/=;()]: one operator or punctuation
// rune. At end of input it rejects the EOF symbol.
func newSymbolRule() bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIISymbol(s.Rune()) {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
}

// newSingleQuotedStringRule returns a rule for '[^']*': a single-quoted string
// with no escapes, newlines included. It accepts at the closing quote and
// stops. At end of input inside the string it rejects the EOF symbol, so an
// unterminated string matches nothing.
func newSingleQuotedStringRule() bytescan.Rule {
	var start, body bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '\'' {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	body = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '\'' {
			return nil, bytescan.StateAccept
		}
		if isAnyRune(s.Rune()) {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newSlashStarCommentRule returns a rule for (?s)/\*(?:[^*]|\*[^/])*\*/: a
// comment that ends at the first "*/". A '*' in the body consumes the rune
// after it unless that rune is '/', so "/***/" does not match. It accepts at
// the closing '/' and stops. At end of input inside the comment it rejects the
// EOF symbol, so an unterminated comment matches nothing.
func newSlashStarCommentRule() bytescan.Rule {
	var start, afterSlash, body, afterStar bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '/' {
			return afterSlash, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterSlash = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '*' {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	body = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '*' {
			return afterStar, bytescan.StateContinue
		}
		if isAnyRune(s.Rune()) {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterStar = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '/' {
			return nil, bytescan.StateAccept
		}
		if isAnyRune(s.Rune()) {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newBOLCommentRule returns a rule for (?m)^#[^\n]*: a '#' at the beginning of
// a line and the rest of that line, without the line end. It decides by
// lookahead: it continues until the line end or the EOF symbol, then excludes
// that one symbol and accepts. At end of input it excludes the EOF symbol and
// accepts the comment read so far.
func newBOLCommentRule() bytescan.Rule {
	var start, body bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.IsBOL() && s.Rune() == '#' {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	body = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept) // the EOF symbol
		}
		if isNotNewline(s.Rune()) {
			return body, bytescan.StateContinue
		}
		if s.IsEOL() {
			return excludeLast(1, bytescan.StateAccept) // the line end
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newLettersAtEndRule returns a rule for [A-Za-z]+\z: a run of ASCII letters
// that ends the input. It is the worked example of practice 5 in
// docs/rule-authoring.md. It waits for the EOF symbol and never accepts a
// letter. At end of input after a letter it excludes the EOF symbol and
// accepts the letters; any other symbol after a letter rejects.
func newLettersAtEndRule() bytescan.Rule {
	var start, letters bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIILetter(s.Rune()) {
			return letters, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	letters = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept) // the EOF symbol
		}
		if isASCIILetter(s.Rune()) {
			return letters, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return start
}

var (
	whitespaceRule      = newWhitespaceRule()
	unsignedIntegerRule = newUnsignedIntegerRule()
	unsignedFloatRule   = newUnsignedFloatRule()
	identifierRule      = newIdentifierRule()
)

// newABeforeAnySymbolRule returns a rule for a(?=.|\z), in Perl notation: an
// 'a' followed by any symbol. It decides by one symbol of lookahead and never
// accepts before it has decided. At end of input after the 'a' it excludes the
// EOF symbol and accepts the 'a'.
func newABeforeAnySymbolRule() bytescan.Rule {
	var start, afterA bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			return afterA, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterA = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept) // the EOF symbol
		}
		if isAnyRune(s.Rune()) {
			return excludeLast(1, bytescan.StateAccept) // the symbol after the 'a'
		}
		return nil, bytescan.StateReject
	}
	return start
}

// TestScannerPathologicalRules tests adversarial or badly-behaved rules to
// ensure the state machine is robust.
// newOddARunRule returns a rule for a(?:aa)*: a run of an odd number of 'a's.
// It accepts after each odd 'a'. At end of input it rejects the EOF symbol, so
// the longest odd run read so far stays the match.
func newOddARunRule() bytescan.Rule {
	var odd, even bytescan.Rule
	odd = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			return even, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	even = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			return odd, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return odd
}

// newEvenARunRule returns a rule for (?:aa)+: a run of an even number of 'a's.
// It accepts after each even 'a'. At end of input it rejects the EOF symbol,
// so the longest even run read so far stays the match.
func newEvenARunRule() bytescan.Rule {
	var odd, even bytescan.Rule
	odd = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			return even, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	even = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			return odd, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return odd
}

// newABCCycleRule returns a rule for (?:abc)*(?:abc|ab|a): a non-empty prefix
// of "abcabc...". Its three states form a cycle, and it accepts after each
// rune. At end of input it rejects the EOF symbol, so the prefix read so far
// stays the match.
func newABCCycleRule() bytescan.Rule {
	var expectA, expectB, expectC bytescan.Rule
	expectA = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			return expectB, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	expectB = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'b' {
			return expectC, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	expectC = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'c' {
			return expectA, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return expectA
}

// newARunUpToRule returns a rule for a{1,limit}, where limit >= 1. It accepts
// after each 'a' and stops after limit of them. At end of input it rejects the
// EOF symbol, so the run read so far stays the match.
func newARunUpToRule(limit int) bytescan.Rule {
	if limit < 1 {
		panic("newARunUpToRule: limit must be at least 1")
	}
	return aRunAt(0, limit)
}

// aRunAt is the state of newARunUpToRule after n 'a's.
func aRunAt(n, limit int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			if n+1 == limit {
				return nil, bytescan.StateAccept
			}
			return aRunAt(n+1, limit), bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
}

// newARunOfLengthRule returns a rule for a{n}, where n >= 1. It continues over
// the first n-1 'a's and accepts at the n-th. At end of input before that it
// rejects the EOF symbol, so it matches nothing.
func newARunOfLengthRule(n int) bytescan.Rule {
	if n < 1 {
		panic("newARunOfLengthRule: n must be at least 1")
	}
	return aRunOfLengthAt(0, n)
}

// aRunOfLengthAt is the state of newARunOfLengthRule after read 'a's.
func aRunOfLengthAt(read, n int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'a' {
			if read+1 == n {
				return nil, bytescan.StateAccept
			}
			return aRunOfLengthAt(read+1, n), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// newHTTPBeforeLetterRule returns a rule for http(?=://[A-Za-z]), in Perl
// notation. It continues through "http://" and one more symbol, then decides.
// It never accepts before it has decided. At end of input it rejects the EOF
// symbol, so it matches nothing.
func newHTTPBeforeLetterRule() bytescan.Rule {
	var afterScheme bytescan.Rule
	afterScheme = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIILetter(s.Rune()) {
			return excludeLast(4, bytescan.StateAccept) // the letter and "://"
		}
		return nil, bytescan.StateReject
	}
	return newLiteralThenRule("http://", afterScheme)
}

// wikipediaPrefix is the text between "https" and the page name that
// newWikipediaHTTPSRule requires.
const wikipediaPrefix = "://wikipedia.org/wiki/"

// isPageNameRune reports whether r is in [A-Za-z0-9_-].
func isPageNameRune(r rune) bool {
	return isASCIIWord(r) || r == '-'
}

// newWikipediaHTTPSRule returns a rule for
// https(?=://wikipedia\.org/wiki/[A-Za-z0-9_-]+), in Perl notation. The
// number of symbols it excludes depends on the input. It never accepts before
// it has decided. At end of input after a non-empty page name it excludes the
// EOF symbol, the page name and the prefix, and accepts "https"; in any other
// state it rejects the EOF symbol.
func newWikipediaHTTPSRule() bytescan.Rule {
	return newLiteralThenRule("https"+wikipediaPrefix, wikipediaPageName(0))
}

// wikipediaPageName is the state of newWikipediaHTTPSRule after n page-name
// runes. The count lives in the returned Rule values.
func wikipediaPageName(n int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			if n == 0 {
				return nil, bytescan.StateReject
			}
			return excludeLast(len(wikipediaPrefix)+n+1, bytescan.StateAccept) // the EOF symbol, the page name and the prefix
		}
		if isPageNameRune(s.Rune()) {
			return wikipediaPageName(n + 1), bytescan.StateContinue
		}
		if n == 0 {
			return nil, bytescan.StateReject
		}
		return excludeLast(len(wikipediaPrefix)+n+1, bytescan.StateAccept) // the symbol after the page name, the page name and the prefix
	}
}

// newTwoCandidateEndsRule returns a rule for ab(?=cd\.*X)|abcd(?=\.*Y), in
// Perl notation. It chooses between two match ends after it has seen what
// follows "abcd" and the dots. It never accepts before it has decided. At end
// of input it rejects the EOF symbol, so it matches nothing.
func newTwoCandidateEndsRule() bytescan.Rule {
	return newLiteralThenRule("abcd", dotsAfterABCD(0))
}

// dotsAfterABCD is the state of newTwoCandidateEndsRule after "abcd" and n
// '.' symbols.
func dotsAfterABCD(n int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		switch s.Rune() {
		case '.':
			return dotsAfterABCD(n + 1), bytescan.StateContinue
		case 'X':
			return excludeLast(n+3, bytescan.StateAccept) // 'X', the n dots and "cd"
		case 'Y':
			return excludeLast(n+1, bytescan.StateAccept) // 'Y' and the n dots
		}
		return nil, bytescan.StateReject
	}
}

// The two branches of abcd(?=ef)|abcdefgh.
const (
	lookaheadOrLongerLong     = "abcdefgh"         // the longer branch
	lookaheadOrLongerShortLen = len("abcd")        // the match of the lookahead branch
	lookaheadOrLongerValidLen = len("abcd" + "ef") // the match plus its lookahead
)

// newLookaheadOrLongerRule returns one rule for abcd(?=ef)|abcdefgh, built
// with delayed acceptance and terminal fallback. It never accepts "abcd" while
// the longer branch is alive: it accepts all eight symbols at 'h'. Once
// "abcdef" is read, "abcd" is the validated fallback. If 'g' or 'h' fails, one
// terminal pushback chain excludes every symbol after "abcd" and accepts
// "abcd". At end of input the same chain also excludes the EOF symbol. Before
// "abcdef" is complete, any other symbol, or the EOF symbol, rejects.
func newLookaheadOrLongerRule() bytescan.Rule {
	return lookaheadOrLongerAt(0)
}

// lookaheadOrLongerAt(i) has read lookaheadOrLongerLong[:i]. "abcd" is
// validated once i reaches lookaheadOrLongerValidLen.
func lookaheadOrLongerAt(i int) bytescan.Rule {
	validated := i >= lookaheadOrLongerValidLen
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			if validated {
				// The i-4 symbols after "abcd", and the EOF symbol.
				return excludeLast(i-lookaheadOrLongerShortLen+1, bytescan.StateAccept)
			}
			return nil, bytescan.StateReject
		}
		expected := s.Rune() == rune(lookaheadOrLongerLong[i])
		switch {
		case expected && i+1 == len(lookaheadOrLongerLong):
			return nil, bytescan.StateAccept
		case expected:
			return lookaheadOrLongerAt(i + 1), bytescan.StateContinue
		case validated:
			// The i-4 symbols after "abcd", and s.
			return excludeLast(i-lookaheadOrLongerShortLen+1, bytescan.StateAccept)
		}
		return nil, bytescan.StateReject
	}
}

// newUnboundedLongerRule returns one rule for ab(?=c)|ab.*z, built with delayed
// acceptance and terminal fallback. As in Go regexp, '.' matches any rune
// except a newline. The longer branch ab.*z stays alive until a newline or the
// end of input, so the rule reads that far before it settles. It accepts at
// every 'z' after "ab" and keeps scanning for a later 'z'. When the longer
// branch dies, it falls back to "ab" if 'c' followed "ab": one terminal
// pushback chain excludes every symbol after "ab" and accepts "ab". At end of
// input the chain also excludes the EOF symbol. If an accept at a 'z' is
// already recorded, the only-grows rule ignores that shorter fallback.
func newUnboundedLongerRule() bytescan.Rule {
	return unboundedLongerPrefix(0)
}

// unboundedLongerPrefix(i) has read "ab"[:i], with i < 2.
func unboundedLongerPrefix(i int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		expected := s.Rune() == rune("ab"[i])
		switch {
		case expected && i == 1:
			return unboundedLongerTail(0, false), bytescan.StateContinue
		case expected:
			return unboundedLongerPrefix(i + 1), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// unboundedLongerTail(n, validated) has read "ab" and n more symbols.
// validated reports whether the first of them was 'c', which validates "ab".
func unboundedLongerTail(n int, validated bool) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			if validated {
				// The n symbols after "ab", and the EOF symbol.
				return excludeLast(n+1, bytescan.StateAccept)
			}
			return nil, bytescan.StateReject
		}
		validates := n == 0 && s.Rune() == 'c'
		next := unboundedLongerTail(n+1, validated || validates)
		switch {
		case s.Rune() == 'z':
			return next, bytescan.StateAccept
		case isNotNewline(s.Rune()):
			return next, bytescan.StateContinue
		case validated:
			// A newline ends ab.*z: the n symbols after "ab", and the newline.
			return excludeLast(n+1, bytescan.StateAccept)
		}
		return nil, bytescan.StateReject
	}
}

// newABeforeBCRule returns a rule for a(?=bc), in Perl notation. It reads "ab"
// and decides on the next symbol. It never accepts before it has decided. At
// end of input it rejects the EOF symbol, so it matches nothing.
func newABeforeBCRule() bytescan.Rule {
	var afterAB bytescan.Rule
	afterAB = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'c' {
			return excludeLast(2, bytescan.StateAccept) // 'c' and 'b'
		}
		return nil, bytescan.StateReject
	}
	return newLiteralThenRule("ab", afterAB)
}

// alwaysPanic panics on every symbol it is shown.
//
// Engine probe: panics on purpose (breaks practices 2 and 6).
func alwaysPanic(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	panic("rule panicked")
}
