package tests

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check the rule states: the invariants of the state
// machine, the order and scope of StatePushBack, pushback chains, and the
// retirement of a rule that returns an invalid state.

// TestScannerStateMachineInvariants verifies that the state machine maintains its invariants.
func TestScannerStateMachineInvariants(t *testing.T) {
	t.Run("State Transition Validation", func(t *testing.T) {
		input := "test"

		// Engine probe: records its transitions on purpose (breaks practices 1,
		// 2, 3, 4 and 6).
		var transitions []string
		var mu sync.Mutex

		trackingRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			mu.Lock()
			transitions = append(transitions, fmt.Sprintf("Start(%c)", s.Rune()))
			mu.Unlock()

			if s.Rune() >= 'a' && s.Rune() <= 'z' {
				var continueRule bytescan.Rule
				continueRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					mu.Lock()
					transitions = append(transitions, fmt.Sprintf("Continue(%c)", s.Rune()))
					mu.Unlock()

					if s.Rune() >= 'a' && s.Rune() <= 'z' {
						return continueRule, bytescan.StateAccept
					}
					return nil, bytescan.StateReject
				}
				return continueRule, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("TRACK", trackingRule)

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, "test", lex.Text())

		assert.Equal(t, "Start(t)", transitions[0])
		assert.Contains(t, transitions, "Continue(e)")
		assert.Contains(t, transitions, "Continue(s)")
		assert.Contains(t, transitions, "Continue(t)")
	})

	t.Run("Scanner Lifecycle", func(t *testing.T) {
		input := "ab cd"

		type lifecycleEvent struct {
			event string
			rune  rune
		}
		// Engine probe: records lifecycle events on purpose (breaks practices
		// 1, 2, 3, 4 and 6).
		var events []lifecycleEvent
		var mu sync.Mutex

		lifecycleRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			mu.Lock()
			events = append(events, lifecycleEvent{"create", s.Rune()})
			mu.Unlock()

			if s.Rune() >= 'a' && s.Rune() <= 'z' {
				return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
					mu.Lock()
					events = append(events, lifecycleEvent{"continue", s.Rune()})
					mu.Unlock()
					r := s.Rune()
					if r >= 'a' && r <= 'z' {
						return nil, bytescan.StateAccept
					}
					if r == ' ' {
						mu.Lock()
						events = append(events, lifecycleEvent{"reject_space", r})
						mu.Unlock()
						return nil, bytescan.StateReject
					}
					return nil, bytescan.StateReject
				}, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("LIFECYCLE", lifecycleRule)
		lx.MustAddRule("WS", whitespaceRule)

		for {
			_, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
		}

		assert.Greater(t, len(events), 0, "Should have lifecycle events")
		assert.Equal(t, lifecycleEvent{"create", 'a'}, events[0])

		foundContinueB := false
		for _, e := range events {
			if e.event == "continue" && e.rune == 'b' {
				foundContinueB = true
				break
			}
		}
		assert.True(t, foundContinueB, "Should have continued with 'b'")
	})

	t.Run("Accept Position Tracking", func(t *testing.T) {
		input := "aaabbb"

		// Engine probe: records accept positions on purpose (breaks practices
		// 1, 2, 4 and 6).
		var acceptPositions []int
		var mu sync.Mutex

		positionTrackingRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			currentPos := 0
			var track func(s bytescan.Symbol) (bytescan.Rule, bytescan.State)
			track = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				currentPos++
				r := s.Rune()
				if r == 'a' {
					mu.Lock()
					acceptPositions = append(acceptPositions, currentPos)
					mu.Unlock()
					return track, bytescan.StateAccept
				}
				if r == 'b' {
					// Don't accept 'b', forcing backtrack to last 'a'.
					return nil, bytescan.StateReject
				}
				return nil, bytescan.StateReject
			}
			return track(s)
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("POSITION", positionTrackingRule)
		lx.MustAddRule("B", newLiteralRule("bbb"))

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, "aaa", lex.Text())

		assert.Contains(t, acceptPositions, 1)
		assert.Contains(t, acceptPositions, 2)
		assert.Contains(t, acceptPositions, 3)

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, "bbb", lex.Text())
	})
}

