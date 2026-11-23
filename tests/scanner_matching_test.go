package tests

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check matching: longest match, rule priority as the
// tiebreaker, UNKNOWN tokens, the BOL flag, scanners with many rules, and a
// rule added after the first Next.

func TestScannerProcessor(t *testing.T) {
	const (
		lexTypeInteger    = bytescan.TokenType("INT")
		lexTypeFloat      = bytescan.TokenType("FLOAT")
		lexTypeWhitespace = bytescan.TokenType("WHITESPACE")
		lexTypeIdentifier = bytescan.TokenType("IDENTIFIER")
		lexTypeKeywordIF  = bytescan.TokenType("IF")
		lexTypeLoop       = bytescan.TokenType("LOOP")
		lexTypeComment    = bytescan.TokenType("COMMENT")
		lexTypeHash       = bytescan.TokenType("HASH")
	)

	testCases := []struct {
		name           string
		input          string
		setupRules     func(lx *bytescan.Scanner)
		expectedTokens []*bytescan.Token
		expectedErr    error
	}{
		{
			name:  "Numeric and Whitespace",
			input: "  12.3  \t   4 5.6 \t 7\n 8 9.0",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeWhitespace, whitespaceRule)
				lx.MustAddRule(lexTypeFloat, unsignedFloatRule)
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeWhitespace, "  ", 0),
				bytescan.NewTokenFromString(lexTypeFloat, "12.3", 2),
				bytescan.NewTokenFromString(lexTypeWhitespace, "  \t   ", 6),
				bytescan.NewTokenFromString(lexTypeInteger, "4", 12),
				bytescan.NewTokenFromString(lexTypeWhitespace, " ", 13),
				bytescan.NewTokenFromString(lexTypeFloat, "5.6", 14),
				bytescan.NewTokenFromString(lexTypeWhitespace, " \t ", 17),
				bytescan.NewTokenFromString(lexTypeInteger, "7", 20),
				bytescan.NewTokenFromString(lexTypeWhitespace, "\n ", 21),
				bytescan.NewTokenFromString(lexTypeInteger, "8", 23),
				bytescan.NewTokenFromString(lexTypeWhitespace, " ", 24),
				bytescan.NewTokenFromString(lexTypeFloat, "9.0", 25),
			},
		},
		{
			name:  "Context-Aware Rules (BOL)",
			input: "  # not a comment\n# a comment",
			setupRules: func(lx *bytescan.Scanner) {
				// The comment rule matches '#' only at the beginning of a line,
				// and pushes back its terminator so it is not included in the
				// comment.
				lx.MustAddRule(lexTypeComment, newBOLCommentRule())
				lx.MustAddRule(lexTypeWhitespace, whitespaceRule)
				lx.MustAddRule(lexTypeHash, newLiteralRule("#")) // Fallback for '#' not at BOL
				lx.MustAddRule(lexTypeIdentifier, newIdentifierRule())
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeWhitespace, "  ", 0),
				bytescan.NewTokenFromString(lexTypeHash, "#", 2),
				bytescan.NewTokenFromString(lexTypeWhitespace, " ", 3),
				bytescan.NewTokenFromString(lexTypeIdentifier, "not", 4),
				bytescan.NewTokenFromString(lexTypeWhitespace, " ", 7),
				bytescan.NewTokenFromString(lexTypeIdentifier, "a", 8),
				bytescan.NewTokenFromString(lexTypeWhitespace, " ", 9),
				bytescan.NewTokenFromString(lexTypeIdentifier, "comment", 10),
				bytescan.NewTokenFromString(lexTypeWhitespace, "\n", 17),
				bytescan.NewTokenFromString(lexTypeComment, "# a comment", 18),
			},
		},
		{
			name:  "Empty Input",
			input: "",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeWhitespace, whitespaceRule)
			},
			expectedTokens: []*bytescan.Token{},
		},
		{
			name:  "No Matching Rules (produces UNKNOWN)",
			input: "abc",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "b", 1),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "c", 2),
			},
		},
		{
			name:  "Longest Match (Float vs Integer)",
			input: "12.345",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
				lx.MustAddRule(lexTypeFloat, unsignedFloatRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeFloat, "12.345", 0),
			},
		},
		{
			name:  "Float with trailing decimal at EOF",
			input: "42.",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeFloat, unsignedFloatRule)
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeFloat, "42.", 0),
			},
		},
		{
			name:  "Fallback from potential Float to Integer",
			input: "42a",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeFloat, unsignedFloatRule)
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeInteger, "42", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 2),
			},
		},
		{
			name:  "Tokens immediately at EOF",
			input: "123",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeInteger, "123", 0),
			},
		},
		{
			name:  "Rule Priority (Keyword vs Identifier)",
			input: "if identifier",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeKeywordIF, newLiteralRule("if"))
				lx.MustAddRule(lexTypeIdentifier, identifierRule)
				lx.MustAddRule(lexTypeWhitespace, whitespaceRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeKeywordIF, "if", 0),
				bytescan.NewTokenFromString(lexTypeWhitespace, " ", 2),
				bytescan.NewTokenFromString(lexTypeIdentifier, "identifier", 3),
			},
		},
		{
			name:  "Rule Priority Reversed (Identifier wins)",
			input: "if",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeIdentifier, identifierRule)
				lx.MustAddRule(lexTypeKeywordIF, newLiteralRule("if"))
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeIdentifier, "if", 0),
			},
		},
		{
			name:  "Unicode Characters",
			input: "123\n世界456",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeInteger, "123", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "\n", 3),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "世", 4),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "界", 5),
				bytescan.NewTokenFromString(lexTypeInteger, "456", 6),
			},
		},
		{
			name:  "Adjacent Floats (No Whitespace)",
			input: "1.2.3",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeFloat, unsignedFloatRule)
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeFloat, "1.2", 0),
				bytescan.NewTokenFromString(lexTypeFloat, ".3", 3),
			},
		},
		{
			name:  "Signed Numerics and Math Operators",
			input: "-12+-3++4-5",
			setupRules: func(lx *bytescan.Scanner) {
				// Rule order matters: SignedInteger must come before a general Symbol rule.
				lx.MustAddRule(bytescan.TokenType("SIGNED_INT"), newSignedIntegerRule())
				lx.MustAddRule(bytescan.TokenType("SYMBOL"), newSymbolRule())
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenType("SIGNED_INT"), "-12", 0),
				bytescan.NewTokenFromString(bytescan.TokenType("SYMBOL"), "+", 3),
				bytescan.NewTokenFromString(bytescan.TokenType("SIGNED_INT"), "-3", 4),
				bytescan.NewTokenFromString(bytescan.TokenType("SYMBOL"), "+", 6),
				bytescan.NewTokenFromString(bytescan.TokenType("SIGNED_INT"), "+4", 7),
				bytescan.NewTokenFromString(bytescan.TokenType("SIGNED_INT"), "-5", 9),
			},
		},
		{
			name:  "SQL-like Statement with Comments and Strings",
			input: "SELECT * FROM users WHERE name = 'John Doe' /* a comment */;",
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(bytescan.TokenType("COMMENT"), newSlashStarCommentRule())
				lx.MustAddRule(bytescan.TokenType("STRING"), newSingleQuotedStringRule())
				lx.MustAddRule(bytescan.TokenType("SELECT"), newLiteralRule("SELECT"))
				lx.MustAddRule(bytescan.TokenType("FROM"), newLiteralRule("FROM"))
				lx.MustAddRule(bytescan.TokenType("WHERE"), newLiteralRule("WHERE"))
				lx.MustAddRule(bytescan.TokenType("IDENTIFIER"), newIdentifierRule())
				lx.MustAddRule(bytescan.TokenType("SYMBOL"), newSymbolRule()) // Catches *, =, ;
				lx.MustAddRule(bytescan.TokenType("WHITESPACE"), newWhitespaceRule())
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenType("SELECT"), "SELECT", 0),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 6),
				bytescan.NewTokenFromString(bytescan.TokenType("SYMBOL"), "*", 7),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 8),
				bytescan.NewTokenFromString(bytescan.TokenType("FROM"), "FROM", 9),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 13),
				bytescan.NewTokenFromString(bytescan.TokenType("IDENTIFIER"), "users", 14),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 19),
				bytescan.NewTokenFromString(bytescan.TokenType("WHERE"), "WHERE", 20),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 25),
				bytescan.NewTokenFromString(bytescan.TokenType("IDENTIFIER"), "name", 26),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 30),
				bytescan.NewTokenFromString(bytescan.TokenType("SYMBOL"), "=", 31),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 32),
				bytescan.NewTokenFromString(bytescan.TokenType("STRING"), "'John Doe'", 33),
				bytescan.NewTokenFromString(bytescan.TokenType("WHITESPACE"), " ", 43),
				bytescan.NewTokenFromString(bytescan.TokenType("COMMENT"), "/* a comment */", 44),
				bytescan.NewTokenFromString(bytescan.TokenType("SYMBOL"), ";", 59),
			},
		},
		{
			name:        "No Rules Configured",
			input:       "abc",
			setupRules:  func(lx *bytescan.Scanner) {},
			expectedErr: bytescan.ErrNoRules,
		},
		{
			name:  "Rule Never Settles (UNKNOWN per rune)",
			input: "aaaa",
			setupRules: func(lx *bytescan.Scanner) {
				// This rule always continues and never accepts, so it is still
				// active at end of input. The VM retires it after the EOF
				// symbol; with no recorded accept, each token is one UNKNOWN
				// rune. Each token reads to the end of input first.
				//
				// Engine probe: never settles on purpose (breaks practices 2
				// and 6).
				var brokenRule bytescan.Rule
				brokenRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return brokenRule, bytescan.StateContinue
				}
				lx.MustAddRule(lexTypeLoop, brokenRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 1),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 2),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 3),
			},
		},
		{
			name:  "Excessive Pushback (Index Out of Bounds)",
			input: "a",
			setupRules: func(lx *bytescan.Scanner) {
				// On 'a' the stack holds one symbol, and the rule tries to exclude
				// three. The second pushback passes the bottom of the stack, so the
				// VM retires the rule (processor.go:110) and no rule matches.
				// Engine probe: excludes more symbols than it read on purpose
				// (breaks practices 2 and 6).
				pushbackRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return excludeLast(3, bytescan.StateAccept)
				}
				lx.MustAddRule(lexTypeLoop, pushbackRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
			},
		},
		{
			name:  "Very large input string with no matches",
			input: strings.Repeat("€", 5000),
			setupRules: func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
			},
			// This tests for performance issues or excessive memory allocation.
			expectedTokens: func() []*bytescan.Token {
				tokens := make([]*bytescan.Token, 5000)
				for i := 0; i < 5000; i++ {
					tokens[i] = bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "€", i)
				}
				return tokens
			}(),
		},
		{
			name:  "Zero-Length Match Causes Infinite Loop",
			input: "a",
			setupRules: func(lx *bytescan.Scanner) {
				// A rule that ACCEPTS a match of length 0 must be handled by the scanner
				// to prevent an infinite loop. It should advance by 1 rune as an UNKNOWN token.
				// One pushback on the first symbol excludes it, so the accept has
				// length 0. TestProcessZeroLengthMatch proves that the VM takes the
				// zero-length branch (bytescan.go:555-558), not the no-match path.
				// Engine probe: an empty match on purpose (breaks practices 2
				// and 6).
				zeroLengthRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return excludeLast(1, bytescan.StateAccept)
				}
				lx.MustAddRule(lexTypeLoop, zeroLengthRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
			},
		},
		{
			name:  "Pushback Infinite Loop",
			input: "abc",
			setupRules: func(lx *bytescan.Scanner) {
				// This rule creates a loop by always pushing back and returning to itself.
				// Engine probe: a hand-built chain that never ends, on purpose
				// (breaks practices 2, 5 and 6).
				var pushbackLoopRule bytescan.Rule
				pushbackLoopRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return pushbackLoopRule, bytescan.StatePushBack
				}
				lx.MustAddRule(lexTypeLoop, pushbackLoopRule)
			},
			// The processor's pushback limit should prevent an infinite loop.
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "b", 1),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "c", 2),
			},
		},
		{
			name:  "Rule returns nil, StateContinue",
			input: "a",
			setupRules: func(lx *bytescan.Scanner) {
				// The processor must handle an invalid (nil, StateContinue) return
				// by deactivating the rule to prevent undefined behavior.
				// Engine probe: continues with no next state on purpose (breaks
				// practices 2 and 6).
				badRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return nil, bytescan.StateContinue
				}
				lx.MustAddRule(lexTypeLoop, badRule)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
			},
		},
		{
			name:  "Rule in StateContinue at EOF",
			input: "ab",
			setupRules: func(lx *bytescan.Scanner) {
				// Rule that continues without accepting, then hits EOF.
				// Engine probe: still active at end of input on purpose (breaks
				// practices 1, 2 and 6).
				ruleAtEOF := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					if s.Rune() == 'a' {
						return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
							if s.Rune() == 'b' {
								// Continue without accepting - will hit EOF.
								return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
									return nil, bytescan.StateReject
								}, bytescan.StateContinue
							}
							return nil, bytescan.StateReject
						}, bytescan.StateContinue
					}
					return nil, bytescan.StateReject
				}
				lx.MustAddRule("EOF_CONTINUE", ruleAtEOF)
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "b", 1),
			},
		},
		{
			name:  "Pushback at EOF",
			input: "a",
			setupRules: func(lx *bytescan.Scanner) {
				// The rule continues at 'a' without accepting, so its only accept
				// comes from the pushback: at end of input the first pushback
				// removes the EOF symbol, which leaves "a" as the match.
				lx.MustAddRule("PUSHBACK_EOF", newABeforeAnySymbolRule())
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString(bytescan.TokenType("PUSHBACK_EOF"), "a", 0),
			},
		},
		{
			name:  "BOL and EOL Flags with Multiple Newlines",
			input: "a\n\nb",
			setupRules: func(lx *bytescan.Scanner) {
				// This test requires capturing the flags of each symbol as it is
				// processed. The rule itself will perform the assertions.
				// Engine probe: records every symbol it is shown on purpose
				// (breaks practices 4 and 6).
				var processedSymbols []bytescan.Symbol
				var mu sync.Mutex

				captureRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					if s.IsEOF() {
						return nil, bytescan.StateReject
					}
					mu.Lock()
					processedSymbols = append(processedSymbols, s)
					mu.Unlock()
					return nil, bytescan.StateAccept // Accept one symbol at a time.
				}
				lx.MustAddRule("CAPTURE", captureRule)

				// This anonymous function will be called after the scanner runs
				// to perform the final assertions on the captured symbols.
				t.Cleanup(func() {
					require.Len(t, processedSymbols, 4, "Should have processed 4 symbols")

					// Symbol 'a' at offset 0
					symA := processedSymbols[0]
					assert.Equal(t, 'a', symA.Rune())
					assert.True(t, symA.IsBOF(), "'a' should be at beginning of file")
					assert.True(t, symA.IsBOL(), "'a' should be at beginning of line")
					assert.False(t, symA.IsEOL(), "'a' should not be at end of line")

					// First '\n' at offset 1
					symNL1 := processedSymbols[1]
					assert.Equal(t, '\n', symNL1.Rune())
					assert.False(t, symNL1.IsBOF(), "first '\\n' is not BOF")
					assert.False(t, symNL1.IsBOL(), "first '\\n' is not BOL")
					assert.True(t, symNL1.IsEOL(), "first '\\n' should be EOL")

					// Second '\n' at offset 2
					symNL2 := processedSymbols[2]
					assert.Equal(t, '\n', symNL2.Rune())
					assert.False(t, symNL2.IsBOF(), "second '\\n' is not BOF")
					assert.True(t, symNL2.IsBOL(), "second '\\n' should be BOL")
					assert.True(t, symNL2.IsEOL(), "second '\\n' should be EOL")

					// Symbol 'b' at offset 3
					symB := processedSymbols[3]
					assert.Equal(t, 'b', symB.Rune())
					assert.False(t, symB.IsBOF(), "'b' is not BOF")
					assert.True(t, symB.IsBOL(), "'b' should be BOL")
					assert.False(t, symB.IsEOL(), "'b' should not be EOL")
				})
			},
			expectedTokens: []*bytescan.Token{
				bytescan.NewTokenFromString("CAPTURE", "a", 0),
				bytescan.NewTokenFromString("CAPTURE", "\n", 1),
				bytescan.NewTokenFromString("CAPTURE", "\n", 2),
				bytescan.NewTokenFromString("CAPTURE", "b", 3),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lx := bytescan.New(strings.NewReader(tc.input))
			if tc.setupRules != nil {
				tc.setupRules(lx)
			}

			foundTokens := make([]*bytescan.Token, 0, len(tc.expectedTokens))

			for {
				lex, err := lx.Next()
				if tc.expectedErr != nil {
					require.Error(t, err, "Expected an error but got nil")
					assert.ErrorIs(t, err, tc.expectedErr, "Error does not match")
					return
				}

				if err == io.EOF {
					break
				}
				require.NoError(t, err)

				foundTokens = append(foundTokens, lex)
			}

			ok := assert.Equal(t, tokenKeys(tc.expectedTokens), tokenKeys(foundTokens), "The stream of tokens did not match the expected output.")
			if !ok {
				for i, lex := range foundTokens {
					t.Logf("\tFound[%d]\tText=%q, Type=%q, Offset=%d", i, lex.Text(), lex.Type(), lex.Offset())
				}
			}

			_, err := lx.Next()
			require.Equal(t, io.EOF, err, "Expected EOF after consuming all tokens")
		})
	}
}

