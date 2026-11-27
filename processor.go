package bytescan

import (
	"fmt"
)

type ruleState struct {
	initialRule Rule
	rule        Rule
	lastAccept  int
	isActive    bool
}

// rulesProcessor processes input symbols through multiple rules and returns the
// best match based on the "maximal munch" principle (longest accepted token).
type rulesProcessor struct {
	rules []TokenType

	// states holds the state of each rule in the order of rules: states[i]
	// is the state of the rule rules[i]. The loops index it by rule number,
	// so the order of registration is the order of evaluation and the
	// tie-break of equal matches (pickBestMatch).
	states []ruleState

	// soleSlot is the sole active state index plus one, or zero when the
	// active set is not known to have exactly one member. Other states may
	// still hold earlier accepts, so pickBestMatch uses all slots.
	soleSlot int

	// ctr points at the engine counters of the scanner in the build with the
	// tag bench, and is empty in the normal build (counters_on.go,
	// counters_off.go). It is not the last field, so its size of zero in the
	// normal build adds no padding.
	ctr ruleCounters

	// buf is the token's stack: one packed entry for each symbol passed to
	// Process since the last Reset (see push). An entry is 4 bytes, half
	// the size of a Symbol, so the stack of a long token holds and copies half
	// as much as a stack of Symbols.
	buf []uint32

	// wide holds the symbols that do not fit in a packed entry, in the order
	// they were pushed; their entries in buf hold wideEntry and their index
	// here. The scanner's own symbols always fit, so wide stays empty unless a
	// Cursor of the caller reports a rune or a size out of the packed range.
	wide []Symbol
}

// The layout of a packed stack entry. A symbol fits when its rune is -1
// (RuneEOF) to 1<<21 - 2, which covers every Unicode code point, and its size
// is at most 15 bytes. The flags always fit: flagMask is below 1<<5.
const (
	runeBits  = 21 // the rune plus one, so RuneEOF is zero
	flagShift = runeBits
	flagBits  = 5
	sizeShift = flagShift + flagBits
	sizeBits  = 4

	runeField = 1<<runeBits - 1
	flagField = 1<<flagBits - 1
	sizeField = 1<<sizeBits - 1

	// wideEntry marks an entry whose symbol is in rulesProcessor.wide: the
	// other bits are its index there.
	wideEntry = 1 << 31
)

// The packed flags field must hold every defined flag. The conversion fails to
// compile if flagMask does not fit in flagBits bits.
const _ = uint8(flagField - flagMask)

// The packed fields must not reach the wide-entry bit.
const _ = uint32(wideEntry - 1<<(sizeShift+sizeBits))

func newRulesProcessor(rules []TokenType, initialStates map[TokenType]Rule) *rulesProcessor {
	if len(rules) != len(initialStates) {
		panic("rules and initialStates length mismatch")
	}

	states := make([]ruleState, 0, len(rules))
	for _, typ := range rules {
		if initialStates[typ] == nil {
			panic(fmt.Sprintf("scanner for rule %q is nil", typ))
		}
		states = append(states, ruleState{
			initialRule: initialStates[typ],
			rule:        initialStates[typ],
			lastAccept:  -1,
			isActive:    true,
		})
	}

	return &rulesProcessor{
		rules:  rules,
		states: states,
		buf:    make([]uint32, 0, 4096),
	}
}

func (rp *rulesProcessor) Reset() {
	rp.soleSlot = 0
	rp.buf = rp.buf[:0]
	rp.wide = rp.wide[:0]
	for i := range rp.states {
		state := &rp.states[i]
		state.rule = state.initialRule
		state.lastAccept = -1
		state.isActive = true
	}
}

func (rp *rulesProcessor) Process(s Symbol) (TokenType, int) {
	// Process the symbol through all active rules
	activeRules := rp.processSymbol(s)

	// If any rules are still active, we need more input
	if activeRules > 0 {
		return TokenTypeUnknown, -1
	}

	// All rules are done; pick the best match
	bestMatch, bestMatchLen := rp.pickBestMatch()

	// If no match was found, return UNKNOWN with length 1 to consume at least
	if bestMatch == TokenTypeUnknown {
		return TokenTypeUnknown, 1
	}

	// The stack keeps its symbols: the caller reads the match from Symbols.
	return bestMatch, bestMatchLen
}