// TestPushBackSymbolOrder: each pushback in a chain shows `next` the next symbol
// down the stack, starting with the symbol just read. It also shows the count
// rule: a helper that issues two pushbacks, entered through one more
// StatePushBack, excludes three symbols.
func TestPushBackSymbolOrder(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// Engine probe: records the symbols it is shown, in hand-built
	// pushback-chain callbacks, on purpose (breaks practices 1, 2, 4 and 6).
	var seen []rune
	// record(k) records the symbol it is shown, then pushes back k more times.
	var record func(k int) bytescan.Rule
	record = func(k int) bytescan.Rule {
		return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			seen = append(seen, s.Rune())
			if k == 0 {
				return nil, bytescan.StateAccept
			}
			return record(k - 1), bytescan.StatePushBack
		}
	}
	rule := newLiteralThenRule("abc", func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.Rune() != 'd' {
			return nil, bytescan.StateReject
		}
		return record(2), bytescan.StatePushBack
	})

	lx := bytescan.New(strings.NewReader("abcd"))
	lx.MustAddRule(lexTypeR, rule)

	expected := tokensWith(bytescan.NewTokenFromString(lexTypeR, "a", 0), "bcd")
	assert.Equal(t, tokenKeys(expected), tokenKeys(lexAll(t, lx)))
	assert.Equal(t, []rune{'d', 'c', 'b'}, seen, "the chain must see the stack from the top down")
}

// TestPushBackIsPerRule: one rule's pushback does not change what another rule
// is shown. The stack is the same for every rule within a step.
func TestPushBackIsPerRule(t *testing.T) {
	const (
		lexTypePush = bytescan.TokenType("PUSH")
		lexTypeSeen = bytescan.TokenType("SEEN")
	)

	// PUSH reads "ab" and, on 'c', excludes 'c' and 'b' and accepts "a".
	pushRule := newABeforeBCRule()

	// SEEN runs after PUSH in each step. It records every symbol it is shown and
	// accepts every letter.
	//
	// Engine probe: records every symbol it is shown on purpose (breaks
	// practices 2, 4 and 6).
	var seen []rune
	var seenRule bytescan.Rule
	seenRule = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		seen = append(seen, s.Rune())
		if isASCIILetter(s.Rune()) {
			return seenRule, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}

	lx := bytescan.New(strings.NewReader("abc"))
	lx.MustAddRule(lexTypePush, pushRule)
	lx.MustAddRule(lexTypeSeen, seenRule)

	lex, err := lx.Next()
	require.NoError(t, err)
	assert.Equal(t, tokenKeys([]*bytescan.Token{bytescan.NewTokenFromString(lexTypeSeen, "abc", 0)}),
		tokenKeys([]*bytescan.Token{lex}))
	assert.Equal(t, []rune{'a', 'b', 'c', bytescan.RuneEOF}, seen,
		"PUSH's chain on 'c' must not change the symbols SEEN is shown")

	_, err = lx.Next()
	assert.Equal(t, io.EOF, err)
}

// TestPushBackAtEOF: at end of input the stack also holds the EOF symbol, and
// the first pushback removes it. On input "a", one pushback leaves "a"; two are a
// zero-length match; three pass the bottom of the stack. Two and three give the
// same token here; TestProcessPushBackAtEOF tells them apart.
func TestPushBackAtEOF(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	for _, tc := range []struct {
		pushbacks int
		expected  *bytescan.Token
	}{
		{1, bytescan.NewTokenFromString(lexTypeR, "a", 0)},
		{2, bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0)},
		{3, bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "a", 0)},
	} {
		t.Run(fmt.Sprintf("%d pushbacks", tc.pushbacks), func(t *testing.T) {
			// Engine probe: two and three pushbacks exclude more than the
			// match on purpose (breaks practices 2 and 6).
			rule := newLiteralThenRule("a", func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				if !s.IsEOF() {
					return nil, bytescan.StateReject
				}
				return excludeLast(tc.pushbacks, bytescan.StateAccept)
			})

			lx := bytescan.New(strings.NewReader("a"))
			lx.MustAddRule(lexTypeR, rule)
			assert.Equal(t, tokenKeys([]*bytescan.Token{tc.expected}), tokenKeys(lexAll(t, lx)))
		})
	}
}

