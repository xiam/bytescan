# Consumer contract

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This is what a consumer actually receives from `Next()`, and the guarantees that
hold for it. How the engine chooses each token is in the
[rule and VM contract](rule-vm-contract.md).

## Contents

- [Scanner/consumer boundary](#scannerconsumer-boundary)
- [Classification](#classification)
- [Matched content versus transformed or decoded values](#matched-content-versus-transformed-or-decoded-values)
- [Source spans](#source-spans)
- [Token constructors and the manual-constructor exception](#token-constructors-and-the-manual-constructor-exception)
- [Byte versus rune units](#byte-versus-rune-units)
- [Line and column conventions](#line-and-column-conventions)
- [Token lifetime versus diagnostic-context lifetime](#token-lifetime-versus-diagnostic-context-lifetime)
- [Unmatched input, EOF, and errors](#unmatched-input-eof-and-errors)
- [Illustrative scenarios](#illustrative-scenarios)

## Scanner/consumer boundary

**Current contract.** The scanner owns lexical policy and nothing else: rule state
transitions, competing-rule evaluation, maximal-munch selection,
registration-order tie breaking, the `UNKNOWN` fallback, and token
construction. It owns **no** text-stream mechanics. **Reconciled with
principle 1.** "Rule state transitions" means that the VM applies the
transitions a rule returns. The state itself is the rule's: it is the `Rule`
value the rule returns, and the VM holds it only as per-token bookkeeping.

Reading, buffering, positions, and replay are owned by the positioned cursor
(`github.com/xiam/bytescan/cursor`). The cursor decodes UTF-8, tracks the logical
position, retains the input a checkpoint needs, and restores a marked position
exactly.

Downstream of the scanner, the consumer owns everything else: deciding what a
`TokenType` means, building a grammar or parser, constructing an AST, and
interpreting or executing. The core makes none of those decisions and exposes no
API for them. A consumer that wants parsing gets tokens and writes a parser
over them. Anything that depends on earlier tokens lives in a layer above
`Next()`; see [Layers above `Next()`](layers.md).

**Intended capability.** The boundary is expected to stay fixed as the toolkit
grows: new rule tools change *how a consumer describes lexical policy*, not
*what the scanner returns*. The output unit remains a descriptive token.

**Open design question 2.** Whether the core should ever expose a stable
token-stream abstraction other than the `Next()` loop; see
[open questions](open-questions.md#question-2).

## Classification

**Current contract.** A token carries exactly one string-valued
classification, `TokenType`. `Type()` returns it; it is whatever string the
consumer registered the rule under. The fallback for input no rule matched is
the constant `TokenTypeUnknown` (`"UNKNOWN"`). There is **no** separate kind
API, no hierarchy, and no built-in set of types — the type space is the
consumer's.

**Intended capability.** The toolkit is intended to let a consumer express both a
broad category and a specific kind (for example, a *literal* that is
specifically a *string literal*), and to carry structured metadata alongside the
type. **This is intent only.** No broad/specific hierarchy, no metadata shape,
and no API for either has been approved. A future API must not be described as
existing.

**Open design question 1.** The shape of classification; see
[open questions](open-questions.md#question-1).

## Matched content versus transformed or decoded values

**Current contract.** `Text()` returns the **matched content**: the runes the
winning rule accepted, exactly as read, as a `string` (from `[]rune`). The scanner
does **not** transform or decode the content. It does not unescape, it does not
parse a number into an `int`, it does not strip quotes, and it does not apply any
language-specific semantics. A `NUMBER` token's `Text()` is the digit string,
not a numeric value. Any decoding is the consumer's job.

`Len()` returns the rune count of `Text()`. `Offset()`, `RuneOffset()`,
`ByteOffset()`, `Line()`, and `Column()` report the **start** position of the
match; `Span()` reports the full range.

**Open design question 3.** Whether the core should offer an optional
post-match *action* hook that can attach a decoded or transformed value to a
token. It is recorded as an intended capability with an open shape under
[Post-match actions](layers.md#post-match-actions); see
[open questions](open-questions.md#question-3). As of this document, no such
hook exists and the token carries matched content only.

## Source spans

**Current contract.** Every token the scanner produces carries a `Span` — a
**half-open** range `[Start, End)` in the source's own coordinates. Both ends
expose byte offset, rune offset, line, and rune column:

- `Start().ByteOffset()` / `End().ByteOffset()` — zero-based byte offsets.
- `Start().RuneOffset()` / `End().RuneOffset()` — zero-based rune offsets.
- `Start().Line()` / `End().Line()` — **1-based** line numbers.
- `Start().Column()` / `End().Column()` — **0-based rune columns**.
- `Span().Bytes()` / `Span().Runes()` — widths in bytes and runes.

`Token` shorthands `ByteOffset()`, `RuneOffset()`, `Line()`, `Column()` all
report the **start**. `Offset()` is the long-standing accessor that counts
**runes** and equals `RuneOffset()`; use `ByteOffset()` when bytes are what you
need. `Len()` equals `Span().Runes()` for a scanner-produced token. On a
byte-mode cursor every rune is one byte, so these coordinates are file
positions; see [Byte versus rune units](#byte-versus-rune-units).

Spans are **committed, not speculative**: a span covers exactly the winning
match, never the furthest point the rules read ahead to. A rule that looks ahead
past its match does not shift the next token's span.

**Evidence.** `TestScannerTokenSpans` (ASCII, multi-byte UTF-8, newline: every
byte of the input covered by exactly one token, in order, with `Len ==
Span().Runes` and `Offset == RuneOffset`); `TestScannerSpanIsCommittedNotSpeculated`
(a rule that reads one symbol past its match still commits a 1-byte span per
token); `TestScannerRuneReaderSourceSpans` (rune-only source yields correct byte,
rune, line, and column coordinates). Source: the commit block of
`Scanner.nextToken` (`bytescan.go`) and `Span`/`Pos` in `bytescan/cursor`.

## Token constructors and the manual-constructor exception

**Current contract.** A `Token` is a plain value; nothing forces a token to
carry a span. There are four exported constructors, and they split into two
kinds:

- `NewToken(typ, text []rune, offset int)` and
  `NewTokenFromString(typ, text, offset)` are the **manual constructors**. They
  record a **rune offset only** and leave the span empty. For such a token
  `Offset()` and `RuneOffset()` return the offset the caller passed, but
  `ByteOffset()`, `Line()`, and `Column()` all report **zero** (the empty span's
  start), and `Span()` is the zero `Span`. These are the "legacy" constructors a
  consumer or test can call to fabricate a token without a cursor.
- `NewTokenWithSpan(typ, text []rune, span)` carries a **full source span**; its
  `Offset()`/`RuneOffset()` are taken from the span's start, so the compatible
  accessor and the span agree.
  `NewEmptyToken(typ, pos)` is the same with no text and the empty span at
  `pos`, for a synthetic token a filter inserts (see
  [Filters](layers.md#filters)).

A **scanner-produced** token — one returned by `Next()` — always carries a real
span covering the committed match, so its byte, rune, line, and column
coordinates are all meaningful. The manual-constructor exception is only that a
consumer-built token may legitimately have a rune offset with no span, and the
byte/line/column accessors then read zero rather than error. This is a
documented property of the value, not a bug.

**Evidence.** `NewToken`/`NewTokenFromString`/`NewTokenWithSpan` and the
accessor doc comments in `token.go`, `NewEmptyToken` in `source.go`
(`TestNewEmptyToken`); the committed-span path of `Scanner.nextToken`
(`bytescan.go`).

## Byte versus rune units

**Current contract.** Both unit systems are first-class and always consistent
within a token:

- **Byte offsets** address the source as a byte stream. They are what you need
  to slice the original `[]byte`, to feed a byte-oriented consumer, or to
  round-trip into the source.
- **Rune offsets** address the source as a sequence of decoded runes. `Offset()`
  and `RuneOffset()` are rune counts; `Len()` is a rune count; columns are rune
  columns.

A multi-byte rune occupies several bytes but one rune and one column. `héllo`
is 5 runes in 6 bytes and 5 columns. `Span().Bytes()` and `Span().Runes()` give
the two widths; they differ exactly by the extra bytes of multi-byte runes.

**Current contract — byte cursor.** On a byte-mode cursor
(`bytesource.NewCursor`, which is `cursor.NewByteReader`) each byte of the
input is one rune of size 1. Byte and rune offsets are both the file offset,
`Span().Bytes()`, `Span().Runes()` and `Len()` are all the length in bytes,
columns count bytes since the last 0x0A, and `WithMaxTokenBytes` counts one
for each byte. A cursor built with `cursor.NewRuneReader` over
`bytesource.NewReader` encodes the runes as UTF-8 instead: its byte units count
a byte of 0x80 or above as two bytes. See
[Byte reads](layers.md#byte-reads-bytesource). **Evidence.**
`TestHighBytePositions`, `TestHighByteGuard` and `TestByteLineColumn`
(`bytesource/bytemode_test.go`).

**Open design question 4.** Whether the core should also expose a
grapheme-cluster coordinate. Columns are currently rune columns, not grapheme
columns; see [open questions](open-questions.md#question-4).

## Line and column conventions

**Current contract.** Lines are **1-based** (the first line is line 1). Columns
are **0-based rune columns** (the first rune of a line is column 0). A `\n` ends
the current line; the next rune is on the next line at column 0. `\r` does not,
by itself, start a new line in the column accounting the way `\n` does — the
cursor's line/column tracking is defined by `bytescan/cursor` and is the authority
here.

**Evidence.** `TestScannerTokenSpans` asserts a token on the second line has
`Line()==2, Column()==0`. `TestScannerRuneReaderSourceSpans` asserts a token after
a newline has `Line()==2, Column()==0` and a mid-line token has the expected
column.

**Current contract — after `\r\n`.** A token after `\r\n` starts the next
line at column 0, and a token after a lone `\r` stays on its line. These are
the cursor's positions; `base.Newline` matches `\r\n` as one token and does
not change them. **Evidence.** `TestCodeNewlinePositions`
(`rules/base/code_test.go`).

**Open design question 5.** Whether the toolkit should promise this convention
independently of the cursor, or let a consumer choose another; see
[open questions](open-questions.md#question-5). The cursor's behavior is the
current authority.

## Token lifetime versus diagnostic-context lifetime

**Current contract.** There are two distinct lifetimes:

- **The token value is durable.** A `Token` is a self-contained value. Its
  `Span`, offsets, line/column, `Type()`, and `Text()` are immutable and remain
  correct no matter how many further tokens the consumer reads. The span is a
  value copied out of the cursor at commit time; it is not a reference into
  cursor state.
- **Diagnostic context is bounded.** `Context(tok, before, after)` returns the
  source text around `tok`, including input that *precedes* it. This is a
  diagnostic aid, and it is only available while `tok` is inside the engine's
  context window (the three most recent committed tokens, `markWindow`). Once
  enough later tokens commit and push `tok` out of that window, its retained
  input is released and `Context` returns
  `cursor.ErrPositionOutOfBuffer`. A call that commits nothing — a failed
  `Next`, or the final EOF probe — releases nothing, so a token's context
  survives a failed call and an EOF probe. `before` and `after` count bytes.
  On a byte-mode cursor the context is the raw input bytes, not the runes of
  `Text()`: `bytesource.Text` of it compares with `Text()` (evidence:
  `TestByteContext`, `bytesource/bytemode_test.go`).

So: the *facts about* a token (its span, type, text) live forever; the *raw
text around* a token lives only as long as the window retains it. A consumer
that needs surrounding text for a token much later must have captured it with
`Context` while the token was still in the window.

**Mark ownership.** The engine keeps a fixed pool of checkpoint slots — one per
token the window retains, plus the token currently being assembled — rather
than allocating a fresh checkpoint for every token. When the cursor can re-arm
a released checkpoint (the `Remark` capability of `cursor.Reader`), a
committed token's slot is released and the next token re-arms that same slot in
place, so the number of live checkpoints is bounded by the window and
steady-state tokenization allocates no checkpoint. A failed `Next` and the EOF
probe reset and release only their own in-flight slot, so earlier committed
context is left intact and a mark is never re-armed while it is still active. A
cursor that does not expose the capability (see `NewWithCursor`) keeps the
older behavior of allocating a checkpoint per token. The window's width,
eviction order, and every behavioral promise above are unchanged by the reuse.

**Evidence.** `TestScannerContext` (a token stays readable across one more token,
then falls out and returns `ErrPositionOutOfBuffer`);
`TestScannerContextReachesPrecedingTokens` (`before` reaches back across preceding
tokens and clamps rather than errors when asked for more than is retained);
`TestScannerContextSurvivesFailedNext` and `TestScannerContextSurvivesEOFProbe`
(a non-committing call releases nothing);
`TestScannerMarkReuseLongSequence` (a long stream lexes under a tight finite
retention budget, so no mark is leaked and registrations stay bounded);
`TestScannerMarkEvictionExact` (exactly the three most recent tokens remain
readable, every earlier one is evicted, and the retained region is the window);
`TestScannerMarkReuseAfterFailures` (repeated failed `Next` calls commit nothing
and the prior context survives, then the stream lexes on);
`TestScannerMarkReuseAfterRelease` (`Release` drops the window and later tokens
re-arm it); `TestScannerMarkFallbackWithoutRemark` (an injected cursor without the
capability lexes identically but still allocates a checkpoint per token).

## Unmatched input, EOF, and errors

**Current contract.**

- **Unmatched input** is never silently dropped. If no rule accepts a symbol, the
  engine emits a **single-rune `UNKNOWN` token** for that symbol and advances by
  exactly one rune. Tokenization always makes progress: every successful `Next()`
  consumes at least one rune, and the `UNKNOWN` fallback guarantees that.
- **EOF** is returned as `io.EOF` from `Next()` once the stream is fully consumed
  and no further token can be produced. A rule that still needs input at EOF
  (an unterminated construct) contributes only the accepts it recorded
  earlier; without one, the next token is a single-rune `UNKNOWN` token. See
  [EOF handling](rule-vm-contract.md#eof-handling).
- **Cost of a rule that never settles.** Such a rule keeps each token open to
  the end of input, so every token it leaves unmatched costs one read to the
  end of input: lexing n runes costs O(n²) symbol steps. The token start is
  marked, so the cursor retains every byte from that start; under the default
  unbounded `WithMaxTokenBytes` all input is retained. `WithMaxTokenBytes`
  bounds both: a token that grows past the bound fails (see
  [Speculative lookahead limits](resources-and-streams.md#speculative-lookahead-limits)).
- **Errors other than EOF** indicate a problem with the underlying reader or an
  unrecoverable engine state (for example a token larger than the configured
  retention bound). On a returned error, `Next` has made **no partial progress**:
  the cursor is rewound to the token start, so a retry starts over rather than
  resuming mid-token. See
  [Returned-error rollback](rule-vm-contract.md#returned-error-rollback).
- **Matching an error.** A failure that the root package decides matches an
  exported sentinel with `errors.Is` (`errors.go`). Package-owned messages start
  with `bytescan:`, and a wrapped message keeps its detail after the sentinel's
  text.

  | Sentinel | Returned by | When |
  |----------|-------------|------|
  | `ErrDuplicateRule` | `AddRule` | the `TokenType` already has a rule |
  | `ErrEmptyRuleType` | `AddRule` | the `TokenType` is `""` |
  | `ErrNilRule` | `AddRule` | the rule is `nil` |
  | `ErrNoRules` | `Next` | no rule is registered |
  | `ErrTokenTooLarge` | `Next` | a token would exceed `WithMaxTokenBytes`; the same error also matches `cursor.ErrRetentionExceeded` |
  | `ErrInternal` | `Next` | a defensive engine check fails; not expected |
  | `ErrNilToken` | `Context` | the token is `nil` |
  | `ErrContextUnsupported` | `Context` | the cursor does not expose retained text |

  Errors that the package does not decide pass through with their identity
  and get no root sentinel:

  - The end of input is `io.EOF` itself, not a wrapped error, so
    `err == io.EOF` holds, before and after `Release`.
  - An error from the caller's reader or cursor, and an error from committing
    the cursor, is wrapped with `%w`, so `errors.Is` matches the original
    error.
  - A token that exceeds the cursor's own retention budget
    (`cursor.WithMaxRetained`) matches `cursor.ErrRetentionExceeded`
    and not `ErrTokenTooLarge`.
  - An error from the cursor's `Context`, such as
    `cursor.ErrPositionOutOfBuffer`, is returned unchanged.
  - `Release` cannot fail, and the scanner stays usable after it.

**Evidence.** `TestScannerErrorIdentities` (each sentinel, plain `io.EOF` before
and after `Release`, both retention failures, an injected cursor error, and a
`Context` error); `TestScannerNextAfterRelease`;
`TestScannerProcessor` "No Matching Rules (produces UNKNOWN)" (each
unmatched rune is its own `UNKNOWN` token); `TestScannerEdgeCasesAtEOF` (a
settled rule yields `io.EOF`; empty input yields `io.EOF` immediately);
`TestScannerProcessor` "Rule Never Settles (UNKNOWN per rune)" (a rule that is
still active at EOF and never accepts yields one `UNKNOWN` rune per input rune,
then `io.EOF`); `TestStillActiveAtEOFRetiresRule` (a still-active sibling does
not stop another rule's match);
`TestScannerZeroLengthMatchPrevention` (a rule that never advances still yields one
`UNKNOWN` rune per input rune, never an infinite loop; it reaches the
zero-length branch of `nextToken`);
`TestScannerRetentionFailureConsumesNothing` (a rejected token consumes nothing and
a retry is identical).

**Open design question 6.** Whether the `UNKNOWN` fallback should be
configurable or replaceable. Today it is fixed to one-rune `UNKNOWN`; see
[open questions](open-questions.md#question-6).

## Illustrative scenarios

These are **prose examples** to make the contract concrete, not new derived scanner
packages and not shipped scanners. Each is labeled **current** (it is what the
engine does today, mapped to a named test or source location) or **aspirational**
(it is what an intended capability would make possible). All four are current.
Where a current scenario has a coverage gap, the gap is recorded as a question.

### 1. Keyword versus a longer identifier — current

Input: `if identifier` with a keyword rule for the literal `if`, an identifier
rule for a letter followed by letters/digits, and a whitespace rule.

Output: `IF "if"`, then `WHITESPACE " "`, then `IDENTIFIER "identifier"`.

The `if` and `identifier` rules both accept the text `if` (the identifier rule
accepts it as a valid identifier), a **tie at length 2**. The keyword rule was
registered first, so registration-order tie priority gives it the token. A longer
identifier that the keyword rule cannot match (`identifier`) is won outright by
the identifier rule under maximal munch.

**Mapped to:** `TestScannerProcessor` "Rule Priority (Keyword vs Identifier)" and
"Rule Priority Reversed (Identifier wins)". This is the canonical demonstration
that ties are decided by registration order, not by type, and that a longer match
outranks a shorter one ([Tie priority](rule-vm-contract.md#tie-priority)).

### 2. Multi-byte and multi-line spans — current

Input: `héllo 世界\n42` with a word rule (Unicode letters), a number rule, a
newline rule, and a space rule.

Output: the word `héllo` is **5 runes in 6 bytes** (the `é` is two bytes); it
starts at line 1, column 0, and its span is `Bytes()==6, Runes()==5`. The `42`
number starts on **line 2, column 0**, after the newline. Every byte of the input
is covered by exactly one token, in order, with each token starting exactly
where the previous one ended.

**Mapped to:** `TestScannerTokenSpans` (the byte/runes/line/column assertions,
including `Len == Span().Runes` and the line-2 column-0 number) and
`TestScannerRuneReaderSourceSpans` (the same coordinates through a rune-only
source, where the cursor re-encodes each rune to UTF-8 so byte offsets stay
meaningful). The positions after `\r\n` are under
[Line and column conventions](#line-and-column-conventions).

### 3. Context-dependent delimiter — current

Input: `  # not a comment\n# a comment` with a comment rule that matches `#`
**only at the beginning of a line** (using `IsBOL`), a whitespace rule, a
literal `#` fallback rule, and an identifier rule.

Output: the first `#` (not at line start) is **not** a comment — it is matched by
the fallback literal `#` rule and the following words are identifiers. The second
`#` **is** at the beginning of a line, so the comment rule accepts it and
consumes through end of line (pushing back the EOF symbol, or the `\n` when a
newline follows, so a newline stays its own token). Result: `WHITESPACE`,
`HASH "#"`, identifiers `not`, `a`, `comment`, `WHITESPACE "\n"`, then
`COMMENT "# a comment"`.

This is the concrete, shipped demonstration of **context sensitivity**: the same
symbol `#` classifies differently depending on its positional context (BOL or
not), using the `Symbol` flags rather than any mode machinery. Context carried
across tokens is a lexical mode, a layer above `Next()`; see
[Lexical modes](layers.md#lexical-modes-modes).

**Mapped to:** `TestScannerProcessor` "Context-Aware Rules (BOL)".

### 4. Unmatched and malformed input — current

**Unmatched.** Input: `abc` with only an integer rule registered. No rule
accepts a letter, so the engine emits one `UNKNOWN` token per rune: `UNKNOWN
"a"`, `UNKNOWN "b"`, `UNKNOWN "c"`. Nothing is dropped; progress is one rune per
token. **Mapped to:** `TestScannerProcessor` "No Matching Rules (produces UNKNOWN)".

**Malformed UTF-8.** Input containing a byte that is not valid UTF-8 (for example
a stray `0xFF`) is **not** an error. The cursor decodes each invalid byte as the
single replacement rune U+FFFD (size 1, one byte, one rune, one column) and
continues. The token's **span remains accurate** — its byte offsets still point
into the original source — but `Text()` is **not** byte-for-byte: the original
invalid byte is represented in `Text()` as U+FFFD, which re-encodes to a
different (three-byte) sequence. In other words: **positions are faithful; text is
lossy** for malformed UTF-8. A consumer that must preserve the exact bytes of a
malformed region should work from the span (byte offsets into the source), not
from `Text()`. **Mapped to:** `TestReadSymbolSizes` (`symbol_internal_test.go`:
an invalid byte is read without an error as one symbol of size 1). The U+FFFD
text and the span of the resulting token are stated in the godoc of
`Scanner.Next` and come from the UTF-8 decoding of `bytescan/cursor`; **no test
yet**.

**Rune-only source.** A source that implements only `io.RuneReader` (no
`io.Reader`) is read rune by rune, and the cursor re-encodes each rune to UTF-8
in its buffer. Byte offsets therefore describe the **UTF-8 encoding of the rune
stream the caller's reader represents**, not any physical byte layout the caller
might have had. Coordinates (byte, rune, line, column) are all still consistent
and correct. **Mapped to:** `TestScannerRuneReaderSourceSpans` and the
`NewRuneReader` path in `bytescan/cursor`.

**Coverage gap (question 14).** There is no committed test asserting the exact
`Text()`/span relationship for a **trailing incomplete** multi-byte sequence at
EOF; see [open questions](open-questions.md#question-14).
