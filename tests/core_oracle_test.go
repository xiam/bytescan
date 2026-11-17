package tests

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check the exported rules and classes of rules/core
// against Go's regexp package, used only as a test oracle for the expression
// that each doc comment documents. They live here so that no file under
// rules/ imports regexp. isAnyRune is defined in scanner_rules_test.go.

// coreOracleRow is one row of TestCoreMatchesRegexpOracle: a rule and the regular
// expression that its doc comment documents.
type coreOracleRow struct {
	expr  string
	rule  bytescan.Rule
	input []string

	// matchesEveryInput exempts a row from the non-matching input and from
	// the match that stops before the end of input: its pattern matches every
	// non-empty input completely.
	matchesEveryInput bool
}

// TestCoreMatchesRegexpOracle: each rules/core primitive returns the same
// first match as the expression its doc comment documents. Go's regexp
// package is only the test oracle for that expression: \A(?:expr) under
// leftmost-longest semantics. rules/core itself does not import regexp. No match on a
// non-empty input is a first token of type UNKNOWN. The empty input gives
// io.EOF and no token. Each row must include a matching input and, unless it
// is exempt, a non-matching input and a match that stops before the end. The
// inputs of each row also end the input in each scanning state of the rule.
func TestCoreMatchesRegexpOracle(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	rows := []coreOracleRow{
		// Literal: one scanning state per rune.
		{
			expr:  `select`,
			rule:  core.Literal("select"),
			input: []string{"select", "selectx", "s", "se", "sel", "sele", "selec", "SELECT", "xselect"},
		},
		{
			expr:  `héllo`,
			rule:  core.Literal("héllo"),
			input: []string{"héllo", "héllo!", "h", "hé", "hél", "héll", "hello"},
		},
		{
			expr:  `a\.b`,
			rule:  core.Literal("a.b"),
			input: []string{"a.b", "a.bc", "a", "a.", "axb"},
		},
		{
			expr:  ` `,
			rule:  core.Literal(" "),
			input: []string{" ", "  ", "x"},
		},

		// LiteralIgnoreCase.
		{
			expr:  `(?i)select`,
			rule:  core.LiteralIgnoreCase("select"),
			input: []string{"select", "SeLeCt", "ſELECT", "SELECTx", "s", "se", "sel", "sele", "selec", "xselect"},
		},
		{
			expr:  `(?i)k`,
			rule:  core.LiteralIgnoreCase("k"),
			input: []string{"k", "K", "K", "kk", "x"},
		},
		{
			expr:  `(?i)σς`,
			rule:  core.LiteralIgnoreCase("σς"),
			input: []string{"σς", "ΣΣ", "ςσx", "Σ", "σx"},
		},

		// AnyLiteral.
		{
			expr:  `a|ab`,
			rule:  core.AnyLiteral("a", "ab"),
			input: []string{"a", "ab", "ax", "abx", "b"},
		},
		{
			expr:  `ab|a`,
			rule:  core.AnyLiteral("ab", "a"),
			input: []string{"a", "ab", "ax", "abx", "b"},
		},
		{
			expr:  `if|ifelse|for`,
			rule:  core.AnyLiteral("if", "ifelse", "for"),
			input: []string{"if", "ifelse", "for", "ifels", "ifelsex", "if(", "forx", "f", "fo", "i", "ife", "ifel"},
		},
		{
			expr:  `é|éa`,
			rule:  core.AnyLiteral("é", "éa"),
			input: []string{"é", "éa", "éx", "e"},
		},

		// AnyLiteralIgnoreCase: the longest literal under simple case folding.
		{
			expr:  `(?i)if|ifelse|for`,
			rule:  core.AnyLiteralIgnoreCase("if", "IFELSE", "For"),
			input: []string{"if", "IfElse", "FOR", "ifels", "IFELSEx", "if(", "forx", "f", "fo", "I", "ife", "ifel", "x"},
		},
		{
			expr:  `(?i)sk|s`,
			rule:  core.AnyLiteralIgnoreCase("sk", "s"),
			input: []string{"s", "S", "ſ", "sK", "ſ\u212a", "sk", "sx", "x"},
		},
		{
			expr:  `(?i)σς`,
			rule:  core.AnyLiteralIgnoreCase("σς", "ΣΣ"),
			input: []string{"σς", "ΣΣ", "ςσx", "Σ", "σx"},
		},

		// Rune: one scanning state.
		{
			expr:  `[-+*/=;()]`,
			rule:  core.Rune(core.RuneSet("-+*/=;()")),
			input: []string{"+", "()", ";x", "a"},
		},
		{
			expr:  `\pL`,
			rule:  core.Rune(core.UnicodeLetter),
			input: []string{"é", "ab", "1"},
		},

		// RuneRun: one scanning state, entered again after each rune.
		{
			expr:  `.+`,
			rule:  core.RuneRun(core.RuneSet("\n").Not()),
			input: []string{"a", "ab\ncd", "\nab", "é x\r\n", "\r"},
		},
		{
			expr:              `(?s).+`,
			rule:              core.RuneRun(isAnyRune),
			input:             []string{"a", "a\nb", "\n", "\r\n", "é\x00ß"},
			matchesEveryInput: true,
		},
		{
			expr:  `[0-9]+`,
			rule:  core.RuneRun(core.ASCIIDigit),
			input: []string{"7", "123", "12a", "a1", "٣"},
		},
		{
			expr:  `[0-9A-Fa-f]+`,
			rule:  core.RuneRun(core.ASCIIHexDigit),
			input: []string{"09afAF", "deadbeefg", "G", "٣"},
		},
		{
			expr:  `[A-Za-z]+`,
			rule:  core.RuneRun(core.ASCIILetter),
			input: []string{"a", "abZ", "ab1", "1a", "é"},
		},
		{
			expr:  `[a-z]+`,
			rule:  core.RuneRun(core.ASCIILower),
			input: []string{"test", "teSt", "T"},
		},
		{
			expr:  `[A-Z]+`,
			rule:  core.RuneRun(core.ASCIIUpper),
			input: []string{"TEST", "TEsT", "t"},
		},
		{
			expr:  `\w+`,
			rule:  core.RuneRun(core.ASCIIWord),
			input: []string{"a_1", "a_1-b", "abcé", "-a", "é"},
		},
		{
			expr:  `\s+`,
			rule:  core.RuneRun(core.ASCIISpace),
			input: []string{" ", " \t\r\n\fx", "\n\n", "\v", "x "},
		},
		{
			expr:  `[ \t\n\r]+`,
			rule:  core.RuneRun(core.RuneSet(" \t\n\r")),
			input: []string{" ", " \t\r\n", " \tx", "\f", "x "},
		},
		{
			expr:  `[ab]+`,
			rule:  core.RuneRun(core.RuneSet("ab")),
			input: []string{"ab", "abc", "c"},
		},
		{
			expr:  `\pL+`,
			rule:  core.RuneRun(core.UnicodeLetter),
			input: []string{"a", "héllo", "世界 x", "1a"},
		},
		{
			expr:  `[^\pL]+`,
			rule:  core.RuneRun(core.UnicodeLetter.Not()),
			input: []string{"12 é", "1\n2", "-_ ", "é", "a1"},
		},
		{
			expr:  `\pM+`,
			rule:  core.RuneRun(core.UnicodeMark),
			input: []string{"́", "́̂e", "é"},
		},
		{
			expr:  `\p{N}+`,
			rule:  core.RuneRun(core.UnicodeNumber),
			input: []string{"42", "٣४5a", "Ⅷx", "½", "a1"},
		},
		{
			expr:  `\pP+`,
			rule:  core.RuneRun(core.UnicodePunct),
			input: []string{"!?", "«»a", "¿", "+", "a!"},
		},
		{
			expr:  `\pS+`,
			rule:  core.RuneRun(core.UnicodeSymbol),
			input: []string{"+", "$€x", "©", "!", "a+"},
		},
		{
			expr:  `\pZ+`,
			rule:  core.RuneRun(core.UnicodeSeparator),
			input: []string{" ", "  x", "\t", "x "},
		},
		{
			expr:  `\p{Greek}+`,
			rule:  core.RuneRun(core.UnicodeScript("Greek")),
			input: []string{"αβγ", "αβγd", "Ωmega", "dα", "a"},
		},
		{
			expr:  `\p{Lu}+`,
			rule:  core.RuneRun(core.UnicodeTables(unicode.Lu)),
			input: []string{"AÉ", "ABc", "a"},
		},
		{
			expr:  `[\p{Greek}\p{Cyrillic}]+`,
			rule:  core.RuneRun(core.UnicodeTables(unicode.Greek, unicode.Cyrillic)),
			input: []string{"αж", "αжa", "a"},
		},
		{
			expr:  `[^\x00-\x7F]+`,
			rule:  core.RuneRun(core.RuneRange(0, 0x7f).Not()),
			input: []string{"é", "世界", "世a", "a世"},
		},
		{
			expr:  `[0-9a-z]+`,
			rule:  core.RuneRun(core.ASCIIDigit.Or(core.ASCIILower)),
			input: []string{"a1", "a1B", "B"},
		},
		{
			// ASCIILower.And(not a vowel): the consonants of [a-z].
			expr:  `[b-df-hj-np-tv-z]+`,
			rule:  core.RuneRun(core.ASCIILower.And(core.RuneSet("aeiou").Not())),
			input: []string{"bcd", "bca", "a", "B"},
		},

		// Repeat: one scanning state per count.
		{
			expr:  `[0-9]{2,4}`,
			rule:  core.Repeat(core.Rune(core.ASCIIDigit), 2, 4),
			input: []string{"1", "12", "123", "1234", "12345", "12a", "1a", "a12"},
		},
		{
			expr:  `a{1,3}`,
			rule:  core.Repeat(core.Rune(core.RuneSet("a")), 1, 3),
			input: []string{"a", "aa", "aaa", "aaaa", "b"},
		},
		{
			expr:  `a{3}`,
			rule:  core.Repeat(core.Rune(core.RuneSet("a")), 3, 3),
			input: []string{"a", "aa", "aaa", "aaaa", "b"},
		},
		{
			expr:  `[0-9]{2,}`,
			rule:  core.Repeat(core.Rune(core.ASCIIDigit), 2, -1),
			input: []string{"1", "12", "123", "123456x", "1a", "a"},
		},

		// RuneRunMax: one scanning state that counts the runes.
		{
			expr:  `[0-9]{1,3}`,
			rule:  core.RuneRunMax(core.ASCIIDigit, 3),
			input: []string{"1", "12", "123", "1234", "12a", "a1"},
		},
		{
			expr:  `[01]{1,1}`,
			rule:  core.RuneRunMax(core.ASCIIBinaryDigit, 1),
			input: []string{"0", "01", "2"},
		},
		{
			expr:              `(?s:.){1,4}`,
			rule:              core.RuneRunMax(core.AnyRune, 4),
			input:             []string{"a", "a\nb", "é\x00ß\n", "\n"},
			matchesEveryInput: true,
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
				assert.True(t, errors.Is(err, io.EOF), "Next on empty input: %v", err)
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

// coreClassSampleRunes returns the runes that TestCoreClassesMatchRegexpOracle checks: every
// valid rune below U+3000, which covers ASCII, Latin, Greek, Cyrillic, the
// combining marks and the general punctuation block, and a few runes above.
func coreClassSampleRunes() []rune {
	var runes []rune
	for r := rune(0); r < 0x3000; r++ {
		if utf8.ValidRune(r) {
			runes = append(runes, r)
		}
	}
	return append(runes, 0x3000, 0x4E16, 0xFF21, 0xFFFD, 0x1F600, 0x10FFFF)
}

// TestCoreClassesMatchRegexpOracle: each rules/core class contains exactly the
// runes of the character class its doc comment documents. Go's regexp package
// is only the test oracle for that class.
func TestCoreClassesMatchRegexpOracle(t *testing.T) {
	rows := []struct {
		expr  string
		class core.Class
	}{
		{`[0-9]`, core.ASCIIDigit},
		{`[0-7]`, core.ASCIIOctalDigit},
		{`[01]`, core.ASCIIBinaryDigit},
		{`[A-Za-z_]`, core.ASCIIIdentifierStart},
		{`(?s:.)`, core.AnyRune},
		{`[0-9A-Fa-f]`, core.ASCIIHexDigit},
		{`[A-Za-z]`, core.ASCIILetter},
		{`[a-z]`, core.ASCIILower},
		{`[A-Z]`, core.ASCIIUpper},
		{`\w`, core.ASCIIWord},
		{`\s`, core.ASCIISpace},
		{`\pL`, core.UnicodeLetter},
		{`\pM`, core.UnicodeMark},
		{`\pN`, core.UnicodeNumber},
		{`\pP`, core.UnicodePunct},
		{`\pS`, core.UnicodeSymbol},
		{`\pZ`, core.UnicodeSeparator},
		{`\p{Greek}`, core.UnicodeScript("Greek")},
		{`\p{Han}`, core.UnicodeScript("Han")},
		{`\p{Lu}`, core.UnicodeTables(unicode.Lu)},
		{`[\p{Greek}\p{Cyrillic}]`, core.UnicodeTables(unicode.Greek, unicode.Cyrillic)},
		{`[-+*/=;()]`, core.RuneSet("-+*/=;()")},
		{`[é世]`, core.RuneSet("é世")},
		{`[\x{0391}-\x{03A9}]`, core.RuneRange(0x391, 0x3a9)},
		{`[^0-9]`, core.ASCIIDigit.Not()},
		{`[0-9a-z]`, core.ASCIIDigit.Or(core.ASCIILower)},
		{`[a-f]`, core.ASCIILower.And(core.ASCIIHexDigit)},
		{`[^\pL\pN]`, core.UnicodeLetter.Or(core.UnicodeNumber).Not()},
	}

	runes := coreClassSampleRunes()
	for _, row := range rows {
		t.Run(row.expr, func(t *testing.T) {
			re := regexp.MustCompile(`\A` + row.expr + `\z`)
			for _, r := range runes {
				if want := re.MatchString(string(r)); row.class(r) != want {
					t.Errorf("class(%U) = %v, want %v", r, !want, want)
				}
			}
		})
	}
}

// coreTokenStream lexes input with rule alone and returns every token, as
// its type and text, until the end of input. It keeps going after an UNKNOWN
// token, as the scanner does, and fails if the stream does not end.
func coreTokenStream(t *testing.T, rule bytescan.Rule, input string) []string {
	t.Helper()
	const lexTypeR = bytescan.TokenType("R")
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeR, rule)
	var stream []string
	for i := 0; ; i++ {
		require.Less(t, i, 2*len(input)+2, "stream of %q does not end", input)
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			return stream
		}
		require.NoError(t, err, "input %q", input)
		stream = append(stream, string(lex.Type())+":"+lex.Text())
	}
}

// regexpTokenStream is the independent oracle of coreTokenStream: at each
// position it takes the longest match of re, which is \A(?:expr) with
// Longest, or else one UNKNOWN rune.
func regexpTokenStream(re *regexp.Regexp, input string) []string {
	var stream []string
	for pos := 0; pos < len(input); {
		if m := re.FindString(input[pos:]); m != "" {
			stream = append(stream, "R:"+m)
			pos += len(m)
			continue
		}
		_, size := utf8.DecodeRuneInString(input[pos:])
		stream = append(stream, "UNKNOWN:"+input[pos:pos+size])
		pos += size
	}
	return stream
}

// TestRuneRunMatchesOneOrMoreRune: RuneRun(c) gives the same token stream as
// OneOrMore(Rune(c)) on every input of the oracle rows and on every string of
// up to six runes over a mixed alphabet.
func TestRuneRunMatchesOneOrMoreRune(t *testing.T) {
	classes := map[string]core.Class{
		"ASCIIDigit":    core.ASCIIDigit,
		"ASCIIWord":     core.ASCIIWord,
		"ASCIISpace":    core.ASCIISpace,
		"UnicodeLetter": core.UnicodeLetter,
		"RuneSet ab":    core.RuneSet("ab"),
	}
	inputs := append(stringsOver("ab1_ é-\t", 4), "é", "1a", "ab\ncd")
	for name, c := range classes {
		t.Run(name, func(t *testing.T) {
			for _, input := range inputs {
				want := coreTokenStream(t, core.OneOrMore(core.Rune(c)), input)
				got := coreTokenStream(t, core.RuneRun(c), input)
				require.Equal(t, want, got, "input %q", input)
			}
		})
	}
}

// TestRepeatOfRuneMatchesClassRepeatOracle: Repeat(Rune(c), min, max) gives the
// same complete token stream as the scan of [a]{min,max} on each input, for
// the (min, max) pairs below. The removed class form Repeat(c, min, max) is
// witnessed by that expression: its rows were checked against it.
//
// Coverage is not the whole accepted domain. Every pair with min and max in
// 1..8 (and max unbounded) is checked, plus boundary pairs near 999, 1000 and
// the unbounded maximum. The domain is about 500,000 pairs, which is too many
// for a unit test; the rest is recorded as a scope gap in the MR description.
func TestRepeatOfRuneMatchesClassRepeatOracle(t *testing.T) {
	var pairs []struct{ min, max int }
	for min := 1; min <= 8; min++ {
		pairs = append(pairs, struct{ min, max int }{min, -1})
		for max := min; max <= 8; max++ {
			pairs = append(pairs, struct{ min, max int }{min, max})
		}
	}
	pairs = append(pairs,
		struct{ min, max int }{999, 1000}, struct{ min, max int }{1000, 1000},
		struct{ min, max int }{1, 1000}, struct{ min, max int }{1000, -1},
		struct{ min, max int }{500, 501},
	)
	alphabet := stringsOver("aX", 5)
	for _, p := range pairs {
		t.Run(fmt.Sprintf("min%d_max%d", p.min, p.max), func(t *testing.T) {
			expr := fmt.Sprintf("[a]{%d,}", p.min)
			if p.max >= 0 {
				expr = fmt.Sprintf("[a]{%d,%d}", p.min, p.max)
			}
			re := regexp.MustCompile(`\A(?:` + expr + `)`)
			re.Longest()
			rule := core.Repeat(core.Rune(core.RuneSet("a")), p.min, p.max)

			inputs := append([]string{}, alphabet...)
			// Runs of a around the counts, separated by a rune that is not a.
			for _, n := range []int{p.min - 1, p.min, p.min + 1, p.max - 1, p.max, p.max + 1, 1000, 1001} {
				if n < 0 || n > 1001 {
					continue
				}
				run := strings.Repeat("a", n)
				inputs = append(inputs, run, run+"X", run+"Xa", "X"+run+"aX"+run)
			}
			for _, input := range inputs {
				want := regexpTokenStream(re, input)
				got := coreTokenStream(t, rule, input)
				require.Equal(t, want, got, "input %q", input)
			}
		})
	}
}

// runPieces is the oracle of RuneRunMax(RuneSet("a"), n) above the limit of
// 1000 that Go's regexp sets for a count: each run of 'a' splits into pieces
// of n runes and a shorter last piece, and every other rune is UNKNOWN.
func runPieces(input string, n int) []string {
	var stream []string
	for pos := 0; pos < len(input); {
		if input[pos] != 'a' {
			_, size := utf8.DecodeRuneInString(input[pos:])
			stream = append(stream, "UNKNOWN:"+input[pos:pos+size])
			pos += size
			continue
		}
		end := pos
		for end < len(input) && end-pos < n && input[end] == 'a' {
			end++
		}
		stream = append(stream, "R:"+input[pos:end])
		pos = end
	}
	return stream
}

// TestRuneRunMaxMatchesRepeatOracle: RuneRunMax(c, n) gives the same complete
// token stream as Repeat(Rune(c), 1, n) and as the scan of [a]{1,n} on each
// input, for n from 1 to 8 and near 1000. Above 1000, where neither Repeat
// nor Go's regexp accepts the count, it gives the stream of runPieces.
func TestRuneRunMaxMatchesRepeatOracle(t *testing.T) {
	alphabet := stringsOver("aX", 5)
	runsAround := func(counts ...int) []string {
		inputs := append([]string{}, alphabet...)
		for _, k := range counts {
			if k < 0 {
				continue
			}
			run := strings.Repeat("a", k)
			inputs = append(inputs, run, run+"X", run+"Xa", "X"+run+"aX"+run)
		}
		return inputs
	}

	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 999, 1000} {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			re := regexp.MustCompile(fmt.Sprintf(`\A(?:[a]{1,%d})`, n))
			re.Longest()
			rule := core.RuneRunMax(core.RuneSet("a"), n)
			repeat := core.Repeat(core.Rune(core.RuneSet("a")), 1, n)
			for _, input := range runsAround(n-1, n, n+1, 2*n+1, 1001) {
				want := regexpTokenStream(re, input)
				require.Equal(t, want, coreTokenStream(t, repeat, input), "Repeat, input %q", input)
				require.Equal(t, want, coreTokenStream(t, rule, input), "RuneRunMax, input %q", input)
			}
		})
	}

	for _, n := range []int{1001, 1500, 4096} {
		t.Run(fmt.Sprintf("n%d", n), func(t *testing.T) {
			rule := core.RuneRunMax(core.RuneSet("a"), n)
			for _, input := range runsAround(n-1, n, n+1, 2*n+1) {
				require.Equal(t, runPieces(input, n), coreTokenStream(t, rule, input), "input of %d bytes", len(input))
			}
		})
	}
}

