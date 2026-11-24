# Table compiler (form A built)

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

**Status: Current contract for form A; form B is not built.** On
2026-11-23 the owner decided to build form A, in the words "Yes, keep form
A", and to build no form that bypasses the VM, in the words "I don't think
bypassing the VM is a good architectural decision right now." On the same
day the owner adopted practice 6 on classes, in the words "Adopt practice 6
(Recommended)". The [decision log](design-principles.md#owner-decisions)
records these decisions, which close
[question 31](design-principles.md#question-31). Form A is built: the table
of a pure core rule, the state bound and the work bound, the fallback to the
engine, the cache for the runes above U+00FF, and the switch that forces the
engine in the tests (`tableRule`, `maxTableStates`, `maxTableWork`,
`tableCache`, `engineForced`, `rules/core/table.go`). Form A adds no exported
name. No form bypasses the VM: the VM calls every registered rule for every
symbol, and a tabled rule stays a `bytescan.Rule`. This page approves no
name, no signature and no data structure. The owner chose the name of this
page on 2026-11-22; the decision log records it. A statement about what the
code does today is marked **Current contract** and cites a test or a symbol.
The other sections keep the design as it was proposed. Form B stays on this
page as the design that was not taken.

This page states no measured number. The shares of the time and the expected
gain are in an internal estimate under `internal-docs/benchmarks/`, which the
release does not export.

## Contents

- [In short](#in-short)
- [What the engine does today](#what-the-engine-does-today)
- [Pure core sets](#pure-core-sets)
- [What the compiler produces](#what-the-compiler-produces)
- [Longest match, ties, lookahead and pushback](#longest-match-ties-lookahead-and-pushback)
- [The fallback to the engine](#the-fallback-to-the-engine)
- [The public API](#the-public-api)
- [What changes in the tokens](#what-changes-in-the-tokens)
- [The design principles](#the-design-principles)
- [The proof of equal tokens](#the-proof-of-equal-tokens)
- [The expected gain](#the-expected-gain)
- [The cost](#the-cost)
- [The recommendation](#the-recommendation)
- [What the owner decides](#what-the-owner-decides)

## In short

A rule built only from `rules/core` is a finite automaton. The engine runs
that automaton step by step and builds its state again for every symbol. A
**table compiler** computes the states once and stores them in a table. Each
step is then one lookup: the current state and the next rune give the next
state. **Current contract** for form A: a pure core rule runs on its table
(`newRuleOf`, `tableState.step`; `TestTableRulesTabled`).

The table gives the same tokens as the engine. The tokens have the same
types, the same text, the same positions and the same errors, and the scanner
reads the same symbols to decide each token. Only the speed and the
allocations change. One condition applies: each class of the rule must give
the same answer for the same rune and have no side effect, as
[practice 6](../rule-authoring.md#practice-6-progress-and-purity) requires
since the owner adopted it on 2026-11-23 (the *Classes* item of
[Composition and ambiguity](composition.md#composition-and-ambiguity)).
A rule that is not built only from `rules/core` keeps the engine and does
not change.

The design has two forms:

- **Form A, a table for each rule.** A composed rule that is pure gets its
  own table. The VM still calls one `Rule` for each registered rule. The
  form keeps the five [design principles](design-principles.md#vm-design-principles).
- **Form B, one table for the scanner.** When every registered rule is pure,
  the scanner compiles all of them into one table. The table holds the token
  type and the tie-break. The VM calls no rule. This form conflicts with
  principle 1 (see [The design principles](#the-design-principles)). The
  owner decided on 2026-11-23 not to build it.

Form B builds on form A: its table is made of the tables of form A.

## What the engine does today

**Current contract.** These facts about the engine decide what a table can
replace.

- **A composed rule is a program.** Each combinator compiles its node graph to
  a nondeterministic automaton, a `program` of instructions in which only the
  leaves read symbols (`program`, `compile`, `rules/core/program.go`). A leaf
  of a core primitive is a `matcher`: a deterministic automaton over runes
  whose states are numbers (`matcher`, `rules/core/node.go`).
- **The engine keeps a set of threads.** Each thread is one leaf that waits
  for the next symbol. For every symbol the engine steps each thread, follows
  the program to the next leaves, merges the threads that are in the same
  state, and returns a new `Rule` value that holds the new set
  (`snapshot.stepCore`, `coreStep.visit`, `merge`, `newRule`,
  `rules/core/engine.go`). Evidence: `TestEngineMatchesReference`
  (`rules/core/engine_internal_test.go`) and `TestCombinatorCost`
  (`rules/core/cost_internal_test.go`).
- **A program without opaque leaves and lookaheads reads only the current
  symbol.** Such a program keeps no log of the symbols of the token
  (`program.needsLog`), and the engine steps it on the fast path
  (`snapshot.stepCore`).
- **A leaf of a core primitive sees only the rune.** A `matcher` sees no flag
  and no end-of-input symbol (`matcher`), and a class sees only the rune
  (`Class`, `rules/core/class.go`). A rule that reads a flag, such as
  `AtLineStart`, composes as an opaque part (`AtLineStart`,
  `rules/core/combinator.go`).
- **A class is a Go function.** `Class` is `func(rune) bool`, and any such
  function converts to a class (`Class`). So the engine cannot list the runes
  of a class; it can only ask the class about one rune.
- **Some primitives already run as small tables.** A registered `Literal`,
  `AnyLiteral` or `Keywords` steps through `Rule` values that are built once
  when the rule is built, so a step allocates nothing (`literalChain`,
  `literalsAt`, `keywordTable`). Evidence: `TestKeywordsCost`
  (`rules/core/keywords_test.go`).
- **The VM calls every active rule for every symbol.** It records the longest
  accept of each rule, and it decides when no rule is active
  (`rulesProcessor.processSymbol`, `rulesProcessor.Process`, `processor.go`).
  It picks the longest match and breaks a tie by registration order
  (`rulesProcessor.pickBestMatch`; [Tie priority](rule-vm-contract.md#tie-priority)).
  Evidence: `TestCombinatorTies` (`rules/core/combinator_test.go`).

## Pure core sets

The design uses two terms. They are not the "pure rules" of
[resumable lexing](resumable-lexing.md#pure-rules), which are any rules that
keep their progress only in their returned `Rule` values. Every pure core rule
is such a pure rule; the reverse is not true.

- A **pure core rule** is a rule that `rules/core` built, whose leaves are all
  matchers of core primitives, and that has no lookahead. In the code of
  today, it is a composed rule whose `program` has `needsLog` false, or a
  primitive whose node is one matcher. These are pure core rules: `Literal`,
  `LiteralIgnoreCase`, `AnyLiteral`, `AnyLiteralIgnoreCase`, `Rune`,
  `RuneRun`, `RuneRunMax`, and `Sequence`, `Choice`, `Optional`,
  `ZeroOrMore`, `OneOrMore` and `Repeat` of pure core rules. The rules of
  `rules/base`, `rules/special` and `rules/octet` that are built only from
  these are pure core rules too.
- These are not pure core rules: a hand-written rule; a core rule wrapped in
  a hand-written function, which composes as opaque
  (`TestWrappedCoreRuleIsOpaque`); `AtLineStart`, which reads a flag;
  `FollowedBy`, `NotFollowedBy`, `Keyword` and the `Keywords` family, which
  hold a lookahead; and every combinator with one of these as a part.
- A **pure core set** is the set of rules registered on one scanner when every
  one of them is a pure core rule.

Form A needs pure core rules. Form B needs a pure core set.

## What the compiler produces

### The states

The compiler runs the engine's step ahead of time. For each set of threads
and each group of runes, it computes the next set once, as a
regular-expression library builds a deterministic automaton from a
nondeterministic one. A **state** of the table is a pair:

- the merged set of threads that `snapshot.stepCore` would hold after some
  input;
- whether the last step reached the end of the program, so that the rule
  accepts the text read so far.

Both parts are the identity of the state. Two inputs lead to the same state
only when both parts are equal. The set of threads alone is not enough,
because the end of the program is recorded apart from the threads
(`coreStep.visit` records `opMatch` in `best`, not as a thread). For example,
in `Choice(Sequence(ZeroOrMore(Literal("a")), Literal("b")), Literal("a"))`
the inputs `a` and `aa` leave the same set of threads, but the rule accepts
`a` and does not accept `aa`. So `a` and `aa` lead to two different states.

For form B, a state is one such pair for each registered rule, or "dead" for
a rule that has stopped. The state accepts for every rule whose pair accepts.

A state never depends on the position in the token. So the states can be
built once and shared by every token and every scanner.

### The symbol classes

A table cannot have one column for each Unicode code point. The compiler
groups runes into **symbol classes**: two runes are in the same symbol class
only when every leaf, in every one of its states, gives the same result for
both: the same next state, the same "match" and the same "more"
(`matcher.step`). The table has one column for each symbol class.

The answers of the `Class` values are not enough to group runes. A literal
leaf compares the rune with its own runes, a trie compares it with its edges,
and a leaf with case folding compares fold keys (`literalMatcher`,
`literalsMatcher`, `literalFoldMatcher`). None of these is a `Class`. For
example, in `Sequence(Literal("a"), Rune(AnyRune))` the only class gives the
same answer for `a` and `b`, but the rule matches `ab` and not `bb`. So `a`
and `b` must be in different symbol classes. The definition above, by the
results of the leaves, keeps them apart.

Because a class is a Go function, the compiler cannot list its runes. The
design handles this in two ranges:

- **The runes U+0000 to U+00FF.** The compiler steps every state of every
  leaf on each of these runes when it builds the table. It groups the runes
  with equal results into symbol classes, and each state gets a full row for
  them. This range holds ASCII text and every byte of `bytesource` input.
- **Every other rune.** The table computes the next state the first time a
  state meets the rune, by the engine's own step, and keeps the result in a
  cache of bounded size. When the cache is full, the table still computes
  the step, but it does not store it. The tokens stay the same; only that
  step is slower.

**Current contract** for both ranges: the full rows (`tableRunes`,
`tableState.step`) and the cache of `tableCacheSize` steps for each table
(`tableCache`, `tableState.far`). A step to a set of threads that the table
does not have continues on the engine for the rest of the token
(`tableState.farStep`). Evidence: `TestTableCacheBounded`,
`TestTableCacheConcurrent` and the `cjk` and `emoji` sets of
`TestTableMatchesEngine` (`rules/core/table_test.go`).

### What a state holds

| Item | Form A | Form B |
| --- | --- | --- |
| The next state for each symbol class | yes | yes |
| Whether the rule accepts here | yes | for each rule |
| The token type and the tie-break | no: the VM keeps them | yes: the first registered rule that accepts here |
| Whether any thread is alive | yes | yes, for the whole set |
| The `Rule` value of the state | yes: one, built once | no: the VM calls no rule |

In form A, each state has one `Rule` value, built once. A step returns the
`Rule` value of the next state, as `literalChain` and `keywordTable` do today,
so a step allocates nothing.

### When the table is built

**Current contract.** The table is built the first time the rule reads a
symbol, not when the rule is built (`tableRule.first`). A combinator that is
only a part of a larger rule never reads a symbol by itself, so it pays
nothing. The build happens once for each rule value and is shared by every
scanner that runs the rule (`TestTabledRuleReuse`). The VM shows the first
symbol of a token to every registered rule, so the first token builds the
table of every registered pure core rule (`TestTableRulesTabled`).

## Longest match, ties, lookahead and pushback

- **Longest match inside a composed rule.** The states are the sets of
  threads of the engine, so the table matches the leftmost-longest text that
  the engine matches ([Composition and ambiguity](composition.md#composition-and-ambiguity)).
- **Longest match between registered rules.** In form A, the VM is unchanged.
  In form B, the table records the length of every accept that it passes and
  keeps the longest one. It decides when no rule is alive, which is the step
  at which the VM decides today (`rulesProcessor.Process`).
- **Ties.** In form A, the VM is unchanged. In form B, a state that accepts
  for several rules records the one registered first. That gives the result
  of `rulesProcessor.pickBestMatch`: the longest length wins, and at that
  length the first registered rule wins.
- **End of input.** A matcher sees no end-of-input symbol, so every thread
  ends on it (`snapshot.stepCore`). The table does the same: the end of input
  leads to the dead state, and the accepts recorded before it decide the
  token.
- **Pushback.** **Current contract, no test yet.** A program without opaque
  leaves and lookaheads never returns a pushback chain. `snapshot.stepCore`
  raises `best` only at the current position and then sets `recorded` to it,
  so the pushback branch of `verdictOf` is not reached. So a pure core rule
  accepts, continues or rejects, and the table needs no pushback.
- **Lookahead.** Excluded. A rule with `FollowedBy`, `NotFollowedBy`,
  `Keyword` or `Keywords` keeps the engine or its own prebuilt states. A later
  version could add the one-rune guard of `Keyword` and `Keywords`, as
  `keywordTable` already encodes it, but that is not part of this design.
- **Flags.** Excluded. A pure core rule reads no flag, so the table reads
  only runes.

## The fallback to the engine

The table is an addition to the engine, not a replacement. The engine stays
for every rule that the table does not cover, and its tests stay.

- **A rule that is not a pure core rule** runs on the engine, as today.
- **A rule set with one rule that is not pure** does not use form B. Each of
  its pure core rules can still use form A.
- **A table that grows too large.** The build stops at a bound on the number
  of states. A rule whose table exceeds the bound keeps the engine (form A),
  and a set whose table exceeds it falls back to form A for each rule
  (form B). The decision is made once, when the table is built.
  **Current contract** for form A: the build stops at `maxTableStates`
  states or at `maxTableWork` units of work, and the rule then keeps the
  engine for good, with the same tokens (`buildTable`, `tableRule.build`).
  Evidence: `TestTableBudgetFits`, `TestTableBudgetWideRules`,
  `TestTableBudgetBoundsClosures`, `TestTableBudgetBoundsCounts` and the
  "over the budget" set of `TestTableMatchesEngine`.
- **A state of form A is also a snapshot of the engine.** So a step that the
  table cannot store can continue on the engine from the same set of threads,
  in the middle of a token, with the same result. **Current contract**
  (`tableState.farStep`; `TestTableStepsMatchEngine`).
- **Current contract.** A composed rule with an opaque part or a lookahead
  has a program that needs a log, so it runs on the engine and builds no
  table (`program.needsLog`, `newRuleOf`). Evidence: the "fallback" set of
  `TestTableRulesTabled`, whose `Sequence` with a hand-written part,
  `FollowedBy` and `NotFollowedBy` build no table.

A first version of form B covers a set only when every rule is pure. A mixed
set could run one table for its pure rules next to the VM for the others,
because the table can report its longest match with the registration number
of its rule. That is not part of this design.

## The public API

No exported name is added, removed or changed.

- **Form A** lives inside `rules/core`. A combinator already knows its own
  program, so no detection is needed.
- **Form B** needs the scanner to know that every registered rule is a pure
  core rule. The root package cannot import `rules/core`, because
  `rules/core` imports it. The design needs a bridge inside the module, for
  example an internal package that `rules/core` fills when it is loaded and
  that the scanner reads when it builds its processor. The bridge must not
  call a hand-written rule: the VM must not show a rule a symbol that it
  never produces. Today a combinator recovers the node of a core rule by
  calling the rule with a probe symbol (`nodeOf`, `rules/core/handshake.go`),
  so a bridge would need another way to recognize a core rule.
- **Tests** need a way to force the engine, so that both paths stay tested.
  That is a switch inside the module, not an exported option. **Current
  contract:** the switch is `engineForced`, which the build tag `coreengine`
  sets (`rules/core/engine_tag_on.go`).

## What changes in the tokens

For a rule set whose classes follow
[practice 6](../rule-authoring.md#practice-6-progress-and-purity), nothing
changes in what `Next` returns. In detail:

**Stays the same:**

Each item below names the code and the tests that pin the behavior today.
The table keeps the code of the scanner and the cursor, so these tests keep
running on it.

- the token type, the text, the byte and rune offsets, the span, and the
  lines and columns, which the cursor computes as today (`Scanner.nextToken`;
  `TestScannerTokenSpans` in `tests/cursor_test.go`,
  `TestCodeNewlinePositions` in `rules/base/code_test.go`);
- the `UNKNOWN` token of one rune when no rule matches
  (`rulesProcessor.Process`; `TestScannerProcessor` "No Matching Rules
  (produces UNKNOWN)" in `tests/scanner_matching_test.go`), and the refusal
  of a zero-length match (`TestScannerZeroLengthMatchPrevention` in
  `tests/scanner_pathological_test.go`);
- the longest match and the tie-break by registration order
  (`rulesProcessor.pickBestMatch`; `TestScannerProcessor` "Rule Priority
  (Keyword vs Identifier)", `TestCombinatorTies`);
- the symbols that the scanner reads to decide a token, so the lookahead
  record (`Scanner.NextLookahead`: `After` and `ReadEnd`;
  `TestLookaheadKnownReach` and `TestLookaheadReadEnd` in
  `tests/lookahead_record_test.go`), the commit after a token (the step back,
  or the symbols that the lean read path keeps for the next token,
  `Scanner.keepCarry`), and the re-lex of the lexer with state;
- the errors: `io.EOF`, `ErrMoreInput` (`TestMoreInputNoFalseEnd` in
  `tests/more_input_test.go`), and the per-token bound of
  `WithMaxTokenBytes`, which `Scanner.nextToken` checks, not the rules
  (`TestScannerMaxTokenBytes` in `tests/cursor_test.go`).

What the table must do to keep these is the proof below. For form A, the
tests of that proof exist and pass (see
[The proof of equal tokens](#the-proof-of-equal-tokens)).

**Changes:**

- **The number of calls of a class.** The table asks a class about a rune
  once, when it builds the row or fills the cache, and not at every step. A
  class that counts its calls sees fewer calls.
- **A class that does not give the same answer for the same rune.** The
  table keeps the first answer. Such a class can give other tokens with the
  table than with the engine. Practice 6 requires that a class give the same
  answer for the same rune, so the tokens of such a class are not specified
  ([Composition and ambiguity](composition.md#composition-and-ambiguity)).
- **A class that panics.** The engine retires the composed rule for the
  token when a class panics (principle 3). The table records a panic for a
  rune as a step to the dead state, so the tokens are the same. The panic can
  happen when the table is built, for a rune that is not in the input; it is
  recovered there too.
- **The engine counters.** In form A, `RuleCalls` does not change, because
  the VM still calls one `Rule` for each active rule. In form B, the VM calls
  no rule, so `RuleCalls`, `Pushbacks` and `MaxLive`
  ([The engine counters](measurement.md#the-engine-counters)) no longer
  describe the work; the measurement contract would need new counters.
- **The leaf calls of a combinator.** The "Cost" paragraph of each combinator
  states its leaf calls per symbol, pinned by `TestCombinatorCost`
  ([Composition and ambiguity](composition.md#composition-and-ambiguity)).
  On the table path, a step on a rune up to U+00FF, or on a rune whose step
  the cache keeps, makes no leaf call; a cache miss is one step of the engine
  (`tableState.step`, `tableState.far`). **Current contract:** those
  statements describe the engine path, and each "Cost" paragraph says so
  (`TestCombinatorCost` runs each rule on the engine; the *Cost* item of
  [Composition and ambiguity](composition.md#composition-and-ambiguity)).
- **The time, the allocations and the bytes allocated.**

## The design principles

- **Form A keeps all five principles. Reconciled with principle 1.** The
  state of a rule stays in the `Rule` value it returns: each state of the
  table has its own `Rule` value, built once and never changed. The VM holds
  what it holds today. A `Rule` value can be called again, also
  concurrently, and one rule can serve several scanners
  (`TestCombinatorReuse`, `TestCombinatorRetainedAncestor`,
  `TestCombinatorSharedRule`).
- **Form B: Conflict with principle 1.** The
  table state stands for the state of every registered rule, and the VM holds
  it. Principle 1 says that the VM keeps no state on a rule's behalf, and the
  decision log closed the option of a VM that holds a composed rule's state
  ([question 9](design-principles.md#question-9)). Form B would reset its
  state before each token, as the VM does today, so nothing would carry over
  from one token to the next. But the state would be the VM's, not the
  rules'. The owner could resolve the conflict in one of two ways: amend
  principle 1 for pure core sets, or build only form A. On 2026-11-23 the
  owner chose to build only form A, so principle 1 stays as it is.
- **Principles 2 to 5.** Both forms keep them. A pure core rule returns no
  bad verdict, so the checks of principle 2 have nothing to catch on the
  table path. A panic of a class retires the rule for the token (principle
  3). The VM still reports only the type, the text and the span (principle
  4). A pure core rule has no lookahead (principle 5).

## The proof of equal tokens

The table is correct only if it gives the tokens of the engine. Three kinds
of test prove it. **Current contract** for form A: the three kinds below
exist. `TestOraclesWithEngineForced` (`tests/engine_forced_test.go`) runs
the oracle tests and `TestReferencesLex` again in the build with the tag
`coreengine`. The differential test is `TestTableMatchesEngine`, with
`TestTableStepsMatchEngine`, `TestTabledRuleReuse`, `TestTableRulesTabled`
and the fuzz target `FuzzTableMatchesEngine` (`rules/core/table_test.go`).
The items below keep the design as it was proposed.

1. **The oracle tests of `tests/`.** They register one rule on a scanner and
   compare its tokens with Go's `regexp`. With the table on, every pure core
   row runs on the table. They are `TestCoreMatchesRegexpOracle`,
   `TestCoreClassesMatchRegexpOracle`, `TestCoreCombinatorsMatchRegexpOracle`,
   `TestRepeatOfRuneMatchesClassRepeatOracle`,
   `TestRuneRunMaxMatchesRepeatOracle`, `TestBaseCodeRulesMatchRegexpOracle`,
   `TestBaseTextMatchesRegexpOracle`, `TestSpecialMatchesRegexpOracle`,
   `TestOctetMatchesRegexpOracle` and `TestOctetClassesMatchRegexpOracle`.
   They run twice: with the table, and with the switch that forces the
   engine.
2. **The `regexp` reference check of the harness.** `TestReferencesLex`
   (`internal/bench/references_test.go`) lexes the rule sets of `json` and
   `stream`, which are pure core sets, and compares the type and the offset
   of every token with one `regexp` alternation of the token classes, one
   group for each rule in registration order. That alternation is one
   automaton for the whole set, as the table of form B is, so it checks
   form B on real rule sets.
3. **A differential test.** A new test runs the table and the engine on the
   same rule set and the same input, and compares every token: the type, the
   text, the offsets, the span, the lookahead record and the error. Its rule
   sets: every pure core row of the oracle tests; the rule sets of `json`,
   `slip` and `stream`; sets with ties; classes with runes above U+00FF;
   byte input through `bytesource`; and the two shapes that a careless
   construction gets wrong: `Choice(Sequence(ZeroOrMore(Literal("a")),
   Literal("b")), Literal("a"))`, whose inputs `a` and `aa` leave the same
   threads with different accepts, and `Sequence(Literal("a"),
   Rune(AnyRune))`, rules made only of literals, `AnyLiteral` and
   `LiteralIgnoreCase`, whose runes a grouping by class answers would merge.
   Its inputs: every string over a small
   alphabet up to a length, the inputs of the reference cases, input that
   arrives in parts (`ErrMoreInput`), and a fuzz target. It also checks the
   fallback: a set with a hand-written rule, with `AtLineStart` and with a
   lookahead runs on the engine, and a table over the bound falls back.

The existing tests of `rules/core` and of the root package stay as they are.
The allocation ceilings of `tests/alloc_test.go` and of
`TestEngineAllocationsPerToken` are lowered where the table lowers the
allocations. The owner approved the names of the new tests on 2026-11-23, in
the words "Approve all (Recommended)".

## The expected gain

The internal estimate gives, for each reference case, the share of the time
that each form would replace, and a range for the gain. It was measured on
`master` after the rule states moved to a slice and the engine step was made
cheaper. In outline:

- The table replaces the time of the engine's stepping and allocation
  (form A), and also the VM's loop over the rules, its decision and its reset
  (form B).
- The cases whose rules are pure core sets gain. **Current contract.** The
  rule sets of `json`, `slip` and `stream` are pure core sets: they are built
  only from `Literal`, `Rune`, `RuneRun`, `RuneRunMax`, `Repeat`,
  `Sequence`, `Choice`, `Optional`, `ZeroOrMore` and the `rules/base` and
  `rules/octet` rules made of them, with no lookahead (`jsonRules`,
  `slipRules` and `lineRules`, `internal/bench/rules.go`). Form A takes most
  of the share that form B would take.
- The cases whose rules are hand-written (`code` and `large`) gain nothing.
- The remaining time is mostly the cursor's read of each rune and the commit
  of each token, which the table does not touch.

## The cost

- **The size of the change.** Form A is a change inside `rules/core`: the
  build of the states from the program, the symbol classes, the cache for the
  runes above U+00FF, the bound and the fallback, and the switch for the
  tests. The engine stays. Form B adds a bridge package, a second processor
  in the root package, the detection of a pure core set, new counters, and
  changes to principle 1 and to the measurement contract.
- **The risk to behavior.** Medium for form A: the states are the engine's
  own sets of threads, but the table is a second path that every change of
  the engine must keep equal. Higher for form B: its processor also meets the
  end of input, `ErrMoreInput`, the per-token bound and the lookahead record.
  The differential test is the guard in both cases.
- **The risk to the rule contract.** Both forms rely on practice 6 as a
  requirement on classes, not only on rules; the owner adopted that
  requirement on 2026-11-23. Both change the meaning of the
  cost statements of the combinators. Form B changes principle 1 and the
  engine counters.
- **State explosion.** A table of sets of threads can grow fast. A `Repeat`
  with counts has one state for each count, and a `Choice` of repetitions
  that overlap can have one state for each set of them. Unicode makes a full
  row impossible. The mitigations:
  - symbol classes, so that a row has one column for each group of runes that
    every leaf treats the same, not for each rune;
  - full rows only for the runes up to U+00FF, and a bounded cache for the
    other runes;
  - a bound on the number of states, past which the rule or the set keeps
    the engine;
  - the build at the first symbol, so that a part of a larger rule builds no
    table.
- **The build cost.** The first symbol that a rule reads pays for the build
  of its table (form A), and the first token of a scanner pays for the table
  of its set (form B). A program that lexes only a short text could be slower
  in total. **Current contract** for form A: the build is paid once for each
  rule value, at its first symbol (`tableRule.first`). A rule over a bound
  pays for the build up to the bound before it keeps the engine. For a
  program that lexes one short input with new rule values, the builds can
  cost more than the engine would cost on the whole input. The cold cases
  `coldjson` and `coldbudget` of the benchmark harness measure that first
  use (`coldCases`, `internal/bench/cold.go`). Their numbers are in the
  comparison reports under `internal-docs/benchmarks/compare/`, which the
  release does not export.

## The recommendation

**Superseded on 2026-11-23.** This section is the recommendation that the
owner decided on. The owner decided to build form A and no form that
bypasses the VM, so form B is not built, and no later decision on form B
is pending
([What the owner decides](#what-the-owner-decides)). The section stays as
the record of the proposal.

The three options of the ticket, with one reason each:

- **Build it.** The engine's stepping and allocation are still the largest
  cost of the cases with pure core sets, and form A removes most of it while
  it keeps every principle.
- **Build it after a second profiling pass.** The read path changed after
  the profiles of this design were taken, and the symbol stack is changing in
  another ticket, so their share of the time moves, and the gain of form B over form A is small enough that it should be
  measured on the code that follows them.
- **Do not build it.** The engine is correct, and a table is a second path
  that every later change of the engine must keep equal.

**The recommendation, as proposed: build form A now; decide form B after a
second profiling pass on top of form A.** Form A takes most of the gain, has
no public API and keeps the principles. Form B needed an owner decision on
principle 1 and on the counters. The owner took the first half of the
recommendation and closed the second: form A is built, and form B is not
built.

## What the owner decides

Decided on 2026-11-23 ([decision log](design-principles.md#owner-decisions)):

- Whether to build the table, and which form: build form A ("Yes, keep
  form A"); no form bypasses the VM, so form B is not built.
- Whether practice 6 becomes an explicit requirement on classes: it does
  ("Adopt practice 6 (Recommended)"). A class gives the same answer for the
  same rune and has no side effect.
- For form B: whether principle 1 is amended for pure core sets. It is not,
  because form B is not built, and no counter replaces `RuleCalls`,
  `Pushbacks` and `MaxLive`.

Decided on 2026-11-23, after form A was built:

- The names of the new tests and the internal names of form A, in the words
  "Approve all (Recommended)".
