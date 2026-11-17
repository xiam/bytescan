package core

import (
	"sync"

	"github.com/xiam/bytescan"
)

// The engine runs the program of a composed rule over the symbols of one
// token. Positions count the symbols of the token: position p is after the
// first p symbols. The engine keeps every leaf that can still match as a
// thread and steps all threads in lockstep, one symbol per VM call. An end
// that a leaf reports before the current position (an opaque rule's pushback
// chain, or a lookahead that decided late) resumes the program at that
// position and catches it up on the stored symbols.
//
// The state after each symbol is a snapshot. A snapshot is never changed once
// built, and each Rule value that the composed rule returns holds its own
// snapshot, so the rule keeps its state only in the Rule values it returns
// (principle 1 of docs/specs/design-principles.md and practice 4 of
// docs/rule-authoring.md). The VM sees one rule with delayed acceptance and
// at most one terminal pushback chain (see snapshot.verdict).

// A snapshot is the state of a composed rule after the first n symbols of a
// token.
type snapshot struct {
	prog *program

	// log holds the symbols of the token; the first n are this snapshot's.
	// It is nil when the program does not need it (program.needsLog).
	log *symbolLog
	n   int

	// threads are the leaves that wait at position n for the next symbol.
	threads []thread

	// pending are the negative lookaheads that have not decided yet. Each one
	// has at least one thread.
	pending []*lookahead

	// best is the longest end of the whole program found so far, or 0.
	best int

	// recorded is the longest match that the VM has recorded for the rule,
	// or 0.
	recorded int
}

// A thread is one leaf that waits for the next symbol.
type thread struct {
	// pc is the opLeaf instruction of the leaf.
	pc int

	// state is the matcher state of a core leaf.
	state int

	// rule, start and last belong to an opaque leaf: the Rule value that
	// reads the next symbol, the position where the leaf started, and the
	// longest end that it has reported, which only grows (as the VM's
	// recorded match does).
	rule  bytescan.Rule
	start int
	last  int

	// look is the innermost lookahead that the thread runs for, or nil.
	look *lookahead
}

// A lookahead is one run of the lookahead program of an opLook instruction,
// started at position at by a match of the rule before it.
type lookahead struct {
	pc    int        // the opLook instruction
	at    int        // where the lookahead starts: the end of the rule before it
	outer *lookahead // the lookahead that the opLook instruction runs for, or nil
}

// symbolLog holds the symbols of one token, for a program that can resume at
// an earlier position (program.needsLog). It is shared by the snapshots of
// that token and only grows: a snapshot reads only its first n symbols, which
// never change. mu guards the slice header, so Rule values of one token can
// be called concurrently.
type symbolLog struct {
	mu   sync.Mutex
	syms []bytescan.Symbol
}

// threadKey identifies a thread for merging: two threads with the same key
// read the same symbols in the same way from now on.
type threadKey struct {
	pc, state, start int
	look             *lookahead
}

// key returns the merge key of t. A core leaf in a given state behaves the
// same wherever it started; an opaque leaf is identified by its start.
func (t thread) key() threadKey {
	return threadKey{pc: t.pc, state: t.state, start: t.start, look: t.look}
}

// newSnapshot returns the snapshot of prog before the first symbol of a
// token. It holds no symbol log, so it can be shared by every token.
func newSnapshot(prog *program) snapshot {
	st := &stepper{prog: prog}
	threads := st.settle(st.closure(nil, prog.start, 0, nil))
	return snapshot{prog: prog, threads: threads, pending: st.pending}
}

// rule returns the Rule value that reads the next symbol at position snap.n
// and returns the verdict of the composed rule. The Rule value holds a copy
// of snap: a snapshot is small enough that Go captures it by value, so the
// snapshot and its Rule value are one allocation, and a step whose verdict
// returns no Rule value allocates neither.
func (snap snapshot) rule() bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return snap.next(s).verdict()
	}
}

// next returns the snapshot after the symbol s at position snap.n.
func (snap snapshot) next(s bytescan.Symbol) snapshot {
	// A program that needs no log reads only the symbol at snap.n.
	var log *symbolLog
	var syms []bytescan.Symbol
	if snap.prog.needsLog {
		log, syms = snap.log.extend(snap.n, s)
	}
	// The stepper is scratch for this call only: it comes from a pool and
	// goes back cleared, so it holds no reference once next returns.
	st := stepperPool.Get().(*stepper)
	*st = stepper{
		prog:    snap.prog,
		syms:    syms,
		cur:     s,
		n:       snap.n + 1,
		pending: append([]*lookahead(nil), snap.pending...),
		best:    snap.best,
	}
	threads := st.settle(st.feed(snap.threads, snap.n))
	recorded := snap.recorded
	if st.best == st.n {
		recorded = st.n // verdict accepts everything read
	}
	after := snapshot{
		prog:     snap.prog,
		log:      log,
		n:        st.n,
		threads:  threads,
		pending:  st.pending,
		best:     st.best,
		recorded: recorded,
	}
	*st = stepper{}
	stepperPool.Put(st)
	return after
}

