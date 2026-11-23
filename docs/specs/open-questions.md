# Open design questions

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

Every entry here is an **Open design question**: the shape is genuinely
undecided. Each entry names the question, records the constraint an answer
must satisfy, and links to where it arises. It does not choose an answer, and
it does not pretend an answer has owner approval.

Questions keep their numbers, so a reference such as "question 2" stays
meaningful. A closed question moves to the
[decision log](design-principles.md#closed-design-questions) and keeps its
number there.

## Contents

- [Open](#open)
- [Closed](#closed)

## Open

- <a name="question-1"></a>**1. Classification shape** — whether classification stays a flat string,
  becomes a typed value, or gains an orthogonal kind/metadata field. The
  constraint: whatever the shape, a token must remain descriptive and
  self-sufficient (a consumer should not need the original source to
  interpret a token's classification).
  ([Classification](consumer-contract.md#classification))

- <a name="question-2"></a>**2. Token-stream abstraction** — whether the core should ever expose a stable
  token-stream abstraction (for example, an iterator) other than the
  imperative `Next()` loop and the `Source` interface that has the same
  shape. The current `Next()` API is the contract; the question is recorded
  so a future change is a deliberate one.
  ([Scanner/consumer boundary](consumer-contract.md#scannerconsumer-boundary),
  [Filters](layers.md#filters))

- <a name="question-3"></a>**3. Decoded/transformed values** — the shape of an optional post-match action
  that attaches a decoded or transformed value to a token. The constraint:
  the VM performs no action (principle 4), so the action lives in a layer
  above `Next()` and acts on the token the VM reports. The question is the
  shape of that layer, not whether the VM runs actions.
  ([Matched content](consumer-contract.md#matched-content-versus-transformed-or-decoded-values),
  [Post-match actions](layers.md#post-match-actions))

- <a name="question-4"></a>**4. Grapheme-cluster coordinates** — whether the core should also expose a
  grapheme-cluster coordinate (a single user-perceived character may be
  several rune code points). Columns are rune columns today; a consumer that
  needs display-width or grapheme behavior must derive it from the span
  itself. ([Byte versus rune units](consumer-contract.md#byte-versus-rune-units))

- <a name="question-5"></a>**5. Line-break convention** — the cursor's positions after `\n`, `\r\n` and a
  lone `\r` are Current contract, pinned by `TestCodeNewlinePositions`.
  Whether the toolkit promises them independently of the cursor, or lets a
  consumer choose another convention, is undecided; the cursor's behavior is
  the current authority. (This absorbs the former question 13; see
  [Closed](#closed).)
  ([Line and column conventions](consumer-contract.md#line-and-column-conventions))

- <a name="question-6"></a>**6. Configurable no-match behavior** — whether the `UNKNOWN` fallback should
  be configurable or replaceable, for example a consumer-supplied "on no
  match" handler that can emit a different token or raise. Today the VM's
  fallback is fixed to one-rune `UNKNOWN`. The constraint: the VM keeps
  reporting `UNKNOWN` when no rule matches (principle 4), so a handler acts
  on that report in a layer above `Next()`, where `Skip` and `WithType`
  already work.
  ([Unmatched input, EOF, and errors](consumer-contract.md#unmatched-input-eof-and-errors),
  [No-match handling and recovery](layers.md#no-match-handling-and-recovery))

- <a name="question-10"></a>**10. Recovery** — whether and how the toolkit offers structured recovery (a
  consumer-facing "no match" hook, a recoverable token stream) instead of
  the fixed one-rune `UNKNOWN` fallback. The constraint is that of
  question 6: the recovery acts on the VM's report in a layer above
  `Next()`. The open question is that layer's shape.
  ([No-match handling and recovery](layers.md#no-match-handling-and-recovery))

- <a name="question-14"></a>**14. Trailing incomplete UTF-8 at EOF** — there is no committed test
  asserting the exact `Text()`/span relationship for a trailing incomplete
  multi-byte sequence at EOF (a leading byte with no continuation bytes
  before the end of input). The cursor's carry/flush logic handles it by
  crediting the incomplete bytes as a replacement rune, but this case is not
  pinned by a named test and is recorded as a question rather than a
  promise. (Scenario 4 under
  [Illustrative scenarios](consumer-contract.md#illustrative-scenarios).)

- <a name="question-15"></a>**15. Declarative frontends** — whether the toolkit
  should eventually offer a declarative frontend, for example a schema- or
  table-driven way of declaring rules, and what shape it takes. The
  constraint: a frontend compiles down to the same engine and the same
  `bytescan.Rule` values, so the VM and its
  [design principles](design-principles.md#vm-design-principles) do not change.
  ([Purpose and scope](core-contract.md#purpose-and-scope))

Questions 16 to 30 come from the contract for
[resumable lexing](resumable-lexing.md). They share one constraint: a
consumer that reads a stream and never goes back pays nothing for an answer,
in memory for each token or in the speed of `Next()`. The owner answered
questions 16 to 21, 23 to 26 and 28 to 30 on 2026-11-18; they are under
[Closed](#closed). Questions 22 and 27 stay open, deferred by the owner.

- <a name="question-22"></a>**22. State that is not a stack of mode names** — some
  formats need more than a stack: a heredoc terminator or a raw-string
  delimiter (one value), a field whose length is stored in the data
  (`bytesource.Fields`), or a scanner nested in another scanner's token. The
  owner deferred one extra value slot until a real example needs it. The
  question is which of these the lexer with state will support, and how. The
  constraint: the state stays a plain value that bytescan can copy and
  compare without the caller's help.
  ([The mode state](resumable-lexing.md#the-mode-state))

- <a name="question-27"></a>**27. Bounded work for one edit** — an edit that changes
  the mode state (an opening quote typed at the top of a large file) can
  have no sync point before the end of the text. The question is whether the
  lexer with state can stop early and report "the tokens from offset X on
  are not valid yet", and continue later. The constraint: the implementer
  never reads a token that is not valid as if it were valid, and the final
  tokens are the same as without the bound. **Deferred by the owner**
  (2026-11-18): it is not part of the milestone of the lexer with state, and
  an edit with no sync point re-lexes to the end of the text. The reason: the
  examples of that milestone hold small texts, optimization comes after the
  final feature set, and a bound touches the change, the queries and the
  errors, so it is designed once, with a real case of a large text.
  ([What the cases show](resumable-lexing.md#what-the-cases-show),
  [A bound on the work of one edit](resumable-lexing.md#a-bound-on-the-work-of-one-edit))

  **What the measurement of 2026-11-20 showed** (the statelex report under
  `internal-docs/benchmarks/`, which is not exported; benchmark
  `BenchmarkEdit` in `statelex`; [The statelex report](measurement.md#the-statelex-report)).
  On texts of growing sizes under rule sets A and B, the latency of a key
  typed, a backspace and a deleted selection did not grow in proportion to
  the size: it grew only as fast as the visited tokens, with the logarithm of
  the size. The opening quote at the start of the text under rule set B, and
  its removal, re-lexed the whole text, and their latency grew in proportion
  to the size, close to the latency of loading the same text. `SetText` also
  grew in proportion to the size, from its comparison of the whole text, not
  from its re-lex. So the edit with no sync point is the only edit shape whose
  re-lex grows with the size. The measurement does not choose a bound, and
  the question stays open.

Question 31 comes from the design of the [table compiler](table-compiler.md).

- <a name="question-31"></a>**31. A table for pure core rules** — whether
  bytescan compiles a rule set built only from `rules/core` to a table of
  states, and in which form: one table for each composed rule, or one table
  for the whole rule set of a scanner. The constraint: the tokens, the
  symbols read for each token and the errors stay those of the engine, no
  exported name changes, and a rule that is not built only from `rules/core`
  keeps the engine. The design, its proof of equal tokens, its cost and its
  recommendation are in [Table compiler](table-compiler.md); the proposal is
  in the [decision log](design-principles.md#owner-decisions), marked
  "proposed". The owner has not decided.

## Closed

These are in the [decision log](design-principles.md#closed-design-questions):

- **7. Panic isolation** — closed 2026-11-12 ([question 7](design-principles.md#question-7)).
- **8. Composition and ambiguity** — closed 2026-11-13 ([question 8](design-principles.md#question-8)).
- **9. State ownership and reset** — closed by principle 1 ([question 9](design-principles.md#question-9)).
- **11. Synthetic-token / progress semantics** — closed by `NewEmptyToken` and filters ([question 11](design-principles.md#question-11)).
- **12. Core/extension seam** — closed by `Source` ([question 12](design-principles.md#question-12)).
- **13. `\r\n` post-sequence line/column** — closed, pinned by `TestCodeNewlinePositions`; the open part is question 5 ([question 13](design-principles.md#question-13)).
- **16. Complete tokens and the true end of input** — closed 2026-11-18 ([question 16](design-principles.md#question-16)).
- **17. The end of the held text** — closed 2026-11-18 ([question 17](design-principles.md#question-17)).
- **18. Positional flags at a restart and at a sync point** — closed 2026-11-18 ([question 18](design-principles.md#question-18)).
- **19. Starting a scan at a position and in a state** — closed 2026-11-18 ([question 19](design-principles.md#question-19)).
- **20. The lookahead record** — closed 2026-11-18 ([question 20](design-principles.md#question-20)).
- **21. The mode stack and the `modes` package** — closed 2026-11-18 ([question 21](design-principles.md#question-21)).
- **23. The edit request, the change, the constructor and the names** — closed 2026-11-18 ([question 23](design-principles.md#question-23)).
- **24. Which tokens count as unchanged in a change** — closed 2026-11-18 ([question 24](design-principles.md#question-24)).
- **25. Lines, columns and rune offsets after an edit** — closed 2026-11-18 ([question 25](design-principles.md#question-25)).
- **26. Edit units** — closed 2026-11-18 ([question 26](design-principles.md#question-26)).
- **28. Reading the lexer state** — closed 2026-11-18 ([question 28](design-principles.md#question-28)).
- **29. Errors while re-lexing** — closed 2026-11-18 ([question 29](design-principles.md#question-29)).
- **30. Packaging, the token store and the build order** — closed 2026-11-18 ([question 30](design-principles.md#question-30)).
