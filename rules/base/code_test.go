package base_test

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check the programming-language tokens with
// hand-written token streams. tests/base_code_oracle_test.go checks each rule
// against the regular expression of its constructor comment.

// Token types of the code scanner of codeScanner.
const (
	codeTypeSpace   = bytescan.TokenType("SPACE")
	codeTypeNewline = bytescan.TokenType("NEWLINE")
	codeTypeKeyword = bytescan.TokenType("KEYWORD")
	codeTypeIdent   = bytescan.TokenType("IDENT")
	codeTypeInt     = bytescan.TokenType("INT")
	codeTypeHex     = bytescan.TokenType("HEX")
	codeTypeOctal   = bytescan.TokenType("OCTAL")
	codeTypeBinary  = bytescan.TokenType("BINARY")
	codeTypeFloat   = bytescan.TokenType("FLOAT")
	codeTypeString  = bytescan.TokenType("STRING")
	codeTypeRaw     = bytescan.TokenType("RAW")
	codeTypeComment = bytescan.TokenType("COMMENT")
	codeTypeOp      = bytescan.TokenType("OP")
)

// codeScanner registers the rules of a small C-like language on lx. The keyword
// comes before the identifier, so it wins the tie on "if". The two comment
// rules compose into one rule with core.Choice.
func codeScanner(lx *bytescan.Scanner) {
	sep := base.DigitSeparator('_')
	lx.MustAddRule(codeTypeSpace, base.Whitespace())
	lx.MustAddRule(codeTypeNewline, base.Newline())
	lx.MustAddRule(codeTypeKeyword, core.Keyword("if", core.ASCIIWord))
	lx.MustAddRule(codeTypeIdent, base.ASCIIIdentifier())
	lx.MustAddRule(codeTypeInt, base.Integer(sep))
	lx.MustAddRule(codeTypeHex, base.HexInteger(sep))
	lx.MustAddRule(codeTypeOctal, base.OctalInteger(sep))
	lx.MustAddRule(codeTypeBinary, base.BinaryInteger(sep))
	lx.MustAddRule(codeTypeFloat, base.Float(sep))
	lx.MustAddRule(codeTypeString, base.Quoted('"', '\\'))
	lx.MustAddRule(codeTypeRaw, base.RawMultiline("`", "`"))
	lx.MustAddRule(codeTypeComment, core.Choice(base.LineComment("//"), base.BlockComment("/*", "*/")))
	lx.MustAddRule(codeTypeOp, core.AnyLiteral("=", "+", "(", ")", ".", "/", "*"))
}

// codeOnly returns a setup that registers rule alone, as type R.
func codeOnly(rule bytescan.Rule) func(lx *bytescan.Scanner) {
	return func(lx *bytescan.Scanner) { lx.MustAddRule("R", rule) }
}

// codeR returns the stream entry of a token of type R.
func codeR(text string, offset int) string { return codeToken("R", text, offset) }

// TestCodeNewlinePositions pins the line and column of each token around
// line breaks: "\r\n" is one Newline token, the token after "\r\n" or "\n"
// starts the next line at column 0, and the token after a lone "\r" stays on
// its line. These are the positions of the cursor; Newline does not change
// them.
func TestCodeNewlinePositions(t *testing.T) {
	lx := bytescan.New(strings.NewReader("a\r\nb\rc\nd\n\re\r\n\r\nf"))
	lx.MustAddRule(codeTypeNewline, base.Newline())
	lx.MustAddRule(codeTypeIdent, base.ASCIIIdentifier())

	var got []string
	for {
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		got = append(got, fmt.Sprintf("%s %q @%d %d:%d", lex.Type(), lex.Text(), lex.Offset(), lex.Line(), lex.Column()))
	}
	assert.Equal(t, []string{
		`IDENT "a" @0 1:0`,
		`NEWLINE "\r\n" @1 1:1`,
		`IDENT "b" @3 2:0`, // after "\r\n": the next line, column 0
		`NEWLINE "\r" @4 2:1`,
		`IDENT "c" @5 2:2`, // after a lone "\r": the same line
		`NEWLINE "\n" @6 2:3`,
		`IDENT "d" @7 3:0`,
		`NEWLINE "\n" @8 3:1`, // "\n\r" is two line breaks
		`NEWLINE "\r" @9 4:0`,
		`IDENT "e" @10 4:1`,
		`NEWLINE "\r\n" @11 4:2`,
		`NEWLINE "\r\n" @13 5:0`,
		`IDENT "f" @15 6:0`,
	}, got)
}

