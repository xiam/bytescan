package core

import (
	"slices"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/xiam/bytescan"
)

// The table of a pure core rule (form A of docs/specs/table-compiler.md).
//
// A composed rule whose program needs no log (program.needsLog) is a pure
// core rule: every leaf is a matcher and no instruction is a lookahead. The
// engine steps such a program with snapshot.stepCore, and the verdict of a
// step depends only on the threads after the step and on whether the step
// reached the end of the program. So the rule is a deterministic automaton
// whose states are the merged sets of threads, and a table can hold its
// steps. Whether a step reaches the end of the program is kept on the edge
// of the step, with the verdict, not in the state: the state after "a" and
// the state after "aa" of Choice(Sequence(ZeroOrMore(Literal("a")),
// Literal("b")), Literal("a")) have the same threads and share one state;
// the edge on "a" from the start state accepts, and the edge on "a" from
// that state to itself continues.
//
// The table is built the first time the rule reads a symbol, never when the
// rule is built: a combinator that is only a part of a larger rule pays
// nothing. One table serves every scanner that runs the rule. Each state of
// the table has one Rule value, built with the table and never changed, so a
// step on a full row, or a step that the cache keeps, returns a prebuilt Rule
// value and allocates nothing.
//
// A state has a full row for the runes U+0000 to U+00FF, one column for each
// symbol class: two runes share a column only when every leaf, in every
// state that the table reaches, gives the same result for both. A rune above
// U+00FF is stepped as the engine steps it, from the state's own threads, the
// first time that the state meets it, and the step is kept in a cache of
// at most tableCacheSize steps for the table (tableCache). The cache is made
// at the first such rune, small, and grows with the steps that it keeps. A
// step that leads to a set of threads of the table continues on the table;
// any other step continues on the engine for the rest of the token. A rune
// below U+0000 (a probe) is stepped by the engine (snapshot.stepCore). The
// build stops at maxTableStates states or maxTableWork units of work; the
// rule then keeps the engine for good. The test switch engineForced keeps
// every rule on the engine.

// tableRunes is the number of runes, from U+0000, that have a full row in a
// table.
const tableRunes = 0x100

// The build budget of one table. A build that needs more states, or more
// work, stops, and the rule keeps the engine for good. A unit of work is one
// step of a leaf on one rune, one instruction that a closure visits, one
// thread that the build stores, or one thread of a state stepped on one
// symbol class. The build checks the budget before it stores, so the work
// bounds the time and the memory of the build, and also the edges of the
// table (each state has at least one thread). The largest table of the rule
// sets of the reference cases has 1000 states, and the largest table of the
// oracle rows of tests/, RuneRunMax(c, 4096), has 4096 states, one for each
// count; both build inside the budget (TestTableBudgetFits). The work bound
// is set by the generated wide rules of TestTableBudgetWideRules: the largest
// work that fits is a choice of 100 keywords, with a margin; an AnyLiteral of
// 1000 words is over it. A rule over the budget pays for the build up to the
// budget once, at its first symbol.
const (
	maxTableStates = 4096
	maxTableWork   = 1 << 18
)

// engineForced keeps every core rule on the engine. It is a switch for the
// tests, so that the table and the engine stay tested (the build tag
// coreengine sets it; see engine_tag_on.go). It is read at the first symbol of
// each token.
var engineForced atomic.Bool

func init() {
	engineForced.Store(coreEngineTag)
}

// tableCacheSize is the most steps that the cache of one table keeps for
// runes above U+00FF, and the most slots that it has. A step that the cache
// cannot keep is computed again each time, with the same verdict.
const tableCacheSize = 1 << 12

// tableCacheMinSlots is the number of slots of the cache of a table when it
// is made. The cache doubles its slots when it keeps more steps than half of
// them, up to tableCacheSize, so a table that meets few runes above U+00FF
// holds a small cache (TestTableFixedMemory).
const tableCacheMinSlots = 1 << 6

// tableCacheProbes is the number of slots of the cache that a step may take:
// its own slot and the next ones. When they are all taken, the step is not
// kept.
const tableCacheProbes = 8

