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
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check the programming-language tokens of rules/base
// against Go's regexp package, used only as a test oracle for the expression
// that each constructor comment documents. They live here so that no file
// under rules/ imports regexp. rules/base tests the line and column of each
// token, constructor panics and complete token streams by hand.

// codeOracleRow is one row of TestBaseCodeRulesMatchRegexpOracle: a base
// rule, the regular expression it implements, and its inputs: the listed
// ones, and every string over alphabet with 1 to maxLen runes.
//
// With firstMatch, the oracle is leftmost-first instead of leftmost-longest.
// It is set for the delimited rules, whose documented expression
// open(?:(?!close).)*close needs a lookahead that Go's regexp cannot express:
// their oracle is the lazy expression open.*?close, which matches the same
// text, because that match is unique.
type codeOracleRow struct {
	name       string
	expr       string
	firstMatch bool
	rule       bytescan.Rule
	input      []string
	alphabet   string
	maxLen     int
}

// TestBaseCodeRulesMatchRegexpOracle: each programming-language rule of
// rules/base returns the same first match as \A(?:expr), on every input of
// its row. No match on a non-empty input is a first token of type UNKNOWN,
// and the empty input gives io.EOF and no token. Each row has a matching
// input, a non-matching input and a match that stops before the end of
// input; the generated inputs end the input in each scanning state of the
// rule.
func TestBaseCodeRulesMatchRegexpOracle(t *testing.T) {
	underscore := base.DigitSeparator('_')

	rows := []codeOracleRow{
		// Layout.
		{name: "Whitespace", expr: `[\t\v\f ]+`, rule: base.Whitespace(), alphabet: " \t\v\fa\n", maxLen: 4},
		{name: "Newline", expr: `\r\n|\n|\r`, rule: base.Newline(), alphabet: "\r\na", maxLen: 4,
			input: []string{"\n\r", "\r\r\n", " \n"}},

		// Identifiers.
		{name: "ASCIIIdentifier", expr: `[A-Za-z_][A-Za-z0-9_]*`, rule: base.ASCIIIdentifier(), alphabet: "aZ_1-", maxLen: 4,
			input: []string{"é", "aé"}},
		{
			name:     "UnicodeIdentifier",
			expr:     `[\p{L}\p{Nl}_][\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}]*`,
			rule:     base.UnicodeIdentifier(),
			alphabet: "aé_1\u0301Ⅷ‿-٣",
			maxLen:   3,
			input:    []string{"名前 x", "größe", "x\u0301y", "\u0301x", "Ⅷx", "x٣", "αβγ.δ", "x‿y", "x\u0903"},
		},
		{
			name:     "Identifier dollar",
			expr:     `[$A-Za-z][$0-9A-Za-z_]*`,
			rule:     base.Identifier(core.ASCIILetter.Or(core.RuneSet("$")), core.ASCIIWord.Or(core.RuneSet("$"))),
			alphabet: "$a_1-",
			maxLen:   4,
		},

		// Integers.
		{name: "Integer", expr: `[0-9]+`, rule: base.Integer(), alphabet: "01a_", maxLen: 4},
		{name: "Integer separator", expr: `[0-9]+(?:_[0-9]+)*`, rule: base.Integer(underscore), alphabet: "1_a", maxLen: 6},
		{name: "Integer separator apostrophe", expr: `[0-9]+(?:'[0-9]+)*`, rule: base.Integer(base.DigitSeparator('\'')), alphabet: "1'_", maxLen: 5},
		{name: "HexInteger", expr: `0[xX][0-9A-Fa-f]+`, rule: base.HexInteger(), alphabet: "0xXfg", maxLen: 4,
			input: []string{"0x1F", "0XdeadBEEF;", "0x", "x1"}},
		{name: "HexInteger separator", expr: `0[xX][0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*`, rule: base.HexInteger(underscore), alphabet: "0xf_", maxLen: 5,
			input: []string{"0x_FF", "0xFF_FF", "0xF__F"}},
		{name: "OctalInteger", expr: `0[oO][0-7]+`, rule: base.OctalInteger(), alphabet: "0oO78", maxLen: 4},
		{name: "OctalInteger separator", expr: `0[oO][0-7]+(?:_[0-7]+)*`, rule: base.OctalInteger(underscore), alphabet: "0o7_8", maxLen: 4},
		{name: "BinaryInteger", expr: `0[bB][01]+`, rule: base.BinaryInteger(), alphabet: "0bB12", maxLen: 4},
		{name: "BinaryInteger separator", expr: `0[bB][01]+(?:_[01]+)*`, rule: base.BinaryInteger(underscore), alphabet: "0b1_", maxLen: 5},

		// Decimals and floats.
		{name: "Decimal", expr: `[0-9]+\.[0-9]+`, rule: base.Decimal(), alphabet: "1.x", maxLen: 6},
		{name: "Decimal separator", expr: `[0-9]+(?:_[0-9]+)*\.[0-9]+(?:_[0-9]+)*`, rule: base.Decimal(underscore), alphabet: "1._", maxLen: 6},
		{
			name:     "Float",
			expr:     `[0-9]+(?:\.[0-9]+(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)`,
			rule:     base.Float(),
			alphabet: "1.e-",
			maxLen:   5,
			input:    []string{"1.5E+10", "6.02e23 x", "1e", "1.5e", "1.5e+", "1..2", ".5", "1.", "1E-"},
		},
		{
			name:     "Float separator",
			expr:     `[0-9]+(?:_[0-9]+)*(?:\.[0-9]+(?:_[0-9]+)*(?:[eE][+-]?[0-9]+(?:_[0-9]+)*)?|[eE][+-]?[0-9]+(?:_[0-9]+)*)`,
			rule:     base.Float(underscore),
			alphabet: "1.e_",
			maxLen:   5,
			input:    []string{"1_0.2_5e1_0", "1_000e+1_0;", "1_.5", "1._5", "1e_1"},
		},

		// Quoted strings.
		{name: "Quoted double", expr: `"(?:\\[^\n\r]|[^"\\\n\r])*"`, rule: base.Quoted('"', '\\'), alphabet: "\"\\a\n", maxLen: 5,
			input: []string{`"say \"hi\"" x`, `"a\\" b"`, "\"a\\\nb\"", "\"a\r\"", "\"é\""}},
		{name: "Quoted doubled quote", expr: `'(?:''|[^'\n\r])*'`, rule: base.Quoted('\'', '\''), alphabet: "'a\n", maxLen: 7,
			input: []string{"'it''s' x", "'a' 'b'", "'a''", "''''"}},
		{name: "QuotedMultiline double", expr: `(?s)"(?:\\.|[^"\\])*"`, rule: base.QuotedMultiline('"', '\\'), alphabet: "\"\\a\n", maxLen: 5,
			input: []string{"\"a\\\r\nb\" x", "\"line 1\r\nline 2\""}},
		{name: "QuotedMultiline doubled quote", expr: `(?s)'(?:''|[^'])*'`, rule: base.QuotedMultiline('\'', '\''), alphabet: "'a\n", maxLen: 6},

		// Raw text, with a leftmost-longest oracle where a plain regular
		// expression exists, and with the lazy oracle.
		{name: "Raw backquote", expr: "`[^`\n\r]*`", rule: base.Raw("`", "`"), alphabet: "`a\n", maxLen: 6},
		{name: "Raw backquote lazy", expr: "`[^\n\r]*?`", firstMatch: true, rule: base.Raw("`", "`"), alphabet: "`a\n", maxLen: 6},
		{name: "Raw prefixed", expr: `r"[^\n\r]*?"`, firstMatch: true, rule: base.Raw(`r"`, `"`), alphabet: "r\"a\n", maxLen: 6},
		{name: "RawMultiline backquote", expr: "`[^`]*`", rule: base.RawMultiline("`", "`"), alphabet: "`a\n", maxLen: 6},
		{name: "RawMultiline triple quote", expr: `(?s)""".*?"""`, firstMatch: true, rule: base.RawMultiline(`"""`, `"""`), alphabet: "\"a", maxLen: 10,
			input: []string{"\"\"\"a\nb\"\"\" x", "\"\"\"\n\"\"\"\"", "\"\"\"\"\"\"\"\"", "\"\"\"a\"\"b\"\"\""}},

		// Comments.
		{name: "LineComment slashes", expr: `//[^\n\r]*`, rule: base.LineComment("//"), alphabet: "/a\n", maxLen: 5,
			input: []string{"// note\r\nx", "//\n", "/ /"}},
		{name: "LineComment hash", expr: `#[^\n\r]*`, rule: base.LineComment("#"), alphabet: "#a\r", maxLen: 4},
		{
			name:     "BlockComment C",
			expr:     `/\*[^*]*\*+(?:[^/*][^*]*\*+)*/`,
			rule:     base.BlockComment("/*", "*/"),
			alphabet: "/*a",
			maxLen:   7,
			input:    []string{"/***/", "/**/", "/*/", "/* a\n * b\n */ x", "/* /* */ */", "/* a */ b */", "/*\r\n*/"},
		},
		{name: "BlockComment C lazy", expr: `(?s)/\*.*?\*/`, firstMatch: true, rule: base.BlockComment("/*", "*/"), alphabet: "/*\n", maxLen: 6},
		{name: "BlockComment Lua", expr: `(?s)--\[\[.*?\]\]`, firstMatch: true, rule: base.BlockComment("--[[", "]]"), alphabet: "-[]a", maxLen: 6,
			input: []string{"--[[ a\n]] x", "--[[]]]", "--[[]", "--[[ ] ]]"}},
		{name: "BlockComment HTML", expr: `(?s)<!--.*?-->`, firstMatch: true, rule: base.BlockComment("<!--", "-->"), alphabet: "<!->", maxLen: 6,
			input: []string{"<!-- a -- b --> x", "<!---->", "<!--->", "<!-- --->"}},
	}

	checkCodeOracleRows(t, rows)
}

