package special_test

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/special"
)

// The tests in this file give complete token streams for the special rules,
// with examples from the specification of each rule, and check Emoji against
// every RGI emoji sequence of the committed Unicode data. The regexp oracle
// tests of these rules are in tests/special_oracle_test.go, because no file
// under rules/ imports regexp.

const (
	lexTypeURL   = bytescan.TokenType("URL")
	lexTypeEmail = bytescan.TokenType("EMAIL")
	lexTypeEmoji = bytescan.TokenType("EMOJI")
	lexTypeWord  = bytescan.TokenType("WORD")
	lexTypePunct = bytescan.TokenType("PUNCT")
	lexTypeSpace = bytescan.TokenType("SPACE")
	lexTypeR     = bytescan.TokenType("R")
)

// r returns the stream entry of a token of type R.
func r(text string, offset int) string { return token(lexTypeR, text, offset) }

// only returns a setup that registers rule alone, as type R.
func only(rule bytescan.Rule) func(lx *bytescan.Scanner) {
	return func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, rule) }
}

// TestURLExamples: URL matches the absolute http, https and ftp examples of
// RFC 3986 whole, and no example of another scheme. The examples are from
// sections 1.1.2, 3, 3.2.2, 5.4 and 6.2.2 of RFC 3986, with the scheme
// changed to http where the RFC uses foo.
func TestURLExamples(t *testing.T) {
	u := func(s string) streamCase { return streamCase{s, []string{r(s, 0)}} }
	runStreamCases(t, []streamCase{
		u("ftp://ftp.is.co.za/rfc/rfc1808.txt"),
		u("http://www.ietf.org/rfc/rfc2396.txt"),
		u("http://example.com:8042/over/there?name=ferret#nose"),
		u("http://a/b/c/d;p?q"),
		u("http://a/b/c/g?y/./x"),
		u("http://a/b/c/d;p?q#s"),
		u("HTTP://www.EXAMPLE.com/"),
		u("http://example.com/%7Esmith/"),
		u("http://example.com/%7esmith/"),
		u("http://example.com:/"),
		u("http://[2001:db8::7]/c=GB?objectClass?one"),
		u("http://[v7.fe80::a+en1]/"),
		u("https://192.0.2.16:80/"),
		u("ftp://user:pass@ftp.example.com/pub/file;type=i"),
		u("http://example.com"),
		u("https://a"),
		{"ldap://[2001:db8::7]/c=GB?objectClass?one", unknowns("ldap://[2001:db8::7]/c=GB?objectClass?one", 0)},
		{"mailto:John.Doe@example.com", unknowns("mailto:John.Doe@example.com", 0)},
		{"news:comp.infosystems.www.servers.unix", unknowns("news:comp.infosystems.www.servers.unix", 0)},
		{"telnet://192.0.2.16:80/", unknowns("telnet://192.0.2.16:80/", 0)},
		{"http:/a", unknowns("http:/a", 0)},
		{"http:///a", unknowns("http:///a", 0)},
		{"httpſ://a", unknowns("httpſ://a", 0)},
		{"http://a b", append([]string{r("http://a", 0)}, unknowns(" b", 8)...)},
		{"<http://a/b>.", append(append(unknowns("<", 0), r("http://a/b", 1)), unknowns(">.", 11)...)},
		{"http://a/b%2", append([]string{r("http://a/b", 0)}, unknowns("%2", 10)...)},
		{"http://a/b%2g", append([]string{r("http://a/b", 0)}, unknowns("%2g", 10)...)},
		{"http://a/é", append([]string{r("http://a/", 0)}, unknowns("é", 9)...)},
		{"http://[::1", unknowns("http://[::1", 0)},
	}, only(special.URL()))
}

