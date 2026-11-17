package core

import (
	"fmt"
)

// maxProgramSize is the largest number of instructions that a composed rule
// compiles to. Repeat copies its rule once per count, so nested counted
// repetitions grow quickly; a larger rule panics at construction.
const maxProgramSize = 1 << 16

// maxRepeatCount is the largest min and max of Repeat, the limit that Go's
// regexp sets for {min,max}.
const maxRepeatCount = 1000

// A program is the compiled form of a node graph: a nondeterministic
// automaton in the style of Thompson's construction, whose only
// symbol-reading instructions are leaves. The engine runs it from start.
type program struct {
	insts []inst
	start int

	// needsLog is false when no instruction is an opaque leaf or a
	// lookahead. Only those resume the program at an earlier position, so a
	// program without them reads only the current symbol and keeps no
	// symbol log.
	needsLog bool
}

// An inst is one instruction of a program.
type inst struct {
	op opcode

	// leaf is the leaf of opLeaf.
	leaf *leaf

	// next is where opLeaf continues after each end of its leaf, and where
	// opLook continues when the lookahead decides for the match.
	next int

	// targets are the instructions where opFork continues, all of them.
	targets []int

	// ahead is the first instruction of the lookahead program of opLook.
	// The lookahead program ends in opLookEnd.
	ahead int

	// negated makes opLook a negative lookahead.
	negated bool
}

// opcode says what an instruction does.
type opcode int

const (
	opMatch   opcode = iota // the composed rule matches the symbols read so far
	opLeaf                  // run leaf; continue at next after each of its ends
	opFork                  // continue at every instruction of targets
	opLook                  // start a lookahead at ahead; continue at next when it decides for the match
	opLookEnd               // the innermost lookahead matched
)

// compile returns the program of the node graph n. The name of the
// constructor that builds the rule is in the panic for a program that is too
// large.
func compile(name string, n *node) *program {
	c := &compiler{name: name}
	match := c.add(inst{op: opMatch})
	start := c.emit(n, match)
	return &program{insts: c.insts, start: start, needsLog: needsLog(c.insts)}
}

// needsLog reports whether a program of insts can resume at an earlier
// position: whether one of them is an opaque leaf or a lookahead.
func needsLog(insts []inst) bool {
	for _, in := range insts {
		if in.op == opLook || (in.op == opLeaf && in.leaf.m == nil) {
			return true
		}
	}
	return false
}

// compiler builds the instructions of a program.
type compiler struct {
	name  string
	insts []inst
}

// add appends one instruction and returns its index.
func (c *compiler) add(in inst) int {
	if len(c.insts) == maxProgramSize {
		panic(fmt.Sprintf("core.%s: the composed rule needs more than %d instructions", c.name, maxProgramSize))
	}
	c.insts = append(c.insts, in)
	return len(c.insts) - 1
}

// emit compiles n so that each match of n continues at next, and returns the
// first instruction of n.
func (c *compiler) emit(n *node, next int) int {
	switch n.kind {
	case nodeLeaf:
		return c.add(inst{op: opLeaf, leaf: n.leaf, next: next})

	case nodeSeq:
		for i := len(n.parts) - 1; i >= 0; i-- {
			next = c.emit(n.parts[i], next)
		}
		return next

	case nodeAlt:
		targets := make([]int, len(n.parts))
		for i, part := range n.parts {
			targets[i] = c.emit(part, next)
		}
		return c.add(inst{op: opFork, targets: targets})

	case nodeOpt:
		return c.add(inst{op: opFork, targets: []int{c.emit(n.parts[0], next), next}})

	case nodeMany:
		loop := c.add(inst{op: opFork})
		c.insts[loop].targets = []int{c.emit(n.parts[0], loop), next}
		return loop

	case nodeMany1:
		loop := c.add(inst{op: opFork})
		first := c.emit(n.parts[0], loop)
		c.insts[loop].targets = []int{first, next}
		return first

	case nodeRepeat:
		return c.emitRepeat(n, next)

	case nodeFollowedBy, nodeNotFollowedBy:
		look := c.add(inst{op: opLook, next: next, negated: n.kind == nodeNotFollowedBy})
		c.insts[look].ahead = c.emit(n.parts[1], c.add(inst{op: opLookEnd}))
		return c.emit(n.parts[0], look)
	}
	panic(fmt.Sprintf("core.%s: unknown node kind %d", c.name, n.kind))
}

// emitRepeat compiles (?:r){min,max} as min copies of r followed by
// (?:r)* when there is no maximum, or by max-min nested optional copies,
// (?:r(?:r)?)?, as Go's regexp does.
func (c *compiler) emitRepeat(n *node, next int) int {
	r := n.parts[0]
	tail := next
	if n.max < 0 {
		tail = c.emit(&node{kind: nodeMany, parts: n.parts}, next)
	} else {
		for i := n.min; i < n.max; i++ {
			tail = c.add(inst{op: opFork, targets: []int{c.emit(r, tail), next}})
		}
	}
	for i := 0; i < n.min; i++ {
		tail = c.emit(r, tail)
	}
	return tail
}
