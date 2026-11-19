package statelex

import (
	"errors"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/core"
)

// requireUnchanged checks that the lexer still holds text and its tokens, and
// that a later edit still works.
func requireUnchanged(t *testing.T, rules []rule, lx *Lexer, text string) {
	t.Helper()
	require.Equal(t, text, heldText(lx))
	require.Equal(t, scan(t, rules, text), held(lx))
	require.Equal(t, len(text), lx.Len())
	_, err := lx.Apply(Insert(len(text), []byte("z")))
	require.NoError(t, err)
	require.Equal(t, scan(t, rules, text+"z"), held(lx))
}

// TestApplyErrRange checks that an edit out of range is refused with
// ErrRange, and changes nothing.
func TestApplyErrRange(t *testing.T) {
	const text = "x = 1\n"
	for _, e := range []Edit{
		{Start: -1, End: 0},
		{Start: 3, End: 2},
		{Start: 0, End: 7},
		{Start: 7, End: 7, Text: []byte("a")},
		Delete(-2, -1),
	} {
		lx := newLexer(t, ruleSetA(), text)
		ch, err := lx.Apply(e)
		require.ErrorIs(t, err, ErrRange, "%+v", e)
		require.Equal(t, Change{}, ch)
		requireUnchanged(t, ruleSetA(), lx, text)
	}
}

// TestApplyErrSplitRune checks that an edit whose start or end falls inside a
// valid UTF-8 character is refused with ErrSplitRune, and changes nothing.
func TestApplyErrSplitRune(t *testing.T) {
	const text = "aé€😀b\n" // characters of 2, 3 and 4 bytes at 1, 3 and 6
	for _, e := range []Edit{
		Insert(2, []byte("x")),
		Insert(4, []byte("x")),
		Insert(5, []byte("x")),
		Insert(7, []byte("x")),
		Insert(9, []byte("x")),
		Delete(0, 2),
		Delete(2, 10),
		Delete(4, 6),
		{Start: 1, End: 8, Text: []byte("y")},
	} {
		lx := newLexer(t, ruleSetA(), text)
		ch, err := lx.Apply(e)
		require.ErrorIs(t, err, ErrSplitRune, "%+v", e)
		require.Equal(t, Change{}, ch)
		requireUnchanged(t, ruleSetA(), lx, text)
	}
}

// TestApplyAtCharacterBoundaries checks the edits that are accepted: at the
// boundaries of valid characters, next to broken bytes, and with new bytes
// that are not valid UTF-8.
func TestApplyAtCharacterBoundaries(t *testing.T) {
	cases := []struct {
		name, text string
		edit       Edit
	}{
		{"between characters", "aé€😀b\n", Delete(3, 6)},
		{"replace a character", "aé€😀b\n", Edit{Start: 1, End: 3, Text: []byte("ó")}},
		{"after a broken first byte", "a\xc3b\n", Insert(2, []byte("x"))},
		{"before a continuation byte", "a\xa9b\n", Insert(1, []byte("x"))},
		{"inside broken bytes", "a\xe2\x82b\n", Insert(2, []byte("x"))},
		{"insert broken bytes", "ab\n", Insert(1, []byte("\xc3"))},
		{"insert a first byte before a continuation byte", "a\xa9\n", Insert(1, []byte("\xc3"))},
		{"complete a character", "a\xc3b\n", Insert(2, []byte("\xa9"))},
		{"complete a character at the end", "a\xe2\x82", Insert(3, []byte("\xac"))},
		{"delete a byte that joins broken bytes", "\xc3x\xa9\n", Delete(1, 2)},
		{"break a character by a deletion", "aé\n", Delete(0, 1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lx := newLexer(t, ruleSetA(), c.text)
			cons := &consumer{toks: held(lx)}
			after := c.text[:c.edit.Start] + string(c.edit.Text) + c.text[c.edit.End:]
			ch, err := lx.Apply(c.edit)
			require.NoError(t, err)
			checkChange(t, ruleSetA(), lx, cons, ch, c.text, after)
		})
	}
}

// TestBrokenBytesBeforeTheEdit is the example of the clause for broken bytes
// in step 1: in "ni\xc3o\n", inserting \xb1 at 3 completes the character ñ,
// and the restart is at 0, two tokens before the edit.
func TestBrokenBytesBeforeTheEdit(t *testing.T) {
	letters := []rule{
		{"WORD", core.RuneRun(core.UnicodeTables(unicode.Letter))},
		{"NL", core.Literal("\n")},
	}
	lx := newLexer(t, letters, "ni\xc3o\n")
	require.Equal(t, []Token{{"WORD", 0, 2}, {bytescan.TokenTypeUnknown, 2, 3}, {"WORD", 3, 4}, {"NL", 4, 5}}, held(lx))
	ch, err := lx.Apply(Insert(3, []byte("\xb1")))
	require.NoError(t, err)
	require.Equal(t, 0, lx.lastRun.restart)
	require.Equal(t, "WORD niño [0,5)", tokenList(ch.Inserted, "niño\n"))
	require.Equal(t, scan(t, letters, "niño\n"), held(lx))
}

