package tests

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check the combinators of rules/core against Go's
// regexp package, used only as a test oracle for the expression of each
// composed rule. They live here so that no file under rules/ imports regexp.
// rules/core tests lookahead, which regexp cannot express, with hand-written
// token streams and with an independent evaluator of its node graph.

// combinatorOracleRow is one row of TestCoreCombinatorsMatchRegexpOracle: a
// composed rule, the regular expression it implements, and its inputs: the
// listed ones, and every string over alphabet with 1 to maxLen runes.
type combinatorOracleRow struct {
	expr     string
	rule     bytescan.Rule
	input    []string
	alphabet string
	maxLen   int
}

// TestCoreCombinatorsMatchRegexpOracle: each composed rule returns the same
// first match as \A(?:expr) under leftmost-longest semantics, on every input
// of its row. No match on a non-empty input is a first token of type
// UNKNOWN, and the empty input gives io.EOF and no token. Each row has a
// matching input, a non-matching input and a match that stops before the end
// of input. The rows include the composite rows that were hand-written rules
// in regexp_test.go, rules built only from core rules, and rules that compose
// hand-written rules from scanner_rules_test.go as opaque parts.
func TestCoreCombinatorsMatchRegexpOracle(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	a, b, c := core.Rune(core.RuneSet("a")), core.Rune(core.RuneSet("b")), core.Rune(core.RuneSet("c"))
	lit := core.Literal

	rows := []combinatorOracleRow{
		// The composite rows of regexp_test.go.
		{
			expr: `(?s)'(?:\\.|[^'\\])*'`,
			rule: core.Sequence(
				lit("'"),
				core.ZeroOrMore(core.Choice(
					core.Sequence(lit(`\`), core.Rune(isAnyRune)),
					core.Rune(core.RuneSet(`'\`).Not()),
				)),
				lit("'"),
			),
			input: []string{
				`''`, `'abc' x`, `'a\'b' x`, `'a\\' x`, `'\n\'' '`, "'a\nb'", "'a\\\nb'",
				`'abc`, `'a\`, `'a\'`, `x'a'`,
			},
			alphabet: `'\a`, maxLen: 7,
		},
		{expr: `(?:ab)+`, rule: core.OneOrMore(lit("ab")), alphabet: "abx", maxLen: 8},
		{expr: `(?:a|bc)+`, rule: core.OneOrMore(core.Choice(a, lit("bc"))), alphabet: "abcx", maxLen: 6},
		{expr: `[0-9]{2,4}`, rule: core.Repeat(core.Rune(core.ASCIIDigit), 2, 4), alphabet: "12a", maxLen: 6},

		// Alternation with the longest match.
		{expr: `a|ab`, rule: core.Choice(lit("a"), lit("ab")), alphabet: "abx", maxLen: 4},
		{expr: `ab|a`, rule: core.Choice(lit("ab"), lit("a")), alphabet: "abx", maxLen: 4},
		{expr: `if|ifelse|for`, rule: core.Choice(lit("if"), lit("ifelse"), lit("for")), alphabet: "ifelsor", maxLen: 4,
			input: []string{"ifelse", "ifelsex", "ifels", "for", "forx"}},

		// Sequences, options and repetitions.
		{expr: `a(?:aa)*`, rule: core.Sequence(a, core.ZeroOrMore(lit("aa"))), alphabet: "ab", maxLen: 9},
		{expr: `(?:aa)+`, rule: core.OneOrMore(lit("aa")), alphabet: "ab", maxLen: 9},
		{expr: `(?:abc)*(?:abc|ab|a)`, rule: core.Sequence(core.ZeroOrMore(lit("abc")), core.Choice(lit("abc"), lit("ab"), a)), alphabet: "abcx", maxLen: 7},
		{expr: `a*ab`, rule: core.Sequence(core.ZeroOrMore(a), lit("ab")), alphabet: "abx", maxLen: 7},
		{expr: `(?:a|ab)(?:c|bcd)`, rule: core.Sequence(core.Choice(a, lit("ab")), core.Choice(c, lit("bcd"))), alphabet: "abcd", maxLen: 6},
		{expr: `[+-]?[0-9]+`, rule: core.Sequence(core.Optional(core.Rune(core.RuneSet("+-"))), core.RuneRun(core.ASCIIDigit)), alphabet: "+-1x", maxLen: 5},
		{expr: `[A-Za-z_][A-Za-z0-9_]*`, rule: core.Sequence(core.Rune(core.ASCIILetter.Or(core.RuneSet("_"))), core.ZeroOrMore(core.Rune(core.ASCIIWord))), alphabet: "a_1-", maxLen: 5},
		{
			expr: `[0-9]+\.[0-9]*|\.[0-9]+`,
			rule: core.Choice(
				core.Sequence(core.RuneRun(core.ASCIIDigit), lit("."), core.ZeroOrMore(core.Rune(core.ASCIIDigit))),
				core.Sequence(lit("."), core.RuneRun(core.ASCIIDigit)),
			),
			alphabet: "1.x", maxLen: 6,
		},
		{expr: `a?b`, rule: core.Sequence(core.Optional(a), b), alphabet: "abx", maxLen: 5},
		{expr: `a*b`, rule: core.Sequence(core.ZeroOrMore(a), b), alphabet: "abx", maxLen: 6},
		{expr: `(?:a?)*`, rule: core.ZeroOrMore(core.Optional(a)), alphabet: "abx", maxLen: 5},
		{expr: `(?:a*)*b`, rule: core.Sequence(core.ZeroOrMore(core.ZeroOrMore(a)), b), alphabet: "abx", maxLen: 6},
		{expr: `(?:a?){2,3}b`, rule: core.Sequence(core.Repeat(core.Optional(a), 2, 3), b), alphabet: "abx", maxLen: 6},
		{expr: `(?:[0-9]+)*x`, rule: core.Sequence(core.ZeroOrMore(core.RuneRun(core.ASCIIDigit)), core.Rune(core.RuneSet("x"))), alphabet: "1x", maxLen: 9},
		{expr: `(?:ab){0,2}c`, rule: core.Sequence(core.Repeat(lit("ab"), 0, 2), c), alphabet: "abcx", maxLen: 7},
		{expr: `(?:ab){2,}`, rule: core.Repeat(lit("ab"), 2, -1), alphabet: "abx", maxLen: 8},
		{expr: `(?:a|b){3}`, rule: core.Repeat(core.Choice(a, b), 3, 3), alphabet: "abx", maxLen: 5},
		{expr: `(?:a|ab){1,3}b`, rule: core.Sequence(core.Repeat(core.Choice(a, lit("ab")), 1, 3), b), alphabet: "abx", maxLen: 7},
		{expr: `(?i)select\s+[a-z]+`, rule: core.Sequence(core.LiteralIgnoreCase("select"), core.RuneRun(core.ASCIISpace), core.RuneRun(core.ASCIILower)), alphabet: "se lct", maxLen: 3,
			input: []string{"select a", "SELECT  ab x", "select", "select ", "selectab", "Select x1"}},
		{expr: `(?:if|ifelse)[0-9]{1,2}`, rule: core.Sequence(core.AnyLiteral("if", "ifelse"), core.Repeat(core.Rune(core.ASCIIDigit), 1, 2)), alphabet: "ifels1", maxLen: 3,
			input: []string{"if1", "ifelse12", "ifelse123", "ifels1", "ifelse"}},

		// Keyword sets: with the guard ASCIIWord, (?!\w) after a word that
		// ends in a word rune is \b. Registered alone, and composed.
		{expr: `(?:if|else|elseif)\b`, rule: core.Keywords("if", "else", "elseif"), alphabet: "elsif ", maxLen: 7,
			input: []string{"if", "else", "elseif", "elseif x", "elsei", "elsex", "else if", "iff"}},
		{expr: `(?i:if|else|elseif)\b`, rule: core.KeywordsIgnoreCase("if", "ELSE", "ElseIf"), alphabet: "eLsIf ", maxLen: 7,
			input: []string{"IF", "Else", "ELSEIF", "elseif x", "elsei", "elsex", "is\u212a", "if\u212a", "ſ"}},
		{expr: `(?:a|ab|ba)\b[ab]?`, rule: core.Sequence(core.Keywords("a", "ab", "ba"), core.Optional(core.Rune(core.RuneSet("ab")))), alphabet: "ab ", maxLen: 5},
		{expr: `(?i:select|from)\b +x`, rule: core.Sequence(core.KeywordsIgnoreCase("select", "from"), core.RuneRun(core.RuneSet(" ")), lit("x")), alphabet: "Sfrom x", maxLen: 4,
			input: []string{"SELECT x", "from  xy", "fromx x", "select", "From y"}},

		// Hand-written rules of scanner_rules_test.go as opaque parts.
		{expr: `[0-9]+[A-Za-z]+`, rule: core.Sequence(newUnsignedIntegerRule(), newASCIILettersRule()), alphabet: "1a-", maxLen: 6},
		{expr: `(?:[0-9]+)*x`, rule: core.Sequence(core.ZeroOrMore(newUnsignedIntegerRule()), core.Rune(core.RuneSet("x"))), alphabet: "1x", maxLen: 9},
		{expr: `(?:[A-Za-z_][A-Za-z0-9_]*|[0-9]+)(?:,|;)`, rule: core.Sequence(core.Choice(newIdentifierRule(), newUnsignedIntegerRule()), core.Rune(core.RuneSet(",;"))), alphabet: "a1,;", maxLen: 5},
		{expr: `a(?:aa)*b?`, rule: core.Sequence(newOddARunRule(), core.Optional(b)), alphabet: "ab", maxLen: 8},
		{expr: `for\b\(?`, rule: core.Sequence(newKeywordRule("for", isASCIIWord), core.Optional(lit("("))), alphabet: "for( ", maxLen: 5},
		{expr: `for\b +x`, rule: core.Sequence(newKeywordRule("for", isASCIIWord), core.RuneRun(core.RuneSet(" ")), lit("x")), alphabet: "forx ", maxLen: 6},
	}

	for _, row := range rows {
		t.Run(row.expr, func(t *testing.T) {
			re := regexp.MustCompile(`\A(?:` + row.expr + `)`)
			re.Longest()

			inputs := append(append([]string{}, row.input...), stringsOver(row.alphabet, row.maxLen)...)
			var matched, unmatched, stoppedEarly bool
			mismatches := 0
			for _, input := range inputs {
				want := re.FindString(input)
				switch {
				case want == "":
					unmatched = true
				case len(want) < len(input):
					matched, stoppedEarly = true, true
				default:
					matched = true
				}

				lx := bytescan.New(strings.NewReader(input))
				lx.MustAddRule(lexTypeR, row.rule)
				lex, err := lx.Next()
				require.NoError(t, err, "input %q", input)
				got := ""
				if lex.Type() == lexTypeR {
					got = lex.Text()
				}
				if got != want {
					mismatches++
					if mismatches <= 5 {
						t.Errorf("on %q the first match is %q, want %q", input, got, want)
					}
				}
			}
			t.Logf("%d inputs, %d mismatches", len(inputs), mismatches)

			lx := bytescan.New(strings.NewReader(""))
			lx.MustAddRule(lexTypeR, row.rule)
			lex, err := lx.Next()
			assert.True(t, errors.Is(err, io.EOF), "Next on empty input: %v", err)
			assert.Nil(t, lex)

			assert.True(t, matched, "row has no matching input")
			assert.True(t, unmatched, "row has no non-matching input")
			assert.True(t, stoppedEarly, "row has no match that stops before the end of input")
		})
	}
}

// stringsOver returns every string over the runes of alphabet with a length
// from 1 to maxLen.
func stringsOver(alphabet string, maxLen int) []string {
	var all []string
	level := []string{""}
	for n := 1; n <= maxLen; n++ {
		var next []string
		for _, s := range level {
			for _, r := range alphabet {
				next = append(next, s+string(r))
			}
		}
		all = append(all, next...)
		level = next
	}
	return all
}
