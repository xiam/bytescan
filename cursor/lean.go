package cursor

import (
	"errors"
	"io"
	"unicode/utf8"

	"github.com/xiam/bytescan/internal/leanread"
)

// This file registers the lean read path of a Reader (package
// internal/leanread). The scanner of this module uses it instead of one
// ReadLocatedRune per rune. The owner decided on 2026-11-21 ("Internal hook
// package") that cursor may import internal/leanread for this, and no other
// internal package (docs/specs/cursor.md, "Independence").

func init() {
	leanread.Hook = leanOf
}

// leanReader is a Reader seen through its lean read path. It has no exported
// method of its own beyond Peek, and only package internal/leanread names the
// interface that Peek implements.
type leanReader Reader

// leanOf returns the lean read path of v when v is a *Reader. A type that
// embeds a *Reader is not a *Reader, so it keeps the public read path, and its
// own methods are still called.
func leanOf(v any) (leanread.Reader, bool) {
	t, ok := v.(*Reader)
	if !ok || t == nil {
		return nil, false
	}
	return (*leanReader)(t), true
}

// Peek implements leanread.Reader. It holds the reader's lock once for the
// whole call. It decodes each rune as readRuneLocked would decode it at the
// same position, and it checks the retention limit as readRuneLocked would
// after a read of the skip bytes. It does not move the logical cursor, the line
// table or the rune offset, and it does not change the UnreadRune state.
//
// The caller holds a checkpoint at the logical cursor, as the scanner does for
// every token, so the bytes from the cursor onward stay in the buffer.
func (l *leanReader) Peek(skip int, dst []leanread.Rune) (int, leanread.At, error) {
	t := (*Reader)(l)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ctr.lock()

	if skip == 0 {
		// The position of the first rune includes a pending partial sequence,
		// as it does for readRuneLocked.
		t.flushCarry()
	}

	at := t.leanAtLocked(skip)

	if len(dst) == 0 {
		return 0, at, nil
	}

	err := t.fillRuneAtLocked(skip)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, at, err
	}

	i := t.r + skip
	if i >= t.w {
		return 0, at, io.EOF
	}

	headroom := t.retentionHeadroom()
	if headroom >= 0 {
		headroom -= skip
	}

	// The first rune was filled as readRuneLocked fills it. In text mode, a
	// later rune is decoded only while the buffer holds utf8.UTFMax bytes from
	// it: with fewer, fillRuneLocked would compact the buffer or read the
	// source before that rune, so the next Peek does it, and only when the
	// caller needs that rune. In byte mode fillRuneLocked does nothing while
	// the buffer holds a byte. So the buffer, the fills and the compactions
	// are the same as with one ReadLocatedRune per rune.
	n := 0
	for n < len(dst) && i < t.w {
		var (
			r    rune
			size int
		)
		switch c := t.buf[i]; {
		case t.byteMode:
			r, size = rune(c), 1
		case n > 0 && t.w-i < utf8.UTFMax:
			return n, at, nil
		case c < utf8.RuneSelf:
			r, size = rune(c), 1
		default:
			if !utf8.FullRune(t.buf[i:t.w]) {
				// Only the first rune can be incomplete: a later one is
				// decoded only with utf8.UTFMax bytes after it.
				at.Partial = true
			}
			r, size = utf8.DecodeRune(t.buf[i:t.w])
		}

		if headroom >= 0 {
			if size > headroom {
				if n == 0 {
					return 0, at, ErrRetentionExceeded
				}
				return n, at, nil
			}
			headroom -= size
		}

		dst[n] = leanread.Rune{Rune: r, Size: size}
		n++
		i += size
	}

	return n, at, nil
}

// leanAtLocked describes the position skip bytes after the logical cursor. At
// skip 0 it is the logical cursor. After it, the position is at column 0 only
// when the byte before it is a newline: a 0x0A byte is always one rune, in text
// mode and in byte mode, and every other rune moves the column.
func (t *Reader) leanAtLocked(skip int) leanread.At {
	if skip == 0 {
		_, column, offset := t.pos.State()
		return leanread.At{
			Start:     offset == 0 && t.runeOffset == 0,
			LineStart: column == 0,
		}
	}
	return leanread.At{LineStart: t.buf[t.r+skip-1] == '\n'}
}

// Epoch implements leanread.Reader. It counts the calls of ResetStart and the
// marks made by Checkpoint and Remark, and it takes no lock.
func (l *leanReader) Epoch() uint64 {
	return (*Reader)(l).epoch.Load()
}
