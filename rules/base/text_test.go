package base_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file give complete token streams for the human-text
// rules: multilingual text, NFD forms, the lookahead of TextNumber and the
// ends of input. The regexp oracle tests of these rules are in
// tests/base_text_oracle_test.go, because no file under rules/ imports regexp.

const (
	lexTypeWord        = bytescan.TokenType("WORD")
	lexTypeNumber      = bytescan.TokenType("NUMBER")
	lexTypePunctuation = bytescan.TokenType("PUNCT")
	lexTypeSymbol      = bytescan.TokenType("SYMBOL")
	lexTypeSpace       = bytescan.TokenType("SPACE")
	lexTypeHan         = bytescan.TokenType("HAN")
	lexTypeThai        = bytescan.TokenType("THAI")
)

// newSpaceRule returns a rule for a run of white space: [\s\pZ]+. At end of
// input it ends with the run read so far.
func newSpaceRule() bytescan.Rule {
	return core.RuneRun(core.ASCIISpace.Or(core.UnicodeSeparator))
}

// addTextRules registers the human-text rules in the order that the package
// documentation recommends: script runs before Word.
func addTextRules(lx *bytescan.Scanner) {
	lx.MustAddRule(lexTypeHan, base.ScriptRun("Han"))
	lx.MustAddRule(lexTypeThai, base.ScriptRun("Thai"))
	lx.MustAddRule(lexTypeWord, base.Word())
	lx.MustAddRule(lexTypeNumber, base.TextNumber(',', '.'))
	lx.MustAddRule(lexTypePunctuation, base.PunctuationRun())
	lx.MustAddRule(lexTypeSymbol, base.Symbol())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())
}

// The NFD forms of the test inputs, written with explicit combining marks.
const (
	nfdEAcute   = "e\u0301" // é: e + COMBINING ACUTE ACCENT
	nfdUUmlaut  = "u\u0308" // ü: u + COMBINING DIAERESIS
	nfdAlphaAcc = "α\u0301" // ά: α + COMBINING ACUTE ACCENT
	nfdShortI   = "и\u0306" // й: и + COMBINING BREVE
)

// TestTextMultilingualStreams: the human-text rules split English, French,
// German, Greek, Russian, Han and Thai text, in NFC and NFD forms, into the
// complete expected token streams. Offsets count runes, so an NFD combining
// mark counts as one rune of its word.
func TestTextMultilingualStreams(t *testing.T) {
	w := func(text string, offset int) string { return token(lexTypeWord, text, offset) }
	n := func(text string, offset int) string { return token(lexTypeNumber, text, offset) }
	p := func(text string, offset int) string { return token(lexTypePunctuation, text, offset) }
	s := func(text string, offset int) string { return token(lexTypeSymbol, text, offset) }
	sp := func(text string, offset int) string { return token(lexTypeSpace, text, offset) }
	han := func(text string, offset int) string { return token(lexTypeHan, text, offset) }
	thai := func(text string, offset int) string { return token(lexTypeThai, text, offset) }

	runStreamCases(t, []streamCase{
		// English.
		{"I don't know...", []string{w("I", 0), sp(" ", 1), w("don't", 2), sp(" ", 7), w("know", 8), p("...", 12)}},
		{"A well-known fact costs $3,500.25?!", []string{
			w("A", 0), sp(" ", 1), w("well-known", 2), sp(" ", 12), w("fact", 13), sp(" ", 17),
			w("costs", 18), sp(" ", 23), s("$", 24), n("3,500.25", 25), p("?!", 33),
		}},
		{"'word' and word'", []string{p("'", 0), w("word", 1), p("'", 5), sp(" ", 6), w("and", 7), sp(" ", 10), w("word", 11), p("'", 15)}},
		{"rock’n’roll, 12.5% off", []string{w("rock’n’roll", 0), p(",", 11), sp(" ", 12), n("12.5%", 13), sp(" ", 18), w("off", 19)}},
		{"well--known -x y-", []string{w("well", 0), p("--", 4), w("known", 6), sp(" ", 11), p("-", 12), w("x", 13), sp(" ", 14), w("y", 15), p("-", 16)}},

		// French, NFC and NFD.
		{"L'été coûte 3,5 €.", []string{
			w("L'été", 0), sp(" ", 5), w("coûte", 6), sp(" ", 11), n("3", 12), p(",", 13), n("5", 14), sp(" ", 15), s("€", 16), p(".", 17),
		}},
		{"l'" + nfdEAcute + "t" + nfdEAcute, []string{w("l'"+nfdEAcute+"t"+nfdEAcute, 0)}},
		{"« Bonjour » ", []string{p("«", 0), sp(" ", 1), w("Bonjour", 2), sp(" ", 9), p("»", 10), sp(" ", 11)}},

		// German, NFC and NFD.
		{"Grüße aus München!", []string{w("Grüße", 0), sp(" ", 5), w("aus", 6), sp(" ", 9), w("München", 10), p("!", 17)}},
		{"M" + nfdUUmlaut + "nchen", []string{w("M"+nfdUUmlaut+"nchen", 0)}},

		// Greek, NFC and NFD.
		{"Καλημέρα, κόσμε;", []string{w("Καλημέρα", 0), p(",", 8), sp(" ", 9), w("κόσμε", 10), p(";", 15)}},
		{nfdAlphaAcc + "λλο", []string{w(nfdAlphaAcc+"λλο", 0)}},

		// Russian, NFC and NFD.
		{"Привет, мир — 100%", []string{w("Привет", 0), p(",", 6), sp(" ", 7), w("мир", 8), sp(" ", 11), p("—", 12), sp(" ", 13), n("100%", 14)}},
		{"мо" + nfdShortI + " дом", []string{w("мо"+nfdShortI, 0), sp(" ", 4), w("дом", 5)}},

		// Han and Thai, which write words without spaces. Word does not stop
		// where the script changes, so it takes a Han run that Hiragana or
		// Latin letters follow.
		{"東京は大きい。", []string{w("東京は大きい", 0), p("。", 6)}},
		{"我爱北京天安门", []string{han("我爱北京天安门", 0)}},
		{"ภาษาไทย ๑๒", []string{thai("ภาษาไทย", 0), sp(" ", 7), thai("๑๒", 8)}},
		{"東京tokyo", []string{w("東京tokyo", 0)}},
	}, addTextRules)
}

