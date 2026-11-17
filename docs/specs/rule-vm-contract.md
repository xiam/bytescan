# Rule and VM contract

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This is how the engine decides, from code and tests: how the VM runs the
registered rules over one token, what it does with each verdict a rule
returns, and what a rule must do to get its intended match. Each item is
load-bearing. The [design principles](design-principles.md) bind every item
here.

Code is cited by symbol. `processSymbol` is `rulesProcessor.processSymbol` in
`processor.go`, `callRule` is in `processor.go`, and `nextToken` is
`Scanner.nextToken` in `bytescan.go`.

## Contents

- [Competing rules, evaluated sequentially](#competing-rules-evaluated-sequentially)
- [Longest match](#longest-match)
- [Tie priority](#tie-priority)
- [Rule states and pushback](#rule-states-and-pushback)
  - [Lookahead combined with a longer match](#lookahead-combined-with-a-longer-match)
- [Progress and zero-length behavior](#progress-and-zero-length-behavior)
- [EOF handling](#eof-handling)
- [Registration validation](#registration-validation)
- [Returned-error rollback](#returned-error-rollback)
- [Panics](#panics)
- [Custom-rule responsibilities](#custom-rule-responsibilities)

## Competing rules, evaluated sequentially

**Current contract.** A rule is a state machine; the engine runs **all** active
rules over the same input, one symbol at a time. "Parallel" means *competing
state machines*, not concurrency: within a single `Next()`, the rules are stepped
**sequentially in registration order** for each symbol. The engine tracks each
rule's furthest acceptance separately, as per-rule bookkeeping
([principle 1](design-principles.md#principle-1-stateless-stack-model)). The
engine reads ahead until every rule has either rejected or terminated, then
selects the winner.

**Evidence.** `processSymbol` iterates `rp.rules` in order;
`TestScannerConcurrentAccess` and `TestScannerConcurrentDeterminism` exercise many
goroutines calling `Next` on one scanner and assert the token stream is
deterministic and complete.

## Longest match

**Current contract.** The engine applies **maximal munch**: the rule with the
**longest** accepted match wins, regardless of registration order. A rule that
accepted 4 symbols beats a rule that accepted 3, even if the 3-rule was
registered first.

**Evidence.** `TestScannerProcessor` "Longest Match (Float vs Integer)" (input
`12.345`, the FLOAT rule's longer acceptance beats INT's `12`);
`TestScannerProcessor` "Float with trailing decimal at EOF" and "Fallback from
potential Float to Integer" (the float's accepted prefix wins where it is
longer).

## Tie priority

**Current contract.** When two rules accept the **same** longest length, the one
**registered earlier** wins. The engine walks rules in registration order with a
strict `>` comparison on accepted length (`rulesProcessor.pickBestMatch`), so a
later rule cannot displace an earlier rule at equal length.

**Evidence.** `TestScannerProcessor` "Rule Priority (Keyword vs Identifier)"
(`if` registered before `identifier` → `if` wins the tie at length 2) and
"Rule Priority Reversed (Identifier wins)" (registering `identifier` first flips
the outcome, proving registration order, not type, decides ties). See also
scenario 1 under [Illustrative scenarios](consumer-contract.md#illustrative-scenarios).

## Rule states and pushback

**Current contract.** A rule reports one of four states per symbol:

- `StateContinue` — possible match, needs more input; a rule that wants more
  input returns a non-nil `next`. **Reconciled with principle 3.** This is not a
  duty the VM depends on: the VM retires a rule that returns
  `(nil, StateContinue)` (the `StateContinue` case of `processSymbol`;
  evidence: `TestScannerProcessor` "Rule returns nil, StateContinue").
- `StateAccept` — a valid match so far; the rule may return a non-nil `next` to
  keep scanning for a longer match, or nil to stop. A nil `next` with
  `StateAccept` records the acceptance and stops the rule. An accept of the
  synthetic EOF symbol itself is discarded; see [EOF handling](#eof-handling).
- `StateReject` — this rule can no longer match; it is deactivated for the
  current token.
- `StatePushBack` — exclude the top symbol of the token's stack from this
  match and show it to the returned `next` rule, in the same step. This is the
  mechanism for lookahead and backtracking. The mechanics follow.

Any other `State` value, returned directly or at the end of a pushback chain,
retires the rule as `StateReject` does (the `default` case of
`processSymbol`). The VM does not panic. An accept that the rule recorded
earlier stays, and the other rules continue on the same stack. This is the
boundary check of
[principle 2](design-principles.md#principle-2-the-vm-checks-boundaries).
Evidence: `TestInvalidStateRetiresRule` (with a sibling, `ab` yields
`WORD "ab"`; as the only rule, one `UNKNOWN` per rune; both for a direct return
and for the end of a pushback chain); `TestProcessDecidesOnEOFSymbol`.

**Current contract — pushback mechanics** (the pushback loop and the verdict
switch of `processSymbol`).

- **The stack.** The VM keeps the symbols read for the current token as a
  stack, `rulesProcessor.buf`. At end of input the synthetic EOF symbol is
  pushed on that stack too.
- **One pushback.** `(next, StatePushBack)` removes the top symbol of the stack
  from this rule's match and calls `next` with the removed symbol, in the same
  step. The first pushback removes the symbol that was just read. At end of
  input, the first pushback removes the EOF symbol. Evidence:
  `TestLookaheadKeywordBoundary` (one pushback excludes the symbol after `for`,
  or the EOF symbol); `TestScannerProcessor` "Context-Aware Rules (BOL)" (the
  comment rule pushes back its terminator so it is not included in the
  comment).
- **Chained pushback.** `next` then decides:
  - `(nil, StateAccept)`: the match is every symbol below the removed ones,
    subject to the only-grows rule below.
  - `StatePushBack` again: remove one more symbol, toward the token start, and
    call the new `next` with that symbol.
  - `StateReject`: the rule is retired. An accept that the rule recorded
    earlier stays.

  Evidence: `TestPushBackSymbolOrder` (a chain on `abcd` is shown `d`, `c`,
  `b`, in that order); `TestLookaheadTwoCandidateEnds` (after `abcd` and any
  number of `.`, one rule yields `ab` on `X` and `abcd` on `Y`, so it chooses
  between two ends after it has seen what follows); `TestScannerPathologicalRules`
  "Rule Rejects After Full Buffer Consumption (Veto Rule)" (a chain that ends
  in the rule's own `StateReject` keeps its earlier accept).
- **Only-grows rule.** Within a token, a rule's recorded match only grows. An
  accept at the end of a pushback chain that is shorter than an accept the
  same rule already recorded is ignored. Pushback therefore cannot withdraw or
  shorten a match that the rule already accepted. A rule that decides by
  lookahead must return `StateContinue`, not `StateAccept`, until it has
  decided. Evidence: `TestScannerPathologicalRules` "Bait and Switch Rule" (the
  chain's accept of length 1 is ignored, and the rule's earlier accept of
  `token` stays); `TestScannerPathologicalRuleWithDeepBacktracking` (the same for
  chains of up to 101 pushbacks); `TestLookaheadFloatVersusRange`
  (`newUnsignedFloatRule` continues until it reads a symbol that ends the float,
  so `1.` is not a float in `1..5`).
- **Count rule.** A rule that excludes `n` trailing symbols returns `n`
  pushbacks and then an accept: the match length is the stack length minus the
  chain's pushback count. A helper that itself issues `k` pushbacks, entered
  through one more `StatePushBack`, excludes `k + 1` symbols. Evidence:
  `TestLookaheadMultiSymbol` (four pushbacks exclude `://` and a letter, which
  leaves `http`); `TestLookaheadVariableLength` (the rule scans a whole page
  name and carries its count in the `Rule` values it returns, so the count
  depends on the input); `TestPushBackSymbolOrder` (a helper that issues two
  pushbacks, entered through one more, excludes three symbols and leaves `a`).
- **Bounds rule.** More pushbacks than symbols on the stack, the EOF symbol
  included, or a nil `next`, retire the rule as a reject. This is the boundary
  check of
  [principle 2](design-principles.md#principle-2-the-vm-checks-boundaries). An
  accept that the rule recorded earlier stays. On input `a`, the stack at end
  of input holds `a` and the EOF symbol: one pushback matches `a`, two are a
  zero-length match (see
  [Progress and zero-length behavior](#progress-and-zero-length-behavior)), and
  three retire the rule. Evidence: `TestProcessPushBackAtEOF` (`Process`
  returns the rule's type with length 1, then the rule's type with length 0,
  then `UNKNOWN` with length 1); `TestPushBackAtEOF` (the same three cases
  through `Next`); `TestPushBackRetiresRuleKeepsEarlierAccept` (both reject
  causes keep the earlier accept of `a`); `TestScannerMaximumLimits` "Maximum
  pushback depth" (a chain of 1001 pushbacks runs to its accept, and one more
  pushback than the stack holds retires the rule); `TestScannerProcessor`
  "Pushback Infinite Loop" (a rule that always pushes back with a non-nil
  `next` yields one `UNKNOWN` token per rune) and "Excessive Pushback (Index
  Out of Bounds)"; `TestScannerPathologicalRules` "Rule Returns nil with
  StatePushBack" (the bad rule is retired, and a sibling rule still wins
  `"a"`).
- **Per rule.** Each rule's pushback is its own. A pushback only reads the
  stack, so other rules are not affected; see
  [principle 1](design-principles.md#principle-1-stateless-stack-model).
  Evidence: `TestPushBackIsPerRule`.
- **Per step.** The exclusion count lasts for the current symbol step only.
  Within a step the VM counts the pushbacks of the chain (`pushbackCount`, a
  local of the per-rule loop of `processSymbol`); it keeps no count across
  steps.

<a name="chain-endings"></a>**Chain endings.** A pushback chain must end in `(nil, StateAccept)` or
`StateReject`. Because the exclusion count is dropped at the end of the step, a
chain that keeps the rule active gives a result that later symbols overwrite.
These two endings are unsupported (history:
[KD-5](design-principles.md#kd-5)):

- A chain that ends in `StateContinue`: the pushback is forgotten, and the
  symbols pushed back are shown to the rule again. Evidence:
  `TestPushBackChainEndingContinue` (a rule that reads `ab`, pushes back `c`,
  and continues lexes `abcde` as one token).
- A chain that ends in `(non-nil next, StateAccept)`: the shorter length is
  recorded, the rule stays active, and a later accept counts the excluded
  symbols back in. Evidence: `TestPushBackChainEndingAcceptWithNext` (`aXbc`
  lexes as one token; the same chain with a nil `next` matches `a`).

Both pins push back on a real symbol. On the EOF symbol no later symbol can
overwrite the result, because the VM retires every rule after the EOF symbol: a
chain that excludes the EOF symbol and ends in `(non-nil next, StateAccept)`
records its accept as `(nil, StateAccept)` would, and a chain that ends in
`StateContinue` records nothing. Evidence: `TestAcceptOfEOFSymbolIsDiscarded`
"pushback excludes the EOF symbol, non-nil next". See
[EOF handling](#eof-handling).

### Lookahead combined with a longer match

**Current contract.** An alternation where one branch ends in lookahead and
another branch matches longer text, such as `abcd(?=ef)|abcdefgh`, is written
as one rule with **delayed acceptance and terminal fallback**:

- The rule does not accept the shorter, lookahead-validated match while a
  longer branch is still alive. It returns `StateContinue` and keeps scanning.
  It keeps the validated end in the `Rule` values it returns (see
  [principle 1](design-principles.md#principle-1-stateless-stack-model)).
- When a longer branch completes, the rule accepts everything it has read, as
  any plain accept does. If another branch can still grow, it returns a
  non-nil `next` with that accept.
- When every longer branch is dead, the rule ends with one terminal pushback
  chain to the last validated shorter match, which ends in
  `(nil, StateAccept)`. The chain excludes every symbol read after that match,
  including the symbol that ended the longer branch. When the input ends the
  longer branch, the chain counts the EOF symbol too: after `abcdef`, it
  excludes `e`, `f`, and the EOF symbol. If no shorter match was validated, the
  rule rejects.
- An accept that the rule already recorded for a longer match stays. The
  only-grows rule ignores the shorter fallback.

The alternative is to accept the shorter match early and then continue
scanning for the longer one. That needs a chain that ends in
`(non-nil next, StateAccept)`, and that ending is unsupported
([chain endings](#chain-endings)). Delayed acceptance needs no such ending, and
no engine or registration change. Evidence: `TestLookaheadDelayedAcceptance`
(`abcdefgh` yields `MATCH "abcdefgh"`; `abcdefxx`, `abcdefgX`, `abcdef` and
`abcdefg` yield `MATCH "abcd"` and then one `UNKNOWN` per rune; `abcde` and
`abcdX` yield one `UNKNOWN` per rune; `abcdefghabcdef` yields
`MATCH "abcdefgh"`, `MATCH "abcd"` at offset 8, and `UNKNOWN` `e` and `f`).
The combinators of `rules/core` build this construction; see
[Composition and ambiguity](composition.md#composition-and-ambiguity). Its
read-ahead cost is under
[A longer branch without a bound](resources-and-streams.md#a-longer-branch-without-a-bound).

## Progress and zero-length behavior

**Current contract.** The engine guarantees forward progress: a token is never
committed with zero length. If a rule's net effect would be a zero-length match
(a pushback that consumes nothing, or an accept of an empty sequence), the engine
does **not** emit an empty token and does **not** loop; it falls back to a
single-rune `UNKNOWN` token and advances by one rune. This makes a
pathologically non-advancing rule terminate rather than spin. This is the
zero-length check of
[principle 2](design-principles.md#principle-2-the-vm-checks-boundaries).

**Evidence.** The zero-length branch of `nextToken` (`textLen == 0`).
`TestProcessZeroLengthMatch` proves the path: one pushback to an acceptor on
the first symbol makes `Process` return the rule's type with length 0, while
two pushbacks on the first symbol pass the bottom of the stack and return
`UNKNOWN` with length 1. Through `Next` both give the same single-rune
`UNKNOWN` token. `TestScannerZeroLengthMatchPrevention` and the
`TestScannerProcessor` case "Zero-Length Match Causes Infinite Loop" use the
one-pushback rule and assert one `UNKNOWN` rune per input rune, never an
infinite loop; the `TestScannerInfiniteLoopProtection` case "Zero Length Accept
Loop" uses it and asserts that lexing terminates.

## EOF handling

**Current contract.** The engine feeds a **synthetic EOF symbol** (carrying
`RuneEOF` and `FlagEOF`, not part of the input) to the rules once the underlying
reader is exhausted. This lets a rule observe end-of-input. No input follows
the EOF symbol, so after it the VM retires every rule that is still active —
through `StateContinue`, or through a non-nil `next` with `StateAccept`.
`Process` therefore always decides on the EOF symbol. Each rule contributes the
longest accept it recorded, and the winner is chosen as usual. If no rule
recorded an accept, the token is unmatched input: one single-rune `UNKNOWN`
token. If no real symbol was consumed and the stream is done, `Next` returns
`io.EOF`. A rule that never settles therefore yields one `UNKNOWN` token per
rune, and each of those tokens reads to the end of input first; see the cost
under [Unmatched input, EOF, and errors](consumer-contract.md#unmatched-input-eof-and-errors).

**Reactions to the EOF symbol.** A rule that wants its match to survive end of
input rejects the EOF symbol (or pushes it back) and does not accept it. The EOF
symbol is on the token's stack, so a pushback at end of input removes it first,
and it counts toward the pushback bound; see
[Rule states and pushback](#rule-states-and-pushback) (evidence:
`TestPushBackAtEOF`, `TestProcessPushBackAtEOF`, and `TestScannerProcessor`
"Pushback at EOF"). Before the EOF symbol the stack holds only real symbols,
so a rule that returns `StateAccept` for the EOF symbol with no pushback
produces the only match whose length counts more than the symbols read: it
counts the synthetic symbol. The VM discards that verdict and retires the rule
(the `StateAccept` case of `processSymbol`, `atEOF` with no pushback); an
accept the rule recorded earlier stays. An accept at the end of a pushback
chain that excludes the EOF symbol is valid and recorded.
**Reconciled with principle 3.** These are not duties the VM depends on: a rule
is free, and the VM checks the result (principle 2). Each reaction retires only
the offending rule, so another rule's valid match is still emitted (history:
[KD-3 and KD-4](design-principles.md#kd-3)).

**Evidence.** `TestScannerEdgeCasesAtEOF` (a settled rule at EOF → `io.EOF`; a rule
in a different state at EOF → the longer real acceptance wins; empty input →
`io.EOF` immediately); `TestScannerProcessor` "Tokens immediately at EOF";
`TestStillActiveAtEOFRetiresRule` (a sibling that always continues, or always
returns itself with `StateAccept`, on `ab`: `WORD "ab"`); `TestScannerProcessor`
"Rule Never Settles (UNKNOWN per rune)" (the only rule never settles: one
`UNKNOWN` per rune, then `io.EOF`); `TestAcceptOfEOFSymbolIsDiscarded`
("sibling": a letters `WORD` rule and a sibling that accepts the EOF symbol,
on `ab`, yield `WORD "ab"` on the first call; "earlier accept by the same
rule": the rule's accept of `ab` wins; the two "pushback excludes the EOF
symbol" cases guard the valid path); `TestProcessDecidesOnEOFSymbol` (`Process`
decides on the EOF symbol in each case). Two branches of `nextToken` stay as
defensive checks that fail the token with `ErrInternal`: an undecided processor
after the EOF symbol (`textLen < 0 && isEOF`) and a match longer than the
symbols read (`textLen > read`). The reactions above make them unreachable.

## Registration validation

**Current contract.** `AddRule` validates before registering:

- A `TokenType` that is already registered is rejected (one rule per type).
- An empty `TokenType` (`""`) is rejected.
- A `nil` rule is rejected.

Each rejection matches its sentinel with `errors.Is`: `ErrDuplicateRule`,
`ErrEmptyRuleType` and `ErrNilRule`. A rejected call registers nothing.
`MustAddRule` panics on any of these. Calling `Next` with **no rules registered**
returns an error matching `ErrNoRules` rather than panicking. See
[Unmatched input, EOF, and errors](consumer-contract.md#unmatched-input-eof-and-errors).

**Evidence.** `TestScannerErrorConditions` "Duplicate Rule Types", "Nil Rule",
"Empty TokenType"; `TestScannerErrorIdentities`. The no-rules path is
`Scanner.getProcessor`.

## Returned-error rollback

**Current contract.** When `Next` returns a **non-EOF error**, the engine has
made **no partial progress**. It rewinds the cursor's logical position back to
the token's start (releasing the token's checkpoint), so the next `Next` sees
exactly the same input. A failed call consumes nothing, and a retry is
identical: it does not resume in the middle of the rejected token.

This rollback is a **logical** rewind of the cursor over already-read, retained
input. It is distinct from two related but separate things:

- It is **not** a re-read of the underlying source. The bytes were already read
  from the reader into the cursor's retained region; rollback repositions the
  logical cursor inside that region. It does not call the underlying
  `io.Reader` again for those bytes.
- It does **not** undo arbitrary side effects a rule may have performed. A rule
  is an ordinary Go function; if it wrote to a file, mutated shared state, or
  printed, the rollback does not and cannot reverse that. The guarantee is about
  the *scanner's* view of the input, not about the rule's effects on the world.

A rule's panic is not an error and does not take this path; see
[Panics](#panics). An invalid `State` does not panic either: the VM retires the
rule ([Rule states and pushback](#rule-states-and-pushback)).

**Evidence.** `TestScannerRetentionFailureConsumesNothing` (a token rejected by a
retention bound leaves the cursor at byte offset 0 and a retry is identical, for
both the cursor-level and token-level bounds); `TestScannerContextSurvivesFailedNext`
(a failed `Next` commits nothing and does not release the previous context).

## Panics

**Current contract.** A panic raised by a rule does not reach the caller. The
VM calls each rule through `callRule`, which wraps only that one call in a
deferred `recover` and returns `(nil, StateReject)` for a call that panicked:

- A rule that panics while it processes a symbol, on a direct call or as the
  next rule of a pushback chain, is retired for the current token as
  `StateReject` does. An accept that the rule recorded earlier in the token
  stays.
- The other rules in the same step and in later steps are evaluated normally,
  and the winner is chosen from the recorded accepts as usual. Lexing
  continues with the next token, in which every rule, the one that panicked
  included, starts again from the `Rule` it was registered with.
- A panic with any value is recovered, a `nil` value included. On Go 1.21 and
  later, `panic(nil)` raises a `*runtime.PanicNilError`; with
  `GODEBUG=panicnil=1`, `recover` returns `nil` for it. `callRule` detects the
  panic with a completion flag, not with the result of `recover`, so both cases
  retire the rule.
- `Next` returns no error and does not panic because of a rule's panic. The
  returned token is the one chosen from the recorded accepts, so retiring a
  rule can change which token wins, for example to `UNKNOWN` when no rule
  remains.
- The VM keeps no state for a panic and does not report the panic value
  (principles 1 and 4). Retiring the rule uses the existing `isActive`
  bookkeeping.

The recovery covers only the rule call. A panic raised by the engine itself,
outside a rule, is out of its scope and is not recovered. Side effects that a
rule performed before it panicked are not undone (see
[Custom-rule responsibilities](#custom-rule-responsibilities)).

**Reconciled with principle 3.** The owner chose this behavior on 2026-11-12
([decision log](design-principles.md#owner-decisions)). It replaces the earlier
contract, under which the panic propagated out of `Next` and a consumer had to
`recover` around its own call; that consumer no longer receives the panic as a
signal (history: [KD-2](design-principles.md#kd-2)).

**Evidence.** `TestScannerPanickingRuleIsRecoveredAndRetired` (the only rule
panics on every symbol: one `UNKNOWN` per rune, then `io.EOF`);
`TestScannerPanicRetiresRuleAndLexingContinues` (a rule panics once on the first
token; a sibling's match wins, and later tokens lex normally);
`TestPanicRetiresRule` (with a sibling, `ab` yields `WORD "ab"`; as the only
rule, one `UNKNOWN` per rune; both for a direct panic and for a panic in a
pushback chain); `TestPanicKeepsEarlierAccept` (`aba` with a rule that accepts
`a` and then panics: `A "a"`, `UNKNOWN "b"`, `A "a"`);
`TestScannerErrorConditions` "Rule Panic Types Are Recovered" (string, error,
integer, and nil values); `TestCallRule` (the same values, also under
`GODEBUG=panicnil=1`); `TestPanicLeavesScannerUsable` (input `abcd ` followed by
40 copies of `ab `, `WithMaxTokenBytes(8)`, and a word rule that panics once on
its third symbol: every later `Next()` succeeds, and every input byte appears
in exactly one token, starting with `WORD "ab"` and `WORD "cd"`);
`TestPanickingRuleConcurrentDeterminism` (concurrent `Next`, run with `-race`).

## Custom-rule responsibilities

**Current contract.** The engine is a general automaton runner.
**Reconciled with principles 2 and 3.** The VM validates every verdict a rule
returns (principle 2), and a rule is free to return anything (principle 3). So
the items below are not duties the VM depends on. They describe how to write a
rule that gets its intended match, and what the VM does today when a rule does
otherwise; each reaction retires only that rule, so other rules are not
affected. Each item links to where the reaction is specified.
How to write and review a rule in this repository is in the
[rule-authoring standard](../rule-authoring.md).

- **Handle EOF.** To keep its match at end of input, a rule rejects (or pushes
  back) the EOF symbol and does not accept it. An accept that counts the EOF
  symbol is discarded and the rule is retired; only its earlier accepts count.
  See [EOF handling](#eof-handling).
- **Settle.** To produce a match, a rule records its accept before the end of
  input, or at the end of a pushback chain that excludes the EOF symbol. A rule
  that never settles contributes only the accepts it recorded earlier, and with
  none it produces `UNKNOWN` tokens, each of which reads to the end of input
  first. See [EOF handling](#eof-handling).
- **Decide before accepting.** A rule that decides by lookahead returns
  `StateContinue` until it has decided, and ends its pushback chain in
  `(nil, StateAccept)` or `StateReject`. An earlier accept cannot be withdrawn
  or shortened by a later pushback, and the other chain endings are
  unsupported; see [Rule states and pushback](#rule-states-and-pushback).
- **Advance.** To produce its own match, a rule's net effect consumes input. A
  rule that never advances is contained by the zero-length/`UNKNOWN` fallback
  and the bounded pushback chain, but it produces `UNKNOWN` tokens rather than
  the rule's intended match. See
  [Progress and zero-length behavior](#progress-and-zero-length-behavior).
- **Return a valid `State`.** The four defined states are the ones the VM acts
  on. Any other value retires the rule as `StateReject` does; see
  [Rule states and pushback](#rule-states-and-pushback).
- **Return rather than panic.** A rule that panics is retired for the current
  token as `StateReject` does, and the VM does not report the panic value; see
  [Panics](#panics).
- **Side effects are the rule's.** The engine offers no transactionality around a
  rule; any side effect a rule performs is permanent and not rolled back, also
  when the rule panics.
