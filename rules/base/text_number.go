package base

import (
	"fmt"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// TextNumberOption changes the form that TextNumber matches. The zero value
// changes nothing.
type TextNumberOption struct {
	apply func(*textNumberForm)
}

// textNumberForm is the form of the digits of TextNumber. TextNumber starts
// from the ASCII digits.
type textNumberForm struct {
	digit core.Class
}

// TextDigits returns the option that makes digit the class of the digits of
// TextNumber, in place of the ASCII digits: with
// TextDigits(core.RuneRange('٠', '٩')), TextNumber('٬', '٫', ...) reads the
// Arabic-Indic "١٬٢٣٤٫٥". The class replaces the ASCII digits, so a number in
// either set of digits takes core.ASCIIDigit.Or(...). It panics if digit is
// nil. digit must give the same answer for the same rune and have no side
// effect (see core.Class).
func TextDigits(digit core.Class) TextNumberOption {
	if digit == nil {
		panic("base.TextDigits: nil class")
	}
	return TextNumberOption{apply: func(f *textNumberForm) { f.digit = digit }}
}

// TextNumber returns a rule for a number of human text, with sep as the
// thousands separator and decimal as the decimal separator. With sep = ','
// and decimal = '.' it is, in Perl notation:
//
//	(?:[0-9]{1,3}(?:,[0-9]{3})+(?![0-9])|[0-9]+)(?:\.[0-9]+)?%?
//
// The integer part is either digits in groups of three after a first group of
// one to three digits, or digits with no separator. A group is exactly three
// digits, so in "1,2345" the number is "1" and the separator is not part of
// it. A fraction needs a digit after the decimal separator, and a percent sign
// may end the number: "3,500.25", "12.5%", "1500". The separators are
// parameters, so TextNumber('.', ',') reads "3.500,25" and
// TextNumber('\u202F', ',') reads "3\u202F500,25", with a narrow no-break space.
// The digits are ASCII unless the option TextDigits gives another class, which
// then takes the place of [0-9] in every part of the form above; there is no
// sign and no exponent.
//
// At end of input after a digit or the percent sign it ends with the number
// read so far; at end of input after a separator it ends with the number
// before the separator, which it excludes with a pushback chain. It reads one
// rune past a group of three digits to check that no digit follows, and
// excludes that rune. It panics if sep equals decimal, if either of them is an
// ASCII digit, a rune of the digit class or the percent sign, or if the digit
// class holds the percent sign.
func TextNumber(sep, decimal rune, opts ...TextNumberOption) bytescan.Rule {
	form := textNumberForm{digit: core.ASCIIDigit}
	for _, opt := range opts {
		if opt.apply != nil {
			opt.apply(&form)
		}
	}
	if form.digit('%') {
		panic("base.TextNumber: the digit class holds '%'")
	}
	mustNumberSeparator("sep", sep, form.digit)
	mustNumberSeparator("decimal", decimal, form.digit)
	if sep == decimal {
		panic(fmt.Sprintf("base.TextNumber: sep and decimal are both %q", sep))
	}

	digit := core.Rune(form.digit)
	grouped := core.NotFollowedBy(
		core.Sequence(
			core.Repeat(digit, 1, 3),
			core.OneOrMore(core.Sequence(core.Rune(runeClass(sep)), core.Repeat(digit, 3, 3))),
		),
		digit,
	)
	return core.Sequence(
		core.Choice(grouped, core.RuneRun(form.digit)),
		core.Optional(core.Sequence(core.Rune(runeClass(decimal)), core.RuneRun(form.digit))),
		core.Optional(core.Literal("%")),
	)
}

// runeClass returns the class of the one rune r.
func runeClass(r rune) core.Class {
	return core.RuneSet(string(r))
}

// mustNumberSeparator panics if the separator r of TextNumber is an ASCII
// digit, a rune of the digit class or the percent sign. name names the
// parameter in the message.
func mustNumberSeparator(name string, r rune, digit core.Class) {
	if core.ASCIIDigit(r) || digit(r) || r == '%' {
		panic(fmt.Sprintf("base.TextNumber: %s is %q", name, r))
	}
}
