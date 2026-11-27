package statelex

import (
	mrand "math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// genPieces are the pieces of the generated texts: tokens of every rule set
// of the tests, the openers and closers that make rules read far ahead, the
// flags rules, multi-byte characters and broken UTF-8.
var genPieces = []string{
	"foo", "bar", "x", "12", "3.5", ".", "=", "==", "+", "*", "/",
	" ", "  ", "\n", "\n", "\"", "\"hi\"", "/*", "*/", "/* c */",
	"(", ")", "<<", ">>", "f (", "#", "#!", "#x",
	"é", "ñ", "€", "😀", "\xc3", "\xe2", "\xa9", "\xe2\x82", "\xf0\x9f",
}

// tailPieces are continuation bytes. Inserted after a first byte that is
// not complete, they complete a character, and the bytes before the edit
// decode to other symbols.
var tailPieces = []string{"\xa9", "\xb1", "\x82\xac", "\xac", "\x98\x80"}

func genText(rng *mrand.Rand, maxPieces int) string {
	var sb strings.Builder
	for i, n := 0, rng.Intn(maxPieces+1); i < n; i++ {
		sb.WriteString(genPieces[rng.Intn(len(genPieces))])
	}
	return sb.String()
}

// genEdit returns an edit of a text of n bytes: an insertion, a deletion or a
// replacement, at the start, in the middle or at the end, or of the whole
// text. The offsets can fall inside a character.
func genEdit(rng *mrand.Rand, n int) Edit {
	pos := func() int {
		switch rng.Intn(4) {
		case 0:
			return 0
		case 1:
			return n
		}
		return rng.Intn(n + 1)
	}
	var a, b int
	switch rng.Intn(6) {
	case 0: // the whole text
		a, b = 0, n
	case 1, 2: // an insertion
		a = pos()
		b = a
	default:
		a, b = pos(), pos()
		if a > b {
			a, b = b, a
		}
	}
	var text []byte
	switch {
	case rng.Intn(6) == 0:
		text = []byte(tailPieces[rng.Intn(len(tailPieces))])
	case a == b || rng.Intn(3) > 0:
		text = []byte(genText(rng, 3))
	}
	return Edit{Start: a, End: b, Text: text}
}

// splitsChar reports whether off falls inside a valid UTF-8 character of text.
func splitsChar(text string, off int) bool {
	_, _, inside := charAt([]byte(text), off)
	return inside
}

// checkChange checks one change against two full scans: the lexer holds the
// tokens of a scan of the new text, the consumer that applies the change holds
// them too, and the ends of the change are trimmed.
func checkChange(t *testing.T, rules []rule, lx *Lexer, c *consumer, ch Change, before, after string) {
	t.Helper()
	want := scan(t, rules, after)
	require.Equal(t, after, heldText(lx))
	require.Equal(t, want, held(lx), "held tokens: %q -> %q", before, after)
	checkTrees(t, lx)
	dropped := c.apply(ch)
	require.Equal(t, ch.Removed, dropped, "removed: %q -> %q", before, after)
	require.Equal(t, want, c.toks, "consumer: %q -> %q", before, after)
	require.Equal(t, len(after)-len(before), ch.Shift())

	// The trimmed ends: the first and the last token of the change differ in
	// type, range or bytes. Nothing from the sync point on is in the change.
	if len(ch.Removed) > 0 && len(ch.Inserted) > 0 {
		o, w := ch.Removed[0], ch.Inserted[0]
		require.False(t, o == w && before[o.Start:o.End] == after[w.Start:w.End], "untrimmed start: %q -> %q", before, after)
		o, w = ch.Removed[len(ch.Removed)-1], ch.Inserted[len(ch.Inserted)-1]
		moved := Token{Type: o.Type, Start: o.Start + ch.Shift(), End: o.End + ch.Shift()}
		require.False(t, moved == w && before[o.Start:o.End] == after[w.Start:w.End], "untrimmed end: %q -> %q", before, after)
	}
	run := lx.lastRun
	require.LessOrEqual(t, ch.OldEnd, run.syncOld)
	require.LessOrEqual(t, ch.NewEnd, run.end)
	require.GreaterOrEqual(t, ch.Start, run.restart)
}

// TestGeneratedEdits applies many generated edits, from a fixed seed, to
// generated texts, under rule set A, a rule set whose rules read far ahead,
// and a rule set whose rules read the BOF and BOL flags. After each edit the
// lexer and a consumer that applies each change hold the tokens of a scan of
// the whole new text. An edit that splits a character is refused, and changes
// nothing. Some edits go through SetText.
func TestGeneratedEdits(t *testing.T) {
	sets := map[string][]rule{"A": ruleSetA(), "far": ruleSetFar(), "flags": ruleSetFlags()}
	rounds := 300
	if testing.Short() {
		rounds = 40
	}
	for name, rules := range sets {
		t.Run(name, func(t *testing.T) {
			rng := mrand.New(mrand.NewSource(129))
			edits, refused := 0, 0
			for round := 0; round < rounds; round++ {
				lx := newLexer(t, rules, "")
				c := &consumer{}
				text := ""
				if round%2 == 1 {
					text = genText(rng, 16)
					ch, err := lx.Apply(Insert(0, []byte(text)))
					require.NoError(t, err)
					checkChange(t, rules, lx, c, ch, "", text)
				}
				for i := 0; i < 20; i++ {
					e := genEdit(rng, len(text))
					after := text[:e.Start] + string(e.Text) + text[e.End:]
					if rng.Intn(5) == 0 {
						ch, err := lx.SetText([]byte(after))
						require.NoError(t, err)
						checkChange(t, rules, lx, c, ch, text, after)
						text = after
						edits++
						continue
					}
					ch, err := lx.Apply(e)
					if splitsChar(text, e.Start) || splitsChar(text, e.End) {
						require.ErrorIs(t, err, ErrSplitRune, "%q %+v", text, e)
						require.Equal(t, text, heldText(lx))
						require.Equal(t, scan(t, rules, text), held(lx))
						refused++
						continue
					}
					require.NoError(t, err)
					checkChange(t, rules, lx, c, ch, text, after)
					text = after
					edits++
				}
			}
			t.Logf("rule set %s: %d edits applied, %d refused with ErrSplitRune", name, edits, refused)
			require.Greater(t, refused, 0)
		})
	}
}
