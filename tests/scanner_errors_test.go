package tests

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check errors: reader errors returned by Next,
// AddRule errors, and lexing after an UNKNOWN token, a retired rule or a
// reader error.

// A mock reader that fails N times before succeeding.
// errTemporaryReader is the error recoverableReader returns while it fails.
var errTemporaryReader = errors.New("temporary reader error")

type recoverableReader struct {
	reader    io.RuneReader
	failCount int
	maxFails  int
}

func (rr *recoverableReader) ReadRune() (rune, int, error) {
	if rr.failCount < rr.maxFails {
		rr.failCount++
		return 0, 0, errTemporaryReader
	}
	return rr.reader.ReadRune()
}

// errSimulatedReader is the error faultyReader returns once it fails.
var errSimulatedReader = errors.New("simulated underlying reader error")

// faultyReader simulates an io.Reader that returns an error after N reads.
type faultyReader struct {
	reader     io.RuneReader
	failAfterN int
	readCount  int
}

func (fr *faultyReader) ReadRune() (r rune, size int, err error) {
	if fr.readCount >= fr.failAfterN {
		return 0, 0, errSimulatedReader
	}
	fr.readCount++
	return fr.reader.ReadRune()
}

// TestScannerHandlesReaderError verifies that the scanner gracefully propagates
// an error from the underlying reader.
func TestScannerHandlesReaderError(t *testing.T) {
	const lexTypeID = bytescan.TokenType("ID")
	input := "abc def"
	reader := &faultyReader{
		reader:     strings.NewReader(input),
		failAfterN: 5, // Fails when trying to read 'e'
	}

	lx := bytescan.New(reader)
	lx.MustAddRule(lexTypeID, identifierRule)
	lx.MustAddRule("WS", whitespaceRule)

	_, err := lx.Next()
	require.NoError(t, err)

	_, err = lx.Next()
	require.NoError(t, err)

	_, err = lx.Next()
	require.Error(t, err)
	assert.ErrorIs(t, err, errSimulatedReader)
}

// TestScannerErrorConditions comprehensively tests error handling.
func TestScannerErrorConditions(t *testing.T) {
	t.Run("Duplicate Rule Types", func(t *testing.T) {
		lx := bytescan.New(strings.NewReader("test"))

		err := lx.AddRule("TEST", newLiteralRule("test"))
		require.NoError(t, err)

		err = lx.AddRule("TEST", newLiteralRule("other"))
		assert.ErrorIs(t, err, bytescan.ErrDuplicateRule)
	})

	t.Run("Nil Rule", func(t *testing.T) {
		lx := bytescan.New(strings.NewReader("test"))

		err := lx.AddRule("NIL", nil)
		assert.ErrorIs(t, err, bytescan.ErrNilRule)
	})

	t.Run("Empty TokenType", func(t *testing.T) {
		lx := bytescan.New(strings.NewReader("test"))

		err := lx.AddRule("", newLiteralRule("test"))
		assert.ErrorIs(t, err, bytescan.ErrEmptyRuleType)
	})

	t.Run("Reader Error Propagation", func(t *testing.T) {
		testCases := []struct {
			name      string
			failAfter int
			input     string
		}{
			{"Immediate failure", 0, "test"},
			{"Mid-token failure", 2, "test"},
			{"Between tokens failure", 5, "test test"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				reader := &faultyReader{
					reader:     strings.NewReader(tc.input),
					failAfterN: tc.failAfter,
				}

				lx := bytescan.New(reader)
				lx.MustAddRule("ID", identifierRule)
				lx.MustAddRule("WS", whitespaceRule)

				var lastErr error
				for {
					_, err := lx.Next()
					if err != nil {
						lastErr = err
						break
					}
				}

				assert.Error(t, lastErr)
				if lastErr != io.EOF {
					assert.Contains(t, lastErr.Error(), "simulated")
				}
			})
		}
	})

	t.Run("Rule Panic Types Are Recovered", func(t *testing.T) {
		// A panic with any value is recovered and retires the rule, a nil
		// value included (on Go 1.21+ panic(nil) raises a
		// *runtime.PanicNilError). A sibling rule's match survives.
		testCases := []struct {
			name       string
			panicValue interface{}
		}{
			{"String panic", "rule panic: string"},
			{"Error panic", errors.New("rule panic: error")},
			{"Integer panic", 42},
			{"Nil panic", nil},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				input := "test"

				// Engine probe: panics on purpose (breaks practice 6).
				panicRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					panic(tc.panicValue)
				}

				t.Run("only rule", func(t *testing.T) {
					lx := bytescan.New(strings.NewReader(input))
					lx.MustAddRule("PANIC", panicRule)

					assertRetireStream(t, lx, unknownTokens(input, 0))
				})

				t.Run("with a sibling", func(t *testing.T) {
					lx := bytescan.New(strings.NewReader(input))
					lx.MustAddRule("PANIC", panicRule)
					lx.MustAddRule("WORD", newASCIILettersRule())

					assertRetireStream(t, lx, []*bytescan.Token{
						bytescan.NewTokenFromString("WORD", input, 0),
					})
				})
			})
		}
	})
}

