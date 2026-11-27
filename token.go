package bytescan

import "github.com/xiam/bytescan/cursor"

// TokenType is a string identifier for a class of token, e.g., "IDENTIFIER".
type TokenType string

const (
	// TokenTypeUnknown is the type of a token that no rule matched. Each such
	// token holds exactly one rune: unmatched input is never dropped and never
	// joined, so a run of n unmatched runes becomes n tokens.
	TokenTypeUnknown TokenType = "UNKNOWN"
)

// Token is one classified piece of input: its TokenType, its text (its
// lexeme, the input the rule matched), its starting position in the input
// source, and the source span it covers. Next returns the input as a stream of
// tokens.
//
// The span is expressed in the source text's own coordinates — byte offset,
// rune offset, line, and rune column — and is derived from the located runes
// the scanner committed to the token, never from cursor state left over after
// speculative read-ahead.
type Token struct {
	typ TokenType

	text   []rune
	offset int

	span cursor.Span
}

// inlineTokenRunes is the longest token text, in runes, that the scanner
// stores inline with its Token.
const inlineTokenRunes = 8

// tokenBox holds a Token and its unshared short text in one allocation.
// The scanner returns &box.token, whose text slices box.text, so the pointer
// keeps the whole box, text included, alive. A token longer than
// inlineTokenRunes keeps its text in a separate array.
type tokenBox struct {
	token Token
	text  [inlineTokenRunes]rune
}

// asciiTokenText is read-only after initialization. Each entry can back the text
// of any scanner-produced token for that rune, regardless of its type or span.
var asciiTokenText = func() (table [128][1]rune) {
	for i := range table {
		table[i][0] = rune(i)
	}
	return table
}()

// tokenText keeps the winning prefix valid across the processor's next Reset.
// Its byte length comes from the input symbols, including on custom cursors.
func (rp *rulesProcessor) tokenText(n int) ([]rune, *tokenBox, int) {
	if n == 1 {
		s := rp.at(0)
		if r := s.Rune(); uint32(r) < uint32(len(asciiTokenText)) {
			return asciiTokenText[r][:], nil, s.Size()
		}
	}
	if n <= inlineTokenRunes {
		box := new(tokenBox)
		text := box.text[:n:n]
		return text, box, rp.CopyText(text)
	}
	text := make([]rune, n)
	return text, nil, rp.CopyText(text)
}

// NewToken creates and returns a new Token.
//
// The offset is a rune offset, matching Offset. The returned token carries no
// span, so ByteOffset, Line, and Column report zero; use NewTokenWithSpan to
// build a token with full source coordinates.
func NewToken(typ TokenType, text []rune, offset int) *Token {
	return &Token{
		typ:    typ,
		text:   text,
		offset: offset,
	}
}

// NewTokenFromString creates and returns a new Token from a string.
//
// Like NewToken, it records a rune offset only and leaves the span empty.
func NewTokenFromString(typ TokenType, text string, offset int) *Token {
	return &Token{
		typ:    typ,
		text:   []rune(text),
		offset: offset,
	}
}

// NewTokenWithSpan creates a Token from its text and the source span it
// covers. The rune offset reported by Offset is taken from the span, so the
// compatible accessor and the span agree.
func NewTokenWithSpan(typ TokenType, text []rune, span cursor.Span) *Token {
	return &Token{
		typ:    typ,
		text:   text,
		offset: span.Start().RuneOffset(),
		span:   span,
	}
}

// Type returns the type of the token.
func (l *Token) Type() TokenType {
	return l.typ
}

// WithType returns a new token of type t with the same text, offset and span
// as l. l itself is not changed. A filter uses it to relabel a token, for
// example to turn a line break that ends a statement into a NEWLINE.
//
// The new token shares its text with l and costs one allocation.
func (l *Token) WithType(t TokenType) *Token {
	c := *l
	c.typ = t
	return &c
}

// Text returns the textual content of the token, its lexeme, as a string.
func (l *Token) Text() string {
	return string(l.text)
}

// Offset returns the zero-based starting rune offset of the token in the
// original input source.
//
// Offset counts runes, not bytes: it is the rune offset of Span().Start(). Use
// ByteOffset for the byte offset of the same position.
func (l *Token) Offset() int {
	return l.offset
}

// Len returns the length of the token's text in runes. This is useful for
// calculating the end position (Offset + Len).
func (l *Token) Len() int {
	return len(l.text)
}

// Span returns the source span the token covers: a half-open range with byte
// offset, rune offset, line, and rune column at both ends.
func (l *Token) Span() cursor.Span {
	return l.span
}

// ByteOffset returns the zero-based byte offset at which the token starts.
func (l *Token) ByteOffset() int {
	return l.span.Start().ByteOffset()
}

// RuneOffset returns the zero-based rune offset at which the token starts. It
// is the same value Offset returns.
func (l *Token) RuneOffset() int {
	return l.offset
}

// Line returns the 1-based line on which the token starts.
func (l *Token) Line() int {
	return l.span.Start().Line()
}

// Column returns the 0-based rune column at which the token starts.
func (l *Token) Column() int {
	return l.span.Start().Column()
}
