package core_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// The tests in this file give complete token streams for the combinators:
// lookahead, which Go's regexp cannot check, composition with hand-written
// rules, and reuse. The regexp oracle tests of the combinators are in
// tests/core_combinator_oracle_test.go, because no file under rules/ imports
// regexp.

// newForKeywordRule returns a hand-written rule for for(?!\w), in Perl
// notation. After "for" it decides by one symbol of lookahead: it excludes
// that symbol and accepts "for", or it rejects when the symbol is a word rune.
// At end of input before the end of "for" it rejects the EOF symbol, so it
// matches nothing; after "for" it excludes the EOF symbol and accepts.
func newForKeywordRule() bytescan.Rule {
	return forAt(0)
}

// forAt is the state of newForKeywordRule that reads rune i of "for". After
// the last rune it hands over to forEnd.
func forAt(i int) bytescan.Rule {
	const lit = "for"
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == rune(lit[i]) {
			if i == len(lit)-1 {
				return forEnd, bytescan.StateContinue
			}
			return forAt(i + 1), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// forEnd is the state of newForKeywordRule after "for".
func forEnd(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.IsEOF() {
		return core.PushBack(1, bytescan.StateAccept) // the EOF symbol
	}
	if core.ASCIIWord(s.Rune()) {
		return nil, bytescan.StateReject
	}
	return core.PushBack(1, bytescan.StateAccept) // the symbol after "for"
}

// newABCDBeforeEFOrABCDEFGHRule returns a hand-written rule for
// abcd(?=ef)|abcdefgh, in Perl notation, with delayed acceptance: after
// "abcdef" it continues while "abcdefgh" can match, and otherwise excludes
// the runes after "abcd" with one pushback chain. At end of input after
// "abcdef" it excludes those runes and the EOF symbol, and accepts "abcd".
func newABCDBeforeEFOrABCDEFGHRule() bytescan.Rule {
	return abcdefghAt(0)
}

// abcdefghAt is the state of newABCDBeforeEFOrABCDEFGHRule that reads rune i
// of "abcdefgh". From i = 6 on, "abcd" is validated by "ef".
func abcdefghAt(i int) bytescan.Rule {
	const long = "abcdefgh"
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			if i >= 6 {
				return core.PushBack(i+1-4, bytescan.StateAccept) // the runes after "abcd", and the EOF symbol
			}
			return nil, bytescan.StateReject
		}
		if s.Rune() == rune(long[i]) {
			if i == len(long)-1 {
				return nil, bytescan.StateAccept
			}
			return abcdefghAt(i + 1), bytescan.StateContinue
		}
		if i >= 6 {
			return core.PushBack(i+1-4, bytescan.StateAccept) // the runes after "abcd", and the symbol just read
		}
		return nil, bytescan.StateReject
	}
}

// TestFollowedBy: an end of the rule counts only when the lookahead matches
// from it, and the match excludes the lookahead's text. The end of input
// never completes a positive lookahead.
func TestFollowedBy(t *testing.T) {
	r := core.FollowedBy(core.RuneRun(core.ASCIIDigit), core.Literal("px"))
	runStreamCases(t, []streamCase{
		{Input: "12px", Expected: append([]string{token(lexTypeR, "12", 0)}, unknowns("px", 2)...)},
		{Input: "1px2px", Expected: []string{
			token(lexTypeR, "1", 0), token(bytescan.TokenTypeUnknown, "p", 1), token(bytescan.TokenTypeUnknown, "x", 2),
			token(lexTypeR, "2", 3), token(bytescan.TokenTypeUnknown, "p", 4), token(bytescan.TokenTypeUnknown, "x", 5),
		}},
		{Input: "12p", Expected: unknowns("12p", 0)},
		{Input: "12", Expected: unknowns("12", 0)},
		{Input: "px", Expected: unknowns("px", 0)},
		{Input: "", Expected: nil},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, r)
	})
}

