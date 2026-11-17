package tests

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check the end of input and other boundaries: rules
// in each state at the EOF symbol, an accept of the EOF symbol, a rule still
// active at the end, and single-rune and maximum-size inputs.

// TestScannerEdgeCasesAtEOF tests various edge cases that occur at end of file.
func TestScannerEdgeCasesAtEOF(t *testing.T) {
	t.Run("Rule in StateAccept at EOF", func(t *testing.T) {
		input := "test"

		// The rule accepts after each lowercase letter, up to end of input.
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("GREEDY", newRunePlusRule(isASCIILowercase))

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, "test", lex.Text())

		_, err = lx.Next()
		assert.Equal(t, io.EOF, err)
	})

	t.Run("Multiple Rules in Different States at EOF", func(t *testing.T) {
		input := "ab"

		// Rule A accepts 'a', continues on 'b', but never accepts the final state.
		// Engine probe: still active at end of input on purpose (breaks
		// practices 1, 2 and 6).
		ruleA := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'a' {
				return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					if s.Rune() == 'b' {
						return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
							return nil, bytescan.StateReject
						}, bytescan.StateContinue // Continue without accepting 'ab'.
					}
					return nil, bytescan.StateReject
				}, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		// Rule B accepts both 'a' and 'b'.
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("A", ruleA)
		lx.MustAddRule("B", newRunePlusRule(isAOrB))

		// Rule B should win with "ab" since its accepted match is longer than A's ("a").
		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("B"), lex.Type())
		assert.Equal(t, "ab", lex.Text())

		_, err = lx.Next()
		assert.Equal(t, io.EOF, err)
	})

	t.Run("Empty Input with Complex Rules", func(t *testing.T) {
		input := ""

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("FLOAT", unsignedFloatRule)
		lx.MustAddRule("INT", unsignedIntegerRule)
		lx.MustAddRule("ID", identifierRule)
		lx.MustAddRule("COMMENT", newSlashStarCommentRule())

		_, err := lx.Next()
		assert.Equal(t, io.EOF, err, "Empty input should immediately return EOF")
	})
}

// TestScannerBoundaryConditions tests various boundary conditions.
func TestScannerBoundaryConditions(t *testing.T) {
	t.Run("Single Rune Input", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected bytescan.TokenType
		}{
			{"1", bytescan.TokenType("INT")},
			{"a", bytescan.TokenType("ID")},
			{" ", bytescan.TokenType("WS")},
			{"€", bytescan.TokenTypeUnknown},
		}

		for _, tc := range testCases {
			lx := bytescan.New(strings.NewReader(tc.input))
			lx.MustAddRule("INT", unsignedIntegerRule)
			lx.MustAddRule("ID", identifierRule)
			lx.MustAddRule("WS", whitespaceRule)

			lex, err := lx.Next()
			require.NoError(t, err)
			assert.Equal(t, tc.expected, lex.Type())
			assert.Equal(t, tc.input, lex.Text())

			_, err = lx.Next()
			assert.Equal(t, io.EOF, err)
		}
	})

	t.Run("Maximum Rule Count", func(t *testing.T) {
		const maxRules = 10000
		input := "test"

		lx := bytescan.New(strings.NewReader(input))

		for i := 0; i < maxRules; i++ {
			ruleType := bytescan.TokenType(fmt.Sprintf("RULE_%d", i))
			ruleText := fmt.Sprintf("rule%d", i)
			err := lx.AddRule(ruleType, newLiteralRule(ruleText))
			require.NoError(t, err, "Should handle %d rules", i+1)
		}

		lx.MustAddRule("TEST", newLiteralRule("test"))

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("TEST"), lex.Type())
		assert.Equal(t, "test", lex.Text())
	})

	t.Run("Maximum Token Length", func(t *testing.T) {
		sizes := []int{
			255,     // 8-bit boundary
			256,     // 8-bit boundary + 1
			65535,   // 16-bit boundary
			65536,   // 16-bit boundary + 1
			1048576, // 1MB
		}

		for _, size := range sizes {
			t.Run(fmt.Sprintf("Size_%d", size), func(t *testing.T) {
				input := strings.Repeat("a", size) + " "

				lx := bytescan.New(strings.NewReader(input))
				lx.MustAddRule("ID", identifierRule)
				lx.MustAddRule("WS", whitespaceRule)

				lex, err := lx.Next()
				require.NoError(t, err)
				assert.Equal(t, size, lex.Len())
				assert.Equal(t, bytescan.TokenType("ID"), lex.Type())

				lex, err = lx.Next()
				require.NoError(t, err)
				assert.Equal(t, bytescan.TokenType("WS"), lex.Type())
			})
		}
	})
}

