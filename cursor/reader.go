// Package cursor provides a buffered reader that tracks the exact position of
// every rune it reads: the byte offset, the rune offset, the line and the
// column.
//
// The package is part of the bytescan module but does not depend on the rest
// of it: its non-test files import only the standard library and
// cursor/position. Any program can use it on its own, without the scanner.
//
// The package has two layers.
//
// The basic layer is the sequential reader: Read, ReadRune, UnreadRune, Seek,
// and Pos. It tracks the position of the next read and is sufficient for a
// caller that consumes the stream once, in order.
//
// The cursor layer adds the mechanics a speculative consumer (a lexer, a
// hand-written parser, a syntax highlighter) needs. It owns stream mechanics
// only and knows nothing about tokens, lexical rules, or longest-match
// selection. See cursor.go.
//
//   - Positions: Pos is an immutable position, Span an immutable half-open
//     range built with NewSpan, and LocatedRune a rune with the position it
//     was read from. Cursor reports the logical next-read position, never the
//     physical read-ahead.
//   - Checkpoints: Checkpoint marks the logical position; Reset returns to it
//     and replays the input exactly; Commit and Release end it. Remark re-arms
//     a released checkpoint without allocating.
//   - Retention: while a checkpoint is active, the reader retains every byte
//     from the oldest active checkpoint onward, so SpanText and Context can
//     return the retained text without moving the cursor. WithMaxRetained
//     bounds that region; a read that would exceed it fails with
//     ErrRetentionExceeded and consumes nothing.
//
// Text mode and byte mode. The text constructors (New, NewWithCapacity,
// NewReader, NewRuneReader) read UTF-8 text: the column counts runes and the
// byte offset counts bytes. NewByteReader reads binary input instead: each
// byte is one rune of size 1, so every position the reader reports is a file
// offset. In both modes the byte 0x0A ends a line.
package cursor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/xiam/bytescan/cursor/position"
)

const (
	defaultCapacity = 64 * 1024
)

var (
	ErrBufferTooSmall  = errors.New("buffer too small")
	ErrInvalidUTF8     = errors.New("invalid UTF-8 encoding")
	ErrSeekOutOfBuffer = errors.New("seek out of buffer")
)

// Reader reads from an io.Reader, buffering data and keeping track of the
// current position (line, column, and offset) in the text stream. It supports
// seeking only within the currently buffered data, which is good enough for
// giving context of the text around the current read position.
//
// A Reader is safe for concurrent use by multiple goroutines. Positions
// reported by the reader are immutable values; no method hands out a mutable
// reference to the reader's state.
type Reader struct {
	// br is the byte source. It is nil when the reader was built over an
	// io.RuneReader source, in which case runeSrc is set instead.
	br io.Reader
	// runeSrc is the rune source, used by NewRuneReader.
	runeSrc io.RuneReader

	// byteMode reads each input byte as one rune of size 1 (see
	// NewByteReader). It is fixed at construction. In byte mode the UTF-8
	// carry below is never used, and the rune offset equals the byte offset.
	byteMode bool

	mu sync.Mutex

	// pos tracks the logical position of the next read: bytes and runes
	// scanned so far, plus the line and rune column. It never describes
	// physical read-ahead.
	pos *position.Position

	// runeOffset is the absolute rune offset of the logical cursor. It is
	// maintained alongside pos because position.Position tracks bytes only.
	runeOffset int

	lastRuneSize int

	capacity int
	// maxRetained bounds the bytes held for checkpoint replay. Zero means no
	// bound. The limit applies only while a checkpoint is active: a basic
	// reader with no checkpoint never pays a retention cost and its buffer
	// compacts as usual.
	maxRetained int

	buf []byte

	// r and w are indices into buf. The absolute byte offset of buf[i] is
	// pos.Offset() - r + i; that invariant holds after every operation.
	r int
	w int

	// checkpoints holds the active checkpoints, oldest first.
	checkpoints []*Checkpoint

	// readErr is a sticky error returned by the underlying source. Replaying
	// from a checkpoint reproduces a reader error deterministically instead of
	// observing whatever the source reports on the second pass.
	readErr error

	// pendingRune holds a rune read from a rune source that did not fit the
	// room available in the buffer. It is encoded on the next fill, so a rune
	// is never dropped at a buffer boundary.
	pendingRune rune
	hasPending  bool

	// utf8Carry holds the leading bytes of an incomplete UTF-8 sequence seen
	// across byte reads. Their byte count is already reflected in the logical
	// position; their rune and column contribution is credited when the
	// sequence completes, which keeps rune coordinates exact when a Read splits
	// a rune.
	utf8Carry    [utf8.UTFMax]byte
	utf8CarryLen int

	// start describes where the source begins in a larger text. It is set by
	// ResetStart, and it is Start{LineStart: true} for a reader that is never
	// reset.
	start Start
}

