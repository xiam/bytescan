package cursor

import (
	"errors"
	"io"
	"unicode/utf8"

	"github.com/xiam/bytescan/cursor/position"
)

// Start describes where a source begins in a larger text. ResetStart takes it
// when a reader is reset onto a source that starts in the middle of a text.
//
// The reader reports positions from the start of the new source, so a rule
// cannot tell from the position alone where the source begins. Start carries
// the two facts that a rule can see at the first symbol:
//
//   - InText reports that the source does not begin at offset 0 of the text.
//     The first symbol then is not the beginning of the file.
//   - LineStart reports that the byte before the source is '\n'. The first
//     symbol then is the beginning of a line.
//
// A source that is the whole text has InText false and LineStart true, because
// offset 0 of a text is the beginning of a line. A reader that is never reset
// reports that Start. The scanner uses the two fields as they are given, so a
// Start with both fields false gives a first symbol with the BOF flag and
// without the BOL flag.
type Start struct {
	InText    bool
	LineStart bool
}

var errResetNilSource = errors.New("cursor: reset onto a nil source")

// ResetStart resets the reader onto src, which begins at the place in a text
// that s describes.
//
// After the reset the reader reads src from its first byte and reports
// positions from there: byte offset 0, rune offset 0, line 1 and column 0. The
// buffered input, the sticky read error and any partial UTF-8 sequence of the
// old source are dropped, also a multi-byte character that the old source cut
// with ErrMoreInput, and a rune that a rune source held back. ErrMoreInput is
// not sticky, so no "more input" condition survives the reset. The options of the reader (WithCapacity,
// WithMaxRetained) and byte mode stay. A reader built with NewRuneReader reads
// src as UTF-8 bytes after the reset.
//
// ResetStart does not check s against src: src is the text from the start on,
// and s is what the caller knows about the bytes before it. An empty src gives
// an empty source.
//
// The reset releases every active checkpoint, because the input that it would
// replay is gone. A released checkpoint can be re-armed with Remark after the
// reset. A scanner on the reader keeps working: its next token arms a new mark
// at the start of the new source.
//
// ResetStart fails, and changes nothing, when src is nil.
func (t *Reader) ResetStart(src io.Reader, s Start) error {
	if src == nil {
		return errResetNilSource
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, c := range t.checkpoints {
		c.mu.Lock()
		c.active = false
		c.mu.Unlock()
		c.reader.Store(nil)
	}
	clear(t.checkpoints)
	t.checkpoints = t.checkpoints[:0]

	t.br = src
	t.runeSrc = nil
	t.pos = position.New()
	t.runeOffset = 0
	t.lastRuneSize = -1
	t.r = 0
	t.w = 0
	t.readErr = nil
	t.pendingRune = 0
	t.hasPending = false
	t.utf8Carry = [utf8.UTFMax]byte{}
	t.utf8CarryLen = 0
	t.start = s

	return nil
}

// Start returns the Start that the last ResetStart stored. A reader that is
// never reset returns Start{LineStart: true}: its source is the whole text.
func (t *Reader) Start() Start {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.start
}
