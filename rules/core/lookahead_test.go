package core_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// TestKeyword: Keyword("for", core.ASCIIWord) is for(?!\w), in Perl notation,
// which Go's regexp syntax cannot express. The cases end the input in each
// scanning state: inside the keyword ("f", "fo") and after it ("for", where
// the end of input completes the lookahead and the keyword is emitted).
func TestKeyword(t *testing.T) {
	const lexTypeKeyword = bytescan.TokenType("KEYWORD")

	keyword := func(offset int) string { return token(lexTypeKeyword, "for", offset) }

	t.Run("alone", func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "for", Expected: []string{keyword(0)}},
			{Input: "for(", Expected: append([]string{keyword(0)}, unknowns("(", 3)...)},
			{Input: "for for", Expected: []string{keyword(0), token(bytescan.TokenTypeUnknown, " ", 3), keyword(4)}},
			{Input: "format", Expected: unknowns("format", 0)},
			{Input: "for_1", Expected: unknowns("for_1", 0)},
			{Input: "f", Expected: unknowns("f", 0)},
			{Input: "fo", Expected: unknowns("fo", 0)},
			{Input: "fox", Expected: unknowns("fox", 0)},
			{Input: "", Expected: nil},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.Keyword("for", core.ASCIIWord))
		})
	})

	// Beside a word-run rule, the keyword wins a tie by registration order,
	// and a longer word wins by length.
	t.Run("beside a word run", func(t *testing.T) {
		const lexTypeWord = bytescan.TokenType("WORD")
		runStreamCases(t, []streamCase{
			{Input: "for", Expected: []string{keyword(0)}},
			{Input: "for x", Expected: []string{keyword(0), token(bytescan.TokenTypeUnknown, " ", 3), token(lexTypeWord, "x", 4)}},
			{Input: "format", Expected: []string{token(lexTypeWord, "format", 0)}},
			{Input: "xfor", Expected: []string{token(lexTypeWord, "xfor", 0)}},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeKeyword, core.Keyword("for", core.ASCIIWord))
			lx.MustAddRule(lexTypeWord, core.RuneRun(core.ASCIIWord))
		})
	})

	// The lookahead class need not be ASCIIWord: "->" not followed by '>'.
	t.Run("other class", func(t *testing.T) {
		const lexTypeArrow = bytescan.TokenType("ARROW")
		runStreamCases(t, []streamCase{
			{Input: "->", Expected: []string{token(lexTypeArrow, "->", 0)}},
			{Input: "->x", Expected: append([]string{token(lexTypeArrow, "->", 0)}, unknowns("x", 2)...)},
			{Input: "->>", Expected: unknowns("->>", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeArrow, core.Keyword("->", core.RuneSet(">")))
		})
	})
}