// TestAcceptOfEOFSymbolIsDiscarded: an accept whose length counts the synthetic
// EOF symbol is longer than the symbols read. The VM discards that verdict and
// retires the rule; an earlier accept by the same rule stays, and a sibling
// rule's match is emitted. An accept whose pushback chain excludes the EOF
// symbol is valid. See "EOF handling" in docs/specs/rule-vm-contract.md.
func TestAcceptOfEOFSymbolIsDiscarded(t *testing.T) {
	const (
		lexTypeWord = bytescan.TokenType("WORD")
		lexTypeR    = bytescan.TokenType("R")
	)

	t.Run("sibling", func(t *testing.T) {
		// Engine probe: accepts the EOF symbol on purpose (breaks practice 2).
		//
		// continueThenAcceptEOF continues on every real symbol and accepts the
		// EOF symbol. It records no other accept.
		var continueThenAcceptEOF bytescan.Rule
		continueThenAcceptEOF = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() {
				return nil, bytescan.StateAccept
			}
			return continueThenAcceptEOF, bytescan.StateContinue
		}

		// R is registered first. Its discarded accept of three symbols would
		// otherwise be the longest.
		lx := bytescan.New(strings.NewReader("ab"))
		lx.MustAddRule(lexTypeR, continueThenAcceptEOF)
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())

		assertRetireStream(t, lx, []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeWord, "ab", 0),
		})
	})

	t.Run("earlier accept by the same rule", func(t *testing.T) {
		// Engine probe: accepts the EOF symbol on purpose (breaks practice 2).
		//
		// acceptAll accepts every letter and also the EOF symbol. Its accept
		// of "ab" is recorded; its accept of the EOF symbol is discarded.
		var acceptAll bytescan.Rule
		acceptAll = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() {
				return nil, bytescan.StateAccept
			}
			if isASCIILetter(s.Rune()) {
				return acceptAll, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader("ab"))
		lx.MustAddRule(lexTypeR, acceptAll)

		assertRetireStream(t, lx, []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeR, "ab", 0),
		})
	})

	// The regression guards: a pushback chain at EOF that excludes the EOF
	// symbol accepts the real symbols. With a nil next the rule stops; with a
	// non-nil next the accept is recorded and the rule is retired, because no
	// input follows the EOF symbol.
	for _, tc := range []struct {
		name string
		next func(self bytescan.Rule) bytescan.Rule
	}{
		{"pushback excludes the EOF symbol, nil next", func(bytescan.Rule) bytescan.Rule { return nil }},
		{"pushback excludes the EOF symbol, non-nil next", func(self bytescan.Rule) bytescan.Rule { return self }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Engine probe: a hand-built chain callback, with a non-nil next
			// in one case, on purpose (breaks practices 1, 2 and 5).
			var rule bytescan.Rule
			rule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				if s.IsEOF() {
					return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
						return tc.next(rule), bytescan.StateAccept
					}, bytescan.StatePushBack
				}
				if isASCIILetter(s.Rune()) {
					return rule, bytescan.StateContinue
				}
				return nil, bytescan.StateReject
			}

			lx := bytescan.New(strings.NewReader("ab"))
			lx.MustAddRule(lexTypeR, rule)

			assertRetireStream(t, lx, []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeR, "ab", 0),
			})
		})
	}
}

// TestStillActiveAtEOFRetiresRule: after the EOF symbol every rule that is still
// active is retired, and the winner is chosen from the recorded accepts. A
// sibling that never settles does not fail the token. See "EOF handling" in
// docs/specs/rule-vm-contract.md; the single never-settling rule is the
// TestScannerProcessor case "Rule Never Settles (UNKNOWN per rune)".
func TestStillActiveAtEOFRetiresRule(t *testing.T) {
	const (
		lexTypeWord = bytescan.TokenType("WORD")
		lexTypeSib  = bytescan.TokenType("SIB")
	)

	// Engine probes: alwaysContinue never settles, and alwaysAccept accepts the
	// EOF symbol, on purpose (breaks practices 2 and 6).
	//
	// alwaysContinue returns itself with StateContinue on every symbol, the EOF
	// symbol included. It records no accept.
	var alwaysContinue bytescan.Rule
	alwaysContinue = func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return alwaysContinue, bytescan.StateContinue
	}

	// alwaysAccept returns itself with StateAccept on every symbol. It records
	// the accept of "ab"; its accept of the EOF symbol is discarded.
	var alwaysAccept bytescan.Rule
	alwaysAccept = func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return alwaysAccept, bytescan.StateAccept
	}

	t.Run("StateContinue sibling", func(t *testing.T) {
		lx := bytescan.New(strings.NewReader("ab"))
		lx.MustAddRule(lexTypeSib, alwaysContinue)
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())

		assertRetireStream(t, lx, []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeWord, "ab", 0),
		})
	})

	t.Run("(self, StateAccept) sibling", func(t *testing.T) {
		// SIB also records "ab", so the two rules tie and WORD, registered
		// first, wins.
		lx := bytescan.New(strings.NewReader("ab"))
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		lx.MustAddRule(lexTypeSib, alwaysAccept)

		assertRetireStream(t, lx, []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeWord, "ab", 0),
		})
	})

	t.Run("(self, StateAccept) sibling registered first keeps its accept", func(t *testing.T) {
		// Registered first, SIB wins the tie with the accept it recorded
		// before the EOF symbol.
		lx := bytescan.New(strings.NewReader("ab"))
		lx.MustAddRule(lexTypeSib, alwaysAccept)
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())

		assertRetireStream(t, lx, []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeSib, "ab", 0),
		})
	})
}
