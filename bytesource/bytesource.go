// Package bytesource makes a byte stream lexable: it is the byte adapter for
// scanners of binary formats and binary protocols.
//
// The scanner reads runes. NewCursor returns a cursor that reads each byte of
// its input as one rune, U+0000 to U+00FF. A rule then matches bytes through
// Text: core.Literal(bytesource.Text("\xff\xd8")) matches the bytes FF D8.
// Bytes turns the text of a token back into its bytes, and AnyByte is the
// class of every byte.
//
// # Positions
//
// Because every byte is one rune, Token.Offset, Token.RuneOffset and
// Token.Len are byte offsets and byte lengths in the input. Use Offset and
// Len for positions in binary data; Hex prints Offset.
//
// The current cursor counts its other positions on a UTF-8 encoding of the
// runes. With the current implementation, Token.ByteOffset and Span.Bytes
// count a byte of 0x80 or above as two bytes, so they are not offsets in the
// input, and Line and Column advance at every 0x0A byte, so they are not
// positions in the input either. These are current limits of the cursor, not
// a contract of this package. A byte mode in textreader will remove them, and
// this package will switch to it.
//
// # Token guards
//
// bytescan.WithMaxTokenBytes counts the bytes of the cursor's encoding, so
// with the current cursor a byte of 0x80 or above costs two bytes against
// the guard. A guard for binary input is therefore twice the largest token in
// bytes when the input can hold such bytes.
//
// # Fields read by a driver
//
// A field whose length is stored in the data is read by a driver, not by a
// rule. Read and Peek read the cursor directly, and Fields is a queue of
// fields that a driver pushes when a header token announces them and then
// steps with NextField, one token per field or, for a field with a
// MaxPiece, one token per piece. The driver, not the queue, is the
// bytescan.Source of the stream.
package bytesource

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/textreader"
)

// AnyByte is the class of every byte: the runes U+0000 to U+00FF.
var AnyByte = core.RuneRange(0, 0xFF)

// byteRunes is an io.RuneReader that returns each byte as one rune.
type byteRunes struct{ r io.ByteReader }

func (b byteRunes) ReadRune() (rune, int, error) {
	c, err := b.r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	return rune(c), 1, nil
}

// NewReader returns an io.RuneReader that reads r and returns each byte as
// one rune, U+0000 to U+00FF, of size 1. When r is not an io.ByteReader, it
// is wrapped in a bufio.Reader.
func NewReader(r io.Reader) io.RuneReader {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return byteRunes{br}
}

// NewCursor returns a cursor over NewReader(r), for bytescan.NewWithCursor
// and bytescan.NewWithCursorOptions.
//
// Offset, RuneOffset and Len of the tokens read through it are byte offsets
// and byte lengths. With the current implementation, ByteOffset, Line and
// Column are not positions in the input; use Offset for positions (see the
// package documentation).
func NewCursor(r io.Reader) *textreader.TextReader {
	return textreader.NewRuneReader(NewReader(r))
}

// Text returns the string whose runes are the bytes of s, the text a rule
// matches for those bytes: Text("\xff\xd8") is "ÿØ".
//
// Use Text for every byte of 0x80 or above in a rule. A Go string such as
// "\xff\xd8" is not valid UTF-8: core.Literal("\xff\xd8") decodes it to
// U+FFFD and never matches the byte runes.
func Text(s string) string {
	r := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		r[i] = rune(s[i])
	}
	return string(r)
}

// Bytes returns the bytes of a token text read through NewReader: each rune
// becomes one byte. It is the inverse of Text.
func Bytes(text string) []byte {
	b := make([]byte, 0, len(text))
	for _, r := range text {
		b = append(b, byte(r))
	}
	return b
}

// Read reads up to n bytes from cur and returns them as one token of type
// typ, with the span it covers. It returns fewer than n bytes when the input
// ends first, and a nil token with a nil error when it reads nothing.
//
// Read allocates for the bytes it reads, not for n: a corrupt length does not
// allocate more than the input holds.
//
// A driver calls Release on the scanner it used last before Read, so that the
// scanner's checkpoints do not pin the bytes that Read consumes.
func Read(cur bytescan.Cursor, typ bytescan.TokenType, n int) (*bytescan.Token, error) {
	start := cur.Cursor()
	text := make([]rune, 0, min(n, 4096))
	for len(text) < n {
		lr, err := cur.ReadLocatedRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		text = append(text, lr.Rune())
	}
	if len(text) == 0 {
		return nil, nil
	}
	return bytescan.NewTokenWithSpan(typ, text, textreader.NewSpan(start, cur.Cursor())), nil
}

// Peek returns the next n bytes of cur, or fewer when the input ends first,
// and leaves the cursor where it was. A driver peeks at a length that is part
// of the field it measures, such as the size at the start of a BMP DIB header.
func Peek(cur bytescan.Cursor, n int) ([]byte, error) {
	cp := cur.Checkpoint()
	defer cp.Release()
	b := make([]byte, 0, n)
	for len(b) < n {
		lr, err := cur.ReadLocatedRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		b = append(b, byte(lr.Rune()))
	}
	if err := cp.Reset(); err != nil {
		return nil, err
	}
	return b, nil
}

// hexBytes is the number of bytes that Hex shows of a token.
const hexBytes = 16

// Hex returns the line for l in the hex view:
//
//	OFFSET TYPE LENGTH BYTES
//
// OFFSET is the byte offset of the token in the input and LENGTH its length
// in bytes, both in decimal. BYTES are its first 16 bytes in hex, followed by
// "..." when it is longer.
func Hex(l *bytescan.Token) string {
	b := Bytes(l.Text())
	more := ""
	if len(b) > hexBytes {
		b, more = b[:hexBytes], " ..."
	}
	return strconv.Itoa(l.Offset()) + " " + string(l.Type()) + " " +
		strconv.Itoa(l.Len()) + " " + strings.TrimSpace(fmt.Sprintf("% x", b)) + more
}
