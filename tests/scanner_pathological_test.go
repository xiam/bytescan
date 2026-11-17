package tests

import (
	"crypto/rand"
	"io"
	mrand "math/rand"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file are adversarial: rules that backtrack deeply, never
// decide, match zero symbols, or behave at random. The scanner must stay correct
// and terminate.

// TestScannerPathologicalRuleWithDeepBacktracking pins the only-grows rule with a
// deep pushback chain. The rule accepts every 'a', continues through 'b', and at
// end of input runs a chain of backtrackDepth+1 pushbacks. On n 'a's followed by
// 'b' the stack holds n+2 symbols (EOF included), so the chain's accept has
// length 1. That is shorter than the n symbols the rule already accepted, so the
// VM ignores it (processor.go:125): a pushback chain, however deep, cannot
// shorten or withdraw a match the rule already recorded.
func TestScannerPathologicalRuleWithDeepBacktracking(t *testing.T) {
	const lexTypePathological = bytescan.TokenType("PATHOLOGICAL")
	const lexTypeUnknown = bytescan.TokenTypeUnknown

	testCases := []struct {
		name           string
		input          string
		backtrackDepth int
		expected       []*bytescan.Token
	}{
		{
			name:           "Shallow backtrack",
			input:          "aaab",
			backtrackDepth: 3,
			expected: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypePathological, "aaa", 0),
				bytescan.NewTokenFromString(lexTypeUnknown, "b", 3),
			},
		},
		{
			name:           "Medium backtrack",
			input:          "aaaaaaaab",
			backtrackDepth: 8,
			expected: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypePathological, "aaaaaaaa", 0),
				bytescan.NewTokenFromString(lexTypeUnknown, "b", 8),
			},
		},
		{
			name:           "Deep backtrack",
			input:          strings.Repeat("a", 100) + "b",
			backtrackDepth: 100,
			expected: []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypePathological, strings.Repeat("a", 100), 0),
				bytescan.NewTokenFromString(lexTypeUnknown, "b", 100),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Engine probe: accepts every 'a' eagerly and then ends with a
			// shorter pushback chain, on purpose (breaks practices 2 and 5).
			var pathologicalRule, finalState bytescan.Rule

			// Excludes the EOF symbol, 'b', and backtrackDepth-1 of the 'a's.
			finalState = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return excludeLast(tc.backtrackDepth+1, bytescan.StateAccept)
			}

			// This rule accepts every 'a' but continues matching. When it sees a 'b',
			// it continues into the final state, whose deep chain reports a match of
			// length 1. The earlier accept of all the 'a's stays.
			pathologicalRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				r := s.Rune()
				if r == 'a' {
					return pathologicalRule, bytescan.StateAccept
				}
				if r == 'b' {
					return finalState, bytescan.StateContinue
				}
				return nil, bytescan.StateReject
			}

			lx := bytescan.New(strings.NewReader(tc.input))
			lx.MustAddRule(lexTypePathological, pathologicalRule)

			var found []*bytescan.Token
			for {
				lex, err := lx.Next()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				found = append(found, lex)
			}

			assert.Equal(t, tokenKeys(tc.expected), tokenKeys(found), "Backtrack depth %d failed", tc.backtrackDepth)
		})
	}
}

// TestScannerZeroLengthMatchPrevention verifies that zero-length matches are properly
// prevented from causing infinite loops.
func TestScannerZeroLengthMatchPrevention(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected int
	}{
		{"Single character", "a", 1},
		{"Multiple characters", "abc", 3},
		{"Long input", strings.Repeat("x", 100), 100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// This rule always produces a zero-length match: one pushback on the
			// first symbol excludes it, and the accept has length 0. The VM
			// commits no empty token and emits one UNKNOWN rune instead
			// (bytescan.go:555-558); TestProcessZeroLengthMatch proves the
			// branch.
			//
			// Engine probe: an empty match on purpose (breaks practices 2 and
			// 6).
			zeroLengthRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return excludeLast(1, bytescan.StateAccept)
			}

			// Each Next reads one symbol and the rule takes two invocations
			// on it (the read and the pushback), so a run over the input and
			// the EOF symbol takes 2*(len+1). The budget doubles that; an
			// infinite loop inside one Next exceeds it and fails the test.
			budget := 4 * (len(tc.input) + 1)

			lx := bytescan.New(strings.NewReader(tc.input))
			lx.MustAddRule("ZERO", boundedRule(t, budget, zeroLengthRule))

			tokenCount := 0
			iterations := 0
			maxIterations := tc.expected*2 + 10

			for iterations < maxIterations {
				lex, err := lx.Next()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				require.NotNil(t, lex)

				// Should produce UNKNOWN tokens, one per character.
				assert.Equal(t, bytescan.TokenTypeUnknown, lex.Type())
				assert.Equal(t, 1, lex.Len(), "Zero-length match should result in single-char UNKNOWN token")

				tokenCount++
				iterations++
			}

			assert.Equal(t, tc.expected, tokenCount, "Should produce one token per input character")
			assert.Less(t, iterations, maxIterations, "Too many iterations - possible infinite loop")
		})
	}
}

