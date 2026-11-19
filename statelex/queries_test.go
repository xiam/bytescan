package statelex

import (
	"errors"
	"io"
	"math/bits"
	mrand "math/rand"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/core"
)

// place is a line and a rune column.
type place struct{ line, column int }

// cursorPlaces returns the line and the column that a cursor reports at each
// rune start of text and at its end, by byte offset. An offset that is not in
// the map falls inside a valid UTF-8 character.
func cursorPlaces(t testing.TB, text string) map[int]place {
	t.Helper()
	rd := cursor.NewReader(strings.NewReader(text))
	places := make(map[int]place)
	for {
		lr, err := rd.ReadLocatedRune()
		if errors.Is(err, io.EOF) {
			end := rd.Cursor()
			places[end.ByteOffset()] = place{end.Line(), end.Column()}
			return places
		}
		require.NoError(t, err)
		p := lr.Pos()
		places[p.ByteOffset()] = place{p.Line(), p.Column()}
	}
}

// scanUnfinished returns the start of the first token of a scan of the whole
// text from its start whose rules were shown the end of input.
func scanUnfinished(t testing.TB, rules []rule, text string) (int, bool) {
	t.Helper()
	sc := bytescan.New(strings.NewReader(text))
	for _, r := range rules {
		sc.MustAddRule(r.typ, r.rule)
	}
	for {
		tok, la, err := sc.NextLookahead()
		if errors.Is(err, io.EOF) {
			return 0, false
		}
		require.NoError(t, err)
		if la.ReadEnd {
			return tok.Span().Start().ByteOffset(), true
		}
	}
}

// incompleteTail returns the start of a UTF-8 character that is not complete
// at the end of text, and the start of the token of toks that holds it.
func incompleteTail(text string, toks []Token) (int, bool) {
	for i := max(0, len(text)-3); i < len(text); i++ {
		if text[i] >= 0xc0 && !utf8.FullRuneInString(text[i:]) {
			for _, tok := range toks {
				if tok.Start <= i && i < tok.End {
					return tok.Start, true
				}
			}
			return i, true
		}
	}
	return 0, false
}

// overlapping returns the tokens of toks that overlap [start, end) after the
// range is clipped to [0, n].
func overlapping(toks []Token, start, end, n int) []Token {
	start, end = min(max(start, 0), n), min(max(end, 0), n)
	if start >= end {
		return nil
	}
	var out []Token
	for _, tok := range toks {
		if tok.End > start && tok.Start < end {
			out = append(out, tok)
		}
	}
	return out
}

// checkQueries checks every query of lx against a scan of the whole text:
// toks are the tokens of that scan, end is its mode state at the end, and
// unfinished is the start of its first token whose rules read the end of
// input, with ok.
func checkQueries(t *testing.T, rng *mrand.Rand, lx *Lexer, text string, toks []Token, end modes.Stack, unfinished int, ok bool) {
	t.Helper()
	n := len(text)
	require.Equal(t, n, lx.Len())

	// Text and Tokens for random ranges, clipped, empty and inverted ones
	// included, and for the whole text.
	ranges := [][2]int{{0, n}, {-5, n + 5}, {n, n}, {n, 0}}
	for i := 0; i < 8; i++ {
		ranges = append(ranges, [2]int{rng.Intn(n+5) - 2, rng.Intn(n+5) - 2})
	}
	for _, r := range ranges {
		a, b := min(max(r[0], 0), n), min(max(r[1], 0), n)
		var want []byte
		if a < b {
			want = []byte(text[a:b])
		}
		require.Equal(t, want, lx.Text(r[0], r[1]), "Text(%d, %d) of %q", r[0], r[1], text)
		require.Equal(t, overlapping(toks, r[0], r[1], n), lx.Tokens(r[0], r[1]), "Tokens(%d, %d) of %q", r[0], r[1], text)
	}

	require.True(t, end.Equal(lx.ModeAtEnd()), "ModeAtEnd of %q: want %s, got %s", text, stackString(end), stackString(lx.ModeAtEnd()))

	if s, inc := incompleteTail(text, toks); inc && (!ok || s < unfinished) {
		unfinished, ok = s, true
	}
	if !ok {
		unfinished = 0
	}
	gotStart, gotOK := lx.Unfinished()
	require.Equal(t, [2]any{unfinished, ok}, [2]any{gotStart, gotOK}, "Unfinished of %q", text)

	// Position at every offset, and Offset back at every place.
	places := cursorPlaces(t, text)
	lineEnds := map[int]int{} // the last column of each line
	for off := -1; off <= n+1; off++ {
		line, col, err := lx.Position(off)
		p, found := places[off]
		switch {
		case off < 0 || off > n:
			require.ErrorIs(t, err, ErrRange, "Position(%d) of %q", off, text)
		case !found:
			require.ErrorIs(t, err, ErrSplitRune, "Position(%d) of %q", off, text)
		default:
			require.NoError(t, err)
			require.Equal(t, p, place{line, col}, "Position(%d) of %q", off, text)
			got, err := lx.Offset(line, col)
			require.NoError(t, err)
			require.Equal(t, off, got, "Offset(%d, %d) of %q", line, col, text)
			lineEnds[line] = max(lineEnds[line], col)
		}
	}
	lines := len(lineEnds)
	for line, last := range lineEnds {
		_, err := lx.Offset(line, last+1)
		require.ErrorIs(t, err, ErrRange, "Offset(%d, %d) of %q", line, last+1, text)
		_, err = lx.Offset(line, -1)
		require.ErrorIs(t, err, ErrRange)
	}
	for _, line := range []int{0, -1, lines + 1} {
		_, err := lx.Offset(line, 0)
		require.ErrorIs(t, err, ErrRange, "Offset(%d, 0) of %q", line, text)
	}
}

