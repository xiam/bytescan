package core

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/xiam/bytescan"
)

// The handshake lets a combinator recover the node of a core rule from a
// plain bytescan.Rule, so that every exported signature stays bytescan.Rule.
//
// At construction, a combinator calls each Rule argument once, through
// callProbe, with a probe: a Symbol that the VM never produces, because its
// rune is below bytescan.RuneEOF. A rule that this package built answers a
// probe by storing its node in the mailbox under the probe's rune, and only
// when callProbe called it directly. Any other rule is an opaque leaf: its
// verdict on the probe is ignored, and a panic is recovered. A rule that
// passes the probe on to a core rule does not make itself that core rule,
// because the core rule then sees a caller other than callProbe.
//
// The probe is the only extra call that a combinator makes to a rule that this
// package did not build. Such a rule is called once per combinator argument,
// at construction, which is harmless for a rule that follows practice 6 of
// docs/rule-authoring.md.

var (
	// mailbox maps the rune of a probe in flight to the node of the core
	// rule that answered it.
	mailbox sync.Map

	// probes counts the probes sent, to give each one its own rune.
	probes atomic.Uint32

	// callProbeName is the name of callProbe, the only caller that a probe
	// answer accepts.
	callProbeName = runtime.FuncForPC(reflect.ValueOf(callProbe).Pointer()).Name()
)

// probeRunes is the number of distinct probe runes: every rune from
// bytescan.RuneEOF-1 down to math.MinInt32.
const probeRunes = 1<<31 - 1

// isProbe reports whether s is a probe. The VM never produces one.
func isProbe(s bytescan.Symbol) bool {
	return s.Rune() < bytescan.RuneEOF
}

// answering returns the start state of a core rule whose node is n and whose
// first symbol start reads. The returned rule answers a probe with n, and
// otherwise behaves as start.
func answering(n *node, start bytescan.Rule) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if isProbe(s) {
			answerProbe(n, s)
			return nil, bytescan.StateReject
		}
		return start(s)
	}
}

// answerProbe stores n for the probe s if the core rule that received s was
// called by callProbe itself.
//
//go:noinline
func answerProbe(n *node, s bytescan.Symbol) {
	// The frames are answerProbe, the rule that answering returned, and the
	// caller of that rule.
	pcs := make([]uintptr, 3)
	pcs = pcs[:runtime.Callers(1, pcs)]
	frames := runtime.CallersFrames(pcs)
	var caller runtime.Frame
	for i := 0; i < 3; i++ {
		frame, more := frames.Next()
		caller = frame
		if !more {
			break
		}
	}
	if caller.Function == callProbeName {
		mailbox.Store(s.Rune(), n)
	}
}

// nodeOf returns the node of the rule r, an argument of the combinator name:
// the node of a core rule, or an opaque leaf for any other rule. It panics if
// r is nil.
func nodeOf(name string, i int, r bytescan.Rule) *node {
	if r == nil {
		panic(fmt.Sprintf("core.%s: rule %d is nil", name, i+1))
	}
	id := bytescan.RuneEOF - 1 - rune(probes.Add(1)%probeRunes)
	callProbe(r, bytescan.NewSymbol(id, bytescan.FlagNone))
	if n, ok := mailbox.LoadAndDelete(id); ok {
		return n.(*node)
	}
	return opaqueNode(r)
}

// nodesOf returns the nodes of the rules rs, the arguments of the combinator
// name. It panics if rs is empty or a rule is nil.
func nodesOf(name string, rs []bytescan.Rule) []*node {
	if len(rs) == 0 {
		panic(fmt.Sprintf("core.%s: no rule", name))
	}
	nodes := make([]*node, len(rs))
	for i, r := range rs {
		nodes[i] = nodeOf(name, i, r)
	}
	return nodes
}

// callProbe calls r with the probe s and ignores the verdict. A panic of r is
// recovered: a rule that this package did not build is free (principle 3).
//
//go:noinline
func callProbe(r bytescan.Rule, s bytescan.Symbol) {
	defer func() { _ = recover() }()
	_, _ = r(s)
}
