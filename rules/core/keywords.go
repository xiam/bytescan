package core

import (
	"github.com/xiam/bytescan"
)

// Keywords returns a rule for any of the words, matched only when no ASCII
// word rune follows: (?:w1|w2|...)(?!\w), in Perl notation, which is
// (?:w1|w2|...)\b for words that end in a word rune. The guard ASCIIWord is
// the class that Keyword takes for the keywords of most programming
// languages, the runes that continue an identifier such as
// base.ASCIIIdentifier; KeywordsWithGuard takes another class.
//
// Each word counts only where the guard does not follow it, and of those the
// rule matches the longest: on "elseif x", Keywords("else", "elseif") matches
// "elseif", and on "elsex" it matches nothing. It decides by one symbol of
// lookahead and never accepts before it has decided. At end of input before
// the end of a word it rejects the EOF symbol; at end of input after a word it
// excludes the EOF symbol and accepts the word. It matches what
// Choice(Keyword(w1, ASCIIWord), Keyword(w2, ASCIIWord), ...) matches, and
// it is one rule: building it and running it cost less than that Choice
// (TestKeywordsCost). It panics if no word is given or a word is empty.
func Keywords(words ...string) bytescan.Rule {
	return keywords("Keywords", ASCIIWord, words, false)
}

// KeywordsWithGuard returns a rule for any of the words, matched only when
// the symbol after the word is not in c: (?:w1|w2|...)(?!c), in Perl notation.
// It is Keywords with the guard class c, for a language whose identifiers
// continue with other runes, such as the Unicode identifier parts of SQL. It
// panics if c is nil, no word is given or a word is empty. c must give the
// same answer for the same rune and have no side effect (see Class).
func KeywordsWithGuard(c Class, words ...string) bytescan.Rule {
	mustClass("KeywordsWithGuard", c)
	return keywords("KeywordsWithGuard", c, words, false)
}

// KeywordsIgnoreCase returns a rule for any of the words under Unicode simple
// case folding, matched only when no ASCII word rune follows:
// (?i:w1|w2|...)(?!\w), in Perl notation. It folds as LiteralIgnoreCase does,
// so 'ſ' (U+017F) matches 's' and 'K' (U+212A) matches 'k'; there is no full
// case folding, so "ß" does not match "ss". The guard reads the rune of the
// input as it is, not folded. Otherwise it behaves as Keywords. It panics if
// no word is given or a word is empty.
func KeywordsIgnoreCase(words ...string) bytescan.Rule {
	return keywords("KeywordsIgnoreCase", ASCIIWord, words, true)
}

// KeywordsIgnoreCaseWithGuard returns a rule for any of the words under
// Unicode simple case folding, matched only when the symbol after the word is
// not in c: (?i:w1|w2|...)(?!c), in Perl notation. It is KeywordsIgnoreCase
// with the guard class c. It panics if c is nil, no word is given or a word is
// empty. c must give the same answer for the same rune and have no side
// effect (see Class).
func KeywordsIgnoreCaseWithGuard(c Class, words ...string) bytescan.Rule {
	mustClass("KeywordsIgnoreCaseWithGuard", c)
	return keywords("KeywordsIgnoreCaseWithGuard", c, words, true)
}

// keywords builds the keyword set of the constructor fn: the words, folded
// when fold is set, with the guard class c. A combinator composes its node,
// the trie of AnyLiteral or AnyLiteralIgnoreCase not followed by one rune of
// c; registered alone, the rule runs the states of a keywordTable built from
// the same trie.
func keywords(fn string, c Class, words []string, fold bool) bytescan.Rule {
	var lits [][]rune
	if fold {
		lits = foldedLiterals(fn, words)
	} else {
		if len(words) == 0 {
			panic("core." + fn + ": no word")
		}
		lits = make([][]rune, 0, len(words))
		for _, w := range words {
			if w == "" {
				panic("core." + fn + ": a word is empty")
			}
			lits = append(lits, []rune(w))
		}
	}
	m := newLiteralsMatcher(lits, fold)
	n := &node{kind: nodeNotFollowedBy, parts: []*node{
		leafNode(m),
		leafNode(classMatcher{c: c, min: 1, max: 1}),
	}}
	return newPrimitive(newKeywordTable(m, c).rules[0], n)
}

