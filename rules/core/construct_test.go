package core_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// TestConstructorPanics: a constructor rejects an invalid argument at
// construction time, before any scanner runs the rule.
func TestConstructorPanics(t *testing.T) {
	cases := map[string]func(){
		"Literal empty":            func() { core.Literal("") },
		"LiteralIgnoreCase empty":  func() { core.LiteralIgnoreCase("") },
		"AnyLiteral none":          func() { core.AnyLiteral() },
		"AnyLiteral empty literal": func() { core.AnyLiteral("a", "") },
		"Rune nil":                 func() { core.Rune(nil) },
		"RuneRun nil":              func() { core.RuneRun(nil) },
		"RuneRunMax nil":           func() { core.RuneRunMax(nil, 3) },
		"RuneRunMax n 0":           func() { core.RuneRunMax(core.ASCIIDigit, 0) },
		"RuneRunMax n -1":          func() { core.RuneRunMax(core.ASCIIDigit, -1) },
		"AtLineStart nil":          func() { core.AtLineStart(nil) },
		"Repeat nil":               func() { core.Repeat(nil, 1, 1) },
		"Repeat max below min":     func() { core.Repeat(core.Rune(core.ASCIIDigit), 3, 2) },
		"Keyword empty":            func() { core.Keyword("", core.ASCIIWord) },
		"Keyword nil":              func() { core.Keyword("for", nil) },
		"Keywords none":            func() { core.Keywords() },
		"Keywords empty word":      func() { core.Keywords("if", "") },
		"KeywordsWithGuard nil":    func() { core.KeywordsWithGuard(nil, "if") },
		"KeywordsWithGuard none":   func() { core.KeywordsWithGuard(core.ASCIIWord) },
		"KeywordsIgnoreCase none":  func() { core.KeywordsIgnoreCase() },
		"KeywordsIgnoreCase empty": func() { core.KeywordsIgnoreCase("") },
		"KeywordsIgnoreCaseWithGuard nil": func() {
			core.KeywordsIgnoreCaseWithGuard(nil, "if")
		},
		"KeywordsIgnoreCaseWithGuard empty": func() {
			core.KeywordsIgnoreCaseWithGuard(core.ASCIIWord, "if", "")
		},
		"AnyLiteralIgnoreCase none":    func() { core.AnyLiteralIgnoreCase() },
		"AnyLiteralIgnoreCase empty":   func() { core.AnyLiteralIgnoreCase("a", "") },
		"RuneSet empty":                func() { core.RuneSet("") },
		"RuneRange hi below lo":        func() { core.RuneRange('b', 'a') },
		"UnicodeTables none":           func() { core.UnicodeTables() },
		"UnicodeTables nil table":      func() { core.UnicodeTables(nil) },
		"UnicodeScript unknown":        func() { core.UnicodeScript("Klingon") },
		"Not nil":                      func() { core.Class(nil).Not() },
		"Or nil":                       func() { core.ASCIIDigit.Or(nil) },
		"And nil":                      func() { core.Class(nil).And(core.ASCIIDigit) },
		"PushBack n 0":                 func() { core.PushBack(0, bytescan.StateAccept) },
		"PushBack continue":            func() { core.PushBack(1, bytescan.StateContinue) },
		"PushBack pushback":            func() { core.PushBack(1, bytescan.StatePushBack) },
		"Sequence none":                func() { core.Sequence() },
		"Sequence nil":                 func() { core.Sequence(core.Literal("a"), nil) },
		"Choice none":                  func() { core.Choice() },
		"Choice nil":                   func() { core.Choice(nil) },
		"Optional nil":                 func() { core.Optional(nil) },
		"ZeroOrMore nil":               func() { core.ZeroOrMore(nil) },
		"OneOrMore nil":                func() { core.OneOrMore(nil) },
		"Repeat rule nil":              func() { core.Repeat(nil, 1, 2) },
		"Repeat min -1":                func() { core.Repeat(core.Literal("a"), -1, 2) },
		"Repeat max 0":                 func() { core.Repeat(core.Literal("a"), 0, 0) },
		"Repeat literal max below min": func() { core.Repeat(core.Literal("a"), 3, 2) },
		"Repeat min 1001":              func() { core.Repeat(core.Literal("a"), 1001, -1) },
		"Repeat max 1001":              func() { core.Repeat(core.Literal("a"), 1, 1001) },
		"Repeat too large":             func() { core.Repeat(core.Repeat(core.Literal("a"), 1000, 1000), 1000, 1000) },
		"FollowedBy nil rule":          func() { core.FollowedBy(nil, core.Literal("a")) },
		"FollowedBy nil ahead":         func() { core.FollowedBy(core.Literal("a"), nil) },
		"NotFollowedBy nil rule":       func() { core.NotFollowedBy(nil, core.Literal("a")) },
		"NotFollowedBy nil":            func() { core.NotFollowedBy(core.Literal("a"), nil) },
	}
	for name, construct := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, construct)
		})
	}
}

