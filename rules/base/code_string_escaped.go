package base

import (
	"fmt"
	"unicode/utf8"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// EscapedRun returns a rule for a run of string body text: one or more runes
// other than a rune of stop and escape, escape sequences, and runes that
// start an opener but are not followed by the rest of it. An escape sequence
// is escape and the rune after it, line breaks included, so an escaped stop
// rune or an escaped opener is part of the run. The run stops before a rune
// of stop and before each opener, such as "${" or "#{".
//
// EscapedRun is the text rule of a body that a mode driver lexes: a template
// literal or an interpolated string, where the driver switches to another
// scanner at the opener and at the closing quote. Three examples, with the
// regular expression of each in Perl notation:
//
//	EscapedRun(core.RuneSet("`"), '\\', "${")  (?:[^`\\$]|\\(?s:.)|\$(?!\{))+
//	EscapedRun(core.RuneSet(`"`), '\\', "#{")  (?:[^"\\#]|\\(?s:.)|#(?!\{))+
//	EscapedRun(core.RuneSet(`"`), '\\')        (?:[^"\\]|\\(?s:.))+
//
// With several openers that start with the same rune, that rune is text when
// none of them follows it. An opener whose first rune is in stop changes
// nothing, because the run stops at that rune anyway. Escape starts an escape
// sequence even when stop contains it. The escaped rune is not checked: a
// language that limits it, or that forbids a line break in the body, composes
// its own run from core rules or checks the text in its driver.
//
// The rule accepts after each item of the run, so it ends with the longest
// run. After a rune that starts an opener it reads the rest of the opener to
// decide. At end of input it ends with the run read so far; an escape rune
// at end of input is not part of the run. It panics if stop is nil, if escape
// is not a valid rune, or if an opener is not valid UTF-8, has fewer than two
// runes, or starts with escape. stop must give the same answer for the same
// rune and have no side effect (see core.Class).
func EscapedRun(stop core.Class, escape rune, openers ...string) bytescan.Rule {
	if stop == nil {
		panic("base.EscapedRun: nil stop class")
	}
	mustCodeRune("EscapedRun", "escape", escape)

	// rests maps the first rune of each opener to the rests of the openers
	// that start with it, in the order the openers were given.
	var firsts []rune
	rests := map[rune][]bytescan.Rule{}
	for _, opener := range openers {
		if !utf8.ValidString(opener) {
			panic(fmt.Sprintf("base.EscapedRun: opener %q is not valid UTF-8", opener))
		}
		first, size := utf8.DecodeRuneInString(opener)
		if utf8.RuneCountInString(opener) < 2 {
			panic(fmt.Sprintf("base.EscapedRun: opener %q has fewer than two runes; add a one-rune opener to stop", opener))
		}
		if first == escape {
			panic(fmt.Sprintf("base.EscapedRun: opener %q starts with escape %U", opener, escape))
		}
		if stop(first) {
			continue
		}
		if _, seen := rests[first]; !seen {
			firsts = append(firsts, first)
		}
		rests[first] = append(rests[first], core.Literal(opener[size:]))
	}

	excluded := string(escape) + string(firsts)
	items := []bytescan.Rule{
		core.Rune(stop.Or(core.RuneSet(excluded)).Not()),
		core.Sequence(core.Literal(string(escape)), core.Rune(core.AnyRune)),
	}
	for _, first := range firsts {
		items = append(items, core.NotFollowedBy(core.Literal(string(first)), core.Choice(rests[first]...)))
	}
	return core.OneOrMore(core.Choice(items...))
}

// QuotedWith returns a rule for a quoted string whose escape sequences must
// match a given rule: quote, then any number of runes of plain other than
// quote and of matches of escape, then quote. In Perl notation it is
// quote(?:[plain]|escape)*quote, with quote taken out of plain.
//
// Use QuotedWith when the language defines its escape sequences and an
// invalid one must not lex as part of a string, as in JSON or Go; Quoted
// accepts an escape rune followed by any rune. plain should not contain the
// rune that starts escape, so that an invalid escape sequence ends the match
// instead of reading as plain text. plain decides whether the string may
// span lines. The JSON string of RFC 8259 is
//
//	QuotedWith('"',
//		core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not(),
//		core.Sequence(core.Literal(`\`), core.Choice(
//			core.Rune(core.RuneSet(`"\/bfnrt`)),
//			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
//		)))
//
// which is "(?:[^"\\\x00-\x1F]|\\["\\/bfnrt]|\\u[0-9A-Fa-f]{4})*".
//
// The rule accepts at the closing quote. When escape can match text that
// starts with quote, the rule reads on after a quote that could also start
// an escape sequence, and ends with the longest string read so far. At end
// of input inside the string, or at a rune that is neither plain nor the
// start of an escape sequence, it matches nothing. It panics if quote is not
// a valid rune, if plain is nil or if escape is nil. plain must give the same
// answer for the same rune and have no side effect (see core.Class).
func QuotedWith(quote rune, plain core.Class, escape bytescan.Rule) bytescan.Rule {
	mustCodeRune("QuotedWith", "quote", quote)
	if plain == nil {
		panic("base.QuotedWith: nil plain class")
	}
	if escape == nil {
		panic("base.QuotedWith: nil escape rule")
	}
	quoteSet := core.RuneSet(string(quote))
	quoteRune := core.Rune(quoteSet)
	return core.Sequence(
		quoteRune,
		core.ZeroOrMore(core.Choice(core.Rune(plain.And(quoteSet.Not())), escape)),
		quoteRune,
	)
}
