package bench

// The rule sets of the reference cases. A rule set from outside this module,
// or from a test file, is copied here, and its comment names its source:
// internal/bench cannot import a main package of bytescan-examples or a test
// file of tests/.

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

// jsonRules is the rule set of the JSON scanner of bytescan-examples
// (json/scanner.go: newScanner, stringRule and numberRule), copied.
func jsonRules() []rule {
	unescaped := core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not()
	escape := core.Sequence(
		core.Literal(`\`),
		core.Choice(
			core.Rune(core.RuneSet(`"\/bfnrt`)),
			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
		),
	)
	noLeadingZeros := base.NoLeadingZeros()
	return []rule{
		{"BEGIN_ARRAY", core.Literal("[")},
		{"BEGIN_OBJECT", core.Literal("{")},
		{"END_ARRAY", core.Literal("]")},
		{"END_OBJECT", core.Literal("}")},
		{"NAME_SEPARATOR", core.Literal(":")},
		{"VALUE_SEPARATOR", core.Literal(",")},
		{"TRUE", core.Literal("true")},
		{"FALSE", core.Literal("false")},
		{"NULL", core.Literal("null")},
		{"STRING", base.QuotedWith('"', unescaped, escape)},
		{"NUMBER", core.Sequence(
			core.Optional(core.Literal("-")),
			core.Choice(base.Integer(noLeadingZeros), base.Float(noLeadingZeros)),
		)},
		{"WHITESPACE", core.RuneRun(core.RuneSet(" \t\n\r"))},
	}
}

// codeRules is the rule set of BenchmarkComplexTokens (tests/bench_test.go),
// with the hand-written rules of tests/scanner_rules_test.go, copied below.
func codeRules() []rule {
	return []rule{
		{"FLOAT", newUnsignedFloatRule()},
		{"STRING", newSingleQuotedStringRule()},
		{"COMMENT", newSlashStarCommentRule()},
		{"ID", newIdentifierRule()},
		{"WS", newWhitespaceRule()},
		{"SYMBOL", newSymbolRule()},
		{"KEYWORD", newLiteralAlternationRule("SELECT", "FROM", "WHERE")},
	}
}

// The special bytes of SLIP (RFC 1055), as the SLIP example names them.
const (
	slipEnd    = 0xC0
	slipEsc    = 0xDB
	slipEscEnd = 0xDC
	slipEscEsc = 0xDD
)

// slipRules is the rule set of the SLIP scanner of bytescan-examples
// (streams/slip/scanner.go: newScanner, maxRun and dataByte), copied. The
// scanner reads through bytesource.NewCursor.
func slipRules() []rule {
	dataByte := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	return []rule{
		{"END", octet.Set(slipEnd)},
		{"DATA", core.RuneRunMax(dataByte, 1000)},
		{"ESC_END", octet.Literal([]byte{slipEsc, slipEscEnd})},
		{"ESC_ESC", octet.Literal([]byte{slipEsc, slipEscEsc})},
	}
}

// lineRules is the rule set of the line scanner of the streamtest self-test
// (addLineRules in streamtest/streamtest_test.go), copied.
func lineRules() []rule {
	return []rule{
		{"WORD", core.Repeat(core.Rune(core.RuneRange('a', 'z').Or(core.RuneRange('0', '9'))), 1, 1000)},
		{"EQUALS", core.Literal("=")},
		{"SPACE", core.Literal(" ")},
		{"NEWLINE", core.Literal("\n")},
	}
}

// largeRules is the identifier rule of BenchmarkLargeToken
// (tests/bench_test.go) and the whitespace rule of
// tests/scanner_rules_test.go, copied below.
func largeRules() []rule {
	return []rule{
		{"ID", newIdentifierRule()},
		{"WS", newWhitespaceRule()},
	}
}

// The hand-written rules below are copied from tests/scanner_rules_test.go.

func excludeLast(n int, s bytescan.State) (bytescan.Rule, bytescan.State) {
	if n < 1 {
		panic("excludeLast: n must be at least 1")
	}
	return pushBackChain(n-1, s), bytescan.StatePushBack
}

func pushBackChain(n int, s bytescan.State) bytescan.Rule {
	return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if n > 0 {
			return pushBackChain(n-1, s), bytescan.StatePushBack
		}
		return nil, s
	}
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isASCIIWord(r rune) bool {
	return r == '_' || isASCIIDigit(r) || isASCIILetter(r)
}

func isASCIIWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func isASCIISymbol(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '=', ';', '(', ')':
		return true
	}
	return false
}

func isAnyRune(rune) bool { return true }

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

func newLiteralAlternationRule(literals ...string) bytescan.Rule {
	var candidates [][]rune
	for _, lit := range literals {
		if lit != "" {
			candidates = append(candidates, []rune(lit))
		}
	}
	return literalAlternationAt(candidates, 0)
}

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

func newWhitespaceRule() bytescan.Rule {
	return newRunePlusRule(isASCIIWhitespace)
}

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

func newUnsignedFloatRule() bytescan.Rule {
	var start, integerPart, afterInitialRadix, afterIntegerRadix, fractionalPart bytescan.Rule

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

	afterIntegerRadix = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept)
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			return nil, bytescan.StateReject
		}
		return excludeLast(1, bytescan.StateAccept)
	}

	afterInitialRadix = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	fractionalPart = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept)
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		return excludeLast(1, bytescan.StateAccept)
	}

	return start
}

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
