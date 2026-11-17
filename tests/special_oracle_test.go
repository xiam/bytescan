package tests

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/special"
)

// The tests in this file check the rules of rules/special against Go's regexp
// package, used only as a test oracle for the expression of each rule. They
// live here so that no file under rules/ imports regexp. rules/special has the
// specification examples, the end-of-input cases and the RGI emoji sequences.

// specialURLExpr is the regular expression of special.URL.
const specialURLExpr = `(?:[hH][tT][tT][pP][sS]?|[fF][tT][pP])://(?:(?:[A-Za-z0-9\-._~!$&'()*+,;=:]|%[0-9A-Fa-f]{2})*@)?` +
	`(?:\[(?:[0-9A-Fa-f:.]+|[vV][0-9A-Fa-f]+\.[A-Za-z0-9\-._~!$&'()*+,;=:]+)\]` +
	`|(?:[A-Za-z0-9\-._~!$&'()*+,;=]|%[0-9A-Fa-f]{2})+)(?::[0-9]*)?` +
	`(?:/(?:[A-Za-z0-9\-._~!$&'()*+,;=:@]|%[0-9A-Fa-f]{2})*)*` +
	`(?:\?(?:[A-Za-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9A-Fa-f]{2})*)?` +
	`(?:#(?:[A-Za-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9A-Fa-f]{2})*)?`

// specialEmailExpr is the regular expression of special.Email: the "valid
// email address" of the WHATWG HTML Living Standard, without its anchors.
const specialEmailExpr = "[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?" +
	`(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*`

// emojiDataPath is the committed copy of emoji-data.txt that the table of
// special.Emoji is generated from.
const emojiDataPath = "../rules/special/internal/gen/data/emoji-data.txt"

// emojiPropertyClasses reads emoji-data.txt and returns, for each property, a
// regexp character class body of its code points, such as `\x{23}\x{30}-\x{39}`.
// It parses the file on its own, independently of internal/gen.
func emojiPropertyClasses(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(emojiDataPath)
	require.NoError(t, err)
	defer f.Close()

	classes := map[string]*strings.Builder{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		text, _, _ := strings.Cut(sc.Text(), "#")
		points, prop, ok := strings.Cut(text, ";")
		if !ok {
			continue
		}
		prop = strings.TrimSpace(prop)
		if classes[prop] == nil {
			classes[prop] = &strings.Builder{}
		}
		lo, hi, isRange := strings.Cut(strings.TrimSpace(points), "..")
		if !isRange {
			hi = lo
		}
		for _, cp := range []string{lo, hi} {
			_, err := strconv.ParseUint(cp, 16, 32)
			require.NoError(t, err, "code point %q", cp)
		}
		fmt.Fprintf(classes[prop], `\x{%s}-\x{%s}`, lo, hi)
	}
	require.NoError(t, sc.Err())
	out := map[string]string{}
	for prop, b := range classes {
		out[prop] = b.String()
	}
	return out
}

// specialEmojiExpr returns the regular expression of special.Emoji, built
// from the grammar of its comment and the property classes of emoji-data.txt.
// The (?!\x{FE0E}) after a lone qualified character is not in the expression;
// emojiTextPresentation is its notFollowedBy.
func specialEmojiExpr(t *testing.T) string {
	c := emojiPropertyClasses(t)
	emoji := `[` + c["Emoji"] + `]`
	presentation := emoji + `\x{FE0F}`
	modifier := `[` + c["Emoji_Modifier_Base"] + `][` + c["Emoji_Modifier"] + `]`
	keycap := `[0-9#*]\x{FE0F}\x{20E3}`
	flag := `[\x{1F1E6}-\x{1F1FF}]{2}`
	tag := `[\x{E0020}-\x{E007E}]+\x{E007F}`
	qualifiedStart := `(?:` + presentation + `|` + modifier + `)`
	base := `(?:` + emoji + `|` + presentation + `|` + modifier + `)`
	element := `(?:` + base + `|` + keycap + `|` + flag + `|` + base + tag + `)`
	// [\p{EPres}--\p{RI}]: Go's regexp has no class subtraction.
	qualifiedCharacter := emojiQualifiedClass(t, c["Emoji_Presentation"])
	first := `(?:` + qualifiedCharacter + `|` + qualifiedStart + `|` + keycap + `|` + flag +
		`|(?:` + qualifiedCharacter + `|` + qualifiedStart + `)` + tag + `)`
	return first + `(?:\x{200D}` + element + `)*`
}