// New returns a new Reader that reads from r with the default buffer
// capacity.
func New(r io.Reader) *Reader {
	return NewWithCapacity(r, defaultCapacity)
}

// NewWithCapacity returns a new Reader with a buffer of at least the
// specified capacity.
func NewWithCapacity(r io.Reader, capacity int) *Reader {
	return NewReader(r, WithCapacity(capacity))
}

// NewReader returns a new positioned cursor over r, configured by opts. It is
// the option-aware counterpart of New; New and NewWithCapacity remain the
// compatible constructors.
func NewReader(r io.Reader, opts ...Option) *Reader {
	t := newTextReader(opts...)
	t.br = r
	return t
}

// NewRuneReader returns a new positioned cursor over an io.RuneReader.
//
// The rune source is read one rune at a time and each rune is encoded back to
// UTF-8 in the reader's buffer, so byte offsets describe the stream the rune
// reader represents. This is not a second UTF-8 decoder: no bytes are decoded,
// the decoder itself is the caller's io.RuneReader.
func NewRuneReader(rr io.RuneReader, opts ...Option) *Reader {
	t := newTextReader(opts...)
	t.runeSrc = rr
	return t
}

// NewByteReader returns a new positioned cursor that reads each byte of r as
// one rune, U+0000 to U+00FF, of size 1. It accepts the same options as
// NewReader.
//
// In byte mode no input is decoded as UTF-8:
//
//   - ReadRune, ReadLocatedRune and PeekRune return rune(b) and size 1 for each
//     byte b, including bytes of 0x80 and above and bytes that would form valid
//     UTF-8 in text mode;
//   - ByteOffset and RuneOffset of every Pos are both the file offset;
//   - the byte 0x0A ends a line, and Column counts the bytes since the last
//     0x0A;
//   - Read, SpanText and Context return the raw input bytes;
//   - Seek, UnreadRune, Checkpoint, Reset, Remark and WithMaxRetained work on
//     file offsets, so a checkpoint over n bytes retains n bytes.
//
// Pos returns a position.Position that holds the same line, column and offset.
// Its Scan method decodes UTF-8, so a caller that scans more input on that
// copy gets text-mode columns for the scanned bytes.
func NewByteReader(r io.Reader, opts ...Option) *Reader {
	t := newTextReader(opts...)
	t.br = r
	t.byteMode = true
	return t
}

