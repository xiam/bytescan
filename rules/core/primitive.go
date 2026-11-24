package core

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/xiam/bytescan"
)

// newPrimitive is the one construction path of every primitive. Each
// primitive constructor validates its arguments, builds the start state of
// the rule and its node n, and returns newPrimitive(start, n). The returned
// rule behaves as start, and a combinator that receives it composes n instead
// of running start as an opaque rule (see handshake.go). A combinator builds
// its rule with newCombinator.
func newPrimitive(start bytescan.Rule, n *node) bytescan.Rule {
	return answering(n, start)
}

// Literal returns a rule for the literal s: regexp.QuoteMeta(s). It accepts
// at the last rune of s and stops. At end of input before that rune it
// rejects the EOF symbol, so it matches nothing. It panics if s is empty.
func Literal(s string) bytescan.Rule {
	if s == "" {
		panic("core.Literal: s must not be empty")
	}
	lit := []rune(s)
	return newPrimitive(literalChain(lit)[0], leafNode(literalMatcher{lit: lit}))
}

// literalChain builds the states of Literal once, when the rule is
// constructed: state i reads rune i of lit and continues in state i+1, so a
// step returns a prebuilt Rule and allocates nothing. The states hold no
// mutable state, so one chain serves every scanner that runs the rule.
func literalChain(lit []rune) []bytescan.Rule {
	chain := make([]bytescan.Rule, len(lit))
	for i := range lit {
		i := i // go.mod predates per-iteration loop variables
		last := i == len(lit)-1
		chain[i] = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() {
				return nil, bytescan.StateReject
			}
			if s.Rune() == lit[i] {
				if last {
					return nil, bytescan.StateAccept
				}
				return chain[i+1], bytescan.StateContinue
			}
			return nil, bytescan.StateReject
		}
	}
	return chain
}

// LiteralIgnoreCase returns a rule for the literal s under Unicode simple case
// folding: (?i)regexp.QuoteMeta(s). It folds as regexp does, so 'ſ' (U+017F)
// matches 's' and 'K' (U+212A) matches 'k'. It accepts at the last rune of s
// and stops. At end of input before that rune it rejects the EOF symbol, so it
// matches nothing. It panics if s is empty.
func LiteralIgnoreCase(s string) bytescan.Rule {
	if s == "" {
		panic("core.LiteralIgnoreCase: s must not be empty")
	}
	lit := []rune(s)
	return newPrimitive(literalFoldChain(lit)[0], leafNode(literalFoldMatcher{lit: lit}))
}

// literalFoldChain builds the states of LiteralIgnoreCase once, as
// literalChain does for Literal: state i reads rune i of lit under simple case
// folding and continues in state i+1.
func literalFoldChain(lit []rune) []bytescan.Rule {
	chain := make([]bytescan.Rule, len(lit))
	for i := range lit {
		i := i // go.mod predates per-iteration loop variables
		last := i == len(lit)-1
		chain[i] = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() {
				return nil, bytescan.StateReject
			}
			if isFoldOf(s.Rune(), lit[i]) {
				if last {
					return nil, bytescan.StateAccept
				}
				return chain[i+1], bytescan.StateContinue
			}
			return nil, bytescan.StateReject
		}
	}
	return chain
}

// isFoldOf reports whether r is in the simple case-folding orbit of want, the
// set that (?i)want matches in regexp.
func isFoldOf(r, want rune) bool {
	if r == want {
		return true
	}
	for f := unicode.SimpleFold(want); f != want; f = unicode.SimpleFold(f) {
		if f == r {
			return true
		}
	}
	return false
}

// AnyLiteral returns a rule for the alternation s1|s2|... of the literals, with
// the longest match: on "ifelse", AnyLiteral("if", "ifelse") matches "ifelse",
// and on "ifels" it matches "if". A literal that is a prefix of a longer one
// accepts at its own last rune, and the rule continues only while a longer
// literal can still match. At end of input it rejects the EOF symbol, so the
// longest literal read so far stays the match. It panics if no literal is
// given or a literal is empty.
func AnyLiteral(ss ...string) bytescan.Rule {
	if len(ss) == 0 {
		panic("core.AnyLiteral: no literal")
	}
	candidates := make([][]rune, 0, len(ss))
	for _, lit := range ss {
		if lit == "" {
			panic("core.AnyLiteral: a literal is empty")
		}
		candidates = append(candidates, []rune(lit))
	}
	return newPrimitive(literalsAt(candidates, 0, false), leafNode(newLiteralsMatcher(candidates, false)))
}