// stepperPool holds the steppers of next. A stepper escapes to the heap
// (the recursion of settle and resume defeats escape analysis), so without
// the pool every step would allocate one. Each call of next takes its own
// stepper, so Rule values stay safe for concurrent use.
var stepperPool = sync.Pool{New: func() any { return new(stepper) }}

// verdict returns what the composed rule tells the VM after the symbols of
// snap. It uses delayed acceptance with terminal fallback ("Lookahead combined
// with a longer match" in docs/specs/rule-vm-contract.md):
//
//   - When the longest end is everything read, the rule accepts, and it
//     continues if a thread is alive. That is never the case at end of input,
//     because the EOF symbol is not part of any match.
//   - Otherwise, while a thread is alive, the rule continues.
//   - When every thread is dead, the rule ends with one pushback chain to the
//     longest end if the VM has not recorded it yet, or it rejects and the
//     recorded match stays.
func (snap snapshot) verdict() (bytescan.Rule, bytescan.State) {
	alive := len(snap.threads) > 0
	switch {
	case snap.best == snap.n:
		if alive {
			return snap.rule(), bytescan.StateAccept
		}
		return nil, bytescan.StateAccept
	case alive:
		return snap.rule(), bytescan.StateContinue
	case snap.best > snap.recorded:
		// No accept is recorded at or after best: the last accept recorded
		// everything read at that time, which was less than best.
		return PushBack(snap.n-snap.best, bytescan.StateAccept) // the symbols after the longest end, the EOF symbol included at end of input
	}
	return nil, bytescan.StateReject
}

// extend returns a log whose first n+1 symbols are the first n of l and then
// s, and those n+1 symbols. It appends to l when l holds exactly n symbols;
// otherwise, when an earlier Rule value of the token is called again, it
// copies the first n to a new log.
func (l *symbolLog) extend(n int, s bytescan.Symbol) (*symbolLog, []bytescan.Symbol) {
	if l != nil {
		l.mu.Lock()
		defer l.mu.Unlock()
		if len(l.syms) == n {
			l.syms = append(l.syms, s)
			return l, l.syms[: n+1 : n+1]
		}
	}
	fresh := &symbolLog{syms: make([]bytescan.Symbol, n, n+16)}
	if l != nil {
		copy(fresh.syms, l.syms[:n])
	}
	fresh.syms = append(fresh.syms, s)
	return fresh, fresh.syms[: n+1 : n+1]
}

// stepper computes the threads of one snapshot from the previous one.
type stepper struct {
	prog *program

	// syms are the symbols of the token read so far, n of them, or nil when
	// the program needs no log. cur is the symbol at n-1, the only one that
	// such a program reads.
	syms []bytescan.Symbol
	cur  bytescan.Symbol
	n    int

	// pending are the negative lookaheads that have not decided.
	pending []*lookahead

	// hit holds every lookahead whose program matched in this step: a
	// positive one has decided for its match and a negative one against
	// it. Their threads are dropped. It is nil until the first hit.
	hit map[*lookahead]bool

	// resumed holds the late ends already resumed in this step. It is nil
	// until the first resumption.
	resumed map[resumeKey]bool

	// started holds the lookaheads started in this step, by instruction,
	// position and outer lookahead. The program reaching the same opLook at
	// the same position for the same outer lookahead joins the lookahead
	// already started, so a repetition of a part that can match the empty
	// text ends even when a lookahead inside it decides later. It is nil
	// until the first lookahead.
	started map[resumeKey]*lookahead

	// best is the longest end of the whole program found so far.
	best int
}

// resumeKey identifies a resumption of the program: where it continues, from
// which position, for which lookahead.
type resumeKey struct {
	pc, at int
	look   *lookahead
}

// feed reads the symbol at position p with every thread in ts, which wait at
// p, and returns the threads that wait at p+1.
func (st *stepper) feed(ts []thread, p int) []thread {
	var out []thread
	for _, t := range ts {
		if !st.dropped(t.look) {
			out = st.read(out, t, p)
		}
	}
	return merge(out)
}

