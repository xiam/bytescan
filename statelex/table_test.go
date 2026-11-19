package statelex

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// editCase is one row of the edit cases of rule set A in
// docs/specs/resumable-lexing.md, in the notation of the document: ␣ is a
// space, ⏎ is a newline, a token is "TYPE text [start,end)", and — is no
// token.
type editCase struct {
	name     string
	before   string
	edit     string // "delete [a,b)", "insert X at k" or "replace [a,b) with X"
	restart  int
	removed  string
	inserted string
	sync     string // "new p = old q, TYPE", or "none"
}

// ruleSetACases holds every row of rule set A: A1 to A35, the ten backspaces
// and the one deletion. Rows are in the order of the document.
var ruleSetACases = []editCase{
	// Deleting.
	{"A1 join two words", "foo␣bar⏎", "delete [3,4)", 0, "WORD foo [0,3), WS ␣ [3,4), WORD bar [4,7)", "WORD foobar [0,6)", "new 6 = old 7, NL"},
	{"A2 join two operators", "a␣=␣=␣b⏎", "delete [3,4)", 2, "OP = [2,3), WS ␣ [3,4), OP = [4,5)", "OP == [2,4)", "new 4 = old 5, WS"},
	{"A3 join a number", "12␣.5⏎", "delete [2,3)", 0, "NUMBER 12 [0,2), WS ␣ [2,3), OP . [3,4), NUMBER 5 [4,5)", "NUMBER 12.5 [0,4)", "new 4 = old 5, NL"},
	{"A4 delete a word between spaces", "one␣two␣three⏎", "delete [4,7)", 3, "WS ␣ [3,4), WORD two [4,7), WS ␣ [7,8)", "WS ␣␣ [3,5)", "new 5 = old 8, WORD"},
	{"A5 delete inside a token", "hello␣world⏎", "delete [2,4)", 0, "WORD hello [0,5)", "WORD heo [0,3)", "new 3 = old 5, WS"},
	{"A6 delete a selection across tokens", "a␣=␣b␣+␣c␣*␣d⏎", "delete [5,9)", 4, "WS ␣ [5,6), OP + [6,7), WS ␣ [7,8), WORD c [8,9)", "—", "new 5 = old 9, WS"},
	{"A7 delete a chunk at the beginning", "x␣=␣1⏎y␣=␣2⏎z␣=␣3⏎", "delete [0,6)", 0, "WORD x [0,1), WS ␣ [1,2), OP = [2,3), WS ␣ [3,4), NUMBER 1 [4,5), NL ⏎ [5,6)", "—", "new 1 = old 7, WS"},
	{"A26 join two lines", "a␣=␣1⏎b␣=␣2⏎", "delete [5,6)", 4, "NL ⏎ [5,6)", "—", "new 6 = old 7, WS"},
	{"A34 delete everything", "x␣=␣1⏎", "delete [0,6)", 0, "WORD x [0,1), WS ␣ [1,2), OP = [2,3), WS ␣ [3,4), NUMBER 1 [4,5), NL ⏎ [5,6)", "—", "none"},
	// Inserting.
	{"A9 insert inside a word", "ab␣cd⏎", "insert X at 1", 0, "WORD ab [0,2)", "WORD aXb [0,3)", "new 3 = old 2, WS"},
	{"A10 an insertion splits a token", "foobar⏎", "insert ␣ at 3", 0, "WORD foobar [0,6)", "WORD foo [0,3), WS ␣ [3,4), WORD bar [4,7)", "new 7 = old 6, NL"},
	{"A11 insert new tokens between tokens", "a␣b⏎", "insert +␣ at 2", 1, "—", "OP + [2,3), WS ␣ [3,4)", "new 4 = old 2, WORD"},
	{"A27 split a line", "a␣=␣1␣b␣=␣2⏎", "replace [5,6) with ⏎", 4, "WS ␣ [5,6)", "NL ⏎ [5,6)", "new 7 = old 7, WS"},
	{"A29 insert a line at the beginning", "b␣=␣2⏎", "insert a␣=␣1⏎ at 0", 0, "—", "WORD a [0,1), WS ␣ [1,2), OP = [2,3), WS ␣ [3,4), NUMBER 1 [4,5), NL ⏎ [5,6)", "new 7 = old 1, WS"},
	{"A30 append a line at the end", "a␣=␣1⏎", "insert b␣=␣2⏎ at 6", 6, "—", "WORD b [6,7), WS ␣ [7,8), OP = [8,9), WS ␣ [9,10), NUMBER 2 [10,11), NL ⏎ [11,12)", "none"},
	{"A35 load a text into an empty lexer", "", "insert x␣=␣1⏎ at 0", 0, "—", "WORD x [0,1), WS ␣ [1,2), OP = [2,3), WS ␣ [3,4), NUMBER 1 [4,5), NL ⏎ [5,6)", "none"},
	// Replacing.
	{"A8 select all, then paste", "x␣=␣1⏎y␣=␣2⏎", "replace [0,12) with say␣\"hi\"⏎", 0, "WORD x [0,1), WS ␣ [1,2), OP = [2,3), WS ␣ [3,4), NUMBER 1 [4,5), NL ⏎ [5,6), WORD y [6,7), WS ␣ [7,8), OP = [8,9), WS ␣ [9,10), NUMBER 2 [10,11)", "WORD say [0,3), WS ␣ [3,4), STRING \"hi\" [4,8)", "none"},
	{"A12 a token that read far ahead", "12.x⏎", "replace [3,4) with 5", 0, "NUMBER 12 [0,2), OP . [2,3), WORD x [3,4)", "NUMBER 12.5 [0,4)", "new 4 = old 4, NL"},
	{"A13 replace a selection", "a␣=␣b␣+␣c⏎", "replace [4,9) with 12", 3, "WORD b [4,5), WS ␣ [5,6), OP + [6,7), WS ␣ [7,8), WORD c [8,9)", "NUMBER 12 [4,6)", "new 6 = old 9, NL"},
	{"A24 replace a word with itself", "foo␣bar⏎", "replace [4,7) with bar", 3, "—", "—", "new 7 = old 7, NL"},
	{"A25 same shape, new bytes", "foo␣bar⏎", "replace [4,7) with baz", 3, "WORD bar [4,7)", "WORD baz [4,7)", "new 7 = old 7, NL"},
	// Strings and comments.
	{"A14 type an opening quote", "x␣=␣hi␣there⏎", "insert \" at 4", 3, "—", "UNKNOWN \" [4,5)", "new 5 = old 4, WORD"},
	{"A15 type the closing quote", "x␣=␣\"hi␣there⏎", "insert \" at 13", 4, "UNKNOWN \" [4,5), WORD hi [5,7), WS ␣ [7,8), WORD there [8,13)", "STRING \"hi␣there\" [4,14)", "new 14 = old 13, NL"},
	{"A16 delete the closing quote", "x␣=␣\"hi␣there\"␣+␣y⏎", "delete [13,14)", 4, "STRING \"hi␣there\" [4,14)", "UNKNOWN \" [4,5), WORD hi [5,7), WS ␣ [7,8), WORD there [8,13)", "new 13 = old 14, WS"},
	{"A17 delete the opening quote", "x␣=␣\"hi␣there\"␣+␣y⏎", "delete [4,5)", 3, "STRING \"hi␣there\" [4,14)", "WORD hi [4,6), WS ␣ [6,7), WORD there [7,12), UNKNOWN \" [12,13)", "new 13 = old 14, WS"},
	{"A18 delete the first of two comment ends", "a␣/*␣x␣*/␣b␣/*␣y␣*/␣c⏎", "delete [7,9)", 2, "COMMENT /*␣x␣*/ [2,9), WS ␣ [9,10), WORD b [10,11), WS ␣ [11,12), COMMENT /*␣y␣*/ [12,19)", "COMMENT /*␣x␣␣b␣/*␣y␣*/ [2,17)", "new 17 = old 19, WS"},
	{"A19 delete the only comment end", "a␣/*␣x␣*/␣b⏎c⏎", "delete [7,9)", 2, "COMMENT /*␣x␣*/ [2,9), WS ␣ [9,10)", "OP / [2,3), OP * [3,4), WS ␣ [4,5), WORD x [5,6), WS ␣␣ [6,8)", "new 8 = old 10, WORD"},
	{"A20 delete the comment start", "a␣/*␣x␣*/␣b⏎", "delete [2,4)", 1, "WS ␣ [1,2), COMMENT /*␣x␣*/ [2,9)", "WS ␣␣ [1,3), WORD x [3,4), WS ␣ [4,5), OP * [5,6), OP / [6,7)", "new 7 = old 9, WS"},
	{"A21 type a comment start before an existing end", "a␣x␣*/␣b⏎c⏎", "insert /*␣ at 2", 1, "WORD x [2,3), WS ␣ [3,4), OP * [4,5), OP / [5,6)", "COMMENT /*␣x␣*/ [2,9)", "new 9 = old 6, WS"},
	{"A22 type a comment start with no end", "a␣x␣b⏎c⏎", "insert /*␣ at 2", 1, "—", "OP / [2,3), OP * [3,4), WS ␣ [4,5)", "new 5 = old 2, WORD"},
	{"A31 edit after an unclosed comment start", "a␣/*␣x⏎b⏎", "insert z at 8", 2, "WORD b [7,8)", "WORD bz [7,9)", "new 9 = old 8, NL"},
	{"A32 close the comment at the end", "a␣/*␣x⏎b⏎", "insert ␣*/ at 8", 2, "OP / [2,3), OP * [3,4), WS ␣ [4,5), WORD x [5,6), NL ⏎ [6,7), WORD b [7,8)", "COMMENT /*␣x⏎b␣*/ [2,11)", "new 11 = old 8, NL"},
	// The end of the text.
	{"A23 type at the end, no final newline", "foo", "insert d at 3", 0, "WORD foo [0,3)", "WORD food [0,4)", "none"},
	{"A28 type before the final newline", "x␣=␣fo⏎", "insert o at 6", 4, "WORD fo [4,6)", "WORD foo [4,7)", "new 7 = old 6, NL"},
	{"A33 close a string at the very end", "x␣=␣\"ab", "insert \" at 7", 4, "UNKNOWN \" [4,5), WORD ab [5,7)", "STRING \"ab\" [4,8)", "none"},
	// Ten backspaces.
	{"backspace 1", "total␣=␣alpha␣+␣beta⏎", "delete [12,13)", 8, "WORD alpha [8,13)", "WORD alph [8,12)", "new 12 = old 13, WS"},
	{"backspace 2", "total␣=␣alph␣+␣beta⏎", "delete [11,12)", 8, "WORD alph [8,12)", "WORD alp [8,11)", "new 11 = old 12, WS"},
	{"backspace 3", "total␣=␣alp␣+␣beta⏎", "delete [10,11)", 8, "WORD alp [8,11)", "WORD al [8,10)", "new 10 = old 11, WS"},
	{"backspace 4", "total␣=␣al␣+␣beta⏎", "delete [9,10)", 8, "WORD al [8,10)", "WORD a [8,9)", "new 9 = old 10, WS"},
	{"backspace 5", "total␣=␣a␣+␣beta⏎", "delete [8,9)", 7, "WS ␣ [7,8), WORD a [8,9), WS ␣ [9,10)", "WS ␣␣ [7,9)", "new 9 = old 10, OP"},
	{"backspace 6", "total␣=␣␣+␣beta⏎", "delete [7,8)", 6, "WS ␣␣ [7,9)", "WS ␣ [7,8)", "new 8 = old 9, OP"},
	{"backspace 7", "total␣=␣+␣beta⏎", "delete [6,7)", 5, "WS ␣ [5,6), OP = [6,7), WS ␣ [7,8)", "WS ␣␣ [5,7)", "new 7 = old 8, OP"},
	{"backspace 8", "total␣␣+␣beta⏎", "delete [5,6)", 0, "WS ␣␣ [5,7)", "WS ␣ [5,6)", "new 6 = old 7, OP"},
	{"backspace 9", "total␣+␣beta⏎", "delete [4,5)", 0, "WORD total [0,5)", "WORD tota [0,4)", "new 4 = old 5, WS"},
	{"backspace 10", "tota␣+␣beta⏎", "delete [3,4)", 0, "WORD tota [0,4)", "WORD tot [0,3)", "new 3 = old 4, WS"},
	{"one deletion", "total␣=␣alpha␣+␣beta⏎", "delete [3,13)", 0, "WORD total [0,5), WS ␣ [5,6), OP = [6,7), WS ␣ [7,8), WORD alpha [8,13)", "WORD tot [0,3)", "new 3 = old 13, WS"},
}