// emojiQualifiedClass returns the regexp class [\p{EPres}--\p{RI}] from the
// class body of Emoji_Presentation, by removing the regional indicators
// U+1F1E6..U+1F1FF from its ranges.
func emojiQualifiedClass(t *testing.T, body string) string {
	t.Helper()
	ranges := regexp.MustCompile(`\\x\{([0-9A-F]+)\}-\\x\{([0-9A-F]+)\}`).FindAllStringSubmatch(body, -1)
	var b strings.Builder
	b.WriteString("[")
	for _, m := range ranges {
		lo, _ := strconv.ParseUint(m[1], 16, 32)
		hi, _ := strconv.ParseUint(m[2], 16, 32)
		for _, part := range [][2]uint64{{lo, min(hi, 0x1F1E5)}, {max(lo, 0x1F200), hi}} {
			if part[0] <= part[1] {
				fmt.Fprintf(&b, `\x{%X}-\x{%X}`, part[0], part[1])
			}
		}
	}
	b.WriteString("]")
	return b.String()
}

// emojiTextPresentation is the notFollowedBy of special.Emoji: a lone
// character with default emoji presentation is not a match when U+FE0E
// follows it, because they form a text presentation sequence.
func emojiTextPresentation(prefix, rest string) bool {
	return utf8.RuneCountInString(prefix) == 1 && strings.HasPrefix(rest, "\uFE0E")
}

// withPrefix returns each string of ss with prefix before it.
func withPrefix(prefix string, ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = prefix + s
	}
	return out
}

// TestSpecialMatchesRegexpOracle: each special rule returns the same first
// match as \A(?:expr) under leftmost-longest semantics, with the lookahead of
// its row, on every input of its row. No match on a non-empty input is a first
// token of type UNKNOWN, and the empty input gives io.EOF and no token. Each
// row has a matching input, a non-matching input and a match that stops before
// the end of input.
func TestSpecialMatchesRegexpOracle(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	rows := []textOracleRow{
		{
			name: "URL",
			expr: specialURLExpr,
			rule: special.URL(),
			input: []string{
				"http://a", "https://example.com:8042/over/there?name=ferret#nose", "ftp://u:p@h/f;type=i",
				"HtTp://A", "http://[::1]:80/", "http://[v1.x:y]/", "http://[v1.]", "http://[]", "http://a%4", "http://a%41b",
				"http://a/b%2g", "http://:80", "http://@a", "http://a:b@c:d", "http://a?b#c?d/e", "http://a#b#c",
				"http:/a", "https", "ftp:", "mailto:a@b", "http://é", "http://a/é", "http://a b", "http://a>", "httpſ://a", "HTTPſ://a",
			},
			alphabet: "h:/a@%1[]", maxLen: 4,
		},
		{
			name:  "URL after scheme",
			expr:  specialURLExpr,
			rule:  special.URL(),
			input: withPrefix("http://", stringsOver("a@:/%4?#[]", 4)),
		},
		{
			name: "Email",
			expr: specialEmailExpr,
			rule: special.Email(),
			input: []string{
				"a@b", "me@example.com", "me@example.com.", "a.b@c-d.e", "a@b-", "a@-b", "a@b..c", ".@a", "a@@b",
				"a@" + strings.Repeat("b", 63), "a@" + strings.Repeat("b", 64), "a@" + strings.Repeat("b", 62) + "-c",
				"a@" + strings.Repeat("b", 62) + "-", "\"a\"@b", "a@é", "é@a", "a@b.c-",
			},
			alphabet: "a@.-é", maxLen: 5,
		},
	}

	emojiExpr := specialEmojiExpr(t)
	rows = append(rows, textOracleRow{
		name:          "Emoji",
		expr:          emojiExpr,
		notFollowedBy: emojiTextPresentation,
		rule:          special.Emoji(),
		input: []string{
			"😀", "👩\u200D❤\uFE0F\u200D👨", "🇺🇸🇫🇷", "🏴\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F",
			"1\uFE0F\u20E3", "1\u20E3", "⌚\uFE0E", "⌚\uFE0F", "👍🏽", "☝🏽", "©", "©\uFE0F", "😀\u200D", "😀\u200D1",
		},
		// 😀 default emoji presentation; 👍 also a modifier base; ☝ a modifier
		// base with text presentation; 🏽 a modifier; 1 text presentation and a
		// keycap base; a regional indicator; VS16, VS15, the keycap mark, ZWJ,
		// a tag and the cancel tag.
		alphabet: "😀👍☝🏽1\U0001F1FA\uFE0F\uFE0E\u20E3\u200D\U000E0067\U000E007F",
		maxLen:   4,
	})

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
						t.Errorf("on %+q the first match is %+q, want %+q", input, got, want)
					}
				}
			}
			t.Logf("%d inputs, %d mismatches", len(inputs), mismatches)

			lx := bytescan.New(strings.NewReader(""))
			lx.MustAddRule(lexTypeR, row.rule)
			lex, err := lx.Next()
			assert.True(t, errors.Is(err, io.EOF), "Next on empty input: %v", err)
			assert.Nil(t, lex)

			assert.True(t, matched, "row has no matching input")
			assert.True(t, unmatched, "row has no non-matching input")
			assert.True(t, stoppedEarly, "row has no match that stops before the end of input")
		})
	}
}