// tableStats counts the tables built in this process, for the tests: the
// builds, the builds that ended over the budget, and the states built.
var tableStats struct {
	builds, overBudget, states atomic.Uint64
}

// tableRule is the start state of a pure core rule: it runs the table, which
// it builds at the first symbol, or the engine.
type tableRule struct {
	prog    *program
	threads []thread // the threads of the start snapshot
	engine  bytescan.Rule

	once  sync.Once
	start bytescan.Rule // the Rule value of the start state, or nil
}

// newRuleOf returns the start state of the rule whose program is prog: the
// table of a pure core rule, built at its first symbol, or the engine.
func newRuleOf(prog *program) bytescan.Rule {
	snap := newSnapshot(prog)
	engine := snap.rule()
	if prog.needsLog {
		return engine
	}
	t := &tableRule{prog: prog, threads: snap.threads, engine: engine}
	return t.first
}

// first reads the first symbol of a token.
func (t *tableRule) first(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if engineForced.Load() {
		return t.engine(s)
	}
	t.once.Do(t.build)
	if t.start == nil {
		return t.engine(s)
	}
	return t.start(s)
}

// build builds the table. A rule whose table is over the budget keeps the
// engine. A panic of the build itself leaves the rule on the engine too, so
// the tokens stay those of the engine; a panic of a leaf is recovered where
// the leaf is called (tableBuilder.call).
func (t *tableRule) build() {
	defer func() { _ = recover() }()
	tab, ok := buildTable(t.prog, t.threads)
	tableStats.builds.Add(1)
	if !ok {
		tableStats.overBudget.Add(1)
		return
	}
	tableStats.states.Add(uint64(len(tab.states)))
	t.start = tab.states[0].step
}

// A table holds the states of a pure core rule.
type table struct {
	// column is the symbol class of each rune below tableRunes.
	column [tableRunes]uint8

	// classes is the number of symbol classes.
	classes int

	// states are the states; the first is the start state.
	states []tableState

	prog *program

	// work is the work of the build, in the units of maxTableWork.
	work int

	// cache keeps the steps of the runes above U+00FF. It is made at the
	// first such rune (cacheOnce), so a table that never meets one pays
	// nothing for it.
	cacheOnce sync.Once
	cache     *tableCache
}

// A tableState is one state of a table: a merged set of threads.
type tableState struct {
	tab *table
	id  int // the index of the state in tab.states

	// edges is the verdict of a step for each symbol class.
	edges []tableEdge

	// threads are the threads of the state, for a rune without a column.
	threads []thread
}

// A tableCache keeps the steps of the states of one table on runes above
// U+00FF: at most tableCacheSize of them. A kept step is one word in a slot
// (cacheWord). A step to a state of the table holds no other memory than the
// Rule value of that state, made once (rules); a step that continues on the
// engine also holds its Rule value of the engine (engine). A kept step never
// changes, and the Rule values of the table read the slots concurrently
// without a lock. A new step is kept under mu, which also grows the slots
// (keep): a slot of the current slots is written once, from empty to a
// step, and a growth copies the steps into new slots, twice as many, and
// then publishes them. A step that finds no empty slot of its own in the
// new slots is dropped by the growth, and is computed again when it is met
// again.
type tableCache struct {
	// slots are the slots: a power of two of them, from tableCacheMinSlots
	// to tableCacheSize. An empty slot is zero.
	slots atomic.Pointer[[]atomic.Uint64]

	// mu serializes keep.
	mu sync.Mutex

	// index holds the sets of threads of the states of the table, sorted:
	// for each state, the hash of its set (setHash) in the high bits and the
	// index of the state in the low cacheStateBits bits. It is only read
	// after the cache is made.
	index []uint64

	// rules holds the Rule value of each state of the table that a kept
	// step leads to, or nil. keep sets it, under mu, before it publishes the
	// step, and never changes it.
	rules []bytescan.Rule

	// engine holds the Rule values of the engine that the kept steps lead
	// to (stepEngine). keep appends to it under mu and publishes the new
	// slice before the step.
	engine atomic.Pointer[[]bytescan.Rule]

	// len is the number of steps kept.
	len atomic.Int32
}

