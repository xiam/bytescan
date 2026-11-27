package bytescan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests call rulesProcessor.Process directly. Through Next, a zero-length
// match and a rule that matched nothing both become the same single-rune
// UNKNOWN token (bytescan.go:555-558 and processor.go:69-71), so only Process
// shows which path a token took.

// Every rule in this file is an engine probe: it exists to drive one path of
// rulesProcessor, and each one says which practice of docs/rule-authoring.md it
// breaks on purpose.

// excludeLast returns the verdict of a rule that excludes the last n symbols
// (n >= 1) from its match and then ends the pushback chain with (nil, s). It is
// the internal-package copy of the shared helper in tests/scanner_rules_test.go,
// which this package cannot import.
func excludeLast(n int, s State) (Rule, State) {
	if n < 1 {
		panic("excludeLast: n must be at least 1")
	}
	return pushBackChain(n-1, s), StatePushBack
}

// pushBackChain returns the pushback-chain callback that issues n more
// StatePushBack verdicts and then returns (nil, s). It ignores the symbol it is
// shown, which is already excluded from the match.
func pushBackChain(n int, s State) Rule {
	return func(Symbol) (Rule, State) {
		if n > 0 {
			return pushBackChain(n-1, s), StatePushBack
		}
		return nil, s
	}
}

// processInput feeds input and then the EOF symbol to a processor with the one
// rule typ, and returns the first settled Process result.
func processInput(t *testing.T, typ TokenType, rule Rule, input string) (TokenType, int) {
	t.Helper()

	rp := newRulesProcessor([]TokenType{typ}, map[TokenType]Rule{typ: rule})
	for _, r := range input {
		if got, n := rp.Process(NewSymbol(r, FlagNone)); n >= 0 {
			return got, n
		}
	}
	got, n := rp.Process(NewSymbol(RuneEOF, FlagEOF))
	if n < 0 {
		t.Fatalf("rule %q remained active at EOF", typ)
	}
	return got, n
}

// TestProcessPushBackAtEOF: the stack holds the EOF symbol, and it counts
// toward the bound. On input "a", one pushback at EOF matches "a"; two match
// zero symbols; three pass the bottom of the two-symbol stack and retire the
// rule, so no rule matches.
func TestProcessPushBackAtEOF(t *testing.T) {
	for _, tc := range []struct {
		pushbacks int
		wantType  TokenType
		wantLen   int
	}{
		{1, "R", 1},
		{2, "R", 0},
		{3, TokenTypeUnknown, 1},
	} {
		// Engine probe: two and three pushbacks exclude more than the match
		// on purpose (breaks practices 1, 2 and 6).
		rule := func(s Symbol) (Rule, State) {
			if s.Rune() != 'a' {
				return nil, StateReject
			}
			return func(s Symbol) (Rule, State) {
				if !s.IsEOF() {
					return nil, StateReject
				}
				return excludeLast(tc.pushbacks, StateAccept)
			}, StateContinue
		}

		gotType, gotLen := processInput(t, "R", rule, "a")
		assert.Equal(t, tc.wantType, gotType, "%d pushbacks", tc.pushbacks)
		assert.Equal(t, tc.wantLen, gotLen, "%d pushbacks", tc.pushbacks)
	}
}

// TestProcessZeroLengthMatch: one pushback to an acceptor on the first symbol is
// a zero-length match of the rule's own type, which nextToken turns into one
// UNKNOWN rune (bytescan.go:555-558). Two pushbacks on the first symbol pass
// the bottom of the stack instead, and Process reports no match.
func TestProcessZeroLengthMatch(t *testing.T) {
	// Engine probes: an empty match, and a chain past the bottom of the
	// stack, on purpose (breaks practices 2 and 6).
	zeroLength := func(Symbol) (Rule, State) { return excludeLast(1, StateAccept) }
	gotType, gotLen := processInput(t, "Z", zeroLength, "a")
	assert.Equal(t, TokenType("Z"), gotType)
	assert.Equal(t, 0, gotLen)

	overPush := func(Symbol) (Rule, State) { return excludeLast(2, StateAccept) }
	gotType, gotLen = processInput(t, "Z", overPush, "a")
	assert.Equal(t, TokenTypeUnknown, gotType)
	assert.Equal(t, 1, gotLen)
}

