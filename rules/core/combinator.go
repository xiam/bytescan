package core

import (
	"fmt"

	"github.com/xiam/bytescan"
)

// newCombinator is the one construction path of every combinator. It
// compiles the node graph n for the combinator name and returns the rule that
// runs it: the table of a pure core rule, or the engine (newRuleOf). The
// returned rule answers the handshake with n, so a combinator that receives
// it composes n (see handshake.go).
func newCombinator(name string, n *node) bytescan.Rule {
	return answering(n, newRuleOf(compile(name, n)))
}

// Sequence returns a rule for the sequence r1r2...: r1, then r2 from each end of
// r1, and so on. It matches the longest such text:
// Sequence(ZeroOrMore(Rune(RuneSet("a"))), Literal("ab")) is a*ab and matches all of
// "aaab". At end of input it ends with the longest sequence read so far, and
// with nothing if the input ends before the last rule has matched. It panics
// if no rule is given or a rule is nil.
//
// Cost: a Sequence of pure core rules runs on a table (see Cost in the
// package documentation). A step of the table on a rune up to U+00FF, or on a
// rune whose step the cache of the table keeps, calls no leaf; a cache miss
// is one step of the engine. On the
// engine, each rule runs from each position where the rule before it ends, and
// two runs of one core rule in the same state are merged. On the engine,
// Sequence(Rune(ASCIILetter), RuneRun(ASCIIWord)) makes 1.0 leaf calls per
// input symbol on a run of letters (TestCombinatorCost).
func Sequence(rs ...bytescan.Rule) bytescan.Rule {
	return newCombinator("Sequence", &node{kind: nodeSeq, parts: nodesOf("Sequence", rs)})
}

// Choice returns a rule for the alternation r1|r2|... with the longest match: on
// "ifelse", Choice(Literal("if"), Literal("ifelse")) matches "ifelse", whatever
// the order of the rules. At end of input it ends with the longest match of
// any rule. It panics if no rule is given or a rule is nil.
//
// Cost: a Choice of pure core rules runs on a table (see Cost in the package
// documentation). A step of the table on a rune up to U+00FF, or on a rune
// whose step the cache of the table keeps, calls no leaf; a cache miss is one
// step of the engine. On the
// engine, every rule reads each symbol while it can still match. On the
// engine, Choice(Literal("if"), Literal("ifelse"), Literal("for")) makes 1.5
// leaf calls per input symbol on repeated "ifelse" (TestCombinatorCost).
func Choice(rs ...bytescan.Rule) bytescan.Rule {
	return newCombinator("Choice", &node{kind: nodeAlt, parts: nodesOf("Choice", rs)})
}

// Optional returns a rule for (?:r)?: r or the empty text. The empty text lets the
// next rule of a Sequence start where Optional starts. Registered alone, Optional(r) matches
// what r matches, because a token is never empty. At end of input it ends as
// r does. It panics if r is nil.
//
// Cost: that of r; Optional reads no symbol itself. Optional of a pure core
// rule runs on a table (see Cost in the package documentation). A step of the
// table on a rune up to U+00FF, or on a rune whose step the cache of the table
// keeps, calls no leaf; a cache miss is one step of the engine. On the engine,
// Sequence(Optional(Rune(RuneSet("+-"))), RuneRun(ASCIIDigit)) makes 1.0 leaf
// calls per input symbol on a signed number (TestCombinatorCost).
func Optional(r bytescan.Rule) bytescan.Rule {
	return newCombinator("Optional", &node{kind: nodeOpt, parts: []*node{nodeOf("Optional", 0, r)}})
}