// AnyLiteralIgnoreCase returns a rule for the alternation of the literals
// under Unicode simple case folding: (?i:s1|s2|...), with the longest match.
// It folds as LiteralIgnoreCase does, so 'ſ' (U+017F) matches 's' and 'K'
// (U+212A) matches 'k'; there is no full case folding, so "ß" does not match
// "ss". Two literals that differ only in case are the same literal. Otherwise
// it behaves as AnyLiteral: a literal that is a prefix of a longer one accepts
// at its own last rune, the rule continues only while a longer literal can
// still match, and at end of input it rejects the EOF symbol, so the longest
// literal read so far stays the match. It panics if no literal is given or a
// literal is empty.
func AnyLiteralIgnoreCase(ss ...string) bytescan.Rule {
	candidates := foldedLiterals("AnyLiteralIgnoreCase", ss)
	return newPrimitive(literalsAt(candidates, 0, true), leafNode(newLiteralsMatcher(candidates, true)))
}

// foldedLiterals returns the runes of each literal of ss, each rune replaced
// by its foldKey. It panics, naming the constructor fn, if no literal is given
// or a literal is empty.
func foldedLiterals(fn string, ss []string) [][]rune {
	if len(ss) == 0 {
		panic("core." + fn + ": no literal")
	}
	lits := make([][]rune, 0, len(ss))
	for _, s := range ss {
		if s == "" {
			panic("core." + fn + ": a literal is empty")
		}
		lit := []rune(s)
		for i, r := range lit {
			lit[i] = foldKey(r)
		}
		lits = append(lits, lit)
	}
	return lits
}

// foldKey returns the smallest rune of the simple case-folding orbit of r.
// Two runes have the same key exactly when isFoldOf reports true for them,
// so a rule that compares keys folds as LiteralIgnoreCase does.
func foldKey(r rune) rune {
	if r < utf8.RuneSelf {
		// The orbit of an ASCII letter holds its upper case form, which is
		// the smallest rune of the orbit ('K' < 'k' < U+212A); every other
		// ASCII rune is alone in its orbit.
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}
	key := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < key {
			key = f
		}
	}
	return key
}

// literalsEdge is one transition of an AnyLiteral state: on rune r, a
// candidate ends here when accepts is set, and the longer candidates continue
// in next, which is nil when none is longer.
type literalsEdge struct {
	r       rune
	accepts bool
	next    bytescan.Rule
}

