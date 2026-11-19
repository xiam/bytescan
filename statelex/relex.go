package statelex

import (
	"bytes"
	"io"
	"unicode/utf8"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/modedef"
	"github.com/xiam/bytescan/modes"
)

// apply replaces the old bytes [a, b) with text and lexes again with the four
// steps of docs/specs/resumable-lexing.md. The edit is already checked.
func (lx *Lexer) apply(a, b int, text []byte) (Change, error) {
	lx.work = workCounters{}
	oldLen := lx.text.len()
	n := len(text)
	d := n - (b - a)

	// Step 1: restart. The clause for broken bytes moves the offset of the
	// reach test back to the first byte of a character that is not complete
	// before a. The test for an edit at the end of the text uses a.
	ra := a
	before := lx.text.read(max(0, a-(utf8.UTFMax-1)), a)
	for i := range before {
		if before[i] >= 0xc0 && !utf8.FullRune(before[i:]) {
			ra = a - len(before) + i
			break
		}
	}
	var restart int
	var stack modes.Stack
	if first, start := lx.toks.firstAffected(ra, a == oldLen); first != nil && start < b {
		restart, stack = start, first.stack
	} else {
		// No token is affected, so ra is the end of the last token that ends
		// at or before it: a token that holds ra inside it would be affected.
		restart = ra
		switch tok := lx.toks.at(restart); {
		case tok != nil:
			stack = tok.stack
		case restart == oldLen:
			stack = lx.endSt
		default:
			return Change{}, lx.fail("no token starts at the restart point %d", restart)
		}
	}

	// The old bytes that the edit removes, and the BOL flag at the old offset
	// b: the trim and the sync test read them after the held text changed.
	// The sync test reads the old flags only at b (see isSync).
	removed := lx.text.read(a, b)
	lx.work.textBytes += len(removed)
	oldLineStartAtB := b == 0
	switch {
	case b > a:
		oldLineStartAtB = removed[len(removed)-1] == '\n'
	case b > 0:
		oldLineStartAtB = lx.text.byteAt(b-1) == '\n'
	}

	lx.text.replace(a, b, text)
	newLen := lx.text.len()

	if lx.testHook != nil {
		if err := lx.testHook(); err != nil {
			return Change{}, lx.fail("%v", err)
		}
	}

	// lineStart reports the BOL flag of the first symbol at the new offset p:
	// p is 0, or the byte before it is a newline.
	lineStart := func(p int) bool {
		return p == 0 || lx.text.byteAt(p-1) == '\n'
	}

	// Step 2: lex forward from the restart point.
	if err := lx.cur.ResetStart(&ropeReader{it: lx.text.iter(restart)}, cursor.Start{
		InText:    restart > 0,
		LineStart: lineStart(restart),
	}); err != nil {
		return Change{}, lx.fail("reset the cursor: %v", err)
	}

	// Step 3: test for sync at each boundary p >= a+n. The old tokens from b
	// on are walked in order, next to the new boundaries.
	olds := lx.toks.iter(lx.toks.root, b)
	oldTok, oldStart := olds.next()
	isSync := func(p int, st modes.Stack) bool {
		if p < a+n {
			return false
		}
		q := p - d
		for oldTok != nil && oldStart < q {
			oldTok, oldStart = olds.next()
		}
		if oldTok == nil || oldStart != q {
			return false
		}
		if !oldTok.stack.Equal(st) {
			return false
		}
		// The flags of the first symbol: BOF at offset 0, BOL at offset 0 or
		// after a newline. After a+n they are equal: p and q are not 0, and
		// the byte before p is the byte before q, in the unchanged text.
		if p > a+n {
			return true
		}
		return (p == 0) == (q == 0) && lineStart(p) == oldLineStartAtB
	}

	var news []*tokNode
	p := restart
	synced := isSync(p, stack)
	for !synced {
		lm := lx.modes[stack.Top()]
		tok, la, err := lm.sc.NextLookahead()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Change{}, lx.fail("lex at %d: %v", p, err)
		}
		span := tok.Span()
		start, length := restart+span.Start().ByteOffset(), span.Bytes()
		if start != p || length <= 0 || p+length > newLen {
			return Change{}, lx.fail("token [%d,%d) at the boundary %d of a text of %d bytes", start, start+length, p, newLen)
		}
		news = append(news, &tokNode{typ: tok.Type(), length: length, after: la.After, readEnd: la.ReadEnd, stack: stack})
		stack = lx.next(stack, lm, tok.Type())
		p += length
		// Step 4: stop on the first sync point.
		synced = isSync(p, stack)
	}
	q := p - d
	if !synced {
		if p != newLen {
			return Change{}, lx.fail("the scan ended at %d in a text of %d bytes", p, newLen)
		}
		lx.endSt = stack
		q = oldLen
	}
	lx.lastRun = relexRun{restart: restart, end: p, syncOld: q, synced: synced}

	// Replace the old run [restart, q) with the new run [restart, p).
	left, rest := lx.toks.split(lx.toks.root, restart)
	oldRun, right := lx.toks.split(rest, q-restart)
	var gone []*tokNode
	var goneStarts []int
	for it := lx.toks.iter(oldRun, 0); ; {
		t, s := it.next()
		if t == nil {
			break
		}
		gone = append(gone, t)
		goneStarts = append(goneStarts, restart+s)
	}
	lx.toks.root = lx.toks.merge(lx.toks.merge(left, lx.toks.build(news)), right)

	// The bytes of the two runs, for the trim. The old run is the new run with
	// the removed bytes in place of the new ones.
	newBytes := lx.text.read(restart, p)
	oldBytes := make([]byte, 0, q-restart)
	oldBytes = append(oldBytes, newBytes[:a-restart]...)
	oldBytes = append(oldBytes, removed...)
	oldBytes = append(oldBytes, newBytes[a+n-restart:]...)

	return trim(restart, d, gone, goneStarts, oldBytes, news, newBytes), nil
}

