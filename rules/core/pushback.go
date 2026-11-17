package core

import (
	"fmt"

	"github.com/xiam/bytescan"
)

// PushBack returns the verdict of a rule that excludes the last n symbols
// (n >= 1) from its match and then ends the pushback chain with (nil, state).
// state is bytescan.StateAccept, to accept the match that remains, or
// bytescan.StateReject, to keep the accept the rule recorded earlier, if any.
//
// It issues n bytescan.StatePushBack verdicts in total: the returned one,
// and n-1 more from the Rule it returns. The first pushback excludes the
// symbol that was just read; at end of input, that is the EOF symbol, and it
// counts towards n. A rule returns the result directly from a scanning state,
// with a comment that names the n excluded symbols:
//
//	if s.IsEOF() {
//		return core.PushBack(1, bytescan.StateAccept) // the EOF symbol
//	}
//
// PushBack panics if n < 1 or if state is neither StateAccept nor
// StateReject. See practice 5 of docs/rule-authoring.md and "Rule states and
// pushback" in docs/specs/rule-vm-contract.md.
func PushBack(n int, state bytescan.State) (bytescan.Rule, bytescan.State) {
	if n < 1 {
		panic(fmt.Sprintf("core.PushBack: n is %d, want at least 1", n))
	}
	if state != bytescan.StateAccept && state != bytescan.StateReject {
		panic(fmt.Sprintf("core.PushBack: state is %v, want ACCEPT or REJECT", state))
	}
	return pushBackChain(n-1, state), bytescan.StatePushBack
}

// pushBackChain is the pushback-chain callback that issues n more
// StatePushBack verdicts and then returns (nil, state). It ignores the symbol
// it is shown, which at end of input can be the EOF symbol: that symbol is
// already excluded from the match. A chain with n < prebuiltPushBacks is
// prebuilt, so PushBack with n <= prebuiltPushBacks allocates nothing.
func pushBackChain(n int, state bytescan.State) bytescan.Rule {
	if n < prebuiltPushBacks {
		if state == bytescan.StateAccept {
			return pushBackAccept[n]
		}
		return pushBackReject[n]
	}
	return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return pushBackChain(n-1, state), bytescan.StatePushBack
	}
}

// prebuiltPushBacks is the number of pushback-chain callbacks prebuilt for
// each final state.
const prebuiltPushBacks = 8

// pushBackAccept[n] and pushBackReject[n] are the prebuilt pushback-chain
// callbacks that issue n more StatePushBack verdicts and then end with
// StateAccept or StateReject. They hold no mutable state.
var pushBackAccept, pushBackReject [prebuiltPushBacks]bytescan.Rule

func init() {
	prebuildPushBacks(&pushBackAccept, bytescan.StateAccept)
	prebuildPushBacks(&pushBackReject, bytescan.StateReject)
}

// prebuildPushBacks fills chain with the pushback-chain callbacks that end with
// state.
func prebuildPushBacks(chain *[prebuiltPushBacks]bytescan.Rule, state bytescan.State) {
	chain[0] = func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		return nil, state
	}
	for n := 1; n < prebuiltPushBacks; n++ {
		prev := chain[n-1]
		chain[n] = func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return prev, bytescan.StatePushBack
		}
	}
}