// TestScannerRecoveryMechanisms tests the scanner's ability to recover from various error conditions.
func TestScannerRecoveryMechanisms(t *testing.T) {
	t.Run("Recovery After Unknown Tokens", func(t *testing.T) {
		input := "valid @#$ valid"

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("ID", identifierRule)
		lx.MustAddRule("WS", whitespaceRule)

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("ID"), lex.Type())
		assert.Equal(t, "valid", lex.Text())

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("WS"), lex.Type())

		for i := 0; i < 3; i++ {
			lex, err = lx.Next()
			require.NoError(t, err)
			assert.Equal(t, bytescan.TokenTypeUnknown, lex.Type())
		}

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("WS"), lex.Type())

		// Should recover and parse next valid token.
		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("ID"), lex.Type())
		assert.Equal(t, "valid", lex.Text())
	})

	t.Run("Recovery After Rule Deactivation", func(t *testing.T) {
		input := "test test"
		// Engine probe: counts its calls in a captured variable on purpose
		// (breaks practices 2, 3, 4 and 6).
		callCount := 0

		var selfDeactivatingRule bytescan.Rule
		// Rule that deactivates itself after the first match.
		selfDeactivatingRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			callCount++
			if callCount > 4 { // Deactivate after matching "test".
				return nil, bytescan.StateReject
			}
			if s.Rune() >= 'a' && s.Rune() <= 'z' {
				return selfDeactivatingRule, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("DEACTIVATE", selfDeactivatingRule)
		lx.MustAddRule("ID", identifierRule) // Backup rule.
		lx.MustAddRule("WS", whitespaceRule)

		// First token uses the deactivating rule.
		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("DEACTIVATE"), lex.Type())
		assert.Equal(t, "test", lex.Text())

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("WS"), lex.Type())

		// Second token should use the backup rule since the first one deactivated.
		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("ID"), lex.Type())
		assert.Equal(t, "test", lex.Text())
	})

	t.Run("Recovery from Reader Errors (if reader recovers)", func(t *testing.T) {
		// This test verifies that the scanner correctly propagates an error from the
		// underlying reader and halts.

		// Create a reader that fails once on the first character.
		reader := &recoverableReader{
			reader:   strings.NewReader("test"),
			maxFails: 1,
		}

		lx := bytescan.New(reader)
		lx.MustAddRule("ID", identifierRule)

		// The first call to Next() will attempt to read the first rune 't', which fails.
		lex, err := lx.Next()

		// The scanner should propagate this error and return no token.
		require.Error(t, err, "Expected an error from the failing reader")
		assert.Nil(t, lex, "Token should be nil on reader error")
		assert.ErrorIs(t, err, errTemporaryReader, "the reader's error keeps its identity")
	})
}
