package core_test

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// TestKeywords: Keywords, KeywordsIgnoreCase and their WithGuard variants
// match the longest word that the guard does not follow. The cases end the
// input in each scanning state: inside a word, after a word, and after a word
// that a longer word continues.
func TestKeywords(t *testing.T) {
	const lexTypeKeyword = bytescan.TokenType("KEYWORD")
	kw := func(text string, offset int) string { return token(lexTypeKeyword, text, offset) }
	unknown := bytescan.TokenTypeUnknown

	t.Run("alone", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "else", Expected: []string{kw("else", 0)}},
			{Input: "elseif", Expected: []string{kw("elseif", 0)}},
			{Input: "elseif(", Expected: append([]string{kw("elseif", 0)}, unknowns("(", 6)...)},
			{Input: "else if", Expected: []string{kw("else", 0), token(unknown, " ", 4), kw("if", 5)}},
			{Input: "elsei", Expected: unknowns("elsei", 0)},
			{Input: "elseifx", Expected: unknowns("elseifx", 0)},
			{Input: "elsex", Expected: unknowns("elsex", 0)},
			{Input: "e", Expected: unknowns("e", 0)},
			{Input: "els", Expected: unknowns("els", 0)},
			{Input: "ELSE", Expected: unknowns("ELSE", 0)},
			{Input: "", Expected: nil},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.Keywords("if", "else", "elseif"))
		})
	})

	// A longer word continues a shorter one with a rune outside the guard:
	// the shorter word stays the match when the longer one does not
	// complete, with a pushback chain over the runes after it.
	t.Run("fallback", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "end", Expected: []string{kw("end", 0)}},
			{Input: "end-", Expected: append([]string{kw("end", 0)}, unknowns("-", 3)...)},
			{Input: "end-i", Expected: append([]string{kw("end", 0)}, unknowns("-i", 3)...)},
			{Input: "end-if", Expected: []string{kw("end-if", 0)}},
			{Input: "end-ifx", Expected: append([]string{kw("end", 0)}, unknowns("-ifx", 3)...)},
			{Input: "end-if-", Expected: append([]string{kw("end-if", 0)}, unknowns("-", 6)...)},
			{Input: "end-i-x", Expected: append([]string{kw("end", 0)}, unknowns("-i-x", 3)...)},
			{Input: "endx", Expected: unknowns("endx", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.Keywords("end", "end-if"))
		})
	})

	// Beside an identifier rule, a keyword wins a tie by registration order
	// and a longer identifier wins by length.
	t.Run("beside an identifier", func(t *testing.T) {
		const lexTypeName = bytescan.TokenType("NAME")
		runStreamCases(t, []streamCase{
			{Input: "if", Expected: []string{kw("if", 0)}},
			{Input: "iffy", Expected: []string{token(lexTypeName, "iffy", 0)}},
			{Input: "else_", Expected: []string{token(lexTypeName, "else_", 0)}},
			{Input: "if x", Expected: []string{kw("if", 0), token(unknown, " ", 2), token(lexTypeName, "x", 3)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.Keywords("if", "else", "elseif"))
			lx.MustAddRule(lexTypeName, core.Sequence(core.Rune(core.ASCIIIdentifierStart), core.ZeroOrMore(core.Rune(core.ASCIIWord))))
		})
	})

	t.Run("ignore case", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "select", Expected: []string{kw("select", 0)}},
			{Input: "SeLeCt", Expected: []string{kw("SeLeCt", 0)}},
			{Input: "ſELECT", Expected: []string{kw("ſELECT", 0)}},
			{Input: "Order", Expected: []string{kw("Order", 0)}},
			{Input: "OR(", Expected: append([]string{kw("OR", 0)}, unknowns("(", 2)...)},
			{Input: "orders", Expected: unknowns("orders", 0)},
			{Input: "SELECTION", Expected: unknowns("SELECTION", 0)},
			{Input: "sel", Expected: unknowns("sel", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.KeywordsIgnoreCase("SELECT", "or", "Order", "select"))
		})
	})

	// The guard reads the rune as it is: U+212A (KELVIN SIGN) folds to 'k'
	// but is not in ASCIIWord, so it ends "is" and continues "isk".
	t.Run("ignore case guard unfolded", func(t *testing.T) {
		const kelvin = "\u212a"
		runStreamCases(t, []streamCase{
			{Input: "is" + kelvin, Expected: []string{kw("is"+kelvin, 0)}},
			{Input: "is" + kelvin + "1", Expected: append([]string{kw("is", 0)}, unknowns(kelvin+"1", 2)...)},
			{Input: "isk1", Expected: unknowns("isk1", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.KeywordsIgnoreCase("is", "isk"))
		})
	})

	t.Run("with guard", func(t *testing.T) {
		ident := core.UnicodeLetter.Or(core.UnicodeNumber).Or(core.RuneSet("_"))
		runStreamCases(t, []streamCase{
			{Input: "función", Expected: unknowns("función", 0)},
			{Input: "fn ", Expected: append([]string{kw("fn", 0)}, unknowns(" ", 2)...)},
			{Input: "fnñ", Expected: unknowns("fnñ", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.KeywordsWithGuard(ident, "fn", "func"))
		})
		runStreamCases(t, []streamCase{
			{Input: "ŚELECT", Expected: []string{kw("ŚELECT", 0)}},
			{Input: "selectñ", Expected: unknowns("selectñ", 0)},
			{Input: "SELECT1", Expected: unknowns("SELECT1", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.KeywordsIgnoreCaseWithGuard(ident, "śelect"))
		})
	})

	// Composed, the set is its node: the words not followed by one rune of
	// the guard.
	t.Run("composed", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "if(", Expected: []string{kw("if(", 0)}},
			{Input: "ELSEIF(", Expected: []string{kw("ELSEIF(", 0)}},
			{Input: "elsif(", Expected: append(unknowns("els", 0), kw("if(", 3))},
			{Input: "ifx(", Expected: unknowns("ifx(", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.Sequence(core.KeywordsIgnoreCase("if", "else", "elseif"), core.Literal("(")))
		})
	})
}

