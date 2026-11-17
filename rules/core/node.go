package core

import (
	"github.com/xiam/bytescan"
)

// A node is one part of the internal node graph of a core rule. Every rule
// that this package builds carries its node (see handshake.go). A combinator
// builds its own node from the nodes of its arguments and compiles the graph
// to one program (program.go), which the engine runs (engine.go). The graph
// is immutable once built, so one node can be shared by any number of rules.
type node struct {
	kind nodeKind

	// leaf is the part that reads symbols, for kind nodeLeaf.
	leaf *leaf

	// parts are the sub-nodes, in order, for every other kind. The
	// lookahead kinds have two: the rule and the lookahead.
	parts []*node

	// min and max are the bounds of nodeRepeat; a negative max is no maximum.
	min, max int
}

// nodeKind says what a node matches.
type nodeKind int

const (
	nodeLeaf          nodeKind = iota // the ends of one leaf
	nodeSeq                           // parts[0] parts[1] ...
	nodeAlt                           // parts[0]|parts[1]|...
	nodeOpt                           // (?:parts[0])?
	nodeMany                          // (?:parts[0])*
	nodeMany1                         // (?:parts[0])+
	nodeRepeat                        // (?:parts[0]){min,max}
	nodeFollowedBy                    // parts[0](?=parts[1])
	nodeNotFollowedBy                 // parts[0](?!parts[1])
)

// A leaf is the only kind of part that reads symbols; the other nodes only
// connect leaves. A leaf is either the matcher of a core primitive or an
// opaque bytescan.Rule, which the engine runs as the VM runs a registered
// rule.
type leaf struct {
	// m is the matcher of a core primitive, or nil.
	m matcher

	// rule is the start state of an opaque rule, when m is nil.
	rule bytescan.Rule
}

// A matcher is the structured form of a core primitive: a deterministic
// automaton over runes whose states are comparable ints. State 0 is the start
// state. The engine merges two threads of one matcher in the same state, which
// keeps a composition of core rules linear in the input.
type matcher interface {
	// step returns the state after the rune r in state st, whether the runes
	// read from the start state up to r match, and whether a longer match is
	// still possible from the returned state. A matcher sees no EOF symbol.
	step(st int, r rune) (next int, match, more bool)
}

// leafNode returns the node of the matcher m.
func leafNode(m matcher) *node {
	return &node{kind: nodeLeaf, leaf: &leaf{m: m}}
}

// opaqueNode returns the node of a rule that this package did not build.
func opaqueNode(r bytescan.Rule) *node {
	return &node{kind: nodeLeaf, leaf: &leaf{rule: r}}
}

// literalMatcher is the matcher of Literal. Its state is the number of runes
// of lit read so far.
type literalMatcher struct {
	lit []rune
}

func (m literalMatcher) step(i int, r rune) (int, bool, bool) {
	if r != m.lit[i] {
		return 0, false, false
	}
	return i + 1, i+1 == len(m.lit), i+1 < len(m.lit)
}

// literalFoldMatcher is the matcher of LiteralIgnoreCase. Its state is the number
// of runes of lit read so far.
type literalFoldMatcher struct {
	lit []rune
}

func (m literalFoldMatcher) step(i int, r rune) (int, bool, bool) {
	if !isFoldOf(r, m.lit[i]) {
		return 0, false, false
	}
	return i + 1, i+1 == len(m.lit), i+1 < len(m.lit)
}

// literalsMatcher is the matcher of AnyLiteral, AnyLiteralIgnoreCase and the
// keyword sets: a trie of the literals. Its state is the index of the trie
// node of the runes read so far. With fold set, the trie holds fold keys
// (foldedLiterals) and step looks up the fold key of the rune it reads.
type literalsMatcher struct {
	trie []trieNode
	fold bool
}

// trieNode is one node of the trie of literalsMatcher.
type trieNode struct {
	next map[rune]int // the child for each rune
	end  bool         // a literal ends here
}

// newLiteralsMatcher returns the matcher of the literals lits, which hold
// fold keys when fold is set.
func newLiteralsMatcher(lits [][]rune, fold bool) literalsMatcher {
	trie := []trieNode{{next: map[rune]int{}}}
	for _, lit := range lits {
		at := 0
		for _, r := range lit {
			child, ok := trie[at].next[r]
			if !ok {
				child = len(trie)
				trie[at].next[r] = child
				trie = append(trie, trieNode{next: map[rune]int{}})
			}
			at = child
		}
		trie[at].end = true
	}
	return literalsMatcher{trie: trie, fold: fold}
}

func (m literalsMatcher) step(at int, r rune) (int, bool, bool) {
	if m.fold {
		r = foldKey(r)
	}
	child, ok := m.trie[at].next[r]
	if !ok {
		return 0, false, false
	}
	return child, m.trie[child].end, len(m.trie[child].next) > 0
}

// classMatcher is the matcher of Rune, RuneRunMax and Keyword: min to max runes of c,
// with a negative max for no maximum. Its state is the number of runes read.
// Without a maximum it stops counting at min-1, because every state from
// there on matches after one more rune and behaves the same. RuneRun does not
// use it: RuneRun(c) is OneOrMore(Rune(c)).
type classMatcher struct {
	c        Class
	min, max int
}

func (m classMatcher) step(n int, r rune) (int, bool, bool) {
	if !m.c(r) {
		return 0, false, false
	}
	read := n + 1
	if m.max < 0 {
		return min(read, m.min-1), read >= m.min, true
	}
	return read, read >= m.min, read < m.max
}