// read reads the symbol at position p with the leaf of t and appends to out
// the threads that wait at p+1: t itself if its leaf can still match, and the
// continuations of each end that the leaf reports.
func (st *stepper) read(out []thread, t thread, p int) []thread {
	in := &st.prog.insts[t.pc]
	s := st.symbol(p)

	if m := in.leaf.m; m != nil {
		if s.IsEOF() {
			return out
		}
		next, match, more := m.step(t.state, s.Rune())
		if more {
			out = append(out, thread{pc: t.pc, state: next, look: t.look})
		}
		if match {
			out = st.closure(out, in.next, p+1, t.look)
		}
		return out
	}

	next, end := st.readOpaque(t, p)
	if next != nil {
		out = append(out, thread{pc: t.pc, rule: next, start: t.start, last: max(t.last, end), look: t.look})
	}
	switch {
	case end <= t.last:
		// No end, or one that does not grow the leaf's match.
	case end == p+1:
		out = st.closure(out, in.next, end, t.look)
	default:
		out = append(out, st.resume(in.next, end, t.look, p+1)...)
	}
	return out
}

// symbol returns the symbol at position p of the token. Without a log, p is
// always the position of the current symbol.
func (st *stepper) symbol(p int) bytescan.Symbol {
	if st.syms == nil {
		return st.cur
	}
	return st.syms[p]
}

// readOpaque calls the Rule value of the opaque thread t with the symbol at
// position p and runs its pushback chain over the stored symbols, as the VM
// does for a registered rule (processor.go). Its stack starts at t.start. It
// returns the Rule value for the next symbol, or nil when the leaf is retired,
// and the end that the leaf accepted, or -1. As in the VM, an
// accept that counts the EOF symbol is discarded, a chain past the start of
// the leaf rejects, and the leaf is retired after the EOF symbol.
func (st *stepper) readOpaque(t thread, p int) (bytescan.Rule, int) {
	s := st.syms[p]
	next, state := callLeaf(t.rule, s)
	excluded := 0
	for state == bytescan.StatePushBack {
		excluded++
		if next == nil || excluded > p+1-t.start {
			next, state = nil, bytescan.StateReject
			break
		}
		next, state = callLeaf(next, st.syms[p+1-excluded])
	}

	end := -1
	switch state {
	case bytescan.StateAccept:
		if !s.IsEOF() || excluded > 0 {
			end = p + 1 - excluded
		}
	case bytescan.StateContinue:
	default:
		next = nil
	}
	if s.IsEOF() {
		next = nil
	}
	return next, end
}

// callLeaf calls the opaque rule r with s. A panic retires only that leaf, as
// the VM retires a registered rule that panics (principle 3).
func callLeaf(r bytescan.Rule, s bytescan.Symbol) (next bytescan.Rule, state bytescan.State) {
	returned := false
	defer func() {
		if !returned {
			_ = recover()
			next, state = nil, bytescan.StateReject
		}
	}()
	next, state = r(s)
	returned = true
	return next, state
}

// resume continues the program at pc from the earlier position at, for the
// lookahead look, and returns the threads that wait at position to (at <= to
// <= st.n): it follows the program at at and reads the stored symbols from at
// to to with the new threads. A resumption that this step has already done
// returns nothing, because its threads are already on their way.
func (st *stepper) resume(pc, at int, look *lookahead, to int) []thread {
	key := resumeKey{pc: pc, at: at, look: look}
	if st.resumed[key] {
		return nil
	}
	if st.resumed == nil {
		st.resumed = map[resumeKey]bool{}
	}
	st.resumed[key] = true

	ts := st.closure(nil, pc, at, look)
	for p := at; p < to; p++ {
		ts = st.feed(ts, p)
	}
	return ts
}

// closure follows the program from pc at position at for the lookahead look,
// through every instruction that reads no symbol, and appends to out the
// threads of the leaves it reaches. It records an end of the whole program in
// st.best, starts the lookaheads it reaches, and decides the lookaheads whose
// program it completes.
func (st *stepper) closure(out []thread, pc, at int, look *lookahead) []thread {
	w := &walk{st: st, at: at, out: out}
	w.visit(pc, look)
	return w.out
}

// walk is one closure: the instructions visited at one position.
type walk struct {
	st   *stepper
	at   int
	out  []thread
	seen keySet[resumeKey]
}

// visit follows the program from pc for the lookahead look. It visits each
// instruction once per lookahead, so a loop that reads no symbol, such as
// (?:a?)*, ends.
func (w *walk) visit(pc int, look *lookahead) {
	if !w.seen.add(resumeKey{pc: pc, look: look}) {
		return
	}

	in := &w.st.prog.insts[pc]
	switch in.op {
	case opLeaf:
		if in.leaf.m != nil {
			w.out = append(w.out, thread{pc: pc, look: look})
			return
		}
		w.out = append(w.out, thread{pc: pc, rule: in.leaf.rule, start: w.at, last: w.at, look: look})

	case opFork:
		for _, target := range in.targets {
			w.visit(target, look)
		}

	case opLook:
		key := resumeKey{pc: pc, at: w.at, look: look}
		if w.st.started[key] != nil {
			return // it decides once for every way the program reached it
		}
		started := &lookahead{pc: pc, at: w.at, outer: look}
		if w.st.started == nil {
			w.st.started = map[resumeKey]*lookahead{}
		}
		w.st.started[key] = started
		if in.negated {
			w.st.pending = append(w.st.pending, started)
		}
		w.visit(in.ahead, started)

	case opLookEnd:
		w.lookMatched(look)

	case opMatch:
		w.st.best = max(w.st.best, w.at)
	}
}