// TestNotFollowedBy: an end of the rule counts only when the lookahead does
// not match from it. The longest end that qualifies wins, so on "12." the
// end after "1" counts. The end of input stops every pending lookahead.
func TestNotFollowedBy(t *testing.T) {
	r := core.NotFollowedBy(core.RuneRun(core.ASCIIDigit), core.Literal("."))
	runStreamCases(t, []streamCase{
		{Input: "12", Expected: []string{token(lexTypeR, "12", 0)}},
		{Input: "12 ", Expected: []string{token(lexTypeR, "12", 0), token(bytescan.TokenTypeUnknown, " ", 2)}},
		{Input: "12.", Expected: []string{
			token(lexTypeR, "1", 0), token(bytescan.TokenTypeUnknown, "2", 1), token(bytescan.TokenTypeUnknown, ".", 2),
		}},
		{Input: ".", Expected: unknowns(".", 0)},
		{Input: "", Expected: nil},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, r)
	})

	// NotFollowedBy(Literal(s), Rune(c)) gives the streams of Keyword(s, c),
	// also beside a word-run rule.
	t.Run("as Keyword", func(t *testing.T) {
		const lexTypeWord = bytescan.TokenType("WORD")
		inputs := []string{"for", "for(", "for for", "format", "for_1", "f", "fo", "xfor", "for x", ""}
		for _, input := range inputs {
			want := lexStream(t, input, func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeR, core.Keyword("for", core.ASCIIWord))
				lx.MustAddRule(lexTypeWord, core.RuneRun(core.ASCIIWord))
			})
			got := lexStream(t, input, func(lx *bytescan.Scanner) {
				lx.MustAddRule(lexTypeR, core.NotFollowedBy(core.Literal("for"), core.Rune(core.ASCIIWord)))
				lx.MustAddRule(lexTypeWord, core.RuneRun(core.ASCIIWord))
			})
			assert.Equal(t, want, got, "input %q", input)
		}
	})
}

// TestNestedLookahead: a negative lookahead decides only after every negative
// lookahead inside it has decided. In b(?!a?(?!a)) the inner a?(?!a)
// matches the empty text before a rune other than 'a', or "a" before one, so
// b matches only before "aa".
func TestNestedLookahead(t *testing.T) {
	a := core.Literal("a")
	r := core.NotFollowedBy(core.Literal("b"), core.NotFollowedBy(core.Optional(a), a))
	runStreamCases(t, []streamCase{
		{Input: "b", Expected: unknowns("b", 0)},
		{Input: "bx", Expected: unknowns("bx", 0)},
		{Input: "ba", Expected: unknowns("ba", 0)},
		{Input: "baa", Expected: append([]string{token(lexTypeR, "b", 0)}, unknowns("aa", 1)...)},
	}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })

	// b(?!a(?!x)): the inner lookahead decides at the end of input.
	r = core.NotFollowedBy(core.Literal("b"), core.NotFollowedBy(a, core.Literal("x")))
	runStreamCases(t, []streamCase{
		{Input: "b", Expected: []string{token(lexTypeR, "b", 0)}},
		{Input: "ba", Expected: unknowns("ba", 0)},
		{Input: "bax", Expected: append([]string{token(lexTypeR, "b", 0)}, unknowns("ax", 1)...)},
	}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
}