// TestCodeScanner lexes small programs with every programming-language rule
// registered together and compares the complete token streams.
func TestCodeScanner(t *testing.T) {
	lex := codeToken
	runCodeCases(t, []codeCase{
		{
			Input: "if x1 = 0x_1F + 1_000 // add\r\n",
			Expected: []string{
				lex(codeTypeKeyword, "if", 0), lex(codeTypeSpace, " ", 2),
				lex(codeTypeIdent, "x1", 3), lex(codeTypeSpace, " ", 5),
				lex(codeTypeOp, "=", 6), lex(codeTypeSpace, " ", 7),
				lex(codeTypeInt, "0", 8), lex(codeTypeIdent, "x_1F", 9), lex(codeTypeSpace, " ", 13),
				lex(codeTypeOp, "+", 14), lex(codeTypeSpace, " ", 15),
				lex(codeTypeInt, "1_000", 16), lex(codeTypeSpace, " ", 21),
				lex(codeTypeComment, "// add", 22), lex(codeTypeNewline, "\r\n", 28),
			},
		},
		{
			Input: "iffy=0xFF_FF+0o17+0b1_0+1.5e-3+2e9+1.",
			Expected: []string{
				lex(codeTypeIdent, "iffy", 0), lex(codeTypeOp, "=", 4),
				lex(codeTypeHex, "0xFF_FF", 5), lex(codeTypeOp, "+", 12),
				lex(codeTypeOctal, "0o17", 13), lex(codeTypeOp, "+", 17),
				lex(codeTypeBinary, "0b1_0", 18), lex(codeTypeOp, "+", 23),
				lex(codeTypeFloat, "1.5e-3", 24), lex(codeTypeOp, "+", 30),
				lex(codeTypeFloat, "2e9", 31), lex(codeTypeOp, "+", 34),
				lex(codeTypeInt, "1", 35), lex(codeTypeOp, ".", 36),
			},
		},
		{
			Input: "/***/x/**/\"a\\\"b\"`r\n\\`",
			Expected: []string{
				lex(codeTypeComment, "/***/", 0), lex(codeTypeIdent, "x", 5),
				lex(codeTypeComment, "/**/", 6), lex(codeTypeString, `"a\"b"`, 10),
				lex(codeTypeRaw, "`r\n\\`", 16),
			},
		},
		{
			// The line comment stops before the line break; "1__0" is two
			// integers around an identifier.
			Input: "1__0 //\n1_",
			Expected: []string{
				lex(codeTypeInt, "1", 0), lex(codeTypeIdent, "__0", 1), lex(codeTypeSpace, " ", 4),
				lex(codeTypeComment, "//", 5), lex(codeTypeNewline, "\n", 7),
				lex(codeTypeInt, "1", 8), lex(codeTypeIdent, "_", 9),
			},
		},
		{
			// An unterminated string and an unterminated comment match
			// nothing, so the scanner goes on from the next rune.
			Input: "\"ab\n/* c",
			Expected: []string{
				lex(bytescan.TokenTypeUnknown, `"`, 0), lex(codeTypeIdent, "ab", 1),
				lex(codeTypeNewline, "\n", 3), lex(codeTypeOp, "/", 4), lex(codeTypeOp, "*", 5),
				lex(codeTypeSpace, " ", 6), lex(codeTypeIdent, "c", 7),
			},
		},
	}, codeScanner)
}

