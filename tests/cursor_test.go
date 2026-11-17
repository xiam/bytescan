package tests

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
)

// tokenKeys projects tokens onto the fields a NewTokenFromString-built
// expectation can express. The source span is asserted by the span-aware tests
// in this file, which build expectations from the text itself.
func tokenKeys(tokens []*bytescan.Token) []string {
	keys := make([]string, len(tokens))
	for i, lex := range tokens {
		keys[i] = fmt.Sprintf("%s|%q|%d", lex.Type(), lex.Text(), lex.Offset())
	}
	return keys
}

func lexAll(t *testing.T, lx *bytescan.Scanner) []*bytescan.Token {
	t.Helper()

	var found []*bytescan.Token
	for {
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		found = append(found, lex)
	}

	return found
}

const (
	lexTypeNumber  = bytescan.TokenType("NUMBER")
	lexTypeWord    = bytescan.TokenType("WORD")
	lexTypeSpace   = bytescan.TokenType("SPACE")
	lexTypeNewline = bytescan.TokenType("NEWLINE")
)

func newSimpleScanner(t *testing.T, input string) *bytescan.Scanner {
	t.Helper()

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeWord, newUnicodeLettersRule())
	lx.MustAddRule(lexTypeNumber, newUnsignedIntegerRule())
	lx.MustAddRule(lexTypeNewline, newExactRuneRule('\n'))
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	return lx
}

// TestScannerTokenSpans checks that every token reports the source span it
// covers, in bytes and runes, for ASCII, multi-byte UTF-8, and newlines.
func TestScannerTokenSpans(t *testing.T) {
	const input = "héllo 世界\n42"

	lx := newSimpleScanner(t, input)
	found := lexAll(t, lx)

	// Every byte of the input must be covered by exactly one token, in order.
	var rebuilt strings.Builder
	for i, lex := range found {
		rebuilt.WriteString(lex.Text())

		if i > 0 {
			prev := found[i-1].Span()
			assert.Equal(t, prev.End(), lex.Span().Start(),
				"token %d must start where token %d ended", i, i-1)
		}
	}

	assert.Equal(t, input, rebuilt.String())
	assert.Equal(t, len(input), found[len(found)-1].Span().End().ByteOffset())

	// The first word is multi-byte: 5 runes in 6 bytes.
	first := found[0]
	assert.Equal(t, lexTypeWord, first.Type())
	assert.Equal(t, "héllo", first.Text())
	assert.Equal(t, 0, first.ByteOffset())
	assert.Equal(t, 0, first.RuneOffset())
	assert.Equal(t, 1, first.Line())
	assert.Equal(t, 0, first.Column())
	assert.Equal(t, 5, first.Span().Runes())
	assert.Equal(t, 6, first.Span().Bytes())
	assert.Equal(t, first.Len(), first.Span().Runes(), "Len and the span must agree")
	assert.Equal(t, first.Offset(), first.RuneOffset())

	// The third word starts on line 2, at column 0, after the newline.
	var onLineTwo *bytescan.Token
	for _, lex := range found {
		if lex.Line() == 2 {
			onLineTwo = lex
			break
		}
	}
	require.NotNil(t, onLineTwo)
	assert.Equal(t, lexTypeNumber, onLineTwo.Type())
	assert.Equal(t, "42", onLineTwo.Text())
	assert.Equal(t, 0, onLineTwo.Column())
	assert.Equal(t, 2, onLineTwo.Line())
}

// newRuneBeforeMarkerRule returns a rule for a(?=X)|b(?=Y)|c(?=Z), in Perl
// notation. It decides by one symbol of lookahead and never accepts before it
// has decided. At end of input it rejects the EOF symbol, so it matches
// nothing.
func newRuneBeforeMarkerRule() bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		switch s.Rune() {
		case 'a':
			return beforeMarker('X'), bytescan.StateContinue
		case 'b':
			return beforeMarker('Y'), bytescan.StateContinue
		case 'c':
			return beforeMarker('Z'), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// beforeMarker is the state of newRuneBeforeMarkerRule after the rune that
// marker must follow.
func beforeMarker(marker rune) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == marker {
			return excludeLast(1, bytescan.StateAccept) // the marker
		}
		return nil, bytescan.StateReject
	}
}

