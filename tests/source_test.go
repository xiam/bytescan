package tests

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
)

// sliceSource is a Source that returns its tokens in order and then err, or
// io.EOF when err is nil. It allocates nothing per call.
type sliceSource struct {
	tokens []*bytescan.Token
	err    error
	next   int
}

func (s *sliceSource) Next() (*bytescan.Token, error) {
	if s.next < len(s.tokens) {
		l := s.tokens[s.next]
		s.next++
		return l, nil
	}
	if s.err != nil {
		return nil, s.err
	}
	return nil, io.EOF
}

// cycleSource is a Source that repeats its tokens forever.
type cycleSource struct {
	tokens []*bytescan.Token
	next   int
}

func (s *cycleSource) Next() (*bytescan.Token, error) {
	l := s.tokens[s.next]
	s.next = (s.next + 1) % len(s.tokens)
	return l, nil
}

// drain reads src until an error and returns the types it read and the error.
func drain(t *testing.T, src bytescan.Source) ([]bytescan.TokenType, error) {
	t.Helper()
	var types []bytescan.TokenType
	for i := 0; i < 1000; i++ {
		l, err := src.Next()
		if err != nil {
			assert.Nil(t, l, "a Source returns a nil token with an error")
			return types, err
		}
		require.NotNil(t, l, "a Source returns a token with a nil error")
		types = append(types, l.Type())
	}
	t.Fatal("source did not end")
	return nil, nil
}

func tokens(types ...bytescan.TokenType) []*bytescan.Token {
	out := make([]*bytescan.Token, len(types))
	for i, typ := range types {
		out[i] = bytescan.NewTokenFromString(typ, string(typ), i)
	}
	return out
}

func TestScannerIsSource(t *testing.T) {
	lx := bytescan.New(strings.NewReader("ab"))
	lx.MustAddRule("A", core.Literal("a"))
	var src bytescan.Source = lx
	types, err := drain(t, src)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, []bytescan.TokenType{"A", bytescan.TokenTypeUnknown}, types)
}

func TestSkip(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name  string
		in    []bytescan.TokenType
		err   error
		skip  []bytescan.TokenType
		want  []bytescan.TokenType
		wantE error
	}{
		{"none skipped", []bytescan.TokenType{"A", "B"}, nil, []bytescan.TokenType{"C"}, []bytescan.TokenType{"A", "B"}, io.EOF},
		{"some skipped", []bytescan.TokenType{"A", "S", "B", "S", "S"}, nil, []bytescan.TokenType{"S"}, []bytescan.TokenType{"A", "B"}, io.EOF},
		{"several types", []bytescan.TokenType{"A", "S", "T", "B"}, nil, []bytescan.TokenType{"T", "S"}, []bytescan.TokenType{"A", "B"}, io.EOF},
		{"all skipped", []bytescan.TokenType{"S", "S"}, nil, []bytescan.TokenType{"S"}, nil, io.EOF},
		{"empty source", nil, nil, []bytescan.TokenType{"S"}, nil, io.EOF},
		{"error passes", []bytescan.TokenType{"A", "S"}, boom, []bytescan.TokenType{"S"}, []bytescan.TokenType{"A"}, boom},
		{"unknown skipped", []bytescan.TokenType{"A", bytescan.TokenTypeUnknown}, nil, []bytescan.TokenType{bytescan.TokenTypeUnknown}, []bytescan.TokenType{"A"}, io.EOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := bytescan.Skip(&sliceSource{tokens: tokens(tc.in...), err: tc.err}, tc.skip...)
			types, err := drain(t, src)
			assert.Equal(t, tc.want, types)
			assert.Same(t, tc.wantE, err, "the error passes through unchanged")
			// The end stays the end.
			_, err = src.Next()
			assert.Same(t, tc.wantE, err)
		})
	}
}

func TestSkipNoTypesReturnsSource(t *testing.T) {
	src := &sliceSource{}
	assert.Same(t, bytescan.Source(src), bytescan.Skip(src))
}

func TestSkipCopiesTypes(t *testing.T) {
	skip := []bytescan.TokenType{"S"}
	src := bytescan.Skip(&sliceSource{tokens: tokens("S", "A")}, skip...)
	skip[0] = "A"
	types, err := drain(t, src)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, []bytescan.TokenType{"A"}, types)
}

func TestSkipOverScanner(t *testing.T) {
	lx := bytescan.New(strings.NewReader("one two\nthree"))
	lx.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	lx.MustAddRule("SPACE", core.RuneRun(core.RuneSet(" \n")))
	src := bytescan.Skip(lx, "SPACE")
	var got []string
	for {
		l, err := src.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		got = append(got, l.Text())
	}
	assert.Equal(t, []string{"one", "two", "three"}, got)
}

// TestSkipAllocations checks that a filter adds no allocation per token it
// passes or drops.
func TestSkipAllocations(t *testing.T) {
	src := bytescan.Skip(&cycleSource{tokens: tokens("A", "S", "B", "T", "S")}, "S", "T")
	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
	})
	assert.Zero(t, allocs, "allocations per Next through Skip")
}

func TestWithType(t *testing.T) {
	lx := bytescan.New(strings.NewReader("ab\ncd"))
	lx.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	lx.MustAddRule("NL", core.Literal("\n"))
	_, err := lx.Next()
	require.NoError(t, err)
	l, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, bytescan.TokenType("NL"), l.Type())

	r := l.WithType("NEWLINE")
	assert.NotSame(t, l, r)
	assert.Equal(t, bytescan.TokenType("NEWLINE"), r.Type())
	assert.Equal(t, bytescan.TokenType("NL"), l.Type(), "the original is not changed")
	assert.Equal(t, l.Text(), r.Text())
	assert.Equal(t, l.Len(), r.Len())
	assert.Equal(t, l.Offset(), r.Offset())
	assert.Equal(t, l.Span(), r.Span())
	assert.Equal(t, l.ByteOffset(), r.ByteOffset())
	assert.Equal(t, l.Line(), r.Line())
	assert.Equal(t, l.Column(), r.Column())

	// A token built without a span keeps its rune offset.
	m := bytescan.NewTokenFromString("A", "xyz", 7).WithType("B")
	assert.Equal(t, bytescan.TokenType("B"), m.Type())
	assert.Equal(t, "xyz", m.Text())
	assert.Equal(t, 7, m.Offset())
	assert.Equal(t, cursor.Span{}, m.Span())
}

func TestNewEmptyToken(t *testing.T) {
	lx := bytescan.New(strings.NewReader("ab\ncd"))
	lx.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	lx.MustAddRule("NL", core.Literal("\n"))
	var last *bytescan.Token
	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		last = l
	}
	pos := last.Span().End()
	e := bytescan.NewEmptyToken("END", pos)
	assert.Equal(t, bytescan.TokenType("END"), e.Type())
	assert.Equal(t, "", e.Text())
	assert.Zero(t, e.Len())
	assert.Equal(t, cursor.NewSpan(pos, pos), e.Span())
	assert.Equal(t, pos.RuneOffset(), e.Offset())
	assert.Equal(t, pos.ByteOffset(), e.ByteOffset())
	assert.Equal(t, 2, e.Line())
	assert.Equal(t, 2, e.Column())
}
