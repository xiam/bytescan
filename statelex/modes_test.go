package statelex

import (
	"errors"
	"fmt"
	"io"
	"math/bits"
	mrand "math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/modedef"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/core"
)

// The rules of these tests are rules of rules/core, so they are pure, and the
// actions are on the rules: there is no switching code.
var (
	mLetters  = core.RuneRun(core.ASCIILetter)
	mSpaces   = core.RuneRun(core.RuneSet(" "))
	mNewline  = core.Literal("\n")
	mQuote    = core.Literal(`"`)
	mNotQuote = core.RuneRun(core.RuneSet(`"`).Not())
)

// ruleSetB returns the modes of rule set B in
// docs/specs/resumable-lexing.md.
func ruleSetB() *modes.ModeSet {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", mLetters).Rule("WS", mSpaces).Rule("NL", mNewline).
		Push("QUOTE", mQuote, "str")
	ms.Mode("str").
		Rule("TEXT", mNotQuote).
		Pop("QUOTE", mQuote)
	return ms
}

// ruleSetNested returns a mode set that pushes two levels: a parenthesis in
// code pushes paren, and a quote in paren pushes str, so the deepest state is
// [code paren str]. A quote in code pushes str too. A ")" in code pops the
// last mode, which does nothing.
func ruleSetNested() *modes.ModeSet {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", mLetters).Rule("WS", mSpaces).Rule("NL", mNewline).
		Push("OPEN", core.Literal("("), "paren").
		Push("QUOTE", mQuote, "str").
		Pop("CLOSE", core.Literal(")"))
	ms.Mode("paren").
		Rule("WORD", mLetters).Rule("WS", mSpaces).Rule("NL", mNewline).
		Push("QUOTE", mQuote, "str").
		Pop("CLOSE", core.Literal(")"))
	ms.Mode("str").
		Rule("TEXT", mNotQuote).
		Pop("QUOTE", mQuote)
	return ms
}

// stTok is a token with the mode state at its start.
type stTok struct {
	Token
	state modes.Stack
}

// stackString writes a mode state in the notation of the specification:
// "[code str]", with the top of the stack last.
func stackString(s modes.Stack) string {
	names := make([]string, s.Len())
	for i := len(names) - 1; i >= 0; i-- {
		names[i] = s.Top()
		s = modedef.PopMode(s).(modes.Stack)
	}
	return "[" + strings.Join(names, " ") + "]"
}

// scanModes returns the tokens of a scan of the whole text from its start by
// a new driver of ms, each with the mode state at its start, and the mode
// state at the end.
func scanModes(t testing.TB, ms *modes.ModeSet, text string) ([]stTok, modes.Stack) {
	t.Helper()
	d, err := ms.Driver(cursor.New(strings.NewReader(text)), modes.Stack{})
	require.NoError(t, err)
	var toks []stTok
	for {
		st := d.Stack()
		tok, err := d.Next()
		if errors.Is(err, io.EOF) {
			return toks, st
		}
		require.NoError(t, err)
		span := tok.Span()
		toks = append(toks, stTok{Token{Type: tok.Type(), Start: span.Start().ByteOffset(), End: span.End().ByteOffset()}, st})
	}
}

// heldStates returns the tokens that the lexer holds, in order, each with the
// mode state saved at its start. It is a test hook.
func heldStates(lx *Lexer) []stTok {
	var toks []stTok
	for it := lx.toks.iter(lx.toks.root, 0); ; {
		n, s := it.next()
		if n == nil {
			return toks
		}
		toks = append(toks, stTok{Token{Type: n.typ, Start: s, End: s + n.length}, n.stack})
	}
}

// tokens drops the mode states.
func tokens(toks []stTok) []Token {
	var out []Token
	for _, tok := range toks {
		out = append(out, tok.Token)
	}
	return out
}

// requireSameStates checks that the lexer holds the tokens of want, with mode
// states equal to those of want, and the mode state end at the end. label
// names the edit in a failure.
func requireSameStates(t *testing.T, lx *Lexer, want []stTok, end modes.Stack, label string) {
	t.Helper()
	got := heldStates(lx)
	require.Equal(t, tokens(want), tokens(got), label)
	for i := range want {
		require.True(t, want[i].state.Equal(got[i].state), "state of token %d: want %s, got %s; %s", i, stackString(want[i].state), stackString(got[i].state), label)
	}
	require.True(t, end.Equal(lx.endSt), "state at the end: want %s, got %s; %s", stackString(end), stackString(lx.endSt), label)
}