// TestRepeatAcceptsBoundaryCounts: the counts that the Repeat contract allows
// build a rule, including min 0, an unbounded maximum and counts of 1000.
func TestRepeatAcceptsBoundaryCounts(t *testing.T) {
	digit := core.Rune(core.ASCIIDigit)
	for _, c := range []struct{ min, max int }{{0, 3}, {1, -1}, {0, -1}, {1000, 1000}, {1, 1000}} {
		assert.NotPanics(t, func() { core.Repeat(digit, c.min, c.max) }, "Repeat(min %d, max %d)", c.min, c.max)
	}
}

// TestRuleReuse: a rule keeps its state only in the Rule values it returns,
// so one Rule value of each primitive gives the same stream for every token,
// in several scanners at once.
func TestRuleReuse(t *testing.T) {
	const (
		lexTypeKeyword = bytescan.TokenType("KEYWORD")
		lexTypeOp      = bytescan.TokenType("OP")
		lexTypeNumber  = bytescan.TokenType("NUMBER")
		lexTypeHex     = bytescan.TokenType("HEX")
		lexTypeWord    = bytescan.TokenType("WORD")
		lexTypeSpace   = bytescan.TokenType("SPACE")
		lexTypeRune    = bytescan.TokenType("RUNE")
	)

	keyword := core.Keyword("select", core.ASCIIWord)
	op := core.AnyLiteral("<", "<=", "<>")
	number := core.Repeat(core.Rune(core.ASCIIDigit), 1, 3)
	hex := core.Literal("0x")
	word := core.RuneRun(core.ASCIIWord)
	space := core.RuneRun(core.ASCIISpace)
	other := core.Rune(core.RuneSet(";"))

	setup := func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeKeyword, keyword)
		lx.MustAddRule(lexTypeOp, op)
		lx.MustAddRule(lexTypeNumber, number)
		lx.MustAddRule(lexTypeHex, hex)
		lx.MustAddRule(lexTypeWord, word)
		lx.MustAddRule(lexTypeSpace, space)
		lx.MustAddRule(lexTypeRune, other)
	}

	// One pass over the input checks the stream; the same Rule values then
	// lex it again, in several scanners at once.
	once := "select a <= 1234 <> 0x select_b < 7;"
	require.Equal(t, []string{
		token(lexTypeKeyword, "select", 0),
		token(lexTypeSpace, " ", 6),
		token(lexTypeWord, "a", 7),
		token(lexTypeSpace, " ", 8),
		token(lexTypeOp, "<=", 9),
		token(lexTypeSpace, " ", 11),
		token(lexTypeWord, "1234", 12),
		token(lexTypeSpace, " ", 16),
		token(lexTypeOp, "<>", 17),
		token(lexTypeSpace, " ", 19),
		token(lexTypeHex, "0x", 20),
		token(lexTypeSpace, " ", 22),
		token(lexTypeWord, "select_b", 23),
		token(lexTypeSpace, " ", 31),
		token(lexTypeOp, "<", 32),
		token(lexTypeSpace, " ", 33),
		token(lexTypeNumber, "7", 34),
		token(lexTypeRune, ";", 35),
	}, lexStream(t, once, setup))

	input := strings.Repeat(once, 3)
	want, err := collectStream(input, setup)
	require.NoError(t, err)

	var wg sync.WaitGroup
	streams := make([][]string, 8)
	errs := make([]error, len(streams))
	for i := range streams {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			streams[i], errs[i] = collectStream(input, setup)
		}(i)
	}
	wg.Wait()
	for i, got := range streams {
		require.NoError(t, errs[i], "scanner %d", i)
		assert.Equal(t, want, got, "scanner %d", i)
	}
}
