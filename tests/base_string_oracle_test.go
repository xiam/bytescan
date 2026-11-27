package tests

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check base.EscapedRun and base.QuotedWith against
// Go's regexp package, used only as a test oracle. rules/base tests the
// token streams around the rules and the constructor panics by hand.

// TestBaseStringRulesMatchRegexpOracle: EscapedRun without an opener and
// QuotedWith return the same first match as \A(?:expr), the expression of
// their constructor comments, on every input of each row.
func TestBaseStringRulesMatchRegexpOracle(t *testing.T) {
	jsonPlain := core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not()
	jsonEscape := core.Sequence(
		core.Literal(`\`),
		core.Choice(
			core.Rune(core.RuneSet(`"\/bfnrt`)),
			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
		),
	)

	rows := []codeOracleRow{
		// Escaped runs with no opener.
		{name: "EscapedRun double quote", expr: `(?s)(?:[^"\\]|\\.)+`, rule: base.EscapedRun(core.RuneSet(`"`), '\\'), alphabet: "\"\\a\n", maxLen: 5,
			input: []string{`say \"hi\"" x`, `a\`, "é\\\n"}},
		{name: "EscapedRun words", expr: `(?s)(?:[^\]\\ \t\n]|\\.)+`, rule: base.EscapedRun(core.RuneSet("]").Or(core.RuneSet(" \t\n")), '\\'), alphabet: "]\\a ", maxLen: 5,
			input: []string{`a\ b c`, `a\]b]`}},
		{name: "EscapedRun escape in stop", expr: `(?s)(?:[^"\\]|\\.)+`, rule: base.EscapedRun(core.RuneSet(`"\`), '\\'), alphabet: "\"\\a", maxLen: 5},

		// Quoted strings with a checked escape grammar.
		{name: "QuotedWith JSON", expr: `"(?:[^"\\\x00-\x1F]|\\["\\/bfnrt]|\\u[0-9A-Fa-f]{4})*"`, rule: base.QuotedWith('"', jsonPlain, jsonEscape),
			alphabet: "\"\\nu1\x01", maxLen: 5,
			input: []string{`"a\u00e9\"b" x`, `"\u00E"`, `"\x"`, `"\/"`, "\"\t\"", `"é"`, `"\u12345"`}},
		{name: "QuotedWith doubled quote", expr: `(?s)'(?:[^']|'')*'`, rule: base.QuotedWith('\'', core.AnyRune, core.Literal("''")), alphabet: "'a\n", maxLen: 7,
			input: []string{"'it''s' x", "'a' 'b'"}},
		{name: "QuotedWith plain contains quote", expr: `'(?:[^'\n]|\\')*'`, rule: base.QuotedWith('\'', core.RuneSet("\n").Not(), core.Literal(`\'`)), alphabet: "'\\a\n", maxLen: 6},
	}

	checkCodeOracleRows(t, rows)
}

// stringOpenerRow is one row of TestBaseEscapedRunOpenerOracle: an EscapedRun
// whose openers each end in "{", and a regular expression that matches the run
// without the lookahead. Go's regexp has no lookahead, so the oracle of
// (?:[^S\\F]|\\(?s:.)|[F](?!\{))+, with F the first runes of the openers, is
// the longest match of
//
//	(?s)(?:(?:[^S\\F]|\\.)|[F]+(?:[^S\\F{]|\\.))*[F]*
//
// with its last rune dropped when that rune is in F and "{" follows the match:
// a rune of F before "{" starts an opener, and every earlier rune of F in the
// run is followed by another rune of F or by text.
type stringOpenerRow struct {
	name     string
	expr     string
	firsts   string
	rule     bytescan.Rule
	input    []string
	alphabet string
	maxLen   int
}

// TestBaseEscapedRunOpenerOracle: EscapedRun with openers returns the oracle
// match of stringOpenerRow on every input of each row.
func TestBaseEscapedRunOpenerOracle(t *testing.T) {
	rows := []stringOpenerRow{
		{name: "template", expr: "(?s)(?:(?:[^`\\\\$]|\\\\.)|\\$+(?:[^`\\\\${]|\\\\.))*\\$*", firsts: "$",
			rule: base.EscapedRun(core.RuneSet("`"), '\\', "${"), alphabet: "`\\${a", maxLen: 6,
			input: []string{"cost: $5 \\${x}`", "a$$${b", "$\\"}},
		{name: "interpolated string", expr: `(?s)(?:(?:[^"\\#$]|\\.)|[#$]+(?:[^"\\#${]|\\.))*[#$]*`, firsts: "#$",
			rule: base.EscapedRun(core.RuneSet(`"`), '\\', "#{", "${"), alphabet: "\"\\#${", maxLen: 5,
			input: []string{`a#b${c`, `#$#{`, `\#{a"`}},
	}

	const lexTypeCode = bytescan.TokenType("CODE")
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			re := regexp.MustCompile(`\A(?:` + row.expr + `)`)
			re.Longest()

			inputs := append(append([]string{}, row.input...), stringsOver(row.alphabet, row.maxLen)...)
			var trimmed, unmatched, stoppedEarly bool
			mismatches := 0
			for _, input := range inputs {
				want := re.FindString(input)
				if want != "" && strings.HasPrefix(input[len(want):], "{") && strings.ContainsAny(want[len(want)-1:], row.firsts) {
					want, trimmed = want[:len(want)-1], true
				}
				switch {
				case want == "":
					unmatched = true
				case len(want) < len(input):
					stoppedEarly = true
				}

				lx := bytescan.New(strings.NewReader(input))
				lx.MustAddRule(lexTypeCode, row.rule)
				lex, err := lx.Next()
				require.NoError(t, err, "input %q", input)
				got := ""
				if lex.Type() == lexTypeCode {
					got = lex.Text()
				}
				if got != want {
					mismatches++
					if mismatches <= 5 {
						assert.Failf(t, "mismatch", "on %q the first match is %q, want %q", input, got, want)
					}
				}
			}
			t.Logf("%d inputs, %d mismatches", len(inputs), mismatches)
			assert.True(t, trimmed, "row has no match that stops before an opener")
			assert.True(t, unmatched, "row has no non-matching input")
			assert.True(t, stoppedEarly, "row has no match that stops before the end of input")
		})
	}
}