// keywordTable holds the states of a keyword set registered alone, built
// once and when the rule is constructed. A state is a node of the trie of the
// words with a fallback: the length of the longest word that ends above the
// node, on its path, and that the guard does not follow, or -1 for none. That
// word is known once the rune after it is read, and it is the match when no
// longer word completes; the state then settles on it with one pushback
// chain. A step returns a prebuilt Rule, and it allocates nothing while that
// chain excludes at most 8 symbols.
//
// A node at depth d whose path has e word ends above it has e+1 states, one
// per fallback: -1, then the depths of those ends, from the root down. The
// child of a node keeps the index of the fallback, and when the node ends a
// word that the guard does not follow, the child takes the next index, that
// word. The tables are flat, so the build makes a few allocations plus one
// per state.
type keywordTable struct {
	fold   bool // the runes of steps are fold keys
	guard  Class
	states []keywordState
	steps  []keywordStep
	rules  []bytescan.Rule // rules[i] is the Rule of states[i]
}

// keywordState is one state of a keywordTable.
type keywordState struct {
	end      bool  // the node ends a word
	fallback int32 // the length of the word to fall back to, or -1
	back     int32 // the symbols that a settle on fallback excludes
	steps    []keywordStep
}

// keywordStep is a transition of a keywordState on the rune r, which is a
// fold key when the set folds: to the state keep when the node ends no word
// or the guard follows it, and to the state valid when the node ends a word
// that the guard does not follow.
type keywordStep struct {
	r           rune
	keep, valid int32
}

// newKeywordTable builds the states of the keyword set of the trie of m, with
// the guard class guard.
func newKeywordTable(m literalsMatcher, guard Class) *keywordTable {
	t := &keywordTable{fold: m.fold, guard: guard}

	// First pass: the first state of each node, and the totals.
	first := make([]int, len(m.trie))
	nStates, nSteps := 0, 0
	var count func(at, ends int)
	count = func(at, ends int) {
		first[at] = nStates
		nStates += ends + 1
		nSteps += (ends + 1) * len(m.trie[at].next)
		if m.trie[at].end {
			ends++
		}
		for _, child := range m.trie[at].next {
			count(child, ends)
		}
	}
	count(0, 0)

	t.states = make([]keywordState, nStates)
	t.steps = make([]keywordStep, 0, nSteps)
	t.rules = make([]bytescan.Rule, nStates)

	// Second pass: the states of each node. fallbacks holds the depths of
	// the word ends above the node, after -1.
	fallbacks := []int{-1}
	var fill func(at, depth int)
	fill = func(at, depth int) {
		tn := m.trie[at]
		for k, fb := range fallbacks {
			i := first[at] + k
			from := len(t.steps)
			for r, child := range tn.next {
				st := keywordStep{r: r, keep: int32(first[child] + k)}
				if tn.end {
					st.valid = int32(first[child] + len(fallbacks))
				}
				t.steps = append(t.steps, st)
			}
			t.states[i] = keywordState{
				end:      tn.end,
				fallback: int32(fb),
				back:     int32(depth + 1 - fb),
				steps:    t.steps[from:len(t.steps):len(t.steps)],
			}
			t.rules[i] = t.state(i)
		}
		if tn.end {
			fallbacks = append(fallbacks, depth)
		}
		for _, child := range tn.next {
			fill(child, depth+1)
		}
		if tn.end {
			fallbacks = fallbacks[:len(fallbacks)-1]
		}
	}
	fill(0, 0)
	return t
}

// state returns the Rule of state i.
func (t *keywordTable) state(i int) bytescan.Rule {
	st := &t.states[i]
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			if st.end {
				return PushBack(1, bytescan.StateAccept) // the EOF symbol
			}
			if st.fallback >= 0 {
				return PushBack(int(st.back), bytescan.StateAccept) // the runes after the fallback word, and the EOF symbol
			}
			return nil, bytescan.StateReject
		}
		r := s.Rune()
		valid := st.end && !t.guard(r)
		key := r
		if t.fold {
			key = foldKey(r)
		}
		for _, step := range st.steps {
			if step.r != key {
				continue
			}
			if valid {
				return t.rules[step.valid], bytescan.StateContinue
			}
			return t.rules[step.keep], bytescan.StateContinue
		}
		if valid {
			return PushBack(1, bytescan.StateAccept) // the rune after the word
		}
		if st.fallback >= 0 {
			return PushBack(int(st.back), bytescan.StateAccept) // the runes after the fallback word
		}
		return nil, bytescan.StateReject
	}
}
