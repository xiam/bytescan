package core

import (
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check the engine against refEnds, an independent
// evaluator of the node graph over the whole input. refEnds knows nothing of
// threads, merging, late ends or the VM verdicts; it computes the set of ends
// of each node from the sets of its parts. Go's regexp is not used under
// rules/; the regexp oracle tests of the combinators are in the tests/
// package.

// refEnds returns every end of n that starts at position pos of in, the
// symbols of the whole input followed by the EOF symbol.
func refEnds(n *node, in []bytescan.Symbol, pos int) map[int]bool {
	ends := map[int]bool{}
	switch n.kind {
	case nodeLeaf:
		for _, end := range refLeafEnds(n.leaf, in, pos) {
			ends[end] = true
		}

	case nodeSeq:
		ends[pos] = true
		for _, part := range n.parts {
			next := map[int]bool{}
			for p := range ends {
				for q := range refEnds(part, in, p) {
					next[q] = true
				}
			}
			ends = next
		}

	case nodeAlt:
		for _, part := range n.parts {
			for q := range refEnds(part, in, pos) {
				ends[q] = true
			}
		}

	case nodeOpt:
		ends = refEnds(n.parts[0], in, pos)
		ends[pos] = true

	case nodeMany, nodeMany1:
		reached := map[int]bool{}
		work := []int{pos}
		if n.kind == nodeMany {
			reached[pos] = true
		}
		for len(work) > 0 {
			p := work[0]
			work = work[1:]
			for q := range refEnds(n.parts[0], in, p) {
				if !reached[q] {
					reached[q] = true
					work = append(work, q)
				}
			}
		}
		ends = reached

	case nodeRepeat:
		current := map[int]bool{pos: true}
		for count := 0; len(current) > 0; count++ {
			if count >= n.min {
				for p := range current {
					ends[p] = true
				}
			}
			if count == n.max || count > len(in)+n.min {
				break
			}
			next := map[int]bool{}
			for p := range current {
				for q := range refEnds(n.parts[0], in, p) {
					next[q] = true
				}
			}
			current = next
		}

	case nodeFollowedBy, nodeNotFollowedBy:
		for p := range refEnds(n.parts[0], in, pos) {
			matched := len(refEnds(n.parts[1], in, p)) > 0
			if matched == (n.kind == nodeFollowedBy) {
				ends[p] = true
			}
		}
	}
	return ends
}

// refLeafEnds returns the ends of the leaf l that starts at position pos of
// in, as the VM would record them for a registered rule: the strictly growing
// accepts.
func refLeafEnds(l *leaf, in []bytescan.Symbol, pos int) []int {
	var ends []int
	if l.m != nil {
		st := 0
		for p := pos; p < len(in) && !in[p].IsEOF(); p++ {
			next, match, more := l.m.step(st, in[p].Rune())
			if match {
				ends = append(ends, p+1)
			}
			if !more {
				break
			}
			st = next
		}
		return ends
	}

	r, last := l.rule, pos
	for p := pos; p < len(in); p++ {
		next, state := callLeaf(r, in[p])
		excluded := 0
		for state == bytescan.StatePushBack {
			excluded++
			if next == nil || excluded > p+1-pos {
				next, state = nil, bytescan.StateReject
				break
			}
			next, state = callLeaf(next, in[p+1-excluded])
		}
		if state == bytescan.StateAccept && (!in[p].IsEOF() || excluded > 0) {
			if end := p + 1 - excluded; end > last {
				last = end
				ends = append(ends, end)
			}
		}
		if (state != bytescan.StateAccept && state != bytescan.StateContinue) || next == nil || in[p].IsEOF() {
			break
		}
		r = next
	}
	return ends
}

// symbolsOf returns the symbols of input followed by the EOF symbol.
func symbolsOf(input string) []bytescan.Symbol {
	var syms []bytescan.Symbol
	for _, r := range input {
		syms = append(syms, bytescan.NewSymbol(r, bytescan.FlagNone))
	}
	return append(syms, bytescan.NewSymbol(bytescan.RuneEOF, bytescan.FlagEOF))
}

// refFirstMatch returns the first match that refEnds gives for n on input:
// the longest non-empty end from position 0, in runes, or 0.
func refFirstMatch(n *node, input string) int {
	best := 0
	for end := range refEnds(n, symbolsOf(input), 0) {
		best = max(best, end)
	}
	return best
}

// firstMatch lexes input with the single rule r and returns the rune length
// of the first token if r matched it, or 0.
func firstMatch(t testing.TB, r bytescan.Rule, input string) int {
	const lexTypeR = bytescan.TokenType("R")
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeR, r)
	lex, err := lx.Next()
	if errors.Is(err, io.EOF) {
		return 0
	}
	require.NoError(t, err)
	if lex.Type() != lexTypeR {
		return 0
	}
	return len([]rune(lex.Text()))
}

