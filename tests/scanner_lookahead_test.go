package tests

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/textreader"
)

// The tests in this file check lookahead built from pushback: multi-symbol and
// variable-length lookahead, delayed acceptance, and lookahead at the end of
// input and at the byte bound of the reader.

// unknownTokens returns one single-rune UNKNOWN token for each rune of s, the
// first at rune offset offset.
func unknownTokens(s string, offset int) []*bytescan.Token {
	var tokens []*bytescan.Token
	for i, r := range []rune(s) {
		tokens = append(tokens, bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, string(r), offset+i))
	}
	return tokens
}

// tokensWith returns head followed by the single-rune UNKNOWN tokens of rest,
// which starts right after head.
func tokensWith(head *bytescan.Token, rest string) []*bytescan.Token {
	return append([]*bytescan.Token{head}, unknownTokens(rest, head.Offset()+head.Len())...)
}

type lookaheadCase struct {
	name     string
	input    string
	expected []*bytescan.Token
}

// runLookaheadCases lexes each case's input with the rules that setup
// registers and compares type, text, and offset of every token.
func runLookaheadCases(t *testing.T, cases []lookaheadCase, setup func(lx *bytescan.Scanner)) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lx := bytescan.New(strings.NewReader(tc.input))
			setup(lx)
			assert.Equal(t, tokenKeys(tc.expected), tokenKeys(lexAll(t, lx)))
		})
	}
}

// TestLookaheadMultiSymbol: a rule reads four symbols past its match to decide,
// then excludes all four. "http" is a PROTOCOL only when "://" and a letter
// follow.
func TestLookaheadMultiSymbol(t *testing.T) {
	const lexTypeProtocol = bytescan.TokenType("PROTOCOL")

	// The rule continues through "http://" and one more symbol. If that symbol
	// is an ASCII letter, four pushbacks exclude it and "://", and the rule
	// accepts "http". It never accepts before it has decided.
	protocolRule := newHTTPBeforeLetterRule()

	runLookaheadCases(t, []lookaheadCase{
		{
			name:     "letter after scheme",
			input:    "http://example.com",
			expected: tokensWith(bytescan.NewTokenFromString(lexTypeProtocol, "http", 0), "://example.com"),
		},
		{
			name:     "non-letter after scheme",
			input:    "http://$",
			expected: unknownTokens("http://$", 0),
		},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeProtocol, protocolRule)
	})
}

// TestLookaheadVariableLength: the number of symbols a rule excludes depends on
// the input. "https" is a PROTOCOL only when "://wikipedia.org/wiki/" and a
// non-empty page name follow.
func TestLookaheadVariableLength(t *testing.T) {
	const lexTypeProtocol = bytescan.TokenType("PROTOCOL")

	// The rule scans the whole page name. On the first other symbol, or the
	// EOF symbol, it excludes that symbol, the page name, and the prefix, and
	// accepts "https".
	protocolRule := newWikipediaHTTPSRule()

	https := bytescan.NewTokenFromString(lexTypeProtocol, "https", 0)

	runLookaheadCases(t, []lookaheadCase{
		{
			name:     "valid URL at EOF",
			input:    "https://wikipedia.org/wiki/Lexical_analysis",
			expected: tokensWith(https, "://wikipedia.org/wiki/Lexical_analysis"),
		},
		{
			name:     "valid URL followed by a space",
			input:    "https://wikipedia.org/wiki/Go-2 next",
			expected: tokensWith(https, "://wikipedia.org/wiki/Go-2 next"),
		},
		{
			name:     "non-Wikipedia URL",
			input:    "https://example.org/wiki/Go",
			expected: unknownTokens("https://example.org/wiki/Go", 0),
		},
		{
			name:     "partial URL",
			input:    "https://wikipedia.org/wi",
			expected: unknownTokens("https://wikipedia.org/wi", 0),
		},
		{
			name:     "empty page name at EOF",
			input:    "https://wikipedia.org/wiki/",
			expected: unknownTokens("https://wikipedia.org/wiki/", 0),
		},
		{
			name:     "empty page name before a space",
			input:    "https://wikipedia.org/wiki/ x",
			expected: unknownTokens("https://wikipedia.org/wiki/ x", 0),
		},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeProtocol, protocolRule)
	})
}

