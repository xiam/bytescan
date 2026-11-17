package base

import (
	"fmt"
	"unicode/utf8"
)

// OptionalIntegerPart returns the option that lets a number with a point have
// no digits before the point: with it, Decimal, Float and HexFloat match
// ".5", ".5e3" and "0x.8p1". A point alone never matches. It changes no other
// rule.
func OptionalIntegerPart() NumberOption {
	return NumberOption{apply: func(f *codeNumberForm) { f.optionalInteger = true }}
}

// OptionalFractionPart returns the option that lets a number with a point
// have no digits after the point: with it, Decimal, Float and HexFloat match
// "1.", "1.e3" and "0x1.p1". On "1.." the number is "1.". It changes no other
// rule.
func OptionalFractionPart() NumberOption {
	return NumberOption{apply: func(f *codeNumberForm) { f.optionalFraction = true }}
}

// NoLeadingZeros returns the option that allows no leading zero in the
// decimal digits before the point of Integer, Decimal and Float: those digits
// are 0|[1-9][0-9]*, so on "007" Integer matches "0", and on "01.5" Float
// matches nothing. The digits after the point and those of an exponent keep
// their zeros. It changes no other rule.
func NoLeadingZeros() NumberOption {
	return NumberOption{apply: func(f *codeNumberForm) { f.noLeadingZeros = true }}
}

// SeparatorAfterPrefix returns the option that allows one separator between
// the radix prefix and the first digit of HexInteger, OctalInteger,
// BinaryInteger and HexFloat, as Go and Python do: with DigitSeparator('_'),
// HexInteger matches "0x_FF" and "0x_F_F", but not "0x_" or "0x__F". Without
// DigitSeparator it changes nothing, and it changes no other rule.
func SeparatorAfterPrefix() NumberOption {
	return NumberOption{apply: func(f *codeNumberForm) { f.separatorAfterPrefix = true }}
}

// OptionalExponent returns the option that makes the exponent of HexFloat
// optional when the mantissa has a point, as in Lua: with it, HexFloat
// matches "0x1.8" and "0x1p4" but not "0x1", which is a HexInteger. It
// changes no other rule.
func OptionalExponent() NumberOption {
	return NumberOption{apply: func(f *codeNumberForm) { f.optionalExponent = true }}
}

// Suffixes returns the option that ends a number with one of suffixes, with
// the longest match: Integer(Suffixes("u", "ul")) matches "1u" and "1ul" and
// not "1". An empty string in suffixes lets the number end with no suffix,
// so Integer(Suffixes("", "u")) matches "1" and "1u". Each number rule takes
// it; Exponent does not. A required suffix makes the rule read the number to
// its end before it can reject it. It panics if no suffix is given, if every
// suffix is empty, or if a suffix is not valid UTF-8.
func Suffixes(suffixes ...string) NumberOption {
	var nonEmpty []string
	optional := false
	for _, s := range suffixes {
		switch {
		case !utf8.ValidString(s):
			panic(fmt.Sprintf("base.Suffixes: suffix %q is not valid UTF-8", s))
		case s == "":
			optional = true
		default:
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) == 0 {
		panic("base.Suffixes: no non-empty suffix")
	}
	return NumberOption{apply: func(f *codeNumberForm) {
		f.suffixes, f.suffixOptional = nonEmpty, optional
	}}
}