// stateList writes tokens in the notation of the rows of rule set B: each
// token is followed by the mode state at its start, read from states.
func stateList(toks []Token, text string, states []stTok) string {
	if len(toks) == 0 {
		return "—"
	}
	at := make(map[int]modes.Stack, len(states))
	for _, s := range states {
		at[s.Start] = s.state
	}
	parts := make([]string, len(toks))
	for i, tok := range toks {
		parts[i] = fmt.Sprintf("%s %s [%d,%d) %s", tok.Type, visible(text[tok.Start:tok.End]), tok.Start, tok.End, stackString(at[tok.Start]))
	}
	return strings.Join(parts, ", ")
}

// ruleSetBCases holds the rows B1 to B4 of rule set B, copied from the
// document. Each token is followed by the mode state at its start.
var ruleSetBCases = []editCase{
	{"B1 delete an opening quote", "say␣\"hi\"␣now␣\"x\"␣end⏎", "delete [4,5)", 3,
		"QUOTE \" [4,5) [code], TEXT hi [5,7) [code str], QUOTE \" [7,8) [code str], WS ␣ [8,9) [code], WORD now [9,12) [code], WS ␣ [12,13) [code], QUOTE \" [13,14) [code], TEXT x [14,15) [code str], QUOTE \" [15,16) [code str], WS ␣ [16,17) [code], WORD end [17,20) [code], NL ⏎ [20,21) [code]",
		"WORD hi [4,6) [code], QUOTE \" [6,7) [code], TEXT ␣now␣ [7,12) [code str], QUOTE \" [12,13) [code str], WORD x [13,14) [code], QUOTE \" [14,15) [code], TEXT ␣end⏎ [15,20) [code str]",
		"none"},
	{"B2 type an opening quote", "say␣hi␣now⏎", "insert \" at 4", 3,
		"WORD hi [4,6) [code], WS ␣ [6,7) [code], WORD now [7,10) [code], NL ⏎ [10,11) [code]",
		"QUOTE \" [4,5) [code], TEXT hi␣now⏎ [5,12) [code str]",
		"none"},
	{"B3 edit inside a string", "say␣\"hi\"␣now⏎", "insert p at 6", 5,
		"TEXT hi [5,7) [code str]",
		"TEXT hpi [5,8) [code str]",
		"new 8 = old 7, QUOTE"},
	{"B4 close a string and open another", "say␣\"hi␣now\"␣end⏎", "insert \"␣\" at 7", 5,
		"TEXT hi␣now [5,11) [code str]",
		"TEXT hi [5,7) [code str], QUOTE \" [7,8) [code str], WS ␣ [8,9) [code], QUOTE \" [9,10) [code], TEXT ␣now [10,14) [code str]",
		"new 14 = old 11, QUOTE"},
}

// TestEditCasesRuleSetB applies the edit of every row of rule set B to a
// lexer that holds the text of the row, and checks the change with the mode
// state at the start of each token, the restart point, the sync point, and
// the tokens and mode states held after the edit.
func TestEditCasesRuleSetB(t *testing.T) {
	require.Len(t, ruleSetBCases, 4)
	for _, c := range ruleSetBCases {
		t.Run(c.name, func(t *testing.T) {
			before := plain(c.before)
			lx, err := New(ruleSetB())
			require.NoError(t, err)
			_, err = lx.Apply(Insert(0, []byte(before)))
			require.NoError(t, err)
			old := heldStates(lx)
			e := parseEdit(t, c.edit)
			after := before[:e.Start] + string(e.Text) + before[e.End:]

			ch, err := lx.Apply(e)
			require.NoError(t, err)
			require.Equal(t, c.removed, stateList(ch.Removed, before, old), "removed")
			require.Equal(t, c.inserted, stateList(ch.Inserted, after, heldStates(lx)), "inserted")
			want, end := scanModes(t, ruleSetB(), after)
			requireSameStates(t, lx, want, end, c.name)
			require.Equal(t, after, heldText(lx))

			run := lx.lastRun
			require.Equal(t, c.restart, run.restart, "restart")
			sync := "none"
			if run.synced {
				sync = fmt.Sprintf("new %d = old %d, %s", run.end, run.syncOld, lx.toks.at(run.end).typ)
			}
			require.Equal(t, c.sync, sync, "sync")
			if !run.synced {
				require.Equal(t, len(after), run.end)
			}
		})
	}
}