func TestScannerPathologicalRules(t *testing.T) {
	t.Run("Interleaving Longest Match", func(t *testing.T) {
		// Ensures the processor tracks the longest match even when the winning rule alternates.
		// Rule A matches odd lengths, Rule B matches even. The absolute longest must win.
		const (
			lexTypeOdd  = bytescan.TokenType("ODD")
			lexTypeEven = bytescan.TokenType("EVEN")
		)
		input := "aaaaa"

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeOdd, newOddARunRule())
		lx.MustAddRule(lexTypeEven, newEvenARunRule())

		// Expected: Rule A's lastAccept is 5, Rule B's is 4. The processor must pick the longest.
		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeOdd, "aaaaa", 0),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Bait and Switch Rule", func(t *testing.T) {
		// A malicious rule ("Bait") finds a long match, but then backtracks to report a shorter one.
		// The scanner must not be fooled and should select the longest match recorded by any rule.
		// This test pins the only-grows rule: within a token a rule's recorded match only
		// grows (processor.go:125), so a pushback chain cannot shorten an earlier accept.
		const (
			lexTypeBait   = bytescan.TokenType("BAIT")
			lexTypeHonest = bytescan.TokenType("HONEST")
		)
		input := "token;"

		// Engine probe: accepts eagerly and then ends with a shorter pushback
		// chain, on purpose (breaks practices 1, 2 and 5).
		var baitRule bytescan.Rule
		baitFinalizer := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == ';' {
				// Exclude five of the six symbols ("oken;"): a match of length 1.
				return excludeLast(5, bytescan.StateAccept)
			}
			return nil, bytescan.StateReject
		}
		match_n := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'n' {
				return baitFinalizer, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
		match_e := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'e' {
				return match_n, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
		match_k := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'k' {
				return match_e, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
		match_o := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'o' {
				return match_k, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
		baitRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 't' {
				return match_o, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeBait, baitRule)
		lx.MustAddRule(lexTypeHonest, newLiteralRule("token"))

		// Expected: Both rules achieve a maximal match of length 5. The Bait rule's
		// attempt to backtrack to a shorter match is ignored. Since "Bait" was added
		// first, it wins the tie for the longest match.
		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeBait, "token", 0),
			bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, ";", 5),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Rule Rejects After Full Buffer Consumption (Veto Rule)", func(t *testing.T) {
		// A rule consumes a valid token, but upon seeing a terminator, rejects its own future
		// participation. The scanner should still honor the longest match already found by that rule.
		const (
			lexTypeVeto   = bytescan.TokenType("VETO")
			lexTypeHonest = bytescan.TokenType("HONEST")
		)
		input := "abc;"

		// Engine probe: accepts eagerly while a lookahead decision is pending,
		// on purpose (breaks practices 1, 2 and 5).
		var vetoRule bytescan.Rule
		vetoFinalizer := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == ';' {
				// Veto: After seeing ';', push it back and end the chain with
				// the rule's own StateReject. This does NOT erase the lastAccept
				// value already recorded for this rule.
				return excludeLast(1, bytescan.StateReject)
			}
			return nil, bytescan.StateReject
		}
		match_c := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'c' {
				return vetoFinalizer, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
		match_b := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'b' {
				return match_c, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
		vetoRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'a' {
				return match_b, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeVeto, vetoRule)
		lx.MustAddRule(lexTypeHonest, newLiteralRule("abc"))

		// Expected: Both VETO and HONEST rules achieve a maximal match of length 3.
		// The VETO rule's subsequent StateReject does not retroactively cancel
		// its match. Since VETO was added first, it wins the tie.
		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeVeto, "abc", 0),
			bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, ";", 3),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Rule Returns nil with StatePushBack", func(t *testing.T) {
		// An invalid user rule returns (nil, StatePushBack). The scanner must handle
		// this by deactivating the rule after the pushback to prevent errors.
		const lexTypeBad = bytescan.TokenType("BAD_PUSHBACK")
		input := "ab"

		// Engine probe: a pushback with no next rule, on purpose (breaks
		// practices 1, 2 and 5).
		badRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() == 'a' {
				return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					// Push back 'b' but provide no next rule.
					return nil, bytescan.StatePushBack
				}, bytescan.StateContinue
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeBad, badRule)
		// This honest rule will be chosen after the bad one deactivates.
		lx.MustAddRule("A", newLiteralRule("a"))

		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(bytescan.TokenType("A"), "a", 0),
			bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "b", 1),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Always Reject Rule", func(t *testing.T) {
		const lexTypeReject = bytescan.TokenType("REJECT")
		input := "abc"

		// Engine probe: rejects every symbol without a state of its own, on
		// purpose, to pin the no-match path (breaks practices 1, 2 and 7).
		alwaysRejectRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeReject, alwaysRejectRule)

		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0),
			bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "b", 1),
			bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "c", 2),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Always Accept Rule", func(t *testing.T) {
		const lexTypeAccept = bytescan.TokenType("ACCEPT")
		input := "abc"

		// Engine probe: accepts every symbol, the EOF symbol included, on
		// purpose (breaks practice 2).
		alwaysAcceptRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return nil, bytescan.StateAccept
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeAccept, alwaysAcceptRule)

		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeAccept, "a", 0),
			bytescan.NewTokenFromString(lexTypeAccept, "b", 1),
			bytescan.NewTokenFromString(lexTypeAccept, "c", 2),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Exponential State Explosion", func(t *testing.T) {
		// This simulates a badly written regex like (a*)* that could cause
		// exponential state explosion.
		const lexTypeExponential = bytescan.TokenType("EXPONENTIAL")
		input := strings.Repeat("a", 20) + "b"

		// Engine probe: counts its calls in a captured variable on purpose
		// (breaks practices 1, 2, 4 and 6).
		callCount := 0
		var exponentialRule bytescan.Rule
		exponentialRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			callCount++
			if callCount > 1000 { // Safety limit.
				return nil, bytescan.StateReject
			}

			if s.Rune() == 'a' {
				return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					if s.Rune() == 'a' {
						// Branch: either continue or accept.
						return exponentialRule, bytescan.StateAccept
					}
					return nil, bytescan.StateReject
				}, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		// The rule takes one invocation per symbol read. The first token reads
		// the whole input, then "b" is read again and the EOF symbol once, so
		// a run takes len+2 invocations. A budget linear in the input fails
		// the test, from inside the rule, on an explosion or a scanner that
		// stops terminating.
		budget := 4 * (len(input) + 1)

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeExponential, boundedRule(t, budget, exponentialRule))

		for {
			_, err := lx.Next()
			if err == io.EOF {
				break
			}
		}

		assert.Less(t, callCount, 1001, "Rule call count should be limited")
	})

	t.Run("Memory Exhaustion Attack", func(t *testing.T) {
		const lexTypeMemory = bytescan.TokenType("MEMORY")
		input := "start"

		// Engine probe: allocates memory outside its returned rules on purpose
		// (breaks practices 2, 3, 4 and 6).
		var memoryTracker [][]byte
		memoryExhaustionRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if len(memoryTracker) < 10 { // Limit to prevent actual exhaustion in test.
				memoryTracker = append(memoryTracker, make([]byte, 1024*1024)) // 1MB
			}

			if s.Rune() >= 'a' && s.Rune() <= 'z' {
				return nil, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeMemory, memoryExhaustionRule)

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, lexTypeMemory, lex.Type())
		assert.Equal(t, "s", lex.Text())

		memoryTracker = nil
	})

	t.Run("Recursive Rule Cycles", func(t *testing.T) {
		const lexTypeCycle = bytescan.TokenType("CYCLE")
		input := "abc"

		// The rule's states form a cycle: a -> b -> c -> a.
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeCycle, newABCCycleRule())

		expected := []*bytescan.Token{
			bytescan.NewTokenFromString(lexTypeCycle, "abc", 0),
		}

		var found []*bytescan.Token
		for {
			lex, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			found = append(found, lex)
		}
		assert.Equal(t, tokenKeys(expected), tokenKeys(found))
	})

	t.Run("Buffer Overflow Attempt", func(t *testing.T) {
		const lexTypeOverflow = bytescan.TokenType("OVERFLOW")
		input := strings.Repeat("a", 65536)

		// The rule stops after 102 'a's, so the input is many tokens.
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeOverflow, newARunUpToRule(102))

		tokenCount := 0
		for {
			_, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			tokenCount++
		}
		assert.Greater(t, tokenCount, 0, "Should process at least one token")
	})

	t.Run("Concurrent State Mutation", func(t *testing.T) {
		// A rule that mutates shared state should be safe as long as the state is
		// managed correctly (e.g., with atomics).
		const lexTypeMutate = bytescan.TokenType("MUTATE")
		input := "test test test "

		sharedCounter := int32(0)

		// Engine probe: writes a shared counter on every call, on purpose
		// (breaks practices 2, 3, 4 and 6).
		var mutatingRule bytescan.Rule
		mutatingRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			atomic.AddInt32(&sharedCounter, 1)
			r := s.Rune()
			if r >= 'a' && r <= 'z' {
				return mutatingRule, bytescan.StateAccept
			}
			if r == ' ' {
				return nil, bytescan.StateReject
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeMutate, mutatingRule)
		lx.MustAddRule("WS", whitespaceRule)

		tokenCount := 0
		for {
			_, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			tokenCount++
		}

		assert.Greater(t, atomic.LoadInt32(&sharedCounter), int32(0), "Rule should have been called")
		assert.Equal(t, 6, tokenCount, "Should produce 6 tokens (3 words + 3 spaces)")
	})
}

