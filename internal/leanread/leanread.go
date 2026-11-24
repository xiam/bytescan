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

// Extent is what the caller of Advance knows of the bytes it moves the cursor
// over, from the runes that Peek decoded from them: the number of runes, the
// number of newlines, and the number of runes after the last newline.
type Extent struct {
	Runes int
	Lines int
	// Column is the number of runes after the last newline. It is used only
	// when Lines is not zero.
	Column int
}

// Advancer is the commit operation of the read path of a cursor whose
// positions are of type P. The scanner asserts it on the Reader that Of
// returns, with P the position type of package cursor, so this package does
// not name that type.
type Advancer[P any] interface {
	// Advance moves the logical cursor forward over the next n bytes, which
	// Peek decoded, and returns the position of the cursor after the move.
	// ext describes the n bytes, and no rune that Peek returned for them was
	// Partial. Advance takes the rune offset, the line and the column of the
	// new position from ext instead of decoding the bytes again for them.
	//
	// Advance leaves the cursor as Seek(n, io.SeekCurrent) would leave it,
	// and it fails as that Seek would, without moving the cursor. When Check
	// is set, it also computes the new position from the bytes, as that Seek
	// does, and fails when ext gives another one; the cursor is then not in a
	// defined state, and the test that set Check fails.
	Advance(n int, ext Extent) (P, error)
}

// Boundary is the work of the read path of a cursor at the boundary between
// two tokens, with the cursor's positions of type P and its marks of type M.
// The scanner asserts it on the Reader that Of returns, with P and M the
// position and checkpoint types of package cursor, so this package does not
// name those types. Each method holds the cursor's lock once.
type Boundary[P, M any] interface {
	// Mark re-arms the released mark m at the logical cursor, as the
	// cursor's Remark does, and returns the position it marked. It fails as
	// that Remark fails, and m is then unchanged.
	Mark(m M) (P, error)

	// AdvanceRelease moves the logical cursor as Advance does, and then
	// releases old as the mark's Release does, under the same lock. It
	// fails as Advance fails, without moving the cursor and without
	// releasing old. A nil old, or one that is not active on this cursor, is
	// not released, and that is not an error: unlike the mark's Release, it
	// leaves a mark that is active on another cursor active.
	AdvanceRelease(n int, ext Extent, old M) (P, error)
}

// Check makes Advance and AdvanceRelease compare the position that their
// caller's Extent gives with the position that the cursor computes from the
// bytes. Tests set it before they start; it is false in a program.
var Check bool

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
