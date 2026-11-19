package statelex

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// checkTrees checks the structure of both treaps: every parent has a priority
// at least that of its children, and every node holds the right sums. The
// rope also holds the right newlines and runes, and no valid UTF-8 character
// crosses the boundary of two chunks. The token tree also holds the right
// furthest reach and end mark.
func checkTrees(t *testing.T, lx *Lexer) {
	t.Helper()
	var text []byte
	var bounds []int
	var rope func(n *ropeNode) (int, int, int)
	rope = func(n *ropeNode) (int, int, int) {
		if n == nil {
			return 0, 0, 0
		}
		for _, c := range []*ropeNode{n.left, n.right} {
			if c != nil {
				require.GreaterOrEqual(t, n.prio, c.prio, "rope heap order")
			}
		}
		require.NotEmpty(t, n.chunk, "empty rope chunk")
		require.Equal(t, bytes.Count(n.chunk, []byte{'\n'}), n.nl, "chunk newlines")
		require.Equal(t, utf8.RuneCount(n.chunk), n.runes, "chunk runes")
		ls, lnl, lr := rope(n.left)
		if len(text) > 0 {
			bounds = append(bounds, len(text))
		}
		text = append(text, n.chunk...)
		rs, rnl, rr := rope(n.right)
		want := [3]int{ls + len(n.chunk) + rs, lnl + n.nl + rnl, lr + n.runes + rr}
		require.Equal(t, want, [3]int{n.size, n.nls, n.rns}, "rope sums")
		return want[0], want[1], want[2]
	}
	rope(lx.text.root)
	for _, b := range bounds {
		_, _, inside := charAt(text, b)
		require.False(t, inside, "a valid character crosses the chunk boundary %d", b)
	}

	type sums struct {
		size, reach int
		end         bool
	}
	var toks func(n *tokNode) sums
	toks = func(n *tokNode) sums {
		if n == nil {
			return sums{}
		}
		for _, c := range []*tokNode{n.left, n.right} {
			if c != nil {
				require.GreaterOrEqual(t, n.prio, c.prio, "token heap order")
			}
		}
		l, r := toks(n.left), toks(n.right)
		want := sums{size: l.size + n.length + r.size, reach: l.size + n.length + n.after, end: l.end || n.readEnd || r.end}
		if n.left != nil {
			want.reach = max(want.reach, l.reach)
		}
		if n.right != nil {
			want.reach = max(want.reach, l.size+n.length+r.reach)
		}
		require.Equal(t, want, sums{n.size, n.reach, n.anyEnd}, "token sums")
		return want
	}
	require.Equal(t, lx.Len(), toks(lx.toks.root).size)
}

// TestTreesStayInHeapOrder makes edits inside the chunks of a rope of many
// chunks, and checks the structure of both trees after each edit.
func TestTreesStayInHeapOrder(t *testing.T) {
	text := strings.Repeat("x = 1\n", 3000)
	lx := newLexer(t, ruleSetA(), text)
	checkTrees(t, lx)
	edits := []Edit{
		Insert(10283, []byte("z")),
		Delete(5000, 5003),
		{Start: 1500, End: 2600, Text: []byte("y = 22\n")},
		Insert(15000, []byte(strings.Repeat("ab ", 700))),
		Delete(100, 9000),
	}
	for i := 0; i < 200; i++ {
		edits = append(edits, Insert((i*7919)%10000, []byte("q")))
	}
	for _, e := range edits {
		after := text[:e.Start] + string(e.Text) + text[e.End:]
		_, err := lx.Apply(e)
		require.NoError(t, err)
		text = after
		checkTrees(t, lx)
		require.Equal(t, text, heldText(lx))
	}
	require.Equal(t, scan(t, ruleSetA(), text), held(lx))
}