// TestScannerWithInputContainingRuneEOF verifies the scanner does not prematurely
// terminate when the input stream itself contains the special RuneEOF value.
func TestScannerWithInputContainingRuneEOF(t *testing.T) {
	const lexTypeID = bytescan.TokenType("ID")
	inputWithEOF := "hello" + string(bytescan.RuneEOF) + "world"

	lx := bytescan.New(strings.NewReader(inputWithEOF))
	lx.MustAddRule(lexTypeID, identifierRule)

	expected := []*bytescan.Token{
		bytescan.NewTokenFromString(lexTypeID, "hello", 0),
		bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, string(bytescan.RuneEOF), 5),
		bytescan.NewTokenFromString(lexTypeID, "world", 6),
	}

	var found []*bytescan.Token
	for {
		lex, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		found = append(found, lex)
	}

	assert.Equal(t, tokenKeys(expected), tokenKeys(found))
}

// TestEqualMatchesResolveToTheFirstRegisteredRule registers three rules in
// each of their six orders. On each token, the longest match wins, and of the
// rules with equal longest matches, the rule registered first wins. The
// scanner decides several tokens in one run, so the test also covers the reset
// of the rule states between tokens.
func TestEqualMatchesResolveToTheFirstRegisteredRule(t *testing.T) {
	const (
		lexTypeLetters = bytescan.TokenType("LETTERS")
		lexTypeAB      = bytescan.TokenType("AB")
		lexTypeAOrB    = bytescan.TokenType("A_OR_B")
		lexTypeSpace   = bytescan.TokenType("SPACE")
	)
	rules := map[bytescan.TokenType]func() bytescan.Rule{
		lexTypeLetters: func() bytescan.Rule { return newRunePlusRule(isASCIILetter) },
		lexTypeAB:      func() bytescan.Rule { return newLiteralRule("ab") },
		lexTypeAOrB:    func() bytescan.Rule { return newRunePlusRule(isAOrB) },
	}
	// tokens lists the tokens of the input and, for each one, the rules
	// whose match is the longest. The first of them in the order of
	// registration is the expected type.
	tokens := []struct {
		text    string
		longest []bytescan.TokenType
	}{
		{"ab", []bytescan.TokenType{lexTypeLetters, lexTypeAB, lexTypeAOrB}},
		{" ", []bytescan.TokenType{lexTypeSpace}},
		{"abc", []bytescan.TokenType{lexTypeLetters}},
		{" ", []bytescan.TokenType{lexTypeSpace}},
		{"ba", []bytescan.TokenType{lexTypeLetters, lexTypeAOrB}},
		{" ", []bytescan.TokenType{lexTypeSpace}},
		{"ab", []bytescan.TokenType{lexTypeLetters, lexTypeAB, lexTypeAOrB}},
	}

	orders := [][]bytescan.TokenType{
		{lexTypeLetters, lexTypeAB, lexTypeAOrB},
		{lexTypeLetters, lexTypeAOrB, lexTypeAB},
		{lexTypeAB, lexTypeLetters, lexTypeAOrB},
		{lexTypeAB, lexTypeAOrB, lexTypeLetters},
		{lexTypeAOrB, lexTypeLetters, lexTypeAB},
		{lexTypeAOrB, lexTypeAB, lexTypeLetters},
	}
	for _, order := range orders {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			var input strings.Builder
			var expected []*bytescan.Token
			for _, tok := range tokens {
				want := tok.longest[0]
				for _, typ := range order {
					if slices.Contains(tok.longest, typ) {
						want = typ
						break
					}
				}
				expected = append(expected, bytescan.NewTokenFromString(want, tok.text, input.Len()))
				input.WriteString(tok.text)
			}

			lx := bytescan.New(strings.NewReader(input.String()))
			for _, typ := range order {
				lx.MustAddRule(typ, rules[typ]())
			}
			lx.MustAddRule(lexTypeSpace, whitespaceRule)

			var found []*bytescan.Token
			for {
				tok, err := lx.Next()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				found = append(found, tok)
			}
			assert.Equal(t, tokenKeys(expected), tokenKeys(found))
		})
	}
}

