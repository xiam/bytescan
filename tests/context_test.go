package tests

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
)

// TestScannerRetentionFailureConsumesNothing covers a token rejected by a
// retention bound: the cursor must be rewound to the token start, so the call
// consumes nothing and a retry behaves identically instead of resuming in the
// middle of the rejected token.
func TestScannerRetentionFailureConsumesNothing(t *testing.T) {
	t.Run("cursor bound", func(t *testing.T) {
		cur := cursor.NewReader(strings.NewReader("abcdef"), cursor.WithMaxRetained(3))

		lx := bytescan.NewWithCursor(cur)
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())

		rejected := func() {
			lex, err := lx.Next()
			require.Error(t, err)
			assert.Nil(t, lex)
			assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
			assert.Equal(t, 0, cur.Cursor().ByteOffset(), "a rejected token must consume nothing")
		}

		// A retry sees the same over-long token, not the tail of it.
		rejected()
		rejected()
	})

	t.Run("token bound", func(t *testing.T) {
		lx := bytescan.NewWithOptions(
			strings.NewReader("abcdef"),
			bytescan.WithMaxTokenBytes(3),
		)
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())

		rejected := func() {
			lex, err := lx.Next()
			require.Error(t, err)
			assert.Nil(t, lex)
			assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
		}

		// Without the rewind the retry would read the remaining three bytes,
		// reach EOF, and commit them as a token.
		rejected()
		rejected()
	})
}

// TestScannerContextReachesPrecedingTokens covers the `before` window: the
// retained input must include the tokens that precede the one being asked
// about, not just the token itself.
func TestScannerContextReachesPrecedingTokens(t *testing.T) {
	lx := bytescan.New(strings.NewReader("one two"))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	first, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "one", first.Text())

	second, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, " ", second.Text())

	third, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "two", third.Text())

	ctx, err := lx.Context(third, 4, 0)
	require.NoError(t, err)
	assert.Equal(t, "one two", ctx, "before must reach back across the preceding tokens")

	// The window is finite: a wider request is clamped to what is retained.
	ctx, err = lx.Context(third, 64, 0)
	require.NoError(t, err)
	assert.Equal(t, "one two", ctx, "a wider request clamps, it does not error")
}

// TestScannerContextSurvivesFailedNext covers a Next that commits nothing: the
// previous token's context must stay readable, because no token was committed
// to displace it.
func TestScannerContextSurvivesFailedNext(t *testing.T) {
	lx := bytescan.NewWithOptions(
		strings.NewReader("a bbbbb"),
		bytescan.WithMaxTokenBytes(2),
	)
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	first, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "a", first.Text())

	second, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, " ", second.Text())

	// The next token cannot fit the bound, so this call fails and commits
	// nothing.
	lex, err := lx.Next()
	require.Error(t, err)
	assert.Nil(t, lex)
	assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)

	ctx, err := lx.Context(second, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, " ", ctx, "a failed Next must not release the previous context")
}

// TestScannerContextSurvivesEOFProbe covers the final EOF probe: it commits
// nothing, so the previous token's context must stay readable.
func TestScannerContextSurvivesEOFProbe(t *testing.T) {
	lx := bytescan.New(strings.NewReader("ab"))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())

	first, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "ab", first.Text())

	_, err = lx.Next()
	require.ErrorIs(t, err, io.EOF)

	ctx, err := lx.Context(first, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, "ab", ctx, "the EOF probe must not release the previous context")
}

// TestScannerMaxTokenBytesAcrossWindow covers consecutive tokens at the
// configured bound: the cursor budget must cover the retained context window
// plus the token currently being assembled, so a stream of tokens that each fit
// the bound must lex all the way through.
func TestScannerMaxTokenBytesAcrossWindow(t *testing.T) {
	lx := bytescan.NewWithOptions(strings.NewReader("abcd"), bytescan.WithMaxTokenBytes(1))
	lx.MustAddRule(lexTypeWord, newExactRuneRule('a'))
	lx.MustAddRule(lexTypeNumber, newExactRuneRule('b'))
	lx.MustAddRule(lexTypeSpace, newExactRuneRule('c'))
	lx.MustAddRule(lexTypeNewline, newExactRuneRule('d'))

	var got []string
	for {
		lex, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		got = append(got, lex.Text())
	}

	assert.Equal(t, []string{"a", "b", "c", "d"}, got)
}

// TestScannerReleaseHandsOffSharedCursor covers the shared-cursor path the
// constructor advertises: once the scanner releases the checkpoints it holds for
// its context window, another consumer can read the remainder through the same
// finite-retention cursor.
func TestScannerReleaseHandsOffSharedCursor(t *testing.T) {
	cur := cursor.NewReader(strings.NewReader("abcdef"), cursor.WithMaxRetained(3))

	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule(lexTypeWord, newExactRuneRule('a'))

	lex, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "a", lex.Text())

	// While the scanner holds its context checkpoint the cursor keeps reserving
	// the input the window needs, so the handoff releases it first.
	lx.Release()

	rest := make([]byte, 8)
	n, err := cur.Read(rest)
	require.NoError(t, err)
	assert.Equal(t, "bcdef", string(rest[:n]), "the next consumer reads the remainder")
}