// lookMatched handles a match of the program of the lookahead look at
// w.at. A positive lookahead decides for the match of the rule before it, the
// first time only, and continues the program at the start of the lookahead. A
// negative lookahead decides against it. Either way the lookahead's other
// threads are dropped.
func (w *walk) lookMatched(look *lookahead) {
	if w.st.hit[look] {
		return
	}
	if w.st.hit == nil {
		w.st.hit = map[*lookahead]bool{}
	}
	w.st.hit[look] = true

	in := &w.st.prog.insts[look.pc]
	if in.negated {
		return
	}
	if look.at == w.at {
		w.visit(in.next, look.outer)
		return
	}
	w.out = append(w.out, w.st.resume(in.next, look.at, look.outer, w.at)...)
}

// settle decides the negative lookaheads that can no longer match: no thread
// runs for them and no negative lookahead inside them is pending. The
// program then continues at their start. It repeats until no lookahead
// decides, drops the threads of every lookahead that was hit, and returns the
// threads that wait at st.n.
func (st *stepper) settle(ts []thread) []thread {
	for {
		ts = st.withoutDropped(ts)
		if len(st.pending) == 0 {
			return merge(ts)
		}
		// A lookahead is live while one of its threads is, or while a
		// negative lookahead inside it has not decided: that decision can
		// still resume the program inside it and match it.
		live := map[*lookahead]bool{}
		for _, t := range ts {
			for look := t.look; look != nil; look = look.outer {
				live[look] = true
			}
		}
		for _, inner := range st.pending {
			if !st.dropped(inner) {
				for look := inner.outer; look != nil; look = look.outer {
					live[look] = true
				}
			}
		}

		pending := st.pending
		st.pending = nil
		var waiting []*lookahead
		decided := false
		for _, look := range pending {
			switch {
			case st.dropped(look):
			case live[look]:
				waiting = append(waiting, look)
			default:
				decided = true
				ts = append(ts, st.resume(st.prog.insts[look.pc].next, look.at, look.outer, st.n)...)
			}
		}
		st.pending = append(waiting, st.pending...)
		if !decided {
			return merge(ts)
		}
	}
}

// dropped reports whether look or a lookahead that it runs for was hit, so
// that its threads no longer matter.
func (st *stepper) dropped(look *lookahead) bool {
	for ; look != nil; look = look.outer {
		if st.hit[look] {
			return true
		}
	}
	return false
}

// withoutDropped returns the threads of ts that are not dropped.
func (st *stepper) withoutDropped(ts []thread) []thread {
	if len(st.hit) == 0 {
		return ts
	}
	kept := ts[:0:0]
	for _, t := range ts {
		if !st.dropped(t.look) {
			kept = append(kept, t)
		}
	}
	return kept
}

// merge returns ts without the threads whose key an earlier thread has. It
// filters ts in place: ts is always a slice that the current step built (the
// output of feed or of settle), never the threads of a snapshot, so the
// snapshots stay unchanged.
func merge(ts []thread) []thread {
	if len(ts) < 2 {
		return ts
	}
	var seen keySet[threadKey]
	kept := ts[:0]
	for _, t := range ts {
		if seen.add(t.key()) {
			kept = append(kept, t)
		}
	}
	return kept
}

// keySet is a set of comparable keys. It scans an inline array while the set
// is small, which avoids an allocation in the common case of a few keys (a
// keySet that does not escape lives on the stack), and moves to a map when it
// grows.
type keySet[K comparable] struct {
	few  [keySetScanLimit]K // the first nfew are the keys while many is nil
	nfew int
	many map[K]bool
}

// keySetScanLimit is the size from which a keySet uses a map.
const keySetScanLimit = 16

// add adds k to the set and reports whether k was new.
func (ks *keySet[K]) add(k K) bool {
	if ks.many != nil {
		if ks.many[k] {
			return false
		}
		ks.many[k] = true
		return true
	}
	for _, have := range ks.few[:ks.nfew] {
		if have == k {
			return false
		}
	}
	if ks.nfew < keySetScanLimit {
		ks.few[ks.nfew] = k
		ks.nfew++
		return true
	}
	ks.many = make(map[K]bool, 2*keySetScanLimit)
	for _, have := range ks.few[:ks.nfew] {
		ks.many[have] = true
	}
	ks.many[k] = true
	ks.nfew = 0
	return true
}