// TestWordNFDIsOneToken: the NFD input "e" + U+0301 is one Word token of
// two runes, as the NFC "é" is one Word token of one rune.
func TestWordNFDIsOneToken(t *testing.T) {
	setup := func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeWord, base.Word()) }
	assert.Equal(t, []string{token(lexTypeWord, nfdEAcute, 0)}, lexStream(t, nfdEAcute, setup))
	assert.Equal(t, []string{token(lexTypeWord, "é", 0)}, lexStream(t, "é", setup))
}

// TestWordEndOfInput: Word ends at the end of input in each of its states:
// after the first letter, after a mark, after an inner letter and after a
// joiner, which it excludes. A leading mark or joiner is not a word.
func TestWordEndOfInput(t *testing.T) {
	w := func(text string, offset int) string { return token(lexTypeWord, text, offset) }
	u := func(text string, offset int) string { return token(bytescan.TokenTypeUnknown, text, offset) }
	runStreamCases(t, []streamCase{
		{"a", []string{w("a", 0)}},
		{nfdEAcute, []string{w(nfdEAcute, 0)}},
		{"ab", []string{w("ab", 0)}},
		{"don'", []string{w("don", 0), u("'", 3)}},
		{"don't", []string{w("don't", 0)}},
		{"x-" + nfdEAcute, []string{w("x-"+nfdEAcute, 0)}},
		{"x-\u0301", []string{w("x", 0), u("-", 1), u("\u0301", 2)}},
		{"\u0301e", []string{u("\u0301", 0), w("e", 1)}},
		{"'a", []string{u("'", 0), w("a", 1)}},
		{"a‐b", []string{w("a‐b", 0)}},
	}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeWord, base.Word()) })
}

