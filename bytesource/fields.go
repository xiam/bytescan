package bytesource

import (
	"errors"

	"github.com/xiam/bytescan"
)

// ErrChunkedThen is returned by Fields.NextField when the field at the front
// of the queue sets both MaxPiece and Then. A chunked field never passes its
// bytes to a callback; it uses Done instead.
var ErrChunkedThen = errors.New("bytesource: field sets both MaxPiece and Then")

// A Field is one field that a driver reads from the cursor: Len bytes of type
// Type.
//
// An unchunked field (MaxPiece of 0 or less) is read as one token of Len
// bytes. Then, when it is not nil, is called with the bytes of the field after
// the whole field was read and before its token is returned; it can push more
// fields.
//
// A chunked field (MaxPiece above 0) is read as tokens of at most MaxPiece
// bytes each, so that memory follows the piece size and not Len. The field
// stays at the front of the queue until Len bytes were read. A chunked field
// never passes its bytes to a callback: it must not set Then.
//
// Done, when it is not nil, is called once, with no bytes, when the whole
// field was read: after the last piece was read and before that piece is
// returned, and after Then for an unchunked field. It can push more fields,
// which are read after the fields already queued.
type Field struct {
	Type     bytescan.TokenType
	Len      int
	MaxPiece int
	Then     func(b []byte)
	Done     func()
}

// Fields is a queue of fields that a driver reads in order from one cursor,
// between the tokens of its scanners. A driver pushes the fields that a header
// token announces, calls Release on its scanner, and calls NextField until it
// returns a nil token; then it goes back to its scanner, and it may push more
// fields later.
//
// Fields is not a bytescan.Source, and its step method is not named Next on
// purpose: an empty queue is not the end of the stream. NextField returns a
// nil token with a nil error when the queue is empty, which a Source must
// never do. The driver that steps the queue is the Source of the stream.
//
// For a chunked field the queue holds at most one piece in memory. Fields is
// not safe for concurrent use; a driver serializes its calls.
type Fields struct {
	cur   bytescan.Cursor
	queue []Field
	head  int // index of the front field in queue
	read  int // bytes read so far of the front field, when it is chunked
}

// NewFields returns an empty queue of fields over cur.
func NewFields(cur bytescan.Cursor) *Fields {
	return &Fields{cur: cur}
}

// Push adds fields to the end of the queue.
//
// A field with a length of 0 or less gives no token when its turn comes: its
// Then is called with no bytes, its Done is called, and the queue advances.
func (f *Fields) Push(fields ...Field) {
	if f.head > 0 && len(f.queue)+len(fields) > cap(f.queue) {
		// Move the queued fields to the front before the storage grows.
		n := copy(f.queue, f.queue[f.head:])
		clear(f.queue[n:])
		f.queue, f.head = f.queue[:n], 0
	}
	f.queue = append(f.queue, fields...)
}

// NextField reads the next field, or the next piece of a chunked field, from
// the cursor and returns its token.
//
// It returns a nil token and a nil error when the queue is empty: the driver
// then goes back to its scanner. When the input ends inside a field, NextField
// returns the bytes read so far as the last token of that field and drops the
// rest of the queue, without calling the field's Then or Done; when the input
// ends exactly where a piece would start, it returns a nil token and drops
// the queue. The driver then reaches the end of the input through its scanner.
//
// When the front field sets both MaxPiece and Then, NextField returns
// ErrChunkedThen, reads nothing and leaves the queue as it is. An error from
// the cursor is returned as it is.
func (f *Fields) NextField() (*bytescan.Token, error) {
	for f.head < len(f.queue) {
		fd := f.queue[f.head]
		if fd.MaxPiece > 0 && fd.Then != nil {
			return nil, ErrChunkedThen
		}
		if fd.Len <= 0 {
			f.pop()
			if fd.Then != nil {
				fd.Then(nil)
			}
			if fd.Done != nil {
				fd.Done()
			}
			continue
		}

		n := fd.Len
		if fd.MaxPiece > 0 {
			n = min(fd.Len-f.read, fd.MaxPiece)
		}
		l, err := Read(f.cur, fd.Type, n)
		if err != nil {
			return nil, err
		}
		if l == nil || l.Len() < n {
			// The input ends inside the field.
			f.drop()
			return l, nil
		}
		if fd.MaxPiece > 0 {
			f.read += n
			if f.read < fd.Len {
				return l, nil
			}
		}
		f.pop()
		if fd.Then != nil {
			fd.Then(Bytes(l.Text()))
		}
		if fd.Done != nil {
			fd.Done()
		}
		return l, nil
	}
	f.drop()
	return nil, nil
}

// pop removes the front field. It clears the slot so that the queue does not
// keep the field's callbacks alive.
func (f *Fields) pop() {
	f.queue[f.head] = Field{}
	f.head++
	f.read = 0
	if f.head == len(f.queue) {
		f.queue, f.head = f.queue[:0], 0
	}
}

// drop empties the queue and keeps its storage for later pushes.
func (f *Fields) drop() {
	clear(f.queue)
	f.queue, f.head, f.read = f.queue[:0], 0, 0
}