// literalsAt builds, once and when the rule is constructed, the state of
// AnyLiteral that reads rune i of each candidate, with every state after it: a
// trie of the candidates from rune i on. A candidate that ends at rune i
// accepts; a longer candidate continues in the next state. A step returns a
// prebuilt Rule and allocates nothing. With fold set, the runes of the
// candidates are fold keys (foldedLiterals), and a state compares the fold key
// of the rune it reads: that is AnyLiteralIgnoreCase.
func literalsAt(candidates [][]rune, i int, fold bool) bytescan.Rule {
	var edges []literalsEdge
	var groups [][][]rune // groups[k]: the candidates longer than i+1 on edges[k].r
	for _, c := range candidates {
		k := 0
		for k < len(edges) && edges[k].r != c[i] {
			k++
		}
		if k == len(edges) {
			edges = append(edges, literalsEdge{r: c[i]})
			groups = append(groups, nil)
		}
		if len(c) == i+1 {
			edges[k].accepts = true
		} else {
			groups[k] = append(groups[k], c)
		}
	}
	for k := range edges {
		if len(groups[k]) > 0 {
			edges[k].next = literalsAt(groups[k], i+1, fold)
		}
	}

	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		r := s.Rune()
		if fold {
			r = foldKey(r)
		}
		for _, e := range edges {
			if e.r != r {
				continue
			}
			if e.accepts {
				return e.next, bytescan.StateAccept
			}
			return e.next, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

// Rune returns a rule for one rune of c: [c]. It accepts that rune and stops.
// It has one scanning state, so at end of input it only sees the empty input,
// and it rejects the EOF symbol. It panics if c is nil. c must give the same
// answer for the same rune and have no side effect (see Class).
func Rune(c Class) bytescan.Rule {
	mustClass("Rune", c)
	return newPrimitive(oneRune(c), leafNode(classMatcher{c: c, min: 1, max: 1}))
}

// oneRune is the only state of Rune.
func oneRune(c Class) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if c(s.Rune()) {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
}

// RuneRun returns a rule for a run of runes of c: [c]+. It is the shorthand
// OneOrMore(Rune(c)), with the same token streams and cost. It panics if c is
// nil. c must give the same answer for the same rune and have no side effect
// (see Class).
func RuneRun(c Class) bytescan.Rule {
	mustClass("RuneRun", c)
	return OneOrMore(Rune(c))
}

// RuneRunMax returns a rule for a run of one to n runes of c: [c]{1,n}. It
// accepts after each rune and stops at the n-th, so a longer run of c is lexed
// as several matches in a row, each of at most n runes. That bounds the
// token of a stream that never ends a run. At end of input it rejects the
// EOF symbol, so the run read so far stays the match. It panics if c is nil
// or n is below 1. c must give the same answer for the same rune and have no
// side effect (see Class).
//
// RuneRunMax(c, n) matches what Repeat(Rune(c), 1, n) matches, but it counts
// the runes in one state instead of holding one copy of Rune(c) per count. Its
// construction cost is the same for every n, and n has no limit of 1000.
//
// Cost: 1.0 leaf calls per input symbol on a run of c (TestCombinatorCost).
func RuneRunMax(c Class, n int) bytescan.Rule {
	mustClass("RuneRunMax", c)
	if n < 1 {
		panic(fmt.Sprintf("core.RuneRunMax: n is %d, want at least 1", n))
	}
	return newCombinator("RuneRunMax", leafNode(classMatcher{c: c, min: 1, max: n}))
}

// Keyword returns a rule for s(?!c), in Perl notation: the literal s, matched
// only when the symbol after it is not in c. With c = ASCIIWord it is s\b for a
// keyword that ends in a word rune. It decides by one symbol of lookahead and
// never accepts before it has decided: after s it excludes the symbol it read
// and accepts s, or it rejects when that symbol is in c. At end of input
// before the end of s it rejects the EOF symbol, so it matches nothing; at end
// of input after s it excludes the EOF symbol and accepts s. It panics if s is
// empty or c is nil. c must give the same answer for the same rune and have
// no side effect (see Class).
func Keyword(s string, c Class) bytescan.Rule {
	if s == "" {
		panic("core.Keyword: s must not be empty")
	}
	mustClass("Keyword", c)
	lit := []rune(s)
	n := &node{kind: nodeNotFollowedBy, parts: []*node{
		leafNode(literalMatcher{lit: lit}),
		leafNode(classMatcher{c: c, min: 1, max: 1}),
	}}
	return newPrimitive(keywordChain(lit, c)[0], n)
}

// keywordChain builds the states of Keyword once, when the rule is
// constructed: state i reads rune i of lit and continues in state i+1, and
// after the last rune it continues in the keywordEnd state, built once too.
func keywordChain(lit []rune, continues Class) []bytescan.Rule {
	end := keywordEnd(continues)
	chain := make([]bytescan.Rule, len(lit))
	for i := range lit {
		i := i // go.mod predates per-iteration loop variables
		last := i == len(lit)-1
		chain[i] = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() {
				return nil, bytescan.StateReject
			}
			if s.Rune() == lit[i] {
				if last {
					return end, bytescan.StateContinue
				}
				return chain[i+1], bytescan.StateContinue
			}
			return nil, bytescan.StateReject
		}
	}
	return chain
}

// keywordEnd is the state of Keyword after the keyword. It decides by one
// symbol of lookahead.
func keywordEnd(continues Class) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return PushBack(1, bytescan.StateAccept) // the EOF symbol
		}
		if continues(s.Rune()) {
			return nil, bytescan.StateReject
		}
		return PushBack(1, bytescan.StateAccept) // the symbol after the keyword
	}
}
