package tests

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// TestLiteralAlternationPrefixAlternative: an alternative that is a prefix of
// a longer one ends its match at its own last rune.
// newLiteralAlternationRule("a", "ab") on "ax" matches "a", not "ax".
func TestLiteralAlternationPrefixAlternative(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	cases := []struct {
		alternatives []string
		input        string
		match        string
	}{
		{alternatives: []string{"a", "ab"}, input: "ax", match: "a"},
		{alternatives: []string{"a", "ab"}, input: "ab", match: "ab"},
		{alternatives: []string{"ab", "a"}, input: "ax", match: "a"},
		{alternatives: []string{"if", "ifelse", "for"}, input: "ifels", match: "if"},
		{alternatives: []string{"if", "ifelse", "for"}, input: "ifelsex", match: "ifelse"},
		{alternatives: []string{"é", "éa"}, input: "éx", match: "é"},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.alternatives, "|")+"/"+tc.input, func(t *testing.T) {
			lx := bytescan.New(strings.NewReader(tc.input))
			lx.MustAddRule(lexTypeR, newLiteralAlternationRule(tc.alternatives...))

			lex, err := lx.Next()
			require.NoError(t, err)
			assert.Equal(t, lexTypeR, lex.Type())
			assert.Equal(t, tc.match, lex.Text())
		})
	}
}

// Rules for the regular expressions in this file. They follow
// docs/rule-authoring.md, as the shared rule helpers in scanner_rules_test.go do;
// newRunePlusRule, newLiteralThenRule and the generic predicates live there.

// Rune predicates. The names say whether a class is ASCII-only or Unicode.

// isQuote reports whether r is in ['"].
func isQuote(r rune) bool { return r == '\'' || r == '"' }