// TestAnyLiteralIgnoreCase: the alternation of the literals under simple case
// folding, with the longest match, registered alone and composed.
func TestAnyLiteralIgnoreCase(t *testing.T) {
	const lexTypeOp = bytescan.TokenType("OP")
	op := func(text string, offset int) string { return token(lexTypeOp, text, offset) }

	rule := core.AnyLiteralIgnoreCase("and", "AND_ALSO", "or", "k")
	runStreamCases(t, []streamCase{
		{Input: "AnD", Expected: []string{op("AnD", 0)}},
		{Input: "and_also", Expected: []string{op("and_also", 0)}},
		{Input: "and_al", Expected: append([]string{op("and", 0)}, unknowns("_al", 3)...)},
		{Input: "Andx", Expected: append([]string{op("And", 0)}, unknowns("x", 3)...)},
		{Input: "K", Expected: []string{op("K", 0)}},
		{Input: "OR", Expected: []string{op("OR", 0)}},
		{Input: "ß", Expected: unknowns("ß", 0)},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeOp, rule)
	})
	runStreamCases(t, []streamCase{
		{Input: "OR1", Expected: []string{op("OR1", 0)}},
		{Input: "AND_ALSO1", Expected: []string{op("AND_ALSO1", 0)}},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeOp, core.Sequence(rule, core.Rune(core.ASCIIDigit)))
	})
}

// TestKeywordsMatchKeywordChoice: on every input over a small alphabet, a
// keyword set gives the same token stream as the idiom it replaces, both
// registered alone and composed in a Sequence: Keywords is
// Choice(Keyword(w, ASCIIWord), ...), and KeywordsIgnoreCaseWithGuard is
// NotFollowedBy(Choice(LiteralIgnoreCase(w), ...), Rune(c)). The words
// include a prefix of another word, followed by a guard rune ("a", "ab") and
// by a rune outside the guard ("a", "a-b").
func TestKeywordsMatchKeywordChoice(t *testing.T) {
	words := []string{"a", "ab", "a-b", "ba", "b-"}
	guard := core.RuneSet("ab")

	choice := func(c core.Class) bytescan.Rule {
		rs := make([]bytescan.Rule, len(words))
		for i, w := range words {
			rs[i] = core.Keyword(w, c)
		}
		return core.Choice(rs...)
	}
	choiceFold := func(c core.Class) bytescan.Rule {
		rs := make([]bytescan.Rule, len(words))
		for i, w := range words {
			rs[i] = core.LiteralIgnoreCase(w)
		}
		return core.NotFollowedBy(core.Choice(rs...), core.Rune(c))
	}

	pairs := []struct {
		name      string
		got, want bytescan.Rule
	}{
		{"Keywords", core.Keywords(words...), choice(core.ASCIIWord)},
		{"KeywordsWithGuard", core.KeywordsWithGuard(guard, words...), choice(guard)},
		{"KeywordsIgnoreCase", core.KeywordsIgnoreCase(words...), choiceFold(core.ASCIIWord)},
		{"KeywordsIgnoreCaseWithGuard", core.KeywordsIgnoreCaseWithGuard(guard, words...), choiceFold(guard)},
	}

	const alphabet = "abAB- "
	var inputs []string
	var grow func(prefix string)
	grow = func(prefix string) {
		if prefix != "" {
			inputs = append(inputs, prefix)
		}
		if len(prefix) == 5 {
			return
		}
		for _, r := range alphabet {
			grow(prefix + string(r))
		}
	}
	grow("")

	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			got, want := p.got, p.want
			gotSeq, wantSeq := core.Sequence(got, core.Literal(" ")), core.Sequence(want, core.Literal(" "))
			for _, in := range inputs {
				require.Equal(t,
					lexStream(t, in, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, want) }),
					lexStream(t, in, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, got) }),
					"alone, on %q", in)
				require.Equal(t,
					lexStream(t, in, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, wantSeq) }),
					lexStream(t, in, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, gotSeq) }),
					"composed, on %q", in)
			}
		})
	}
}

