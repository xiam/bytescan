package base

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// codeHorizontalSpace is [\t\v\f ]: the ASCII whitespace runes that do not
// break a line.
var codeHorizontalSpace = core.RuneSet("\t\v\f ")

// codeLineBreak is [\n\r]: the runes that start a line break.
var codeLineBreak = core.RuneSet("\n\r")

// Whitespace returns a rule for a run of horizontal whitespace: [\t\v\f ]+.
// A line break is not whitespace here; Newline matches it. The rule accepts
// after each rune. At end of input it rejects the EOF symbol, so the run read
// so far stays the match.
func Whitespace() bytescan.Rule {
	return core.RuneRun(codeHorizontalSpace)
}

// Newline returns a rule for one line break: \r\n|\n|\r. "\r\n" is one
// token, and "\n\r" is two. On "\r" the rule reads one more symbol to decide
// whether it is "\r\n"; at end of input after "\r" it rejects the EOF symbol,
// so "\r" stays the match.
//
// The scanner computes the line and column of each token, and Newline does not
// change them: the cursor starts a line after each '\n'. So the token after
// "\r\n" or "\n" starts on the next line at column 0, and the token after a
// lone "\r" stays on the same line, one column after the "\r".
func Newline() bytescan.Rule {
	return core.AnyLiteral("\r\n", "\n", "\r")
}