// The bits of a cacheWord, from the lowest: the rune (cacheRuneBits), the
// index of the state (cacheStateBits), the kind of the step (2 bits), whether
// the step accepts (1 bit), the value of the step (cacheValueBits), and the
// bit that marks the slot as taken (cacheUsed).
const (
	cacheRuneBits  = 21 // unicode.MaxRune < 1<<21
	cacheStateBits = 13 // maxTableStates <= 1<<13
	cacheKeyBits   = cacheRuneBits + cacheStateBits
	cacheValueBits = 24
	cacheUsed      = 1 << 63
)

// The kinds of a step that the cache keeps.
const (
	// stepNone: no thread is alive after the step.
	stepNone = iota
	// stepState: the step leads to the state whose index is the value.
	stepState
	// stepEngine: the step continues on the engine, with the Rule value
	// whose index in tableCache.engine is the value.
	stepEngine
)

// A cacheWord is one step that the cache keeps, or zero for an empty slot.
type cacheWord = uint64

// cacheKey returns the key of the step of the state with index id on the
// rune r: the low cacheKeyBits bits of its cacheWord.
func cacheKey(id int, r rune) uint64 {
	return uint64(r) | uint64(id)<<cacheRuneBits
}

// cacheHash returns the hash of the key of a step, which picks its first
// slot.
func cacheHash(key uint64) uint32 {
	h := uint32(key>>cacheRuneBits)*0x9E3779B1 ^ uint32(key&(1<<cacheRuneBits-1))*0x85EBCA77
	return h ^ h>>15
}

// A tableEdge is the verdict of one step: the Rule value of the next state,
// or nil when no thread is alive, and the state that the rule returns.
type tableEdge struct {
	next  bytescan.Rule
	state bytescan.State
}

// step reads the symbol s in the state ts. A rune with a column is one
// lookup. The EOF symbol ends every thread, and no thread accepts on it, so
// the rule rejects, as snapshot.stepCore does. A rune above U+00FF is a
// lookup in the cache of the table, or a step of the engine that the cache
// then keeps (tableState.far). A rune below U+0000 is stepped by the engine
// from the threads of ts; the snapshot that it steps from has best and
// recorded equal, as every snapshot of a pure core rule has, so its verdict
// is that of the engine.
func (ts *tableState) step(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.IsEOF() {
		return nil, bytescan.StateReject
	}
	r := s.Rune()
	if r >= 0 && r < tableRunes {
		e := &ts.edges[ts.tab.column[r]]
		return e.next, e.state
	}
	if r >= tableRunes {
		return ts.far(r)
	}
	return snapshot{prog: ts.tab.prog}.stepCore(ts.threads, s)
}

// far reads the rune r, above U+00FF, in the state ts: the step that the
// cache keeps, or a new step, which the cache keeps if it has room for it
// (tableCache.keep). A rune above unicode.MaxRune is stepped each time.
func (ts *tableState) far(r rune) (bytescan.Rule, bytescan.State) {
	tab := ts.tab
	tab.cacheOnce.Do(tab.newCache)
	c := tab.cache
	if r > unicode.MaxRune {
		edge, _, _ := ts.farStep(r)
		return edge.next, edge.state
	}
	key := cacheKey(ts.id, r)
	h := cacheHash(key)
	slots := *c.slots.Load()
	w, empty := probe(slots, h, key)
	if w != 0 {
		return c.edge(w)
	}
	edge, kind, to := ts.farStep(r)
	if (empty >= 0 || len(slots) < tableCacheSize) && c.len.Load() < tableCacheSize {
		if w := c.keep(h, key, edge, kind, to); w != 0 {
			return c.edge(w)
		}
	}
	return edge.next, edge.state
}

// edge returns the verdict of the step that the word w keeps.
func (c *tableCache) edge(w cacheWord) (bytescan.Rule, bytescan.State) {
	kind := w >> cacheKeyBits & 3
	accept := w>>(cacheKeyBits+2)&1 == 1
	v := int(w >> (cacheKeyBits + 3) & (1<<cacheValueBits - 1))
	var next bytescan.Rule
	switch kind {
	case stepState:
		next = c.rules[v]
	case stepEngine:
		next = (*c.engine.Load())[v]
	}
	switch {
	case accept:
		return next, bytescan.StateAccept
	case next == nil:
		return nil, bytescan.StateReject
	}
	return next, bytescan.StateContinue
}