// TestEmailExamples: Email matches the addresses of RFC 3696 section 3 that
// need no quoting or escape, and none that does.
func TestEmailExamples(t *testing.T) {
	e := func(s string) streamCase { return streamCase{s, []string{r(s, 0)}} }
	runStreamCases(t, []streamCase{
		e("customer/department=shipping@example.com"),
		e("$A12345@example.com"),
		e("!def!xyz%abc@example.com"),
		e("_somename@example.com"),
		e("John.Doe@example.com"),
		e("user@localhost"),
		e("a@b-c.d1"),
		e(".a..b.@example.com"),
		e("a@" + strings.Repeat("x", 63) + ".com"),
		{`Abc\@def@example.com`, append(unknowns(`Abc\@`, 0), r("def@example.com", 5))},
		{`"Abc@def"@example.com`, append(append(unknowns(`"`, 0), r("Abc@def", 1)), unknowns(`"@example.com`, 8)...)},
		{"a@[192.0.2.1]", unknowns("a@[192.0.2.1]", 0)},
		{"me@example.com.", append([]string{r("me@example.com", 0)}, unknowns(".", 14)...)},
		{"me@example-", append([]string{r("me@example", 0)}, unknowns("-", 10)...)},
		{"me@-example.com", unknowns("me@-example.com", 0)},
		{"a@" + strings.Repeat("x", 64), append([]string{r("a@"+strings.Repeat("x", 63), 0)}, unknowns("x", 65)...)},
		{"a@é.com", unknowns("a@é.com", 0)},
		{"@example.com", unknowns("@example.com", 0)},
	}, only(special.Email()))
}

// Emoji test inputs, named after their CLDR short names.
const (
	emojiGrinning      = "\U0001F600"                                         // 😀 default emoji presentation
	emojiThumbsUp      = "\U0001F44D"                                         // 👍 a modifier base
	emojiIndexUp       = "☝"                                                  // ☝ a modifier base with text presentation
	emojiMediumTone    = "\U0001F3FD"                                         // 🏽 a skin tone modifier
	emojiCopyright     = "©"                                                  // © an emoji character with text presentation
	emojiWatch         = "⌚"                                                  // ⌚ default emoji presentation
	emojiWoman         = "\U0001F469"                                         // 👩
	emojiMan           = "\U0001F468"                                         // 👨
	emojiHeart         = "❤"                                                  // ❤ text presentation
	emojiBlackFlag     = "\U0001F3F4"                                         // 🏴 the base of the subdivision flags
	emojiFlagUS        = "\U0001F1FA\U0001F1F8"                               // 🇺🇸
	emojiFlagFR        = "\U0001F1EB\U0001F1F7"                               // 🇫🇷
	emojiTagsGBENG     = "\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067" // the tag_spec "gbeng"
	emojiVS16          = "️"
	emojiVS15          = "︎"
	emojiZWJ           = "‍"
	emojiKeycapMark    = "⃣"
	emojiCancelTag     = "\U000E007F"
	emojiCoupleHeart   = emojiWoman + emojiZWJ + emojiHeart + emojiVS16 + emojiZWJ + emojiMan // 👩‍❤️‍👨
	emojiFlagEngland   = emojiBlackFlag + emojiTagsGBENG + emojiCancelTag                     // 🏴󠁧󠁢󠁥󠁮󠁧󠁿
	emojiKeycapOne     = "1" + emojiVS16 + emojiKeycapMark                                    // 1️⃣
	emojiWomanMedium   = emojiWoman + emojiMediumTone                                         // 👩🏽
	emojiRainbowFlag   = "\U0001F3F3" + emojiVS16 + emojiZWJ + "\U0001F308"                   // 🏳️‍🌈
	emojiRegionalU     = "\U0001F1FA"
	emojiPersonGolfing = "\U0001F3CC" + emojiMediumTone + emojiZWJ + "♂" + emojiVS16 // 🏌🏽‍♂️
)