func newTextReader(opts ...Option) *Reader {
	t := &Reader{
		capacity:     defaultCapacity,
		pos:          position.New(),
		lastRuneSize: -1,
		start:        Start{LineStart: true},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.capacity < utf8.UTFMax {
		t.capacity = utf8.UTFMax
	}
	t.buf = make([]byte, t.capacity)
	return t
}

func (t *Reader) fillAtLeast(n int) (bool, error) {
	if n < 0 {
		return false, fmt.Errorf("invalid size: %d", n)
	}

	if n == 0 {
		// Nothing to do.
		return true, nil
	}

	if n > t.capacity && t.runeSrc == nil && len(t.checkpoints) == 0 {
		// The requested read is larger than the buffer this is not allowed.
		return false, ErrBufferTooSmall
	}

	// If we already have enough data in the buffer, just return.
	if n <= t.w-t.r {
		return true, nil
	}

	// The next read will be beyond the buffer, so we need to release space.
	// Compaction stops at the oldest active checkpoint: bytes a checkpoint may
	// still need to replay are never discarded.
	if t.r+n >= len(t.buf) {
		t.compact()
	}

	var readErr error

	for t.w-t.r < n && readErr == nil {
		// Make room for a rune held over from an earlier window, or for the
		// next read when a checkpoint still needs the bytes already buffered.
		if t.hasPending && len(t.buf)-t.w < encodedRuneLen(t.pendingRune) {
			if err := t.grow(t.w + encodedRuneLen(t.pendingRune)); err != nil {
				readErr = err
				break
			}
		}
		if t.w >= len(t.buf) && (t.runeSrc != nil || len(t.checkpoints) > 0) {
			if err := t.grow(len(t.buf) + 1); err != nil {
				readErr = err
				break
			}
		}

		var (
			bytesRead int
			err       error
		)
		bytesRead, err = t.readInto(t.buf[t.w:])
		t.w += bytesRead
		readErr = err

		// A rune source that read a rune too large for the room left holds it
		// back without writing a byte. That is progress: the next pass makes
		// room for the held rune and encodes it.
		if bytesRead == 0 && readErr == nil && !t.hasPending {
			readErr = io.ErrNoProgress
		}
	}

	if readErr == nil {
		return true, nil
	}

	if errors.Is(readErr, ErrMoreInput) {
		// The source has no input now. The error is not sticky, so it is
		// reported here and not deferred behind the buffered bytes: the caller
		// decides whether the bytes it has are enough.
		return t.w-t.r >= n, readErr
	}

	if !errors.Is(readErr, io.EOF) && t.w-t.r > 0 {
		// The source failed, but buffered bytes are still owed to the caller.
		// Serve them first: the sticky error surfaces once the buffer drains,
		// which is what makes replay from a checkpoint deterministic and keeps
		// a multi-rune fill from reporting a failure mid-buffer.
		return true, nil
	}

	return t.w-t.r >= n, readErr
}

// encodedRuneLen reports how many bytes enc holds once the rune is encoded to
// UTF-8.
func encodedRuneLen(r rune) int {
	if n := utf8.RuneLen(r); n > 0 {
		return n
	}
	// Invalid runes are encoded as the three-byte replacement character.
	return 3
}

// readInto performs one underlying read into p and records a sticky error.
func (t *Reader) readInto(p []byte) (int, error) {
	if t.readErr != nil {
		return 0, t.readErr
	}

	var (
		n   int
		err error
	)
	if t.runeSrc != nil {
		n, err = t.fillRunes(p)
	} else {
		n, err = t.br.Read(p)
	}

	// ErrMoreInput is not sticky: the source has no input now, and the next
	// read asks it again.
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, ErrMoreInput) {
		t.readErr = err
	}

	return n, err
}

// fillRunes reads one rune from the rune source and encodes it into p.
//
// It reads exactly one rune per call, so a live source is never asked for a
// rune before the cursor needs one: a fill returns as soon as the rune that
// arrived is buffered. A rune that does not fit the room left in p is held in
// the reader rather than dropped, and encoded on the next fill; the caller
// makes room for it or reports the retention limit.
func (t *Reader) fillRunes(p []byte) (int, error) {
	var r rune

	if t.hasPending {
		r, t.hasPending = t.pendingRune, false
	} else {
		var err error
		r, _, err = t.runeSrc.ReadRune()
		if err != nil {
			return 0, err
		}
	}

	if encodedRuneLen(r) > len(p) {
		t.pendingRune, t.hasPending = r, true
		return 0, nil
	}

	return utf8.EncodeRune(p, r), nil
}

// grow makes room for at least min bytes in the buffer.
//
// Growth is bounded by the retention budget, not here: a checkpoint only forces
// the reader to keep the retained region, and the cursor cannot advance past
// that budget, so the buffer settles instead of growing without bound.
func (t *Reader) grow(min int) error {
	if min <= len(t.buf) {
		return nil
	}

	newLen := len(t.buf)
	if newLen == 0 {
		newLen = utf8.UTFMax
	}
	for newLen < min {
		newLen *= 2
	}

	grown := make([]byte, newLen)
	copy(grown, t.buf[:t.w])
	t.buf = grown

	return nil
}

// retentionHeadroom reports how many more bytes the logical cursor may advance
// before the retained region would exceed the configured limit. It reports -1
// when no limit applies, which is the case whenever the limit is unset or no
// checkpoint is active.
func (t *Reader) retentionHeadroom() int {
	if t.maxRetained <= 0 || len(t.checkpoints) == 0 {
		return -1
	}

	return t.maxRetained - (t.pos.Offset() - t.retainFloor())
}