// TestNullableRepetitionWithLookahead: a repetition of a part that can match
// the empty text ends even when a lookahead inside the part decides after the
// part's other runs. (?:a?(?=a?(?!a)))* matches only non-empty text before a
// rune other than 'a'. Each case must finish within a bound, so a regression
// fails instead of hanging the suite.
func TestNullableRepetitionWithLookahead(t *testing.T) {
	a := core.Literal("a")
	part := core.FollowedBy(core.Optional(a), core.NotFollowedBy(core.Optional(a), a))
	cases := []streamCase{
		{Input: "x", Expected: unknowns("x", 0)},
		{Input: "a", Expected: []string{token(lexTypeR, "a", 0)}},
		{Input: "ax", Expected: append([]string{token(lexTypeR, "a", 0)}, unknowns("x", 1)...)},
		{Input: "aa", Expected: []string{token(lexTypeR, "aa", 0)}},
		{Input: "", Expected: nil},
	}
	rules := map[string]bytescan.Rule{
		"ZeroOrMore": core.ZeroOrMore(part),
		"OneOrMore":  core.OneOrMore(part),
		"Repeat":     core.Repeat(part, 0, -1),
	}
	for name, r := range rules {
		t.Run(name, func(t *testing.T) {
			for _, tc := range cases {
				done := make(chan []string, 1)
				go func() {
					stream, _ := collectStream(tc.Input, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
					done <- stream
				}()
				select {
				case got := <-done:
					assert.Equal(t, tc.Expected, got, "input %q", tc.Input)
				case <-time.After(5 * time.Second):
					require.FailNowf(t, "unexpected test state", "input %q: lexing did not finish", tc.Input)
				}
			}
		})
	}
}

// TestLookaheadDelayedAcceptance: abcd(?=ef)|abcdefgh built from combinators
// gives the streams of the hand-written delayed-acceptance rule that
// "Lookahead combined with a longer match" in docs/specs/rule-vm-contract.md
// pins: the composed rule settles with one terminal pushback chain.
func TestLookaheadDelayedAcceptance(t *testing.T) {
	matchABCD := func(offset int) string { return token(lexTypeR, "abcd", offset) }
	cases := []streamCase{
		{Input: "abcdefgh", Expected: []string{token(lexTypeR, "abcdefgh", 0)}},
		{Input: "abcdefxx", Expected: append([]string{matchABCD(0)}, unknowns("efxx", 4)...)},
		{Input: "abcdef", Expected: append([]string{matchABCD(0)}, unknowns("ef", 4)...)},
		{Input: "abcdefg", Expected: append([]string{matchABCD(0)}, unknowns("efg", 4)...)},
		{Input: "abcdefgX", Expected: append([]string{matchABCD(0)}, unknowns("efgX", 4)...)},
		{Input: "abcde", Expected: unknowns("abcde", 0)},
		{Input: "abcdX", Expected: unknowns("abcdX", 0)},
		{Input: "abcdefghabcdef", Expected: append([]string{token(lexTypeR, "abcdefgh", 0), matchABCD(8)}, unknowns("ef", 12)...)},
	}

	t.Run("combinators", func(t *testing.T) {
		r := core.Choice(core.FollowedBy(core.Literal("abcd"), core.Literal("ef")), core.Literal("abcdefgh"))
		runStreamCases(t, cases, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
	})
	t.Run("hand-written", func(t *testing.T) {
		runStreamCases(t, cases, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newABCDBeforeEFOrABCDEFGHRule())
		})
	})
}

