# bytescan core contract

This document is the index of the canonical prose specification for the
`bytescan` core: what the engine does today, what it is intended to become,
and where the design is still open. The specification is split into the
documents listed under [Documents](#documents); this index holds the purpose,
the status convention and the terminology that they share. It is paired with
the [documentation entrypoint](../README.md) and the
[root README](../../README.md). Where the specification and code disagree,
code wins and the specification is wrong — file a change against the
specification rather than against behavior.

## Contents

- [Documents](#documents)
- [Status convention](#status-convention)
- [Purpose and scope](#purpose-and-scope)
- [Terminology](#terminology)
- [Where each section went](#where-each-section-went)

## Documents

| Document | What it specifies |
| --- | --- |
| [Consumer contract](consumer-contract.md) | What `Next()` returns: the scanner/consumer boundary, classification, matched content, spans, units, lines and columns, token and context lifetimes, unmatched input, EOF and errors, and illustrative scenarios |
| [Rule and VM contract](rule-vm-contract.md) | How the engine decides: competing rules, longest match, tie priority, rule states and pushback, progress, EOF handling, registration, rollback, panics, and what a custom rule must do |
| [Composition (`rules/`)](composition.md) | The construction capabilities of the rule packages and the composition contract of the `rules/core` combinators |
| [Resources and streams](resources-and-streams.md) | The per-token bound, shared cursors, the memory of an open token, read-ahead cost, long-running streams, and concurrency |
| [Layers above `Next()`](layers.md) | `Source` and filters, synthetic tokens, lexical modes (`modes`), byte reads (`bytesource`), the core/extension boundary, post-match actions, and no-match handling |
| [Design principles and decision log](design-principles.md) | The five VM design principles with the engine's conformance, owner decisions, closed questions, and the history of the known deviations |
| [Open design questions](open-questions.md) | Every open question with its constraint, numbered |
| [Cursor contract](cursor.md) | The positioned cursor `bytescan/cursor`: positions, checkpoints and replay, retention and its bound, checkpoint reuse, rune sources, byte mode, and the limits of the basic reader |

## Status convention

Every substantive statement in the specification carries one of four
statuses. Read the last three as a promise ladder, not as a to-do list. The
first one sits above the ladder and binds it.

- **Design principle (non-negotiable)** — a rule the owner has fixed for the
  design of the engine. It binds every engine change: a change that breaks a
  principle is rejected, and a principle is not traded for a feature. This
  status does **not** by itself promise that the current engine conforms. Each
  principle records its current conformance separately, either as Current
  contract with evidence, or as a **Known deviation**: current behavior that
  breaks the principle, with a code reference and its fix status (for example
  "not fixed yet"). A consumer must not rely on a Known deviation, because the
  principle requires it to change. The engine has no open Known deviation;
  the [history](design-principles.md#known-deviations) is in the decision log.
  A statement elsewhere in the specification that disagrees with a principle is
  reconciled with it and marked **Reconciled with principle N**, or marked
  **Conflict with principle N** until the owner resolves it. See
  [VM design principles](design-principles.md#vm-design-principles).
- **Current contract** — the behavior exists in code at `master`. Consumers may
  rely on it. Citing a named test or source location is required, because this
  is the part of the specification that is load-bearing. A source location is
  cited by symbol (a function, method, type or field), not by line number. A
  cited test must execute the code path behind the sentence it supports. Where
  no committed test executes that path, the statement cites the source location
  and carries the marker **no test yet**. The statement is still Current
  contract, because the code at `master` implements it; the marker records the
  gap so that a later change adds the test. Replacing a "no test yet" marker
  with a test that executes the path does not change the statement. A number
  that no committed test reproduces is labeled a **recorded measurement**, not
  a contract.
- **Intended capability** — the owner intends the toolkit to provide this, and
  the specification records the *semantic* intent so later work is decidable.
  It is **not** available API. No method signature, data structure, or
  construction order is approved by this statement.
- **Open design question** — the shape is genuinely undecided. The
  specification names the question and records the constraint it must satisfy;
  it does not choose an answer, and it does not pretend an answer has owner
  approval. Every open question is in [open questions](open-questions.md).

A promise that has no status is a bug in the specification.

## Purpose and scope

`bytescan` reads a stream of data and classifies what it finds, for text and
binary data alike; that is the whole of its job, and parsing is out of scope.
It is a **reusable toolkit for constructing many different scanners**. A
consumer supplies the lexical policy for one language, one text format or one
binary format and receives back a stream of **descriptive tokens** — content,
classification, and source position — and then decides what to do with them.

**Current contract.** The engine available today is a single general-purpose
tokenization core (`Scanner`) driven by rule state machines. It tokenizes; it
does not parse. A rule is either written by hand or built from the rule
packages under `rules/`, in four tiers: `rules/core` (character classes,
primitives, combinators and the `PushBack` helper), `rules/base` (common tokens
of programming languages and of human text, composed from core),
`rules/special` (URL, Email and Emoji, composed from core) and `rules/octet`
(byte values of binary formats, composed from core). Every constructor
returns a `bytescan.Rule`, so package rules and hand-written rules register
and compose the same way. The tier model and the import rules are in
[docs/rules.md](../rules.md); the evidence for each capability is under
[Construction capabilities](composition.md#construction-capabilities).

**Current contract.** Lexical modes, filters and byte reads live in a layer
above `Next()`: the `modes` package runs several scanners in turn over one
shared cursor, a `Source` filter such as `Skip` reads one stream of tokens
and returns another, and `bytesource` reads a byte stream. See
[Layers above `Next()`](layers.md).

**Intended capability.** On top of that core, the toolkit is intended to offer
the rule tools that have not shipped: named rules that can be referenced by
name, and post-match actions, so that a consumer can describe a scanner
declaratively in native Go instead of hand-rolling automata. See
[Construction capabilities](composition.md#construction-capabilities) and
[Post-match actions](layers.md#post-match-actions). Whether a declarative
frontend, such as a schema- or table-driven one, should compile down to the
same engine is [open question 15](open-questions.md#question-15).
**Reconciled with principles 1 and 4.** A lexical mode needs state across
tokens, and a post-match action is an action, so both live outside the VM, in a
consumer or builder layer above `Next()`. The VM itself stays as the
[VM design principles](design-principles.md#vm-design-principles) describe it.

**Out of scope for the core.** Parsing, grammar validation, AST construction,
and interpretation are downstream consumers' jobs. The core never produces a
syntax tree and never decides what a token *means* beyond assigning it a
classification and a span. See
[Scanner/consumer boundary](consumer-contract.md#scannerconsumer-boundary).

**Use cases, not shipped scanners.** Programming languages, data formats,
regular-expression syntax, natural-language segmentation, binary formats and
long-running streams are use cases of the toolkit: each is a domain a consumer
could build a scanner for using the core and the rule packages under `rules/`. The example programs of the separate
[bytescan-examples](https://github.com/xiam/bytescan-examples) repository (for
example SQL, Python, Go, JSON, English and Japanese text, ZIP and BMP files,
and MQTT and SSE streams) are examples, catalogued in its README, not scanner
packages. None of them implies a
claim of **universal linguistic understanding**. A natural-language "scanner"
built on this core would be a consumer's choice of rules; the core provides
recognition and classification machinery and nothing about language
semantics. The specification makes no claim that a scanner for any of these is
available out of the box.

## Terminology

- **Rule** — a state-transition function of the form
  `func(Symbol) (next Rule, state State)`. A rule is one state of a finite
  automaton; the returned `next` is the next state. **Current contract**
  (`Rule`, `rule.go`).
- **State** — the outcome of one transition: `StateContinue` (possibly matches,
  needs more input), `StateAccept` (a valid match so far), `StateReject`
  (can no longer match), or `StatePushBack` (exclude the top symbol of the
  token's stack, first the just-read symbol, from this match and show it to
  `next`; a chain of pushbacks excludes more; see
  [Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback)).
  **Current contract** (`State`, `state.go`).
- **Symbol** — a rune enriched with positional flags: `Rune()`, `Size()` (source
  bytes), and `IsEOF`/`IsBOF`/`IsEOL`/`IsBOL`. A rule decides on the rune and the
  flags together. **Current contract** (`Symbol`, `symbol.go`).
- **Token** — the output unit: one classified piece of input, with its
  classification (`TokenType`), its text (`[]rune`), its starting offset, and
  the source `Span` it covers. **Current contract** (`Token`, `token.go`).
- **Lexeme** — a token's text: the input the winning rule matched, as `Text()`
  returns it. **Current contract** (`Token.Text`, `token.go`).
- **In-flight token** — the in-progress match the engine assembles
  speculatively before it commits it. It becomes a returned token only when the
  rules settle on a winning match and the engine commits the cursor over exactly
  that range. **Current contract** (`Scanner.nextToken`, `bytescan.go`).
- **Maximal munch** — the engine selects the *longest* accepted match as the next
  token. **Current contract** (`rulesProcessor.pickBestMatch`, `processor.go`).
- **Cursor** — the positioned text stream the scanner reads through, provided by
  `github.com/xiam/bytescan/cursor`. It owns reading, buffering, positions,
  checkpoints, and replay. The scanner owns no text-stream mechanics.
  **Current contract** (the package doc and `Cursor`, `bytescan.go`).
- **Checkpoint** — a marked logical position on the cursor that the engine uses
  to rewind a token that is abandoned. The engine reuses a fixed pool of
  checkpoint slots (the context window plus the in-flight token) when the
  cursor can re-arm a released checkpoint, instead of allocating one per token.
  **Current contract** (`Scanner.remarkSlot`, `markSlots`, `bytescan.go`).
- **Context window** — a fixed-size ring of the most recent committed tokens
  (three today) whose retained input the engine keeps so `Context` can read the
  text around one of them. **Current contract** (`markWindow`, `bytescan.go`).
- **VM** — the rule engine: `rulesProcessor` (`processor.go`) together with the
  token loop in `Scanner.nextToken` (`bytescan.go`) that feeds it symbols
  and commits its result. The cursor is not part of the VM. In the
  specification "the engine" and "the VM" mean the same thing. **Current
  contract** (`rulesProcessor`, `Scanner.nextToken`).

## Where each section went

Every section heading of the single-file specification, and where its content
is now. A link to an old section of this file lands on its row here.

| Old section | New location |
| --- | --- |
| Status convention | [Status convention](#status-convention) (this file) |
| Purpose and scope | [Purpose and scope](#purpose-and-scope) (this file) |
| Terminology | [Terminology](#terminology) (this file) |
| <a name="vm-design-principles"></a>VM design principles | [design-principles.md: VM design principles](design-principles.md#vm-design-principles) |
| <a name="principle-1-stateless-stack-model"></a>Principle 1: stateless stack model | [design-principles.md: Principle 1](design-principles.md#principle-1-stateless-stack-model) |
| <a name="principle-2-the-vm-checks-boundaries"></a>Principle 2: the VM checks boundaries | [design-principles.md: Principle 2](design-principles.md#principle-2-the-vm-checks-boundaries) |
| <a name="principle-3-rules-are-free"></a>Principle 3: rules are free | [design-principles.md: Principle 3](design-principles.md#principle-3-rules-are-free) |
| <a name="principle-4-the-vm-only-reports-matches"></a>Principle 4: the VM only reports matches | [design-principles.md: Principle 4](design-principles.md#principle-4-the-vm-only-reports-matches) |
| <a name="principle-5-lookahead-belongs-to-the-rule"></a>Principle 5: lookahead belongs to the rule | [design-principles.md: Principle 5](design-principles.md#principle-5-lookahead-belongs-to-the-rule) |
| <a name="known-deviations"></a>Known deviations (KD-1 to KD-8) | [design-principles.md: Known deviations](design-principles.md#known-deviations), now history; each entry has its own anchor there, from [`#kd-1`](design-principles.md#kd-1) to [`#kd-8`](design-principles.md#kd-8) |
| <a name="scannerconsumer-boundary"></a>Scanner/consumer boundary | [consumer-contract.md: Scanner/consumer boundary](consumer-contract.md#scannerconsumer-boundary) |
| <a name="filters"></a>Filters | [layers.md: Filters](layers.md#filters) |
| <a name="consumer-contract"></a>Consumer contract | [consumer-contract.md](consumer-contract.md) |
| <a name="classification"></a>Classification | [consumer-contract.md: Classification](consumer-contract.md#classification) |
| <a name="matched-content-versus-transformed-or-decoded-values"></a>Matched content versus transformed or decoded values | [consumer-contract.md: Matched content](consumer-contract.md#matched-content-versus-transformed-or-decoded-values) |
| <a name="source-spans"></a>Source spans | [consumer-contract.md: Source spans](consumer-contract.md#source-spans) |
| <a name="token-constructors-and-the-manual-constructor-exception"></a>Token constructors and the manual-constructor exception | [consumer-contract.md: Token constructors](consumer-contract.md#token-constructors-and-the-manual-constructor-exception) |
| <a name="byte-versus-rune-units"></a>Byte versus rune units | [consumer-contract.md: Byte versus rune units](consumer-contract.md#byte-versus-rune-units); its byte-stream statement is in [layers.md: Byte reads](layers.md#byte-reads-bytesource) |
| <a name="line-and-column-conventions"></a>Line and column conventions | [consumer-contract.md: Line and column conventions](consumer-contract.md#line-and-column-conventions) |
| <a name="token-lifetime-versus-diagnostic-context-lifetime"></a>Token lifetime versus diagnostic-context lifetime | [consumer-contract.md: Token lifetime](consumer-contract.md#token-lifetime-versus-diagnostic-context-lifetime) |
| <a name="unmatched-input-eof-and-errors"></a>Unmatched input, EOF, and errors | [consumer-contract.md: Unmatched input, EOF, and errors](consumer-contract.md#unmatched-input-eof-and-errors) |
| <a name="execution-contract"></a>Execution contract | [rule-vm-contract.md](rule-vm-contract.md) |
| <a name="competing-rules-evaluated-sequentially"></a>Competing rules, evaluated sequentially | [rule-vm-contract.md: Competing rules](rule-vm-contract.md#competing-rules-evaluated-sequentially) |
| <a name="longest-match"></a>Longest match | [rule-vm-contract.md: Longest match](rule-vm-contract.md#longest-match) |
| <a name="tie-priority"></a>Tie priority | [rule-vm-contract.md: Tie priority](rule-vm-contract.md#tie-priority) |
| <a name="rule-states-and-pushback"></a>Rule states and pushback | [rule-vm-contract.md: Rule states and pushback](rule-vm-contract.md#rule-states-and-pushback) |
| <a name="lookahead-combined-with-a-longer-match"></a>Lookahead combined with a longer match | [rule-vm-contract.md: Lookahead combined with a longer match](rule-vm-contract.md#lookahead-combined-with-a-longer-match) |
| <a name="progress-and-zero-length-behavior"></a>Progress and zero-length behavior | [rule-vm-contract.md: Progress and zero-length behavior](rule-vm-contract.md#progress-and-zero-length-behavior) |
| <a name="eof-handling"></a>EOF handling | [rule-vm-contract.md: EOF handling](rule-vm-contract.md#eof-handling) |
| <a name="registration-validation"></a>Registration validation | [rule-vm-contract.md: Registration validation](rule-vm-contract.md#registration-validation) |
| <a name="returned-error-rollback"></a>Returned-error rollback | [rule-vm-contract.md: Returned-error rollback](rule-vm-contract.md#returned-error-rollback) |
| <a name="speculative-lookahead-limits"></a>Speculative lookahead limits | [resources-and-streams.md: Speculative lookahead limits](resources-and-streams.md#speculative-lookahead-limits) |
| <a name="long-running-streams"></a>Long-running streams | [resources-and-streams.md: Long-running streams](resources-and-streams.md#long-running-streams) |
| <a name="concurrency-boundaries"></a>Concurrency boundaries | [resources-and-streams.md: Concurrency boundaries](resources-and-streams.md#concurrency-boundaries) |
| <a name="panics"></a>Panics | [rule-vm-contract.md: Panics](rule-vm-contract.md#panics) |
| <a name="custom-rule-responsibilities"></a>Custom-rule responsibilities | [rule-vm-contract.md: Custom-rule responsibilities](rule-vm-contract.md#custom-rule-responsibilities) |
| <a name="construction-capabilities"></a>Construction capabilities | [composition.md: Construction capabilities](composition.md#construction-capabilities); its lexical-modes and post-match-action entries are in [layers.md](layers.md), its composition contract in [composition.md: Composition and ambiguity](composition.md#composition-and-ambiguity), its closed and open questions in the [decision log](design-principles.md#closed-design-questions) and [open questions](open-questions.md), and its core/extension boundary in [layers.md: Core/extension boundary](layers.md#coreextension-boundary) |
| <a name="illustrative-scenarios"></a>Illustrative scenarios | [consumer-contract.md: Illustrative scenarios](consumer-contract.md#illustrative-scenarios) |
| <a name="1-keyword-versus-a-longer-identifier--current"></a><a name="1-keyword-versus-a-longer-identifier-current"></a>1. Keyword versus a longer identifier — current | [consumer-contract.md: Illustrative scenarios](consumer-contract.md#illustrative-scenarios), scenario 1 |
| <a name="2-multi-byte-and-multi-line-spans--current"></a><a name="2-multi-byte-and-multi-line-spans-current"></a>2. Multi-byte and multi-line spans — current | [consumer-contract.md: Illustrative scenarios](consumer-contract.md#illustrative-scenarios), scenario 2 |
| <a name="3-context-dependent-delimiter--current"></a><a name="3-context-dependent-delimiter-current"></a>3. Context-dependent delimiter — current | [consumer-contract.md: Illustrative scenarios](consumer-contract.md#illustrative-scenarios), scenario 3; its lexical-modes paragraph is in [layers.md: Lexical modes](layers.md#lexical-modes-modes) |
| <a name="4-unmatched-and-malformed-input--current"></a><a name="4-unmatched-and-malformed-input-current"></a>4. Unmatched and malformed input — current | [consumer-contract.md: Illustrative scenarios](consumer-contract.md#illustrative-scenarios), scenario 4 |
| <a name="motivating-use-cases--not-shipped-scanners"></a><a name="motivating-use-cases-not-shipped-scanners"></a>Motivating use cases — not shipped scanners | [Purpose and scope](#purpose-and-scope) (this file), "Use cases, not shipped scanners" |
| <a name="open-design-questions-index"></a>Open design questions (index) | [open-questions.md](open-questions.md); questions 7, 8, 9, 11, 12 and 13 are closed in the [decision log](design-principles.md#closed-design-questions) |