// TestLookaheadTwoCandidateEnds: one rule chooses between two match ends after
// it has seen what follows. After "abcd" and any number of '.', 'X' yields "ab"
// and 'Y' yields "abcd".
func TestLookaheadTwoCandidateEnds(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// After "abcd" and n dots, 'X' excludes itself, the n dots, and "cd". 'Y'
	// excludes itself and the n dots. Any other symbol, or the EOF symbol,
	// rejects.
	rule := newTwoCandidateEndsRule()

	ab := bytescan.NewTokenFromString(lexTypeR, "ab", 0)
	abcd := bytescan.NewTokenFromString(lexTypeR, "abcd", 0)

	runLookaheadCases(t, []lookaheadCase{
		{name: "X right after abcd", input: "abcdX", expected: tokensWith(ab, "cdX")},
		{name: "X after dots", input: "abcd...X", expected: tokensWith(ab, "cd...X")},
		{name: "Y right after abcd", input: "abcdY", expected: tokensWith(abcd, "Y")},
		{name: "Y after dots", input: "abcd..Y", expected: tokensWith(abcd, "..Y")},
		{name: "other symbol", input: "abcd..Z", expected: unknownTokens("abcd..Z", 0)},
		{name: "end of input", input: "abcd", expected: unknownTokens("abcd", 0)},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, rule)
	})
}

// TestLookaheadKeywordBoundary: a keyword rule with no identifier rule beside
// it decides by one symbol of lookahead. "for" matches before a non-identifier
// symbol and at end of input, and not inside "format".
func TestLookaheadKeywordBoundary(t *testing.T) {
	const lexTypeFor = bytescan.TokenType("FOR")

	// After "for", an identifier character rejects. Any other symbol, the EOF
	// symbol included, is excluded by one pushback and "for" is accepted.
	forRule := newKeywordRule("for", isASCIIWord)

	keyword := bytescan.NewTokenFromString(lexTypeFor, "for", 0)

	runLookaheadCases(t, []lookaheadCase{
		{name: "before a parenthesis", input: "for (", expected: tokensWith(keyword, " (")},
		{name: "at end of input", input: "for", expected: []*bytescan.Token{keyword}},
		{name: "inside a longer word", input: "format", expected: unknownTokens("format", 0)},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeFor, forRule)
	})
}

// TestRuleAuthoringEOFPushbackExample is the worked example of practice 5 in
// docs/rule-authoring.md: newLettersAtEndRule waits for the EOF symbol,
// excludes it, and emits a non-empty token at end of input. A letter run that
// does not end the input does not match.
func TestRuleAuthoringEOFPushbackExample(t *testing.T) {
	const lexTypeWord = bytescan.TokenType("WORD")

	runLookaheadCases(t, []lookaheadCase{
		{
			name:     "letters that end the input",
			input:    "ab",
			expected: []*bytescan.Token{bytescan.NewTokenFromString(lexTypeWord, "ab", 0)},
		},
		{
			name:     "only the last run ends the input",
			input:    "ab cd",
			expected: append(unknownTokens("ab ", 0), bytescan.NewTokenFromString(lexTypeWord, "cd", 3)),
		},
		{name: "letters before a space", input: "ab ", expected: unknownTokens("ab ", 0)},
		{name: "no letter", input: "1", expected: unknownTokens("1", 0)},
		{name: "empty input", input: "", expected: nil},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeWord, newLettersAtEndRule())
	})
}

// properPrefixes returns every non-empty proper prefix of s, rune by rune.
func properPrefixes(s string) []string {
	runes := []rune(s)
	var prefixes []string
	for i := 1; i < len(runes); i++ {
		prefixes = append(prefixes, string(runes[:i]))
	}
	return prefixes
}