// TestSequenceWithPushBackChain: a hand-written rule that ends in a pushback chain
// composes inside Sequence. The next rule starts where the chain ends and reads
// the excluded symbols again; the VM sees only the composed rule's verdicts.
func TestSequenceWithPushBackChain(t *testing.T) {
	t.Run(`for(?!\w)\(?`, func(t *testing.T) {
		r := core.Sequence(newForKeywordRule(), core.Optional(core.Literal("(")))
		runStreamCases(t, []streamCase{
			{Input: "for(", Expected: []string{token(lexTypeR, "for(", 0)}},
			{Input: "for", Expected: []string{token(lexTypeR, "for", 0)}},
			{Input: "for x", Expected: append([]string{token(lexTypeR, "for", 0)}, unknowns(" x", 3)...)},
			{Input: "for((", Expected: append([]string{token(lexTypeR, "for(", 0)}, unknowns("(", 4)...)},
			{Input: "format", Expected: unknowns("format", 0)},
			{Input: "fo", Expected: unknowns("fo", 0)},
		}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
	})

	t.Run(`for(?!\w)\s+x`, func(t *testing.T) {
		r := core.Sequence(newForKeywordRule(), core.RuneRun(core.ASCIISpace), core.Literal("x"))
		runStreamCases(t, []streamCase{
			{Input: "for  x", Expected: []string{token(lexTypeR, "for  x", 0)}},
			{Input: "for x!", Expected: append([]string{token(lexTypeR, "for x", 0)}, unknowns("!", 5)...)},
			{Input: "for  ", Expected: unknowns("for  ", 0)},
			{Input: "forx", Expected: unknowns("forx", 0)},
		}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
	})
}

// TestOpaqueRuleIsAtomic: a hand-written rule composes through the ends that
// the VM would record for it, the accepts that grow its match. On "abcdefgh"
// the hand-written abcd(?=ef)|abcdefgh accepts all eight runes and never
// reports "abcd", so Sequence with RuneRun(ASCIILower) after it finds no match there. The
// same alternation built from combinators reports both ends.
func TestOpaqueRuleIsAtomic(t *testing.T) {
	t.Run("hand-written", func(t *testing.T) {
		r := core.Sequence(newABCDBeforeEFOrABCDEFGHRule(), core.RuneRun(core.ASCIILower))
		runStreamCases(t, []streamCase{
			{Input: "abcdefgh", Expected: unknowns("abcdefgh", 0)},
			{Input: "abcdefx", Expected: []string{token(lexTypeR, "abcdefx", 0)}},
		}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
	})
	t.Run("combinators", func(t *testing.T) {
		alt := core.Choice(core.FollowedBy(core.Literal("abcd"), core.Literal("ef")), core.Literal("abcdefgh"))
		r := core.Sequence(alt, core.RuneRun(core.ASCIILower))
		runStreamCases(t, []streamCase{
			{Input: "abcdefgh", Expected: []string{token(lexTypeR, "abcdefgh", 0)}},
			{Input: "abcdefx", Expected: []string{token(lexTypeR, "abcdefx", 0)}},
		}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
	})
}

// TestWrappedCoreRuleIsOpaque: a hand-written rule that passes some symbols on
// to a core rule keeps its own behaviour inside a combinator. The handshake
// does not mistake it for the core rule it calls.
func TestWrappedCoreRuleIsOpaque(t *testing.T) {
	letters := core.RuneRun(core.ASCIILower)
	notX := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.Rune() == 'x' {
			return nil, bytescan.StateReject
		}
		return letters(s)
	}
	r := core.Sequence(notX, core.Literal("!"))
	runStreamCases(t, []streamCase{
		{Input: "ab!", Expected: []string{token(lexTypeR, "ab!", 0)}},
		{Input: "xb!", Expected: append(unknowns("x", 0), token(lexTypeR, "b!", 1))},
	}, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
}

// TestNullableRuleAlone: a composed rule that can match the empty text
// matches only non-empty text when it is registered alone, because a token
// is never empty.
func TestNullableRuleAlone(t *testing.T) {
	cases := []streamCase{
		{Input: "aab", Expected: append([]string{token(lexTypeR, "aa", 0)}, unknowns("b", 2)...)},
		{Input: "b", Expected: unknowns("b", 0)},
		{Input: "", Expected: nil},
	}
	rules := map[string]bytescan.Rule{
		"Optional":             core.Optional(core.RuneRun(core.RuneSet("a"))),
		"ZeroOrMore":           core.ZeroOrMore(core.Rune(core.RuneSet("a"))),
		"Repeat":               core.Repeat(core.Rune(core.RuneSet("a")), 0, 2),
		"ZeroOrMore(Optional)": core.ZeroOrMore(core.Optional(core.Rune(core.RuneSet("a")))),
	}
	for name, r := range rules {
		t.Run(name, func(t *testing.T) {
			runStreamCases(t, cases, func(lx *bytescan.Scanner) { lx.MustAddRule(lexTypeR, r) })
		})
	}
}

// TestCombinatorTies: a composed rule is one registered rule. Against another
// rule of the same length, registration order decides, as for any rule.
func TestCombinatorTies(t *testing.T) {
	const lexTypeIdent = bytescan.TokenType("IDENT")
	keyword := core.Choice(core.Literal("if"), core.Literal("for"))
	ident := core.Sequence(core.Rune(core.ASCIILetter), core.ZeroOrMore(core.RuneRun(core.ASCIIWord)))

	runStreamCases(t, []streamCase{
		{Input: "for", Expected: []string{token(lexTypeR, "for", 0)}},
		{Input: "form", Expected: []string{token(lexTypeIdent, "form", 0)}},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, keyword)
		lx.MustAddRule(lexTypeIdent, ident)
	})
	runStreamCases(t, []streamCase{
		{Input: "for", Expected: []string{token(lexTypeIdent, "for", 0)}},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeIdent, ident)
		lx.MustAddRule(lexTypeR, keyword)
	})
}

// TestCombinatorReuse: a composed rule keeps its state only in the Rule
// values it returns. One Rule value gives the same stream in several scanners
// at once, and a Rule value from the middle of a token gives the same verdict
// when it is called twice.
func TestCombinatorReuse(t *testing.T) {
	const lexTypeNumber = bytescan.TokenType("NUMBER")
	number := core.Sequence(
		core.Optional(core.Rune(core.RuneSet("+-"))),
		core.RuneRun(core.ASCIIDigit),
		core.Optional(core.Sequence(core.Literal("."), core.RuneRun(core.ASCIIDigit))),
		core.NotFollowedBy(core.Optional(core.Literal("e")), core.Rune(core.ASCIILetter)),
	)
	setup := func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeNumber, number)
		lx.MustAddRule(lexTypeR, core.RuneRun(core.ASCIISpace))
	}
	input := strings.Repeat("12 -3.5 +7e 4.25 1.x 9a ", 20)
	want, err := collectStream(input, setup)
	require.NoError(t, err)
	require.Equal(t, token(lexTypeNumber, "12", 0), want[0])

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

	t.Run("mid-token value called concurrently", func(t *testing.T) {
		r := core.ZeroOrMore(core.Rune(core.RuneSet("ab")))
		next, _ := r(bytescan.NewSymbol('a', bytescan.FlagNone))
		var wg sync.WaitGroup
		states := make([]bytescan.State, 8)
		for i := range states {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				sym := []rune("bx")[i%2]
				after, state := next(bytescan.NewSymbol(sym, bytescan.FlagNone))
				if after != nil {
					_, state = after(bytescan.NewSymbol('a', bytescan.FlagNone))
				}
				states[i] = state
			}(i)
		}
		wg.Wait()
		for i, state := range states {
			if i%2 == 0 {
				assert.Equal(t, bytescan.StateAccept, state, "after %q", "ab"+"a")
			} else {
				assert.NotEqual(t, bytescan.StateAccept, state, "after %q", "ax")
			}
		}
	})

	t.Run("mid-token value called twice", func(t *testing.T) {
		next, state := number(bytescan.NewSymbol('1', bytescan.FlagNone))
		require.Equal(t, bytescan.StateContinue, state)
		for i := 0; i < 2; i++ {
			after, state := next(bytescan.NewSymbol('.', bytescan.FlagNone))
			require.Equal(t, bytescan.StateContinue, state)
			_, state = after(bytescan.NewSymbol('5', bytescan.FlagNone))
			require.Equal(t, bytescan.StateContinue, state)
		}
		_, state = next(bytescan.NewSymbol('2', bytescan.FlagNone))
		require.Equal(t, bytescan.StateContinue, state)
	})
}

