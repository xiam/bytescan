package base

import (
	"fmt"
	"unicode/utf8"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// Quoted returns a rule for a quoted string on one line: quote, then any
// number of escape sequences and other runes, then quote. An escape sequence
// is escape and the rune after it, so an escaped quote does not end the
// string. No line break may appear in the string, escaped or not;
// QuotedMultiline allows it. When escape equals quote, the escape sequence is
// the doubled quote, as in SQL. A string with no escape is Raw(q, q). Two
// examples, with the regular expression of each:
//
//	Quoted('"', '\\')   "(?:\\[^\n\r]|[^"\\\n\r])*"
//	Quoted('\'', '\'')  '(?:''|[^'\n\r])*'
//
// The rule accepts at the closing quote. When escape equals quote, that quote
// can also start a doubled quote, so the rule reads one more symbol and then
// ends with the longest string read so far: on the input "'a'" + "'" it
// matches "'a'". At end of input inside the string it matches nothing; at end of input
// after a closing quote it ends with the longest string read so far. It
// panics if quote or escape is not a valid rune, or if either is '\n' or
// '\r', because the string could then contain a line break.
func Quoted(quote, escape rune) bytescan.Rule {
	mustCodeRune("Quoted", "quote", quote)
	mustCodeRune("Quoted", "escape", escape)
	mustCodeNotLineBreak("Quoted", "quote", quote)
	mustCodeNotLineBreak("Quoted", "escape", escape)
	return codeQuoted(quote, escape, codeLineBreak.Not())
}

// QuotedMultiline returns a rule for a quoted string that may span lines:
// Quoted with every rune, a line break included, allowed in the string and
// after escape. With quote '"' and escape '\\' it is
// (?s)"(?:\\.|[^"\\])*". It ends at end of input as Quoted does. It panics if
// quote or escape is not a valid rune.
func QuotedMultiline(quote, escape rune) bytescan.Rule {
	mustCodeRune("QuotedMultiline", "quote", quote)
	mustCodeRune("QuotedMultiline", "escape", escape)
	return codeQuoted(quote, escape, core.AnyRune)
}

// codeQuoted returns the rule of Quoted and QuotedMultiline: inside the
// string, a rune of allowed is either part of an escape sequence or any rune
// other than quote and escape.
func codeQuoted(quote, escape rune, allowed core.Class) bytescan.Rule {
	quoteRune := core.Rune(core.RuneSet(string(quote)))
	plain := allowed.And(core.RuneSet(string(quote) + string(escape)).Not())

	var escaped bytescan.Rule
	if escape == quote {
		escaped = core.Literal(string(quote) + string(quote))
	} else {
		escaped = core.Sequence(core.Literal(string(escape)), core.Rune(allowed))
	}

	return core.Sequence(
		quoteRune,
		core.ZeroOrMore(core.Choice(escaped, core.Rune(plain))),
		quoteRune,
	)
}

// Raw returns a rule for raw text on one line: open, then any runes other
// than a line break, then the first close after open. The text has no
// escape, so it cannot contain close. In Perl notation it is
// open(?:(?!close)[^\n\r])*close; Raw("`", "`") is `[^`\n\r]*`. RawMultiline
// allows line breaks.
//
// The rule accepts at the first close and stops. At end of input before
// close, or at a line break before close, it matches nothing. It panics if
// open or close is empty or contains '\n' or '\r', because the text could
// then contain a line break.
func Raw(open, close string) bytescan.Rule {
	mustCodeDelimiters("Raw", open, close)
	mustCodeTextNotLineBreak("Raw", "open", open)
	mustCodeTextNotLineBreak("Raw", "close", close)
	return codeDelimited(open, close, codeLineBreak.Not())
}

// RawMultiline returns a rule for raw text that may span lines: Raw with
// every rune, a line break included, allowed in the text. In Perl notation
// it is open(?s:(?:(?!close).)*)close; RawMultiline("`", "`") is a Go raw
// string literal. It ends at end of input as Raw does. It panics if open or
// close is empty.
func RawMultiline(open, close string) bytescan.Rule {
	mustCodeDelimiters("RawMultiline", open, close)
	return codeDelimited(open, close, core.AnyRune)
}

// codeDelimited returns the rule for open(?:(?!close)[allowed])*close: open,
// then runes of allowed, up to the first close. The match is unique, because
// close must not start at any position before the close that ends it.
//
// The lookahead (?!close) is checked at each position after open and before
// the last rune of the text, as the end of the part before that position:
// either close follows open at once, or open and the runes before the last
// one are each not followed by close, and the last rune is followed by close.
// Each lookahead reads at most len(close) symbols, so the cost stays linear
// in the length of the text.
func codeDelimited(open, close string, allowed core.Class) bytescan.Rule {
	openLit, closeLit := core.Literal(open), core.Literal(close)
	text := core.Rune(allowed)
	return core.Choice(
		core.Sequence(openLit, closeLit),
		core.Sequence(
			core.NotFollowedBy(openLit, closeLit),
			core.ZeroOrMore(core.NotFollowedBy(text, closeLit)),
			text,
			closeLit,
		),
	)
}

// mustCodeRune panics if r is not a valid rune. fn and arg name the caller
// and the argument in the message.
func mustCodeRune(fn, arg string, r rune) {
	if !utf8.ValidRune(r) {
		panic(fmt.Sprintf("base.%s: %s %U is not a valid rune", fn, arg, r))
	}
}

// mustCodeNotLineBreak panics if r is '\n' or '\r'. fn and arg name the
// caller and the argument in the message.
func mustCodeNotLineBreak(fn, arg string, r rune) {
	if codeLineBreak(r) {
		panic(fmt.Sprintf("base.%s: %s %U is a line break; use %sMultiline", fn, arg, r, fn))
	}
}

// mustCodeTextNotLineBreak panics if text contains '\n' or '\r'. fn and arg
// name the caller and the argument in the message.
func mustCodeTextNotLineBreak(fn, arg, text string) {
	for _, r := range text {
		if codeLineBreak(r) {
			panic(fmt.Sprintf("base.%s: %s %q contains a line break", fn, arg, text))
		}
	}
}

// mustCodeDelimiters panics if open or close is empty. fn names the caller in
// the message.
func mustCodeDelimiters(fn, open, close string) {
	if open == "" {
		panic("base." + fn + ": open must not be empty")
	}
	if close == "" {
		panic("base." + fn + ": close must not be empty")
	}
}