// TestQueriesAfterGeneratedEdits applies many generated edits, from a fixed
// seed, under rule set A and under the mode sets of TestGeneratedEditsModes.
// After each edit each query gives the value of a scan of the whole new text
// from its start: the bytes and the tokens of a range, the mode state at the
// end, the first token whose rules read the end of input, and the line and
// the column that a cursor reports at each offset.
func TestQueriesAfterGeneratedEdits(t *testing.T) {
	t.Run("A", func(t *testing.T) {
		rules := ruleSetA()
		rng := mrand.New(mrand.NewSource(131))
		rounds := 200
		if testing.Short() {
			rounds = 30
		}
		start, _ := New(modeSet(rules))
		for round := 0; round < rounds; round++ {
			lx := newLexer(t, rules, "")
			text := ""
			for i := 0; i < 20; i++ {
				e := genEdit(rng, len(text))
				if splitsChar(text, e.Start) || splitsChar(text, e.End) {
					continue
				}
				_, err := lx.Apply(e)
				require.NoError(t, err)
				text = text[:e.Start] + string(e.Text) + text[e.End:]
				u, ok := scanUnfinished(t, rules, text)
				checkQueries(t, rng, lx, text, scan(t, rules, text), start.ModeAtEnd(), u, ok)
			}
		}
	})
	rng := mrand.New(mrand.NewSource(131))
	runModeEdits(t, func(t *testing.T, ms *modes.ModeSet, lx *Lexer, _ *consumer, _ Change, _, after string) {
		want, end := scanModes(t, ms, after)
		fresh, err := New(ms)
		require.NoError(t, err)
		_, err = fresh.Apply(Insert(0, []byte(after)))
		require.NoError(t, err)
		u, ok := fresh.toks.firstEnd()
		checkQueries(t, rng, lx, after, tokens(want), end, u, ok)
	})
}

// TestQueriesOverManyChunks makes edits of a text of many chunks with
// multi-byte characters and broken UTF-8, and edits that complete a
// character with the bytes before them. After each edit the trees hold the
// right counts, and Position and Offset give the values of a cursor at random
// offsets.
func TestQueriesOverManyChunks(t *testing.T) {
	rng := mrand.New(mrand.NewSource(1310))
	var sb strings.Builder
	for sb.Len() < 6*maxChunk {
		sb.WriteString(genPieces[rng.Intn(len(genPieces))])
	}
	text := sb.String()
	lx := newLexer(t, ruleSetA(), text)
	for i := 0; i < 300; i++ {
		e := genEdit(rng, len(text))
		if splitsChar(text, e.Start) || splitsChar(text, e.End) {
			continue
		}
		_, err := lx.Apply(e)
		require.NoError(t, err)
		text = text[:e.Start] + string(e.Text) + text[e.End:]
		checkTrees(t, lx)
		places := cursorPlaces(t, text)
		for j := 0; j < 20; j++ {
			off := rng.Intn(len(text) + 1)
			line, col, err := lx.Position(off)
			p, found := places[off]
			if !found {
				require.ErrorIs(t, err, ErrSplitRune)
				continue
			}
			require.NoError(t, err)
			require.Equal(t, p, place{line, col}, "Position(%d)", off)
			got, err := lx.Offset(line, col)
			require.NoError(t, err)
			require.Equal(t, off, got)
		}
	}
	require.Equal(t, scan(t, ruleSetA(), text), lx.Tokens(0, lx.Len()))
}