// TestSetTextWidensToCharacters checks that SetText widens the middle to
// character boundaries of the held text: ñ and ó share their first byte.
func TestSetTextWidensToCharacters(t *testing.T) {
	letters := []rule{{"WORD", core.RuneRun(core.UnicodeTables(unicode.Letter))}, {"WS", core.RuneRun(core.RuneSet(" "))}}
	lx := newLexer(t, letters, "añb c")
	ch, err := lx.SetText([]byte("aób c"))
	require.NoError(t, err)
	require.Equal(t, []Token{{"WORD", 0, 4}}, ch.Removed)
	require.Equal(t, []Token{{"WORD", 0, 4}}, ch.Inserted)
	require.Equal(t, "aób c", heldText(lx))
	require.Equal(t, scan(t, letters, "aób c"), held(lx))

	ch, err = lx.SetText([]byte("aób c"))
	require.NoError(t, err)
	require.Empty(t, ch.Removed)
	require.Empty(t, ch.Inserted)
	require.Equal(t, 0, ch.Shift())
}

// TestInternalError checks that a broken invariant during a re-lex returns an
// error that wraps bytescan.ErrInternal, and that every later call that can
// fail returns the same error. The broken invariant is simulated with a test
// hook.
func TestInternalError(t *testing.T) {
	lx := newLexer(t, ruleSetA(), "x = 1\n")
	lx.testHook = func() error { return errors.New("simulated") }
	_, err := lx.Apply(Insert(0, []byte("y")))
	require.ErrorIs(t, err, bytescan.ErrInternal)
	lx.testHook = nil

	_, err2 := lx.Apply(Insert(0, []byte("z")))
	require.Equal(t, err, err2)
	_, err2 = lx.Apply(Edit{Start: -1})
	require.Equal(t, err, err2)
	_, err2 = lx.SetText([]byte("other"))
	require.Equal(t, err, err2)
}

// TestNewChecksTheModeSet checks the errors of New.
func TestNewChecksTheModeSet(t *testing.T) {
	_, err := New(nil)
	require.Error(t, err)

	// The errors of modes.ModeSet.Driver.
	ms := modes.NewModeSet("code")
	_, err = New(ms)
	require.Error(t, err, "the start mode is not in the set")

	ms = modes.NewModeSet("code")
	ms.Mode("code").Rule("WORD", core.RuneRun(core.ASCIILetter)).Rule("WORD", core.Literal("x"))
	_, err = New(ms)
	require.ErrorIs(t, err, bytescan.ErrDuplicateRule)

	ms = modes.NewModeSet("code")
	ms.Mode("code").Push("Q", core.Literal(`"`), "str")
	_, err = New(ms)
	require.Error(t, err, "a push to a mode that is not in the set")

	// A set with two modes.
	ms = modes.NewModeSet("code")
	ms.Mode("code").Push("Q", core.Literal(`"`), "str")
	ms.Mode("str").Pop("Q", core.Literal(`"`))
	_, err = New(ms)
	require.NoError(t, err)

	// A set with one mode.
	ms = modes.NewModeSet("code")
	ms.Mode("code").Rule("WORD", core.RuneRun(core.ASCIILetter))
	lx, err := New(ms)
	require.NoError(t, err)
	require.Equal(t, 0, lx.Len())

	// A change to the set after New does not change the lexer.
	ms.Mode("code").Rule("NUMBER", core.RuneRun(core.ASCIIDigit))
	_, err = lx.Apply(Insert(0, []byte("ab12")))
	require.NoError(t, err)
	require.Equal(t, []Token{{"WORD", 0, 2}, {bytescan.TokenTypeUnknown, 2, 3}, {bytescan.TokenTypeUnknown, 3, 4}}, held(lx))
}

// TestOneModeStack checks a set with one mode whose rules push and pop that
// mode. The mode state is part of the sync test, so an edit that changes the
// depth of the stack lexes on until the depths agree again.
func TestOneModeStack(t *testing.T) {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Push("OPEN", core.Literal("("), "code").
		Pop("CLOSE", core.Literal(")"))
	lx, err := New(ms)
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte("a(b)c")))
	require.NoError(t, err)
	ch, err := lx.Apply(Delete(1, 2))
	require.NoError(t, err)
	require.Equal(t, []Token{{"WORD", 0, 1}, {"OPEN", 1, 2}, {"WORD", 2, 3}}, ch.Removed)
	require.Equal(t, []Token{{"WORD", 0, 2}}, ch.Inserted)
	require.Equal(t, []Token{{"WORD", 0, 2}, {"CLOSE", 2, 3}, {"WORD", 3, 4}}, held(lx))
	// CLOSE does not sync: before the edit its stack was [code code], and
	// after it [code]. WORD c syncs: its stack is [code] in both texts.
	require.Equal(t, relexRun{restart: 0, end: 3, syncOld: 4, synced: true}, lx.lastRun)
	require.True(t, lx.endSt.Equal(lx.startSt))
}