// TestTextNumberStreams: TextNumber reads grouped and plain integers,
// fractions and percent signs with the separators it is given, checks that a
// group of three digits is not followed by a digit, and ends at the end of
// input in each of its states.
func TestTextNumberStreams(t *testing.T) {
	n := func(text string, offset int) string { return token(lexTypeNumber, text, offset) }
	u := func(text string, offset int) string { return token(bytescan.TokenTypeUnknown, text, offset) }
	setup := func(sep, decimal rune) func(lx *bytescan.Scanner) {
		return func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeNumber, base.TextNumber(sep, decimal)) }
	}

	t.Run("comma and full stop", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"3,500.25", []string{n("3,500.25", 0)}},
			{"12.5%", []string{n("12.5%", 0)}},
			{"1500", []string{n("1500", 0)}},
			{"1,234,567", []string{n("1,234,567", 0)}},
			{"1,2345", []string{n("1", 0), u(",", 1), n("2345", 2)}},
			{"1,234,5678", []string{n("1,234", 0), u(",", 5), n("5678", 6)}},
			{"1234,567", []string{n("1234", 0), u(",", 4), n("567", 5)}},
			{"12,34", []string{n("12", 0), u(",", 2), n("34", 3)}},
			{"1,234x", []string{n("1,234", 0), u("x", 5)}},
			{"1,234%", []string{n("1,234%", 0)}},
			{"1,234.5,6", []string{n("1,234.5", 0), u(",", 7), n("6", 8)}},
			{"1.2.3", []string{n("1.2", 0), u(".", 3), n("3", 4)}},
			{"%5", []string{u("%", 0), n("5", 1)}},
			{"x", []string{u("x", 0)}},

			// The end of input in each state.
			{"7", []string{n("7", 0)}},
			{"1,", []string{n("1", 0), u(",", 1)}},
			{"1,2", []string{n("1", 0), u(",", 1), n("2", 2)}},
			{"1,23", []string{n("1", 0), u(",", 1), n("23", 2)}},
			{"1,234", []string{n("1,234", 0)}},
			{"1.", []string{n("1", 0), u(".", 1)}},
			{"1,234.", []string{n("1,234", 0), u(".", 5)}},
			{"1.5", []string{n("1.5", 0)}},
			{"5%", []string{n("5%", 0)}},
		}, setup(',', '.'))
	})

	t.Run("full stop and comma", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"3.500,25", []string{n("3.500,25", 0)}},
			{"3,5%", []string{n("3,5%", 0)}},
			{"3.50", []string{n("3", 0), u(".", 1), n("50", 2)}},
		}, setup('.', ','))
	})

	t.Run("narrow no-break space and comma", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"3\u202F500,25", []string{n("3\u202F500,25", 0)}},
			{"3 500", []string{n("3", 0), u(" ", 1), n("500", 2)}},
		}, setup('\u202F', ','))
	})
}

// TestTextNumberDigitsStreams: with TextDigits, TextNumber reads its form
// with the digits of the class in place of the ASCII digits, checks the group
// lookahead with that class, and reads no ASCII digit unless the class holds
// them.
func TestTextNumberDigitsStreams(t *testing.T) {
	n := func(text string, offset int) string { return token(lexTypeNumber, text, offset) }
	u := func(text string, offset int) string { return token(bytescan.TokenTypeUnknown, text, offset) }
	arabicIndic := core.RuneRange('٠', '٩')

	t.Run("Arabic-Indic digits and separators", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"١٬٢٣٤٫٥٠", []string{n("١٬٢٣٤٫٥٠", 0)}},
			{"١٢٣", []string{n("١٢٣", 0)}},
			{"٢٥%", []string{n("٢٥%", 0)}},
			{"١٬٢٣٤٥", []string{n("١", 0), u("٬", 1), n("٢٣٤٥", 2)}},
			{"١٬٢٣٤5", []string{n("١٬٢٣٤", 0), u("5", 5)}},
			{"12", []string{u("1", 0), u("2", 1)}},
			{"١٫", []string{n("١", 0), u("٫", 1)}},
			{"١٬", []string{n("١", 0), u("٬", 1)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeNumber, base.TextNumber('٬', '٫', base.TextDigits(arabicIndic)))
		})
	})

	t.Run("ASCII or Arabic-Indic digits", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"1,٢٣٤.5", []string{n("1,٢٣٤.5", 0)}},
			{"1,234٥", []string{n("1", 0), u(",", 1), n("234٥", 2)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeNumber, base.TextNumber(',', '.', base.TextDigits(core.ASCIIDigit.Or(arabicIndic))))
		})
	})

	t.Run("zero option", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"1,234.5", []string{n("1,234.5", 0)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeNumber, base.TextNumber(',', '.', base.TextNumberOption{}))
		})
	})
}