// TestScannerSpanIsCommittedNotSpeculated drives a rule that reads ahead past the
// token it matches, then checks that the committed spans cover the input
// exactly. A token's start must not drift to where speculation stopped.
func TestScannerSpanIsCommittedNotSpeculated(t *testing.T) {
	const input = "aXbYcZ"

	// ruleOne matches "a" only when followed by "X" (and "b" before "Y", "c"
	// before "Z"): the scanner reads one symbol past the token to decide, then
	// must commit over exactly one byte. One pushback excludes the lookahead
	// symbol. The lookahead symbol itself matches no rule, so it becomes an
	// UNKNOWN token.
	ruleOne := newRuneBeforeMarkerRule()

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("LOOKAHEAD", ruleOne)

	found := lexAll(t, lx)

	require.Len(t, found, 6)
	for i, lex := range found {
		wantType := bytescan.TokenType("LOOKAHEAD")
		if i%2 == 1 {
			wantType = bytescan.TokenTypeUnknown
		}
		assert.Equal(t, wantType, lex.Type(), "token %d", i)
		assert.Equal(t, string(input[i]), lex.Text())
		assert.Equal(t, i, lex.ByteOffset(), "token %d must start at its own byte", i)
		assert.Equal(t, i, lex.RuneOffset())
		assert.Equal(t, 1, lex.Span().Bytes())
		assert.Equal(t, i+1, lex.Span().End().ByteOffset())
	}
}

// TestScannerRuneReaderSourceSpans checks the compatibility path: a source that
// only implements io.RuneReader still yields correct byte, rune, line, and
// column coordinates.
func TestScannerRuneReaderSourceSpans(t *testing.T) {
	lx := bytescan.New(&runeSource{runes: []rune("ab\n世界")})
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule("CJK", newNonASCIIRunesRule())
	lx.MustAddRule(lexTypeNewline, newExactRuneRule('\n'))
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	found := lexAll(t, lx)
	require.Len(t, found, 3)

	assert.Equal(t, "ab", found[0].Text())
	assert.Equal(t, 0, found[0].ByteOffset())
	assert.Equal(t, 2, found[0].Span().End().ByteOffset())

	newline := found[1]
	assert.Equal(t, lexTypeNewline, newline.Type())
	assert.Equal(t, 2, newline.ByteOffset())
	assert.Equal(t, 1, newline.Line())
	assert.Equal(t, 2, newline.Column())

	// '世界' is two runes in six bytes, starting one byte after the newline.
	cjk := found[2]
	assert.Equal(t, "世界", cjk.Text())
	assert.Equal(t, 3, cjk.ByteOffset())
	assert.Equal(t, 3, cjk.RuneOffset())
	assert.Equal(t, 2, cjk.Line())
	assert.Equal(t, 0, cjk.Column())
	assert.Equal(t, 6, cjk.Span().Bytes())
	assert.Equal(t, 2, cjk.Span().Runes())
	assert.Equal(t, 9, cjk.Span().End().ByteOffset())
}

// TestScannerMaxTokenBytes checks that the retention bound is enforced with a
// deterministic error and no partial progress, and that it does not fire when
// the token fits.
func TestScannerMaxTokenBytes(t *testing.T) {
	t.Run("within the bound", func(t *testing.T) {
		lx := bytescan.NewWithOptions(strings.NewReader("12345 abc"), bytescan.WithMaxTokenBytes(64))
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		lx.MustAddRule(lexTypeNumber, newUnsignedIntegerRule())
		lx.MustAddRule(lexTypeSpace, newSpaceRule())

		found := lexAll(t, lx)
		require.Len(t, found, 3)
		assert.Equal(t, "12345", found[0].Text())
		assert.Equal(t, "abc", found[2].Text())
	})

	t.Run("over the bound", func(t *testing.T) {
		lx := bytescan.NewWithOptions(strings.NewReader(strings.Repeat("a", 1024)),
			bytescan.WithMaxTokenBytes(32))
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())

		lex, err := lx.Next()
		require.Error(t, err)
		assert.Nil(t, lex)
		assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
	})
}