// TestEmojiExamples: Emoji matches the sequence examples of UTS #51 sections 1.4
// and 2 as one token each, and no unqualified emoji character, lone
// component or text presentation sequence.
func TestEmojiExamples(t *testing.T) {
	e := func(s string) streamCase { return streamCase{s, []string{r(s, 0)}} }
	n := func(s string) int { return len([]rune(s)) }
	runStreamCases(t, []streamCase{
		e(emojiGrinning),
		e(emojiWatch),
		e(emojiCopyright + emojiVS16),
		e(emojiThumbsUp + emojiMediumTone),
		e(emojiIndexUp + emojiMediumTone),
		e(emojiMediumTone),
		e(emojiKeycapOne),
		e("#" + emojiVS16 + emojiKeycapMark),
		e(emojiFlagUS),
		e(emojiFlagEngland),
		e(emojiCoupleHeart),
		e(emojiRainbowFlag),
		e(emojiPersonGolfing),
		e(emojiWoman + emojiZWJ + emojiCopyright),
		e(emojiBlackFlag + emojiVS16 + emojiTagsGBENG + emojiCancelTag),
		e(emojiWoman + emojiZWJ + emojiFlagEngland),
		e(emojiWoman + emojiZWJ + emojiKeycapOne),
		e(emojiWoman + emojiZWJ + emojiFlagUS),
		e("1" + emojiVS16),
		{emojiFlagUS + emojiFlagFR, []string{r(emojiFlagUS, 0), r(emojiFlagFR, 2)}},
		{emojiGrinning + emojiMediumTone, []string{r(emojiGrinning, 0), r(emojiMediumTone, 1)}},
		{emojiWomanMedium + emojiMediumTone, []string{r(emojiWomanMedium, 0), r(emojiMediumTone, 2)}},
		{"1", unknowns("1", 0)},
		{"#", unknowns("#", 0)},
		{emojiCopyright, unknowns(emojiCopyright, 0)},
		{emojiIndexUp, unknowns(emojiIndexUp, 0)},
		{emojiHeart + emojiZWJ + emojiWoman, append(unknowns(emojiHeart+emojiZWJ, 0), r(emojiWoman, 2))},
		{"1" + emojiKeycapMark, unknowns("1"+emojiKeycapMark, 0)},
		{emojiRegionalU, unknowns(emojiRegionalU, 0)},
		{emojiRegionalU + " " + emojiRegionalU, unknowns(emojiRegionalU+" "+emojiRegionalU, 0)},
		{emojiWatch + emojiVS15, unknowns(emojiWatch+emojiVS15, 0)},
		{emojiZWJ + emojiGrinning, append(unknowns(emojiZWJ, 0), r(emojiGrinning, 1))},
		{emojiVS16, unknowns(emojiVS16, 0)},
		{emojiTagsGBENG + emojiCancelTag, unknowns(emojiTagsGBENG+emojiCancelTag, 0)},
		{emojiBlackFlag + emojiCancelTag, append([]string{r(emojiBlackFlag, 0)}, unknowns(emojiCancelTag, 1)...)},
		{emojiGrinning + emojiZWJ + emojiZWJ + emojiGrinning, append(append([]string{r(emojiGrinning, 0)}, unknowns(emojiZWJ+emojiZWJ, 1)...), r(emojiGrinning, 3))},
		{emojiGrinning + emojiZWJ + "a", append([]string{r(emojiGrinning, 0)}, unknowns(emojiZWJ+"a", 1)...)},
		{"a" + emojiGrinning, append(unknowns("a", 0), r(emojiGrinning, 1))},
		{emojiCoupleHeart + emojiCoupleHeart, []string{r(emojiCoupleHeart, 0), r(emojiCoupleHeart, n(emojiCoupleHeart))}},
	}, only(special.Emoji()))
}

