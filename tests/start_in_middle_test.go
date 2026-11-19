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
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
)

// ruleSetAShebang is the rule that reads the BOF flag: "#!" at the beginning
// of the file, up to the end of the line.
const ruleSetAShebang bytescan.TokenType = "SHEBANG"

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

// restartFlags is the BOF and BOL flags of one symbol.
type restartFlags struct {
	bof, bol bool
}

// restartToken is one token with its four coordinates.
type restartToken struct {
	typ                               bytescan.TokenType
	text                              string
	byteOffset, runeOffset, line, col int
}

func (tok restartToken) String() string {
	return fmt.Sprintf("%s %q @%d/%d %d:%d", tok.typ, tok.text, tok.byteOffset, tok.runeOffset, tok.line, tok.col)
}

// restartScanner is a scanner with SHEBANG, rule set A and DIRECTIVE. Its
// first rule is a probe that records the flags of the first symbol of every
// token, including the EOF symbol that ends the scan, and then rejects. The
// probe captures a variable, so it is not pure; it changes no token.
type restartScanner struct {
	sc    *bytescan.Scanner
	flags []restartFlags
}

func newRestartScanner(c bytescan.Cursor) *restartScanner {
	rs := &restartScanner{sc: bytescan.NewWithCursor(c)}
	rs.sc.MustAddRule("PROBE", func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		rs.flags = append(rs.flags, restartFlags{bof: s.IsBOF(), bol: s.IsBOL()})
		return nil, bytescan.StateReject
	})
	rs.sc.MustAddRule(ruleSetAShebang, atFileStart(base.LineComment("#!")))
	addRuleSetA(rs.sc)
	return rs
}

// lex returns every token up to io.EOF and the probe's flags for this scan.
func (rs *restartScanner) lex(t *testing.T) ([]restartToken, []restartFlags) {
	t.Helper()

	rs.flags = nil
	var toks []restartToken
	for {
		tok, err := rs.sc.Next()
		if errors.Is(err, io.EOF) {
			return toks, rs.flags
		}
		require.NoError(t, err)
		toks = append(toks, restartToken{
			typ: tok.Type(), text: tok.Text(),
			byteOffset: tok.ByteOffset(), runeOffset: tok.RuneOffset(),
			line: tok.Line(), col: tok.Column(),
		})
	}
}

// startOf returns the Start of the text from the byte offset p, as the
// specification computes it.
func startOf(text string, p int) cursor.Start {
	return cursor.Start{InText: p > 0, LineStart: p == 0 || text[p-1] == '\n'}
}

// fromRestart converts the coordinates of a token of the whole text into the
// coordinates that a scan started at byte offset p reports: offsets from p,
// lines from the line of p, and columns from the column of p on that line.
func fromRestart(text string, p int, tok restartToken) restartToken {
	before := text[:p]
	line := 1 + strings.Count(before, "\n")
	col := utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:])

	tok.byteOffset -= p
	tok.runeOffset -= utf8.RuneCountInString(before)
	if tok.line == line {
		tok.col -= col
	}
	tok.line -= line - 1
	return tok
}

// restartTexts returns the texts of the restart tests: fixed texts with several
// lines, multi-byte runes, every rule of rule set A, DIRECTIVE and SHEBANG, and
// texts generated from tokenStartPieces.
func restartTexts() []string {
	texts := []string{
		"",
		"#!run x\nfoo = 1.5\n",
		"#! a\n#!b\n# c\n",
		"x == \"s\" /* a\nb */ y\n# dir\n  # no\n",
		"héllo wörld\n世界 = 12.5 + 3\n\"é\" /* ü */\n",
		"foo bar\n\n\nbaz",
		"\n#x\n",
		"/* open\n\" open\nend",
	}
	rng := mrand.New(mrand.NewSource(126))
	for i := 0; i < 300; i++ {
		texts = append(texts, tokenStartText(rng, 1, 14))
	}
	return texts
}

