// Package leanread is the read path between the scanner and the cursor of this
// module. It lets the scanner decode the next runes of a token from the
// cursor's buffer in one call, without one public method call, one
// LocatedRune and two locks for each rune.
//
// The package imports only the standard library. Package cursor registers
// Hook in its init function, and the scanner calls Of once, when it is built.
// A cursor that Of does not recognize keeps the public read path
// (cursor.Reader.ReadLocatedRune). No exported name of a public package is
// added for this path (docs/specs/cursor.md, "Independence").
package leanread

// Rune is one rune that Peek decoded, with the number of bytes it occupies in
// the stream.
type Rune struct {
	Rune rune
	Size int
}

// At describes the position at which a Peek starts.
type At struct {
	// Start reports that the position is the start of the stream: byte offset
	// 0 and rune offset 0.
	Start bool
	// LineStart reports that the position is at column 0.
	LineStart bool
	// Partial reports that the first rune Peek returned was decoded from an
	// incomplete UTF-8 sequence: utf8.RuneError of size 1, decoded because
	// the source returned no more bytes for it then. Later bytes can complete
	// the sequence, and a Seek over the range would then decode it as one
	// rune. A caller that moves the cursor over such a rune moves it one rune
	// at a time, so the position counts the rune as ReadLocatedRune did.
	Partial bool
}

// Reader is the read path of a cursor.
type Reader interface {
	// Peek decodes runes into dst. The first rune starts skip bytes after the
	// logical cursor, and skip must be the sum of the sizes of runes that
	// earlier calls returned since the cursor last moved. Peek does not move
	// the logical cursor: the caller moves it later, with a Seek over the
	// bytes it used.
	//
	// Peek decodes the runes that the buffer already holds. It reads from the
	// source only when the buffer does not hold the first rune, and then only
	// as much as a read of that one rune would. It returns n > 0 and a nil
	// error when it decoded runes. It returns n == 0 and the error that a read
	// of the rune at skip would return when it decoded none, io.EOF at the
	// end of the input. The retention limit applies as if the cursor had
	// advanced over the skip bytes. An empty dst returns 0 and a nil error,
	// and reads nothing.
	//
	// at describes the position skip bytes after the logical cursor, also
	// when the error is not nil.
	Peek(skip int, dst []Rune) (n int, at At, err error)

	// Epoch returns the number of events that can change what a rune
	// decoded by Peek depends on: a reset onto a new source
	// (cursor.Reader.ResetStart), which changes the bytes at an offset, and
	// a new mark (cursor.Reader.Checkpoint, a successful
	// cursor.Reader.Remark), which can move the oldest byte the cursor
	// retains back and so lower the retention headroom. While it stays the
	// same, runes that Peek decoded at an offset can be used again when the
	// cursor is back at that offset. It takes no lock.
	Epoch() uint64
}

// Hook returns the read path of v, a cursor of this module. Package cursor
// sets it in its init function. It is nil when package cursor is not linked.
var Hook func(v any) (Reader, bool)

// Of returns the read path of v. It reports false when v has none.
func Of(v any) (Reader, bool) {
	if Hook == nil {
		return nil, false
	}
	return Hook(v)
}