// next returns the mode state after a token of type typ that the mode lm
// made. A token that no rule of the mode made leaves the state unchanged.
func (lx *Lexer) next(st modes.Stack, lm *lexMode, typ bytescan.TokenType) modes.Stack {
	r, ok := lm.actions[typ]
	if !ok {
		return st
	}
	switch r.Action {
	case modedef.Push:
		st = modedef.PushMode(st, r.Next).(modes.Stack)
	case modedef.Pop:
		st = modedef.PopMode(st).(modes.Stack)
	default:
		return st
	}
	if lx.modes[st.Top()] != lm {
		lm.sc.Release()
	}
	return st
}

// trim returns the change of a re-lexed run: the old tokens and the new
// tokens from the restart point, without the tokens at both ends that have the
// same type, the same range (moved by d at the end) and the same bytes.
// oldBytes and newBytes are the bytes of the two runs, from the restart point.
func trim(restart, d int, olds []*tokNode, oldStarts []int, oldBytes []byte, news []*tokNode, newBytes []byte) Change {
	newStarts := make([]int, len(news))
	for i, p := 0, restart; i < len(news); i++ {
		newStarts[i] = p
		p += news[i].length
	}
	same := func(i, j int, moved int) bool {
		o, w := olds[i], news[j]
		os, ws := oldStarts[i]-restart, newStarts[j]-restart
		return o.typ == w.typ && o.length == w.length && oldStarts[i]+moved == newStarts[j] &&
			bytes.Equal(oldBytes[os:os+o.length], newBytes[ws:ws+w.length])
	}

	lo := 0
	for lo < len(olds) && lo < len(news) && same(lo, lo, 0) {
		lo++
	}
	oe, ne := len(olds), len(news)
	for oe > lo && ne > lo && same(oe-1, ne-1, d) {
		oe--
		ne--
	}

	start := restart
	if lo > 0 {
		start = newStarts[lo-1] + news[lo-1].length
	}
	c := Change{Start: start, OldEnd: start, NewEnd: start}
	for i := lo; i < oe; i++ {
		c.Removed = append(c.Removed, Token{Type: olds[i].typ, Start: oldStarts[i], End: oldStarts[i] + olds[i].length})
		c.OldEnd = oldStarts[i] + olds[i].length
	}
	for j := lo; j < ne; j++ {
		c.Inserted = append(c.Inserted, Token{Type: news[j].typ, Start: newStarts[j], End: newStarts[j] + news[j].length})
		c.NewEnd = newStarts[j] + news[j].length
	}
	return c
}