// stringsOver returns every string over the runes of alphabet with a length
// from 1 to maxLen.
func stringsOver(alphabet string, maxLen int) []string {
	runes := []rune(alphabet)
	all := []string{}
	level := []string{""}
	for n := 1; n <= maxLen; n++ {
		var next []string
		for _, s := range level {
			for _, r := range runes {
				next = append(next, s+string(r))
			}
		}
		all = append(all, next...)
		level = next
	}
	return all
}

// lit is Literal, and runes is Rune(RuneSet(s)); short names keep the rows
// readable.
func lit(s string) bytescan.Rule   { return Literal(s) }
func runes(s string) bytescan.Rule { return Rune(RuneSet(s)) }

// opaqueRun is a rule that this package did not build, for [c]+. It accepts
// after each rune of c.
func opaqueRun(c Class) bytescan.Rule {
	var loop bytescan.Rule
	loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if c(s.Rune()) {
			return loop, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return loop
}

// opaqueBefore is a rule that this package did not build, for x(?=y) in Perl
// notation, with the runes x and y. After x it reads one more symbol and, on
// y, excludes it with a pushback chain.
func opaqueBefore(x, y rune) bytescan.Rule {
	var start, after bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == x {
			return after, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	after = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == y {
			return PushBack(1, bytescan.StateAccept) // the y after x
		}
		return nil, bytescan.StateReject
	}
	return start
}

// opaqueDelayed is a rule that this package did not build, for
// ab(?=c)|abcd in Perl notation, with delayed acceptance: after "abc" it
// waits for 'd', and otherwise ends with a pushback chain to "ab".
func opaqueDelayed() bytescan.Rule {
	return delayedAt(0)
}

// delayedAt is the state of opaqueDelayed that reads rune i of "abcd".
func delayedAt(i int) bytescan.Rule {
	const long = "abcd"
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			if i == 3 {
				return PushBack(2, bytescan.StateAccept) // the 'c' and the EOF symbol
			}
			return nil, bytescan.StateReject
		}
		if s.Rune() == rune(long[i]) {
			if i == 3 {
				return nil, bytescan.StateAccept
			}
			return delayedAt(i + 1), bytescan.StateContinue
		}
		if i == 3 {
			return PushBack(2, bytescan.StateAccept) // the 'c' and the symbol just read
		}
		return nil, bytescan.StateReject
	}
}

// engineRow is one composed rule that TestEngineMatchesReference checks on
// every string over alphabet up to maxLen runes.
type engineRow struct {
	name     string
	rule     bytescan.Rule
	alphabet string
	maxLen   int
}