// retainFloor returns the absolute byte offset of the oldest byte the reader
// must keep. Without an active checkpoint nothing needs to be retained and the
// floor is the logical cursor itself.
func (t *Reader) retainFloor() int {
	floor := t.pos.Offset()

	for _, c := range t.checkpoints {
		if c == nil {
			continue
		}

		// A concurrent Pos or a Remark on another reader may be reading or
		// rewriting these fields right now, so the floor is sampled under
		// the value's own lock. The value is in this reader's active set,
		// so it is still armed on this reader.
		c.mu.Lock()
		if c.active && c.byteOffset < floor {
			floor = c.byteOffset
		}
		c.mu.Unlock()
	}

	return floor
}

// compact shifts the buffer forward, releasing bytes before the retention
// floor. It never advances past the logical cursor.
func (t *Reader) compact() {
	bufferStart := t.pos.Offset() - t.r

	shift := t.retainFloor() - bufferStart
	if shift <= 0 {
		return
	}
	if shift > t.r {
		shift = t.r
	}

	copy(t.buf, t.buf[shift:t.w])
	t.w -= shift
	t.r -= shift

	t.trimLines()
}

// trimLines drops the per-line position entries no Rewind can still need. The
// oldest byte a Rewind can reach is the physical buffer start: UnreadRune, a
// backward Seek and Checkpoint.Reset all move only within the buffer, and the
// buffer start is never above the oldest active checkpoint. It is called
// wherever the buffer start advances.
func (t *Reader) trimLines() {
	t.pos.TrimBefore(t.pos.Offset() - t.r)
}

// accountBytes advances the logical position over b, which continues the stream
// at the current position.
//
// The byte offset counts every byte immediately. A trailing incomplete UTF-8
// sequence is carried instead: its bytes are already counted, and its rune and
// column contribution is credited once the sequence completes. That keeps rune
// coordinates exact when a byte read splits a rune.
func (t *Reader) accountBytes(b []byte) {
	if t.byteMode {
		t.scanLocked(b)
		t.runeOffset += len(b)
		return
	}

	if t.utf8CarryLen > 0 {
		need := encodedLeadLen(t.utf8Carry[0])

		if t.utf8CarryLen+len(b) < need {
			t.pos.AdvanceBytes(len(b))
			copy(t.utf8Carry[t.utf8CarryLen:], b)
			t.utf8CarryLen += len(b)
			return
		}

		take := need - t.utf8CarryLen
		copy(t.utf8Carry[t.utf8CarryLen:], b[:take])

		// Undo the provisional byte advance for the carried bytes and account
		// the completed sequence as a whole.
		t.pos.AdvanceBytes(-t.utf8CarryLen)
		t.pos.Scan(t.utf8Carry[:need])
		t.runeOffset += utf8.RuneCount(t.utf8Carry[:need])
		t.utf8CarryLen = 0

		b = b[take:]
	}

	i := 0
	for i < len(b) && utf8.FullRune(b[i:]) {
		_, size := utf8.DecodeRune(b[i:])
		i += size
	}

	t.pos.Scan(b[:i])
	t.runeOffset += utf8.RuneCount(b[:i])

	if i < len(b) {
		t.pos.AdvanceBytes(len(b) - i)
		copy(t.utf8Carry[:], b[i:])
		t.utf8CarryLen = len(b) - i
	}
}

// scanLocked advances the logical position over b, which continues the stream
// at the position. It updates the line, column and byte offset, not the rune
// offset. In text mode b is decoded as UTF-8. In byte mode each byte counts as
// one rune.
func (t *Reader) scanLocked(b []byte) {
	if !t.byteMode {
		t.pos.Scan(b)
		return
	}

	// position.Position.Scan decodes UTF-8, and it decodes a byte that does not
	// start a complete, valid sequence as utf8.RuneError of size 1. Only the
	// bytes 0xC2 to 0xF4 can start a valid multibyte sequence. Each segment
	// below ends at the first such byte, which has no byte after it in the
	// segment. So Scan counts every byte of the segment as one rune, and the
	// position API needs no byte-mode variant.
	for len(b) > 0 {
		i := 0
		for i < len(b) {
			c := b[i]
			i++
			if c >= 0xC2 && c <= 0xF4 {
				break
			}
		}
		t.pos.Scan(b[:i])
		b = b[i:]
	}
}

