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

## Closed

These are in the [decision log](design-principles.md#closed-design-questions):

- **7. Panic isolation** — closed 2026-11-12 ([question 7](design-principles.md#question-7)).
- **8. Composition and ambiguity** — closed 2026-11-13 ([question 8](design-principles.md#question-8)).
- **9. State ownership and reset** — closed by principle 1 ([question 9](design-principles.md#question-9)).
- **11. Synthetic-token / progress semantics** — closed by `NewEmptyToken` and filters ([question 11](design-principles.md#question-11)).
- **12. Core/extension seam** — closed by `Source` ([question 12](design-principles.md#question-12)).
- **13. `\r\n` post-sequence line/column** — closed, pinned by `TestCodeNewlinePositions`; the open part is question 5 ([question 13](design-principles.md#question-13)).