// TestProcessDecidesOnEOFSymbol: Process always decides on the EOF symbol. A
// rule still active after it is retired, an accept that counts it is
// discarded, and an invalid State retires the rule without a panic. With no
// recorded accept, Process reports UNKNOWN with length 1; an accept recorded
// before the EOF symbol stays.
func TestProcessDecidesOnEOFSymbol(t *testing.T) {
	// Engine probes: never settles, accepts the EOF symbol, and returns an
	// invalid State, on purpose (breaks practices 1, 2 and 6).
	var alwaysContinue Rule
	alwaysContinue = func(Symbol) (Rule, State) { return alwaysContinue, StateContinue }

	var alwaysAccept Rule
	alwaysAccept = func(Symbol) (Rule, State) { return alwaysAccept, StateAccept }

	invalidOnEOF := func(Symbol) (Rule, State) {
		return func(Symbol) (Rule, State) { return nil, State(9) }, StateAccept
	}

	for _, tc := range []struct {
		name     string
		rule     Rule
		wantType TokenType
		wantLen  int
	}{
		{"still active, no accept", alwaysContinue, TokenTypeUnknown, 1},
		{"accepts the EOF symbol after accepting a", alwaysAccept, "R", 1},
		{"invalid State on the EOF symbol after accepting a", invalidOnEOF, "R", 1},
	} {
		gotType, gotLen := processInput(t, "R", tc.rule, "a")
		assert.Equal(t, tc.wantType, gotType, tc.name)
		assert.Equal(t, tc.wantLen, gotLen, tc.name)
	}
}

// TestCallRule: callRule passes a returned verdict through unchanged, an
// invalid State and a non-nil next included, and turns a panic with any value
// into (nil, StateReject). The VM's reaction to these verdicts is tested
// through Next in tests/scanner_rule_states_test.go and
// tests/scanner_panic_test.go.
func TestCallRule(t *testing.T) {
	sym := NewSymbol('a', FlagNone)

	// Engine probes: the rules below return each verdict, an invalid State
	// included, or panic, on purpose (breaks practices 2 and 6).

	t.Run("returned verdicts pass through", func(t *testing.T) {
		var self Rule
		self = func(Symbol) (Rule, State) { return self, StateAccept }

		next, state := callRule(self, sym)
		assert.NotNil(t, next)
		assert.Equal(t, StateAccept, state)

		for _, want := range []State{StateContinue, StateAccept, StateReject, StatePushBack, State(9)} {
			want := want
			next, state := callRule(func(Symbol) (Rule, State) { return nil, want }, sym)
			assert.Nil(t, next)
			assert.Equal(t, want, state)
		}
	})

	panicValues := []struct {
		name  string
		value interface{}
	}{
		{"string", "boom"},
		{"error", assert.AnError},
		{"integer", 42},
		{"nil", nil},
	}

	check := func(t *testing.T) {
		for _, pv := range panicValues {
			pv := pv
			t.Run(pv.name, func(t *testing.T) {
				var next Rule
				var state State
				assert.NotPanics(t, func() {
					next, state = callRule(func(Symbol) (Rule, State) { panic(pv.value) }, sym)
				})
				assert.Nil(t, next)
				assert.Equal(t, StateReject, state)
			})
		}
	}

	t.Run("panics become a reject", check)

	// With panicnil=1, recover returns nil for panic(nil). callRule detects the
	// panic with a completion flag, so the rule is still retired.
	t.Run("panics become a reject with GODEBUG panicnil=1", func(t *testing.T) {
		t.Setenv("GODEBUG", "panicnil=1")
		check(t)
	})
}

// TestStackKeepsEverySymbol covers the packed stack: at returns every symbol
// that push stored, unchanged, and CopyText copies the runes and sums the
// sizes. The symbols include the edges of the packed range and symbols out of
// it, which a Cursor of the caller can report and which go to the wide list.
func TestStackKeepsEverySymbol(t *testing.T) {
	const allFlags = uint8(flagMask)
	edges := []Symbol{
		{r: RuneEOF, flags: uint8(FlagEOF)},
		{r: 0},
		{r: 'a', flags: allFlags, size: 1},
		{r: 0x10FFFF, size: 4},
		{r: runeField - 1, flags: allFlags, size: sizeField}, // the largest that fits
		{r: runeField}, // the rune is out of range
		{r: -2},        // a probe rune of rules/core
		{r: RuneEOF - 1000},
		{r: 1<<31 - 1}, // s.r+1 wraps
		{r: -1 << 31},
		{r: 'b', size: sizeField + 1}, // the size is out of range
		{r: 'c', size: 255},
		{r: 'd', flags: 0x80}, // a flag bit no Symbol of the package holds
	}

	for _, wideFirst := range []bool{false, true} {
		rp := newRulesProcessor(nil, map[TokenType]Rule{})
		syms := edges
		if wideFirst {
			syms = append([]Symbol{{r: runeField}}, edges...)
		}
		// Two rounds: Reset empties the stack and the wide list.
		for round := 0; round < 2; round++ {
			rp.Reset()
			for _, s := range syms {
				rp.push(s)
			}
			for i := 0; i < 3*len(syms); i++ { // past the end of syms: packed symbols after wide ones
				rp.push(syms[i%len(syms)])
			}
			want := make([]Symbol, 0, 4*len(syms))
			want = append(want, syms...)
			for i := 0; i < 3*len(syms); i++ {
				want = append(want, syms[i%len(syms)])
			}

			assert.Equal(t, len(want), rp.Len())
			wantRunes := make([]rune, len(want))
			wantSize := 0
			for i, s := range want {
				assert.Equal(t, s, rp.at(i), "symbol %d (wide first: %v)", i, wideFirst)
				wantRunes[i] = s.r
				wantSize += int(s.size)
			}
			for _, n := range []int{0, 1, len(want)} {
				got := make([]rune, n)
				size := rp.CopyText(got)
				assert.Equal(t, wantRunes[:n], got)
				sum := 0
				for _, s := range want[:n] {
					sum += int(s.size)
				}
				assert.Equal(t, sum, size)
			}
		}
	}

	// The scanner's own symbols always fit: a rune of the cursor (at most
	// 0x10FFFF, or utf8.RuneError), a size of 1 to 4, the defined flags.
	rp := newRulesProcessor(nil, map[TokenType]Rule{})
	for _, s := range []Symbol{{r: 0x10FFFF, flags: allFlags, size: 4}, {r: RuneEOF, flags: allFlags}} {
		rp.push(s)
	}
	assert.Empty(t, rp.wide, "a symbol of the scanner went to the wide list")
}