// TestScriptRunExtraStreams: an extra class continues a script run and never
// starts one, and several extra classes compose.
func TestScriptRunExtraStreams(t *testing.T) {
	k := func(text string, offset int) string { return token(lexTypeWord, text, offset) }
	u := func(text string, offset int) string { return token(bytescan.TokenTypeUnknown, text, offset) }

	t.Run("prolonged sound mark", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"コーヒー", []string{k("コーヒー", 0)}},
			{"タワーに", []string{k("タワー", 0), u("に", 3)}},
			{"ーコ", []string{u("ー", 0), k("コ", 1)}},
			{"カ\u3099ー", []string{k("カ\u3099ー", 0)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeWord, base.ScriptRun("Katakana", core.RuneSet("ー")))
		})
	})

	t.Run("two extra classes", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{"mp3 don't", []string{k("mp3", 0), u(" ", 3), k("don't", 4)}},
			{"3d", []string{u("3", 0), k("d", 1)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeWord, base.ScriptRun("Latin", core.ASCIIDigit, core.RuneSet("'")))
		})
	})
}

// TestTextRulesEmptyInput: on the empty input every rule gives io.EOF and no
// token.
func TestTextRulesEmptyInput(t *testing.T) {
	rules := map[string]bytescan.Rule{
		"Word":                       base.Word(),
		"TextNumber":                 base.TextNumber(',', '.'),
		"Punctuation":                base.Punctuation(),
		"PunctuationRun":             base.PunctuationRun(),
		"Symbol":                     base.Symbol(),
		"ScriptRun":                  base.ScriptRun("Han"),
		"TextNumber with TextDigits": base.TextNumber('٬', '٫', base.TextDigits(core.RuneRange('٠', '٩'))),
		"ScriptRun with extra":       base.ScriptRun("Katakana", core.RuneSet("ー")),
	}
	for name, rule := range rules {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, lexStream(t, "", func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeWord, rule) }))
		})
	}
}

// TestTextConstructorPanics: TextNumber, TextDigits and ScriptRun panic on an invalid
// argument when the rule is built.
func TestTextConstructorPanics(t *testing.T) {
	cases := map[string]func(){
		"TextNumber same separators":   func() { base.TextNumber(',', ',') },
		"TextNumber digit separator":   func() { base.TextNumber('1', '.') },
		"TextNumber digit decimal":     func() { base.TextNumber(',', '0') },
		"TextNumber percent decimal":   func() { base.TextNumber(',', '%') },
		"TextNumber percent separator": func() { base.TextNumber('%', '.') },
		"ScriptRun unknown script":     func() { base.ScriptRun("Klingon") },
		"TextDigits nil class":         func() { base.TextDigits(nil) },
		"TextNumber separator in digit class": func() {
			base.TextNumber('٬', '٫', base.TextDigits(core.RuneRange('٠', '٬')))
		},
		"TextNumber ASCII digit separator with digit class": func() {
			base.TextNumber('1', '٫', base.TextDigits(core.RuneRange('٠', '٩')))
		},
		"TextNumber percent in digit class": func() {
			base.TextNumber(',', '.', base.TextDigits(core.ASCIIDigit.Or(core.RuneSet("%"))))
		},
		"ScriptRun nil extra class": func() { base.ScriptRun("Han", core.UnicodeMark, nil) },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, build)
		})
	}
}

// BenchmarkWord measures Word on one long word of plain letters, of letters
// and NFD marks, and of parts joined by hyphens, and core.RuneRun of letters
// on the plain letters as a baseline. The time per rune stays about the same
// as the word grows 16 times, so the cost is linear in the token length.
func BenchmarkWord(b *testing.B) {
	cases := []struct {
		name string
		unit string
		rule func() bytescan.Rule
	}{
		{"baseline", "a", func() bytescan.Rule { return core.RuneRun(core.UnicodeLetter) }},
		{"letters", "a", base.Word},
		{"marks", nfdEAcute, base.Word},
		{"joiners", "ab-", base.Word},
	}
	for _, bc := range cases {
		for _, size := range []int{1 << 10, 1 << 14} {
			input := strings.Repeat(bc.unit, size/len([]rune(bc.unit))) + "x"
			runes := len([]rune(input))
			b.Run(fmt.Sprintf("%s/%d", bc.name, runes), func(b *testing.B) {
				rule := bc.rule()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					lx := bytescan.New(strings.NewReader(input))
					lx.MustAddRule(lexTypeWord, rule)
					lex, err := lx.Next()
					if err != nil || len([]rune(lex.Text())) != runes {
						b.Fatalf("got %v, %v", lex, err)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(runes), "ns/rune")
			})
		}
	}
}