// parseEdit reads an edit in the notation of the document.
func parseEdit(t *testing.T, s string) Edit {
	t.Helper()
	f := strings.Fields(s)
	rng := func(r string) (int, int) {
		a, b, ok := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(r, "["), ")"), ",")
		require.True(t, ok, s)
		x, err := strconv.Atoi(a)
		require.NoError(t, err)
		y, err := strconv.Atoi(b)
		require.NoError(t, err)
		return x, y
	}
	switch {
	case len(f) == 2 && f[0] == "delete":
		a, b := rng(f[1])
		return Delete(a, b)
	case len(f) == 4 && f[0] == "insert" && f[2] == "at":
		at, err := strconv.Atoi(f[3])
		require.NoError(t, err)
		return Insert(at, []byte(plain(f[1])))
	case len(f) == 4 && f[0] == "replace" && f[2] == "with":
		a, b := rng(f[1])
		return Edit{Start: a, End: b, Text: []byte(plain(f[3]))}
	}
	t.Fatalf("bad edit %q", s)
	return Edit{}
}

// TestEditCasesRuleSetA applies the edit of every row of rule set A to a
// lexer that holds the text of the row, and checks the change, the restart
// point, the sync point and the tokens held after the edit.
func TestEditCasesRuleSetA(t *testing.T) {
	require.Len(t, ruleSetACases, 46) // A1 to A35, ten backspaces, one deletion
	for _, c := range ruleSetACases {
		t.Run(c.name, func(t *testing.T) {
			before := plain(c.before)
			lx := newLexer(t, ruleSetA(), before)
			e := parseEdit(t, c.edit)
			after := before[:e.Start] + string(e.Text) + before[e.End:]

			ch, err := lx.Apply(e)
			require.NoError(t, err)
			require.Equal(t, c.removed, tokenList(ch.Removed, before), "removed")
			require.Equal(t, c.inserted, tokenList(ch.Inserted, after), "inserted")
			require.Equal(t, scan(t, ruleSetA(), after), held(lx), "held tokens")
			require.Equal(t, after, heldText(lx))

			// The work of re-lexing: the run starts at the restart point of
			// the row and ends at its sync point.
			run := lx.lastRun
			require.Equal(t, c.restart, run.restart, "restart")
			sync := "none"
			if run.synced {
				typ := lx.toks.at(run.end).typ
				sync = fmt.Sprintf("new %d = old %d, %s", run.end, run.syncOld, typ)
			}
			require.Equal(t, c.sync, sync, "sync")
			if !run.synced {
				require.Equal(t, len(after), run.end)
			}
			if strings.HasPrefix(c.name, "backspace") {
				require.Less(t, run.end-run.restart, len(after), "the run is shorter than the text")
			}

			// Change.Start, OldEnd and NewEnd bound the tokens of the change.
			if len(ch.Removed) > 0 {
				require.Equal(t, ch.Removed[0].Start, ch.Start)
				require.Equal(t, ch.Removed[len(ch.Removed)-1].End, ch.OldEnd)
			} else {
				require.Equal(t, ch.Start, ch.OldEnd)
			}
			if len(ch.Inserted) > 0 {
				require.Equal(t, ch.Inserted[0].Start, ch.Start)
				require.Equal(t, ch.Inserted[len(ch.Inserted)-1].End, ch.NewEnd)
			} else {
				require.Equal(t, ch.Start, ch.NewEnd)
			}
			require.Equal(t, len(after)-len(before), ch.Shift())
		})
	}
}

