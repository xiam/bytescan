package base_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check EscapedRun and QuotedWith with hand-written
// token streams, including the openers that need a lookahead.
// tests/base_string_oracle_test.go checks both rules against the regular
// expressions of their constructor comments.

// jsonEscape is the escape sequence of an RFC 8259 string.
func jsonEscape() bytescan.Rule {
	return core.Sequence(
		core.Literal(`\`),
		core.Choice(
			core.Rune(core.RuneSet(`"\/bfnrt`)),
			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
		),
	)
}

// jsonPlain is the class of the unescaped runes of an RFC 8259 string.
var jsonPlain = core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not()

// TestEscapedRunEndOfInput: EscapedRun, registered alone, on inputs that end
// in each of its scanning states, and on the stop runes and openers that end
// a run.
func TestEscapedRunEndOfInput(t *testing.T) {
	unknowns := codeUnknowns
	cases := map[string]struct {
		rule  bytescan.Rule
		cases []codeCase
	}{
		"no opener": {base.EscapedRun(core.RuneSet(`"`), '\\'), []codeCase{
			{"", nil},
			{"a", []string{codeR("a", 0)}},
			{`a\`, append([]string{codeR("a", 0)}, unknowns(`\`, 1)...)},
			{`\`, unknowns(`\`, 0)},
			{`a\"b"c`, append([]string{codeR(`a\"b`, 0)}, append(unknowns(`"`, 4), codeR("c", 5))...)},
			{"a\\\nb", []string{codeR("a\\\nb", 0)}},
			{"a\nb", []string{codeR("a\nb", 0)}},
		}},
		"template": {base.EscapedRun(core.RuneSet("`"), '\\', "${"), []codeCase{
			{"$", []string{codeR("$", 0)}},
			{"a$", []string{codeR("a$", 0)}},
			{"a${", []string{codeR("a", 0), codeUnknowns("$", 1)[0], codeR("{", 2)}},
			{"${", []string{codeUnknowns("$", 0)[0], codeR("{", 1)}},
			{"$$${", []string{codeR("$$", 0), codeUnknowns("$", 2)[0], codeR("{", 3)}},
			{"a$b{", []string{codeR("a$b{", 0)}},
			{`\${`, []string{codeR(`\${`, 0)}},
			{"$`", append([]string{codeR("$", 0)}, unknowns("`", 1)...)},
			{"a\\`b`", append([]string{codeR("a\\`b", 0)}, unknowns("`", 4)...)},
		}},
		"openers with one first rune": {base.EscapedRun(core.RuneSet(`"`), '\\', "${", "$("), []codeCase{
			{"a$b", []string{codeR("a$b", 0)}},
			{"a${", []string{codeR("a", 0), codeUnknowns("$", 1)[0], codeR("{", 2)}},
			{"a$(", []string{codeR("a", 0), codeUnknowns("$", 1)[0], codeR("(", 2)}},
		}},
		"openers with two first runes": {base.EscapedRun(core.RuneSet(`"`), '\\', "${", "#{"), []codeCase{
			{"#a$b", []string{codeR("#a$b", 0)}},
			{"a#{", []string{codeR("a", 0), codeUnknowns("#", 1)[0], codeR("{", 2)}},
			{"a${", []string{codeR("a", 0), codeUnknowns("$", 1)[0], codeR("{", 2)}},
		}},
		"long opener": {base.EscapedRun(core.AnyRune.Not(), '\\', "]=]"), []codeCase{
			{"a]=", []string{codeR("a]=", 0)}},
			{"a]]=]", []string{codeR("a]", 0), codeUnknowns("]", 2)[0], codeR("=]", 3)}},
			{"a]=a]=]", []string{codeR("a]=a", 0), codeUnknowns("]", 4)[0], codeR("=]", 5)}},
		}},
		"opener starts with a stop rune": {base.EscapedRun(core.RuneSet("$"), '\\', "${"), []codeCase{
			{"a$b", append([]string{codeR("a", 0)}, append(unknowns("$", 1), codeR("b", 2))...)},
		}},
		"escape in stop": {base.EscapedRun(core.RuneSet(`"\`), '\\'), []codeCase{
			{`a\"b`, []string{codeR(`a\"b`, 0)}},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			runCodeCases(t, tc.cases, codeOnly(tc.rule))
		})
	}
}

// TestEscapedRunTemplateBody: EscapedRun beside the opener and the closing
// quote, as the template scanner of a mode driver registers them. No two rules
// match the same text.
func TestEscapedRunTemplateBody(t *testing.T) {
	setup := func(lx *bytescan.Scanner) {
		lx.MustAddRule("TEXT", base.EscapedRun(core.RuneSet("`"), '\\', "${"))
		lx.MustAddRule("OPEN", core.Literal("${"))
		lx.MustAddRule("CLOSE", core.Literal("`"))
	}
	runCodeCases(t, []codeCase{
		{"a ${", []string{codeToken("TEXT", "a ", 0), codeToken("OPEN", "${", 2)}},
		{"cost: $5 \\${x}`", []string{codeToken("TEXT", "cost: $5 \\${x}", 0), codeToken("CLOSE", "`", 14)}},
		{"a\n$${b`", []string{
			codeToken("TEXT", "a\n$", 0), codeToken("OPEN", "${", 3),
			codeToken("TEXT", "b", 5), codeToken("CLOSE", "`", 6),
		}},
	}, setup)
}

// TestQuotedWithEndOfInput: QuotedWith, registered alone, on inputs that end
// in each of its scanning states, and on escapes that the escape rule
// rejects.
func TestQuotedWithEndOfInput(t *testing.T) {
	unknowns := codeUnknowns
	cases := map[string]struct {
		rule  bytescan.Rule
		cases []codeCase
	}{
		"JSON": {base.QuotedWith('"', jsonPlain, jsonEscape()), []codeCase{
			{`"`, unknowns(`"`, 0)},
			{`"a`, unknowns(`"a`, 0)},
			{`"a\`, unknowns(`"a\`, 0)},
			{`"a\u12`, unknowns(`"a\u12`, 0)},
			{`""`, []string{codeR(`""`, 0)}},
			{`"a\"bé\n"`, []string{codeR(`"a\"bé\n"`, 0)}},
			{`"\x"`, unknowns(`"\x"`, 0)},
			{"\"a\nb\"", unknowns("\"a\nb\"", 0)},
			{`"a"b`, append([]string{codeR(`"a"`, 0)}, unknowns("b", 3)...)},
		}},
		"plain contains quote": {base.QuotedWith('\'', core.AnyRune, core.Literal(`\'`)), []codeCase{
			{"'a'b'", append([]string{codeR("'a'", 0)}, append(unknowns("b", 3), unknowns("'", 4)...)...)},
		}},
		"doubled quote escape": {base.QuotedWith('\'', core.AnyRune, core.Literal("''")), []codeCase{
			{"'a'", []string{codeR("'a'", 0)}},
			{"'a''", append([]string{codeR("'a'", 0)}, unknowns("'", 3)...)},
			{"'a'''", []string{codeR("'a'''", 0)}},
			{"'it''s'", []string{codeR("'it''s'", 0)}},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			runCodeCases(t, tc.cases, codeOnly(tc.rule))
		})
	}
}

// TestStringLongTokens: a token of 1<<15 runes is lexed as one token by
// EscapedRun and QuotedWith. A rule whose cost grew with the square of the
// token length would take minutes here.
func TestStringLongTokens(t *testing.T) {
	const n = 1 << 15
	cases := map[string]struct {
		rule  bytescan.Rule
		input string
	}{
		"EscapedRun":  {base.EscapedRun(core.RuneSet("`\n"), '\\', "${"), strings.Repeat(`a$\$`, n/4)},
		"QuotedWith":  {base.QuotedWith('"', jsonPlain, jsonEscape()), `"` + strings.Repeat(`aé\"`, n/9) + `"`},
		"QuotedWith2": {base.QuotedWith('\'', core.AnyRune, core.Literal("''")), "'" + strings.Repeat("a''", n/3) + "'"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, []string{codeR(tc.input, 0)}, codeStream(t, tc.input+"\n", func(lx *bytescan.Scanner) {
				lx.MustAddRule("R", tc.rule)
				lx.MustAddRule("NEWLINE", base.Newline())
			})[:1])
		})
	}
}

// TestStringConstructorPanics: EscapedRun and QuotedWith reject an invalid
// argument at construction time, before any scanner runs the rule.
func TestStringConstructorPanics(t *testing.T) {
	stop := core.RuneSet(`"`)
	cases := map[string]func(){
		"EscapedRun nil stop":               func() { base.EscapedRun(nil, '\\') },
		"EscapedRun invalid escape":         func() { base.EscapedRun(stop, -1) },
		"EscapedRun empty opener":           func() { base.EscapedRun(stop, '\\', "") },
		"EscapedRun one-rune opener":        func() { base.EscapedRun(stop, '\\', "$") },
		"EscapedRun invalid opener":         func() { base.EscapedRun(stop, '\\', "$\xff") },
		"EscapedRun opener starts w escape": func() { base.EscapedRun(stop, '\\', `\{`) },
		"QuotedWith invalid quote":          func() { base.QuotedWith(0xD800, jsonPlain, jsonEscape()) },
		"QuotedWith nil plain":              func() { base.QuotedWith('"', nil, jsonEscape()) },
		"QuotedWith nil escape":             func() { base.QuotedWith('"', jsonPlain, nil) },
	}
	for name, construct := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, construct)
		})
	}
}