// TestPushBackShowsWideSymbols covers a pushback chain over a stack that mixes
// packed and wide symbols: every symbol a pushback shows is the one pushed
// there, and the match length counts both kinds.
func TestPushBackShowsWideSymbols(t *testing.T) {
	input := []Symbol{
		{r: 'a', size: 1},
		{r: -7},
		{r: 'b', size: 200},
		{r: 0x10FFFF, size: 4},
		{r: runeField + 3, flags: uint8(FlagBOL)},
		{r: 'c', size: 1},
	}
	end := Symbol{r: ';', size: 1}

	var shown []Symbol
	var chain func(left int) Rule
	chain = func(left int) Rule {
		return func(s Symbol) (Rule, State) {
			shown = append(shown, s)
			if left == 0 {
				return nil, StateAccept
			}
			return chain(left - 1), StatePushBack
		}
	}
	// Engine probe: it continues over the input and, at end, pushes back over
	// end and every input symbol but the first (breaks practices 2 and 6).
	var step Rule
	step = func(s Symbol) (Rule, State) {
		if s == end {
			return chain(len(input) - 1), StatePushBack
		}
		return step, StateContinue
	}

	rp := newRulesProcessor([]TokenType{"R"}, map[TokenType]Rule{"R": step})
	for _, s := range input {
		_, n := rp.Process(s)
		assert.Equal(t, -1, n)
	}
	typ, n := rp.Process(end)
	assert.Equal(t, TokenType("R"), typ)
	assert.Equal(t, 1, n)

	want := []Symbol{end}
	for i := len(input) - 1; i >= 1; i-- {
		want = append(want, input[i])
	}
	assert.Equal(t, want, shown)
	assert.NotEmpty(t, rp.wide)
}

// Check that stepping only the remaining rule still allows an earlier
// retired rule to win by length or by registration order.
func TestProcessSoleSurvivor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pushbacks int
		want      TokenType
		length    int
	}{
		{"retired rule has longer accept", 3, "early", 2},
		{"retired rule registered first", 2, "early", 2},
		{"survivor has longer accept", 1, "late", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			var early, late Rule
			early = func(s Symbol) (Rule, State) {
				calls = append(calls, "early:"+string(s.Rune()))
				if s.Rune() == 'a' {
					return early, StateContinue
				}
				return nil, StateAccept
			}
			late = func(s Symbol) (Rule, State) {
				if s.IsEOF() {
					calls = append(calls, "late:EOF")
					return excludeLast(tc.pushbacks, StateAccept)
				}
				calls = append(calls, "late:"+string(s.Rune()))
				return late, StateContinue
			}
			rp := newRulesProcessor([]TokenType{"early", "late"}, map[TokenType]Rule{
				"early": early,
				"late":  late,
			})

			for attempt := 0; attempt < 2; attempt++ {
				calls = nil
				for _, r := range "abc" {
					typ, length := rp.Process(NewSymbol(r, FlagNone))
					assert.Equal(t, TokenTypeUnknown, typ)
					assert.Equal(t, -1, length)
				}
				typ, length := rp.Process(NewSymbol(RuneEOF, FlagEOF))
				assert.Equal(t, tc.want, typ)
				assert.Equal(t, tc.length, length)
				assert.Equal(t, []string{"early:a", "late:a", "early:b", "late:b", "late:c", "late:EOF"}, calls)
				rp.Reset()
			}
		})
	}
}