// TestSpecialEndOfInput: the input ends in each scanning state of each rule,
// and each rule ends with the longest match read so far, or with nothing.
func TestSpecialEndOfInput(t *testing.T) {
	cases := map[string]struct {
		rule  bytescan.Rule
		cases []streamCase
	}{
		"URL": {special.URL(), []streamCase{
			{"h", unknowns("h", 0)},
			{"https", unknowns("https", 0)},
			{"http:", unknowns("http:", 0)},
			{"http:/", unknowns("http:/", 0)},
			{"http://", unknowns("http://", 0)},
			{"http://a", []string{r("http://a", 0)}},
			{"http://u@", append([]string{r("http://u", 0)}, unknowns("@", 8)...)},
			{"http://u:p", []string{r("http://u:", 0), unknowns("p", 9)[0]}},
			{"http://a:", []string{r("http://a:", 0)}},
			{"http://a:8", []string{r("http://a:8", 0)}},
			{"http://a%", append([]string{r("http://a", 0)}, unknowns("%", 8)...)},
			{"http://a%4", append([]string{r("http://a", 0)}, unknowns("%4", 8)...)},
			{"http://a%41", []string{r("http://a%41", 0)}},
			{"http://[", unknowns("http://[", 0)},
			{"http://[::", unknowns("http://[::", 0)},
			{"http://[v1", unknowns("http://[v1", 0)},
			{"http://[v1.", unknowns("http://[v1.", 0)},
			{"http://[v1.a", unknowns("http://[v1.a", 0)},
			{"http://[::]", []string{r("http://[::]", 0)}},
			{"http://a/", []string{r("http://a/", 0)}},
			{"http://a/b", []string{r("http://a/b", 0)}},
			{"http://a/b%", append([]string{r("http://a/b", 0)}, unknowns("%", 10)...)},
			{"http://a?", []string{r("http://a?", 0)}},
			{"http://a?q", []string{r("http://a?q", 0)}},
			{"http://a#", []string{r("http://a#", 0)}},
			{"http://a#f", []string{r("http://a#f", 0)}},
		}},
		"Email": {special.Email(), []streamCase{
			{"a", unknowns("a", 0)},
			{"a@", unknowns("a@", 0)},
			{"a@b", []string{r("a@b", 0)}},
			{"a@b-", append([]string{r("a@b", 0)}, unknowns("-", 3)...)},
			{"a@b-c", []string{r("a@b-c", 0)}},
			{"a@b.", append([]string{r("a@b", 0)}, unknowns(".", 3)...)},
			{"a@b.c", []string{r("a@b.c", 0)}},
			{"a@b.c--", append([]string{r("a@b.c", 0)}, unknowns("--", 5)...)},
		}},
		"Emoji": {special.Emoji(), []streamCase{
			{emojiWatch, []string{r(emojiWatch, 0)}},
			{emojiCopyright, unknowns(emojiCopyright, 0)},
			{emojiIndexUp, unknowns(emojiIndexUp, 0)},
			{emojiThumbsUp, []string{r(emojiThumbsUp, 0)}},
			{"1", unknowns("1", 0)},
			{"1" + emojiVS16, []string{r("1"+emojiVS16, 0)}},
			{emojiRegionalU, unknowns(emojiRegionalU, 0)},
			{emojiBlackFlag + emojiTagsGBENG, append([]string{r(emojiBlackFlag, 0)}, unknowns(emojiTagsGBENG, 1)...)},
			{emojiWoman + emojiZWJ, append([]string{r(emojiWoman, 0)}, unknowns(emojiZWJ, 1)...)},
			{emojiWoman + emojiZWJ + emojiIndexUp, []string{r(emojiWoman+emojiZWJ+emojiIndexUp, 0)}},
			{emojiWoman + emojiZWJ + emojiRegionalU, []string{r(emojiWoman+emojiZWJ+emojiRegionalU, 0)}},
			{emojiWoman + emojiZWJ + "1" + emojiVS16, []string{r(emojiWoman+emojiZWJ+"1"+emojiVS16, 0)}},
			{emojiWoman + emojiZWJ + emojiBlackFlag + emojiTagsGBENG, append([]string{r(emojiWoman+emojiZWJ+emojiBlackFlag, 0)}, unknowns(emojiTagsGBENG, 3)...)},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			runStreamCases(t, tc.cases, only(tc.rule))
		})
	}
}

// TestSpecialEmptyInput: on the empty input each rule gives io.EOF and no
// token.
func TestSpecialEmptyInput(t *testing.T) {
	for name, rule := range map[string]bytescan.Rule{"URL": special.URL(), "Email": special.Email(), "Emoji": special.Emoji()} {
		t.Run(name, func(t *testing.T) {
			lx := bytescan.New(strings.NewReader(""))
			lx.MustAddRule(lexTypeR, rule)
			lex, err := lx.Next()
			assert.True(t, errors.Is(err, io.EOF), "Next on empty input: %v", err)
			assert.Nil(t, lex)
		})
	}
}

