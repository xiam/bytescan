package tests

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check rules that panic: the scanner recovers, retires the
// rule, keeps the accepts the rule recorded before the panic, and stays usable,
// also when many goroutines lex at once.

// TestScannerPanickingRuleIsRecoveredAndRetired: the only rule panics on every
// symbol. The VM recovers each panic and retires the rule as for StateReject,
// so no rule matches and each rune is one UNKNOWN token, then io.EOF. The
// panic never reaches the caller. See "Panics" in
// docs/specs/rule-vm-contract.md.
func TestScannerPanickingRuleIsRecoveredAndRetired(t *testing.T) {
	const lexTypePanic = bytescan.TokenType("PANIC")
	input := "crash"

	// Engine probe: panics on purpose (breaks practice 6).
	panickingRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		panic("user rule panicked")
	}

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypePanic, panickingRule)

	assertRetireStream(t, lx, unknownTokens(input, 0))
}

// TestScannerPanicRetiresRuleAndLexingContinues: a rule panics once, on the
// first symbol of the first token. The VM recovers the panic and retires that
// rule for the token; the other rules' matches survive, and later tokens are
// lexed with every rule, the panicking one included.
func TestScannerPanicRetiresRuleAndLexingContinues(t *testing.T) {
	const (
		lexTypePanic  = bytescan.TokenType("PANIC")
		lexTypeNormal = bytescan.TokenType("NORMAL")
	)
	input := "panic normal"

	// Engine probe: panics once, counted in a captured variable, on purpose
	// (breaks practices 2, 3, 4 and 6).
	panicCount := 0
	recoveringPanicRule := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		r := s.Rune()
		if r == 'p' && panicCount == 0 {
			panicCount++
			panic("recoverable panic")
		}
		if r >= 'a' && r <= 'z' {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypePanic, recoveringPanicRule)
	lx.MustAddRule(lexTypeNormal, identifierRule)
	lx.MustAddRule("WS", whitespaceRule)

	// In the second token PANIC runs again and accepts "n", but NORMAL's
	// longer "normal" wins.
	assertRetireStream(t, lx, []*bytescan.Token{
		bytescan.NewTokenFromString(lexTypeNormal, "panic", 0),
		bytescan.NewTokenFromString("WS", " ", 5),
		bytescan.NewTokenFromString(lexTypeNormal, "normal", 6),
	})
	assert.Equal(t, 1, panicCount)
}

