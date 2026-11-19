package tests

import (
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The token types of rule set A in docs/specs/resumable-lexing.md, and of one
// more rule that reads the BOL flag.
const (
	ruleSetAWord      bytescan.TokenType = "WORD"
	ruleSetANumber    bytescan.TokenType = "NUMBER"
	ruleSetAOp        bytescan.TokenType = "OP"
	ruleSetAString    bytescan.TokenType = "STRING"
	ruleSetAComment   bytescan.TokenType = "COMMENT"
	ruleSetAWS        bytescan.TokenType = "WS"
	ruleSetANL        bytescan.TokenType = "NL"
	ruleSetADirective bytescan.TokenType = "DIRECTIVE"
)

// addRuleSetA registers rule set A of docs/specs/resumable-lexing.md, in its
// registration order, and then DIRECTIVE: a '#' at the beginning of a line,
// up to the end of the line. DIRECTIVE is the rule that reads the BOL flag.
// Every rule is built from rules/core and rules/base, so it keeps its
// progress only in the Rule values it returns.
func addRuleSetA(lx *bytescan.Scanner) {
	addRuleSetAWrapped(lx, func(r bytescan.Rule) bytescan.Rule { return r })
}

// addRuleSetAWrapped registers the rules of addRuleSetA, in the same order,
// each passed through wrap.
func addRuleSetAWrapped(lx *bytescan.Scanner, wrap func(bytescan.Rule) bytescan.Rule) {
	digits := core.RuneRun(core.ASCIIDigit)
	lx.MustAddRule(ruleSetAWord, wrap(core.RuneRun(core.ASCIILetter)))
	lx.MustAddRule(ruleSetANumber, wrap(core.Sequence(digits, core.Optional(core.Sequence(core.Literal("."), digits)))))
	lx.MustAddRule(ruleSetAOp, wrap(core.AnyLiteral("==", "=", "+", "*", "/", ".")))
	lx.MustAddRule(ruleSetAString, wrap(core.Sequence(core.Literal(`"`), core.ZeroOrMore(core.Rune(core.RuneSet("\"\n").Not())), core.Literal(`"`))))
	lx.MustAddRule(ruleSetAComment, wrap(base.BlockComment("/*", "*/")))
	lx.MustAddRule(ruleSetAWS, wrap(core.RuneRun(core.RuneSet(" "))))
	lx.MustAddRule(ruleSetANL, wrap(core.Literal("\n")))
	lx.MustAddRule(ruleSetADirective, wrap(core.AtLineStart(base.LineComment("#"))))
}

// startToken is one token of a text, with its offsets in that text.
type startToken struct {
	typ        bytescan.TokenType
	text       string
	byteOffset int
	runeOffset int
}

// lexRuleSetA lexes text with addRuleSetA and returns every token.
func lexRuleSetA(t *testing.T, text string) []startToken {
	t.Helper()

	lx := bytescan.New(strings.NewReader(text))
	addRuleSetA(lx)

	var toks []startToken
	for {
		tok, err := lx.Next()
		if errors.Is(err, io.EOF) {
			return toks
		}
		require.NoError(t, err, "text %q", text)
		toks = append(toks, startToken{
			typ:        tok.Type(),
			text:       tok.Text(),
			byteOffset: tok.ByteOffset(),
			runeOffset: tok.RuneOffset(),
		})
	}
}

// endingFrom returns the tokens that start at or after the byte offset
// boundary of text, with their offsets counted from the boundary. It reports
// false when no token starts exactly at the boundary.
func endingFrom(toks []startToken, text string, boundary int) ([]string, bool) {
	runeBoundary := utf8.RuneCountInString(text[:boundary])
	var ending []string
	atBoundary := false
	for _, tok := range toks {
		if tok.byteOffset < boundary {
			continue
		}
		if tok.byteOffset == boundary {
			atBoundary = true
		}
		ending = append(ending, fmt.Sprintf("%s %q @%d/%d",
			tok.typ, tok.text, tok.byteOffset-boundary, tok.runeOffset-runeBoundary))
	}
	return ending, atBoundary
}

// tokenStartPieces are the pieces that the generated texts are made of. They
// form every token of rule set A and DIRECTIVE, unclosed strings and comments,
// UNKNOWN runes, and a rune of two bytes.
var tokenStartPieces = []string{
	"a", "foo", "1", "12", ".", "5", "=", "==", "+", "*", "/",
	`"`, `"s"`, "/*", "*/", " ", "\n", "#", "é", "?",
}

func tokenStartText(rng *mrand.Rand, minPieces, maxPieces int) string {
	var sb strings.Builder
	n := minPieces + rng.Intn(maxPieces-minPieces+1)
	for i := 0; i < n; i++ {
		sb.WriteString(tokenStartPieces[rng.Intn(len(tokenStartPieces))])
	}
	return sb.String()
}

// TestTokenDecidedFromItsStart pins the current contract "a token is decided
// from its own start" of docs/specs/resumable-lexing.md. It lexes pairs of
// texts that have different beginnings and the same ending. The ending starts
// at a token boundary that has the same BOF and BOL flags in both texts: both
// beginnings are non-empty, so neither boundary is BOF, and both or neither
// end with a newline. The tokens of the ending must be equal in both texts:
// the type, the text, and the byte and rune offsets counted from the
// boundary. A pair whose boundary is not a token start in both texts is
// skipped.
func TestTokenDecidedFromItsStart(t *testing.T) {
	const (
		seed  = 123
		pairs = 3000
	)
	rng := mrand.New(mrand.NewSource(seed))

	compared := 0
	seen := map[bytescan.TokenType]int{}
	for i := 0; i < pairs; i++ {
		ending := tokenStartText(rng, 1, 10)
		first := tokenStartText(rng, 1, 10)
		second := tokenStartText(rng, 1, 10)
		if first == second || strings.HasSuffix(first, "\n") != strings.HasSuffix(second, "\n") {
			continue
		}

		textA, textB := first+ending, second+ending
		toksA := lexRuleSetA(t, textA)
		endA, okA := endingFrom(toksA, textA, len(first))
		endB, okB := endingFrom(lexRuleSetA(t, textB), textB, len(second))
		if !okA || !okB {
			continue
		}

		require.Equal(t, endA, endB, "ending %q after %q and after %q", ending, first, second)
		compared++
		for _, tok := range toksA {
			if tok.byteOffset >= len(first) {
				seen[tok.typ]++
			}
		}
	}

	// The generated pairs must exercise the statement: enough pairs, and an
	// ending token of every type.
	require.GreaterOrEqual(t, compared, 500, "pairs compared")
	for _, typ := range []bytescan.TokenType{
		ruleSetAWord, ruleSetANumber, ruleSetAOp, ruleSetAString, ruleSetAComment,
		ruleSetAWS, ruleSetANL, ruleSetADirective, bytescan.TokenTypeUnknown,
	} {
		require.Positive(t, seen[typ], "no ending token of type %s", typ)
	}
}

// TestTokenDecidedFromItsStartBOLException pins the exception that the
// statement names: the text before a token reaches a rule through the BOL
// flag. The ending "# a" follows a newline in one text and a space in the
// other, so the BOL flag at the boundary differs, and DIRECTIVE matches only
// in the first text.
func TestTokenDecidedFromItsStartBOLException(t *testing.T) {
	const ending = "# a"
	for _, tc := range []struct {
		first, second string
	}{
		{"x\n", "x "},
		{"\n", "/"},
		{"a\n\n", "a\n "},
	} {
		textA, textB := tc.first+ending, tc.second+ending
		endA, okA := endingFrom(lexRuleSetA(t, textA), textA, len(tc.first))
		endB, okB := endingFrom(lexRuleSetA(t, textB), textB, len(tc.second))
		require.True(t, okA && okB, "the boundary is a token start in %q and in %q", textA, textB)

		require.Equal(t, []string{`DIRECTIVE "# a" @0/0`}, endA, "after %q", tc.first)
		require.Equal(t, []string{`UNKNOWN "#" @0/0`, `WS " " @1/1`, `WORD "a" @2/2`}, endB, "after %q", tc.second)
	}
}