// TestModesNotASyncPoint checks case B1: the new QUOTE at [6,7) and the old
// QUOTE at [7,8) are at the same place of the unchanged text and have the same
// type, but the mode state differs, so new offset 6 is not a sync point and
// the re-lex runs to the end of the text.
func TestModesNotASyncPoint(t *testing.T) {
	before := "say \"hi\" now \"x\" end\n"
	lx, err := New(ruleSetB())
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte(before)))
	require.NoError(t, err)
	oldTok := lx.toks.at(7)
	require.NotNil(t, oldTok)

	_, err = lx.Apply(Delete(4, 5))
	require.NoError(t, err)
	newTok := lx.toks.at(6)
	require.NotNil(t, newTok)

	// Same place (6 = 7 - 1) and same type; the mode states differ.
	require.Equal(t, oldTok.typ, newTok.typ)
	require.Equal(t, "[code str]", stackString(oldTok.stack))
	require.Equal(t, "[code]", stackString(newTok.stack))

	run := lx.lastRun
	require.False(t, run.synced, "new offset 6 was taken as a sync point")
	require.Equal(t, len(before)-1, run.end, "the re-lex runs to the end of the text")
	require.Equal(t, 3, run.restart)
}

// modePieces are the pieces of the generated texts of the mode sets: words,
// spaces, newlines, quotes and parentheses that open and close the modes,
// multi-byte characters and broken UTF-8.
var modePieces = []string{
	"say", "hi", "x", " ", "  ", "\n", "\"", "\"", "\"hi\"", "(", "(", ")", "(a)",
	"(\"b c\")", "é", "\xc3", "+",
}

func genModeText(rng *mrand.Rand, maxPieces int) string {
	var sb strings.Builder
	for i, n := 0, rng.Intn(maxPieces+1); i < n; i++ {
		sb.WriteString(modePieces[rng.Intn(len(modePieces))])
	}
	return sb.String()
}

// genModeEdit returns an edit of a text of n bytes, as genEdit does, with new
// bytes made of modePieces.
func genModeEdit(rng *mrand.Rand, n int) Edit {
	e := genEdit(rng, n)
	if len(e.Text) > 0 {
		e.Text = []byte(genModeText(rng, 3))
	}
	return e
}

// modeSets are the mode sets of the generated edits.
var modeSets = []struct {
	name string
	set  func() *modes.ModeSet
}{
	{"B", ruleSetB},
	{"nested", ruleSetNested},
}

// runModeEdits applies generated edits from a fixed seed to generated texts
// under each mode set, and calls check after each accepted edit. An edit that
// splits a character is refused. Each new lexer comes with a new consumer,
// which check may use.
func runModeEdits(t *testing.T, check func(t *testing.T, ms *modes.ModeSet, lx *Lexer, c *consumer, ch Change, before, after string)) {
	rounds := 300
	if testing.Short() {
		rounds = 40
	}
	for _, set := range modeSets {
		t.Run(set.name, func(t *testing.T) {
			ms := set.set()
			rng := mrand.New(mrand.NewSource(130))
			edits, deep := 0, 0
			for round := 0; round < rounds; round++ {
				lx, err := New(ms)
				require.NoError(t, err)
				c := &consumer{}
				text := ""
				if round%2 == 1 {
					text = genModeText(rng, 16)
					ch, err := lx.Apply(Insert(0, []byte(text)))
					require.NoError(t, err)
					check(t, ms, lx, c, ch, "", text)
				}
				for i := 0; i < 20; i++ {
					e := genModeEdit(rng, len(text))
					after := text[:e.Start] + string(e.Text) + text[e.End:]
					var ch Change
					var err error
					if rng.Intn(5) == 0 {
						ch, err = lx.SetText([]byte(after))
					} else {
						ch, err = lx.Apply(e)
						if splitsChar(text, e.Start) || splitsChar(text, e.End) {
							require.ErrorIs(t, err, ErrSplitRune)
							continue
						}
					}
					require.NoError(t, err)
					check(t, ms, lx, c, ch, text, after)
					if lx.toks.at(lx.lastRun.restart) != nil && lx.toks.at(lx.lastRun.restart).stack.Len() > 2 {
						deep++
					}
					text = after
					edits++
				}
			}
			t.Logf("mode set %s: %d edits applied, %d restarted two levels deep", set.name, edits, deep)
			if set.name == "nested" {
				require.Greater(t, deep, 0)
			}
		})
	}
}