// ZeroOrMore returns a rule for (?:r)*: r zero or more times, each time from an end
// of the time before. An iteration never starts again at a position where an
// iteration already started, so an r that can match the empty text, such as
// Optional(x), does not loop. Registered alone, ZeroOrMore(r) matches what OneOrMore(r)
// matches, because a token is never empty. At end of input it ends with the
// longest repetition read so far. It panics if r is nil.
//
// Cost: ZeroOrMore of a pure core rule runs on a table (see Cost in the
// package documentation). A step of the table on a rune up to U+00FF, or on a
// rune whose step the cache of the table keeps, calls no leaf; a cache miss
// is one step of the engine. On the
// engine, one run of r starts from each position where an iteration ends, and
// runs of a core rule in the same state are merged. On the engine,
// Sequence(Rune(ASCIILetter), ZeroOrMore(RuneRun(ASCIIWord))) makes 1.0 leaf
// calls per input symbol on a run of letters (TestCombinatorCost). A rule that
// this package did not build is an opaque part, so the composed rule keeps the
// engine, and the runs of the opaque part cannot be merged. With such a rule
// for \w+ in place of RuneRun(ASCIIWord), 200 letters make 100.5 calls per
// symbol, a cost that grows with the square of the token length
// (TestCombinatorCost, TestOpaqueZeroOrMoreIsNotMerged).
func ZeroOrMore(r bytescan.Rule) bytescan.Rule {
	return newCombinator("ZeroOrMore", &node{kind: nodeMany, parts: []*node{nodeOf("ZeroOrMore", 0, r)}})
}

// OneOrMore returns a rule for (?:r)+: r one or more times, each time from an end
// of the time before; it is Sequence(r, ZeroOrMore(r)). At end of input it ends with the
// longest repetition read so far, and with nothing before the first match of
// r. It panics if r is nil.
//
// Cost: as ZeroOrMore, on the table and on the engine. On the engine,
// OneOrMore(Literal("ab")) makes 1.0 leaf calls per input symbol on repeated
// "ab" (TestCombinatorCost).
func OneOrMore(r bytescan.Rule) bytescan.Rule {
	return newCombinator("OneOrMore", &node{kind: nodeMany1, parts: []*node{nodeOf("OneOrMore", 0, r)}})
}

// Repeat returns a rule for (?:r){min,max}: r from min to max times, each
// time from an end of the time before, or (?:r){min,} when max is negative.
// With min = 0 it can match the empty text, as Optional can. At end of input it
// ends with the longest repetition of at least min times read so far, and
// with nothing before that. It panics if r is nil, if min is negative, if max
// is 0, if 0 < max < min, if min or max is above 1000 (the limit of Go's
// regexp), or if the composed rule is too large: the rule holds one copy of r
// per count.
//
// A bounded run of one class, Repeat(Rune(c), 1, n), is RuneRunMax(c, n),
// which counts in one state: it costs the same to build for every n and has
// no limit of 1000.
//
// The removed class form Repeat(c, min, max) is Repeat(Rune(c), min, max). It
// differs in two ways: Repeat(Rune(c), 0, n) is accepted where the class form
// panicked, and counts above 1000 panic where the class form accepted them.
//
// Cost: Repeat of a pure core rule runs on a table (see Cost in the package
// documentation). A step of the table on a rune up to U+00FF, or on a rune
// whose step the cache of the table keeps, calls no leaf; a cache miss is one
// step of the engine. The counts can add states and build work to the table,
// and large counts can put the table over the build budget; the rule then
// keeps the engine (TestTableBudgetFits, TestTableBudgetBoundsClosures). On the
// engine, one run of r per count starts from each position where the count
// before it ends. On the engine, Repeat(Rune(ASCIIDigit), 2, 4) makes 1.0 leaf
// calls per input symbol on a run of digits (TestCombinatorCost).
func Repeat(r bytescan.Rule, min, max int) bytescan.Rule {
	switch {
	case min < 0:
		panic(fmt.Sprintf("core.Repeat: min is %d, want at least 0", min))
	case max == 0:
		panic("core.Repeat: max is 0, the rule would match only the empty text")
	case max > 0 && max < min:
		panic(fmt.Sprintf("core.Repeat: max %d is below min %d", max, min))
	case min > maxRepeatCount || max > maxRepeatCount:
		panic(fmt.Sprintf("core.Repeat: a count is above %d", maxRepeatCount))
	}
	part := nodeOf("Repeat", 0, r)
	return newCombinator("Repeat", &node{kind: nodeRepeat, parts: []*node{part}, min: min, max: max})
}