// probe looks for the step with the key key, of hash h, in the
// tableCacheProbes slots of slots from slot h on. It returns the word of
// the step when a slot keeps it, or else zero and the index of the first
// empty slot, or -1 when every slot is taken. A step is always kept in the
// first empty slot, so the slots after an empty one do not keep it.
func probe(slots []atomic.Uint64, h uint32, key uint64) (cacheWord, int) {
	mask := uint32(len(slots) - 1)
	for i := uint32(0); i < tableCacheProbes; i++ {
		at := (h + i) & mask
		w := slots[at].Load()
		if w == 0 {
			return 0, int(at)
		}
		if w&(1<<cacheKeyBits-1) == key {
			return w, -1
		}
	}
	return 0, -1
}

// keep keeps the step with the key key, of hash h, and returns its word, or
// returns the word of the step that another call kept first. The step leads
// to edge; kind and to are what farStep reports about it. keep grows the
// slots first when the step would fill more than half of them, or when its
// slots are taken, while there are fewer than tableCacheSize. It returns
// zero when the cache has no room for the step.
func (c *tableCache) keep(h uint32, key uint64, edge tableEdge, kind, to int) cacheWord {
	c.mu.Lock()
	defer c.mu.Unlock()
	slots := *c.slots.Load()
	w, empty := probe(slots, h, key)
	if w != 0 {
		return w
	}
	n := int(c.len.Load())
	if n >= tableCacheSize {
		return 0
	}
	if (empty < 0 || 2*(n+1) > len(slots)) && len(slots) < tableCacheSize {
		slots = c.grow(slots)
		_, empty = probe(slots, h, key)
	}
	if empty < 0 {
		return 0
	}
	v := 0
	switch kind {
	case stepState:
		if c.rules[to] == nil {
			c.rules[to] = edge.next
		}
		v = to
	case stepEngine:
		engine := *c.engine.Load()
		if len(engine) >= 1<<cacheValueBits {
			return 0
		}
		v = len(engine)
		engine = append(engine, edge.next)
		c.engine.Store(&engine)
	}
	w = cacheUsed | key | uint64(kind)<<cacheKeyBits | uint64(v)<<(cacheKeyBits+3)
	if edge.state == bytescan.StateAccept {
		w |= 1 << (cacheKeyBits + 2)
	}
	slots[empty].Store(w)
	c.len.Add(1)
	return w
}

// grow publishes new slots, twice as many as old, with the steps of old, and
// returns them. A step that finds no empty slot of its own is dropped. It is
// called with mu held.
func (c *tableCache) grow(old []atomic.Uint64) []atomic.Uint64 {
	slots := make([]atomic.Uint64, 2*len(old))
	for i := range old {
		w := old[i].Load()
		if w == 0 {
			continue
		}
		key := w & (1<<cacheKeyBits - 1)
		if _, empty := probe(slots, cacheHash(key), key); empty >= 0 {
			slots[empty].Store(w)
		} else {
			c.len.Add(-1)
		}
	}
	c.slots.Store(&slots)
	return slots
}