// TestScannerChaos is a non-deterministic stress test that uses a rule with
// random behavior to process random input. Its primary purpose is to detect
// panics or deadlocks under unpredictable conditions. Run with -race.
func TestScannerChaos(t *testing.T) {
	const (
		lexTypeChaos = bytescan.TokenType("CHAOS")
		inputSize    = 10000
	)

	inputBytes := make([]byte, inputSize)
	_, err := io.ReadFull(rand.Reader, inputBytes)
	require.NoError(t, err)
	for i, b := range inputBytes {
		inputBytes[i] = (b % 94) + 32 // Keep it to printable ASCII
	}
	input := string(inputBytes)

	lx := bytescan.New(strings.NewReader(input))

	rng := mrand.New(mrand.NewSource(42))

	// Engine probe: random verdicts and a captured counter, on purpose (breaks
	// practices 2, 4, 5 and 6).
	matchLength := 0
	var chaosRule bytescan.Rule
	chaosRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		matchLength++
		u := rng.Float64()

		if u < 0.1 && matchLength > 1 {
			// Exclude the symbol just read and accept the rest.
			return excludeLast(1, bytescan.StateAccept)
		}

		if u < 0.7 && matchLength < 100 {
			return chaosRule, bytescan.StateContinue
		}

		matchLength = 0
		if u < 0.85 {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}

	// The rule continues only while matchLength is below 100, so a token reads
	// at most 100 symbols. The excludeLast branch does not reset matchLength,
	// which only makes the next token shorter. Each read takes at most one
	// extra pushback invocation, and every token consumes at least one symbol,
	// so a run over the input and the EOF symbol takes at most
	// 2*100*(inputSize+1) invocations. This is a worst-case bound, far above
	// the count a correct run takes; a scanner that stops terminating exceeds it
	// and fails the test.
	//
	// The rule ignores the symbol it reads and its RNG has a fixed seed, so a
	// run does not depend on the random input: a failure reproduces with any
	// input.
	budget := 2 * 100 * (inputSize + 1)
	lx.MustAddRule(lexTypeChaos, boundedRule(t, budget, chaosRule))

	tokenCount := 0
	for {
		_, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Errors are expected in a chaos test; the goal is to ensure no panics or hangs.
			t.Logf("Chaos test encountered error (expected): %v", err)
			break
		}
		tokenCount++

		if tokenCount > inputSize*2 {
			t.Logf("Chaos test produced maximum tokens, stopping")
			break
		}
	}

	assert.Greater(t, tokenCount, 0, "Chaos test should have produced at least one token")
}