// TestGeneratedEditsModes applies many generated edits under rule set B and
// under a mode set that pushes two levels. After each edit the lexer holds
// the tokens of a scan of the whole new text from its start by a new driver,
// with the mode state of that scan at the start of each token and at the end.
func TestGeneratedEditsModes(t *testing.T) {
	runModeEdits(t, func(t *testing.T, ms *modes.ModeSet, lx *Lexer, _ *consumer, ch Change, before, after string) {
		want, end := scanModes(t, ms, after)
		require.Equal(t, after, heldText(lx))
		requireSameStates(t, lx, want, end, fmt.Sprintf("%q -> %q", before, after))
		checkTrees(t, lx)
		require.Equal(t, len(after)-len(before), ch.Shift())
		run := lx.lastRun
		require.LessOrEqual(t, ch.OldEnd, run.syncOld)
		require.LessOrEqual(t, ch.NewEnd, run.end)
		require.GreaterOrEqual(t, ch.Start, run.restart)
	})
}

// TestModesChangeIsEnough is the consumer of TestGeneratedEdits under the mode
// sets: it keeps only a type and a byte range for each token, applies each
// change, and reads nothing else from the lexer. After each edit its list
// equals the tokens of a scan of the whole new text.
func TestModesChangeIsEnough(t *testing.T) {
	runModeEdits(t, func(t *testing.T, ms *modes.ModeSet, _ *Lexer, c *consumer, ch Change, before, after string) {
		want, _ := scanModes(t, ms, after)
		dropped := c.apply(ch)
		require.Equal(t, ch.Removed, dropped, "removed: %q -> %q", before, after)
		require.Equal(t, tokens(want), c.toks, "consumer: %q -> %q", before, after)
	})
}

// TestStartInsidePushedMode joins the restart of a scan in the middle (#126)
// with the saved mode state (#127). For every TEXT token two levels deep, in
// [code paren str], it inserts a letter inside the token. The re-lex restarts
// at the start of that token, inside the pushed modes, and gives the tokens
// and the mode states of a scan of the whole new text from its start.
func TestStartInsidePushedMode(t *testing.T) {
	const text = "a (\"bc de\") f (\"gh\" (\"ij\")) \"kl\"\n"
	ms := ruleSetNested()
	full, _ := scanModes(t, ms, text)
	count := 0
	for _, tok := range full {
		if tok.Type != "TEXT" || tok.state.Len() != 3 || tok.End-tok.Start < 2 {
			continue
		}
		count++
		t.Run(fmt.Sprintf("restart at %d", tok.Start), func(t *testing.T) {
			lx, err := New(ms)
			require.NoError(t, err)
			_, err = lx.Apply(Insert(0, []byte(text)))
			require.NoError(t, err)
			e := Insert(tok.Start+1, []byte("z"))
			after := text[:e.Start] + "z" + text[e.Start:]
			ch, err := lx.Apply(e)
			require.NoError(t, err)

			require.Equal(t, tok.Start, lx.lastRun.restart)
			require.Equal(t, "[code paren str]", stackString(lx.toks.at(tok.Start).stack))
			require.True(t, lx.lastRun.synced)
			require.Equal(t, []Token{tok.Token}, ch.Removed)
			require.Equal(t, []Token{{"TEXT", tok.Start, tok.End + 1}}, ch.Inserted)

			want, end := scanModes(t, ms, after)
			requireSameStates(t, lx, want, end, visible(after))
		})
	}
	require.Equal(t, 3, count)
}

// TestPopOfTheLastMode checks that a pop while only the start mode is on the
// stack does nothing: the state stays [code], and the next token is lexed in
// code.
func TestPopOfTheLastMode(t *testing.T) {
	ms := ruleSetNested()
	lx, err := New(ms)
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte("a b")))
	require.NoError(t, err)
	ch, err := lx.Apply(Insert(1, []byte(")")))
	require.NoError(t, err)
	require.Equal(t, []Token{{"CLOSE", 1, 2}}, ch.Inserted)
	want, end := scanModes(t, ms, "a) b")
	requireSameStates(t, lx, want, end, "a) b")
	for _, tok := range heldStates(lx) {
		require.Equal(t, "[code]", stackString(tok.state))
	}
}