// TestSpecialWithBaseRules: with the rules of package base registered after
// them, as the package documentation recommends, the special rules take the
// URL, the address and the whole emoji sequence out of prose.
func TestSpecialWithBaseRules(t *testing.T) {
	setup := func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeURL, special.URL())
		lx.MustAddRule(lexTypeEmail, special.Email())
		lx.MustAddRule(lexTypeEmoji, special.Emoji())
		lx.MustAddRule(lexTypeWord, base.Word())
		lx.MustAddRule(lexTypePunct, base.Punctuation())
		lx.MustAddRule("SYMBOL", base.Symbol())
		lx.MustAddRule(lexTypeSpace, base.Whitespace())
	}
	u := func(s string, o int) string { return token(lexTypeURL, s, o) }
	m := func(s string, o int) string { return token(lexTypeEmail, s, o) }
	j := func(s string, o int) string { return token(lexTypeEmoji, s, o) }
	w := func(s string, o int) string { return token(lexTypeWord, s, o) }
	p := func(s string, o int) string { return token(lexTypePunct, s, o) }
	sp := func(s string, o int) string { return token(lexTypeSpace, s, o) }
	runStreamCases(t, []streamCase{
		{"see <https://example.com/a?b=c> now", []string{
			w("see", 0), sp(" ", 3), token("SYMBOL", "<", 4), u("https://example.com/a?b=c", 5),
			token("SYMBOL", ">", 30), sp(" ", 31), w("now", 32),
		}},
		{"mailto:John.Doe@example.com", []string{w("mailto", 0), p(":", 6), m("John.Doe@example.com", 7)}},
		{"mail me@example.com.", []string{w("mail", 0), sp(" ", 4), m("me@example.com", 5), p(".", 19)}},
		{"http://user@example.com", []string{u("http://user@example.com", 0)}},
		{"ok 👍🏽 " + emojiCoupleHeart, []string{w("ok", 0), sp(" ", 2), j("👍🏽", 3), sp(" ", 5), j(emojiCoupleHeart, 6)}},
		{"© 2026", []string{token("SYMBOL", "©", 0), sp(" ", 1), token(bytescan.TokenTypeUnknown, "2", 2),
			token(bytescan.TokenTypeUnknown, "0", 3), token(bytescan.TokenTypeUnknown, "2", 4), token(bytescan.TokenTypeUnknown, "6", 5)}},
	}, setup)
}

// TestSpecialLongTokens: a token of about 1<<15 runes is lexed as one token
// by each rule whose text can grow without bound. A rule whose cost grew with
// the square of the token length would take minutes here.
func TestSpecialLongTokens(t *testing.T) {
	const n = 1 << 15
	cases := map[string]struct {
		rule  bytescan.Rule
		input string
	}{
		"URL path":     {special.URL(), "http://a" + strings.Repeat("/b%41", n/5)},
		"URL host":     {special.URL(), "http://" + strings.Repeat("a.", n/2) + "b:8"},
		"URL userinfo": {special.URL(), "http://" + strings.Repeat("u:", n/2) + "@h"},
		"URL query":    {special.URL(), "http://a?" + strings.Repeat("k=v&", n/4) + "#" + strings.Repeat("f/", n/2)},
		"Email local":  {special.Email(), strings.Repeat("a.", n/2) + "@b.c"},
		"Email domain": {special.Email(), "a@" + strings.Repeat("b-c.", n/4) + "d"},
		"Emoji ZWJ":    {special.Emoji(), emojiWoman + strings.Repeat(emojiZWJ+emojiWomanMedium, n/3)},
		"Emoji tags":   {special.Emoji(), emojiBlackFlag + strings.Repeat("\U000E0061", n) + emojiCancelTag},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, []string{r(tc.input, 0)}, lexStream(t, tc.input+" ", func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeR, tc.rule)
				lx.MustAddRule(lexTypeSpace, base.Whitespace())
			})[:1])
		})
	}
}

