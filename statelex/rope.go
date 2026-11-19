package statelex

import (
	"bytes"
	"io"
	"unicode/utf8"
)

// The held text is a rope: a treap of chunks, kept in text order. Each node
// holds one chunk and the summed byte count of its subtree. The priorities
// keep the tree balanced in expectation, so a path from the root has about
// O(log n) nodes.
//
// A chunk is never changed after it is made. Splitting a chunk slices it, so
// it copies no byte. Only the new bytes of an edit, and the small chunks next
// to them that are joined to them, are copied.
//
// Each node also keeps the newlines and the runes of its chunk and of its
// subtree, so a line, a column or a rune offset is found by going down one
// path. A rune is a symbol of the decoder: a valid UTF-8 character, or one
// byte that does not start a valid character. No valid character crosses the
// boundary of two chunks, so the runes of a chunk are those of the whole text
// in its bytes, and an edit changes the counts only on its path.

// maxChunk is the largest chunk that the rope makes.
const maxChunk = 1024

type ropeNode struct {
	left, right *ropeNode
	prio        uint64
	chunk       []byte
	nl, runes   int // the newlines and the runes of the chunk
	size        int // the bytes of the subtree
	nls, rns    int // the newlines and the runes of the subtree
}

func (n *ropeNode) sums() (size, nls, rns int) {
	if n == nil {
		return 0, 0, 0
	}
	return n.size, n.nls, n.rns
}

func (n *ropeNode) sz() int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *ropeNode) update() {
	ls, lnl, lr := n.left.sums()
	rs, rnl, rr := n.right.sums()
	n.size = ls + len(n.chunk) + rs
	n.nls = lnl + n.nl + rnl
	n.rns = lr + n.runes + rr
}

// newRopeNode returns a node that holds chunk, with its counts.
func (r *rope) newRopeNode(prio uint64, chunk []byte) *ropeNode {
	r.work.countBytes += len(chunk)
	n := &ropeNode{prio: prio, chunk: chunk, nl: bytes.Count(chunk, []byte{'\n'}), runes: utf8.RuneCount(chunk)}
	n.update()
	return n
}

// rope is the held text.
type rope struct {
	root *ropeNode
	rnd  *prng
	work *workCounters
}

func (r *rope) len() int { return r.root.sz() }

// split returns the bytes before off and the bytes from off on. A chunk that
// holds off is sliced in two.
func (r *rope) split(n *ropeNode, off int) (*ropeNode, *ropeNode) {
	if n == nil {
		return nil, nil
	}
	r.work.ropeNodes++
	ls := n.left.sz()
	switch {
	case off <= ls:
		a, b := r.split(n.left, off)
		n.left = b
		n.update()
		return a, n
	case off >= ls+len(n.chunk):
		a, b := r.split(n.right, off-ls-len(n.chunk))
		n.right = a
		n.update()
		return n, b
	}
	// off is inside the chunk of n: slice it, with no copy. The second half
	// takes the priority of n, so it is not above any ancestor of n, and it
	// is not below any node of the right subtree of n: both trees stay in
	// heap order. off is not inside a valid character, so the runes of the
	// two halves add up to those of the chunk. The counts of the smaller half
	// are counted, and those of the other half are the difference.
	k := off - ls
	var right *ropeNode
	if k <= len(n.chunk)-k {
		left := r.newRopeNode(n.prio, n.chunk[:k:k])
		right = &ropeNode{prio: n.prio, chunk: n.chunk[k:], nl: n.nl - left.nl, runes: n.runes - left.runes}
		n.chunk, n.nl, n.runes = left.chunk, left.nl, left.runes
	} else {
		right = r.newRopeNode(n.prio, n.chunk[k:])
		n.chunk, n.nl, n.runes = n.chunk[:k:k], n.nl-right.nl, n.runes-right.runes
	}
	right.update()
	rightTree := n.right
	n.right = nil
	n.update()
	return n, r.merge(right, rightTree)
}

func (r *rope) merge(a, b *ropeNode) *ropeNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	r.work.ropeNodes++
	if a.prio > b.prio {
		a.right = r.merge(a.right, b)
		a.update()
		return a
	}
	b.left = r.merge(a, b.left)
	b.update()
	return b
}

// lastChunk returns the length of the last chunk of n, or 0.
func (r *rope) lastChunk(n *ropeNode) int {
	for n != nil {
		r.work.ropeNodes++
		if n.right == nil {
			return len(n.chunk)
		}
		n = n.right
	}
	return 0
}

// firstChunk returns the length of the first chunk of n, or 0.
func (r *rope) firstChunk(n *ropeNode) int {
	for n != nil {
		r.work.ropeNodes++
		if n.left == nil {
			return len(n.chunk)
		}
		n = n.left
	}
	return 0
}