// TestPanicRetiresRule: a rule that panics while it processes a symbol is
// retired for the current token, as StateReject does, whether it panics on a
// direct call or as the next rule of a pushback chain. The panic does not
// reach the caller, Next returns no error, and a sibling rule's match is
// emitted. See "Panics" in docs/specs/rule-vm-contract.md.
func TestPanicRetiresRule(t *testing.T) {
	const (
		lexTypeWord = bytescan.TokenType("WORD")
		lexTypeBad  = bytescan.TokenType("BAD")
	)

	// Engine probes: both panic on purpose (breaks practices 1, 2 and 6).
	//
	// direct continues on its first symbol and panics on its second.
	direct := func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return alwaysPanic, bytescan.StateContinue
	}

	// chained continues on its first symbol. On its second it pushes back,
	// and the next rule of the chain panics on the pushed-back symbol.
	chained := func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return alwaysPanic, bytescan.StatePushBack
		}, bytescan.StateContinue
	}

	for _, tc := range []struct {
		name string
		bad  bytescan.Rule
	}{
		{"direct", direct},
		{"in a pushback chain", chained},
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

		t.Run(tc.name+", sibling registered first", func(t *testing.T) {
			lx := bytescan.New(strings.NewReader("ab"))
			lx.MustAddRule(lexTypeWord, newASCIILettersRule())
			lx.MustAddRule(lexTypeBad, tc.bad)

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

// TestPanicKeepsEarlierAccept: a rule accepts "a" and then panics on the next
// symbol, directly or in a pushback chain. It has no sibling. Its earlier
// accept stands, as for StateReject, so the first token is that rule's "a".
// The next token starts fresh: the rule rejects "b", which becomes UNKNOWN,
// and then accepts the last "a" and panics on the EOF symbol.
func TestPanicKeepsEarlierAccept(t *testing.T) {
	const lexTypeA = bytescan.TokenType("A")

	// Engine probes: acceptThenPanic and its next rules panic on purpose
	// (breaks practices 1, 2 and 6).
	panicNext := map[string]bytescan.Rule{
		"direct": alwaysPanic,
		"in a pushback chain": func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return alwaysPanic, bytescan.StatePushBack
		},
	}

	for name, next := range panicNext {
		t.Run(name, func(t *testing.T) {
			acceptThenPanic := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				if s.Rune() == 'a' {
					return next, bytescan.StateAccept
				}
				return nil, bytescan.StateReject
			}

			lx := bytescan.New(strings.NewReader("aba"))
			lx.MustAddRule(lexTypeA, acceptThenPanic)

			assertRetireStream(t, lx, []*bytescan.Token{
				bytescan.NewTokenFromString(lexTypeA, "a", 0),
				bytescan.NewTokenFromString(bytescan.TokenTypeUnknown, "b", 1),
				bytescan.NewTokenFromString(lexTypeA, "a", 2),
			})
		})
	}
}

// TestPanicLeavesScannerUsable is the KD-2 probe (docs/specs/design-principles.md),
// inverted. The input is longer than the cursor's retention budget
// (WithMaxTokenBytes(8) gives 4 × 8 = 32 bytes), and the WORD rule panics once,
// on the third symbol of the first token. The panic leaves no orphaned
// checkpoint and loses no symbol: no later Next fails, and every input byte
// appears in exactly one token, in order.
func TestPanicLeavesScannerUsable(t *testing.T) {
	const (
		lexTypeWord = bytescan.TokenType("WORD")
		lexTypeWS   = bytescan.TokenType("WS")
	)

	input := "abcd " + strings.Repeat("ab ", 40)

	// Engine probe: panics once, recorded in a captured variable, on purpose
	// (breaks practices 1, 2, 4 and 6).
	panicked := false
	var word func(depth int) bytescan.Rule
	word = func(depth int) bytescan.Rule {
		return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if depth == 2 && !panicked {
				panicked = true
				panic("third symbol")
			}
			if isASCIILetter(s.Rune()) {
				return word(depth + 1), bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}
	}

	lx := bytescan.NewWithOptions(strings.NewReader(input), bytescan.WithMaxTokenBytes(8))
	lx.MustAddRule(lexTypeWord, word(0))
	lx.MustAddRule(lexTypeWS, newWhitespaceRule())

	// WORD recorded "ab" before it panicked on "c", so its accept stands. The
	// next token starts at "c".
	expected := []*bytescan.Token{
		bytescan.NewTokenFromString(lexTypeWord, "ab", 0),
		bytescan.NewTokenFromString(lexTypeWord, "cd", 2),
		bytescan.NewTokenFromString(lexTypeWS, " ", 4),
	}
	for i := 0; i < 40; i++ {
		expected = append(expected,
			bytescan.NewTokenFromString(lexTypeWord, "ab", 5+3*i),
			bytescan.NewTokenFromString(lexTypeWS, " ", 7+3*i),
		)
	}

	assertRetireStream(t, lx, expected)
	assert.True(t, panicked, "the WORD rule must have panicked")
}

// TestPanickingRuleConcurrentDeterminism: a rule that panics in every
// identifier token does not break concurrent Next calls. Concurrent callers
// interleave tokens, so, as in TestScannerConcurrentDeterminism, the tokens are
// collected in any order and sorted by offset to restore the stream order
// before the full sequence is compared. Run with -race.
func TestPanickingRuleConcurrentDeterminism(t *testing.T) {
	const (
		lexTypeBoom = bytescan.TokenType("BOOM")
		lexTypeID   = bytescan.TokenType("ID")
		lexTypeWS   = bytescan.TokenType("WS")
		numTokens   = 100
	)

	var b strings.Builder
	var expected []*bytescan.Token
	for i := 0; i < numTokens; i++ {
		id := fmt.Sprintf("id%d", i)
		expected = append(expected,
			bytescan.NewTokenFromString(lexTypeID, id, b.Len()),
			bytescan.NewTokenFromString(lexTypeWS, " ", b.Len()+len(id)),
		)
		b.WriteString(id + " ")
	}
	input := b.String()

	for run := 0; run < 3; run++ {
		// BOOM accepts letters and panics on the first digit, so it panics
		// once in every identifier token. Its accept of "id" stands, but ID's
		// longer match wins.
		// Engine probe: panics and counts its panics on purpose (breaks
		// practices 2, 4 and 6).
		var panics atomic.Int64
		var boom bytescan.Rule
		boom = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if isASCIIDigit(s.Rune()) {
				panics.Add(1)
				panic("digit")
			}
			if isASCIILetter(s.Rune()) {
				return boom, bytescan.StateAccept
			}
			return nil, bytescan.StateReject
		}

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeBoom, boom)
		lx.MustAddRule(lexTypeID, identifierRule)
		lx.MustAddRule(lexTypeWS, whitespaceRule)

		var (
			mu     sync.Mutex
			tokens []*bytescan.Token
			errs   []error
			wg     sync.WaitGroup
		)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					lex, err := lx.Next()
					if err == io.EOF {
						return
					}
					mu.Lock()
					if err != nil {
						errs = append(errs, err)
						mu.Unlock()
						return
					}
					tokens = append(tokens, lex)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		require.Empty(t, errs, "run %d", run)
		sort.Slice(tokens, func(i, j int) bool {
			return tokens[i].Offset() < tokens[j].Offset()
		})
		assert.Equal(t, tokenKeys(expected), tokenKeys(tokens), "run %d", run)
		assert.Equal(t, int64(numTokens), panics.Load(), "run %d", run)
	}
}