// fillRuneLocked makes the buffer hold the next rune at the cursor, reading the
// source only while it does not. In byte mode that is one byte. In text mode it
// is a complete UTF-8 sequence in the sense of utf8.FullRune, which includes an
// invalid byte that decodes to utf8.RuneError of size 1; a proper prefix of a
// multi-byte sequence reads more. So a live source that has delivered a rune
// and then blocks still yields that rune.
//
// Each read asks for one more byte, and a source read returns what has
// arrived, so a fill never waits for input the rune does not need. At io.EOF,
// or when a source error is owed after the buffered bytes, an incomplete
// sequence is left as it is and decodes as before. When the source returns
// ErrMoreInput before the rune is complete, the fill returns that error and
// the incomplete sequence stays in the buffer for the next read.
func (t *Reader) fillRuneLocked() error {
	if t.byteMode {
		_, err := t.fillAtLeast(1)
		if err != nil && t.w-t.r > 0 && errors.Is(err, ErrMoreInput) {
			// The byte arrived with the signal: the read does not need more.
			return nil
		}
		return err
	}

	for {
		have := t.w - t.r
		if have >= utf8.UTFMax {
			return nil
		}

		// Release space at the same point a fill for a maximal rune did, so
		// the bytes kept behind the cursor do not depend on how much input
		// the source has delivered.
		if t.r+utf8.UTFMax >= len(t.buf) {
			t.compact()
		}

		if have > 0 && utf8.FullRune(t.buf[t.r:t.w]) {
			return nil
		}

		if _, err := t.fillAtLeast(have + 1); err != nil {
			if errors.Is(err, ErrMoreInput) && t.w-t.r > have && utf8.FullRune(t.buf[t.r:t.w]) {
				// The bytes that arrived with the signal complete the rune,
				// so this read does not need more input.
				return nil
			}
			return err
		}

		if t.w-t.r == have {
			// No byte arrived: a source error is owed after the buffered
			// bytes, which are served first.
			return nil
		}
	}
}

// flushCarry credits a carried incomplete sequence as if it had been decoded on
// its own. Operations other than Read call it first, so the position they start
// from includes every byte read so far.
func (t *Reader) flushCarry() {
	if t.utf8CarryLen == 0 {
		return
	}

	t.pos.AdvanceBytes(-t.utf8CarryLen)
	t.pos.Scan(t.utf8Carry[:t.utf8CarryLen])
	t.runeOffset += utf8.RuneCount(t.utf8Carry[:t.utf8CarryLen])
	t.utf8CarryLen = 0
}

// encodedLeadLen reports how many bytes belong to the UTF-8 sequence that starts
// with the leading byte b. A byte that cannot start a sequence reports one, so
// it is accounted as a single replacement rune.
func encodedLeadLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b&0xE0 == 0xC0:
		return 2
	case b&0xF0 == 0xE0:
		return 3
	case b&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the rune
// and its size in bytes.
func (t *Reader) ReadRune() (r rune, size int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	located, err := t.readRuneLocked()
	if err != nil {
		return 0, 0, err
	}

	return located.r, located.size, nil
}

