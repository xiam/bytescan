// Package lextest holds the test helpers that the tests in tests/ and in the
// packages under rules/ share: a token stream as a list of strings, a table
// runner for complete streams, and whether the race detector is on.
//
// A stream entry is "TYPE \"text\" @offset", so a failing test shows the type,
// the text and the offset of every token in one diff. The package is internal
// so that it is not part of the public API.
package lextest

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// Token returns the stream entry of one token.
func Token(typ bytescan.TokenType, text string, offset int) string {
	return fmt.Sprintf("%s %q @%d", typ, text, offset)
}

// Unknowns returns the stream entries of the single-rune UNKNOWN tokens of s,
// the first at offset.
func Unknowns(s string, offset int) []string {
	var stream []string
	for i, r := range []rune(s) {
		stream = append(stream, Token(bytescan.TokenTypeUnknown, string(r), offset+i))
	}
	return stream
}

// Collect lexes input with the rules that setup registers and returns the
// stream read before the first error other than io.EOF, and that error. It
// takes no testing.T, so it can run on other goroutines.
func Collect(input string, setup func(lx *bytescan.Scanner)) ([]string, error) {
	lx := bytescan.New(strings.NewReader(input))
	setup(lx)

	var stream []string
	for {
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			return stream, nil
		}
		if err != nil {
			return stream, err
		}
		stream = append(stream, Token(lex.Type(), lex.Text(), lex.Offset()))
	}
}

// LexStream lexes input with the rules that setup registers and returns every
// token as a stream entry. It fails the test on an error other than io.EOF.
func LexStream(t *testing.T, input string, setup func(lx *bytescan.Scanner)) []string {
	t.Helper()
	stream, err := Collect(input, setup)
	require.NoError(t, err, "input %q", input)
	return stream
}

// StreamCase is the shape of one input of a stream test and its complete
// expected token stream. Each test package declares its own named type with
// these two fields, so that its tables can use unkeyed literals, and passes it
// to RunStreamCases.
type StreamCase interface {
	~struct {
		Input    string
		Expected []string
	}
}

// RunStreamCases lexes each case with the rules that setup registers, in a
// subtest named after the input, and compares the complete token stream.
func RunStreamCases[C StreamCase](t *testing.T, cases []C, setup func(lx *bytescan.Scanner)) {
	t.Helper()
	for _, c := range cases {
		tc := struct {
			Input    string
			Expected []string
		}(c)
		t.Run(tc.Input, func(t *testing.T) {
			require.Equal(t, tc.Expected, LexStream(t, tc.Input, setup))
		})
	}
}
