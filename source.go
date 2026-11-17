package bytescan

import "github.com/xiam/bytescan/cursor"

// Source is anything that yields tokens one at a time: a *Scanner, or a
// filter or driver in a layer above Next.
//
// Each call to Next returns either a token and a nil error, or a nil token
// and a non-nil error. At the end of the stream the error is io.EOF itself, not
// a wrapped value, so a caller can compare it with ==. A value whose Next can
// return a nil token with a nil error is not a Source.
//
// *Scanner satisfies Source. A filter that reads one Source and returns
// another, such as Skip, keeps this contract as long as the Source it reads
// keeps it.
type Source interface {
	Next() (*Token, error)
}

var _ Source = (*Scanner)(nil)

// Skip returns a Source that reads src and drops every token whose type is one
// of types. Every other token, and every error, including io.EOF, passes
// through unchanged, in order.
//
// Skip reads src only when its own Next is called, and it holds no token back.
// It allocates only when it is called, and nothing for each token it passes
// or drops. With no types, Skip returns src itself.
//
// To keep a few types instead of dropping a few, list the types to drop: the
// set of types is the one the caller registered its rules under, plus
// TokenTypeUnknown.
func Skip(src Source, types ...TokenType) Source {
	if len(types) == 0 {
		return src
	}
	return &skipFilter{
		src:   src,
		types: append([]TokenType(nil), types...),
	}
}

type skipFilter struct {
	src   Source
	types []TokenType
}

// Next returns the next token of the underlying Source whose type is not
// skipped, or the first error it returns. A nil token with a nil error, which
// breaks the Source contract, is passed through rather than dereferenced.
func (f *skipFilter) Next() (*Token, error) {
	for {
		l, err := f.src.Next()
		if err != nil {
			return nil, err
		}
		if l == nil || !f.skips(l.typ) {
			return l, nil
		}
	}
}

func (f *skipFilter) skips(t TokenType) bool {
	for _, s := range f.types {
		if s == t {
			return true
		}
	}
	return false
}

// NewEmptyToken returns a token of type typ with no text at position pos: its
// span starts and ends at pos, so Offset, ByteOffset, Line and Column report
// the coordinates of pos and Len is 0.
//
// A filter uses it for a synthetic token that stands for no input, such as an
// inserted semicolon or an INDENT. The scanner itself never returns an empty
// token.
func NewEmptyToken(typ TokenType, pos cursor.Pos) *Token {
	return NewTokenWithSpan(typ, nil, cursor.NewSpan(pos, pos))
}