func (t *Reader) readRuneLocked() (LocatedRune, error) {
	t.flushCarry()

	// Buffer the next rune. An io.EOF is tolerated here, as a partial
	// sequence may still be buffered.
	err := t.fillRuneLocked()
	if err != nil && !errors.Is(err, io.EOF) {
		return LocatedRune{}, err
	}

	// If the buffer is empty after trying to fill, we are at the end of the stream.
	if t.r >= t.w {
		return LocatedRune{}, io.EOF
	}

	// In byte mode every byte is one rune of size 1. Otherwise let
	// utf8.DecodeRune handle all cases: valid ASCII, valid multi-byte, and
	// invalid UTF-8 sequences. If the sequence is invalid, it returns
	// (utf8.RuneError, 1).
	var (
		r    rune
		size int
	)
	if t.byteMode {
		r, size = rune(t.buf[t.r]), 1
	} else {
		r, size = utf8.DecodeRune(t.buf[t.r:t.w])
	}

	// The retention budget bounds the retained region, so it is enforced where
	// the logical cursor would advance. A rune larger than the remaining budget
	// is reported whole: it is never half-decoded and the cursor does not move.
	if headroom := t.retentionHeadroom(); headroom >= 0 && size > headroom {
		return LocatedRune{}, ErrRetentionExceeded
	}

	// Advance the reader's position. This is crucial.
	// For an invalid byte, size will be 1, allowing us to skip it and continue.
	// Scan reports where the rune started, which is the located rune's position.
	line, column, offset := t.pos.Scan(t.buf[t.r : t.r+size])

	located := LocatedRune{
		r:    r,
		size: size,
		pos: Pos{
			byteOffset: offset,
			runeOffset: t.runeOffset,
			line:       line,
			column:     column,
		},
	}

	t.r += size
	t.runeOffset++

	// Update state to allow for UnreadRune.
	t.lastRuneSize = size

	// The error is nil because we successfully "read" a rune from the stream,
	// even if that rune is the replacement/error character. The caller is
	// responsible for checking if r == utf8.RuneError.
	return located, nil
}

// ReadLocatedRune reads the next rune and reports it together with the immutable
// position it started at. The returned located rune describes a committed read:
// it is not changed by later read-ahead.
func (t *Reader) ReadLocatedRune() (LocatedRune, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.readRuneLocked()
}

// PeekRune reports the rune at the logical cursor without consuming it. The
// logical cursor is unchanged on return, so the rune can be read again with
// ReadRune or ReadLocatedRune.
//
// The reported location is the one the next read will return: a pending partial
// UTF-8 sequence is projected onto the reported position, so a peek never
// describes a location the committed read would not, and the stored logical
// cursor is left exactly as it was. Because peeking is not a read, it also ends
// the single-level UnreadRune authority, exactly as a read other than ReadRune
// would.
func (t *Reader) PeekRune() (LocatedRune, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.lastRuneSize = -1

	err := t.fillRuneLocked()
	if err != nil && !errors.Is(err, io.EOF) {
		return LocatedRune{}, err
	}

	if t.r >= t.w {
		return LocatedRune{}, io.EOF
	}

	if t.byteMode {
		return LocatedRune{r: rune(t.buf[t.r]), size: 1, pos: t.cursorLocked()}, nil
	}

	r, size := utf8.DecodeRune(t.buf[t.r:t.w])

	return LocatedRune{r: r, size: size, pos: t.projectedCursorLocked()}, nil
}

// UnreadRune unreads the last rune read by ReadRune. It is an error to call
// UnreadRune if the most recent method called on the Reader was not
// ReadRune.  Only one level of unread is supported.
func (t *Reader) UnreadRune() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.flushCarry()

	if t.lastRuneSize < 0 || t.r < t.lastRuneSize {
		return bufio.ErrInvalidUnreadRune
	}

	if err := t.pos.Rewind(t.lastRuneSize, 1); err != nil {
		return fmt.Errorf("rewind: %w", err)
	}

	t.r -= t.lastRuneSize
	if t.runeOffset > 0 {
		t.runeOffset--
	}
	t.lastRuneSize = -1

	return nil
}