// rgiSequence is one RGI emoji sequence of the Unicode data files: its text,
// its type field and the line it was read from.
type rgiSequence struct {
	text, typ string
	line      int
}

// readRGISequences reads the RGI emoji sequences of the committed copy of an
// emoji-sequences.txt or emoji-zwj-sequences.txt file: each code point of a
// range, and each listed sequence. It also returns the version of the file.
func readRGISequences(t *testing.T, path string) ([]rgiSequence, string) {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var seqs []rgiSequence
	version := ""
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if v, ok := strings.CutPrefix(text, "# Version:"); ok && version == "" {
			version = strings.TrimSpace(v)
		}
		if i := strings.IndexByte(text, '#'); i >= 0 {
			text = text[:i]
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields := strings.Split(text, ";")
		require.Len(t, fields, 3, "%s:%d", path, line)
		typ := strings.TrimSpace(fields[1])
		points := strings.TrimSpace(fields[0])
		if lo, hi, ok := strings.Cut(points, ".."); ok {
			for c := parseCodePoint(t, lo); c <= parseCodePoint(t, hi); c++ {
				seqs = append(seqs, rgiSequence{string(c), typ, line})
			}
			continue
		}
		var b strings.Builder
		for _, cp := range strings.Fields(points) {
			b.WriteRune(parseCodePoint(t, cp))
		}
		seqs = append(seqs, rgiSequence{b.String(), typ, line})
	}
	require.NoError(t, sc.Err())
	return seqs, version
}

// parseCodePoint parses a code point in hexadecimal.
func parseCodePoint(t *testing.T, s string) rune {
	t.Helper()
	v, err := strconv.ParseUint(s, 16, 32)
	require.NoError(t, err, "code point %q", s)
	return rune(v)
}

// TestEmojiRGISequences: Emoji matches each RGI emoji sequence of the
// committed Unicode data, of the version of the generated table, as one
// token: each sequence alone, and all of them in one input separated by
// spaces.
func TestEmojiRGISequences(t *testing.T) {
	files := []string{
		"internal/gen/data/emoji-sequences.txt",
		"internal/gen/data/emoji-zwj-sequences.txt",
	}
	var all []rgiSequence
	types := map[string]int{}
	for _, path := range files {
		seqs, version := readRGISequences(t, path)
		require.True(t, strings.HasPrefix(special.UnicodeEmojiVersion, version+"."),
			"%s has version %q, the table has %q", path, version, special.UnicodeEmojiVersion)
		for _, s := range seqs {
			types[s.typ]++
		}
		all = append(all, seqs...)
	}
	t.Logf("%d RGI emoji sequences: %v", len(all), types)
	for _, typ := range []string{"Basic_Emoji", "Emoji_Keycap_Sequence", "RGI_Emoji_Flag_Sequence",
		"RGI_Emoji_Tag_Sequence", "RGI_Emoji_Modifier_Sequence", "RGI_Emoji_ZWJ_Sequence"} {
		require.NotZero(t, types[typ], "no sequence of type %s", typ)
	}

	rule := special.Emoji()
	failures := 0
	for _, s := range all {
		got := lexStream(t, s.text, only(rule))
		if want := []string{r(s.text, 0)}; !assert.ObjectsAreEqual(want, got) {
			failures++
			if failures <= 10 {
				t.Errorf("line %d (%s) %+q: got %v", s.line, s.typ, s.text, got)
			}
		}
	}
	require.Zero(t, failures, "of %d sequences", len(all))

	var input strings.Builder
	var want []string
	offset := 0
	for _, s := range all {
		want = append(want, r(s.text, offset), token(lexTypeSpace, " ", offset+len([]rune(s.text))))
		input.WriteString(s.text + " ")
		offset += len([]rune(s.text)) + 1
	}
	require.Equal(t, want, lexStream(t, input.String(), func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, rule)
		lx.MustAddRule(lexTypeSpace, base.Whitespace())
	}))
}
