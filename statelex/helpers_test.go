package statelex

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// rule is one rule of a rule set without modes, in registration order.
type rule struct {
	typ  bytescan.TokenType
	rule bytescan.Rule
}

// ruleSetA is rule set A of docs/specs/resumable-lexing.md, in its
// registration order. Every rule is built from rules/core and rules/base, so
// it is pure.
func ruleSetA() []rule {
	digits := core.RuneRun(core.ASCIIDigit)
	return []rule{
		{"WORD", core.RuneRun(core.ASCIILetter)},
		{"NUMBER", core.Sequence(digits, core.Optional(core.Sequence(core.Literal("."), digits)))},
		{"OP", core.AnyLiteral("==", "=", "+", "*", "/", ".")},
		{"STRING", core.Sequence(core.Literal(`"`), core.ZeroOrMore(core.Rune(core.RuneSet("\"\n").Not())), core.Literal(`"`))},
		{"COMMENT", base.BlockComment("/*", "*/")},
		{"WS", core.RuneRun(core.RuneSet(" "))},
		{"NL", core.Literal("\n")},
	}
}

// ruleSetFar is a set of pure rules in which rules read far ahead: CALL is a
// word followed by spaces and "(", so it reads the spaces after every word,
// and RAW is "<<" up to the next ">>", across lines, so an unclosed "<<" reads
// to the end of the text.
func ruleSetFar() []rule {
	letters := core.RuneRun(core.ASCIILetter)
	return []rule{
		{"CALL", core.FollowedBy(letters, core.Sequence(core.ZeroOrMore(core.Rune(core.RuneSet(" "))), core.Literal("(")))},
		{"WORD", letters},
		{"NUMBER", core.RuneRun(core.ASCIIDigit)},
		{"RAW", base.RawMultiline("<<", ">>")},
		{"PUNCT", core.AnyLiteral("(", ")", "<", ">", ",")},
		{"WS", core.RuneRun(core.RuneSet(" "))},
		{"NL", core.Literal("\n")},
	}
}

// ruleSetFlags is a set of pure rules in which SHEBANG reads the BOF flag
// ("#!" at the beginning of the text, up to the end of the line) and DIRECTIVE
// reads the BOL flag ("#" at the beginning of a line, up to the end of the
// line). The other rules are those of rule set A.
func ruleSetFlags() []rule {
	return append([]rule{
		{"SHEBANG", atFileStart(base.LineComment("#!"))},
		{"DIRECTIVE", core.AtLineStart(base.LineComment("#"))},
	}, ruleSetA()...)
}

// atFileStart returns a rule that behaves as r when its first symbol has the
// BOF flag, and rejects otherwise. It keeps its progress only in the Rule
// values it returns.
func atFileStart(r bytescan.Rule) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if !s.IsEOF() && s.IsBOF() {
			return r(s)
		}
		return nil, bytescan.StateReject
	}
}

// modeSet returns a set with the one mode "code" that holds rules.
func modeSet(rules []rule) *modes.ModeSet {
	ms := modes.NewModeSet("code")
	m := ms.Mode("code")
	for _, r := range rules {
		m.Rule(r.typ, r.rule)
	}
	return ms
}

// newLexer returns a lexer for rules that holds text.
func newLexer(t testing.TB, rules []rule, text string) *Lexer {
	t.Helper()
	lx, err := New(modeSet(rules))
	require.NoError(t, err)
	if text != "" {
		_, err = lx.Apply(Insert(0, []byte(text)))
		require.NoError(t, err)
	}
	return lx
}

// scan returns the tokens of a scan of the whole text from its start.
func scan(t testing.TB, rules []rule, text string) []Token {
	t.Helper()
	sc := bytescan.New(strings.NewReader(text))
	for _, r := range rules {
		sc.MustAddRule(r.typ, r.rule)
	}
	var toks []Token
	for {
		tok, err := sc.Next()
		if errors.Is(err, io.EOF) {
			return toks
		}
		require.NoError(t, err)
		span := tok.Span()
		toks = append(toks, Token{Type: tok.Type(), Start: span.Start().ByteOffset(), End: span.End().ByteOffset()})
	}
}

// held returns the tokens that the lexer holds, in order, read from the token
// tree. It is a test hook that does not go through the queries.
func held(lx *Lexer) []Token {
	var toks []Token
	for it := lx.toks.iter(lx.toks.root, 0); ; {
		n, s := it.next()
		if n == nil {
			return toks
		}
		toks = append(toks, Token{Type: n.typ, Start: s, End: s + n.length})
	}
}

// heldText returns the text that the lexer holds. It is a test hook.
func heldText(lx *Lexer) string {
	return string(lx.text.read(0, lx.text.len()))
}

// consumer keeps only a list of tokens and applies each change to it. It reads
// nothing from the lexer except the change.
type consumer struct {
	toks []Token
}

// apply drops the tokens in [ch.Start, ch.OldEnd), adds ch.Inserted and
// moves every later token by ch.Shift(). It returns the tokens it dropped.
func (c *consumer) apply(ch Change) (dropped []Token) {
	var out []Token
	i := 0
	for ; i < len(c.toks) && c.toks[i].End <= ch.Start; i++ {
		out = append(out, c.toks[i])
	}
	out = append(out, ch.Inserted...)
	for ; i < len(c.toks) && c.toks[i].Start < ch.OldEnd; i++ {
		dropped = append(dropped, c.toks[i])
	}
	for ; i < len(c.toks); i++ {
		tok := c.toks[i]
		tok.Start += ch.Shift()
		tok.End += ch.Shift()
		out = append(out, tok)
	}
	c.toks = out
	return dropped
}

// visible writes a text in the notation of the specification: ␣ for a space
// and ⏎ for a newline.
func visible(s string) string {
	return strings.NewReplacer(" ", "␣", "\n", "⏎").Replace(s)
}

// plain reverses visible.
func plain(s string) string {
	return strings.NewReplacer("␣", " ", "⏎", "\n").Replace(s)
}

// tokenList writes tokens in the notation of the specification, with their
// bytes from text: "WORD foo [0,3), WS ␣ [3,4)", or "—" for no token.
func tokenList(toks []Token, text string) string {
	if len(toks) == 0 {
		return "—"
	}
	parts := make([]string, len(toks))
	for i, tok := range toks {
		parts[i] = fmt.Sprintf("%s %s [%d,%d)", tok.Type, visible(text[tok.Start:tok.End]), tok.Start, tok.End)
	}
	return strings.Join(parts, ", ")
}
