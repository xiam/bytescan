package core_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// ruleSink keeps a built rule alive so that the compiler cannot drop the
// construction that a build-cost test measures.
var ruleSink bytescan.Rule

// buildCost returns the heap allocations and bytes of one call of build, as
// an average over runs calls.
func buildCost(build func() bytescan.Rule, runs int) (allocs float64, bytes uint64) {
	allocs = testing.AllocsPerRun(runs, func() { ruleSink = build() })

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < runs; i++ {
		ruleSink = build()
	}
	runtime.ReadMemStats(&after)
	ruleSink = nil
	return allocs, (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
}

// TestRuneRunMaxBuildCost: building RuneRunMax(c, 1000) allocates at least ten
// times less, in allocations and in bytes, than building
// Repeat(Rune(c), 1, 1000), which holds one copy of Rune(c) per count.
func TestRuneRunMaxBuildCost(t *testing.T) {
	c := core.ASCIIDigit
	maxAllocs, maxBytes := buildCost(func() bytescan.Rule { return core.RuneRunMax(c, 1000) }, 50)
	repAllocs, repBytes := buildCost(func() bytescan.Rule { return core.Repeat(core.Rune(c), 1, 1000) }, 5)
	t.Logf("RuneRunMax(c, 1000): %.0f allocs, %d bytes; Repeat(Rune(c), 1, 1000): %.0f allocs, %d bytes",
		maxAllocs, maxBytes, repAllocs, repBytes)

	require.Positive(t, maxAllocs)
	assert.GreaterOrEqual(t, repAllocs, 10*maxAllocs, "allocations")
	assert.GreaterOrEqual(t, repBytes, 10*maxBytes, "bytes")
}

// TestRuneRunMaxBuildCostDoesNotGrow: the construction cost of RuneRunMax is
// the same for every n, far above the limit of 1000 of Repeat included.
func TestRuneRunMaxBuildCostDoesNotGrow(t *testing.T) {
	c := core.ASCIIDigit
	smallAllocs, smallBytes := buildCost(func() bytescan.Rule { return core.RuneRunMax(c, 1) }, 50)
	for _, n := range []int{1000, 1001, 1 << 20, 1<<31 - 1} {
		allocs, bytes := buildCost(func() bytescan.Rule { return core.RuneRunMax(c, n) }, 50)
		assert.Equal(t, smallAllocs, allocs, "allocations of RuneRunMax(c, %d)", n)
		assert.Equal(t, smallBytes, bytes, "bytes of RuneRunMax(c, %d)", n)
	}
}

// TestRuneRunMaxAboveRepeatLimit: a bound above 1000 splits a longer run into
// pieces of exactly n runes and a shorter last piece.
func TestRuneRunMaxAboveRepeatLimit(t *testing.T) {
	const n = 1500
	input := strings.Repeat("7", 2*n+400) + "x" + strings.Repeat("7", n)
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("RUN", core.RuneRunMax(core.ASCIIDigit, n))

	var got []int
	for {
		lex, err := lx.Next()
		if err != nil {
			break
		}
		if lex.Type() == "RUN" {
			got = append(got, lex.Len())
		}
	}
	assert.Equal(t, []int{n, n, 400, n}, got)
}