// newLettersAtEndRule returns a rule for [A-Za-z]+\z: a run of ASCII letters
// that ends the input. It is the worked example of practice 5 in
// docs/rule-authoring.md, written with core.PushBack. At end of input it
// excludes the EOF symbol and accepts the letters; any other symbol after a
// letter rejects.
func newLettersAtEndRule() bytescan.Rule {
	var start, letters bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if core.ASCIILetter(s.Rune()) {
			return letters, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	letters = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return core.PushBack(1, bytescan.StateAccept) // the EOF symbol
		}
		if core.ASCIILetter(s.Rune()) {
			return letters, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return start
}

// newABBeforeCDRule returns a rule for ab(?=cd), in Perl notation. It never
// accepts before the lookahead has decided. On the 'd' it excludes "cd" and
// accepts "ab". At end of input it rejects the EOF symbol, so it matches
// nothing.
func newABBeforeCDRule() bytescan.Rule {
	return abcdAt(0)
}

// abcdAt is the state of newABBeforeCDRule that reads rune i of "abcd".
func abcdAt(i int) bytescan.Rule {
	const text = "abcd"
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == rune(text[i]) {
			if i == len(text)-1 {
				return core.PushBack(2, bytescan.StateAccept) // the 'd' just read and the 'c' before it
			}
			return abcdAt(i + 1), bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// newABOrABCDBeforeXRule returns a rule for ab|abcd(?=x), in Perl notation. It
// accepts "ab" eagerly, because the pending lookahead can only grow the
// match. After "abcd" any symbol but 'x' rejects, so "ab" stays the match, and
// an 'x' grows the match to "abcd" through PushBack(1, StateAccept). At end of
// input it rejects the EOF symbol.
func newABOrABCDBeforeXRule() bytescan.Rule {
	var afterABCD bytescan.Rule
	afterABCD = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == 'x' {
			return core.PushBack(1, bytescan.StateAccept) // the 'x' just read
		}
		return nil, bytescan.StateReject
	}
	return abOrABCDAt(0, afterABCD)
}

// abOrABCDAt is the state of newABOrABCDBeforeXRule that reads rune i of
// "abcd". It accepts at the 'b' and hands the symbol after "abcd" to then.
func abOrABCDAt(i int, then bytescan.Rule) bytescan.Rule {
	const text = "abcd"
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() != rune(text[i]) {
			return nil, bytescan.StateReject
		}
		switch i {
		case 1:
			return abOrABCDAt(i+1, then), bytescan.StateAccept
		case len(text) - 1:
			return then, bytescan.StateContinue
		}
		return abOrABCDAt(i+1, then), bytescan.StateContinue
	}
}

// TestPushBack: hand-written rules that settle on a shorter match with
// core.PushBack give the expected streams, at end of input too.
func TestPushBack(t *testing.T) {
	t.Run(`[A-Za-z]+\z`, func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "ab", Expected: []string{token(lexTypeR, "ab", 0)}},
			{Input: "ab cd", Expected: append(unknowns("ab ", 0), token(lexTypeR, "cd", 3))},
			{Input: "a", Expected: []string{token(lexTypeR, "a", 0)}},
			{Input: "1", Expected: unknowns("1", 0)},
			{Input: "", Expected: nil},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newLettersAtEndRule())
		})
	})

	t.Run(`ab(?=cd)`, func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "abcd", Expected: append([]string{token(lexTypeR, "ab", 0)}, unknowns("cd", 2)...)},
			{Input: "abcdabcd", Expected: []string{
				token(lexTypeR, "ab", 0), token(bytescan.TokenTypeUnknown, "c", 2), token(bytescan.TokenTypeUnknown, "d", 3),
				token(lexTypeR, "ab", 4), token(bytescan.TokenTypeUnknown, "c", 6), token(bytescan.TokenTypeUnknown, "d", 7),
			}},
			{Input: "abc", Expected: unknowns("abc", 0)},
			{Input: "abx", Expected: unknowns("abx", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newABBeforeCDRule())
		})
	})

	t.Run(`ab|abcd(?=x)`, func(t *testing.T) {
		runStreamCases(t, []streamCase{
			{Input: "abcdx", Expected: append([]string{token(lexTypeR, "abcd", 0)}, unknowns("x", 4)...)},
			{Input: "abcdy", Expected: append([]string{token(lexTypeR, "ab", 0)}, unknowns("cdy", 2)...)},
			{Input: "abcd", Expected: append([]string{token(lexTypeR, "ab", 0)}, unknowns("cd", 2)...)},
			{Input: "ab", Expected: []string{token(lexTypeR, "ab", 0)}},
			{Input: "a", Expected: unknowns("a", 0)},
		}, func(lx *bytescan.Scanner) {
			lx.MustAddRule(lexTypeR, newABOrABCDBeforeXRule())
		})
	})

	// The verdict sequence: n PushBack verdicts, then (nil, state). n runs
	// past 8, where the chain is no longer prebuilt.
	t.Run("verdicts", func(t *testing.T) {
		for _, state := range []bytescan.State{bytescan.StateAccept, bytescan.StateReject} {
			for n := 1; n <= 12; n++ {
				next, got := core.PushBack(n, state)
				pushbacks := 0
				for got == bytescan.StatePushBack {
					pushbacks++
					require.NotNil(t, next)
					next, got = next(bytescan.NewSymbol(bytescan.RuneEOF, bytescan.FlagEOF))
				}
				require.Equal(t, n, pushbacks, "PushBack(%d, %v)", n, state)
				require.Equal(t, state, got)
				require.Nil(t, next)
			}
		}
	})
}