// TestStartAtEveryBoundary pins "Starting a scan in the middle" of
// docs/specs/resumable-lexing.md. For every token boundary p of each text, and
// for the end of the text, a scan started at p with Reader.ResetStart gives the
// tokens that the scan of the whole text gives from p on: the same types and
// texts, and the coordinates counted from p. The first symbol of every token,
// and the EOF symbol, have the same BOF and BOL flags in both scans. One
// cursor and one scanner serve every restart of a text, so the test also pins
// that a scanner keeps working after its cursor is reset.
func TestStartAtEveryBoundary(t *testing.T) {
	seen := map[bytescan.TokenType]int{}
	restarts := 0

	for _, text := range restartTexts() {
		full, fullFlags := newRestartScanner(cursor.New(strings.NewReader(text))).lex(t)
		require.Len(t, fullFlags, len(full)+1, "text %q", text)

		cur := cursor.New(strings.NewReader(""))
		rs := newRestartScanner(cur)

		for k := 0; k <= len(full); k++ {
			p := len(text)
			if k < len(full) {
				p = full[k].byteOffset
			}

			require.NoError(t, cur.ResetStart(strings.NewReader(text[p:]), startOf(text, p)))
			got, gotFlags := rs.lex(t)

			want := make([]string, 0, len(full)-k)
			for _, tok := range full[k:] {
				want = append(want, fromRestart(text, p, tok).String())
				seen[tok.typ]++
			}
			gotStr := make([]string, 0, len(got))
			for _, tok := range got {
				gotStr = append(gotStr, tok.String())
			}
			require.Equal(t, want, gotStr, "text %q, start %d", text, p)
			require.Equal(t, fullFlags[k:], gotFlags, "flags: text %q, start %d", text, p)
			restarts++
		}
	}

	require.GreaterOrEqual(t, restarts, 2000, "restarts compared")
	for _, typ := range []bytescan.TokenType{
		ruleSetAWord, ruleSetANumber, ruleSetAOp, ruleSetAString, ruleSetAComment,
		ruleSetAWS, ruleSetANL, ruleSetADirective, ruleSetAShebang, bytescan.TokenTypeUnknown,
	} {
		require.Positive(t, seen[typ], "no restarted token of type %s", typ)
	}
}