// engineRows returns the rows of TestEngineMatchesReference. They cover every
// combinator, nullable parts inside repetitions, nested lookaheads, and
// opaque leaves with and without pushback chains.
func engineRows() []engineRow {
	a, b, c := runes("a"), runes("b"), runes("c")
	return []engineRow{
		{"a|ab", Choice(lit("a"), lit("ab")), "abx", 4},
		{"(?:ab)+", OneOrMore(lit("ab")), "abx", 7},
		{"(?:a|bc)+", OneOrMore(Choice(a, lit("bc"))), "abcx", 6},
		{"a*ab", Sequence(ZeroOrMore(a), lit("ab")), "abx", 7},
		{"(?:abc)*(?:abc|ab|a)", Sequence(ZeroOrMore(lit("abc")), Choice(lit("abc"), lit("ab"), a)), "abcx", 6},
		{"(?:a|ab)(?:c|bcd)", Sequence(Choice(a, lit("ab")), Choice(c, lit("bcd"))), "abcd", 6},
		{"a?b", Sequence(Optional(a), b), "abx", 5},
		{"(?:a?)*", ZeroOrMore(Optional(a)), "abx", 5},
		{"(?:a?)*b", Sequence(ZeroOrMore(Optional(a)), b), "abx", 6},
		{"(?:a*)*b", Sequence(ZeroOrMore(ZeroOrMore(a)), b), "abx", 6},
		{"(?:a?){2,3}b", Sequence(Repeat(Optional(a), 2, 3), b), "abx", 6},
		{"(?:ab){0,2}c", Sequence(Repeat(lit("ab"), 0, 2), c), "abcx", 6},
		{"(?:a|b){3}", Repeat(Choice(a, b), 3, 3), "abx", 5},
		{"(?:ab){2,}", Repeat(lit("ab"), 2, -1), "abx", 7},
		{"(?:a+)*x", Sequence(ZeroOrMore(RuneRun(RuneSet("a"))), runes("x")), "ax", 8},
		{"a+(?=b)", FollowedBy(RuneRun(RuneSet("a")), b), "abx", 6},
		{"a+(?!ab)", NotFollowedBy(RuneRun(RuneSet("a")), lit("ab")), "abx", 6},
		{"(?:a(?=b)|ab)c", Sequence(Choice(FollowedBy(a, b), lit("ab")), c), "abcx", 6},
		{"(?:a(?!b))+", OneOrMore(NotFollowedBy(a, b)), "abx", 7},
		{"(?:a?(?=b))*b", Sequence(ZeroOrMore(FollowedBy(Optional(a), b)), b), "abx", 6},
		{"(?:a?(?!a))*", ZeroOrMore(NotFollowedBy(Optional(a), a)), "abx", 6},
		{"a(?=b(?!c))", FollowedBy(a, NotFollowedBy(b, c)), "abcx", 5},
		{"a(?!b(?=c))", NotFollowedBy(a, FollowedBy(b, c)), "abcx", 5},
		{"b(?!a?(?!a))", NotFollowedBy(b, NotFollowedBy(Optional(a), a)), "abx", 5},
		{"b(?!a?(?=a))", NotFollowedBy(b, FollowedBy(Optional(a), a)), "abx", 5},
		{"b(?=a?(?!a))", FollowedBy(b, NotFollowedBy(Optional(a), a)), "abx", 5},
		{"b(?!a(?!b(?!c)))c?", Sequence(NotFollowedBy(b, NotFollowedBy(a, NotFollowedBy(b, c))), Optional(c)), "abcx", 6},
		{"(?:a(?!b?(?!b)))+", OneOrMore(NotFollowedBy(a, NotFollowedBy(Optional(b), b))), "abx", 6},
		{"(?:a?(?=a?(?!a)))*", ZeroOrMore(FollowedBy(Optional(a), NotFollowedBy(Optional(a), a))), "abx", 5},
		{"(?:a?(?=a?(?!a)))+b", Sequence(OneOrMore(FollowedBy(Optional(a), NotFollowedBy(Optional(a), a))), b), "abx", 5},
		{"(?:a?(?!a?(?=b)))*", ZeroOrMore(NotFollowedBy(Optional(a), FollowedBy(Optional(a), b))), "abx", 5},
		{"(?:a?(?=a?(?!a))){0,}b", Sequence(Repeat(FollowedBy(Optional(a), NotFollowedBy(Optional(a), a)), 0, -1), b), "abx", 5},
		{"a(?=b+c)b", Sequence(FollowedBy(a, Sequence(RuneRun(RuneSet("b")), c)), b), "abcx", 6},
		{"Keyword(ab, [ab])c?", Sequence(Keyword("ab", RuneSet("ab")), Optional(c)), "abcx", 6},
		{"opaque [a]+ then ab", Sequence(opaqueRun(RuneSet("a")), lit("ab")), "abx", 7},
		{"(?:opaque [a]+)*b", Sequence(ZeroOrMore(opaqueRun(RuneSet("a"))), b), "abx", 7},
		{"opaque a(?=b) then bc", Sequence(opaqueBefore('a', 'b'), lit("bc")), "abcx", 6},
		{"(?:opaque a(?=b) then b)+", OneOrMore(Sequence(opaqueBefore('a', 'b'), b)), "abx", 7},
		{"opaque ab(?=c)|abcd then c?d", Sequence(opaqueDelayed(), Optional(c), runes("d")), "abcd", 6},
		{"opaque ab(?=c)|abcd, alt cd", Choice(opaqueDelayed(), Sequence(lit("ab"), FollowedBy(c, runes("x")))), "abcdx", 5},
		{"(?:opaque a(?=b))?(?=b)b", Sequence(FollowedBy(Optional(opaqueBefore('a', 'b')), b), b), "abx", 5},
	}
}