// TestQueriesREPL is the REPL of docs/specs/resumable-lexing.md: under rule
// set B the line `say␣"` leaves the string open, and the line `say␣"hi"`
// closes it. Under rule set A the line `a␣/*␣x⏎b⏎` leaves the comment open.
func TestQueriesREPL(t *testing.T) {
	for _, c := range []struct{ text, end string }{
		{`say "`, "[code str]"},
		{`say "hi"`, "[code]"},
	} {
		lx, err := New(ruleSetB())
		require.NoError(t, err)
		_, err = lx.Apply(Insert(0, []byte(c.text)))
		require.NoError(t, err)
		require.Equal(t, c.end, stackString(lx.ModeAtEnd()), c.text)
		_, ok := lx.Unfinished()
		require.False(t, ok, c.text)
	}
	lx, err := New(ruleSetB())
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte(`say "`)))
	require.NoError(t, err)
	require.Len(t, lx.Tokens(0, lx.Len()), 3)

	lx = newLexer(t, ruleSetA(), plain("a␣/*␣x⏎b⏎"))
	start, ok := lx.Unfinished()
	require.True(t, ok)
	require.Equal(t, 2, start)
	require.Equal(t, []Token{{"OP", 2, 3}}, lx.Tokens(2, 3))
}

// TestQueriesLimits checks the queries at the limits that the specification
// states: a range is clipped to [0, Len()], an empty or an inverted range
// gives nothing, a token that crosses an end of the range is returned whole,
// an empty text answers every query, a place outside the text returns
// ErrRange, and an offset inside a valid character returns ErrSplitRune.
func TestQueriesLimits(t *testing.T) {
	empty := newLexer(t, ruleSetA(), "")
	require.Nil(t, empty.Text(0, 0))
	require.Nil(t, empty.Text(-3, 3))
	require.Nil(t, empty.Tokens(-3, 3))
	require.Equal(t, "[code]", stackString(empty.ModeAtEnd()))
	start, ok := empty.Unfinished()
	require.False(t, ok)
	require.Equal(t, 0, start)
	line, col, err := empty.Position(0)
	require.NoError(t, err)
	require.Equal(t, place{1, 0}, place{line, col})
	off, err := empty.Offset(1, 0)
	require.NoError(t, err)
	require.Equal(t, 0, off)
	_, _, err = empty.Position(1)
	require.ErrorIs(t, err, ErrRange)
	_, err = empty.Offset(1, 1)
	require.ErrorIs(t, err, ErrRange)
	_, err = empty.Offset(2, 0)
	require.ErrorIs(t, err, ErrRange)

	lx := newLexer(t, ruleSetA(), "foo bar\nñé")
	require.Equal(t, []byte("foo bar\nñé"), lx.Text(-10, 100))
	require.Nil(t, lx.Text(4, 4))
	require.Nil(t, lx.Text(5, 2))
	require.Nil(t, lx.Tokens(4, 4))
	require.Nil(t, lx.Tokens(5, 2))
	require.Nil(t, lx.Tokens(100, 200))
	// [2, 5) overlaps "foo", the space and "bar": each is whole.
	require.Equal(t, []Token{{"WORD", 0, 3}, {"WS", 3, 4}, {"WORD", 4, 7}}, lx.Tokens(2, 5))
	require.Equal(t, []byte("o b"), lx.Text(2, 5))

	_, _, err = lx.Position(-1)
	require.ErrorIs(t, err, ErrRange)
	_, _, err = lx.Position(lx.Len() + 1)
	require.ErrorIs(t, err, ErrRange)
	_, _, err = lx.Position(9) // the second byte of ñ
	require.ErrorIs(t, err, ErrSplitRune)
	line, col, err = lx.Position(lx.Len())
	require.NoError(t, err)
	require.Equal(t, place{2, 2}, place{line, col})
	_, err = lx.Offset(1, 8) // line 1 has 7 runes and its newline
	require.ErrorIs(t, err, ErrRange)
	off, err = lx.Offset(1, 7) // the newline
	require.NoError(t, err)
	require.Equal(t, 7, off)
	_, err = lx.Offset(2, 3)
	require.ErrorIs(t, err, ErrRange)
	_, err = lx.Offset(3, 0)
	require.ErrorIs(t, err, ErrRange)

	// Broken bytes are character boundaries: each is one column.
	broken := newLexer(t, ruleSetA(), "a\xe2\x82b")
	for off, want := range []place{{1, 0}, {1, 1}, {1, 2}, {1, 3}, {1, 4}} {
		line, col, err := broken.Position(off)
		require.NoError(t, err)
		require.Equal(t, want, place{line, col}, "offset %d", off)
	}
}

