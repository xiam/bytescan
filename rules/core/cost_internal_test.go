package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// countingMatcher counts the steps of the matcher m in *calls.
type countingMatcher struct {
	m     matcher
	calls *int
}

func (c countingMatcher) step(st int, r rune) (int, bool, bool) {
	*c.calls++
	return c.m.step(st, r)
}

// countingRule returns r with every call to it and to the Rule values it
// returns counted in *calls.
func countingRule(r bytescan.Rule, calls *int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		*calls++
		next, state := r(s)
		if next != nil {
			next = countingRule(next, calls)
		}
		return next, state
	}
}

// instrumented returns a copy of the node graph n whose leaves count their
// calls in *calls.
func instrumented(n *node, calls *int) *node {
	c := *n
	if n.leaf != nil {
		if n.leaf.m != nil {
			c.leaf = &leaf{m: countingMatcher{m: n.leaf.m, calls: calls}}
		} else {
			c.leaf = &leaf{rule: countingRule(n.leaf.rule, calls)}
		}
		return &c
	}
	c.parts = make([]*node, len(n.parts))
	for i, part := range n.parts {
		c.parts[i] = instrumented(part, calls)
	}
	return &c
}

// costRow is one measurement of TestCombinatorCost: the leaf calls per input
// symbol that a combinator's doc comment states for a rule and an input.
type costRow struct {
	name  string
	rule  bytescan.Rule
	input string
	want  float64 // leaf calls per input symbol, as documented
}

// costRows returns the measurements that the combinators' doc comments state.
func costRows() []costRow {
	letters := strings.Repeat("abcdefghij", 100)
	return []costRow{
		{"Sequence", Sequence(Rune(ASCIILetter), RuneRun(ASCIIWord)), letters, 1.0},
		{"Choice", Choice(Literal("if"), Literal("ifelse"), Literal("for")), strings.Repeat("ifelse", 100), 1.5},
		{"Optional", Sequence(Optional(Rune(RuneSet("+-"))), RuneRun(ASCIIDigit)), "+" + strings.Repeat("1234567890", 100), 1.0},
		{"ZeroOrMore", Sequence(Rune(ASCIILetter), ZeroOrMore(RuneRun(ASCIIWord))), letters, 1.0},
		{"ZeroOrMore with an opaque rule", Sequence(Rune(ASCIILetter), ZeroOrMore(countingRule(RuneRun(ASCIIWord), new(int)))), letters[:200], 100.5},
		{"OneOrMore", OneOrMore(Literal("ab")), strings.Repeat("ab", 500), 1.0},
		{"Repeat", Repeat(Rune(ASCIIDigit), 2, 4), strings.Repeat("1234", 250), 1.0},
		{"RuneRunMax", RuneRunMax(ASCIIDigit, 7), strings.Repeat("1234567890", 100), 1.0},
		{"FollowedBy", FollowedBy(Literal("abcd"), Literal("ef")), strings.Repeat("abcdef", 100), 1.33},
		{"NotFollowedBy", NotFollowedBy(Literal("for"), Rune(ASCIIWord)), strings.Repeat("for ", 250), 1.25},
	}
}

// measureCost lexes input with the rule of row, with counted leaves, and
// returns the leaf calls per input symbol.
func measureCost(t testing.TB, row costRow) float64 {
	calls := 0
	n := instrumented(nodeOf("test", 0, row.rule), &calls)
	r := newSnapshot(compile("test", n)).rule()

	lx := bytescan.New(strings.NewReader(row.input))
	lx.MustAddRule(bytescan.TokenType("R"), r)
	for {
		_, err := lx.Next()
		if err != nil {
			break
		}
	}
	return float64(calls) / float64(len([]rune(row.input)))
}

// TestCombinatorCost: the leaf calls per input symbol of each combinator are
// those that its doc comment states.
func TestCombinatorCost(t *testing.T) {
	for _, row := range costRows() {
		t.Run(row.name, func(t *testing.T) {
			got := measureCost(t, row)
			t.Logf("%s: %.2f leaf calls per input symbol", row.name, got)
			assert.InDelta(t, row.want, got, 0.05)
		})
	}
}

// BenchmarkCombinators reports the time and the leaf calls per input symbol
// of each row of TestCombinatorCost.
func BenchmarkCombinators(b *testing.B) {
	for _, row := range costRows() {
		b.Run(row.name, func(b *testing.B) {
			b.ReportMetric(measureCost(b, row), "leafcalls/symbol")
			b.SetBytes(int64(len(row.input)))
			for i := 0; i < b.N; i++ {
				lx := bytescan.New(strings.NewReader(row.input))
				lx.MustAddRule(bytescan.TokenType("R"), row.rule)
				for {
					if _, err := lx.Next(); err != nil {
						break
					}
				}
			}
		})
	}
}

// TestOpaqueZeroOrMoreIsNotMerged documents the cost of an opaque rule inside ZeroOrMore:
// each end of the rule starts one more run, so the calls grow with the
// square of the token length.
func TestOpaqueZeroOrMoreIsNotMerged(t *testing.T) {
	small := measureCost(t, costRow{rule: Sequence(Rune(ASCIILetter), ZeroOrMore(countingRule(RuneRun(ASCIIWord), new(int)))), input: strings.Repeat("a", 100)})
	large := measureCost(t, costRow{rule: Sequence(Rune(ASCIILetter), ZeroOrMore(countingRule(RuneRun(ASCIIWord), new(int)))), input: strings.Repeat("a", 200)})
	require.Greater(t, large, 1.9*small)
}

// TestLookaheadIsNotMerged documents the cost of a lookahead that overlaps a
// repetition: each end of the rule starts its own run of the lookahead, so
// the calls per symbol grow with the token length, about n for n runes.
func TestLookaheadIsNotMerged(t *testing.T) {
	rule := FollowedBy(RuneRun(RuneSet("a")), Sequence(RuneRun(RuneSet("a")), Literal("b")))
	for _, n := range []int{100, 200} {
		got := measureCost(t, costRow{rule: rule, input: strings.Repeat("a", n) + "b"})
		t.Logf("n = %d: %.1f leaf calls per input symbol", n, got)
		assert.InDelta(t, float64(n), got, float64(n)/10)
	}
}
