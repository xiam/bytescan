package statelex

import (
	"fmt"
	"unicode/utf8"

	"github.com/xiam/bytescan/modes"
)

// clip clips the range [start, end) to [0, Len()].
func (lx *Lexer) clip(start, end int) (int, int) {
	n := lx.text.len()
	return min(max(start, 0), n), min(max(end, 0), n)
}

// Text returns a copy of the held bytes in [start, end). The range is clipped
// to [0, Len()]. An empty or an inverted range gives no byte. It costs one
// path of the rope, plus the bytes returned.
func (lx *Lexer) Text(start, end int) []byte {
	start, end = lx.clip(start, end)
	if start >= end {
		return nil
	}
	return lx.text.read(start, end)
}

// Tokens returns, in order, every held token that overlaps [start, end), whole,
// with its full byte range. The range is clipped to [0, Len()]. An empty or an
// inverted range gives no token. It costs one path of the token tree, plus the
// tokens returned.
func (lx *Lexer) Tokens(start, end int) []Token {
	start, end = lx.clip(start, end)
	if start >= end {
		return nil
	}
	var toks []Token
	it := lx.toks.iter(lx.toks.root, lx.toks.startOf(start))
	for {
		n, s := it.next()
		if n == nil || s >= end {
			return toks
		}
		toks = append(toks, Token{Type: n.typ, Start: s, End: s + n.length})
	}
}

// ModeAtEnd returns the mode state after the last held token: the stack that
// holds only the start mode for an empty text. The lexer keeps it after each
// edit.
func (lx *Lexer) ModeAtEnd() modes.Stack {
	return lx.endSt
}

// Unfinished returns the start of the first held token whose rules read the
// end of the text, and true. When the text ends inside a UTF-8 character that
// is not complete, it also returns true; the start is then the earliest of the
// start of that character, the start of the token that holds that character,
// and the start of a token whose rules read the end of the text. Otherwise it
// returns 0 and false. It costs one path of the token tree: each node keeps
// whether a token below it read the end.
func (lx *Lexer) Unfinished() (start int, ok bool) {
	start, ok = lx.toks.firstEnd()
	n := lx.text.len()
	tail := lx.text.read(max(0, n-(utf8.UTFMax-1)), n)
	for i := range tail {
		if tail[i] >= 0xc0 && !utf8.FullRune(tail[i:]) {
			if c := lx.toks.startOf(n - len(tail) + i); !ok || c < start {
				start = c
			}
			return start, true
		}
	}
	return start, ok
}

// Position returns the line, counted from 1, and the rune column, counted
// from 0, of the byte offset offset, as a cursor counts them: a newline ends a
// line, and a byte that does not start a valid UTF-8 character is one rune.
// Len() is a place in the text. It returns an error that matches ErrRange when
// offset is outside [0, Len()], and an error that matches ErrSplitRune when
// offset falls inside a valid UTF-8 character. After an internal error (see
// Apply) it returns that error. It costs one path of the rope
// and a part of one or two chunks, in either direction.
func (lx *Lexer) Position(offset int) (line, column int, err error) {
	if lx.err != nil {
		return 0, 0, lx.err
	}
	n := lx.text.len()
	if offset < 0 || offset > n {
		return 0, 0, fmt.Errorf("%w: offset %d in a text of %d bytes", ErrRange, offset, n)
	}
	if lx.splitsChar(offset) {
		return 0, 0, fmt.Errorf("%w: offset %d", ErrSplitRune, offset)
	}
	nls, rns := lx.text.prefix(offset)
	lineRunes := 0
	if nls > 0 {
		_, lineRunes = lx.text.afterNewline(nls)
	}
	return nls + 1, rns - lineRunes, nil
}

// Offset returns the byte offset of a line, counted from 1, and a rune
// column, counted from 0. The column can be the number of runes of the line:
// the place of the newline that ends it, or Len() on the last line. It
// returns an error that matches ErrRange for a line that the text does not
// have, a negative column, or a column past the end of its line. After an
// internal error (see Apply) it returns that error. Offset is the
// inverse of Position at every place that Position accepts.
func (lx *Lexer) Offset(line, column int) (int, error) {
	if lx.err != nil {
		return 0, lx.err
	}
	_, nls, rns := lx.text.root.sums()
	if line < 1 || line > nls+1 || column < 0 {
		return 0, fmt.Errorf("%w: line %d, column %d in a text of %d lines", ErrRange, line, column, nls+1)
	}
	first := 0
	if line > 1 {
		_, first = lx.text.afterNewline(line - 1)
	}
	last := rns // the runes before the end of the line
	if line <= nls {
		_, last = lx.text.afterNewline(line)
		last-- // the newline
	}
	if column > last-first {
		return 0, fmt.Errorf("%w: line %d has %d columns, not %d", ErrRange, line, last-first, column)
	}
	return lx.text.runeStart(first + column), nil
}