// TestUnfinishedIncompleteCharacter checks the texts that end inside a UTF-8
// character that is not complete. No token read the end of input, and
// Unfinished returns the start of the character, or the start of the token
// that holds it when that token starts earlier.
func TestUnfinishedIncompleteCharacter(t *testing.T) {
	for _, c := range []struct {
		text  string
		start int
		ok    bool
	}{
		{"\xc3", 0, true},
		{"foo\xc3", 3, true},
		{"\xe2\x82", 0, true},
		{"ab \xf0\x9f\x98", 3, true},
		{"\xc3\xb1", 0, false},  // ñ is complete
		{"a\xff", 0, false},     // \xff never starts a character
		{"a\xe2\x28", 0, false}, // \xe2 cannot be completed
	} {
		lx := newLexer(t, ruleSetA(), c.text)
		_, readEnd := scanUnfinished(t, ruleSetA(), c.text)
		require.False(t, readEnd, "%q", c.text)
		start, ok := lx.Unfinished()
		require.Equal(t, [2]any{c.start, c.ok}, [2]any{start, ok}, "%q", c.text)
	}

	// A token of four symbols holds the incomplete character, and it reads
	// nothing after them: the token starts earlier than the character.
	anyRune := core.Rune(core.RuneSet("\x00").Not())
	ms := modes.NewModeSet("code")
	ms.Mode("code").Rule("ANY", core.Sequence(core.Literal("xy"), anyRune, anyRune))
	lx, err := New(ms)
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte("xy\xe2\x82")))
	require.NoError(t, err)
	require.Equal(t, []Token{{"ANY", 0, 4}}, lx.Tokens(0, 4))
	start, ok := lx.Unfinished()
	require.True(t, ok)
	require.Equal(t, 0, start)
}

// TestCoordinatesAfterEdits makes an edit that adds a newline, one that
// removes a newline and one that adds a multi-byte character. After each
// edit, Position and Offset give the line and the column of a cursor over
// the whole new text at every offset.
func TestCoordinatesAfterEdits(t *testing.T) {
	text := "ab cd\nef"
	lx := newLexer(t, ruleSetA(), text)
	for _, e := range []Edit{
		Insert(2, []byte("\n")), // "ab⏎ cd⏎ef"
		Delete(6, 7),            // "ab⏎ cdef"
		Insert(4, []byte("ñ€")), // "ab⏎ cñ€def"
		Insert(0, []byte("😀\n")),
	} {
		_, err := lx.Apply(e)
		require.NoError(t, err)
		text = text[:e.Start] + string(e.Text) + text[e.End:]
		for off, p := range cursorPlaces(t, text) {
			line, col, err := lx.Position(off)
			require.NoError(t, err)
			require.Equal(t, p, place{line, col}, "Position(%d) of %q", off, text)
			got, err := lx.Offset(p.line, p.column)
			require.NoError(t, err)
			require.Equal(t, off, got, "Offset(%d, %d) of %q", p.line, p.column, text)
		}
	}
}