// farStep steps the threads of ts on the rune r, above U+00FF, as
// snapshot.stepCore steps them from a snapshot at position 0, and returns
// the verdict, its kind (stepNone, stepState or stepEngine) and, for
// stepState, the index of the next state. A step that leads to the set of
// threads of a state of the table returns the Rule value of that state; a
// step to any other set returns a Rule value of the engine with that set,
// as stepCore does. A panic of a leaf is not recovered: the step panics, as
// the engine's does, and the cache keeps nothing.
func (ts *tableState) farStep(r rune) (edge tableEdge, kind, to int) {
	var scratch [2 * fewThreads]thread
	out := scratch[:0]
	c := coreStep{insts: ts.tab.prog.insts}
	for _, t := range ts.threads {
		out = c.read(out, t, r, 1)
	}
	out = merge(out)
	accept := c.best == 1
	switch {
	case len(out) == 0 && accept:
		return tableEdge{state: bytescan.StateAccept}, stepNone, 0
	case len(out) == 0:
		return tableEdge{state: bytescan.StateReject}, stepNone, 0
	}
	state := bytescan.StateContinue
	if accept {
		state = bytescan.StateAccept
	}
	var setScratch [2 * fewThreads]leafThread
	set := setScratch[:0]
	for _, t := range out {
		set = append(set, leafThread{pc: t.pc, state: t.state})
	}
	if i, ok := ts.tab.find(canonical(set)); ok {
		return tableEdge{next: ts.tab.states[i].step, state: state}, stepState, i
	}
	// A set that the table does not have: the engine, as stepCore returns
	// it (verdictOf).
	head := snapshot{prog: ts.tab.prog, n: 1, best: c.best}
	if accept {
		head.recorded = 1
	}
	return tableEdge{next: newRule(head, out), state: state}, stepEngine, 0
}

// find returns the index of the state of the table whose set of threads is
// the canonical set set, and whether there is one.
func (tab *table) find(set []leafThread) (int, bool) {
	const idMask = 1<<cacheStateBits - 1
	index := tab.cache.index
	h := setHash(set) &^ idMask
	i, _ := slices.BinarySearch(index, h)
	for ; i < len(index) && index[i]&^idMask == h; i++ {
		id := int(index[i] & idMask)
		if sameThreads(tab.states[id].threads, set) {
			return id, true
		}
	}
	return 0, false
}

// sameThreads reports whether the threads of a state are the canonical set
// set.
func sameThreads(threads []thread, set []leafThread) bool {
	if len(threads) != len(set) {
		return false
	}
	for i, t := range threads {
		if t.pc != set[i].pc || t.state != set[i].state {
			return false
		}
	}
	return true
}

// setHash returns a hash of the canonical set of threads set.
func setHash(set []leafThread) uint64 {
	h := uint64(14695981039346656037)
	for _, t := range set {
		h = hashInt(h, t.pc)
		h = hashInt(h, t.state)
	}
	return h
}

// hashInt adds v to the FNV-1a hash h, one byte at a time.
func hashInt(h uint64, v int) uint64 {
	u := uint64(v)
	for i := 0; i < 8; i++ {
		h ^= u & 0xFF
		h *= 1099511628211
		u >>= 8
	}
	return h
}

// newCache makes the cache of the table, with tableCacheMinSlots slots and
// the index of its sets of threads.
func (tab *table) newCache() {
	c := &tableCache{
		index: make([]uint64, len(tab.states)),
		rules: make([]bytescan.Rule, len(tab.states)),
	}
	slots := make([]atomic.Uint64, tableCacheMinSlots)
	c.slots.Store(&slots)
	c.engine.Store(new([]bytescan.Rule))
	var set []leafThread
	for i := range tab.states {
		set = set[:0]
		for _, t := range tab.states[i].threads {
			set = append(set, leafThread{pc: t.pc, state: t.state})
		}
		c.index[i] = setHash(set)&^(1<<cacheStateBits-1) | uint64(i)
	}
	slices.Sort(c.index)
	tab.cache = c
}

// leafResult is the result of one step of a leaf: the values that
// matcher.step returns, or a panic.
type leafResult struct {
	next        int
	match, more bool
	panicked    bool
}

// leafThread is a thread of a table: an opLeaf instruction and the state of
// its matcher.
type leafThread struct {
	pc, state int
}

// threadSteps holds the steps of one leaf thread for every symbol class of
// its leaf.
type threadSteps struct {
	// part is the class of each rune for the leaf of the thread, and steps
	// the successors of the thread for each class of part.
	part  *leafPart
	steps []successors
}

// successors are what one thread leaves after one rune: the threads that
// wait for the next rune, whether the program reached its end, and whether
// the leaf panicked.
type successors struct {
	threads  []leafThread
	match    bool
	panicked bool
}