// lineAnchorTokenStream is the oracle of a rule that contains AtLineStart:
// at each position it takes the longest match of expr, which may contain
// (?m:^), or else one UNKNOWN rune. The rune before the position is given to
// the regular expression as context, so that (?m:^) sees whether the position
// is at the beginning of a line.
func lineAnchorTokenStream(expr, input string) []string {
	atStart := regexp.MustCompile(`\A(?:` + expr + `)`)
	atStart.Longest()
	afterRune := regexp.MustCompile(`\A(?s:.)(` + expr + `)`)
	afterRune.Longest()

	var stream []string
	prev := 0 // the byte offset of the rune before pos
	for pos := 0; pos < len(input); {
		var m string
		if pos == 0 {
			m = atStart.FindString(input)
		} else if sub := afterRune.FindStringSubmatchIndex(input[prev:]); sub != nil {
			m = input[prev+sub[2] : prev+sub[3]]
		}
		if m == "" {
			_, size := utf8.DecodeRuneInString(input[pos:])
			m = input[pos : pos+size]
			stream = append(stream, "UNKNOWN:"+m)
		} else {
			stream = append(stream, "R:"+m)
		}
		_, size := utf8.DecodeLastRuneInString(input[:pos+len(m)])
		prev, pos = pos+len(m)-size, pos+len(m)
	}
	return stream
}