// TestPushBackRetiresRuleKeepsEarlierAccept: a pushback past the bottom of the
// stack, or (nil, StatePushBack), retires the rule. The accept it recorded
// earlier stays.
func TestPushBackRetiresRuleKeepsEarlierAccept(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// Engine probes: the chains below pass the bottom of the stack, or push
	// back with no next rule, on purpose (breaks practices 1, 2, 5 and 6).
	//
	// more accepts every real symbol. It is reached only if the VM does not
	// retire the rule, and then the rule would match all of "abc".
	var more bytescan.Rule
	more = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		return more, bytescan.StateAccept
	}

	// over(k) pushes back k more times and then hands the rule to more.
	var over func(k int) bytescan.Rule
	over = func(k int) bytescan.Rule {
		return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if k == 0 {
				return more, bytescan.StateAccept
			}
			return over(k - 1), bytescan.StatePushBack
		}
	}

	for _, tc := range []struct {
		name string
		onB  bytescan.Rule
	}{
		{
			// On 'b' the stack holds two symbols, and the chain issues three
			// pushbacks.
			name: "pushback past the bottom of the stack",
			onB: func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return over(2), bytescan.StatePushBack
			},
		},
		{
			name: "nil next with StatePushBack",
			onB: func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return nil, bytescan.StatePushBack
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				if s.Rune() != 'a' {
					return nil, bytescan.StateReject
				}
				return tc.onB, bytescan.StateAccept
			}

			lx := bytescan.New(strings.NewReader("abc"))
			lx.MustAddRule(lexTypeR, rule)

			expected := tokensWith(bytescan.NewTokenFromString(lexTypeR, "a", 0), "bc")
			assert.Equal(t, tokenKeys(expected), tokenKeys(lexAll(t, lx)))
		})
	}
}

// TestPushBackChainEndingContinue pins the v0.2.3 result of an unsupported chain
// ending: a chain that ends in StateContinue. The VM keeps the pushback count
// for the current symbol step only, so the rule stays active and the excluded
// symbol is counted back in by later accepts. A chain must end in
// (nil, StateAccept) or StateReject; see "Rule states and pushback" in
// docs/specs/rule-vm-contract.md.
func TestPushBackChainEndingContinue(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// Engine probe: a chain that ends in StateContinue, on purpose (breaks
	// practices 1, 2 and 5).
	//
	// tail accepts every real symbol and rejects the EOF symbol.
	var tail bytescan.Rule
	tail = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		return tail, bytescan.StateAccept
	}

	// The rule reads "ab" and, on the real symbol 'c', pushes 'c' back and
	// continues into tail. The pushback is forgotten when the step ends.
	rule := newLiteralThenRule("ab", func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return tail, bytescan.StateContinue
		}, bytescan.StatePushBack
	})

	lx := bytescan.New(strings.NewReader("abcde"))
	lx.MustAddRule(lexTypeR, rule)

	expected := []*bytescan.Token{bytescan.NewTokenFromString(lexTypeR, "abcde", 0)}
	assert.Equal(t, tokenKeys(expected), tokenKeys(lexAll(t, lx)))
}