// TestCodeEndOfInput: each rule, registered alone, on inputs that end in each
// of its scanning states. An input that ends before the rule has matched
// gives UNKNOWN tokens; one that ends after a match emits the match.
func TestCodeEndOfInput(t *testing.T) {
	unknowns := codeUnknowns
	sep := base.DigitSeparator('_')
	cases := map[string]struct {
		rule  bytescan.Rule
		cases []codeCase
	}{
		"Whitespace": {base.Whitespace(), []codeCase{
			{" \t", []string{codeR(" \t", 0)}},
			{"\v\f\n", append([]string{codeR("\v\f", 0)}, unknowns("\n", 2)...)},
			{"", nil},
		}},
		"Newline": {base.Newline(), []codeCase{
			{"\r", []string{codeR("\r", 0)}},
			{"\r\n", []string{codeR("\r\n", 0)}},
			{"\n\r", []string{codeR("\n", 0), codeR("\r", 1)}},
			{"", nil},
		}},
		"UnicodeIdentifier": {base.UnicodeIdentifier(), []codeCase{
			{"é", []string{codeR("é", 0)}},
			{"é", []string{codeR("é", 0)}},
			{"́e", append(unknowns("́", 0), codeR("e", 1))},
			{"名前", []string{codeR("名前", 0)}},
		}},
		"Integer separator": {base.Integer(sep), []codeCase{
			{"1", []string{codeR("1", 0)}},
			{"1_", append([]string{codeR("1", 0)}, unknowns("_", 1)...)},
			{"1_0", []string{codeR("1_0", 0)}},
		}},
		"HexInteger": {base.HexInteger(), []codeCase{
			{"0", unknowns("0", 0)},
			{"0x", unknowns("0x", 0)},
			{"0xf", []string{codeR("0xf", 0)}},
		}},
		"Decimal": {base.Decimal(), []codeCase{
			{"1", unknowns("1", 0)},
			{"1.", unknowns("1.", 0)},
			{"1.5", []string{codeR("1.5", 0)}},
		}},
		"Float": {base.Float(), []codeCase{
			{"1", unknowns("1", 0)},
			{"1e", unknowns("1e", 0)},
			{"1e+", unknowns("1e+", 0)},
			{"1e+5", []string{codeR("1e+5", 0)}},
			{"1.5", []string{codeR("1.5", 0)}},
			{"1.5e", append([]string{codeR("1.5", 0)}, unknowns("e", 3)...)},
			{"1.5e-", append([]string{codeR("1.5", 0)}, unknowns("e-", 3)...)},
		}},
		"Integer no leading zeros": {base.Integer(base.NoLeadingZeros()), []codeCase{
			{"007", []string{codeR("0", 0), codeR("0", 1), codeR("7", 2)}},
			{"10", []string{codeR("10", 0)}},
		}},
		"Integer suffix": {base.Integer(base.Suffixes("u", "ul")), []codeCase{
			{"1", unknowns("1", 0)},
			{"1u", []string{codeR("1u", 0)}},
			{"1ul", []string{codeR("1ul", 0)}},
			{"1ulx", append([]string{codeR("1ul", 0)}, unknowns("x", 3)...)},
		}},
		"Integer optional suffix": {base.Integer(base.Suffixes("", "u")), []codeCase{
			{"1", []string{codeR("1", 0)}},
			{"1u", []string{codeR("1u", 0)}},
		}},
		"HexInteger separator after prefix": {base.HexInteger(sep, base.SeparatorAfterPrefix()), []codeCase{
			{"0x_", unknowns("0x_", 0)},
			{"0x_f", []string{codeR("0x_f", 0)}},
		}},
		"Decimal optional parts": {base.Decimal(base.OptionalIntegerPart(), base.OptionalFractionPart()), []codeCase{
			{".", unknowns(".", 0)},
			{"1.", []string{codeR("1.", 0)}},
			{".5", []string{codeR(".5", 0)}},
			{"1..2", []string{codeR("1.", 0), codeR(".2", 2)}},
		}},
		"Float optional parts": {base.Float(base.OptionalIntegerPart(), base.OptionalFractionPart()), []codeCase{
			{"1.", []string{codeR("1.", 0)}},
			{"1.e", append([]string{codeR("1.", 0)}, unknowns("e", 2)...)},
			{"1.e5", []string{codeR("1.e5", 0)}},
			{".e1", unknowns(".e1", 0)},
		}},
		"HexFloat": {base.HexFloat(), []codeCase{
			{"0x1.8", unknowns("0x1.8", 0)},
			{"0x1p", unknowns("0x1p", 0)},
			{"0x1p+", unknowns("0x1p+", 0)},
			{"0x1p1", []string{codeR("0x1p1", 0)}},
			{"0x1.8p-2", []string{codeR("0x1.8p-2", 0)}},
		}},
		"HexFloat optional exponent": {base.HexFloat(base.OptionalExponent()), []codeCase{
			{"0x1", unknowns("0x1", 0)},
			{"0x1.8", []string{codeR("0x1.8", 0)}},
			{"0x1.8p", append([]string{codeR("0x1.8", 0)}, unknowns("p", 5)...)},
		}},
		"Exponent": {base.Exponent("eE"), []codeCase{
			{"e", unknowns("e", 0)},
			{"e+", unknowns("e+", 0)},
			{"E-1", []string{codeR("E-1", 0)}},
		}},
		"Quoted": {base.Quoted('"', '\\'), []codeCase{
			{`"`, unknowns(`"`, 0)},
			{`"a`, unknowns(`"a`, 0)},
			{`"a\`, unknowns(`"a\`, 0)},
			{`"a"`, []string{codeR(`"a"`, 0)}},
		}},
		"Quoted doubled quote": {base.Quoted('\'', '\''), []codeCase{
			{"'a'", []string{codeR("'a'", 0)}},
			{"'a''", append([]string{codeR("'a'", 0)}, unknowns("'", 3)...)},
			{"'a'''", []string{codeR("'a'''", 0)}},
		}},
		"RawMultiline line-break delimiters": {base.RawMultiline("\n<<", ">>\n"), []codeCase{
			{"\n<<a\nb>>\n", []string{codeR("\n<<a\nb>>\n", 0)}},
		}},
		"QuotedMultiline line-break escape": {base.QuotedMultiline('"', '\n'), []codeCase{
			{"\"a\n\"b\"", []string{codeR("\"a\n\"b\"", 0)}},
			{"\"a\r\nb\"", []string{codeR("\"a\r\nb\"", 0)}},
		}},
		"RawMultiline triple quote": {base.RawMultiline(`"""`, `"""`), []codeCase{
			{`""`, unknowns(`""`, 0)},
			{`"""`, unknowns(`"""`, 0)},
			{`"""a""`, unknowns(`"""a""`, 0)},
			{`""""""`, []string{codeR(`""""""`, 0)}},
			{`"""a""""`, append([]string{codeR(`"""a"""`, 0)}, unknowns(`"`, 7)...)},
		}},
		"LineComment": {base.LineComment("--"), []codeCase{
			{"-", unknowns("-", 0)},
			{"--", []string{codeR("--", 0)}},
			{"-- a", []string{codeR("-- a", 0)}},
		}},
		"BlockComment": {base.BlockComment("/*", "*/"), []codeCase{
			{"/", unknowns("/", 0)},
			{"/*", unknowns("/*", 0)},
			{"/*/", unknowns("/*/", 0)},
			{"/**", unknowns("/**", 0)},
			{"/***/", []string{codeR("/***/", 0)}},
			{"/****/", []string{codeR("/****/", 0)}},
			{"/* /* */ */", append([]string{codeR("/* /* */", 0)}, unknowns(" */", 8)...)},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			runCodeCases(t, tc.cases, codeOnly(tc.rule))
		})
	}
}

// TestCodeLongTokens: a token of 1<<15 runes is lexed as one token by each
// rule whose text can grow without bound. A rule whose cost grew with the
// square of the token length would take minutes here.
func TestCodeLongTokens(t *testing.T) {
	const n = 1 << 15
	body := strings.Repeat("a*/", n/3)
	cases := map[string]struct {
		rule  bytescan.Rule
		input string
	}{
		"Whitespace":                {base.Whitespace(), strings.Repeat(" \t", n/2)},
		"UnicodeIdentifier":         {base.UnicodeIdentifier(), "x" + strings.Repeat("é1", n/2)},
		"Integer separator":         {base.Integer(base.DigitSeparator('_')), "1" + strings.Repeat("_1", n/2)},
		"Float":                     {base.Float(), strings.Repeat("1", n) + ".5e" + strings.Repeat("1", n)},
		"Integer suffix":            {base.Integer(base.Suffixes("u")), strings.Repeat("1", n) + "u"},
		"Float optional parts":      {base.Float(base.OptionalIntegerPart(), base.OptionalFractionPart()), "." + strings.Repeat("1", n) + "e" + strings.Repeat("1", n)},
		"HexFloat":                  {base.HexFloat(), "0x" + strings.Repeat("f", n) + ".8p" + strings.Repeat("1", n)},
		"Quoted":                    {base.Quoted('"', '\\'), `"` + strings.Repeat(`a\"`, n/3) + `"`},
		"QuotedMultiline":           {base.QuotedMultiline('\'', '\''), "'" + strings.Repeat("a\n''", n/4) + "'"},
		"Raw":                       {base.Raw("<<", ">>"), "<<" + strings.Repeat("a>", n/2) + "b>>"},
		"RawMultiline triple quote": {base.RawMultiline(`"""`, `"""`), `"""` + strings.Repeat("\"\"a\n", n/4) + `"""`},
		"LineComment":               {base.LineComment("//"), "//" + body},
		"BlockComment":              {base.BlockComment("/*", "**/"), "/*" + body + "**/"},
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

// TestCodeConstructorPanics: a constructor rejects an invalid argument at
// construction time, before any scanner runs the rule.
func TestCodeConstructorPanics(t *testing.T) {
	cases := map[string]func(){
		"Identifier nil start":           func() { base.Identifier(nil, core.ASCIIWord) },
		"Identifier nil rest":            func() { base.Identifier(core.ASCIILetter, nil) },
		"DigitSeparator invalid":         func() { base.DigitSeparator(-1) },
		"DigitSeparator surrogate":       func() { base.DigitSeparator(0xD800) },
		"Suffixes none":                  func() { base.Suffixes() },
		"Suffixes empty only":            func() { base.Suffixes("", "") },
		"Suffixes invalid":               func() { base.Suffixes("u", "\xff") },
		"Exponent empty":                 func() { base.Exponent("") },
		"Exponent invalid":               func() { base.Exponent("e\xff") },
		"Quoted invalid quote":           func() { base.Quoted(-1, '\\') },
		"Quoted invalid escape":          func() { base.Quoted('"', 0x110000) },
		"Quoted newline quote":           func() { base.Quoted('\n', '\\') },
		"Quoted carriage return quote":   func() { base.Quoted('\r', '\\') },
		"Quoted newline escape":          func() { base.Quoted('"', '\n') },
		"Quoted carriage return escape":  func() { base.Quoted('"', '\r') },
		"QuotedMultiline invalid quote":  func() { base.QuotedMultiline(-1, '\\') },
		"QuotedMultiline invalid escape": func() { base.QuotedMultiline('"', -1) },
		"Raw empty open":                 func() { base.Raw("", "`") },
		"Raw empty close":                func() { base.Raw("`", "") },
		"Raw newline open":               func() { base.Raw("\n", "`") },
		"Raw carriage return close":      func() { base.Raw("`", "x\r") },
		"LineComment newline":            func() { base.LineComment("#\n") },
		"RawMultiline empty open":        func() { base.RawMultiline("", "`") },
		"RawMultiline empty close":       func() { base.RawMultiline("`", "") },
		"LineComment empty":              func() { base.LineComment("") },
		"BlockComment empty open":        func() { base.BlockComment("", "*/") },
		"BlockComment empty close":       func() { base.BlockComment("/*", "") },
	}
	for name, construct := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, construct)
		})
	}
}

// TestCodeNumberOptions: the zero NumberOption changes nothing, the last
// DigitSeparator counts, and options compose in any order.
func TestCodeNumberOptions(t *testing.T) {
	runCodeCases(t, []codeCase{
		{"1_0", append(append([]string{codeR("1", 0)}, codeUnknowns("_", 1)...), codeR("0", 2))},
	}, codeOnly(base.Integer(base.NumberOption{})))
	runCodeCases(t, []codeCase{
		{"1_0'0", append([]string{codeR("1", 0)}, append(codeUnknowns("_", 1), codeR("0'0", 2))...)},
	}, codeOnly(base.Integer(base.DigitSeparator('_'), base.DigitSeparator('\''))))
	for _, opts := range [][]base.NumberOption{
		{base.NoLeadingZeros(), base.DigitSeparator('_'), base.Suffixes("", "n")},
		{base.Suffixes("", "n"), base.DigitSeparator('_'), base.NoLeadingZeros()},
	} {
		runCodeCases(t, []codeCase{
			{"01_0n", []string{codeR("0", 0), codeR("1_0n", 1)}},
		}, codeOnly(base.Integer(opts...)))
	}
}