// Read reads up to len(p) bytes into p and returns the number of bytes read.
// For reads larger than the buffer capacity, it will read directly from the
// underlying reader, and discard any previously buffered data. The direct path
// is never taken while a checkpoint is active: those bytes may still be needed
// for replay, so they are buffered instead.
func (t *Reader) Read(p []byte) (n int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	needed, filled := len(p), 0

	// The retention budget is measured against the bytes the cursor would
	// actually advance over, not the size of the caller's destination. `Read`
	// may legitimately return fewer bytes than requested, so make the buffer
	// show what is really available before judging the request over budget. An
	// over-budget read still does no partial work.
	if headroom := t.retentionHeadroom(); headroom >= 0 && needed > headroom {
		_, _ = t.fillAtLeast(headroom + 1)

		avail := t.w - t.r
		want := needed
		if avail < want {
			want = avail
		}
		if want > headroom {
			return 0, ErrRetentionExceeded
		}
	}

	var readErr error

	for filled < needed {
		buffered := t.w - t.r

		if buffered > 0 {
			// We have data some in the buffer. move as much data as possible from it
			// to `p`

			n = needed - filled
			if n > buffered {
				n = buffered
			}

			copy(p[filled:], t.buf[t.r:t.r+n])
			t.accountBytes(p[filled : filled+n])
			t.r += n

			filled += n
		}

		if readErr != nil {
			break
		}

		// The size of the requested read is larger than the buffer, there's no way
		// we can handle this
		if needed-filled > t.capacity && t.br != nil && len(t.checkpoints) == 0 {

			// Read remaining data directly into p, through the same sticky
			// bookkeeping as the buffered path so an error returned alongside
			// data is remembered and surfaced to the caller and later reads.
			n, readErr = t.readInto(p[filled:])

			// Reset the buffer since we dumped it all into p. Nothing behind
			// the cursor stays rewindable once this Read returns, so the line
			// table is trimmed to the cursor after every capacity-sized chunk
			// instead of growing with len(p).
			t.r = 0
			t.w = 0
			t.lastRuneSize = -1

			for done := 0; done < n; {
				end := min(done+t.capacity, n)
				t.accountBytes(p[filled+done : filled+end])
				t.trimLines()
				done = end
			}

			filled += n

			break
		}

		// Fill the buffer with more data for the next read
		_, readErr = t.fillAtLeast(needed - filled)
	}

	t.lastRuneSize = -1

	// An error returned alongside data must reach the caller rather than being
	// deferred, which is what io.Reader requires. ErrMoreInput is the
	// exception, as io.EOF is: the bytes are returned first, and the next Read
	// asks the source again.
	moreInput := filled > 0 && errors.Is(readErr, ErrMoreInput)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !moreInput {
		return filled, readErr
	}

	if filled == 0 && readErr != nil {
		return 0, readErr
	}

	return filled, nil
}

// Seek sets the offset for the next Read or ReadRune, interpreting offset and
// whence according to the io.Seeker interface. It returns the new byte offset.
//
// Seek moves only within the bytes the reader holds in its buffer, and it can
// read ahead from the source to reach a forward target. It does not perform a
// seek on the underlying io.Reader. io.SeekEnd counts from the end of the
// buffered bytes, not from the end of the input.
//
// Seek first computes the absolute stream offset of the target, for every
// whence. Then:
//
//   - A target before stream offset 0 fails with the error "cursor: negative
//     position".
//   - A target at or after offset 0 but before the start of the buffer fails
//     with ErrSeekOutOfBuffer. The bytes there were read and discarded.
//   - A forward target past the buffer capacity, or after the end of the
//     input, fails with ErrSeekOutOfBuffer.
//   - A forward target past the retention limit of an active checkpoint fails
//     with ErrRetentionExceeded (see WithMaxRetained).
//
// A failed Seek does not move the position. The byte offset, the rune offset,
// the line and the column stay the same. A Read can stop inside a multi-byte
// UTF-8 sequence; after a failed Seek, that sequence is still pending, and
// the next Read that completes it counts it as one rune. To reach a forward
// target, a failed Seek can still have read ahead from the source into the
// buffer.
//
// A Seek to the current position does nothing. A Seek that moves first
// settles a pending partial sequence: each byte of it counts as one rune.
func (t *Reader) Seek(offset int64, whence int) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	current := int64(t.pos.Offset())
	bufferStart := current - int64(t.r)

	// base is the absolute stream offset that offset counts from.
	var base int64
	switch whence {
	case io.SeekStart:
		base = 0
	case io.SeekCurrent:
		base = current
	case io.SeekEnd:
		base = bufferStart + int64(t.w)
	default:
		return 0, errors.New("cursor: invalid whence")
	}

	// A target that does not fit an int64 is past the buffer capacity.
	if offset > 0 && base > math.MaxInt64-offset {
		return 0, ErrSeekOutOfBuffer
	}
	target := base + offset

	if target < 0 {
		return 0, errors.New("cursor: negative position")
	}
	if target < bufferStart {
		return 0, ErrSeekOutOfBuffer
	}

	rel := target - current
	if rel == 0 {
		// A seek that does not move the cursor is a no-op: it must not settle a
		// pending partial UTF-8 sequence or otherwise change the position.
		return current, nil
	}

	// newR is the new read pointer relative to the start of t.buf. It is not
	// negative, because the target is at or after the buffer start. It is
	// checked before it is converted to int for buffer operations.
	newR := target - bufferStart
	if newR > int64(len(t.buf)) {
		return 0, ErrSeekOutOfBuffer
	}
	relInt := int(rel)

	// Every check below runs before the position changes, so a failed seek
	// leaves the position and a pending partial UTF-8 sequence as they are. A
	// seek that moves is a position-changing operation, so the pending
	// sequence is settled just before the cursor moves.
	if rel > 0 { // Seeking Forward
		if headroom := t.retentionHeadroom(); headroom >= 0 && relInt > headroom {
			return 0, ErrRetentionExceeded
		}

		bytesAvailable := t.w - t.r
		if relInt > bytesAvailable {
			if _, err := t.fillAtLeast(relInt); err != nil && !errors.Is(err, io.EOF) {
				// Bytes can arrive together with ErrMoreInput. The seek fails
				// with the signal only when they do not reach the target.
				if !errors.Is(err, ErrMoreInput) || t.r+relInt > t.w {
					return 0, fmt.Errorf("fillAtLeast: %w", err)
				}
			}
		}

		if t.r+relInt > t.w {
			return 0, ErrSeekOutOfBuffer
		}

		t.flushCarry()
		t.scanLocked(t.buf[t.r : t.r+relInt])
		t.runeOffset += t.runeCount(t.buf[t.r : t.r+relInt])
		t.r += relInt

	} else { // Seeking Backward
		newRInt := int(newR)

		t.flushCarry()

		// Count runes in the slice we're rewinding over. The rewind cannot
		// fail: the target is at or after the buffer start, and the reader
		// keeps the line positions from the buffer start onward.
		runeCount := t.runeCount(t.buf[newRInt:t.r])
		if err := t.pos.Rewind(-relInt, runeCount); err != nil {
			return 0, fmt.Errorf("pos.Rewind: %w", err)
		}
		t.runeOffset -= runeCount
		if t.runeOffset < 0 {
			t.runeOffset = 0
		}
		t.r += relInt
	}

	t.lastRuneSize = -1

	return int64(t.pos.Offset()), nil
}