// build returns a tree that holds the bytes of the pieces, in order, in new
// chunks. It copies every byte it holds.
func (r *rope) build(pieces ...[]byte) *ropeNode {
	total := 0
	for _, p := range pieces {
		total += len(p)
	}
	if total == 0 {
		return nil
	}
	buf := make([]byte, 0, total)
	for _, p := range pieces {
		buf = append(buf, p...)
	}
	r.work.textBytes += total

	// A chunk ends at a character boundary: a valid character that would
	// cross the end of a chunk goes into the next chunk.
	nodes := make([]*ropeNode, 0, (total+maxChunk-1)/maxChunk)
	for len(buf) > 0 {
		k := min(len(buf), maxChunk)
		if c, _, inside := charAt(buf, k); inside && k < len(buf) {
			k = c
		}
		nodes = append(nodes, r.newRopeNode(r.rnd.next(), buf[:k:k]))
		buf = buf[k:]
	}
	return buildRope(nodes)
}

// buildRope makes a treap of the nodes, in order, in O(len(nodes)): the
// right spine of the tree is kept on a stack.
func buildRope(nodes []*ropeNode) *ropeNode {
	var spine []*ropeNode
	for _, n := range nodes {
		var last *ropeNode
		for len(spine) > 0 && spine[len(spine)-1].prio < n.prio {
			last = spine[len(spine)-1]
			spine = spine[:len(spine)-1]
			last.update()
		}
		n.left = last
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

// replace replaces the bytes [start, end) with text. A small chunk on either
// side of the edit is joined to the new bytes, so that a run of small edits
// does not leave a run of small chunks.
//
// start and end are not inside a valid character of the held text. The new
// bytes can still complete a character with the bytes next to them, as when
// "\xc3" before the edit and "\xb1" in text make "ñ". So the bytes that are
// rebuilt start at the first byte of a character that is not complete before
// start, and they end after a valid character that crosses end in the new
// text. Then no valid character crosses the boundary of two chunks.
func (r *rope) replace(start, end int, text []byte) {
	pre := r.read(max(0, start-(utf8.UTFMax-1)), start)
	k := len(pre)
	for i := range pre {
		if pre[i] >= 0xc0 && !utf8.FullRune(pre[i:]) {
			k = i
			break
		}
	}
	pre = pre[k:]
	lo := start - len(pre)
	head := make([]byte, 0, len(pre)+len(text))
	head = append(append(head, pre...), text...)
	post := r.read(end, min(r.len(), end+utf8.UTFMax-1))
	hi := end
	tail := head[max(0, len(head)-(utf8.UTFMax-1)):]
	if c, size, inside := charAt(append(tail[:len(tail):len(tail)], post...), len(tail)); inside {
		hi = end + c + size - len(tail)
	}
	mid := append(head, post[:hi-end]...)

	left, rest := r.split(r.root, lo)
	_, right := r.split(rest, hi-lo)

	var before, after []byte
	if k := r.lastChunk(left); k > 0 && k+len(mid) <= maxChunk/2 {
		var last *ropeNode
		left, last = r.split(left, left.sz()-k)
		before = last.chunk
	}
	if k := r.firstChunk(right); k > 0 && len(before)+len(mid)+k <= maxChunk/2 {
		var first *ropeNode
		first, right = r.split(right, k)
		after = first.chunk
	}
	r.root = r.merge(r.merge(left, r.build(before, mid, after)), right)
}

// read copies the bytes [start, end) of the text into a new slice.
func (r *rope) read(start, end int) []byte {
	out := make([]byte, 0, end-start)
	it := r.iter(start)
	for len(out) < end-start {
		c := it.next()
		if c == nil {
			break
		}
		out = append(out, c[:min(len(c), end-start-len(out))]...)
	}
	return out
}

// byteAt returns the byte at off.
func (r *rope) byteAt(off int) byte {
	n := r.root
	for n != nil {
		r.work.ropeNodes++
		ls := n.left.sz()
		switch {
		case off < ls:
			n = n.left
		case off < ls+len(n.chunk):
			return n.chunk[off-ls]
		default:
			off -= ls + len(n.chunk)
			n = n.right
		}
	}
	return 0
}

// ropeIter returns the chunks of the text from an offset on, in order.
type ropeIter struct {
	r     *rope
	stack []*ropeNode // nodes whose chunk and right subtree come next
	first []byte      // the rest of the chunk that holds the start offset
}

// iter returns an iterator from off. It costs one path from the root.
func (r *rope) iter(off int) *ropeIter {
	it := &ropeIter{r: r}
	n := r.root
	for n != nil {
		r.work.ropeNodes++
		ls := n.left.sz()
		switch {
		case off < ls:
			it.stack = append(it.stack, n)
			n = n.left
		case off < ls+len(n.chunk):
			it.first = n.chunk[off-ls:]
			it.pushLeft(n.right)
			return it
		default:
			off -= ls + len(n.chunk)
			n = n.right
		}
	}
	return it
}

func (it *ropeIter) pushLeft(n *ropeNode) {
	for n != nil {
		it.r.work.ropeNodes++
		it.stack = append(it.stack, n)
		n = n.left
	}
}

// next returns the next chunk, or nil at the end of the text.
func (it *ropeIter) next() []byte {
	if it.first != nil {
		c := it.first
		it.first = nil
		return c
	}
	if len(it.stack) == 0 {
		return nil
	}
	n := it.stack[len(it.stack)-1]
	it.stack = it.stack[:len(it.stack)-1]
	it.pushLeft(n.right)
	return n.chunk
}

// ropeReader reads the text from an offset to its end. It returns at most one
// chunk for each call, so the cursor reads only what the scanner asks for.
type ropeReader struct {
	it  *ropeIter
	cur []byte
}

func (rd *ropeReader) Read(p []byte) (int, error) {
	for len(rd.cur) == 0 {
		rd.cur = rd.it.next()
		if rd.cur == nil {
			return 0, io.EOF
		}
	}
	n := copy(p, rd.cur)
	rd.cur = rd.cur[n:]
	return n, nil
}

// prng is a xorshift generator for the priorities of the treaps. It is seeded
// with a constant, so a lexer builds the same trees for the same edits.
type prng struct{ s uint64 }

func (g *prng) next() uint64 {
	g.s ^= g.s << 13
	g.s ^= g.s >> 7
	g.s ^= g.s << 17
	return g.s
}

// count returns the newlines and the runes of chunk[:k]. It reads the shorter
// side of k and takes the other side from the counts of the chunk.
func (r *rope) count(n *ropeNode, k int) (nls, rns int) {
	if k <= len(n.chunk)-k {
		r.work.countBytes += k
		return bytes.Count(n.chunk[:k], []byte{'\n'}), utf8.RuneCount(n.chunk[:k])
	}
	r.work.countBytes += len(n.chunk) - k
	return n.nl - bytes.Count(n.chunk[k:], []byte{'\n'}), n.runes - utf8.RuneCount(n.chunk[k:])
}

// prefix returns the newlines and the runes of the bytes [0, off). off is not
// inside a valid character. It costs one path from the root and a part of one
// chunk.
func (r *rope) prefix(off int) (nls, rns int) {
	n := r.root
	for n != nil {
		r.work.ropeNodes++
		ls, lnl, lr := n.left.sums()
		switch {
		case off < ls:
			n = n.left
		case off <= ls+len(n.chunk):
			cnl, cr := r.count(n, off-ls)
			return nls + lnl + cnl, rns + lr + cr
		default:
			nls += lnl + n.nl
			rns += lr + n.runes
			off -= ls + len(n.chunk)
			n = n.right
		}
	}
	return nls, rns
}

// afterNewline returns the offset just after the newline number j, counted
// from 1, and the runes before that offset. j is at least 1 and at most the
// newlines of the text.
func (r *rope) afterNewline(j int) (off, rns int) {
	n := r.root
	for n != nil {
		r.work.ropeNodes++
		ls, lnl, lr := n.left.sums()
		switch {
		case j <= lnl:
			n = n.left
		case j <= lnl+n.nl:
			j -= lnl
			i := 0
			for ; ; i++ {
				if n.chunk[i] == '\n' {
					if j--; j == 0 {
						break
					}
				}
			}
			_, cr := r.count(n, i+1)
			return off + ls + i + 1, rns + lr + cr
		default:
			j -= lnl + n.nl
			off += ls + len(n.chunk)
			rns += lr + n.runes
			n = n.right
		}
	}
	return off, rns
}

// runeStart returns the byte offset of the rune number rn, counted from 0, or
// the length of the text when rn is the number of runes of the text.
func (r *rope) runeStart(rn int) int {
	off := 0
	n := r.root
	for n != nil {
		r.work.ropeNodes++
		ls, _, lr := n.left.sums()
		switch {
		case rn < lr:
			n = n.left
		case rn < lr+n.runes:
			rn -= lr
			i := 0
			for ; rn > 0; rn-- {
				_, size := utf8.DecodeRune(n.chunk[i:])
				i += size
			}
			r.work.countBytes += i
			return off + ls + i
		default:
			rn -= lr + n.runes
			off += ls + len(n.chunk)
			n = n.right
		}
	}
	return off
}
