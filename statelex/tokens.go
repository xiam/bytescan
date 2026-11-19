package statelex

import (
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/modes"
)

// The held tokens are a treap in text order. A leaf of the design is a node
// here: each node holds one token, and the sums of its subtree. No node holds
// an absolute offset: the start of a token is the summed length of the tokens
// before it, found on the way down. So the tokens after an edit move by d
// bytes with no work.

type tokNode struct {
	left, right *tokNode
	prio        uint64

	typ     bytescan.TokenType
	length  int         // the bytes of the token
	after   int         // Lookahead.After
	readEnd bool        // Lookahead.ReadEnd
	stack   modes.Stack // the mode state at the start of the token

	size   int  // the summed length of the subtree
	reach  int  // the furthest reach in the subtree, from the start of the subtree
	anyEnd bool // a token of the subtree read the end of the text
}

func (n *tokNode) sz() int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *tokNode) update() {
	ls := n.left.sz()
	n.size = ls + n.length + n.right.sz()
	n.reach = ls + n.length + n.after
	n.anyEnd = n.readEnd
	if n.left != nil {
		n.reach = max(n.reach, n.left.reach)
		n.anyEnd = n.anyEnd || n.left.anyEnd
	}
	if n.right != nil {
		n.reach = max(n.reach, ls+n.length+n.right.reach)
		n.anyEnd = n.anyEnd || n.right.anyEnd
	}
}

// tokTree is the held tokens.
type tokTree struct {
	root *tokNode
	rnd  *prng
	work *workCounters
}

// split returns the tokens that start before off and the tokens from off on.
// off is a token boundary.
func (t *tokTree) split(n *tokNode, off int) (*tokNode, *tokNode) {
	if n == nil {
		return nil, nil
	}
	t.work.tokens++
	ls := n.left.sz()
	if off <= ls {
		a, b := t.split(n.left, off)
		n.left = b
		n.update()
		return a, n
	}
	a, b := t.split(n.right, off-ls-n.length)
	n.right = a
	n.update()
	return n, b
}

func (t *tokTree) merge(a, b *tokNode) *tokNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	t.work.tokens++
	if a.prio > b.prio {
		a.right = t.merge(a.right, b)
		a.update()
		return a
	}
	b.left = t.merge(a, b.left)
	b.update()
	return b
}

// build makes a treap of the nodes, in order, in O(len(nodes)).
func (t *tokTree) build(nodes []*tokNode) *tokNode {
	var spine []*tokNode
	for _, n := range nodes {
		t.work.tokens++
		n.prio = t.rnd.next()
		var last *tokNode
		for len(spine) > 0 && spine[len(spine)-1].prio < n.prio {
			last = spine[len(spine)-1]
			spine = spine[:len(spine)-1]
			last.update()
		}
		n.left = last
		n.right = nil
		if len(spine) > 0 {
			spine[len(spine)-1].right = n
		}
		spine = append(spine, n)
	}
	for i := len(spine) - 1; i >= 0; i-- {
		spine[i].update()
	}
	if len(spine) == 0 {
		return nil
	}
	return spine[0]
}

// firstAffected returns the first token, in text order, whose reach is more
// than a, or that read the end of the text when atEnd is true, and the start
// of that token. It returns nil when no token qualifies. The furthest reach
// and the end mark in each node lead the search down one path.
func (t *tokTree) firstAffected(a int, atEnd bool) (*tokNode, int) {
	qualifies := func(n *tokNode, base int) bool {
		return n != nil && (base+n.reach > a || atEnd && n.anyEnd)
	}
	n, base := t.root, 0
	if !qualifies(n, base) {
		return nil, 0
	}
	for n != nil {
		t.work.find++
		if qualifies(n.left, base) {
			n = n.left
			continue
		}
		s := base + n.left.sz()
		if s+n.length+n.after > a || atEnd && n.readEnd {
			return n, s
		}
		base = s + n.length
		n = n.right
	}
	return nil, 0
}

// startOf returns the start of the token that holds off, or off when no token
// holds it (off is the length of the text). It costs one path.
func (t *tokTree) startOf(off int) int {
	n, base := t.root, 0
	for n != nil {
		t.work.find++
		s := base + n.left.sz()
		switch {
		case off < s:
			n = n.left
		case off < s+n.length:
			return s
		default:
			base = s + n.length
			n = n.right
		}
	}
	return off
}

// firstEnd returns the start of the first token, in text order, that read the
// end of the text, and true; or 0 and false. The end mark in each node leads
// the search down one path.
func (t *tokTree) firstEnd() (int, bool) {
	n, base := t.root, 0
	if n == nil || !n.anyEnd {
		return 0, false
	}
	for n != nil {
		t.work.find++
		if n.left != nil && n.left.anyEnd {
			n = n.left
			continue
		}
		s := base + n.left.sz()
		if n.readEnd {
			return s, true
		}
		base = s + n.length
		n = n.right
	}
	return 0, false
}

// at returns the token that starts at off, or nil when no token starts there.
func (t *tokTree) at(off int) *tokNode {
	n, base := t.root, 0
	for n != nil {
		t.work.find++
		s := base + n.left.sz()
		switch {
		case off < s:
			n = n.left
		case off == s:
			return n
		case off < s+n.length:
			return nil
		default:
			base = s + n.length
			n = n.right
		}
	}
	return nil
}

// tokIter returns the tokens from an offset on, in order, with their starts.
type tokIter struct {
	t     *tokTree
	stack []*tokNode
	bases []int // the start of the subtree of each node on the stack
}

// iter returns an iterator over the tokens that start at or after off. It
// costs one path from the root.
func (t *tokTree) iter(root *tokNode, off int) *tokIter {
	it := &tokIter{t: t}
	n, base := root, 0
	for n != nil {
		t.work.tokens++
		s := base + n.left.sz()
		if off <= s {
			it.stack = append(it.stack, n)
			it.bases = append(it.bases, base)
			n = n.left
		} else {
			base = s + n.length
			n = n.right
		}
	}
	return it
}

// next returns the next token and its start, or nil.
func (it *tokIter) next() (*tokNode, int) {
	if len(it.stack) == 0 {
		return nil, 0
	}
	k := len(it.stack) - 1
	n, base := it.stack[k], it.bases[k]
	it.stack, it.bases = it.stack[:k], it.bases[:k]
	start := base + n.left.sz()
	m, mb := n.right, start+n.length
	for m != nil {
		it.t.work.tokens++
		it.stack = append(it.stack, m)
		it.bases = append(it.bases, mb)
		m = m.left
	}
	return n, start
}
