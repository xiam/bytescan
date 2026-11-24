package cursor

import (
	"errors"
	"fmt"
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

// The lean read path of a Reader has the commit operation and the boundary
// operations of the scanner.
var (
	_ leanread.Advancer[Pos]              = (*leanReader)(nil)
	_ leanread.Boundary[Pos, *Checkpoint] = (*leanReader)(nil)
)

// leanReader is a Reader seen through its lean read path. Its only exported
// methods are Peek, Epoch, Advance, Mark and AdvanceRelease, and only package
// internal/leanread names the interfaces that they implement
// (leanread.Reader, leanread.Advancer and leanread.Boundary).
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

// errAdvanceMismatch is the error of Advance, with leanread.Check set, when
// the Extent of its caller gives another position than the bytes do.
var errAdvanceMismatch = errors.New("cursor: the extent of the caller gives another position than the bytes")

// Advance implements leanread.Advancer for the positions of this package. It
// holds the reader's lock once, and it reads nothing from the source: Peek
// left the n bytes in the buffer. It still scans them into the line table of
// the position, which a later Rewind of the position reads, and that scan
// reports the line, the column and the byte offset before the move. The
// position after the move comes from them and from ext: no second decode for
// the rune offset, and no other read of the position's state.
//
// A move of no byte, or over bytes that the buffer does not hold, is a Seek,
// with its errors.
func (l *leanReader) Advance(n int, ext leanread.Extent) (Pos, error) {
	t := (*Reader)(l)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ctr.lock()

	return t.advanceLocked(n, ext)
}

// advanceLocked is Advance with the reader's lock held.
func (t *Reader) advanceLocked(n int, ext leanread.Extent) (Pos, error) {
	if n <= 0 || n > t.w-t.r {
		if _, err := t.seekLocked(int64(n), io.SeekCurrent); err != nil {
			return Pos{}, err
		}
		return t.cursorLocked(), nil
	}

	if headroom := t.retentionHeadroom(); headroom >= 0 && n > headroom {
		return Pos{}, ErrRetentionExceeded
	}

	// As in Seek, a move settles a pending partial sequence first.
	t.flushCarry()

	b := t.buf[t.r : t.r+n]
	line, column, offset := t.scanLocked(b)

	pos := Pos{
		byteOffset: offset + n,
		runeOffset: t.runeOffset + ext.Runes,
		line:       line + ext.Lines,
		column:     column + ext.Runes,
	}
	if ext.Lines > 0 {
		pos.column = ext.Column
	}

	if leanread.Check {
		want := t.cursorLocked()
		want.runeOffset = t.runeOffset + t.runeCount(b)
		if pos != want {
			// The line table moved; the test that set Check fails.
			return Pos{}, fmt.Errorf("%w: %+v from the extent, %+v from the bytes", errAdvanceMismatch, pos, want)
		}
	}

	t.runeOffset = pos.runeOffset
	t.r += n
	t.lastRuneSize = -1

	return pos, nil
}

// AdvanceRelease implements leanread.Boundary. For a checkpoint of this reader
// (the scanner's own marks), it is Advance followed by old.Release, under one
// lock of the reader. It releases old only when the move succeeds and old is
// active on this reader. Otherwise old is left as it is: unlike old.Release, it
// does not release a checkpoint that is active on another reader.
func (l *leanReader) AdvanceRelease(n int, ext leanread.Extent, old *Checkpoint) (Pos, error) {
	t := (*Reader)(l)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ctr.lock()

	pos, err := t.advanceLocked(n, ext)
	if err != nil {
		return Pos{}, err
	}

	if old != nil && old.reader.Load() == t {
		// As in Release, the value is verified under its own lock. The
		// lock is held once, for the check and for the release.
		old.mu.Lock()
		if old.active && old.reader.Load() == t {
			t.dropCheckpointLocked(old)
			old.active = false
			old.mu.Unlock()
			old.reader.Store(nil)
		} else {
			old.mu.Unlock()
		}
	}

	return pos, nil
}

// Mark implements leanread.Boundary. It is Remark, and it returns the
// position that it marked, which Pos on c returns after it. It holds the
// reader's lock once, as Remark does, and does not lock c again for Pos.
func (l *leanReader) Mark(c *Checkpoint) (Pos, error) {
	return (*Reader)(l).remark(c)
}
