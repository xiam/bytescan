package statelex

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// flagsCase is an edit under ruleSetFlags that moves a candidate sync point
// to a place with other BOF or BOL flags. The old token at the candidate has
// the same place and the same mode state, so only the flags in the sync test
// keep the re-lex going.
type flagsCase struct {
	name, before string
	edit         Edit
	removed      string
	inserted     string
}

func runFlagsCase(t *testing.T, c flagsCase) {
	t.Helper()
	rules := ruleSetFlags()
	lx := newLexer(t, rules, c.before)
	cons := &consumer{toks: held(lx)}
	after := c.before[:c.edit.Start] + string(c.edit.Text) + c.before[c.edit.End:]
	ch, err := lx.Apply(c.edit)
	require.NoError(t, err)
	checkChange(t, rules, lx, cons, ch, c.before, after)
	require.Equal(t, c.removed, tokenList(ch.Removed, c.before), "removed")
	require.Equal(t, c.inserted, tokenList(ch.Inserted, after), "inserted")
}

// TestFlagsDeleteFirstLine deletes the first line. The first token of the
// second line, DIRECTIVE #!sh, starts at the same place of the unchanged text,
// in the same mode state, but its first symbol gains the BOF flag: it becomes
// SHEBANG #!sh.
func TestFlagsDeleteFirstLine(t *testing.T) {
	runFlagsCase(t, flagsCase{
		name:     "delete the first line",
		before:   "x\n#!sh\ny\n",
		edit:     Delete(0, 2),
		removed:  "WORD x [0,1), NL ⏎ [1,2), DIRECTIVE #!sh [2,6)",
		inserted: "SHEBANG #!sh [0,4)",
	})
}

// TestFlagsInsertNewlineBeforeBoundary inserts a newline just before the
// boundary at "#x". The old token there, UNKNOWN #, is at the same place in the
// same mode state, but its first symbol gains the BOL flag: the new token is
// DIRECTIVE #x.
func TestFlagsInsertNewlineBeforeBoundary(t *testing.T) {
	runFlagsCase(t, flagsCase{
		name:     "insert a newline",
		before:   "a #x\n",
		edit:     Insert(2, []byte("\n")),
		removed:  "UNKNOWN # [2,3), WORD x [3,4)",
		inserted: "NL ⏎ [2,3), DIRECTIVE #x [3,5)",
	})
}

// TestFlagsRemoveNewlineBeforeBoundary removes the newline just before the
// boundary at "#x". The old token there, DIRECTIVE #x, is at the same place in
// the same mode state, but its first symbol loses the BOL flag.
func TestFlagsRemoveNewlineBeforeBoundary(t *testing.T) {
	runFlagsCase(t, flagsCase{
		name:     "remove a newline",
		before:   "a \n#x\n",
		edit:     Delete(2, 3),
		removed:  "NL ⏎ [2,3), DIRECTIVE #x [3,5)",
		inserted: "UNKNOWN # [2,3), WORD x [3,4)",
	})
}
