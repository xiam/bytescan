# VM design principles and decision log

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This document holds the five VM design principles, the evidence that the
engine conforms to each of them, and the decision log: owner decisions, closed
design questions, and the history of the known deviations.

## Contents

- [VM design principles](#vm-design-principles)
  - [Principle 1: stateless stack model](#principle-1-stateless-stack-model)
  - [Principle 2: the VM checks boundaries](#principle-2-the-vm-checks-boundaries)
  - [Principle 3: rules are free](#principle-3-rules-are-free)
  - [Principle 4: the VM only reports matches](#principle-4-the-vm-only-reports-matches)
  - [Principle 5: lookahead belongs to the rule](#principle-5-lookahead-belongs-to-the-rule)
- [Decision log](#decision-log)
  - [Owner decisions](#owner-decisions)
  - [Closed design questions](#closed-design-questions)
  - [Known deviations](#known-deviations)

## VM design principles

**Status: Design principle (non-negotiable).** The owner stated these five
principles on 2026-11-12. The stateless design is deliberate, not accidental.
Every change to the engine must keep all five. A change that would break one is
rejected; changing a principle is an owner decision, not part of an engine
change.

Each principle is followed by its current conformance at `master`, as Current
contract statements. The engine conforms to all five. Where a behaviour is
specified in another document, the conformance entry links to it and does not
repeat it. The [known deviations](#known-deviations) that the engine had
against these principles are history.

Code is cited by symbol. `processSymbol` is `rulesProcessor.processSymbol` in
`processor.go`, and `nextToken` is `Scanner.nextToken` in `bytescan.go`.

### Principle 1: stateless stack model

**Design principle (non-negotiable).** The VM is based on a simple stack of the
symbols read for the token in progress. The VM keeps no state on a rule's
behalf. For one token it holds only:

- the stack: the symbols read for this token;
- the read position;
- the boundaries;
- per-rule bookkeeping: each rule's current `Rule`, its longest accepted
  length, and whether it is still active.

The VM resets all of it before the next token starts, so nothing carries over
from one token to the next. The VM never holds marks, modes, or any other state
on a rule's behalf, and it keeps no count for a rule across symbol steps. A
rule's own state is the `Rule` value it returns.

**Current contract — conformance.** The engine conforms.

- **The per-token holdings.** The stack is `rulesProcessor.buf`;
  `processSymbol` pushes every symbol it is given on it, the synthetic EOF
  symbol included. It is the only copy of the token's symbols: `nextToken`
  keeps none of its own and slices the winning text from the stack
  (`rulesProcessor.Symbols`) before the next `Reset`. The per-rule bookkeeping
  is `ruleState.rule`, `ruleState.lastAccept`, and `ruleState.isActive`. The
  read position is the cursor together with the in-flight checkpoint that marks
  the token start (armed by `remarkSlot` at the start of `nextToken`). The
  boundaries are the length of the stack, the number of real symbols read
  (`read` in `nextToken`: the stack without the EOF symbol, which is always
  its last), and the per-token byte count `tokenBytes`, a local of one
  `nextToken` call.
- **Reset timing.** `rulesProcessor.Reset` runs from `Scanner.getProcessor`
  when the next token starts. Between two tokens the processor still holds the
  last token's symbols and `Rule` values, but nothing reads them before the
  reset. So the accurate statement is "reset before the next token starts;
  nothing carries over", not "cleared when the token ends". `Reset` returns
  every rule to the `Rule` it was registered with. Evidence:
  `TestScannerTokenSpans` (a multi-token stream in which every byte is covered
  by exactly one token, in order; it would fail if one token's rule state
  carried into the next).
- **The per-step pushback count.** Inside one `processSymbol` call the VM
  counts the pushbacks of one rule's chain. `pushbackCount` is declared inside
  the per-rule loop of `processSymbol`, so there is one count per rule per
  call, not one per call. The VM uses it to choose the popped symbol, to bound
  the chain, and to compute the match length. The count ends with that rule's
  turn. So the VM does count within one symbol step, and it keeps no count
  across symbol steps.
- **The stack is a model.** A pushback does not remove anything from
  `rp.buf`. The pushback loop of `processSymbol` reads
  `rp.buf[len(rp.buf)-pushbackCount]`. So one rule's pushback cannot change
  what any other rule is shown, and the stack is the same for every rule
  within a step. Evidence: `TestPushBackIsPerRule` for the isolation between
  two rules.
- **Fields that persist across tokens.** These fields outlive a token. None of
  them holds state on a rule's behalf, and no rule can read any of them, so they
  do not break the principle:
  - `ruleState.initialRule` and `rulesProcessor.rules`: registration data, the
    `Rule` each type was registered with and the registration order.
  - `rulesProcessor.states`: the map that holds each rule's `ruleState`. The
    map itself persists; the bookkeeping in each entry is reset as described
    above.
  - `Scanner.rules` and `Scanner.rulesMap`: the registration table.
  - `Scanner.processor`: a cache of the processor, dropped by `AddRule` and
    rebuilt by `getProcessor`; its per-token contents are reset as described
    above.
  - `Scanner.cursor`: the stream position, owned by `bytescan/cursor`.
  - `Scanner.marks` and `Scanner.marksNext`: the context-window ring of
    checkpoints for committed tokens. They mark source positions for
    `Context`, not rule state. The context window is not a deviation.
  - `Scanner.canRemark` (a cursor capability) and `Scanner.maxTokenBytes`
    (a configured boundary).
  - `Scanner.mu` and `Scanner.rulesMu`: the locks.
- **State a rule keeps itself.** A rule is an ordinary Go function, and it may
  keep its own state in the closures of the `Rule` values it returns. That
  state is the rule's, not the VM's: the VM neither holds it nor resets it, and
  [returned-error rollback](rule-vm-contract.md#returned-error-rollback) does
  not undo it.

### Principle 2: the VM checks boundaries

**Design principle (non-negotiable).** Boundary checks are the VM's
responsibility, not the rule's. The VM validates every verdict a rule returns
against what it holds, including:

- a pushback past the bottom of the stack;
- a nil `next` where the verdict needs one;
- a match longer than the symbols read;
- a zero-length match;
- a `State` value outside the four defined constants.

`WithMaxTokenBytes` is unbounded by default (see `WithMaxTokenBytes`), so a
rule that never settles makes the VM retain input until the end of input.

**Current contract — conformance.** The engine conforms. The VM detects every
case in the list. Each reaction to a bad verdict retires only the rule that
returned it, or refuses only the bad match, so principle 3 holds for these cases
as well. The per-token byte bound below is a configured limit, not a verdict,
and fails only the token that exceeds it. Each reaction is specified once in
the rule and VM contract; this list names it and links there.

- **Pushback past the bottom of the stack, and a nil `next` with
  `StatePushBack`.** The rule is retired as a reject: the bounds rule of
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback).
- **Nil `next` with `StateContinue`.** The rule is retired; see
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback). A
  nil `next` with `StateAccept` is not a boundary violation: it records the
  acceptance and stops the rule.
- **A match longer than the symbols read.** Only an accept of the synthetic
  EOF symbol itself can be longer than the symbols read. The VM discards that
  verdict and retires the rule; see
  [EOF handling](rule-vm-contract.md#eof-handling). `nextToken` keeps a
  defensive length check that this makes unreachable.
- **A zero-length match.** The VM commits no empty token; see
  [Progress and zero-length behavior](rule-vm-contract.md#progress-and-zero-length-behavior).
- **A `State` outside the four constants.** The VM retires the rule as
  `StateReject` does and does not panic; see
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback).
- **A rule still active at the end of input.** After the EOF symbol the VM
  retires every rule that is still active, and `Process` always decides; see
  [EOF handling](rule-vm-contract.md#eof-handling). `nextToken` keeps a
  defensive check for an undecided processor after the EOF symbol that this
  makes unreachable.
- **The per-token byte bound.** When `WithMaxTokenBytes` is set, a token that
  would exceed it fails; see
  [Speculative lookahead limits](resources-and-streams.md#speculative-lookahead-limits).

### Principle 3: rules are free

**Design principle (non-negotiable).** A rule may do anything, including return
bad values. The VM reacts only to what it can do within its boundaries. A bad
value from one rule must not corrupt the VM, the stack, or another rule.

**Current contract — conformance.** The engine conforms. Every reaction listed
under principle 2 retires only the rule that returned the bad verdict: an
accept that the rule recorded earlier stays, and the other rules continue on
the same stack, so a sibling rule's valid match is emitted.

- A bad pushback or a nil `next`: evidence `TestScannerPathologicalRules` "Rule
  Returns nil with StatePushBack" (the bad rule is retired, and a sibling rule
  still wins `"a"`). A rule that never advances is contained by the pushback
  bound and the zero-length check under principle 2.
- An invalid `State`, an accept of the synthetic EOF symbol, and a rule still
  active at the end of input: evidence `TestInvalidStateRetiresRule`,
  `TestAcceptOfEOFSymbolIsDiscarded`, and `TestStillActiveAtEOFRetiresRule`.
- A rule that panics, on a direct call or as the next rule of a pushback
  chain, is retired for the current token as `StateReject` does, and lexing
  continues; the panic does not reach the caller. The behaviour is specified
  under [Panics](rule-vm-contract.md#panics).

### Principle 4: the VM only reports matches

**Design principle (non-negotiable).** The VM emits which rule matched and what
it matched: the type, the text, and the span. It performs no action and
interprets nothing. A consumer acts on that information.

**Current contract — conformance.** The engine conforms.

- `Next()` returns a token built from the winning type, the matched runes, and
  the committed span (the end of `nextToken`). The engine runs no hook and
  decodes nothing; see
  [Matched content](consumer-contract.md#matched-content-versus-transformed-or-decoded-values).
- **A rule's panic is not reported.** The VM retires the rule and discards the
  panic value; `Next()` returns the token chosen from the recorded accepts and
  no error. See [Panics](rule-vm-contract.md#panics).
- **`UNKNOWN` when no rule matches** (`rulesProcessor.Process`). Verdict:
  conforms. `UNKNOWN` is the VM's report that no rule matched one symbol; it is
  not an action and not an interpretation of the input. Evidence:
  `TestScannerProcessor` "No Matching Rules (produces UNKNOWN)".
- **`UNKNOWN` in place of the winning rule's type on a zero-length match**
  (the zero-length branch of `nextToken`). Verdict: conforms. This is the
  principle-2 boundary reaction: the VM refuses the zero-length match, so no
  rule matched a non-empty text, and the report says so. Evidence:
  `TestProcessZeroLengthMatch`.

### Principle 5: lookahead belongs to the rule

**Design principle (non-negotiable).** A rule decides how far to read and how
much to give back with `StatePushBack`. The VM applies the stack operation and
the boundary check.

**Current contract — conformance.** The engine conforms.

- The VM reads the next symbol while any rule is still active
  (`rulesProcessor.Process` reports "undecided" while a rule is active), so the
  rules decide how far a token reads. The VM has no lookahead mechanism of its
  own.
- Each `StatePushBack` pops one more symbol from the model stack and shows it to
  the returned `next`: the current symbol first, then earlier symbols in reverse
  order (the pushback loop of `processSymbol`). The boundary check is
  principle 2's. Evidence: `TestPushBackSymbolOrder`; `TestScannerProcessor`
  "Context-Aware Rules (BOL)" (the comment rule pushes back its terminator, so
  the terminator is not part of the comment).
- A pushback chain that ends in `StateContinue` or in a non-nil `next` with
  `StateAccept` is not remembered after the step; that ending is unsupported.
  See the chain endings under
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback).

## Decision log

Decisions and closed questions, newest first within each list. Each entry says
where the resulting contract now lives. A closed question keeps its number from
the [open questions](open-questions.md), so an old reference to it stays
meaningful.

### Owner decisions

- **2026-11-19 — where an empty change sits.** An edit that changes no
  token, such as case A24, gives an empty change. It sits where the trim at
  the start and the trim at the end meet: `Start`, `OldEnd` and `NewEnd` are
  equal there, and `Shift()` is 0. The owner recorded the behavior of the
  code; no code changed. Current contract; see
  [What the change contains](resumable-lexing.md#what-the-change-contains).
- **2026-11-18 — the internal error of the lexer with state.** A broken
  engine invariant during a re-lex returns an error that wraps the existing
  `bytescan.ErrInternal`. The owner added no exported name for it. Every
  later call that can fail returns the same error. This answers the point of
  question 29 that was left open. Current contract; see
  [Errors](resumable-lexing.md#errors).
- **2026-11-18 — the questions of the lexer with state.** The owner answered
  questions 17, 23, 24, 25, 26, 28 and 29 and approved the names and
  signatures that [Resumable lexing](resumable-lexing.md#the-lexer-with-state)
  gives for them: the package `statelex`; one constructor that takes a
  `modes.ModeSet`; one edit for each call, at character boundaries, with any
  bytes; a complete change value that lists the removed and the inserted
  tokens; a trim by type, range and bytes; the end of the held text as the
  end of input; five queries and two position conversions; no option that
  can fail a re-lex. The owner deferred question 27. When the owner decided,
  all of it was Intended capability. It is a Current contract. Closes
  questions 17, 23, 24, 25, 26, 28 and 29; see [Closed design questions](#closed-design-questions).
- **2026-11-18 — the engine questions of resumable lexing.** The owner
  answered questions 16, 18, 19, 20, 21 and 30 and approved the names and
  signatures that [Resumable lexing](resumable-lexing.md) gives for them:
  "need more input" is a sentinel from the source; a second scanner method
  returns the lookahead record; a scan restarts with positions from the
  restart point and two start flags; the flags are part of the sync test;
  the rules carry the mode actions; the lexer with state is an optional
  package of this module, built after the engine pieces, with balanced trees
  inside. When the owner decided, all of it was Intended capability. It is a
  Current contract. Closes
  questions 16, 18, 19, 20, 21 and 30; see
  [Closed design questions](#closed-design-questions).
- **2026-11-18 — resumable lexing and the lexer with state.** The lexer state
  for text that changes after it was lexed is bytescan's job; handling the
  document (undo, lines, display) is not. Three layers: the engine, unchanged,
  so a stream consumer pays nothing; pure rule definitions; and an optional
  lexer with state that holds its own copy of the text and accepts requests to
  change ranges that were already lexed. A re-lex restarts at a token boundary.
  A scanner reports "need more input" as a distinct signal. bytescan defines
  the mode state as a stack of mode names. One edit gives one change message:
  the removed tokens, the inserted tokens, and the shift of everything after.
  This decision approved no API (the answers above approved some names).
  When the owner decided, all of it was Intended capability. It is a Current
  contract since the package `statelex` was built. See
  [Resumable lexing](resumable-lexing.md).
- **2026-11-17 — the cursor moves into bytescan.** The cursor of
  `github.com/xiam/textreader` becomes the package `github.com/xiam/bytescan/cursor`
  (type `cursor.Reader`, was `TextReader`), which imports only the standard
  library. bytescan uses it instead of textreader from v0.4.0.
- **2026-11-16 — the byte-exact cursor.** Option A: a byte mode in the cursor,
  then a switch of `bytesource` to it. Done: `bytesource.NewCursor` returns
  `cursor.NewByteReader`; see [Byte reads](layers.md#byte-reads-bytesource).
- **2026-11-16 — positioning.** The project reads streams of data and
  classifies what it finds, for text and binary data alike; parsing is out of
  scope. See [Purpose and scope](core-contract.md#purpose-and-scope).
- **2026-11-13 — composition and ambiguity.** Leftmost-longest inside a
  composed rule, registration-order ties between registered rules, delayed
  acceptance with one terminal pushback chain. Closes question 8. The contract
  is [Composition and ambiguity](composition.md#composition-and-ambiguity).
- **2026-11-12 — rule panics.** The VM recovers a rule's panic and retires that
  rule for the current token, as for `StateReject`, and does not report the
  panic value. It replaces the earlier contract, under which the panic
  propagated out of `Next` and a consumer had to `recover` around its own
  call. Closes question 7. The contract is
  [Panics](rule-vm-contract.md#panics).
- **2026-11-12 — the five VM design principles.** See
  [VM design principles](#vm-design-principles).

### Closed design questions

- <a name="question-7"></a>**7. Panic isolation** — closed by the owner decision of 2026-11-12 above.
  ([Panics](rule-vm-contract.md#panics))

- <a name="question-8"></a>**8. Composition and ambiguity** — closed by the owner decision of
  2026-11-13 above.
  ([Composition and ambiguity](composition.md#composition-and-ambiguity))

- <a name="question-9"></a>**9. State ownership and reset** — closed by principle 1. **Reconciled with
  principle 1.** A composed rule owns its internal state, and it carries that
  state in the `Rule` values it returns. The VM holds none of it and resets
  every rule to its registered `Rule` before each token. A composed rule must
  fit that model; the option of changing the model is closed. State that must
  survive from one token to the next lives above `Next()`, as for lexical
  modes. ([Composition and ambiguity](composition.md#composition-and-ambiguity),
  [Lexical modes](layers.md#lexical-modes-modes))

- <a name="question-11"></a>**11. Synthetic-token and progress semantics** — closed by what shipped
  (the `Source` filters). The VM never emits a zero-width or synthetic token:
  it refuses a zero-length match (principle 2) and reports only what a rule matched
  (principle 4), so every token `Next()` returns covers a non-empty source
  range. A synthetic token comes from a layer above `Next()`:
  `NewEmptyToken` builds one with no text at a position, and a filter inserts
  it. ([Filters](layers.md#filters))

- <a name="question-12"></a>**12. Core/extension seam** — closed by what shipped
  (the `Source` filters and the `modes` driver). The seam for language-specific
  policies that are not about recognizing text, such as indentation, is a
  `Source` layered above `Next()`: a filter or a driver.
  ([Core/extension boundary](layers.md#coreextension-boundary))

- <a name="question-13"></a>**13. `\r\n` post-sequence line/column** — closed: the cursor's positions
  after `\r\n` and after a lone `\r` are Current contract, pinned by
  `TestCodeNewlinePositions`. Whether the toolkit promises them independently
  of the cursor stays open as question 5.
  ([Line and column conventions](consumer-contract.md#line-and-column-conventions))

- <a name="question-16"></a>**16. Complete tokens and the true end of input** — closed by the
  owner decision of 2026-11-18 above. A source that has no input now returns
  the sentinel `cursor.ErrMoreInput`; `io.EOF` stays the true end. `Next`
  returns the same value, not wrapped (`bytescan.ErrMoreInput` is a second
  name for it). A token is complete when the rules decided it before the
  available input ran out; the unfinished token is not returned and is lexed
  again from its start. Current contract.
  ([Need more input](resumable-lexing.md#need-more-input))

- <a name="question-17"></a>**17. The end of the held text** — closed by the owner decision of
  2026-11-18 above. At the end of its text, the lexer with state shows the
  rules the end of input, always, so every byte belongs to a token. A REPL
  sees an open construct through `ModeAtEnd` and `Unfinished`. Current
  contract, for the end of input and for the two queries.
  ([The end of the held text](resumable-lexing.md#the-end-of-the-held-text))

- <a name="question-18"></a>**18. Positional flags at a restart and at a sync point** — closed by
  the owner decision of 2026-11-18 above. A restart receives its flags from
  `cursor.Start` (question 19). A boundary is a sync point only when the BOF
  and BOL flags of its first symbol are the same in the old and the new
  text; the flags are computed from the text, and nothing is stored for each
  token. Current contract.
  ([Why the sync test is exact](resumable-lexing.md#why-the-sync-test-is-exact))

- <a name="question-19"></a>**19. Starting a scan at a position and in a state** — closed by the
  owner decision of 2026-11-18 above. `cursor.Reader.ResetStart` resets the
  cursor onto the text from a position; the scan reports positions from the
  restart point, and `cursor.Start` carries the two facts that a rule can see
  there (`InText`, `LineStart`). The lexer with state adds the restart offset
  to byte offsets. The scanner reads the start through `cursor.Reader.Start`
  (owner decision, 2026-11-18).
  The mode state of the start comes from question 21, a Current contract.
  Current contract for the position and the flags; a restart in a given mode
  state is a Current contract (`TestStartInsidePushedMode`).
  ([Starting a scan in the middle](resumable-lexing.md#starting-a-scan-in-the-middle),
  [Cursor contract](cursor.md))

- <a name="question-20"></a>**20. The lookahead record** — closed by the owner decision of
  2026-11-18 above. `Scanner.NextLookahead` returns the token and a
  `Lookahead` value: the bytes shown to the rules after the end of the token
  (`After`) and whether the rules were shown the end of input (`ReadEnd`).
  `Next`, `Token` and `Source` do not change. Current contract.
  ([The lookahead record](resumable-lexing.md#the-lookahead-record))

- <a name="question-21"></a>**21. The mode stack and the `modes` package** — closed by the owner
  decision of 2026-11-18 above. A `modes.ModeSet` holds named modes; each rule
  of a mode carries its action (`Rule` stays, `Push` pushes a mode, `Pop`
  pops). A pop of the last mode does nothing. `modes.Stack` is an opaque value
  with `Top`, `Len` and `Equal`. `Driver`, `SwitchFunc` and `Cache` do not
  change. Current contract (`modes.ModeSet`, `modes.StackDriver`;
  `TestRuleSetB`, `TestStackDriverPopLastMode`).
  ([The mode state](resumable-lexing.md#the-mode-state),
  [Lexical modes](layers.md#lexical-modes-modes))

- <a name="question-23"></a>**23. The edit request, the change, the constructor and the names** —
  closed by the owner decision of 2026-11-18 above. Package `statelex`.
  `New(ms *modes.ModeSet)` builds an empty lexer, and reads the definitions
  through a bridge that is not exported. `Apply(e Edit)` takes one edit and
  returns one `Change`, a complete value with `Start`, `OldEnd`, `NewEnd`,
  `Removed`, `Inserted` and the method `Shift`. A `Token` is a type and a
  byte range, with no mode state. `Insert`, `Delete` and `SetText` are the
  conveniences. Current contract for a set with one
  mode and for a set of several modes.
  ([The constructor](resumable-lexing.md#the-constructor),
  [The edit request](resumable-lexing.md#the-edit-request),
  [The change message](resumable-lexing.md#the-change-message))

- <a name="question-24"></a>**24. Which tokens count as unchanged in a change** — closed by the
  owner decision of 2026-11-18 above. A token at either end of the re-lexed
  run is left out when the old and the new token have the same type, the
  same range (moved by `d` at the end) and the same bytes. The trim at the
  start does not stop at the edit. Current contract.
  ([What the change contains](resumable-lexing.md#what-the-change-contains))

- <a name="question-25"></a>**25. Lines, columns and rune offsets after an edit** — closed by the
  owner decision of 2026-11-18 above. The rope keeps newline and rune counts
  in each node; `Position` and `Offset` convert between a byte offset and a
  line and rune column in O(log n). No token stores a coordinate. UTF-16
  columns are not part of this milestone. Current contract.
  ([Lines, columns and rune offsets](resumable-lexing.md#lines-columns-and-rune-offsets))

- <a name="question-26"></a>**26. Edit units** — closed by the owner decision of 2026-11-18
  above. An edit uses byte offsets. `Apply` refuses with `ErrSplitRune`, and
  changes nothing, an edit whose start or end falls inside a valid UTF-8
  character; the owner: "I don't see a reason why to split Unicode
  characters. That is usually not expected anywhere." The new bytes may be
  any bytes, invalid UTF-8 included. Step 1 of the re-lex has a clause for
  broken bytes before the edit. Current contract.
  ([The edit request](resumable-lexing.md#the-edit-request),
  [The four steps](resumable-lexing.md#the-four-steps))

- <a name="question-28"></a>**28. Reading the lexer state** — closed by the owner decision of
  2026-11-18 above. `Len`, `Text`, `Tokens`, `ModeAtEnd` and `Unfinished`; a
  range is clipped and a query never fails. A query for the token or the mode
  state at an offset is not part of this milestone. Current contract.
  ([The queries](resumable-lexing.md#the-queries))

- <a name="question-29"></a>**29. Errors while re-lexing** — closed by the owner decision of
  2026-11-18 above. The lexer takes no scanner option that can fail a token.
  `ErrRange` and `ErrSplitRune` change nothing. `New` checks the mode set. An
  internal error makes every later call that can fail return it; it wraps
  `bytescan.ErrInternal` (the owner decision on the internal error above).
  Current contract.
  ([Errors](resumable-lexing.md#errors))

- <a name="question-30"></a>**30. Packaging, the token store and the build order** — closed by
  the owner decision of 2026-11-18 above. The lexer with state is an optional
  package of this module (`statelex`, question 23), which the core never
  imports. It is built after the four engine pieces. It holds the text in a
  rope and the tokens in a balanced tree with summed lengths and the furthest
  reach, not visible in the API. Current contract.
  ([Scope and layers](resumable-lexing.md#scope-and-layers),
  [The lexer with state](resumable-lexing.md#the-lexer-with-state))

### Known deviations

A **Known deviation** is current behaviour that breaks a design principle,
with its fix status; a consumer must not rely on one. The engine has none at
`master`. This list audited the engine against the five principles. It was
first written against v0.2.3, where KD-1, KD-2, KD-3, and KD-4 deviated.
Release v0.2.4 fixed them. Every entry now conforms and is kept as history; the
current behaviour is specified at the link of each entry.

- <a name="kd-1"></a>**KD-1. A `State` outside the four constants.** Conforms to principles 2
  and 3 since v0.2.4. In v0.2.3 the VM panicked here, with the consequences
  that KD-2 records for a rule's own panic in v0.2.3. Now:
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback).

- <a name="kd-2"></a>**KD-2. A rule that panics.** Conforms to principles 3 and 4 since v0.2.4.
  In v0.2.3 the panic propagated out of `Next()` without running `abandon`, so
  a caller that recovered found the symbols read before the panic lost and the
  in-flight checkpoint orphaned on the next `Next()`. The probe that showed it
  is now `TestPanicLeavesScannerUsable`, inverted. Now:
  [Panics](rule-vm-contract.md#panics).

- <a name="kd-3"></a>**KD-3. A rule that accepts the synthetic EOF symbol while a sibling rule
  has a valid match.** Conforms to principles 2 and 3 since v0.2.4. In v0.2.3
  the token failed for every rule, on every retry. Now:
  [EOF handling](rule-vm-contract.md#eof-handling).

- <a name="kd-4"></a>**KD-4. A rule still active at the end of input while a sibling rule has a
  valid match.** Conforms to principles 2 and 3 since v0.2.4. In v0.2.3 the
  token failed for every rule, on every retry. Now:
  [EOF handling](rule-vm-contract.md#eof-handling).

- <a name="kd-5"></a>**KD-5. A pushback chain that ends in `StateContinue` or in a non-nil
  `next` with `StateAccept`.** Conforms to principles 1 and 5; the ending is
  unsupported. The VM forgets the pushback count at the end of the step, so the
  symbols pushed back are shown to the rule again and later match lengths do
  not account for them. Remembering the count across steps would itself break
  principle 1. Now: the chain endings under
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback).

- <a name="kd-6"></a>**KD-6. `(nil, StateContinue)`.** Conforms to principles 2 and 3: the VM
  retires the rule. Now:
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback).

- <a name="kd-7"></a>**KD-7. The fields of `rulesProcessor`, `ruleState`, and `Scanner`.**
  Conform to principle 1. Each field is either per-token stack or bookkeeping,
  which `Reset` restores before the next token, or a field that persists across
  tokens and holds nothing on a rule's behalf. Now:
  [principle 1](#principle-1-stateless-stack-model).

- <a name="kd-8"></a>**KD-8. The two places where the VM chooses a type itself.** Conform to
  principle 4: `UNKNOWN` when no rule matches and `UNKNOWN` on a zero-length
  match. Now: [principle 4](#principle-4-the-vm-only-reports-matches).