// TestScannerContext checks the diagnostic context accessor: it reaches back
// across the preceding tokens while the token is inside the context window,
// and stops working once later tokens push it out.
func TestScannerContext(t *testing.T) {
	lx := bytescan.New(strings.NewReader("one two three"))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	first, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "one", first.Text())

	ctx, err := lx.Context(first, 0, 4)
	require.NoError(t, err)
	assert.Equal(t, "one two", ctx)

	// One more committed token does not release it: the scanner retains a window
	// of recent tokens so `before` and `after` can reach across them.
	second, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, " ", second.Text())

	ctx, err = lx.Context(first, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "one", ctx, "the first token is still inside the window")

	// Once enough later tokens commit, it falls out of the window and its
	// retained input is released.
	for i := 0; i < 3; i++ {
		_, err = lx.Next()
		require.NoError(t, err)
	}

	_, err = lx.Context(first, 0, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, cursor.ErrPositionOutOfBuffer)
}

// TestScannerNewWithCursor checks the cursor-injection constructor.
func TestScannerNewWithCursor(t *testing.T) {
	cur := cursor.NewReader(strings.NewReader("ab\ncd"))

	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeNewline, newExactRuneRule('\n'))

	found := lexAll(t, lx)
	require.Len(t, found, 3)
	assert.Equal(t, "ab", found[0].Text())
	assert.Equal(t, 1, found[0].Line())
	assert.Equal(t, "cd", found[2].Text())
	assert.Equal(t, 2, found[2].Line())
	assert.Equal(t, 3, found[2].ByteOffset())
}

// The function type of NewWithCursor is part of the API: callers store it as a
// function value, so it must not gain a variadic parameter.
var _ func(bytescan.Cursor) *bytescan.Scanner = bytescan.NewWithCursor

// TestScannerNewWithCursorOptionsMaxTokenBytes checks that WithMaxTokenBytes
// bounds a scanner over a caller-owned cursor, with the same error as
// NewWithOptions, and that NewWithCursor stays unbounded.
func TestScannerNewWithCursorOptionsMaxTokenBytes(t *testing.T) {
	input := strings.Repeat("x", 100)
	run := core.RuneRun(core.RuneSet("x"))

	t.Run("with the option", func(t *testing.T) {
		cur := cursor.New(strings.NewReader(input))
		lx := bytescan.NewWithCursorOptions(cur, bytescan.WithMaxTokenBytes(16))
		lx.MustAddRule(lexTypeWord, run)

		lex, err := lx.Next()
		require.Error(t, err)
		assert.Nil(t, lex)
		assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
		assert.Equal(t, 0, cur.Cursor().ByteOffset(), "a failed Next consumes nothing")
	})

	t.Run("without the option", func(t *testing.T) {
		cur := cursor.New(strings.NewReader(input))
		lx := bytescan.NewWithCursor(cur)
		lx.MustAddRule(lexTypeWord, run)

		found := lexAll(t, lx)
		require.Len(t, found, 1)
		assert.Equal(t, input, found[0].Text())
	})

	t.Run("cursor budget below the bound", func(t *testing.T) {
		// The scanner does not size a caller-owned cursor, so the cursor's own
		// retention budget fails the token before the per-token bound does.
		cur := cursor.NewReader(strings.NewReader(input), cursor.WithMaxRetained(8))
		lx := bytescan.NewWithCursorOptions(cur, bytescan.WithMaxTokenBytes(64))
		lx.MustAddRule(lexTypeWord, run)

		lex, err := lx.Next()
		require.Error(t, err)
		assert.Nil(t, lex)
		assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
		assert.NotErrorIs(t, err, bytescan.ErrTokenTooLarge, "the cursor's budget fails first, not the scanner's bound")
		assert.Equal(t, 0, cur.Cursor().ByteOffset(), "a failed Next consumes nothing")
	})

	t.Run("no options", func(t *testing.T) {
		cur := cursor.New(strings.NewReader(input))
		lx := bytescan.NewWithCursorOptions(cur)
		lx.MustAddRule(lexTypeWord, run)

		found := lexAll(t, lx)
		require.Len(t, found, 1)
		assert.Equal(t, input, found[0].Text())
	})
}