// TestPushBackChainEndingAcceptWithNext pins the v0.2.3 result of the other
// unsupported chain ending: (non-nil next, StateAccept). The VM records the
// shorter length, keeps the rule active, and drops the pushback count when the
// step ends, so a later accept counts the excluded symbol back in. The
// supported ending, (nil, StateAccept), is shown for contrast. See "Rule states
// and pushback" in docs/specs/rule-vm-contract.md.
func TestPushBackChainEndingAcceptWithNext(t *testing.T) {
	const lexTypeR = bytescan.TokenType("R")

	// Engine probe: a chain that ends in a non-nil next with StateAccept, on
	// purpose (breaks practices 1, 2 and 5).
	//
	// more accepts every real symbol and rejects the EOF symbol.
	var more bytescan.Rule
	more = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		return more, bytescan.StateAccept
	}

	// The rule continues at 'a' and, on the real symbol 'X', pushes 'X' back
	// and accepts with next.
	ruleWithNext := func(next bytescan.Rule) bytescan.Rule {
		return newLiteralThenRule("a", func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() != 'X' {
				return nil, bytescan.StateReject
			}
			return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return next, bytescan.StateAccept
			}, bytescan.StatePushBack
		})
	}

	runLookaheadCases(t, []lookaheadCase{
		{
			name:     "unsupported: non-nil next",
			input:    "aXbc",
			expected: []*bytescan.Token{bytescan.NewTokenFromString(lexTypeR, "aXbc", 0)},
		},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, ruleWithNext(more))
	})

	runLookaheadCases(t, []lookaheadCase{
		{
			name:     "supported: nil next",
			input:    "aXbc",
			expected: tokensWith(bytescan.NewTokenFromString(lexTypeR, "a", 0), "Xbc"),
		},
	}, func(lx *bytescan.Scanner) {
		lx.MustAddRule(lexTypeR, ruleWithNext(nil))
	})
}

// assertRetireStream lexes all of lx and compares each token's type, text, rune
// offset, and byte span with expected. The inputs are ASCII, so an expected
// token's byte span is [offset, offset+len). lexAll fails the test on any
// error, so the stream must end in io.EOF.
func assertRetireStream(t *testing.T, lx *bytescan.Scanner, expected []*bytescan.Token) {
	t.Helper()

	key := func(typ bytescan.TokenType, text string, offset, start, end int) string {
		return fmt.Sprintf("%s|%q|%d|%d-%d", typ, text, offset, start, end)
	}

	want := make([]string, len(expected))
	for i, lex := range expected {
		want[i] = key(lex.Type(), lex.Text(), lex.Offset(), lex.Offset(), lex.Offset()+lex.Len())
	}

	found := lexAll(t, lx)
	got := make([]string, len(found))
	for i, lex := range found {
		got[i] = key(lex.Type(), lex.Text(), lex.Offset(), lex.Span().Start().ByteOffset(), lex.Span().End().ByteOffset())
	}

	assert.Equal(t, want, got)

	_, err := lx.Next()
	assert.Equal(t, io.EOF, err, "lexing must stay at io.EOF")
}

// TestInvalidStateRetiresRule: a State outside the four constants retires only
// the rule that returned it, as StateReject does. The VM does not panic, and a
// sibling rule's match is emitted. See "Rule states and pushback" in
// docs/specs/rule-vm-contract.md.
func TestInvalidStateRetiresRule(t *testing.T) {
	const (
		lexTypeWord = bytescan.TokenType("WORD")
		lexTypeBad  = bytescan.TokenType("BAD")
		invalid     = bytescan.State(9)
	)

	// Engine probes: both return an invalid State on purpose (breaks
	// practices 1 and 2).
	//
	// direct continues on its first symbol and returns the invalid State on
	// its second.
	direct := func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return nil, invalid
		}, bytescan.StateContinue
	}

	// chained continues on its first symbol. On its second it starts a
	// pushback chain whose last step returns the invalid State, so the VM
	// meets the value at the end of the chain, not as a direct verdict.
	chained := func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return nil, invalid
			}, bytescan.StatePushBack
		}, bytescan.StateContinue
	}

	for _, tc := range []struct {
		name string
		bad  bytescan.Rule
	}{
		{"direct", direct},
		{"end of pushback chain", chained},
	} {
		t.Run(tc.name+", with a sibling", func(t *testing.T) {
			// BAD is registered first, so it steps before WORD on each symbol.
			lx := bytescan.New(strings.NewReader("ab"))
			lx.MustAddRule(lexTypeBad, tc.bad)
			lx.MustAddRule(lexTypeWord, newASCIILettersRule())

			assertRetireStream(t, lx, []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeWord, "ab", 0),
			})
		})

		t.Run(tc.name+", only rule", func(t *testing.T) {
			// BAD never accepts, so each token is one UNKNOWN rune. On the
			// last rune its second symbol is the EOF symbol.
			lx := bytescan.New(strings.NewReader("ab"))
			lx.MustAddRule(lexTypeBad, tc.bad)

			assertRetireStream(t, lx, unknownTokens("ab", 0))
		})
	}
}