// processSymbol pushes s on the token's stack and steps every active rule over
// it. It returns the number of rules that are still active.
//
// The VM validates each verdict and retires only the rule that returned a bad
// one; the other rules continue on the same stack:
//
//   - A State outside the four constants, returned directly or at the end of a
//     pushback chain, retires the rule as StateReject does.
//   - s is the synthetic EOF symbol when it carries FlagEOF; only nextToken's
//     end-of-input symbol does. An accept that counts it (an accept with no
//     pushback) is longer than the symbols read, so the VM discards it. An
//     accept whose pushback chain excludes it is recorded as usual.
//   - No input follows the EOF symbol, so every rule is retired after it,
//     whatever its verdict. Process then always decides.
//
// An accept that the rule recorded earlier stays in each case.
func (rp *rulesProcessor) processSymbol(s Symbol) int {
	rp.push(s)
	atEOF := s.IsEOF()

	// calls and pushbacks feed the engine counters only: the normal build
	// drops them with the inlined empty call below.
	activeRules, calls, pushbacks := 0, 0, 0
	first, end := 0, len(rp.states)
	if rp.soleSlot != 0 {
		first, end = rp.soleSlot-1, rp.soleSlot
	}
	lastActive := 0
	for i := first; i < end; i++ {
		var pushbackCount int

		state := &rp.states[i]
		if !state.isActive {
			continue
		}

		nextRule, nextState := callRule(state.rule, s)
		calls++
		for nextState == StatePushBack {
			pushbackCount++
			if nextRule != nil && pushbackCount <= len(rp.buf) {
				pushedBackSymbol := rp.at(len(rp.buf) - pushbackCount)
				nextRule, nextState = callRule(nextRule, pushedBackSymbol)
				pushbacks++
			} else {
				nextRule, nextState = nil, StateReject
			}
		}

		keepActive := false
		switch nextState {
		case StateAccept:
			// At EOF, an accept with no pushback counts the EOF symbol. It is
			// discarded, and the rule is retired below.
			if !atEOF || pushbackCount > 0 {
				matchLen := len(rp.buf) - pushbackCount
				if matchLen > state.lastAccept {
					state.lastAccept = matchLen
				}
			}
			keepActive = nextRule != nil
		case StateContinue:
			keepActive = nextRule != nil
		default:
			// StateReject, and any State outside the four constants, retire
			// the rule.
		}

		if keepActive && !atEOF {
			state.rule = nextRule
			activeRules++
			lastActive = i + 1
		} else {
			state.isActive = false
		}
	}

	rp.soleSlot = 0
	if activeRules == 1 {
		rp.soleSlot = lastActive
	}
	rp.ctr.step(calls+pushbacks, pushbacks, activeRules)

	return activeRules
}

func (rp *rulesProcessor) pickBestMatch() (TokenType, int) {
	bestMatch := TokenTypeUnknown
	bestMatchLen := -1

	// The strict comparison keeps the first rule of equal matches: the rule
	// registered first.
	for i := range rp.states {
		state := &rp.states[i]
		if state.lastAccept > bestMatchLen {
			bestMatch = rp.rules[i]
			bestMatchLen = state.lastAccept
		}
	}

	return bestMatch, bestMatchLen
}

// callRule calls r with s and returns its verdict. If r does not return
// normally, callRule recovers the panic and returns (nil, StateReject), so the
// VM retires that rule for the current token as for a reject (principle 3;
// "Panics" in docs/specs/rule-vm-contract.md). The VM neither keeps nor
// reports the panic value.
//
// The completion flag, not the result of recover, detects the panic: with
// GODEBUG panicnil=1 (a main module that declares a go version before 1.21),
// recover returns nil for panic(nil), and the panic must still retire the rule.
// The deferred recover covers only the call to r, never the VM's own code.
func callRule(r Rule, s Symbol) (next Rule, state State) {
	returned := false
	defer func() {
		if !returned {
			_ = recover()
			next, state = nil, StateReject
		}
	}()

	next, state = r(s)
	returned = true
	return next, state
}

// push pushes s on the stack: packed when it fits, and in wide otherwise.
// Either way, at returns s unchanged.
func (rp *rulesProcessor) push(s Symbol) {
	// s.r+1 wraps for the largest rune, and a negative sum converts to a
	// large uint32: both fail the range check.
	v := uint32(s.r + 1)
	if v <= runeField && s.flags <= flagField && s.size <= sizeField {
		rp.buf = append(rp.buf, v|uint32(s.flags)<<flagShift|uint32(s.size)<<sizeShift)
		return
	}
	rp.buf = append(rp.buf, wideEntry|uint32(len(rp.wide)))
	rp.wide = append(rp.wide, s)
}

// at returns the symbol at index i of the stack, 0 being the first symbol of
// the token.
func (rp *rulesProcessor) at(i int) Symbol {
	v := rp.buf[i]
	if v&wideEntry != 0 {
		return rp.wide[v&^wideEntry]
	}
	return Symbol{
		r:     rune(v&runeField) - 1,
		flags: uint8(v >> flagShift & flagField),
		size:  uint8(v >> sizeShift & sizeField),
	}
}

// Len returns the number of symbols on the stack: every symbol passed to
// Process since the last Reset, the synthetic EOF symbol included once it is
// passed.
func (rp *rulesProcessor) Len() int {
	return len(rp.buf)
}

// CopyText copies the runes of the first len(dst) symbols of the stack into
// dst, in order, and returns the sum of their sizes (Symbol.Size). The stack is
// the only copy of the token's symbols, so the caller copies the text before
// the next Reset. len(dst) must not exceed Len.
func (rp *rulesProcessor) CopyText(dst []rune) int {
	entries := rp.buf[:len(dst)]
	size := 0
	if len(rp.wide) == 0 {
		for i, v := range entries {
			dst[i] = rune(v&runeField) - 1
			size += int(v >> sizeShift & sizeField)
		}
		return size
	}
	for i := range entries {
		s := rp.at(i)
		dst[i] = s.r
		size += int(s.size)
	}
	return size
}