// TestScannerWithManyRules is a stress test for a large number of rules.
func TestScannerWithManyRules(t *testing.T) {
	const numRules = 2000
	targetLexType := bytescan.TokenType(fmt.Sprintf("RULE_%d", numRules-1))
	targetText := fmt.Sprintf("id%d", numRules-1)
	input := fmt.Sprintf("   %s   ", targetText)

	lx := bytescan.New(strings.NewReader(input))

	for i := 0; i < numRules; i++ {
		ruleText := fmt.Sprintf("id%d", i)
		ruleType := bytescan.TokenType(fmt.Sprintf("RULE_%d", i))
		lx.MustAddRule(ruleType, newLiteralRule(ruleText))
	}
	lx.MustAddRule("WS", whitespaceRule)

	lex, err := lx.Next()
	require.NoError(t, err)
	assert.Equal(t, bytescan.TokenType("WS"), lex.Type())

	lex, err = lx.Next()
	require.NoError(t, err)
	assert.Equal(t, targetLexType, lex.Type())
	assert.Equal(t, targetText, lex.Text())

	lex, err = lx.Next()
	require.NoError(t, err)
	assert.Equal(t, bytescan.TokenType("WS"), lex.Type())

	_, err = lx.Next()
	require.Equal(t, io.EOF, err)
}