// FollowedBy returns a rule for r(?=ahead), in Perl notation: an end of r
// counts only if ahead matches from that end. The match does not include the
// text of ahead. Each end of r waits until ahead first matches from it, or
// until ahead can no longer match, so the rule reads past its match and then
// excludes the extra symbols with one terminal pushback chain. At end of
// input an end of r counts only if ahead has matched before the end of
// input. It panics if r or ahead is nil.
//
// Cost: a rule with a lookahead keeps the engine (see Tables in the package
// documentation), so this cost is the cost of the engine. It is that of r,
// plus one run of ahead from each end of r until it first matches.
// FollowedBy(Literal("abcd"), Literal("ef")) makes 1.33 leaf calls
// per input symbol on repeated "abcdef", and the pushback chain of each match
// excludes the 2 symbols of "ef" (TestCombinatorCost). The runs of ahead
// from different ends are not merged: FollowedBy(RuneRun(RuneSet("a")),
// Sequence(RuneRun(RuneSet("a")), Literal("b"))) on n runes 'a' and a 'b' makes about
// n leaf calls per symbol (TestLookaheadIsNotMerged).
func FollowedBy(r, ahead bytescan.Rule) bytescan.Rule {
	parts := []*node{nodeOf("FollowedBy", 0, r), nodeOf("FollowedBy", 1, ahead)}
	return newCombinator("FollowedBy", &node{kind: nodeFollowedBy, parts: parts})
}

// NotFollowedBy returns a rule for r(?!ahead), in Perl notation: an end of r
// counts only if ahead does not match from that end. The match does not
// include the symbols that ahead reads. Each end of r waits until ahead can no
// longer match from it; an ahead that can always grow, such as RuneRun(ASCIIWord),
// reads to the end of its run. At end of input every pending ahead stops
// without a match, so the ends of r that wait for it count. It panics if r or
// ahead is nil.
//
// Cost: a rule with a lookahead keeps the engine (see Tables in the package
// documentation), so this cost is the cost of the engine. It is that of r,
// plus one run of ahead from each end of r until it matches or can no longer
// match. NotFollowedBy(Literal("for"), Rune(ASCIIWord)), which
// matches as Keyword("for", ASCIIWord), makes 1.25 leaf calls per input symbol on
// repeated "for " (TestCombinatorCost). As for FollowedBy, the runs of ahead
// from different ends are not merged.
func NotFollowedBy(r, ahead bytescan.Rule) bytescan.Rule {
	parts := []*node{nodeOf("NotFollowedBy", 0, r), nodeOf("NotFollowedBy", 1, ahead)}
	return newCombinator("NotFollowedBy", &node{kind: nodeNotFollowedBy, parts: parts})
}

// AtLineStart returns a rule for (?m:^)r: r, matched only when its first
// symbol is at the beginning of a line. It reads the beginning-of-line flag of
// that symbol (bytescan.Symbol.IsBOL), which the scanner sets at the start of
// the input and after each '\n'. When the first symbol is not at the
// beginning of a line, the rule rejects it; otherwise it behaves as r. At end
// of input before the first symbol of r it rejects the EOF symbol, so it
// matches nothing; after that it ends as r does. It panics if r is nil.
//
// AtLineStart composes as a rule that this package did not build: inside a
// combinator it is an opaque part (see the package documentation), which
// sees the symbols with their flags at the position where it starts.
//
// Cost: that of r, plus one flag test on the first symbol. Registered alone,
// AtLineStart hands its first symbol to r, and r reads the rest of the token,
// so a pure core r runs on its own table (TestTableRulesTabled). Inside a
// combinator, AtLineStart is an opaque part, and that combinator keeps the
// engine (program.needsLog).
func AtLineStart(r bytescan.Rule) bytescan.Rule {
	if r == nil {
		panic("core.AtLineStart: rule is nil")
	}
	return lineStart(r)
}

// lineStart is the only state of AtLineStart: it reads the first symbol and
// hands a symbol at the beginning of a line to r. A probe carries no flag, so
// it is rejected and AtLineStart composes as an opaque part.
func lineStart(r bytescan.Rule) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.IsBOL() {
			return r(s) // r reads its first symbol
		}
		return nil, bytescan.StateReject
	}
}