// TestScannerSharedCursorPerScannerMaxTokenBytes checks that two scanners on one
// cursor each enforce their own WithMaxTokenBytes bound. The driver releases
// the outgoing scanner at every switch, as a mode driver does.
func TestScannerSharedCursorPerScannerMaxTokenBytes(t *testing.T) {
	cur := cursor.New(strings.NewReader("xxxxxxxx xx " + strings.Repeat("x", 20)))

	newScanner := func(n int) *bytescan.Scanner {
		lx := bytescan.NewWithCursorOptions(cur, bytescan.WithMaxTokenBytes(n))
		lx.MustAddRule(lexTypeWord, core.RuneRun(core.RuneSet("x")))
		lx.MustAddRule(lexTypeSpace, newSpaceRule())
		return lx
	}
	small, large := newScanner(4), newScanner(16)

	next := func(lx, other *bytescan.Scanner) (*bytescan.Token, error) {
		other.Release()
		return lx.Next()
	}
	requireToken := func(lex *bytescan.Token, err error, text string) {
		t.Helper()
		require.NoError(t, err)
		require.NotNil(t, lex)
		assert.Equal(t, text, lex.Text())
	}
	requireOverBound := func(lex *bytescan.Token, err error) {
		t.Helper()
		require.Error(t, err)
		assert.Nil(t, lex)
		assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
		assert.ErrorIs(t, err, bytescan.ErrTokenTooLarge)
	}

	// 8 bytes: over the small bound, within the large one.
	lex, err := next(small, large)
	requireOverBound(lex, err)
	lex, err = next(large, small)
	requireToken(lex, err, "xxxxxxxx")

	// 2 bytes: within the small bound.
	lex, err = next(small, large)
	requireToken(lex, err, " ")
	lex, err = small.Next()
	requireToken(lex, err, "xx")
	lex, err = small.Next()
	requireToken(lex, err, " ")

	// 20 bytes: over both bounds, each with its own limit.
	lex, err = small.Next()
	requireOverBound(lex, err)
	lex, err = next(large, small)
	requireOverBound(lex, err)
	assert.Equal(t, 12, cur.Cursor().ByteOffset(), "a failed Next consumes nothing")
}

func TestScannerContextRejectsNilToken(t *testing.T) {
	lx := bytescan.New(strings.NewReader("x"))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())

	_, err := lx.Context(nil, 1, 1)
	require.ErrorIs(t, err, bytescan.ErrNilToken)
}

// isUnicodeLetter reports whether r is in \pL.
func isUnicodeLetter(r rune) bool { return unicode.IsLetter(r) }

// isNonASCII reports whether r is in [^\x00-\x7F].
func isNonASCII(r rune) bool { return r >= 0x80 }

// newUnicodeLettersRule returns a rule for \pL+. It accepts after each letter.
// At end of input it rejects the EOF symbol, so the letters read so far stay
// the match.
func newUnicodeLettersRule() bytescan.Rule {
	return newRunePlusRule(isUnicodeLetter)
}

// newNonASCIIRunesRule returns a rule for [^\x00-\x7F]+. It accepts after each
// non-ASCII rune. At end of input it rejects the EOF symbol, so the runes read
// so far stay the match.
func newNonASCIIRunesRule() bytescan.Rule {
	return newRunePlusRule(isNonASCII)
}

// newSpaceRule returns a rule for a single space, " ".
func newSpaceRule() bytescan.Rule {
	return newExactRuneRule(' ')
}

// newExactRuneRule returns a rule for the single rune r. It accepts r and
// stops. At end of input it rejects the EOF symbol.
func newExactRuneRule(r rune) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == r {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
}

// runeSource implements io.RuneReader and nothing else.
type runeSource struct {
	runes []rune
	pos   int
}

func (s *runeSource) ReadRune() (rune, int, error) {
	if s.pos >= len(s.runes) {
		return 0, 0, io.EOF
	}

	r := s.runes[s.pos]
	s.pos++

	return r, len(string(r)), nil
}

func BenchmarkScannerCursor(b *testing.B) {
	input := strings.Repeat("the quick brown fox 12345 jumps over 42 lazy dogs\n", 256)

	b.SetBytes(int64(len(input)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		lx.MustAddRule(lexTypeNumber, newUnsignedIntegerRule())
		lx.MustAddRule(lexTypeNewline, newExactRuneRule('\n'))
		lx.MustAddRule(lexTypeSpace, newSpaceRule())

		for {
			if _, err := lx.Next(); err != nil {
				break
			}
		}
	}
}