// leafPart groups the runes below tableRunes by the result of one leaf in
// one state: class[r] is the group of r, and reps holds the smallest rune
// of each group.
type leafPart struct {
	class [tableRunes]uint8
	reps  []rune
}

// partKey identifies a leafPart. A class leaf gives the same grouping in every
// state, because its result depends on the state only for the runes of the
// class (classMatcher.step), so its key has state 0.
type partKey struct {
	leaf  *leaf
	state int
}

// tableBuilder builds one table.
type tableBuilder struct {
	prog *program
	work int

	parts    map[partKey]*leafPart
	partList []*leafPart
	threads  map[leafThread]*threadSteps
	closures map[int]successors

	// stepsSlab and succSlab are storage for threadSteps, allocated in
	// blocks.
	stepsSlab []threadSteps
	succSlab  []successors

	// rowBuf is storage for the row of one leaf (partOf).
	rowBuf *[tableRunes]leafResult

	// mark, epoch and stack are the storage of closure: mark[pc] is epoch
	// when the current closure has visited pc.
	mark  []int
	epoch int
	stack []int
}

// buildTable builds the table of the pure core program prog, whose start
// snapshot has the threads start. It reports false when the table is over
// the budget.
func buildTable(prog *program, start []thread) (*table, bool) {
	b := &tableBuilder{
		prog:     prog,
		parts:    map[partKey]*leafPart{},
		threads:  map[leafThread]*threadSteps{},
		closures: map[int]successors{},
	}

	// Find every thread that the program can reach, with its steps. Every
	// thread that the build stores counts as work, before it is stored.
	b.work += len(start)
	if b.work > maxTableWork {
		return nil, false
	}
	first := make([]leafThread, 0, len(start))
	for _, t := range start {
		first = append(first, leafThread{pc: t.pc, state: t.state})
	}
	first = canonical(first)
	queue := append([]leafThread(nil), first...)
	for _, t := range first {
		b.threads[t] = nil
	}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		ts, ok := b.stepsOf(t)
		if !ok {
			return nil, false
		}
		b.threads[t] = ts
		for _, s := range ts.steps {
			for _, next := range s.threads {
				if _, seen := b.threads[next]; !seen {
					b.work++
					if b.work > maxTableWork {
						return nil, false
					}
					b.threads[next] = nil
					queue = append(queue, next)
				}
			}
		}
	}

	// The symbol classes: the runes that every leaf groups together.
	tab := &table{prog: prog}
	tab.classes = b.refine(&tab.column)
	reps := make([]rune, tab.classes)
	for r := tableRunes - 1; r >= 0; r-- {
		reps[tab.column[r]] = rune(r)
	}

	// The states, from the start state on.
	type edge struct {
		to    int // the next state, or -1 when no thread is alive
		state bytescan.State
	}
	var sets [][]leafThread
	var edges []edge
	index := map[string]int{}
	var key []byte
	var slab []leafThread // storage for sets
	total := 0            // the threads of every set
	intern := func(set []leafThread) (int, bool) {
		key = setKey(key[:0], set)
		if i, ok := index[string(key)]; ok {
			return i, true
		}
		if len(sets) == maxTableStates {
			return 0, false
		}
		index[string(key)] = len(sets)
		if cap(slab)-len(slab) < len(set) {
			slab = make([]leafThread, 0, max(len(set), 2*cap(slab), 16))
		}
		at := len(slab)
		slab = append(slab, set...)
		sets = append(sets, slab[at:len(slab):len(slab)])
		total += len(set)
		return len(sets) - 1, true
	}
	intern(first)
	var next []leafThread
	var steps []*threadSteps
	for i := 0; i < len(sets); i++ {
		steps = steps[:0]
		for _, t := range sets[i] {
			steps = append(steps, b.threads[t])
		}
		for _, r := range reps {
			next = next[:0]
			match, panicked := false, false
			for _, ts := range steps {
				s := ts.steps[ts.part.class[r]]
				if s.panicked {
					panicked = true
					break
				}
				b.work += len(s.threads)
				if b.work > maxTableWork {
					return nil, false
				}
				next = append(next, s.threads...)
				match = match || s.match
			}
			b.work += len(steps)
			if b.work > maxTableWork {
				return nil, false
			}
			e := edge{to: -1}
			switch {
			case panicked:
				// The engine would panic at this step, and the VM
				// retires the rule for the token as for a reject.
				e.state = bytescan.StateReject
			case len(next) == 0 && match:
				e.state = bytescan.StateAccept
			case len(next) == 0:
				e.state = bytescan.StateReject
			default:
				to, ok := intern(canonical(next))
				if !ok {
					return nil, false
				}
				e.to = to
				e.state = bytescan.StateContinue
				if match {
					e.state = bytescan.StateAccept
				}
			}
			edges = append(edges, e)
		}
	}

	// The Rule value of each state, then the edges that return them.
	k := tab.classes
	tab.states = make([]tableState, len(sets))
	flat := make([]tableEdge, len(edges))
	all := make([]thread, total)
	for i, set := range sets {
		threads := all[:len(set):len(set)]
		all = all[len(set):]
		for j, t := range set {
			threads[j] = thread{pc: t.pc, state: t.state}
		}
		tab.states[i] = tableState{tab: tab, id: i, edges: flat[i*k : (i+1)*k : (i+1)*k], threads: threads}
	}
	rules := make([]bytescan.Rule, len(sets))
	for i := range tab.states {
		rules[i] = tab.states[i].step
	}
	for i, e := range edges {
		flat[i].state = e.state
		if e.to >= 0 {
			flat[i].next = rules[e.to]
		}
	}
	tab.work = b.work
	return tab, true
}