// TestWorkOfASmallEditInModes is TestWorkOfASmallEdit with modes: one
// insertion and one deletion inside a string near the start of texts of
// growing sizes under rule set B. The work counters of the edit grow with the
// logarithm of the size, not with the untouched text after the edit.
func TestWorkOfASmallEditInModes(t *testing.T) {
	const line = "a \"b\" c\n" // 7 tokens
	sizes := []int{1 << 12, 1 << 15, 1 << 18}
	edits := []Edit{Insert(4, []byte("y")), Delete(4, 5)}

	type amounts struct{ textBytes, ropeNodes, tokens, find, run int }
	got := make([][]amounts, len(edits))
	for _, size := range sizes {
		text := strings.Repeat(line, size/len(line))
		lx, err := New(ruleSetB())
		require.NoError(t, err)
		_, err = lx.Apply(Insert(0, []byte(text)))
		require.NoError(t, err)
		ntoks := 7 * (size / len(line))
		for i, e := range edits {
			_, err := lx.Apply(e)
			require.NoError(t, err)

			// The run is the TEXT token of the string, which starts in the
			// state [code str], and it syncs at the closing quote.
			run := lx.lastRun
			require.Equal(t, 3, run.restart)
			require.True(t, run.synced)
			require.Equal(t, "[code str]", stackString(lx.toks.at(run.restart).stack))

			w := lx.work
			a := amounts{w.textBytes, w.ropeNodes, w.tokens, w.find, run.end - run.restart}
			got[i] = append(got[i], a)
			t.Logf("edit %d, %7d bytes, %6d tokens: %+v", i, len(text), ntoks, a)

			require.LessOrEqual(t, w.textBytes, len(e.Text)+(e.End-e.Start)+maxChunk)
			logN := bits.Len(uint(ntoks))
			require.LessOrEqual(t, w.tokens, 40*logN)
			require.LessOrEqual(t, w.ropeNodes, 40*bits.Len(uint(len(text)/maxChunk+1)))
			require.LessOrEqual(t, w.find, 10*logN)
		}
	}
	for i, as := range got {
		first, last := as[0], as[len(as)-1]
		require.Equal(t, first.textBytes, last.textBytes, i)
		require.Equal(t, first.run, last.run, i)
		require.Less(t, last.tokens, 4*first.tokens, i)
		require.Less(t, last.ropeNodes, 4*first.ropeNodes, i)
		require.Less(t, last.find, 4*first.find, i)
	}
}

// TestModesTrimLeavesOutAChangedState checks the trim of a token whose mode
// state differs. In x"" under rule set B, an opening quote typed at 0 flips
// the state of the two quotes after x, and the re-lex runs to the end of the
// text. Each of the two quotes has the same type, the same moved range and
// the same bytes in both texts, so the change leaves it out, as the
// specification states. The lexer holds the new states.
func TestModesTrimLeavesOutAChangedState(t *testing.T) {
	ms := ruleSetB()
	lx, err := New(ms)
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte(`x""`)))
	require.NoError(t, err)
	old := heldStates(lx)
	require.Equal(t, `WORD x [0,1) [code], QUOTE " [1,2) [code], QUOTE " [2,3) [code str]`, stateList(tokens(old), `x""`, old))
	c := &consumer{toks: tokens(old)}

	ch, err := lx.Apply(Insert(0, []byte(`"`)))
	require.NoError(t, err)
	require.False(t, lx.lastRun.synced)
	now := heldStates(lx)
	require.Equal(t, `QUOTE " [0,1) [code], TEXT x [1,2) [code str], QUOTE " [2,3) [code str], QUOTE " [3,4) [code]`, stateList(tokens(now), `"x""`, now))

	require.Equal(t, Change{Start: 0, OldEnd: 1, NewEnd: 2,
		Removed:  []Token{{"WORD", 0, 1}},
		Inserted: []Token{{"QUOTE", 0, 1}, {"TEXT", 1, 2}},
	}, ch)
	want, end := scanModes(t, ms, `"x""`)
	requireSameStates(t, lx, want, end, `"x""`)
	c.apply(ch)
	require.Equal(t, tokens(want), c.toks)
}
