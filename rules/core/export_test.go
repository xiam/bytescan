package core

import (
	"github.com/xiam/bytescan"
)

// This file gives the external tests of this package access to the table of
// a pure core rule (table.go). It is compiled only into the tests.

// SetEngineForced sets the switch that keeps every core rule on the engine,
// and returns the function that restores the previous value.
func SetEngineForced(on bool) (restore func()) {
	was := engineForced.Swap(on)
	return func() { engineForced.Store(was) }
}

// EngineForced reports whether every core rule runs on the engine.
func EngineForced() bool {
	return engineForced.Load()
}

// TableSize builds the table of the rule r, as its first symbol would, and
// returns its number of states and of symbol classes. tabled is false when r
// is not a pure core rule, and inBudget is false when its table is over the
// budget.
func TableSize(r bytescan.Rule) (states, classes int, tabled, inBudget bool) {
	n := nodeOf("TableSize", 0, r)
	prog := compile("TableSize", n)
	if prog.needsLog {
		return 0, 0, false, false
	}
	tab, ok := buildTable(prog, newSnapshot(prog).threads)
	if !ok {
		return 0, 0, true, false
	}
	return len(tab.states), tab.classes, true, true
}

// TableWork builds the table of the rule r, as its first symbol would, and
// returns the work of the build, in the units of the work budget, and that
// budget. ok is false when r is not a pure core rule or its table is over
// the budget.
func TableWork(r bytescan.Rule) (work, budget int, ok bool) {
	n := nodeOf("TableWork", 0, r)
	prog := compile("TableWork", n)
	if prog.needsLog {
		return 0, maxTableWork, false
	}
	tab, ok := buildTable(prog, newSnapshot(prog).threads)
	if !ok {
		return 0, maxTableWork, false
	}
	return tab.work, maxTableWork, true
}

// TableBuilds returns the number of tables that the rules of this process
// have built, the number of those that ended over the budget, and the
// states that the others built.
func TableBuilds() (builds, overBudget, states uint64) {
	return tableStats.builds.Load(), tableStats.overBudget.Load(), tableStats.states.Load()
}

// TableCacheSize is the most steps that the cache of one table keeps for
// runes above U+00FF.
const TableCacheSize = tableCacheSize

// TableCache builds the table of the rule r, as its first symbol would, and
// returns the Rule value of its start state and a function that returns the
// number of steps that the cache of that table keeps. ok is false when r
// has no table: it is not a pure core rule, or its table is over the budget.
func TableCache(r bytescan.Rule) (start bytescan.Rule, kept func() int, ok bool) {
	n := nodeOf("TableCache", 0, r)
	prog := compile("TableCache", n)
	if prog.needsLog {
		return nil, nil, false
	}
	tab, ok := buildTable(prog, newSnapshot(prog).threads)
	if !ok {
		return nil, nil, false
	}
	kept = func() int {
		tab.cacheOnce.Do(tab.newCache)
		return int(tab.cache.len.Load())
	}
	return tab.states[0].step, kept, true
}
