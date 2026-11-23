package core_test

import (
	"bufio"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

// repeatingInput is an io.Reader that repeats pat forever: a long-lived input
// with no end, so an allocation test measures the steady state only.
type repeatingInput struct {
	pat []byte
	off int
}

func (in *repeatingInput) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		k := copy(p[n:], in.pat[in.off:])
		n += k
		in.off = (in.off + k) % len(in.pat)
	}
	return n, nil
}

// handSpace is a hand-written rule for one or more spaces that allocates
// nothing, so a shape that uses it measures only the composed rule beside it.
func handSpace(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.Rune() == ' ' {
		return handSpace, bytescan.StateAccept
	}
	return nil, bytescan.StateReject
}

// An engineShape is a rule set of an allocation study and its input cycle,
// of tokens tokens.
type engineShape struct {
	name   string
	cycle  string
	tokens int
	rules  func(lx *bytescan.Scanner)

	// ceiling is the most allocations per token that the shape may cost in a
	// build without the race detector.
	ceiling float64
}

// engineShapes are the shapes S3, S4 and S5 of that study. Their ceilings
// are about 15% above the values measured with the current engine (S3 2.50,
// S4 2.00, S5 2.50 allocations per token, the Token included).
var engineShapes = []engineShape{
	{
		name: "S3 core.RuneRun", cycle: "ab ", tokens: 2, ceiling: 2.9,
		rules: func(lx *bytescan.Scanner) {
			lx.MustAddRule("WORD", core.RuneRun(core.ASCIILetter))
			lx.MustAddRule("SPACE", core.RuneRun(core.RuneSet(" ")))
		},
	},
	{
		name: "S4 base.ASCIIIdentifier", cycle: "ab ", tokens: 2, ceiling: 2.3,
		rules: func(lx *bytescan.Scanner) {
			lx.MustAddRule("WORD", base.ASCIIIdentifier())
			lx.MustAddRule("SPACE", handSpace)
		},
	},
	{
		name: "S5 base.Quoted", cycle: `"ab" `, tokens: 2, ceiling: 2.9,
		rules: func(lx *bytescan.Scanner) {
			lx.MustAddRule("STRING", base.Quoted('"', '\\'))
			lx.MustAddRule("SPACE", handSpace)
		},
	},
}

// newShapeScanner returns a scanner for shape over the endless repetition of its
// cycle, warmed up so that its buffers and checkpoint slots have their steady
// size.
func newShapeScanner(tb testing.TB, shape engineShape) (*bytescan.Scanner, func()) {
	tb.Helper()
	lx := bytescan.New(bufio.NewReader(&repeatingInput{pat: []byte(shape.cycle)}))
	shape.rules(lx)
	cycle := func() {
		for i := 0; i < shape.tokens; i++ {
			if _, err := lx.Next(); err != nil {
				tb.Fatalf("Next: %v", err)
			}
		}
	}
	for i := 0; i < 1000; i++ {
		cycle()
	}
	return lx, cycle
}

// TestEngineAllocationsPerToken covers the steady-state allocations of the
// composed engine: one per symbol that a composed rule reads while it is
// alive with at most four threads, as in these shapes, and none for the step
// that ends it.
//
// The ceilings hold only without the race detector. Under -race,
// sync.Pool.Put drops about one item in four, so the pooled stepper of a step
// is allocated again about a quarter of the time; the race build logs the
// numbers and asserts no ceiling.
func TestEngineAllocationsPerToken(t *testing.T) {
	for _, shape := range engineShapes {
		t.Run(shape.name, func(t *testing.T) {
			_, cycle := newShapeScanner(t, shape)
			got := testing.AllocsPerRun(1000, cycle) / float64(shape.tokens)
			t.Logf("%s on %q: %.2f allocations per token (race detector: %v)", shape.name, shape.cycle, got, raceDetector)
			if raceDetector {
				return
			}
			assert.LessOrEqual(t, got, shape.ceiling, "allocations per token")
		})
	}
}

// BenchmarkEngineShapes measures the steady-state cost of a token for each
// shape of TestEngineAllocationsPerToken.
func BenchmarkEngineShapes(b *testing.B) {
	for _, shape := range engineShapes {
		b.Run(shape.name, func(b *testing.B) {
			lx, _ := newShapeScanner(b, shape)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := lx.Next(); err != nil {
					b.Fatalf("Next: %v", err)
				}
			}
		})
	}
}

// TestEngineRuleConcurrent covers a composed rule shared by goroutines:
// one composed rule lexes the same stream in several scanners at once, and
// one mid-token Rule value is called from several goroutines with different
// symbols. Each composed rule is one that keeps no symbol log (no opaque leaf,
// no lookahead) or one that does, so both paths of the engine run under
// -race.
func TestEngineRuleConcurrent(t *testing.T) {
	rules := []struct {
		name string
		rule bytescan.Rule
	}{
		{"no symbol log", base.Quoted('"', '\\')},
		{"symbol log", core.Sequence(core.RuneRun(core.ASCIILetter), core.NotFollowedBy(core.Literal("x"), core.Literal("!")))},
	}
	for _, rc := range rules {
		t.Run(rc.name, func(t *testing.T) {
			setup := func(lx *bytescan.Scanner) {
				lx.MustAddRule("R", rc.rule)
				lx.MustAddRule("SPACE", handSpace)
			}
			input := strings.Repeat(`"ab\"c" ab "" abx abx! `, 32)
			want, err := collectStream(input, setup)
			require.NoError(t, err)

			var wg sync.WaitGroup
			streams := make([][]string, 8)
			errs := make([]error, len(streams))
			for i := range streams {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					streams[i], errs[i] = collectStream(input, setup)
				}(i)
			}
			wg.Wait()
			for i, got := range streams {
				require.NoError(t, errs[i], "scanner %d", i)
				assert.Equal(t, want, got, "scanner %d", i)
			}
		})
	}

	t.Run("mid-token value", func(t *testing.T) {
		r := base.Quoted('"', '\\')
		next, state := r(bytescan.NewSymbol('"', bytescan.FlagNone))
		require.Equal(t, bytescan.StateContinue, state)
		next, state = next(bytescan.NewSymbol('a', bytescan.FlagNone))
		require.Equal(t, bytescan.StateContinue, state)

		var wg sync.WaitGroup
		states := make([]bytescan.State, 16)
		for i := range states {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if i%2 == 0 {
					_, states[i] = next(bytescan.NewSymbol('"', bytescan.FlagNone))
					return
				}
				after, state := next(bytescan.NewSymbol('b', bytescan.FlagNone))
				if after != nil {
					_, state = after(bytescan.NewSymbol('"', bytescan.FlagNone))
				}
				states[i] = state
			}(i)
		}
		wg.Wait()
		for i, state := range states {
			assert.Equal(t, bytescan.StateAccept, state, "goroutine %d", i)
		}
	})
}