// TestAtLineStartMatchesRegexpOracle: AtLineStart(r) gives the same complete
// token stream as the scan of (?m:^)expr, registered alone and inside
// combinators. The beginning of a line is the start of the input and the
// position after each '\n'.
func TestAtLineStartMatchesRegexpOracle(t *testing.T) {
	rows := []struct {
		expr  string
		rule  bytescan.Rule
		input []string
	}{
		{
			expr:  `(?m:^)#`,
			rule:  core.AtLineStart(core.Literal("#")),
			input: []string{"#", "##", "a#", "#\n#", "x\n#a#", "\n\n#", "\r#", "\r\n#"},
		},
		{
			expr:  `(?m:^)[0-9]+`,
			rule:  core.AtLineStart(core.RuneRun(core.ASCIIDigit)),
			input: []string{"12\n34 56", "a1\n2", "1\r\n2", "1\n\n2\n", "é1\n1é"},
		},
		{
			expr:  `(?m:^)é+`,
			rule:  core.AtLineStart(core.RuneRun(core.RuneSet("é"))),
			input: []string{"éé", "aé\né", "世\né世é"},
		},
		{
			expr:  `(?m:^)(?:end|endif)`,
			rule:  core.AtLineStart(core.AnyLiteral("end", "endif")),
			input: []string{"endif\nend", " end\nendi", "endend"},
		},
		{
			expr:  `a\n(?m:^)b`,
			rule:  core.Sequence(core.Literal("a\n"), core.AtLineStart(core.Literal("b"))),
			input: []string{"a\nb", "a\nbx\na\nb", "a\na\nb"},
		},
		{
			expr:  `a(?m:^)b`,
			rule:  core.Sequence(core.Literal("a"), core.AtLineStart(core.Literal("b"))),
			input: []string{"ab", "\nab"},
		},
		{
			expr:  `(?:(?m:^)a|b)+`,
			rule:  core.OneOrMore(core.Choice(core.AtLineStart(core.Literal("a")), core.Literal("b"))),
			input: []string{"ab", "ba", "aab", "a\nab", "bb\nabab"},
		},
		{
			expr:  `[ \n]*(?m:^)x`,
			rule:  core.Sequence(core.ZeroOrMore(core.Rune(core.RuneSet(" \n"))), core.AtLineStart(core.Literal("x"))),
			input: []string{"x", " x", "\nx", " \n x", "  \n\nx\n x"},
		},
	}

	for _, row := range rows {
		t.Run(row.expr, func(t *testing.T) {
			for _, input := range row.input {
				want := lineAnchorTokenStream(row.expr, input)
				got := coreTokenStream(t, row.rule, input)
				require.Equal(t, want, got, "input %q", input)
			}
		})
	}
}