// TestLookaheadRulesAtEndOfInput: practice 9 of docs/rule-authoring.md. The
// input ends in each scanning state of the lookahead rules whose own tests do
// not reach that state at end of input. Each case compares the complete
// token stream.
func TestLookaheadRulesAtEndOfInput(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// In these states the lookahead is not complete at end of input, so the
	// rule matches nothing and every rune is UNKNOWN.
	for _, tc := range []struct {
		name   string
		rule   func() bytescan.Rule
		inputs []string
	}{
		{"http(?=://[A-Za-z])", newHTTPBeforeLetterRule, append(properPrefixes("http://"), "http://")},
		{"https(?=://wikipedia...)", newWikipediaHTTPSRule, append(properPrefixes("https"+wikipediaPrefix), "https"+wikipediaPrefix)},
		{"a(?=bc)", newABeforeBCRule, properPrefixes("abc")},
		{"ab(?=cd\\.*X)|abcd(?=\\.*Y)", newTwoCandidateEndsRule, append(properPrefixes("abcd"), "abcd..")},
		{"a(?=X)|b(?=Y)|c(?=Z)", newRuneBeforeMarkerRule, []string{"a", "b", "c"}},
		{"abcd(?=ef)", newABCDBeforeEFRule, append(properPrefixes("abcde"), "abcde")},
		{"for(?!\\w)", func() bytescan.Rule { return newKeywordRule("for", isASCIIWord) }, properPrefixes("for")},
		{"\\Afoo", newStartOfInputFooRule, properPrefixes("foo")},
		{"abcd(?=ef)|abcdefgh", newLookaheadOrLongerRule, append(properPrefixes("abcde"), "abcde")},
		{"ab(?=c)|ab.*z", newUnboundedLongerRule, []string{"a", "ab", "abx"}},
		{"ab|abcd(?=ef)", newGrowingAcceptRule, []string{"a"}},
	} {
		for _, input := range tc.inputs {
			t.Run(tc.name+"/"+input, func(t *testing.T) {
				lx := bytescan.New(strings.NewReader(input))
				lx.MustAddRule(lexTypeR, tc.rule())
				assert.Equal(t, tokenKeys(unknownTokens(input, 0)), tokenKeys(lexAll(t, lx)))
			})
		}
	}

	// In these states the rule has a match at end of input.
	ab := bytescan.NewTokenFromString(lexTypeR, "ab", 0)
	t.Run("ab|abcd(?=ef)", func(t *testing.T) {
		runLookaheadCases(t, []lookaheadCase{
			{name: "abc", input: "abc", expected: tokensWith(ab, "c")},
			{name: "abcd", input: "abcd", expected: tokensWith(ab, "cd")},
			{name: "abcde", input: "abcde", expected: tokensWith(ab, "cde")},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newGrowingAcceptRule())
		})
	})
	t.Run("ab(?=c)|ab.*z", func(t *testing.T) {
		runLookaheadCases(t, []lookaheadCase{
			{name: "abc", input: "abc", expected: tokensWith(ab, "c")},
			{name: "abcx", input: "abcx", expected: tokensWith(ab, "cx")},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newUnboundedLongerRule())
		})
	})
}

// TestLookaheadFloatVersusRange: newUnsignedFloatRule decides by lookahead
// whether "1." is a float. A second '.' makes it reject, so the integer rule
// wins "1" in a range such as "1..5".
func TestLookaheadFloatVersusRange(t *testing.T) {
	const (
		lexTypeInteger = bytescan.TokenType("INT")
		lexTypeFloat   = bytescan.TokenType("FLOAT")
	)

	cases := []lookaheadCase{
		{
			name:  "trailing radix before a space",
			input: "1. ",
			expected: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeFloat, "1.", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, " ", 2),
			},
		},
		{
			name:  "range",
			input: "1..5",
			expected: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeInteger, "1", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, ".", 1),
				bytescan.NewTokenFromString(lexTypeFloat, ".5", 2),
			},
		},
		{
			name:  "float",
			input: "1.5",
			expected: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeFloat, "1.5", 0),
			},
		},
	}

	t.Run("float registered first", func(t *testing.T) {
		runLookaheadCases(t, cases, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeFloat, newUnsignedFloatRule())
			lx.MustAddRule(lexTypeInteger, newUnsignedIntegerRule())
		})
	})
	t.Run("integer registered first", func(t *testing.T) {
		runLookaheadCases(t, cases, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeInteger, newUnsignedIntegerRule())
			lx.MustAddRule(lexTypeFloat, newUnsignedFloatRule())
		})
	})
}