// stepsOf returns the steps of the thread t for every class of its leaf. It
// reports false when the build is over the budget.
func (b *tableBuilder) stepsOf(t leafThread) (*threadSteps, bool) {
	in := &b.prog.insts[t.pc]
	part, ok := b.partOf(in.leaf, t.state)
	if !ok {
		return nil, false
	}
	ts := b.newSteps(part)
	for i, r := range part.reps {
		res := b.call(in.leaf.m, t.state, r)
		switch {
		case res.panicked:
			ts.steps[i] = successors{panicked: true}
			continue
		case res.more:
			b.work++
			if b.work > maxTableWork {
				return nil, false
			}
			ts.steps[i].threads = append(ts.steps[i].threads, leafThread{pc: t.pc, state: res.next})
		}
		if res.match {
			c, ok := b.closure(in.next)
			if !ok {
				return nil, false
			}
			b.work += len(c.threads)
			if b.work > maxTableWork {
				return nil, false
			}
			ts.steps[i].threads = append(ts.steps[i].threads, c.threads...)
			ts.steps[i].match = c.match
		}
	}
	return ts, b.work <= maxTableWork
}

// newSteps returns new threadSteps for a thread whose leaf groups the runes
// as part. It allocates them in blocks.
func (b *tableBuilder) newSteps(part *leafPart) *threadSteps {
	if len(b.stepsSlab) == 0 {
		b.stepsSlab = make([]threadSteps, 16)
	}
	ts := &b.stepsSlab[0]
	b.stepsSlab = b.stepsSlab[1:]
	k := len(part.reps)
	if len(b.succSlab) < k {
		b.succSlab = make([]successors, max(k, 64))
	}
	ts.part, ts.steps = part, b.succSlab[:k:k]
	b.succSlab = b.succSlab[k:]
	return ts
}

// partOf returns the grouping of the runes below tableRunes by the result of
// the leaf l in state st. It reports false when the build is over the
// budget.
func (b *tableBuilder) partOf(l *leaf, st int) (*leafPart, bool) {
	key := partKey{leaf: l, state: st}
	if _, ok := l.m.(classMatcher); ok {
		key.state = 0
	}
	if p, ok := b.parts[key]; ok {
		return p, true
	}
	b.work += tableRunes
	if b.work > maxTableWork {
		return nil, false
	}
	p := &leafPart{}
	if b.rowBuf == nil {
		b.rowBuf = new([tableRunes]leafResult)
	}
	row := b.rowBuf
	b.row(l.m, key.state, row)
	var results []leafResult
	for r := rune(0); r < tableRunes; r++ {
		res := row[r]
		i := slices.Index(results, res)
		if i < 0 {
			i = len(results)
			results = append(results, res)
			p.reps = append(p.reps, r)
		}
		p.class[r] = uint8(i)
	}
	b.parts[key] = p
	b.partList = append(b.partList, p)
	return p, true
}

