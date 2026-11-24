package base

import (
	"unicode"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// codeUnicodeIdentifierStart is [\p{L}\p{Nl}_]: the start runes of a Unicode
// identifier.
var codeUnicodeIdentifierStart = core.UnicodeTables(unicode.L, unicode.Nl).Or(core.RuneSet("_"))

// codeUnicodeIdentifierContinue is [\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}]:
// the runes that continue a Unicode identifier. \p{Pc} contains '_'.
var codeUnicodeIdentifierContinue = core.UnicodeTables(unicode.L, unicode.Nl, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc)

// Identifier returns a rule for an identifier: one rune of start, then any
// number of runes of rest: [start][rest]*. It accepts after each rune. At end
// of input it rejects the EOF symbol, so the identifier read so far stays the
// match. It panics if start or rest is nil. start and rest must give the same
// answer for the same rune and have no side effect (see core.Class).
//
// Identifier matches keywords too. To lex a keyword as its own token, register
// core.Keyword(word, rest) before the identifier rule: both match the same
// text, and the VM breaks the tie by registration order.
func Identifier(start, rest core.Class) bytescan.Rule {
	if start == nil {
		panic("base.Identifier: nil start class")
	}
	if rest == nil {
		panic("base.Identifier: nil rest class")
	}
	return core.Sequence(core.Rune(start), core.ZeroOrMore(core.Rune(rest)))
}

// ASCIIIdentifier returns a rule for an ASCII identifier:
// [A-Za-z_][A-Za-z0-9_]*. It is Identifier with those classes, and it ends at
// end of input as Identifier does.
func ASCIIIdentifier() bytescan.Rule {
	return Identifier(core.ASCIIIdentifierStart, core.ASCIIWord)
}

// UnicodeIdentifier returns a rule for a Unicode identifier:
// [\p{L}\p{Nl}_][\p{L}\p{Nl}\p{Mn}\p{Mc}\p{Nd}\p{Pc}]*. It approximates the
// default identifier of Unicode UAX #31, with '_' as a start rune and without
// the Other_ID_Start and Other_ID_Continue runes. It does not normalize its
// input: "é" as one rune and "e" + U+0301 are both identifiers, but different
// ones. It ends at end of input as Identifier does.
func UnicodeIdentifier() bytescan.Rule {
	return Identifier(codeUnicodeIdentifierStart, codeUnicodeIdentifierContinue)
}