// TestLookaheadDelayedAcceptance: one rule expresses a lookahead branch next to
// a longer branch, abcd(?=ef)|abcdefgh. The rule delays its accept until the
// longer branch is dead and then falls back to the validated "abcd" with one
// terminal pushback chain. See "Lookahead combined with a longer match" in
// docs/specs/rule-vm-contract.md.
func TestLookaheadDelayedAcceptance(t *testing.T) {
	const lexTypeMatch = bytescan.TokenType("MATCH")

	abcd := bytescan.NewTokenFromString(lexTypeMatch, "abcd", 0)

	runLookaheadCases(t, []lookaheadCase{
		{
			name:     "longer branch completes",
			input:    "abcdefgh",
			expected: []*bytescan.Token{bytescan.NewTokenFromString(lexTypeMatch, "abcdefgh", 0)},
		},
		{name: "longer branch fails at g", input: "abcdefxx", expected: tokensWith(abcd, "efxx")},
		{name: "end of input after the lookahead", input: "abcdef", expected: tokensWith(abcd, "ef")},
		{name: "end of input inside the longer branch", input: "abcdefg", expected: tokensWith(abcd, "efg")},
		{name: "longer branch fails at h", input: "abcdefgX", expected: tokensWith(abcd, "efgX")},
		{name: "end of input inside the lookahead", input: "abcde", expected: unknownTokens("abcde", 0)},
		{name: "lookahead fails", input: "abcdX", expected: unknownTokens("abcdX", 0)},
		{
			name:  "two tokens",
			input: "abcdefghabcdef",
			expected: append(
				[]*bytescan.Token{bytescan.NewTokenFromString(lexTypeMatch, "abcdefgh", 0)},
				tokensWith(bytescan.NewTokenFromString(lexTypeMatch, "abcd", 8), "ef")...,
			),
		},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeMatch, newLookaheadOrLongerRule())
	})
}

// TestLookaheadUnboundedReadAheadHitsByteBound: a longer branch that can stay
// alive without bound reads ahead to the end of input before the token
// settles, and WithMaxTokenBytes counts that read-ahead. Under ab(?=c)|ab.*z,
// the final match is "ab", but the rule reads the whole input to decide. See
// "Speculative lookahead limits" in docs/specs/resources-and-streams.md.
func TestLookaheadUnboundedReadAheadHitsByteBound(t *testing.T) {
	const (
		lexTypeMatch = bytescan.TokenType("MATCH")
		maxBytes     = 64
	)

	ab := bytescan.NewTokenFromString(lexTypeMatch, "ab", 0)
	fits := "abc" + strings.Repeat("x", maxBytes-3) // 64 bytes
	exceeds := fits + "x"                           // 65 bytes
	require.Len(t, fits, maxBytes)
	require.Len(t, exceeds, maxBytes+1)

	t.Run("over the bound", func(t *testing.T) {
		lx := bytescan.NewWithOptions(strings.NewReader(exceeds), bytescan.WithMaxTokenBytes(maxBytes))
		lx.MustAddRule(lexTypeMatch, newUnboundedLongerRule())

		rejected := func() {
			lex, err := lx.Next()
			require.Error(t, err)
			assert.Nil(t, lex)
			assert.ErrorIs(t, err, textreader.ErrRetentionExceeded)
		}

		// A failed Next consumes nothing, so the retry fails the same way. Had
		// the first call consumed "ab", the 63 bytes left would fit the bound.
		rejected()
		rejected()
	})

	t.Run("within the bound", func(t *testing.T) {
		lx := bytescan.NewWithOptions(strings.NewReader(fits), bytescan.WithMaxTokenBytes(maxBytes))
		lx.MustAddRule(lexTypeMatch, newUnboundedLongerRule())
		assert.Equal(t, tokenKeys(tokensWith(ab, fits[2:])), tokenKeys(lexAll(t, lx)))
	})

	runLookaheadCases(t, []lookaheadCase{
		{name: "default scanner, same input", input: exceeds, expected: tokensWith(ab, exceeds[2:])},
		{name: "longer branch completes", input: "abcxz", expected: []*bytescan.Token{bytescan.NewTokenFromString(lexTypeMatch, "abcxz", 0)}},
		{name: "newline ends the longer branch", input: "abc\nz", expected: tokensWith(ab, "c\nz")},
		{name: "lookahead fails", input: "abxx", expected: unknownTokens("abxx", 0)},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeMatch, newUnboundedLongerRule())
	})
}
