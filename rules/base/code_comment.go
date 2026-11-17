package base

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// LineComment returns a rule for a comment from prefix to the end of the
// line: prefix[^\n\r]*, with prefix taken literally. The match excludes the
// line break, so Newline matches it. The rule accepts at the end of prefix
// and after each rune. At end of input it rejects the EOF symbol, so the
// comment read so far stays the match. It panics if prefix is empty or
// contains '\n' or '\r'.
func LineComment(prefix string) bytescan.Rule {
	if prefix == "" {
		panic("base.LineComment: prefix must not be empty")
	}
	mustCodeTextNotLineBreak("LineComment", "prefix", prefix)
	return core.Sequence(core.Literal(prefix), core.ZeroOrMore(core.Rune(codeLineBreak.Not())))
}

// BlockComment returns a rule for a comment between open and close that may
// span lines: open, then any runes, then the first close after open. It is
// RawMultiline(open, close): in Perl notation open(?s:(?:(?!close).)*)close.
// BlockComment("/*", "*/") is /\*[^*]*\*+(?:[^/*][^*]*\*+)*/ and matches all
// of "/***/". Comments do not nest: on "/* /* */ */" it matches
// "/* /* */". The rule accepts at the first close and stops. At end of
// input before close it matches nothing. It panics if open or close is
// empty.
func BlockComment(open, close string) bytescan.Rule {
	mustCodeDelimiters("BlockComment", open, close)
	return codeDelimited(open, close, core.AnyRune)
}