// TestScannerComplexInteractions tests complex interactions between multiple rules.
func TestScannerComplexInteractions(t *testing.T) {
	t.Run("Overlapping Keywords and Identifiers", func(t *testing.T) {
		input := "if iffy while whiley"

		lx := bytescan.New(strings.NewReader(input))
		// Keywords first for higher priority on same-length matches.
		lx.MustAddRule("IF", newLiteralRule("if"))
		lx.MustAddRule("WHILE", newLiteralRule("while"))
		lx.MustAddRule("ID", identifierRule)
		lx.MustAddRule("WS", whitespaceRule)

		expected := []bytescan.TokenType{
			"IF",    // "if"
			"WS",    // " "
			"ID",    // "iffy" - longer match wins over "if"
			"WS",    // " "
			"WHILE", // "while"
			"WS",    // " "
			"ID",    // "whiley" - longer match wins over "while"
		}

		for _, expectedType := range expected {
			lex, err := lx.Next()
			require.NoError(t, err)
			assert.Equal(t, expectedType, lex.Type())
		}

		_, err := lx.Next()
		assert.Equal(t, io.EOF, err)
	})

	t.Run("Ambiguous Number Formats", func(t *testing.T) {
		input := "123 123. .123 123.456"

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("FLOAT", unsignedFloatRule)
		lx.MustAddRule("INT", unsignedIntegerRule)
		lx.MustAddRule("WS", whitespaceRule)

		expected := []struct {
			typ  bytescan.TokenType
			text string
		}{
			{"INT", "123"},
			{"WS", " "},
			{"FLOAT", "123."},
			{"WS", " "},
			{"FLOAT", ".123"},
			{"WS", " "},
			{"FLOAT", "123.456"},
		}

		for _, exp := range expected {
			lex, err := lx.Next()
			require.NoError(t, err)
			assert.Equal(t, exp.typ, lex.Type())
			assert.Equal(t, exp.text, lex.Text())
		}

		_, err := lx.Next()
		assert.Equal(t, io.EOF, err)
	})
}