// keywordSet50 is a set of 50 keywords, most of them SQL reserved words, for
// TestKeywordsCost.
var keywordSet50 = []string{
	"all", "alter", "and", "any", "as", "between", "by", "case", "cast", "check",
	"create", "cross", "default", "delete", "distinct", "drop", "else", "end", "except", "exists",
	"false", "fetch", "for", "foreign", "from", "full", "group", "having", "in", "inner",
	"insert", "into", "is", "join", "left", "like", "not", "null", "on", "or",
	"order", "outer", "right", "select", "set", "table", "then", "union", "where", "with",
}

// TestKeywordsCost: a keyword set of 50 words costs no more to build and no
// more allocations per token than each idiom it replaces: a Choice of
// Keyword (the Lua, Ruby and TypeScript examples), NotFollowedBy over a
// Choice of LiteralIgnoreCase (the SQL example), and NotFollowedBy over
// AnyLiteral, the shortest composition from the other primitives.
func TestKeywordsCost(t *testing.T) {
	require.Len(t, keywordSet50, 50)
	guard := core.Rune(core.ASCIIWord)

	keywordChoice := func() bytescan.Rule {
		rs := make([]bytescan.Rule, len(keywordSet50))
		for i, w := range keywordSet50 {
			rs[i] = core.Keyword(w, core.ASCIIWord)
		}
		return core.Choice(rs...)
	}
	foldChoice := func() bytescan.Rule {
		rs := make([]bytescan.Rule, len(keywordSet50))
		for i, w := range keywordSet50 {
			rs[i] = core.LiteralIgnoreCase(w)
		}
		return core.NotFollowedBy(core.Choice(rs...), guard)
	}

	type idiom struct {
		name  string
		build func() bytescan.Rule
	}
	cases := []struct {
		name   string
		build  func() bytescan.Rule
		idioms []idiom
		cycle  string
	}{
		{
			name:  "Keywords",
			build: func() bytescan.Rule { return core.Keywords(keywordSet50...) },
			idioms: []idiom{
				{"Choice(Keyword...)", keywordChoice},
				{"NotFollowedBy(AnyLiteral)", func() bytescan.Rule {
					return core.NotFollowedBy(core.AnyLiteral(keywordSet50...), guard)
				}},
			},
			cycle: "select from where order or in into ",
		},
		{
			name:  "KeywordsIgnoreCase",
			build: func() bytescan.Rule { return core.KeywordsIgnoreCase(keywordSet50...) },
			idioms: []idiom{
				{"NotFollowedBy(Choice(LiteralIgnoreCase...))", foldChoice},
			},
			cycle: "SELECT From where ORDER or In INTO ",
		},
	}

	perToken := func(build func() bytescan.Rule, cycle string) float64 {
		tokens := 2 * strings.Count(cycle, " ")
		lx := bytescan.New(bufio.NewReader(&repeatingInput{pat: []byte(cycle)}))
		lx.MustAddRule("KEYWORD", build())
		lx.MustAddRule("SPACE", handSpace)
		next := func() {
			for i := 0; i < tokens; i++ {
				l, err := lx.Next()
				if err != nil {
					t.Fatalf("Next: %v", err)
				}
				if l.Type() == bytescan.TokenTypeUnknown {
					t.Fatalf("UNKNOWN token %q", l.Text())
				}
			}
		}
		for i := 0; i < 200; i++ {
			next()
		}
		return testing.AllocsPerRun(200, next) / float64(tokens)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allocs, bytes := buildCost(tc.build, 20)
			tokenAllocs := perToken(tc.build, tc.cycle)
			t.Logf("%s: build %.0f allocs, %d bytes; %.2f allocations per token (race detector: %v)",
				tc.name, allocs, bytes, tokenAllocs, raceDetector)
			for _, id := range tc.idioms {
				idAllocs, idBytes := buildCost(id.build, 20)
				idToken := perToken(id.build, tc.cycle)
				t.Logf("%s: build %.0f allocs, %d bytes; %.2f allocations per token", id.name, idAllocs, idBytes, idToken)
				assert.LessOrEqual(t, allocs, idAllocs, "build allocations against %s", id.name)
				assert.LessOrEqual(t, bytes, idBytes, "build bytes against %s", id.name)
				if !raceDetector {
					assert.LessOrEqual(t, tokenAllocs, idToken, "allocations per token against %s", id.name)
				}
			}
			if !raceDetector {
				// One allocation per token: the Token itself.
				assert.Equal(t, 1.0, tokenAllocs, "allocations per token")
			}
		})
	}
}
