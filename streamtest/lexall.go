package streamtest

import (
	"io"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/textreader"
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
// (textreader.WithMaxRetained), builds a scanner over it with newScanner, and
// returns LexAll of that scanner.
//
// It checks that a driver releases its scanners before it reads a field longer
// than maxRetained from the cursor: a scanner that is not released keeps a
// checkpoint that pins the field, the cursor fails with an error that wraps
// textreader.ErrRetentionExceeded, and LexRetained fails t. rr is the
// cursor's rune source: a bufio.Reader over text, or bytesource.NewReader
// over binary input.
func LexRetained[L bytescan.Source](t testing.TB, rr io.RuneReader, maxRetained int, newScanner func(bytescan.Cursor) L) []*bytescan.Token {
	t.Helper()
	return LexAll(t, newScanner(textreader.NewRuneReader(rr, textreader.WithMaxRetained(maxRetained))))
}