// TestStartFlags pins the BOF and BOL flags of the first symbol of a restarted
// scan. A start in the middle of a line has no BOL flag; a start after a
// newline has the BOL flag; a start that is not offset 0 has no BOF flag; and
// offset 0 has both. Each case equals what the scan of the whole text presents
// at the same position, and the rules that read the flags (SHEBANG, DIRECTIVE)
// give the same first token.
func TestStartFlags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		p     int
		flags restartFlags
		first string
	}{
		{"offset 0", "#!a\n", 0, restartFlags{bof: true, bol: true}, `SHEBANG "#!a" @0/0 1:0`},
		{"middle of a line", "x #!a", 2, restartFlags{}, `UNKNOWN "#" @0/0 1:0`},
		{"after a newline", "x\n#!a", 2, restartFlags{bol: true}, `DIRECTIVE "#!a" @0/0 1:0`},
		{"after a multi-byte rune", "é#", 2, restartFlags{}, `UNKNOWN "#" @0/0 1:0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full, fullFlags := newRestartScanner(cursor.New(strings.NewReader(tc.text))).lex(t)
			k := 0
			for full[k].byteOffset != tc.p {
				k++
			}
			require.Equal(t, tc.flags, fullFlags[k], "the scan of the whole text")

			cur := cursor.New(strings.NewReader(""))
			require.NoError(t, cur.ResetStart(strings.NewReader(tc.text[tc.p:]), startOf(tc.text, tc.p)))
			got, gotFlags := newRestartScanner(cur).lex(t)
			require.Equal(t, tc.flags, gotFlags[0])
			require.Equal(t, tc.first, got[0].String())
			require.Equal(t, fromRestart(tc.text, tc.p, full[k]).String(), got[0].String())
		})
	}
}

// TestStartFlagsAreUsedForTheFirstSymbolOnly pins that the start flags reach
// only the symbol at offset 0. A start in the middle of a line that is followed
// by a newline gives the next line the BOL flag from its position, and no later
// symbol has the BOF flag.
func TestStartFlagsAreUsedForTheFirstSymbolOnly(t *testing.T) {
	cur := cursor.New(strings.NewReader(""))
	require.NoError(t, cur.ResetStart(strings.NewReader("a\n#!x"), cursor.Start{InText: true}))
	got, flags := newRestartScanner(cur).lex(t)

	require.Equal(t, []string{`WORD "a" @0/0 1:0`, `NL "\n" @1/1 1:1`, `DIRECTIVE "#!x" @2/2 2:0`}, []string{got[0].String(), got[1].String(), got[2].String()})
	require.Equal(t, []restartFlags{{}, {}, {bol: true}, {}}, flags)
}

// TestStartNotAtABoundary pins a start that is not a token boundary. The scan
// gives the tokens of the text from p, with the flags of the start: they equal
// the tokens that follow a one-rune token with the same effect on the flags. A
// '\n' before the start gives the BOL flag, and an unmatched '?' does not.
// Every rune boundary of every text is a start here.
func TestStartNotAtABoundary(t *testing.T) {
	starts := 0
	for _, text := range restartTexts()[:80] {
		for p := 1; p <= len(text); p++ {
			if p < len(text) && !utf8.RuneStart(text[p]) {
				continue
			}

			s := startOf(text, p)
			prefix := "?"
			if s.LineStart {
				prefix = "\n"
			}
			oracleText := prefix + text[p:]
			oracle, _ := newRestartScanner(cursor.New(strings.NewReader(oracleText))).lex(t)
			var want []string
			for _, tok := range oracle[1:] {
				want = append(want, fmt.Sprintf("%s %q @%d/%d", tok.typ, tok.text, tok.byteOffset-1, tok.runeOffset-1))
			}

			cur := cursor.New(strings.NewReader(""))
			require.NoError(t, cur.ResetStart(strings.NewReader(text[p:]), s))
			got, _ := newRestartScanner(cur).lex(t)
			var gotStr []string
			for _, tok := range got {
				gotStr = append(gotStr, fmt.Sprintf("%s %q @%d/%d", tok.typ, tok.text, tok.byteOffset, tok.runeOffset))
			}
			require.Equal(t, want, gotStr, "text %q, start %d", text, p)
			starts++
		}
	}
	require.GreaterOrEqual(t, starts, 500, "starts compared")

	// One case by hand: a start inside a word gives the rest of the word.
	cur := cursor.New(strings.NewReader(""))
	require.NoError(t, cur.ResetStart(strings.NewReader("bar baz"), cursor.Start{InText: true}))
	got, _ := newRestartScanner(cur).lex(t)
	require.Equal(t, `WORD "bar" @0/0 1:0`, got[0].String())
}

// TestStartAtOrAfterTheEnd pins a start at or after the end of a text: the
// source is empty, Next returns io.EOF at once, and the EOF symbol has the
// flags of the start.
func TestStartAtOrAfterTheEnd(t *testing.T) {
	for _, s := range []cursor.Start{{InText: true}, {InText: true, LineStart: true}} {
		cur := cursor.New(strings.NewReader("abc"))
		require.NoError(t, cur.ResetStart(strings.NewReader(""), s))
		got, flags := newRestartScanner(cur).lex(t)
		require.Empty(t, got)
		require.Equal(t, []restartFlags{{bof: false, bol: s.LineStart}}, flags)
	}
}

// TestStartAfterMoreInput pins that a scanner that stopped with
// bytescan.ErrMoreInput, in the middle of a token, scans a new source after
// ResetStart. The unfinished token of the old source is dropped with it: the
// first token of the new source starts at offset 0 and has the flags of the
// start.
func TestStartAfterMoreInput(t *testing.T) {
	src := &feedSource{}
	src.feed("foo \"open")
	cur := cursor.New(src)
	rs := newRestartScanner(cur)

	tok, err := rs.sc.Next()
	require.NoError(t, err)
	require.Equal(t, "foo", tok.Text())
	tok, err = rs.sc.Next()
	require.NoError(t, err)
	require.Equal(t, " ", tok.Text())
	_, err = rs.sc.Next()
	require.ErrorIs(t, err, bytescan.ErrMoreInput)

	require.NoError(t, cur.ResetStart(strings.NewReader("#!x\nbar"), cursor.Start{InText: true, LineStart: true}))
	got, flags := rs.lex(t)
	var gotStr []string
	for _, tok := range got {
		gotStr = append(gotStr, tok.String())
	}
	require.Equal(t, []string{`DIRECTIVE "#!x" @0/0 1:0`, `NL "\n" @3/3 1:3`, `WORD "bar" @4/4 2:0`}, gotStr)
	require.Equal(t, restartFlags{bol: true}, flags[0])
}