// verdictTrace calls r with each symbol of input in turn, each time on the
// Rule value that the call before returned, and returns the states. It stops
// when a call returns no Rule value.
func verdictTrace(r bytescan.Rule, input string) []bytescan.State {
	var states []bytescan.State
	for _, sym := range input {
		if r == nil {
			break
		}
		var state bytescan.State
		r, state = r(bytescan.NewSymbol(sym, bytescan.FlagNone))
		states = append(states, state)
	}
	return states
}

// advanceRule returns the Rule value after the symbols of input, which must
// all continue the token.
func advanceRule(t *testing.T, r bytescan.Rule, input string) bytescan.Rule {
	t.Helper()
	for _, sym := range input {
		require.NotNil(t, r, "the token ended before %q of %q", sym, input)
		r, _ = r(bytescan.NewSymbol(sym, bytescan.FlagNone))
	}
	require.NotNil(t, r, "the token ended after %q", input)
	return r
}

// literals returns the Choice of a Literal of each word.
func literals(words ...string) bytescan.Rule {
	rs := make([]bytescan.Rule, len(words))
	for i, w := range words {
		rs[i] = core.Literal(w)
	}
	return core.Choice(rs...)
}

// TestCombinatorRetainedAncestor: a Rule value kept from the middle of a token
// gives the same verdicts after its descendants have advanced. Two branches
// leave one ancestor and advance two or more symbols each; then the ancestor
// and both branches are called again, from several goroutines at once, and
// every verdict equals that of a fresh rule fed the whole input. The rules
// keep one, two, three, four and more than four threads, with and without a
// symbol log, so every way that a state stores its threads is covered.
func TestCombinatorRetainedAncestor(t *testing.T) {
	number := core.Sequence(
		core.Optional(core.Rune(core.RuneSet("+-"))),
		core.RuneRun(core.ASCIIDigit),
		core.Optional(core.Sequence(core.Literal("."), core.RuneRun(core.ASCIIDigit))),
		core.NotFollowedBy(core.Optional(core.Literal("e")), core.Rune(core.ASCIILetter)),
	)
	words := core.Choice(
		core.Literal("abcd"), core.Literal("abce"), core.Literal("abcf"),
		core.Literal("abxy"), core.Literal("abxz"), core.Sequence(core.Literal("ab"), core.RuneRun(core.ASCIILetter)),
		core.Sequence(core.RuneRun(core.ASCIILetter), core.Literal("q")),
	)
	cases := []struct {
		name             string
		rule             bytescan.Rule
		prefix, a, b, at string // at: what the ancestor reads again
	}{
		{"one thread", core.RuneRun(core.ASCIILetter), "ab", "cde ", "xyz1", "q "},
		{"two threads", literals("abcdef", "abcdeg"), "a", "bcdef", "bcdeg", "bx"},
		{"three threads", literals("abcdef", "abcdeg", "abcdeh"), "ab", "cdeh", "cdex", "cdeg"},
		{"four threads", literals("abcdef", "abcdeg", "abcdeh", "abcdei"), "a", "bcdei", "bcdef", "bcx"},
		{"many threads", words, "ab", "cdq", "xyzq ", "cfq"},
		{"symbol log", number, "1", ".5x", "23.e", "2.25 "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := func(input, suffix string) []bytescan.State {
				all := verdictTrace(tc.rule, input+suffix)
				require.GreaterOrEqual(t, len(all), len(input), "fresh run of %q", input+suffix)
				return all[len(input):]
			}
			ancestor := advanceRule(t, tc.rule, tc.prefix)
			branchA := advanceRule(t, ancestor, tc.a[:2])
			branchB := advanceRule(t, ancestor, tc.b[:2])
			// Each branch advances once more, so the ancestor has descendants
			// two and three steps away.
			_ = advanceRule(t, branchA, tc.a[2:3])
			_ = advanceRule(t, branchB, tc.b[2:3])

			runs := []struct {
				r            bytescan.Rule
				input, after string
			}{
				{ancestor, tc.prefix, tc.at},
				{ancestor, tc.prefix, tc.a},
				{branchA, tc.prefix + tc.a[:2], tc.a[2:]},
				{branchB, tc.prefix + tc.b[:2], tc.b[2:]},
			}
			var wg sync.WaitGroup
			got := make([][]bytescan.State, 4*len(runs))
			for i := range got {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					run := runs[i%len(runs)]
					got[i] = verdictTrace(run.r, run.after)
				}(i)
			}
			wg.Wait()
			for i, states := range got {
				run := runs[i%len(runs)]
				assert.Equal(t, fresh(run.input, run.after), states, "goroutine %d: %q then %q", i, run.input, run.after)
			}
		})
	}
}