// newStartOfInputFooRule returns a rule for \Afoo. The 'f' must be the first
// symbol of the input. It accepts at the last 'o' and stops. At end of input
// before that it rejects the EOF symbol, so it matches nothing.
func newStartOfInputFooRule() bytescan.Rule {
	var start, afterF, afterFO bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.IsBOF() && s.Rune() == 'f' {
			return afterF, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterF = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'o' {
			return afterFO, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterFO = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'o' {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newBackreferenceQuoteRule returns a rule for (['"]).*?\1: the closing quote
// must equal the opening quote. The start state passes the opening quote to
// quotedBody as a parameter, so it lives in the returned Rule values. The
// first equal quote ends the match, as the lazy .*? does, and . excludes a
// newline. At end of input before the closing quote it rejects the EOF symbol,
// so it matches nothing.
func newBackreferenceQuoteRule() bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isQuote(s.Rune()) {
			return quotedBody(s.Rune()), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// quotedBody is the state of (['"]).*?\1 after the opening quote open.
func quotedBody(open rune) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		switch r := s.Rune(); {
		case r == open:
			return nil, bytescan.StateAccept
		case isNotNewline(r):
			return quotedBody(open), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// excludeEFOnF is the lookahead state of (?=ef) after the 'e': an 'f' confirms
// the lookahead. excludeLast(2) excludes the 'f' just read and the 'e' before
// it, and accepts the match that ends before "ef".
func excludeEFOnF(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.IsEOF() {
		return nil, bytescan.StateReject
	}
	if s.Rune() == 'f' {
		return excludeLast(2, bytescan.StateAccept)
	}
	return nil, bytescan.StateReject
}

// newGrowingAcceptRule returns a rule for ab|abcd(?=ef). It accepts "ab" and
// keeps reading; after "abcde" an 'f' grows the accept to "abcd". Any other
// symbol rejects, and the earlier accept of "ab" stays. The eager accept of
// "ab" is safe under practice 5 of docs/rule-authoring.md: the pending
// lookahead can only grow the match. At end of input it rejects the EOF
// symbol, so after "ab" the match is "ab".
func newGrowingAcceptRule() bytescan.Rule {
	var start, afterA, afterAB bytescan.Rule
	afterAB = newLiteralThenRule("cde", excludeEFOnF)
	afterA = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'b' {
			return afterAB, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	start = newLiteralThenRule("a", afterA)
	return start
}

// newABCDBeforeEFRule returns a rule for abcd(?=ef). It never accepts before
// the lookahead has decided. At end of input it rejects the EOF symbol, so it
// matches nothing.
func newABCDBeforeEFRule() bytescan.Rule {
	return newLiteralThenRule("abcde", excludeEFOnF)
}

// regexpRow is one row of TestRegexpConformance.
type regexpRow struct {
	expr  string
	rule  bytescan.Rule
	input []string

	// matchesEveryInput exempts a row from the non-matching input and from
	// the match that stops before the end of input: its pattern matches every
	// non-empty input completely.
	matchesEveryInput bool
}

// TestRegexpConformance: a hand-written rule returns the same first match as
// Go's regexp for \A(?:expr) under leftmost-longest semantics. No match on a
// non-empty input is a first token of type UNKNOWN. The empty input gives
// io.EOF and no token. Each row must include a matching input and, unless it
// is exempt, a non-matching input and a match that stops before the end.
func TestRegexpConformance(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// The rows of the rules/core primitives (literals, classes, runs and
	// counted repetition) are in TestCoreMatchesRegexpOracle, and the
	// composite rows (escaped strings, (?:ab)+, (?:a|bc)+) are rows of
	// composed rules in TestCoreCombinatorsMatchRegexpOracle. The rows here
	// pin the hand-written rules of this package.
	rows := []regexpRow{
		{
			expr:  `.+`,
			rule:  newRunePlusRule(isNotNewline),
			input: []string{"a", "ab\ncd", "\nab", "é x\r\n", "\r"},
		},
		{
			expr:              `(?s).+`,
			rule:              newRunePlusRule(isAnyRune),
			input:             []string{"a", "a\nb", "\n", "\r\n", "é\x00ß"},
			matchesEveryInput: true,
		},
		{
			expr:  `\w+`,
			rule:  newRunePlusRule(isASCIIWord),
			input: []string{"a_1", "a_1-b", "abcé", "-a", "é"},
		},
		{
			expr:  `a|ab`,
			rule:  newLiteralAlternationRule("a", "ab"),
			input: []string{"a", "ab", "ax", "abx", "b"},
		},
		{
			expr:  `if|ifelse|for`,
			rule:  newLiteralAlternationRule("if", "ifelse", "for"),
			input: []string{"if", "ifelse", "for", "ifels", "if(", "forx", "f", "fo", "i", "ife", "ifel"},
		},

		// The shared rules of scanner_rules_test.go and cursor_test.go.
		{
			expr:  `select`,
			rule:  newLiteralRule("select"),
			input: []string{"select", "selectx", "s", "se", "sel", "sele", "selec", "SELECT", "xselect"},
		},
		{
			expr:  `[ \t\n\r]+`,
			rule:  newWhitespaceRule(),
			input: []string{" ", " \t\r\n", " \tx", "\f", "x "},
		},
		{
			expr:  `[0-9]+`,
			rule:  newUnsignedIntegerRule(),
			input: []string{"7", "123", "12a", "a1", "٣"},
		},
		{
			expr:  `[A-Za-z]+`,
			rule:  newASCIILettersRule(),
			input: []string{"a", "abZ", "ab1", "1a", "é"},
		},
		{
			expr:  `\pL+`,
			rule:  newUnicodeLettersRule(),
			input: []string{"a", "héllo", "世界 x", "1a"},
		},
		{
			expr:  `[^\x00-\x7F]+`,
			rule:  newNonASCIIRunesRule(),
			input: []string{"é", "世界", "世a", "a世"},
		},
		{
			expr:  ` `,
			rule:  newSpaceRule(),
			input: []string{" ", "  ", "x"},
		},
		{
			expr:  `[A-Za-z_][A-Za-z0-9_]*`,
			rule:  newIdentifierRule(),
			input: []string{"a", "_", "a_1", "a-b", "1a", "_é"},
		},
		{
			expr:  `[+-]?[0-9]+`,
			rule:  newSignedIntegerRule(),
			input: []string{"1", "-12", "+12x", "+", "-x", "x"},
		},
		{
			expr:  `[-+*/=;()]`,
			rule:  newSymbolRule(),
			input: []string{"+", "()", ";x", "a"},
		},
		{
			expr:  `'[^']*'`,
			rule:  newSingleQuotedStringRule(),
			input: []string{"''", "'a'", "'a\nb' x", "'", "'abc", "x'a'"},
		},
		{
			expr: `(?s)/\*(?:[^*]|\*[^/])*\*/`,
			rule: newSlashStarCommentRule(),
			input: []string{
				"/**/", "/* a */", "/* a */ b */", "/* a\n*/", "/* ** */",
				"/", "/*", "/* a *", "/***/", "/x", "x",
			},
		},
		{
			// Without "..": the rule rejects "1." before a second radix point,
			// which regexp cannot express. TestLookaheadFloatVersusRange checks
			// that case.
			expr:  `[0-9]+\.[0-9]*|\.[0-9]+`,
			rule:  newUnsignedFloatRule(),
			input: []string{"1.5", "1.", "12.x", "1.2.3", ".5", ".x", ".", "1", "x"},
		},
		{
			expr:  `#[^\n]*`,
			rule:  newBOLCommentRule(),
			input: []string{"#", "# a", "# a\nb", "x#", "\n#"},
		},
		{
			expr:  `(?s)a`,
			rule:  newABeforeAnySymbolRule(),
			input: []string{"a", "ab", "a\n", "b"},
		},
		{
			expr:  `a(?:aa)*`,
			rule:  newOddARunRule(),
			input: []string{"a", "aa", "aaa", "aaab", "b"},
		},
		{
			expr:  `(?:aa)+`,
			rule:  newEvenARunRule(),
			input: []string{"a", "aa", "aaa", "aaaab", "b"},
		},
		{
			expr:  `(?:abc)*(?:abc|ab|a)`,
			rule:  newABCCycleRule(),
			input: []string{"a", "ab", "abc", "abca", "abcab", "abx", "b"},
		},
		{
			expr:  `a{1,3}`,
			rule:  newARunUpToRule(3),
			input: []string{"a", "aa", "aaa", "aaaa", "b"},
		},
		{
			expr:  `a{3}`,
			rule:  newARunOfLengthRule(3),
			input: []string{"a", "aa", "aaa", "aaaa", "b"},
		},
		{
			expr:  `[a-z]+`,
			rule:  newRunePlusRule(isASCIILowercase),
			input: []string{"test", "teSt", "T"},
		},
		{
			expr:  `[ab]+`,
			rule:  newRunePlusRule(isAOrB),
			input: []string{"ab", "abc", "c"},
		},
	}

	for _, row := range rows {
		t.Run(row.expr, func(t *testing.T) {
			re := regexp.MustCompile(`\A(?:` + row.expr + `)`)
			re.Longest()

			var matched, unmatched, stoppedEarly bool
			for _, input := range row.input {
				want := re.FindString(input)
				switch {
				case want == "":
					unmatched = true
				case len(want) < len(input):
					matched, stoppedEarly = true, true
				default:
					matched = true
				}

				t.Run(input, func(t *testing.T) {
					require.NotEmpty(t, input, "the empty input is checked separately")

					lx := bytescan.New(strings.NewReader(input))
					lx.MustAddRule(lexTypeR, row.rule)

					lex, err := lx.Next()
					require.NoError(t, err)
					if want == "" {
						assert.Equal(t, bytescan.TokenTypeUnknown, lex.Type())
						return
					}
					assert.Equal(t, lexTypeR, lex.Type())
					assert.Equal(t, want, lex.Text())
				})
			}

			t.Run("empty input", func(t *testing.T) {
				lx := bytescan.New(strings.NewReader(""))
				lx.MustAddRule(lexTypeR, row.rule)

				lex, err := lx.Next()
				assert.ErrorIs(t, err, io.EOF, "Next on empty input: %v", err)
				assert.Nil(t, lex)
			})

			assert.True(t, matched, "row has no matching input")
			if row.matchesEveryInput {
				assert.True(t, strings.ContainsRune(strings.Join(row.input, ""), '\n'), "row has no input with a newline")
				return
			}
			assert.True(t, unmatched, "row has no non-matching input")
			assert.True(t, stoppedEarly, "row has no match that stops before the end of input")
		})
	}
}

// TestRegexpWithoutOracle: constructs that Go's regexp cannot check, compared
// with hand-written token streams.
func TestRegexpWithoutOracle(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	t.Run(`\Afoo`, func(t *testing.T) {
		runLookaheadCases(t, []lookaheadCase{
			{
				name:     "foofoo",
				input:    "foofoo",
				expected: tokensWith(bytescan.NewTokenFromString(lexTypeR, "foo", 0), "foo"),
			},
			{name: "xfoo", input: "xfoo", expected: unknownTokens("xfoo", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newStartOfInputFooRule())
		})
	})

	t.Run(`(['"]).*?\1`, func(t *testing.T) {
		runLookaheadCases(t, []lookaheadCase{
			{
				name:  "each quote closes its own kind",
				input: `"a'b" 'c"d'`,
				expected: []*bytescan.Token{
					bytescan.NewTokenFromString(lexTypeR, `"a'b"`, 0),
					bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, " ", 5),
					bytescan.NewTokenFromString(lexTypeR, `'c"d'`, 6),
				},
			},
			{
				name:  "first equal quote ends the match",
				input: `'a''b'`,
				expected: []*bytescan.Token{
					bytescan.NewTokenFromString(lexTypeR, `'a'`, 0),
					bytescan.NewTokenFromString(lexTypeR, `'b'`, 3),
				},
			},
			{name: "no equal closing quote", input: `"abc'`, expected: unknownTokens(`"abc'`, 0)},
			{name: "dot does not match a newline", input: "'a\nb'", expected: unknownTokens("'a\nb'", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newBackreferenceQuoteRule())
		})
	})

	t.Run(`ab|abcd(?=ef)`, func(t *testing.T) {
		runLookaheadCases(t, []lookaheadCase{
			{
				name:     "abcdef",
				input:    "abcdef",
				expected: tokensWith(bytescan.NewTokenFromString(lexTypeR, "abcd", 0), "ef"),
			},
			{
				name:     "abcdxx",
				input:    "abcdxx",
				expected: tokensWith(bytescan.NewTokenFromString(lexTypeR, "ab", 0), "cdxx"),
			},
			{
				name:     "ab",
				input:    "ab",
				expected: []*bytescan.Token{bytescan.NewTokenFromString(lexTypeR, "ab", 0)},
			},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newGrowingAcceptRule())
		})
	})
}

// TestRegexpScannerLevelPatterns: constructs shown with a pattern of several
// rules that together give the token stream of the regular expression.
func TestRegexpScannerLevelPatterns(t *testing.T) {
	// abcd(?=ef)|abcdefgh, the two-rule form: one rule matches "abcd" when
	// "ef" follows, and a second rule matches "abcdefgh". Longest match picks
	// the second rule when the input has "gh" after "abcdef".
	t.Run(`abcd(?=ef)|abcdefgh`, func(t *testing.T) {
		const (
			lexTypeLookahead = bytescan.TokenType("LOOKAHEAD")
			lexTypeLong      = bytescan.TokenType("LONG")
		)

		cases := []lookaheadCase{
			{
				name:     "abcdefgh",
				input:    "abcdefgh",
				expected: []*bytescan.Token{bytescan.NewTokenFromString(lexTypeLong, "abcdefgh", 0)},
			},
			{
				name:     "abcdefxx",
				input:    "abcdefxx",
				expected: tokensWith(bytescan.NewTokenFromString(lexTypeLookahead, "abcd", 0), "efxx"),
			},
			{name: "abcdxx", input: "abcdxx", expected: unknownTokens("abcdxx", 0)},
		}

		t.Run("lookahead rule registered first", func(t *testing.T) {
			runLookaheadCases(t, cases, func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeLookahead, newABCDBeforeEFRule())
				lx.MustAddRule(lexTypeLong, newLiteralRule("abcdefgh"))
			})
		})
		t.Run("long rule registered first", func(t *testing.T) {
			runLookaheadCases(t, cases, func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeLong, newLiteralRule("abcdefgh"))
				lx.MustAddRule(lexTypeLookahead, newABCDBeforeEFRule())
			})
		})
	})

	// \bfor\b: a keyword rule for "for" registered before a rule for a run of
	// ASCII word runes, \w+. A word boundary is a change between \w and not
	// \w, so the competitor must consume every \w run, including one that
	// starts with a digit. On "for" both rules match three runes, and
	// registration order gives the keyword. Where a \w rune precedes or
	// follows "for", the word-run rule matches a longer token and wins. Go's
	// regexp checks the keyword offsets that the scanner produces.
	t.Run(`\bfor\b`, func(t *testing.T) {
		const (
			lexTypeKeyword = bytescan.TokenType("KEYWORD")
			lexTypeWordRun = bytescan.TokenType("WORD_RUN")
		)

		keyword := func(offset int) *bytescan.Token {
			return bytescan.NewTokenFromString(lexTypeKeyword, "for", offset)
		}
		wordRun := func(text string, offset int) *bytescan.Token {
			return bytescan.NewTokenFromString(lexTypeWordRun, text, offset)
		}
		unknown := func(text string, offset int) *bytescan.Token {
			return bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, text, offset)
		}

		cases := []lookaheadCase{
			{name: "for", input: "for", expected: []*bytescan.Token{keyword(0)}},
			{name: "for(", input: "for(", expected: []*bytescan.Token{keyword(0), unknown("(", 3)}},
			{name: " for ", input: " for ", expected: []*bytescan.Token{unknown(" ", 0), keyword(1), unknown(" ", 4)}},
			{name: "xfor", input: "xfor", expected: []*bytescan.Token{wordRun("xfor", 0)}},
			{name: "format", input: "format", expected: []*bytescan.Token{wordRun("format", 0)}},
			{name: "for_1", input: "for_1", expected: []*bytescan.Token{wordRun("for_1", 0)}},
			{name: "1for", input: "1for", expected: []*bytescan.Token{wordRun("1for", 0)}},
			{name: "99for", input: "99for", expected: []*bytescan.Token{wordRun("99for", 0)}},
			{name: "1for(", input: "1for(", expected: []*bytescan.Token{wordRun("1for", 0), unknown("(", 4)}},
		}

		setup := func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, newLiteralRule("for"))
			lx.MustAddRule(lexTypeWordRun, newRunePlusRule(isASCIIWord))
		}

		runLookaheadCases(t, cases, setup)

		// The inputs are ASCII, so byte offsets equal rune offsets.
		re := regexp.MustCompile(`\bfor\b`)
		for _, tc := range cases {
			t.Run(tc.name+" against regexp", func(t *testing.T) {
				var want []int
				for _, loc := range re.FindAllStringIndex(tc.input, -1) {
					want = append(want, loc[0])
				}

				lx := bytescan.New(strings.NewReader(tc.input))
				setup(lx)
				var got []int
				for _, lex := range lexAll(t, lx) {
					if lex.Type() == lexTypeKeyword {
						got = append(got, lex.Offset())
					}
				}
				assert.Equal(t, want, got, "keyword offsets in %q", tc.input)
			})
		}
	})
}
