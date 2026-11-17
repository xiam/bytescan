package base

import (
	"fmt"
	"unicode/utf8"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// A NumberOption changes the form of the digits that a number rule matches.
// Integer, HexInteger, OctalInteger, BinaryInteger, Decimal, Float, HexFloat
// and Exponent take options, and each option names the rules that it
// changes; a rule ignores an option that does not change it. Options compose:
// Float(DigitSeparator('_'), OptionalIntegerPart(), OptionalFractionPart())
// is the float of Python. When two options set the same thing, the last one
// counts. The zero NumberOption changes nothing.
type NumberOption struct {
	apply func(*codeNumberForm)
}

// codeNumberForm is the form of the digits of a number rule. The zero value is
// plain digits with no separator, both parts of a decimal number required,
// leading zeros allowed, the exponent of HexFloat required and no suffix.
type codeNumberForm struct {
	separator    rune
	hasSeparator bool

	optionalInteger      bool
	optionalFraction     bool
	noLeadingZeros       bool
	separatorAfterPrefix bool
	optionalExponent     bool

	// suffixes are the non-empty suffixes of Suffixes, and suffixOptional
	// is set when Suffixes also took the empty string.
	suffixes       []string
	suffixOptional bool
}

// DigitSeparator returns the option that allows one sep between two digits of
// each digit run of a number: with DigitSeparator('_'), Integer matches
// [0-9]+(?:_[0-9]+)*, so "1_000" is one integer. A separator never starts or
// ends a run, and two separators never follow each other: "1__0" and "1_"
// match "1". In a prefixed integer the separator stays between digits, so
// "0x_FF" does not match unless SeparatorAfterPrefix is also given. It panics
// if sep is not a valid rune.
func DigitSeparator(sep rune) NumberOption {
	if !utf8.ValidRune(sep) {
		panic(fmt.Sprintf("base.DigitSeparator: %U is not a valid rune", sep))
	}
	return NumberOption{apply: func(f *codeNumberForm) {
		f.separator, f.hasSeparator = sep, true
	}}
}

// codeNumberFormOf returns the form that opts set.
func codeNumberFormOf(opts []NumberOption) codeNumberForm {
	var f codeNumberForm
	for _, opt := range opts {
		if opt.apply != nil {
			opt.apply(&f)
		}
	}
	return f
}

// digits returns the rule for one digit run of class c in form f: [c]+, or
// [c]+(?:sep[c]+)* with a separator.
func (f codeNumberForm) digits(c core.Class) bytescan.Rule {
	if !f.hasSeparator {
		return core.RuneRun(c)
	}
	return core.Sequence(core.RuneRun(c), f.separatedRuns(c))
}

// separatedRuns returns (?:sep[c]+)* for the separator of form f, which must
// have one.
func (f codeNumberForm) separatedRuns(c core.Class) bytescan.Rule {
	return core.ZeroOrMore(core.Sequence(f.separatorRune(), core.RuneRun(c)))
}

// separatorRune returns the rule for one separator of form f, which must have
// one.
func (f codeNumberForm) separatorRune() bytescan.Rule {
	return core.Rune(core.RuneSet(string(f.separator)))
}

// integerPart returns the rule for the decimal digits before the point of
// Integer, Decimal and Float: a digit run, or with NoLeadingZeros
// 0|[1-9][0-9]*, where each digit run takes the separator of form f.
func (f codeNumberForm) integerPart() bytescan.Rule {
	if !f.noLeadingZeros {
		return f.digits(core.ASCIIDigit)
	}
	nonZero := []bytescan.Rule{core.Rune(core.RuneRange('1', '9')), core.Optional(core.RuneRun(core.ASCIIDigit))}
	if f.hasSeparator {
		nonZero = append(nonZero, f.separatedRuns(core.ASCIIDigit))
	}
	return core.Choice(core.Literal("0"), core.Sequence(nonZero...))
}

// prefixed returns the rule for a prefixed integer: '0', one rune of
// prefix, then a digit run of class c in form f, and the suffix of form f.
func (f codeNumberForm) prefixed(prefix string, c core.Class) bytescan.Rule {
	return f.suffixed(core.Sequence(core.Literal("0"), core.Rune(core.RuneSet(prefix)), f.prefixedDigits(c)))
}

// prefixedDigits returns the digit run of class c in form f that follows a
// radix prefix, with one optional separator before it under
// SeparatorAfterPrefix.
func (f codeNumberForm) prefixedDigits(c core.Class) bytescan.Rule {
	if f.separatorAfterPrefix && f.hasSeparator {
		return core.Sequence(core.Optional(f.separatorRune()), f.digits(c))
	}
	return f.digits(c)
}

// pointFraction returns the rule for a point and the digits of class c after
// it in form f, \.[c]+, then exp when it is not nil: \.[c]+(?:exp)?.
// OptionalFractionPart makes the digits after the point optional.
func (f codeNumberForm) pointFraction(c core.Class, exp bytescan.Rule) bytescan.Rule {
	frac := f.digits(c)
	if f.optionalFraction {
		frac = core.Optional(frac)
	}
	if exp == nil {
		return core.Sequence(core.Literal("."), frac)
	}
	return core.Sequence(core.Literal("."), frac, core.Optional(exp))
}

// orWithoutInteger returns withInteger, or under OptionalIntegerPart the
// choice of withInteger and the form that withoutInteger builds.
func (f codeNumberForm) orWithoutInteger(withInteger bytescan.Rule, withoutInteger func() bytescan.Rule) bytescan.Rule {
	if !f.optionalInteger {
		return withInteger
	}
	return core.Choice(withInteger, withoutInteger())
}

// exponent returns the rule for an exponent: [marks][+-]?[0-9]+ in form f.
func (f codeNumberForm) exponent(marks string) bytescan.Rule {
	return core.Sequence(
		core.Rune(core.RuneSet(marks)),
		core.Optional(core.Rune(core.RuneSet("+-"))),
		f.digits(core.ASCIIDigit),
	)
}

// suffixed returns r followed by the suffix of form f: one of the suffixes,
// optional when Suffixes took the empty string, or r alone with no suffix.
func (f codeNumberForm) suffixed(r bytescan.Rule) bytescan.Rule {
	if len(f.suffixes) == 0 {
		return r
	}
	suffix := core.AnyLiteral(f.suffixes...)
	if f.suffixOptional {
		suffix = core.Optional(suffix)
	}
	return core.Sequence(r, suffix)
}

// Integer returns a rule for a decimal integer: [0-9]+. It matches leading
// zeros ("007"), because the meaning of a leading zero differs between
// languages. It matches no sign and no prefix: on "0x1F" it matches "0", and
// HexInteger matches all of it. It accepts after each digit. At end of input
// it rejects the EOF symbol, so the digits read so far stay the match. With
// DigitSeparator it waits after a separator for the next digit, and at end of
// input after a separator it ends with the digits before it. With
// NoLeadingZeros it matches 0|[1-9][0-9]*, and with Suffixes a suffix ends it.
func Integer(opts ...NumberOption) bytescan.Rule {
	f := codeNumberFormOf(opts)
	return f.suffixed(f.integerPart())
}

// HexInteger returns a rule for a hexadecimal integer: 0[xX][0-9A-Fa-f]+. At
// end of input after the prefix it matches nothing; after a digit it rejects
// the EOF symbol, so the digits read so far stay the match. With
// DigitSeparator the separator goes between digits, as for Integer, and
// with SeparatorAfterPrefix also between the prefix and the first digit:
// 0[xX]_?[0-9A-Fa-f]+(?:_[0-9A-Fa-f]+)*. With Suffixes a suffix ends it.
// OctalInteger and BinaryInteger take the same options.
func HexInteger(opts ...NumberOption) bytescan.Rule {
	return codeNumberFormOf(opts).prefixed("xX", core.ASCIIHexDigit)
}

// OctalInteger returns a rule for an octal integer with the 0o prefix:
// 0[oO][0-7]+. The C form with a leading zero alone ("017") is a decimal
// Integer here. It ends at end of input as HexInteger does, and it takes
// DigitSeparator as HexInteger does.
func OctalInteger(opts ...NumberOption) bytescan.Rule {
	return codeNumberFormOf(opts).prefixed("oO", core.ASCIIOctalDigit)
}

// BinaryInteger returns a rule for a binary integer: 0[bB][01]+. It ends at
// end of input as HexInteger does, and it takes DigitSeparator as HexInteger
// does.
func BinaryInteger(opts ...NumberOption) bytescan.Rule {
	return codeNumberFormOf(opts).prefixed("bB", core.ASCIIBinaryDigit)
}

// Decimal returns a rule for a decimal number with a fraction:
// [0-9]+\.[0-9]+. It needs digits on both sides of the point, because "1."
// and ".5" are numbers in some languages and not in others ("1..2" is a range
// and "1.max(2)" a method call in some); on "1." it matches nothing. It accepts
// after each digit of the fraction. At end of input after the point it
// matches nothing; after a digit of the fraction it rejects the EOF symbol,
// so the number read so far stays the match. With DigitSeparator each digit
// run takes the separator, as for Integer.
//
// OptionalFractionPart lets the digits after the point be absent, so "1." is
// a Decimal, and at end of input after the point it then matches "1.".
// OptionalIntegerPart adds a form with no digits before the point, so ".5" is
// a Decimal; a point alone never is. With both, Decimal is
// [0-9]+\.[0-9]*|\.[0-9]+. NoLeadingZeros applies to the digits before the
// point, and with Suffixes a suffix ends the number.
func Decimal(opts ...NumberOption) bytescan.Rule {
	f := codeNumberFormOf(opts)
	return f.suffixed(f.orWithoutInteger(
		core.Sequence(f.integerPart(), f.pointFraction(core.ASCIIDigit, nil)),
		func() bytescan.Rule {
			return core.Sequence(core.Literal("."), f.digits(core.ASCIIDigit))
		},
	))
}

// Float returns a rule for a floating-point number with a fraction, an
// exponent or both: [0-9]+(?:\.[0-9]+(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+).
// It matches "1.5", "1.5e-3" and "1e9", but not "1", which is an Integer, and
// it needs digits on both sides of the point, as Decimal does. On "1.5e" it
// matches "1.5": it reads the exponent while it can still match and then ends
// with the longest number read so far. At end of input it ends the same way.
// With DigitSeparator each digit run takes the separator, as for Integer.
//
// OptionalFractionPart, OptionalIntegerPart, NoLeadingZeros and Suffixes
// change the number as they change Decimal. With OptionalFractionPart and
// OptionalIntegerPart, Float is
// [0-9]+(?:\.[0-9]*(?:[eE][+-]?[0-9]+)?|[eE][+-]?[0-9]+)|\.[0-9]+(?:[eE][+-]?[0-9]+)?,
// so "1.", ".5", "1.e3" and ".5e3" are floats.
func Float(opts ...NumberOption) bytescan.Rule {
	f := codeNumberFormOf(opts)
	exp := func() bytescan.Rule { return f.exponent("eE") }
	return f.suffixed(f.orWithoutInteger(
		core.Sequence(
			f.integerPart(),
			core.Choice(f.pointFraction(core.ASCIIDigit, exp()), exp()),
		),
		func() bytescan.Rule {
			return core.Sequence(core.Literal("."), f.digits(core.ASCIIDigit), core.Optional(exp()))
		},
	))
}

// HexFloat returns a rule for a hexadecimal floating-point number, as C99 and
// Go write it: a 0x prefix, hexadecimal digits with an optional fraction, and
// a binary exponent of decimal digits that is required:
// 0[xX][0-9A-Fa-f]+(?:\.[0-9A-Fa-f]+)?[pP][+-]?[0-9]+. It matches "0x1p-2"
// and "0x1.8p3", but not "0x1.8", and on "0x1.8p" it matches nothing. At end
// of input before a digit of the exponent it matches nothing; after one it
// rejects the EOF symbol, so the number read so far stays the match.
//
// DigitSeparator, SeparatorAfterPrefix, OptionalFractionPart,
// OptionalIntegerPart and Suffixes change it as they change HexInteger and
// Float. With DigitSeparator('_'), SeparatorAfterPrefix, OptionalIntegerPart
// and OptionalFractionPart it is the hex_float_lit of Go. OptionalExponent makes the exponent optional when the
// mantissa has a point, as in Lua, so "0x1.8" matches and "0x1" still does
// not: with OptionalExponent the rule needs a point, an exponent or both, as
// Float does. NoLeadingZeros does not change it.
func HexFloat(opts ...NumberOption) bytescan.Rule {
	f := codeNumberFormOf(opts)
	hex := core.ASCIIHexDigit
	exp := func() bytescan.Rule { return f.exponent("pP") }
	var mantissa bytescan.Rule
	if f.optionalExponent {
		mantissa = f.orWithoutInteger(
			core.Sequence(f.prefixedDigits(hex), core.Choice(f.pointFraction(hex, exp()), exp())),
			func() bytescan.Rule {
				return core.Sequence(core.Literal("."), f.digits(hex), core.Optional(exp()))
			},
		)
	} else {
		mantissa = core.Sequence(
			f.orWithoutInteger(
				core.Sequence(f.prefixedDigits(hex), core.Optional(f.pointFraction(hex, nil))),
				func() bytescan.Rule { return core.Sequence(core.Literal("."), f.digits(hex)) },
			),
			exp(),
		)
	}
	return f.suffixed(core.Sequence(core.Literal("0"), core.Rune(core.RuneSet("xX")), mantissa))
}

// Exponent returns a rule for an exponent: one rune of marks, an optional
// sign and decimal digits, [marks][+-]?[0-9]+; Exponent("eE") is the exponent
// of Float and Exponent("pP") that of HexFloat. A scanner composes it after its
// own mantissa, for example core.Sequence(Decimal(), Exponent("eE")) for a
// number whose exponent is required. On "e+" it matches nothing; at end of
// input after a digit it rejects the EOF symbol, so the exponent read so far
// stays the match. DigitSeparator changes the digits as it changes Integer;
// the other options do not change Exponent. It panics if marks is empty or is
// not valid UTF-8.
func Exponent(marks string, opts ...NumberOption) bytescan.Rule {
	if marks == "" || !utf8.ValidString(marks) {
		panic(fmt.Sprintf("base.Exponent: marks %q must be non-empty valid UTF-8", marks))
	}
	return codeNumberFormOf(opts).exponent(marks)
}