// TestEmptyChangePlace checks where an empty change sits: where the trim at
// the start and the trim at the end meet. In case A24, "bar" replaced with
// "bar" in "foo bar\n", that place is offset 7 (owner decision,
// 2026-11-19).
func TestEmptyChangePlace(t *testing.T) {
	lx := newLexer(t, ruleSetA(), "foo bar\n")
	ch, err := lx.Apply(Edit{Start: 4, End: 7, Text: []byte("bar")})
	require.NoError(t, err)
	require.Equal(t, Change{Start: 7, OldEnd: 7, NewEnd: 7}, ch)
	require.Equal(t, 0, ch.Shift())
}

// TestTenBackspacesInARow applies the ten backspaces to one lexer, one after
// the other, and checks that each change is the change of its row.
func TestTenBackspacesInARow(t *testing.T) {
	var rows []editCase
	for _, c := range ruleSetACases {
		if strings.HasPrefix(c.name, "backspace") {
			rows = append(rows, c)
		}
	}
	require.Len(t, rows, 10)
	lx := newLexer(t, ruleSetA(), plain(rows[0].before))
	for _, c := range rows {
		before := heldText(lx)
		require.Equal(t, plain(c.before), before, c.name)
		e := parseEdit(t, c.edit)
		ch, err := lx.Apply(e)
		require.NoError(t, err)
		after := heldText(lx)
		require.Equal(t, c.removed, tokenList(ch.Removed, before), c.name)
		require.Equal(t, c.inserted, tokenList(ch.Inserted, after), c.name)
	}
	require.Equal(t, "tot␣+␣beta⏎", visible(heldText(lx)))
}