// TestWorkOfASmallQuery asks for the same small range and the same offset in
// texts of growing sizes. The text and the tokens outside the range grow, and
// the answer does not. The work counters of the lexer report the held tokens
// that a query visited, the nodes of the rope that it visited and the bytes
// that it read to count. Each query stays inside the cost that the
// specification states: one path of a tree, plus what it returns (Text,
// Tokens), or a part of a chunk (Position, Offset), or the end mark in each
// node (Unfinished). While the size grows 64 times, the tokens visited do
// not grow four times, and the nodes of the rope do not grow sixteen times.
func TestWorkOfASmallQuery(t *testing.T) {
	const line = "x = 1\n"
	sizes := []int{1 << 12, 1 << 15, 1 << 18}
	type amounts struct{ tokens, find, ropeNodes, countBytes int }
	// Each range and offset is asked near the start of the text and in its
	// middle, at the start of a line: mid is the offset of that line, and
	// midLine its number.
	queries := []struct {
		name string
		run  func(lx *Lexer, mid, midLine int) any
	}{
		{"Text", func(lx *Lexer, _, _ int) any { return string(lx.Text(7, 10)) }},
		{"Text mid", func(lx *Lexer, mid, _ int) any { return string(lx.Text(mid+1, mid+4)) }},
		{"Tokens", func(lx *Lexer, _, _ int) any { return lx.Tokens(7, 10) }},
		{"Tokens mid", func(lx *Lexer, mid, _ int) any { return lx.Tokens(mid+1, mid+4) }},
		{"Unfinished", func(lx *Lexer, _, _ int) any { s, ok := lx.Unfinished(); return [2]any{s, ok} }},
		{"Position", func(lx *Lexer, _, _ int) any { l, c, err := lx.Position(8); return [3]any{l, c, err} }},
		{"Position mid", func(lx *Lexer, mid, _ int) any { l, c, err := lx.Position(mid + 2); return [3]any{l, c, err} }},
		{"Offset", func(lx *Lexer, _, _ int) any { o, err := lx.Offset(2, 2); return [2]any{o, err} }},
		{"Offset mid", func(lx *Lexer, _, midLine int) any { o, err := lx.Offset(midLine, 2); return [2]any{o, err} }},
	}
	got := make(map[string][]amounts)
	for _, size := range sizes {
		// The last line ends with an open comment, so a token at the end read
		// the end of input.
		lines := size / len(line)
		text := strings.Repeat(line, lines) + "/*"
		lx := newLexer(t, ruleSetA(), text)
		midLine := lines/2 + 1
		mid := (midLine - 1) * len(line)
		ntoks := len(held(lx))
		logN := bits.Len(uint(ntoks))
		for _, q := range queries {
			lx.work = workCounters{}
			answer := q.run(lx, mid, midLine)
			w := lx.work
			a := amounts{w.tokens, w.find, w.ropeNodes, w.countBytes}
			got[q.name] = append(got[q.name], a)
			t.Logf("%-12s %7d bytes, %6d tokens: %+v -> %v", q.name, len(text), ntoks, a, answer)

			// A path from the root of a treap has about 2 ln n nodes. The
			// tokens returned are at most 3, and a count reads at most one
			// chunk on each of two paths.
			require.LessOrEqual(t, w.tokens+w.find, 10*logN+3, "%s at %d bytes", q.name, size)
			require.LessOrEqual(t, w.ropeNodes, 20*bits.Len(uint(len(text)/maxChunk+1)), "%s at %d bytes", q.name, size)
			require.LessOrEqual(t, w.countBytes, 2*maxChunk, "%s at %d bytes", q.name, size)
		}
	}
	for name, as := range got {
		first, last := as[0], as[len(as)-1]
		require.Less(t, last.tokens+last.find, 4*max(1, first.tokens+first.find), name)
		// The smallest rope has 4 chunks, so a path in it is very short: the
		// rope paths may grow more, but far less than the 64 times of the text.
		require.Less(t, last.ropeNodes, 16*max(1, first.ropeNodes), name)
	}
}

// TestInternalErrorCoordinates checks that Position and Offset, the queries
// that can return an error, return the internal error of a failed edit for
// every input, valid or not, as Apply and SetText do (TestInternalError).
func TestInternalErrorCoordinates(t *testing.T) {
	lx := newLexer(t, ruleSetA(), "x = 1\nñ")
	_, _, err := lx.Position(7) // inside ñ, before the failure
	require.ErrorIs(t, err, ErrSplitRune)
	lx.testHook = func() error { return errors.New("simulated") }
	_, err = lx.Apply(Insert(0, []byte("y")))
	require.ErrorIs(t, err, bytescan.ErrInternal)
	lx.testHook = nil

	for _, off := range []int{0, 7, lx.Len(), -1, lx.Len() + 1} {
		_, _, err2 := lx.Position(off)
		require.Equal(t, err, err2, "Position(%d)", off)
	}
	for _, p := range []place{{1, 0}, {2, 1}, {0, 0}, {1, 100}, {3, 0}} {
		_, err2 := lx.Offset(p.line, p.column)
		require.Equal(t, err, err2, "Offset(%d, %d)", p.line, p.column)
	}
}
