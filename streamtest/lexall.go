package streamtest

import (
	"io"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
)

// LexAll reads src until io.EOF and returns its tokens, in order. It fails t
// with the number of tokens read so far if src returns any other error.
//
// LexAll keeps every token, so it is for short inputs; Run lexes a long
// stream without keeping it.
func LexAll(t testing.TB, src bytescan.Source) []*bytescan.Token {
	t.Helper()
	var ls []*bytescan.Token
	for {
		l, err := src.Next()
		if err == io.EOF {
			return ls
		}
		if err != nil {
			t.Fatalf("streamtest: after %d tokens: %v", len(ls), err)
		}
		ls = append(ls, l)
	}
}

// LexRetained builds a cursor over rr that retains at most maxRetained bytes
// (cursor.WithMaxRetained), builds a scanner over it with newScanner, and
// returns LexAll of that scanner.
//
// It checks that a driver releases its scanners before it reads a field longer
// than maxRetained from the cursor: a scanner that is not released keeps a
// checkpoint that pins the field, the cursor fails with an error that wraps
// cursor.ErrRetentionExceeded, and LexRetained fails t. rr is the
// cursor's rune source, usually a bufio.Reader over text.
//
// The cursor encodes the runes of rr as UTF-8, so over bytesource.NewReader
// it retains two bytes for each byte of 0x80 or above. Use LexRetainedBytes
// for binary input.
func LexRetained[L bytescan.Source](t testing.TB, rr io.RuneReader, maxRetained int, newScanner func(bytescan.Cursor) L) []*bytescan.Token {
	t.Helper()
	return LexAll(t, newScanner(cursor.NewRuneReader(rr, cursor.WithMaxRetained(maxRetained))))
}

// LexRetainedBytes does what LexRetained does on a byte-mode cursor: it
// builds cursor.NewByteReader(r, cursor.WithMaxRetained(maxRetained)), the
// cursor of bytesource.NewCursor with a retention limit, builds a scanner over
// it with newScanner, and returns LexAll of that scanner.
//
// The cursor retains one byte for each byte of r, so maxRetained is a number
// of input bytes: a driver that releases its scanners before it reads a field
// of n bytes passes with a limit of n, and a scanner that is not released
// makes the cursor fail with an error that wraps cursor.ErrRetentionExceeded,
// which fails t.
func LexRetainedBytes[L bytescan.Source](t testing.TB, r io.Reader, maxRetained int, newScanner func(bytescan.Cursor) L) []*bytescan.Token {
	t.Helper()
	return LexAll(t, newScanner(cursor.NewByteReader(r, cursor.WithMaxRetained(maxRetained))))
}