// TestScannerInfiniteLoopProtection specifically tests that the scanner protects against
// various types of infinite loops.
func TestScannerInfiniteLoopProtection(t *testing.T) {
	// Engine probes: each rule never settles or matches nothing, on purpose
	// (breaks practices 1, 2, 5 and 6).
	testCases := []struct {
		name string
		rule bytescan.Rule
	}{
		{
			name: "Always Continue",
			rule: func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				var loop bytescan.Rule
				loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return loop, bytescan.StateContinue
				}
				return loop(s)
			},
		},
		{
			name: "Always PushBack",
			rule: func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				var loop bytescan.Rule
				loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					return loop, bytescan.StatePushBack
				}
				return loop(s)
			},
		},
		{
			name: "Zero Length Accept Loop",
			// One pushback on the first symbol excludes it: a zero-length accept.
			rule: func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return excludeLast(1, bytescan.StateAccept)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := "test"

			// A rule that never settles is retired at EOF and yields a
			// one-rune UNKNOWN token, so the k-th Next from the end reads at
			// most k symbols (the rest of the input and the EOF symbol). Each
			// of these rules takes at most two invocations per symbol read,
			// so a run takes at most (len+1)*(len+2) invocations. A scanner
			// stuck in a loop exceeds the budget and fails the test.
			n := len(input)
			budget := (n + 1) * (n + 2)

			lx := bytescan.New(strings.NewReader(input))
			lx.MustAddRule("LOOP", boundedRule(t, budget, tc.rule))

			iterations := 0
			maxIterations := 100

			for iterations < maxIterations {
				_, err := lx.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					// Expected - the scanner should error out of an infinite loop.
					break
				}
				iterations++
			}
		})
	}
}