// row sets row to the result of the matcher m in state st on each rune
// below tableRunes. It steps every rune under one recover, and steps them
// again one by one (call) only when one of them panics.
func (b *tableBuilder) row(m matcher, st int, row *[tableRunes]leafResult) {
	panicked := true
	func() {
		defer func() {
			if panicked {
				_ = recover()
			}
		}()
		for r := rune(0); r < tableRunes; r++ {
			res := &row[r]
			res.next, res.match, res.more = m.step(st, r)
		}
		panicked = false
	}()
	if panicked {
		for r := rune(0); r < tableRunes; r++ {
			row[r] = b.call(m, st, r)
		}
	}
}

// call steps the matcher m in state st on the rune r. A panic of the matcher,
// which is a panic of a class, is recorded as the result.
func (b *tableBuilder) call(m matcher, st int, r rune) (res leafResult) {
	returned := false
	defer func() {
		if !returned {
			_ = recover()
			res = leafResult{panicked: true}
		}
	}()
	res.next, res.match, res.more = m.step(st, r)
	returned = true
	return res
}

// closure returns the threads that the program reaches from pc without
// reading a symbol, and whether it reaches its end, as coreStep.visit
// follows it after a leaf ends. Each instruction that it visits counts as
// work, so a program whose closures are large (a long Repeat of a part that
// can match the empty text) stops at the budget instead of storing them. It
// reports false when the build is over the budget.
func (b *tableBuilder) closure(pc int) (successors, bool) {
	if c, ok := b.closures[pc]; ok {
		return c, true
	}
	if b.mark == nil {
		b.mark = make([]int, len(b.prog.insts))
	}
	b.epoch++
	var s successors
	stack := append(b.stack[:0], pc)
	for len(stack) > 0 {
		at := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if b.mark[at] == b.epoch {
			continue
		}
		b.mark[at] = b.epoch
		b.work++
		if b.work > maxTableWork {
			return successors{}, false
		}
		in := &b.prog.insts[at]
		switch in.op {
		case opLeaf:
			s.threads = append(s.threads, leafThread{pc: at})
		case opFork:
			stack = append(stack, in.targets...)
		case opMatch:
			s.match = true
		}
	}
	b.stack = stack
	b.closures[pc] = s
	return s, true
}

// refine sets column to the symbol classes: the coarsest grouping of the
// runes below tableRunes that every leafPart refines. It returns the number
// of classes.
func (b *tableBuilder) refine(column *[tableRunes]uint8) int {
	*column = [tableRunes]uint8{}
	n := 1
	var ids []int
	for _, p := range b.partList {
		groups := len(p.reps)
		ids = slices.Grow(ids[:0], n*groups)[:n*groups]
		for i := range ids {
			ids[i] = -1
		}
		m := 0
		for r := 0; r < tableRunes; r++ {
			at := int(column[r])*groups + int(p.class[r])
			if ids[at] < 0 {
				ids[at] = m
				m++
			}
			column[r] = uint8(ids[at])
		}
		n = m
	}
	return n
}

// canonical returns the threads of set sorted and without duplicates, in
// the storage of set.
func canonical(set []leafThread) []leafThread {
	slices.SortFunc(set, func(a, b leafThread) int {
		if a.pc != b.pc {
			return a.pc - b.pc
		}
		return a.state - b.state
	})
	return slices.Compact(set)
}

// setKey appends to key the bytes that identify the canonical set of
// threads set.
func setKey(key []byte, set []leafThread) []byte {
	for _, t := range set {
		key = appendVarint(key, t.pc)
		key = appendVarint(key, t.state)
	}
	return key
}

// appendVarint appends v to b in the unsigned varint encoding.
func appendVarint(b []byte, v int) []byte {
	u := uint64(v)
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}