// TestEngineMatchesReference: on every input, the first match of each
// composed rule through the VM is the longest end that refEnds computes from
// its node graph.
func TestEngineMatchesReference(t *testing.T) {
	for _, row := range engineRows() {
		t.Run(row.name, func(t *testing.T) {
			n := nodeOf("test", 0, row.rule)
			mismatches := 0
			for _, input := range stringsOver(row.alphabet, row.maxLen) {
				want := refFirstMatch(n, input)
				if got := firstMatch(t, row.rule, input); got != want {
					mismatches++
					if mismatches <= 5 {
						t.Errorf("on %q the first match has %d runes, want %d", input, got, want)
					}
				}
			}
			if mismatches > 5 {
				t.Errorf("%d mismatches in all", mismatches)
			}
		})
	}
}

// TestEngineEndsMatchReference: the snapshot after each symbol has the
// longest end that refEnds gives for that prefix, as far as the ends are
// decided: an end the engine reports is never above the reference, and once
// every thread is dead, the engine has found the longest reference end.
func TestEngineEndsMatchReference(t *testing.T) {
	for _, row := range engineRows() {
		t.Run(row.name, func(t *testing.T) {
			n := nodeOf("test", 0, row.rule)
			prog := compile("test", n)
			for _, input := range stringsOver(row.alphabet, min(row.maxLen, 5)) {
				syms := symbolsOf(input)
				ends := sortedEnds(refEnds(n, syms, 0))
				snap := newSnapshot(prog)
				for _, s := range syms {
					snap = snap.next(s)
					if len(snap.threads) == 0 {
						break
					}
				}
				want := 0
				if len(ends) > 0 {
					want = ends[len(ends)-1]
				}
				require.Equal(t, want, snap.best, "input %q", input)
				require.Empty(t, snap.pending, "input %q", input)
			}
		})
	}
}

// sortedEnds returns the non-empty ends of ends in increasing order.
func sortedEnds(ends map[int]bool) []int {
	var sorted []int
	for end := range ends {
		if end > 0 {
			sorted = append(sorted, end)
		}
	}
	sort.Ints(sorted)
	return sorted
}

// TestProgramNeedsLog: a program keeps a symbol log only when it has an
// opaque leaf or a lookahead, the only instructions that resume it at an
// earlier position.
func TestProgramNeedsLog(t *testing.T) {
	a, b := runes("a"), runes("b")
	cases := []struct {
		name string
		rule bytescan.Rule
		want bool
	}{
		{"a|ab", Choice(lit("a"), lit("ab")), false},
		{"a*b", Sequence(ZeroOrMore(a), b), false},
		{"[a]+", RuneRun(RuneSet("a")), false},
		{"a+(?=b)", FollowedBy(RuneRun(RuneSet("a")), b), true},
		{"a+(?!b)", NotFollowedBy(RuneRun(RuneSet("a")), b), true},
		{"opaque [a]+ then b", Sequence(opaqueRun(RuneSet("a")), b), true},
	}
	for _, tc := range cases {
		prog := compile("test", nodeOf("test", 0, tc.rule))
		require.Equal(t, tc.want, prog.needsLog, tc.name)
		if !tc.want {
			snap := newSnapshot(prog).next(bytescan.NewSymbol('a', bytescan.FlagNone))
			require.Nil(t, snap.log, "%s: a program without a log keeps none", tc.name)
		}
	}
}
