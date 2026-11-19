package statelex

import (
	"math/bits"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWorkOfASmallEdit makes one insertion and one deletion near the start of
// texts of growing sizes. Each text has a sync point next to the edit, so the
// re-lexed run is the same at every size. The work counters of the edit report
// the bytes of the held text that were copied, the nodes of the rope that were
// visited or changed, the held tokens that were visited, moved or rewritten,
// the steps that found the first affected token, and the bytes that the rope
// read to count newlines and runes. The test checks the cost
// that the specification states: the bytes copied do not depend on the size of
// the text, and the other amounts grow with the logarithm of the size, not
// with the untouched text after the edit.
func TestWorkOfASmallEdit(t *testing.T) {
	const line = "x = 1\n"
	sizes := []int{1 << 12, 1 << 15, 1 << 18}
	edits := []struct {
		name string
		edit Edit
	}{
		{"insert", Insert(1, []byte("y"))},
		{"delete", Delete(1, 2)}, // deletes the y again
	}

	type amounts struct{ textBytes, ropeNodes, tokens, find, run, countBytes int }
	got := make(map[string][]amounts)
	for _, size := range sizes {
		text := strings.Repeat(line, size/len(line))
		lx := newLexer(t, ruleSetA(), text)
		ntoks := 6 * (size / len(line))
		for _, e := range edits {
			_, err := lx.Apply(e.edit)
			require.NoError(t, err)

			run := lx.lastRun
			require.Equal(t, relexRun{restart: 0, end: 1 + len(e.edit.Text), syncOld: 1 + (e.edit.End - e.edit.Start), synced: true}, run, "%s at %d bytes", e.name, size)

			w := lx.work
			a := amounts{w.textBytes, w.ropeNodes, w.tokens, w.find, run.end - run.restart, w.countBytes}
			got[e.name] = append(got[e.name], a)
			t.Logf("%s, %7d bytes, %6d tokens: %+v", e.name, len(text), ntoks, a)

			// The bytes copied: the new bytes, the removed bytes and the small
			// chunks next to the edit that are joined to the new bytes.
			require.LessOrEqual(t, w.textBytes, len(e.edit.Text)+(e.edit.End-e.edit.Start)+maxChunk, "%s at %d bytes", e.name, size)

			// A path from the root of a treap has about 2 ln n nodes, and
			// an edit goes down a few paths of each tree.
			logN := bits.Len(uint(ntoks))
			require.LessOrEqual(t, w.tokens, 40*logN, "%s at %d bytes", e.name, size)
			require.LessOrEqual(t, w.ropeNodes, 40*bits.Len(uint(len(text)/maxChunk+1)), "%s at %d bytes", e.name, size)
			require.LessOrEqual(t, w.find, 10*logN, "%s at %d bytes", e.name, size)

			// The counts: the new chunks, and the smaller half of a chunk
			// that a split slices, on a few paths.
			require.LessOrEqual(t, w.countBytes, w.textBytes+4*maxChunk, "%s at %d bytes", e.name, size)
		}
	}

	// From the smallest text to the largest, the size grows 64 times. The
	// bytes copied stay the same, and no other amount grows four times.
	for name, as := range got {
		first, last := as[0], as[len(as)-1]
		require.Equal(t, first.textBytes, last.textBytes, name)
		require.Equal(t, first.run, last.run, name)
		require.Equal(t, first.countBytes, last.countBytes, name)
		require.Less(t, last.tokens, 4*first.tokens, name)
		require.Less(t, last.ropeNodes, 4*first.ropeNodes, name)
		require.Less(t, last.find, 4*first.find, name)
	}
}
