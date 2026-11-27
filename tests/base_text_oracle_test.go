package tests

import (
	"io"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file check the human-text rules of rules/base against
// Go's regexp package, used only as a test oracle for the expression of each
// rule. They live here so that no file under rules/ imports regexp.
// rules/base has the multilingual and NFD token streams of these rules.

// textOracleRow is one row of TestBaseTextMatchesRegexpOracle: a rule, the
// regular expression it implements, and its inputs: the listed ones, and
// every string over alphabet with 1 to maxLen runes.
//
// A rule with lookahead, which regexp cannot express, sets notFollowedBy: the
// oracle then takes the longest prefix that expr matches in full and that
// notFollowedBy does not reject, given the prefix and the rest of the input.
type textOracleRow struct {
	name          string
	expr          string
	notFollowedBy func(prefix, rest string) bool
	rule          bytescan.Rule
	input         []string
	alphabet      string
	maxLen        int
}

// groupedNumberFollowedByDigit returns a notFollowedBy function for
// TextNumber(sep, decimal): it rejects a prefix that is a grouped integer
// with sep, such as "1,234", when a digit follows it. That is the (?![0-9])
// of the grouped integer.
func groupedNumberFollowedByDigit(sep string) func(prefix, rest string) bool {
	grouped := regexp.MustCompile(`\A[0-9]{1,3}(?:` + regexp.QuoteMeta(sep) + `[0-9]{3})+\z`)
	return func(prefix, rest string) bool {
		return grouped.MatchString(prefix) && rest != "" && rest[0] >= '0' && rest[0] <= '9'
	}
}

// groupedNumberFollowedByClass returns a notFollowedBy function for
// TextNumber(sep, decimal, TextDigits(...)) whose digits are the regexp
// character class digits, such as "[٠-٩]": it rejects a prefix that is a
// grouped integer with sep when a digit of that class follows it.
func groupedNumberFollowedByClass(sep, digits string) func(prefix, rest string) bool {
	grouped := regexp.MustCompile(`\A` + digits + `{1,3}(?:` + regexp.QuoteMeta(sep) + digits + `{3})+\z`)
	digit := regexp.MustCompile(`\A` + digits)
	return func(prefix, rest string) bool {
		return grouped.MatchString(prefix) && digit.MatchString(rest)
	}
}

// TestBaseTextMatchesRegexpOracle: each human-text rule returns the same
// first match as \A(?:expr) under leftmost-longest semantics, with the
// lookahead of its row, on every input of its row. No match on a non-empty
// input is a first token of type UNKNOWN, and the empty input gives io.EOF
// and no token. Each row has a matching input, a non-matching input and a
// match that stops before the end of input.
func TestBaseTextMatchesRegexpOracle(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	rows := []textOracleRow{
		{
			name: "Word",
			expr: `\pL[\pL\pM]*(?:['’\-‐]\pL[\pL\pM]*)*`,
			rule: base.Word(),
			input: []string{
				"don't", "l'été", "well-known", "e\u0301", "'word", "word'", "a‐b", "a‐", "Grüße",
				"Καλημέρα", "мой", "и\u0306", "東京tokyo", "a1", "1a", "rock’n’roll", "a’", "’a", "a’’a", "a’-a",
			},
			alphabet: "aй\u0301'- ", maxLen: 5,
		},
		{
			name:          "TextNumber comma full stop",
			expr:          `(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]+)(?:\.[0-9]+)?%?`,
			notFollowedBy: groupedNumberFollowedByDigit(","),
			rule:          base.TextNumber(',', '.'),
			input: []string{
				"3,500.25", "12.5%", "1,234,567", "1,234,5678", "1,2345", "1234,567", "1,234.5%x",
				"12,345,678.9", "1,234,567,8901", "1,111", "1,1111", "1,111.1%", "1,111,", "1,111.", "11,111%x",
			},
			alphabet: "1,.%x", maxLen: 5,
		},
		{
			name:          "TextNumber full stop comma",
			expr:          `(?:[0-9]{1,3}(?:\.[0-9]{3})+|[0-9]+)(?:,[0-9]+)?%?`,
			notFollowedBy: groupedNumberFollowedByDigit("."),
			rule:          base.TextNumber('.', ','),
			input:         []string{"3.500,25", "1.234.567,8%", "1.2345", "1.111", "1.111,1%"},
			alphabet:      "1.,x", maxLen: 5,
		},
		{
			name:          "TextNumber narrow no-break space comma",
			expr:          `(?:[0-9]{1,3}(?:\x{202F}[0-9]{3})+|[0-9]+)(?:,[0-9]+)?%?`,
			notFollowedBy: groupedNumberFollowedByDigit("\u202F"),
			rule:          base.TextNumber('\u202F', ','),
			input:         []string{"3\u202F500,25", "3 500", "12\u202F345\u202F6789", "1\u202F111", "1\u202F1111"},
			alphabet:      "1\u202F, ", maxLen: 5,
		},
		{
			name:          "TextNumber Arabic-Indic digits",
			expr:          `(?:[٠-٩]{1,3}(?:٬[٠-٩]{3})+|[٠-٩]+)(?:٫[٠-٩]+)?%?`,
			notFollowedBy: groupedNumberFollowedByClass("٬", "[٠-٩]"),
			rule:          base.TextNumber('٬', '٫', base.TextDigits(core.RuneRange('٠', '٩'))),
			input: []string{
				"١٬٢٣٤٫٥", "٣٥٠٠٫٢٥%", "١٬٢٣٤٥", "١٬٢٣٤٬٥٦٧", "١٢", "12", "١٬٢٣٤1", "١,٢٣٤", "١٫",
			},
			alphabet: "١٬٫%1", maxLen: 5,
		},
		{
			name:          "TextNumber ASCII or Arabic-Indic digits",
			expr:          `(?:[0-9٠-٩]{1,3}(?:,[0-9٠-٩]{3})+|[0-9٠-٩]+)(?:\.[0-9٠-٩]+)?%?`,
			notFollowedBy: groupedNumberFollowedByClass(",", "[0-9٠-٩]"),
			rule:          base.TextNumber(',', '.', base.TextDigits(core.ASCIIDigit.Or(core.RuneRange('٠', '٩')))),
			input:         []string{"1,٢٣٤.5", "١٢3", "1,23٤5", "x1"},
			alphabet:      "1١,.", maxLen: 5,
		},
		{
			name:     "Punctuation",
			expr:     `\pP`,
			rule:     base.Punctuation(),
			input:    []string{"...", "«a", "—", "。", "¿?", "$"},
			alphabet: ".?a$", maxLen: 3,
		},
		{
			name:     "PunctuationRun",
			expr:     `\pP+`,
			rule:     base.PunctuationRun(),
			input:    []string{"...", "?!", "»,", "—-", "。」", "!$"},
			alphabet: ".?a«$", maxLen: 4,
		},
		{
			name:     "Symbol",
			expr:     `\pS`,
			rule:     base.Symbol(),
			input:    []string{"$5", "€", "©", "+-", "😀", "^"},
			alphabet: "$+a.", maxLen: 3,
		},
		{
			name:     "ScriptRun Han",
			expr:     `\p{Han}[\p{Han}\pM]*`,
			rule:     base.ScriptRun("Han"),
			input:    []string{"我爱北京天安门", "東京は", "々", "〇一", "。", "a東"},
			alphabet: "東京\u0301aは", maxLen: 4,
		},
		{
			name:     "ScriptRun Thai",
			expr:     `\p{Thai}[\p{Thai}\pM]*`,
			rule:     base.ScriptRun("Thai"),
			input:    []string{"ภาษาไทย", "ภาษาไทย ไทย", "๑๒๓", "\u0E31ก", "ก\u0301", "aก", "กa"},
			alphabet: "ก\u0E31\u0301a", maxLen: 4,
		},
		{
			name:     "ScriptRun Katakana with the prolonged sound mark",
			expr:     `\p{Katakana}[\p{Katakana}\pMー]*`,
			rule:     base.ScriptRun("Katakana", core.RuneSet("ー")),
			input:    []string{"コーヒー", "コーヒーを", "ーコ", "タワー。", "カ\u3099ー", "ひー"},
			alphabet: "コーを\u0301", maxLen: 4,
		},
		{
			name:     "ScriptRun Latin with two extra classes",
			expr:     `\p{Latin}[\p{Latin}\pM'0-9]*`,
			rule:     base.ScriptRun("Latin", core.RuneSet("'"), core.ASCIIDigit),
			input:    []string{"mp3", "don't", "'a", "1a", "a1'b c"},
			alphabet: "a'1 ", maxLen: 4,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			first := regexp.MustCompile(`\A(?:` + row.expr + `)`)
			first.Longest()
			whole := regexp.MustCompile(`\A(?:` + row.expr + `)\z`)

			inputs := append(append([]string{}, row.input...), stringsOver(row.alphabet, row.maxLen)...)
			var matched, unmatched, stoppedEarly bool
			mismatches := 0
			for _, input := range inputs {
				var want string
				if row.notFollowedBy == nil {
					want = first.FindString(input)
				} else {
					want = longestPrefixMatch(whole, row.notFollowedBy, input)
				}
				switch {
				case want == "":
					unmatched = true
				case len(want) < len(input):
					matched, stoppedEarly = true, true
				default:
					matched = true
				}

				lx := bytescan.New(strings.NewReader(input))
				lx.MustAddRule(lexTypeR, row.rule)
				lex, err := lx.Next()
				require.NoError(t, err, "input %q", input)
				got := ""
				if lex.Type() == lexTypeR {
					got = lex.Text()
				}
				if got != want {
					mismatches++
					if mismatches <= 5 {
						assert.Failf(t, "mismatch", "on %q the first match is %q, want %q", input, got, want)
					}
				}
			}
			t.Logf("%d inputs, %d mismatches", len(inputs), mismatches)

			lx := bytescan.New(strings.NewReader(""))
			lx.MustAddRule(lexTypeR, row.rule)
			lex, err := lx.Next()
			assert.ErrorIs(t, err, io.EOF, "Next on empty input: %v", err)
			assert.Nil(t, lex)

			assert.True(t, matched, "row has no matching input")
			assert.True(t, unmatched, "row has no non-matching input")
			assert.True(t, stoppedEarly, "row has no match that stops before the end of input")
		})
	}
}

// longestPrefixMatch returns the longest non-empty prefix of input, cut at a
// rune boundary, that whole matches and that notFollowedBy does not reject,
// or "" if there is none.
func longestPrefixMatch(whole *regexp.Regexp, notFollowedBy func(prefix, rest string) bool, input string) string {
	for end := len(input); end > 0; {
		prefix, rest := input[:end], input[end:]
		if whole.MatchString(prefix) && !notFollowedBy(prefix, rest) {
			return prefix
		}
		_, size := utf8.DecodeLastRuneInString(prefix)
		end -= size
	}
	return ""
}