// runeCount reports how many runes the reader counts in b: one per byte in byte
// mode, the UTF-8 rune count otherwise.
func (t *Reader) runeCount(b []byte) int {
	if t.byteMode {
		return len(b)
	}
	return utf8.RuneCount(b)
}

// Pos returns a copy of the reader's current position (line, column, and
// offset).  Modifying the returned Position will not affect the reader's
// state.
//
// Pos reports the logical next-read position: bytes buffered for look-ahead are
// never reflected in it.
//
// The reader drops the line positions of input behind the start of its buffer
// (see position.Position.TrimBefore), and the returned copy holds the same line
// positions. Rewind on the copy is exact for any target at or after the buffer
// start; a target further back can return an error. A Rewind to offset 0 still
// resets the copy.
func (t *Reader) Pos() *position.Position {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.pos.Copy()
}

// Cursor returns the immutable logical position of the next read. It is the
// value form of Pos.
func (t *Reader) Cursor() Pos {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.cursorLocked()
}

func (t *Reader) cursorLocked() Pos {
	line, column, offset := t.pos.State()

	return Pos{
		byteOffset: offset,
		runeOffset: t.runeOffset,
		line:       line,
		column:     column,
	}
}

// projectedCursorLocked reports the logical cursor as the next settling
// operation would leave it. Bytes of an incomplete UTF-8 sequence that a chunk
// carried over already count toward the byte offset; their rune and column
// contribution is projected here on a copy, so inspecting a peek can describe
// the rune the next read will return without moving the stored cursor.
func (t *Reader) projectedCursorLocked() Pos {
	pos := t.cursorLocked()
	if t.utf8CarryLen == 0 {
		return pos
	}

	carried := t.utf8Carry[:t.utf8CarryLen]

	projected := t.pos.Copy()
	projected.ScanRunes(carried)
	line, column, offset := projected.State()

	return Pos{
		byteOffset: offset,
		runeOffset: t.runeOffset + utf8.RuneCount(carried),
		line:       line,
		column:     column,
	}
}
