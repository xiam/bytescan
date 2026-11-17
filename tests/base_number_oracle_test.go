package tests

import (
	"strings"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// TestBaseNumberOptionsMatchRegexpOracle: each NumberOption, alone and
// composed with DigitSeparator, gives the rule that its doc comment documents,
// checked as TestBaseCodeRulesMatchRegexpOracle checks the rules without
// options.
func TestBaseNumberOptionsMatchRegexpOracle(t *testing.T) {
	underscore := base.DigitSeparator('_')
	optInt, optFrac := base.OptionalIntegerPart(), base.OptionalFractionPart()
	noZeros, afterPrefix := base.NoLeadingZeros(), base.SeparatorAfterPrefix()

	checkCodeOracleRows(t, []codeOracleRow{
		// Optional parts.
		{name: "Decimal optional integer", expr: `[0-9]+\.[0-9]+|\.[0-9]+`, rule: base.Decimal(optInt), alphabet: "1.x", maxLen: 5},
		{name: "Decimal optional fraction", expr: `[0-9]+\.[0-9]*`, rule: base.Decimal(optFrac), alphabet: "1.x", maxLen: 5},
		{name: "Decimal optional parts", expr: `[0-9]+\.[0-9]*|\.[0-9]+`, rule: base.Decimal(optInt, optFrac), alphabet: "1.x", maxLen: 5,
			input: []string{".", "..", "1..2", ".5.5"}},
		{name: "Decimal optional parts separator", expr: `[0-9]+(?:_[0-9]+)*\.(?:[0-9]+(?:_[0-9]+)*)?|\.[0-9]+(?:_[0-9]+)*`,
			rule: base.Decimal(underscore, optInt, optFrac), alphabet: "1._", maxLen: 6, input: []string{"._1", "1_.", "1._"}},
		{
			name:     "Float optional integer",
			expr:     `[0-9]+(?:\.[0-9]+(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)|\.[0-9]+(?:[eE][+-]?[0-9]+)?`,
			rule:     base.Float(optInt),
			alphabet: "1.e-",
			maxLen:   5,
			input:    []string{".5e+10 x", ".e1"},
		},
		{
			name:     "Float optional fraction",
			expr:     `[0-9]+(?:\.[0-9]*(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)`,
			rule:     base.Float(optFrac),
			alphabet: "1.e-",
			maxLen:   5,
			input:    []string{"1.e+10 x", "1.e"},
		},
		{
			name:     "Float optional parts",
			expr:     `[0-9]+(?:\.[0-9]*(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)|\.[0-9]+(?:[eE][+-]?[0-9]+)?`,
			rule:     base.Float(optInt, optFrac),
			alphabet: "1.e+",
			maxLen:   5,
		},
		{
			name:     "Float optional parts separator",
			expr:     `(?:[0-9]+(?:_[0-9]+)*)(?:\.(?:[0-9]+(?:_[0-9]+)*)?(?:[eE][+-]?[0-9]+(?:_[0-9]+)*)?|[eE][+-]?[0-9]+(?:_[0-9]+)*)|\.[0-9]+(?:_[0-9]+)*(?:[eE][+-]?[0-9]+(?:_[0-9]+)*)?`,
			rule:     base.Float(underscore, optInt, optFrac),
			alphabet: "1.e_",
			maxLen:   5,
		},

		// No leading zeros.
		{name: "Integer no leading zeros", expr: `0|[1-9][0-9]*`, rule: base.Integer(noZeros), alphabet: "01a", maxLen: 4},
		{name: "Integer no leading zeros separator", expr: `0|[1-9][0-9]*(?:_[0-9]+)*`, rule: base.Integer(underscore, noZeros),
			alphabet: "01_", maxLen: 6, input: []string{"0_1", "10_0", "1_0_"}},
		{name: "Decimal no leading zeros", expr: `(?:0|[1-9][0-9]*)\.[0-9]+`, rule: base.Decimal(noZeros), alphabet: "01.", maxLen: 5},
		{
			name:     "Float no leading zeros",
			expr:     `(?:0|[1-9][0-9]*)(?:\.[0-9]+(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)`,
			rule:     base.Float(noZeros),
			alphabet: "01.e",
			maxLen:   5,
			input:    []string{"0e00", "1.00e01", "01e1"},
		},
		{name: "Decimal no leading zeros optional parts", expr: `(?:0|[1-9][0-9]*)\.[0-9]*|\.[0-9]+`, rule: base.Decimal(noZeros, optInt, optFrac),
			alphabet: "01.", maxLen: 5},

		// Separator after the prefix.
		{name: "HexInteger separator after prefix", expr: `0[xX]_?[0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*`, rule: base.HexInteger(underscore, afterPrefix),
			alphabet: "0xf_", maxLen: 5, input: []string{"0x_FF", "0x_F_F", "0x__F", "0x_"}},
		{name: "OctalInteger separator after prefix", expr: `0[oO]_?[0-7]+(?:_[0-7]+)*`, rule: base.OctalInteger(underscore, afterPrefix),
			alphabet: "0o7_8", maxLen: 5},
		{name: "BinaryInteger separator after prefix", expr: `0[bB]_?[01]+(?:_[01]+)*`, rule: base.BinaryInteger(underscore, afterPrefix),
			alphabet: "0b1_", maxLen: 5},
		{name: "HexInteger after prefix without separator", expr: `0[xX][0-9A-Fa-f]+`, rule: base.HexInteger(afterPrefix),
			alphabet: "0xf_", maxLen: 4},

		// Suffixes.
		{name: "Integer suffix", expr: `[0-9]+(?:u|ul|ull)`, rule: base.Integer(base.Suffixes("u", "ul", "ull")), alphabet: "1ul", maxLen: 5,
			input: []string{"1ulx", "1uu"}},
		{name: "Integer optional suffix", expr: `[0-9]+(?:u|L)?`, rule: base.Integer(base.Suffixes("", "u", "L")), alphabet: "1uL", maxLen: 4},
		{name: "HexInteger suffix", expr: `0[xX][0-9A-Fa-f]+n`, rule: base.HexInteger(base.Suffixes("n")), alphabet: "0xfn", maxLen: 5},
		{name: "Float suffix", expr: `(?:[0-9]+(?:\.[0-9]+(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+))[jJ]`, rule: base.Float(base.Suffixes("j", "J")),
			alphabet: "1.ej", maxLen: 5},
		{name: "Decimal suffix last counts", expr: `[0-9]+\.[0-9]+f`, rule: base.Decimal(base.Suffixes("d"), base.Suffixes("f")), alphabet: "1.fd", maxLen: 5},

		// HexFloat.
		{name: "HexFloat", expr: `0[xX][0-9A-Fa-f]+(?:\.[0-9A-Fa-f]+)?[pP][+-]?[0-9]+`, rule: base.HexFloat(), alphabet: "0x1.p", maxLen: 6,
			input: []string{"0x1p-2", "0x1.8p3 x", "0x1.8", "0x1.8p", "0x.8p1", "0x1.p1", "0xfp+1f"}},
		{name: "HexFloat optional parts", expr: `0[xX](?:[0-9A-Fa-f]+(?:\.[0-9A-Fa-f]*)?|\.[0-9A-Fa-f]+)[pP][+-]?[0-9]+`,
			rule: base.HexFloat(optInt, optFrac), alphabet: "0x1.p", maxLen: 6, input: []string{"0x.p1", "0X.8P-0"}},
		{name: "HexFloat optional exponent", expr: `0[xX][0-9A-Fa-f]+(?:\.[0-9A-Fa-f]+(?:[pP][+-]?[0-9]+)?|[pP][+-]?[0-9]+)`,
			rule: base.HexFloat(base.OptionalExponent()), alphabet: "0x1.p", maxLen: 6},
		{name: "HexFloat separator", expr: `0[xX]_?[0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*(?:\.[0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*)?[pP][+-]?[0-9]+(?:_[0-9]+)*`,
			rule: base.HexFloat(underscore, afterPrefix), alphabet: "0x1._p", maxLen: 5,
			input: []string{"0x_1_F.F_Fp1_0", "0x._1p1", "0x_1.1p1", "0x1_.1p1", "0x1._1p1", "0x1.1_p1", "0x1.1p_1", "0x1.1p1_"}},
		{name: "HexFloat ignores no leading zeros", expr: `0[xX][0-9A-Fa-f]+(?:\.[0-9A-Fa-f]+)?[pP][+-]?[0-9]+`, rule: base.HexFloat(noZeros),
			alphabet: "0x0p1", maxLen: 6, input: []string{"0x00p01"}},

		// Exponent.
		{name: "Exponent", expr: `[eE][+-]?[0-9]+`, rule: base.Exponent("eE"), alphabet: "eE+1x", maxLen: 4},
		{name: "Exponent separator", expr: `[pP][+-]?[0-9]+(?:_[0-9]+)*`, rule: base.Exponent("pP", underscore, optInt, noZeros), alphabet: "p-1_", maxLen: 5},
	})
}

// TestBaseNumberLanguageGrammars: the number grammars of five languages,
// composed from base number rules and their options, return the same first
// match as each language's grammar written as a regular expression. The
// listed inputs are the numbers that the example programs (now in the
// bytescan-examples repository) test, and the generated inputs end the input
// in each scanning state.
func TestBaseNumberLanguageGrammars(t *testing.T) {
	underscore := base.DigitSeparator('_')
	optInt, optFrac := base.OptionalIntegerPart(), base.OptionalFractionPart()
	noZeros, afterPrefix := base.NoLeadingZeros(), base.SeparatorAfterPrefix()

	// Digit runs with "_" between digits, as Go and Python write them.
	const (
		dec = `[0-9](?:_?[0-9])*`
		hex = `[0-9A-Fa-f](?:_?[0-9A-Fa-f])*`
	)

	// Go, section "Integer literals", "Floating-point literals" and
	// "Imaginary literals" of the Go specification. The legacy octal form
	// 0_?[0-7]... is not a base rule.
	goInt := `0|[1-9](?:_?` + dec + `)?|0[bB]_?[01](?:_?[01])*|0[oO]?_?[0-7](?:_?[0-7])*|0[xX]_?` + hex
	goExp := `[eE][+-]?` + dec
	goFloat := dec + `\.(?:` + dec + `)?(?:` + goExp + `)?|` + dec + goExp + `|\.` + dec + `(?:` + goExp + `)?|` +
		`0[xX](?:_?` + hex + `\.(?:` + hex + `)?|_?` + hex + `|\.` + hex + `)[pP][+-]?` + dec
	goLegacyOctal := core.Sequence(core.Literal("0"), core.Optional(core.Literal("_")),
		core.RuneRun(core.RuneRange('0', '7')), core.ZeroOrMore(core.Sequence(core.Literal("_"), core.RuneRun(core.RuneRange('0', '7')))))
	goIntRule := core.Choice(
		base.Integer(underscore, noZeros),
		base.BinaryInteger(underscore, afterPrefix),
		base.OctalInteger(underscore, afterPrefix),
		goLegacyOctal,
		base.HexInteger(underscore, afterPrefix),
	)
	goFloatRule := func(opts ...base.NumberOption) bytescan.Rule {
		return core.Choice(
			base.Float(append([]base.NumberOption{underscore, optInt, optFrac}, opts...)...),
			base.HexFloat(append([]base.NumberOption{underscore, afterPrefix, optInt, optFrac}, opts...)...),
		)
	}
	imaginary := base.Suffixes("i")
	goImaginaryRule := core.Choice(
		base.Integer(underscore, imaginary),
		base.BinaryInteger(underscore, afterPrefix, imaginary),
		base.OctalInteger(underscore, afterPrefix, imaginary),
		base.HexInteger(underscore, afterPrefix, imaginary),
		goFloatRule(imaginary),
	)
	goInputs := strings.Fields(`0 7 1234567890 1_000_000 0b1011 0B_1_0 0o17 0O_7 017 0_17 0x1F 0XdeadBEEF 0x_ff_ff
		0. 1.5 72.40 072.40 1.e+0 6.67428e-11 1E6 .25 .12345E+5 1_5. 0.15e+0_2
		0x1p-2 0x2.p10 0x1.Fp+0 0X.8p-0 0X_1FFFP-16
		0i 0123i 0o123i 0xabci 0.i 2.71828i 1.e+0i 6.67428e-11i 1E6i .25i .12345E+5i 0x1p-2i 1_000i
		089 0o8 0b2 0x 1_ 1__2 _1 0_x1 0x1.p 0x1p 0x.p1 1e 1e+ 1.5_ 0b1i 0xi 08.5`)

	// Python 3.12, sections 2.4.5 to 2.4.7. The zero form "0"+(["_"] "0")*
	// is not a base rule.
	pyInt := `[1-9](?:_?[0-9])*|0+(?:_?0)*|0[bB](?:_?[01])+|0[oO](?:_?[0-7])+|0[xX](?:_?[0-9A-Fa-f])+`
	pyPoint := `(?:` + dec + `)?\.` + dec + `|` + dec + `\.`
	pyFloat := pyPoint + `|(?:` + dec + `|` + pyPoint + `)[eE][+-]?` + dec
	pyZeros := core.Sequence(core.RuneRun(core.RuneSet("0")), core.ZeroOrMore(core.Sequence(core.Literal("_"), core.RuneRun(core.RuneSet("0")))))
	pyIntRule := core.Choice(
		base.Integer(underscore, noZeros),
		pyZeros,
		base.BinaryInteger(underscore, afterPrefix),
		base.OctalInteger(underscore, afterPrefix),
		base.HexInteger(underscore, afterPrefix),
	)
	pyJ := base.Suffixes("j", "J")
	pyInputs := strings.Fields(`0 7 1_000 0o17 0O_7 0b1010 0B_1 0xFF 0X_dead_BEEF 00 0_0
		1.5 1. .5 1e9 1E-3 1.5e+10 1_0.2_5 09.5 0e0
		1j 1.5J .5j 1e3j 09j
		007 1__0 1_ 0x 0b2 0o8 1e 1e+ 1.e 1.5e-x 0x_ 1if x 1.__class__ 1..2`)

	// Lua 5.4, section 3.1.
	luaNumeral := `[0-9]+(?:\.[0-9]*)?(?:[eE][+-]?[0-9]+)?|\.[0-9]+(?:[eE][+-]?[0-9]+)?|` +
		`0[xX](?:[0-9A-Fa-f]+(?:\.[0-9A-Fa-f]*)?|\.[0-9A-Fa-f]+)(?:[pP][+-]?[0-9]+)?`
	luaInputs := strings.Fields(`3 345 0xff 0xBEBADA 3.0 3.1416 314.16e-2 0.31416E1 34e1
		0x0.1E 0xA23p-4 0X1.921FB54442D18P+1 3. .5 .5e2 3.e2 0x.8 0xA. 0x1P4
		1..2 0x 0xg 3e 1e+ 0x1p 3x .e1 08`)

	// JSON, RFC 8259, section 6.
	jsonNumber := `-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?`
	jsonInputs := strings.Fields(`0 -0 7 -7 1234567890 0.5 -0.5 10.25 1e5 1E5 1e+5 1e-5 0e0 2.5e10 -2.5E-10 0.0e+0
		01 -01 00 1. 1.e5 .5 - -a +1 1e 1e+ 1E- 0x1F`)

	// SQL, ISO/IEC 9075-2:2016, 5.3.
	sqlExact := `[0-9]+(?:\.[0-9]*)?|\.[0-9]+`
	sqlInputs := strings.Fields(`0 42 007 1.5 1. .5 0.00 1e10 1E10 1.5E-3 .5e+2 1.e5 1e 1e+ 1.2.3 1..2 0x1F -5 +5 1_000 1abc`)
	sqlExactRule := func() bytescan.Rule { return core.Choice(base.Integer(), base.Decimal(optInt, optFrac)) }

	checkCodeOracleRows(t, []codeOracleRow{
		{name: "Go int_lit", expr: goInt, rule: goIntRule, input: goInputs, alphabet: "0o7_x8", maxLen: 5},
		{name: "Go float_lit", expr: goFloat, rule: goFloatRule(), input: goInputs, alphabet: "0x.p1_", maxLen: 5},
		{name: "Go float_lit decimal", expr: goFloat, rule: goFloatRule(), alphabet: "1.e-_", maxLen: 5},
		{name: "Go imaginary_lit", expr: `(?:[0-9](?:_?[0-9])*|` + goInt + `|` + goFloat + `)i`, rule: goImaginaryRule,
			input: goInputs, alphabet: "0x1.ei", maxLen: 5},
		{name: "Python integer", expr: pyInt, rule: pyIntRule, input: pyInputs, alphabet: "01_x", maxLen: 5},
		{name: "Python floatnumber", expr: pyFloat, rule: base.Float(underscore, optInt, optFrac), input: pyInputs, alphabet: "1._e", maxLen: 5},
		{name: "Python imagnumber", expr: `(?:` + pyFloat + `|` + dec + `)[jJ]`,
			rule:  core.Choice(base.Float(underscore, optInt, optFrac, pyJ), base.Integer(underscore, pyJ)),
			input: pyInputs, alphabet: "1._ej", maxLen: 5},
		{name: "Lua numeral", expr: luaNumeral,
			rule:  core.Choice(base.Integer(), base.Float(optInt, optFrac), base.HexInteger(), base.HexFloat(optInt, optFrac, base.OptionalExponent())),
			input: luaInputs, alphabet: "0x1.p", maxLen: 5},
		{name: "Lua numeral decimal", expr: luaNumeral,
			rule:     core.Choice(base.Integer(), base.Float(optInt, optFrac), base.HexInteger(), base.HexFloat(optInt, optFrac, base.OptionalExponent())),
			alphabet: "1.e-", maxLen: 5},
		{name: "JSON number", expr: jsonNumber,
			rule:  core.Sequence(core.Optional(core.Literal("-")), core.Choice(base.Integer(noZeros), base.Float(noZeros))),
			input: jsonInputs, alphabet: "-01.e", maxLen: 5},
		{name: "SQL exact numeric", expr: sqlExact, rule: sqlExactRule(), input: sqlInputs, alphabet: "1.e", maxLen: 5},
		{name: "SQL approximate numeric", expr: `(?:` + sqlExact + `)[Ee][+-]?[0-9]+`,
			rule:  core.Sequence(sqlExactRule(), base.Exponent("Ee")),
			input: sqlInputs, alphabet: "1.E-", maxLen: 5},
	})
}