// TestCombinatorSharedRule: one registered composed rule, shared by two
// scanners that lex different inputs at the same time, gives each scanner the
// token stream that it gives in a scanner alone.
func TestCombinatorSharedRule(t *testing.T) {
	const (
		lexTypeNumber = bytescan.TokenType("NUMBER")
		lexTypeWord   = bytescan.TokenType("WORD")
	)
	number := core.Sequence(
		core.Optional(core.Rune(core.RuneSet("+-"))),
		core.RuneRun(core.ASCIIDigit),
		core.Optional(core.Sequence(core.Literal("."), core.RuneRun(core.ASCIIDigit))),
		core.NotFollowedBy(core.Optional(core.Literal("e")), core.Rune(core.ASCIILetter)),
	)
	word := core.Choice(core.Literal("if"), core.Literal("ifelse"), core.RuneRun(core.ASCIILetter))
	space := core.RuneRun(core.ASCIISpace)
	setup := func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeNumber, number)
		lx.MustAddRule(lexTypeWord, word)
		lx.MustAddRule(lexTypeR, space)
	}
	inputs := []string{
		strings.Repeat("12 -3.5 +7e 4.25 1.x 9a ", 40),
		strings.Repeat("ifelse if abc 0.5 ifx 77 ", 40),
	}
	want := make([][]string, len(inputs))
	for i, input := range inputs {
		var err error
		want[i], err = collectStream(input, setup)
		require.NoError(t, err)
	}

	for round := 0; round < 4; round++ {
		var wg sync.WaitGroup
		got := make([][]string, len(inputs))
		errs := make([]error, len(inputs))
		for i, input := range inputs {
			wg.Add(1)
			go func(i int, input string) {
				defer wg.Done()
				got[i], errs[i] = collectStream(input, setup)
			}(i, input)
		}
		wg.Wait()
		for i := range inputs {
			require.NoError(t, errs[i], "round %d, scanner %d", round, i)
			assert.Equal(t, want[i], got[i], "round %d, scanner %d", round, i)
		}
	}
}