// checkCodeOracleRows checks that each rule of rows returns the same first
// match as \A(?:expr) on every input of its row, as
// TestBaseCodeRulesMatchRegexpOracle documents.
func checkCodeOracleRows(t *testing.T, rows []codeOracleRow) {
	t.Helper()
	const lexTypeCode = bytescan.TokenType("CODE")

	total := 0
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			re := regexp.MustCompile(`\A(?:` + row.expr + `)`)
			if !row.firstMatch {
				re.Longest()
			}

			inputs := append(append([]string{}, row.input...), stringsOver(row.alphabet, row.maxLen)...)
			total += len(inputs)
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
						t.Errorf("on %q the first match is %q, want %q", input, got, want)
					}
				}
			}
			t.Logf("%d inputs, %d mismatches", len(inputs), mismatches)

			lx := bytescan.New(strings.NewReader(""))
			lx.MustAddRule(lexTypeCode, row.rule)
			lex, err := lx.Next()
			assert.True(t, errors.Is(err, io.EOF), "Next on empty input: %v", err)
			assert.Nil(t, lex)

			assert.True(t, matched, "row has no matching input")
			assert.True(t, unmatched, "row has no non-matching input")
			assert.True(t, stoppedEarly, "row has no match that stops before the end of input")
		})
	}
	t.Logf("%d rows, %d inputs", len(rows), total)
}