func TestScannerProcessorRecreationWithNewRules(t *testing.T) {
	const (
		lexTypeA = bytescan.TokenType("A")
		lexTypeB = bytescan.TokenType("B")
	)

	input := "a a a b b b"
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeA, newLiteralRule("a"))
	lx.MustAddRule("WS", newWhitespaceRule())

	// Process the first token to ensure the initial processor is created.
	lex, err := lx.Next()
	require.NoError(t, err)
	assert.Equal(t, lexTypeA, lex.Type())

	// Now, add a new rule. This should invalidate the internal processor.
	err = lx.AddRule(lexTypeB, newLiteralRule("b"))
	require.NoError(t, err)

	// Continue lexing. The scanner should seamlessly create a new processor
	// that includes the new rule for "b".
	var foundTypes []bytescan.TokenType
	for {
		lex, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		foundTypes = append(foundTypes, lex.Type())
	}

	// The first 'a' was consumed before the rule change.
	// The rest should be processed with the new rule set.
	expectedTypes := []bytescan.TokenType{
		"WS",
		lexTypeA,
		"WS",
		lexTypeA,
		"WS",
		lexTypeB, // This verifies the new rule was active.
		"WS",
		lexTypeB,
		"WS",
		lexTypeB,
	}
	assert.Equal(t, expectedTypes, foundTypes)
}
