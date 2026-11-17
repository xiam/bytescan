package bytescan

// A Rule is a state transition function for a finite automaton.
// It accepts a Symbol and returns the next Rule (the next state) and a State
// value indicating the result of the transition (e.g., continue, accept, reject).
//
// The scanner calls every registered rule on each symbol of a token. At the
// start of each token, every rule starts again from the Rule value it was
// registered with, so a rule keeps its progress only in the Rule values it
// returns. A rule that keeps state in variables it captures, or that changes
// anything outside itself, is not reset and is not undone when a token fails.
// A rule written this way, as a pure function of the symbol and the Rule it
// is, can be registered on several scanners and composed into other rules.
//
// A nil next retires the rule for the current token. At the end of input the
// rule receives a synthetic symbol for which Symbol.IsEOF reports true; it is
// not part of the input. See State for what each result does.
//
// A rule that panics does not stop the scanner: the panic is recovered, the rule
// is retired for the current token as StateReject does, and Next returns no
// error for it. A match the rule recorded earlier in the token stays.
type Rule func(s Symbol) (next Rule, state State)
