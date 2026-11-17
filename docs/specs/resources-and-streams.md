# Resources and streams

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This document states what a scanner holds while it works: the input it retains
for a token, the memory of an open token, the bound that limits both, the
behaviour on a stream of unbounded length, and what is safe to call from
several goroutines.

## Contents

- [Speculative lookahead limits](#speculative-lookahead-limits)
  - [A scanner over a shared cursor](#a-scanner-over-a-shared-cursor)
  - [Memory held by an open token](#memory-held-by-an-open-token)
  - [A longer branch without a bound](#a-longer-branch-without-a-bound)
- [Long-running streams](#long-running-streams)
- [Concurrency boundaries](#concurrency-boundaries)

## Speculative lookahead limits

**Current contract.** While a token is assembled the engine holds a checkpoint
covering everything read for that token, including maximal-munch **lookahead**.
`WithMaxTokenBytes(n)` bounds how many source bytes one token may retain. The
bound **includes** look-ahead: a rule that keeps matching forces the engine to
retain everything it has read for the token so far, and the counter advances with
every speculatively read symbol. A token that would exceed the bound makes `Next`
return an error matching `ErrTokenTooLarge` and wrapping
`textreader.ErrRetentionExceeded`, with no partial progress (the cursor is
rewound; see
[Returned-error rollback](rule-vm-contract.md#returned-error-rollback)). The
default is unbounded. When the bound is set, the cursor's retention budget is
sized to cover the context window plus the in-flight token, so a stream of
tokens that each fit the bound lexes all the way through.

**Evidence.** `TestScannerMaxTokenBytes` (within the bound lexes normally; over the
bound returns `ErrRetentionExceeded` with no token);
`TestScannerMaxTokenBytesAcrossWindow` (consecutive 1-byte tokens under a 1-byte
bound lex fully); `TestScannerRetentionFailureConsumesNothing` (the bound fires with
no progress); `TestScannerErrorIdentities` (the bound matches `ErrTokenTooLarge`).

### A scanner over a shared cursor

**Current contract.** `NewWithCursor(c)` has no per-token bound, and its
function type is unchanged. `NewWithCursorOptions(c, opts...)` accepts the same
options as `NewWithOptions`; with `WithMaxTokenBytes(n)` it enforces the bound
with the same semantics (the same error, look-ahead counted, no partial
progress). The bound is **per scanner**: several scanners on one cursor can each
have their own. The scanner does not change the cursor. The cursor's retention
budget (`textreader.WithMaxRetained`) is set by the caller and is **shared** by
every scanner on the cursor, including the context-window checkpoints of a scanner
that has not called `Release`. A budget too small for a token makes `Next` fail
with `ErrRetentionExceeded` from the cursor before the per-token bound is
reached.

**Evidence.** `TestScannerNewWithCursorOptionsMaxTokenBytes` (a 100-byte run over
an unbounded cursor fails under `WithMaxTokenBytes(16)` and consumes nothing, and
is one token under `NewWithCursor`; under a cursor budget of 8 bytes and a
bound of 64 the cursor's budget fails the token first);
`TestScannerSharedCursorPerScannerMaxTokenBytes`
(two scanners on one cursor with bounds 4 and 16 each fail only past their own
bound); the compile-time assertion in `tests/cursor_test.go` that
`NewWithCursor` is a `func(bytescan.Cursor) *bytescan.Scanner`.

### Memory held by an open token

**Current contract.** A scanner holds memory in proportion to its longest open
token. The rule stack holds one 8-byte `Symbol` per rune read for the token,
and the cursor retains the token's input. For ASCII input that is about 10 B of
live heap per input byte. The stack is the only copy of the symbols (see
[principle 1](design-principles.md#principle-1-stateless-stack-model)).
`WithMaxTokenBytes` (through `NewWithOptions`, or `NewWithCursorOptions` for a
scanner over a shared cursor) is the only hard bound, and it is fail-stop, as
described above; there is no default bound. A rule whose read-ahead can be
rejected (`base.Quoted`, `base.QuotedMultiline`, `base.Raw`,
`base.RawMultiline`, `base.BlockComment`, `base.Decimal`, `base.Float`) can
make the scanner read a long run more than once: the rule reads to the end of the
run and rejects it, a shorter token commits, and the next token reads the rest
of the run again.

**Evidence.** `TestOpenTokenLiveHeap` (live heap at the last rune of a 1 MiB
open `base.Quoted` token is at most 12.7 B per input byte, the measured value
plus 20%); `TestSymbolSize` (`unsafe.Sizeof(Symbol{})` is at most 8).

**Recorded measurement, not a contract.** With the current scanner, a
`base.Quoted` rule on an unterminated quote measured 10.6 B of live heap per
input byte on one open token of 1 MiB and 10.3 B on 4 MiB, with peak heap about
32 B per byte. No committed test reproduces the 4 MiB and peak figures; only
the 1 MiB ceiling above is pinned.

### A longer branch without a bound

**Current contract.** A rule built as in
[Lookahead combined with a longer match](rule-vm-contract.md#lookahead-combined-with-a-longer-match)
settles only when every longer branch is dead. If a longer branch can stay
alive without bound, as `ab.*z` can, the rule reads ahead to the end of input,
or to the first symbol that ends that branch, before the token settles. It does
so even when the final match is short. This cost comes from longest-match
semantics, not from the construction.

- **Worst case: quadratic over repeated tokens.** When every token of a
  repeated-token family reads ahead to the end of input, the total work is
  quadratic in the input length. This is a worst case, not the cost for every
  input: a token whose longer branch dies early reads only that far.
- **Byte bound.** With `WithMaxTokenBytes(n)` the read-ahead counts toward the
  bound. The token fails with an error wrapping
  `textreader.ErrRetentionExceeded` when the read-ahead exceeds `n` bytes, even
  when the final match is short. The failed `Next` consumes nothing, so a retry
  fails the same way.

**Evidence.** `TestLookaheadUnboundedReadAheadHitsByteBound` (`ab(?=c)|ab.*z`
under `WithMaxTokenBytes(64)` on 65 bytes that start with `abc`: two calls to
`Next` each return no token and an error wrapping
`textreader.ErrRetentionExceeded`; on 64 bytes, and on the 65 bytes with the
default unbounded scanner, the first token is `MATCH "ab"`).

**Recorded measurement, not a contract.** Under `ab(?=c)|ab.*z` on repeated
`abc`, every `ab` token reads to the end of input. On 30, 300, and 3,000 bytes
the rule was called 341, 30,401, and 3,004,001 times; 155, 15,050, and
1,500,500 of those calls returned `StatePushBack`. The counts came from a
probe that wrapped the rule of
`TestLookaheadUnboundedReadAheadHitsByteBound` in a call counter; no committed
test reproduces them.

## Long-running streams

**Current contract.** The engine lexes a stream of unbounded length in
constant memory, as long as every token is bounded and the consumer keeps no
token. After a warm-up, the live heap stays flat, the goroutine count does
not change, and the allocations per token stay within a measured budget. This
holds for one scanner with rules from the three text tiers (`rules/core`,
`rules/base` and `rules/special`), for a mode driver (several scanners over one
cursor, with `Release` on every switch), and for a byte stream read through
the byte adapter `bytesource`, with payloads read from the cursor by their
length. The cursor retains exactly the input of the tokens in the context
window: `Context` reaches back to the start of the oldest of them and no
further, and a token before the window is no longer readable. `Release` drops
the retained input to none. A token that stays open is not covered: it holds
memory in proportion to its length (see
[Memory held by an open token](#memory-held-by-an-open-token)), and
`WithMaxTokenBytes` is the only hard bound.

**Evidence.** `TestSoakMixedTiers`, `TestSoakModeDriver` and
`TestSoakByteStream` (`tests/soak_test.go`) lex 256 KiB of generated input per
workload, or 64 KiB under the race detector, and sample after a forced garbage
collection. They fail when the live heap rises along the stream (a
least-squares slope above 1/64 byte of heap per byte of input and more than
64 KiB of growth), when the live heap is more than 1 MiB above the baseline,
when the goroutine count changes, when the allocations per token pass the
budget recorded in the test, when the cursor retains more than the window, or
when input is still retained after `Release`. The slope and the allocations
are asserted only without the race detector. `TestSoakLeakVariants` proves
that each check fails with a deliberate leak. A long run is
`go test -timeout 0 -run Soak ./tests -soak=30m` (a duration or an input size
per workload).

**Current contract — `streamtest`.** The public package `streamtest` checks the
live-heap part of this contract for a user's own scanner, filter or mode driver:
it lexes a generated stream of a byte budget, samples the live heap after a
forced garbage collection, and fails past a ceiling (see its package
documentation). The stream examples under
[streams](https://github.com/xiam/bytescan-examples/tree/main/streams) in bytescan-examples use it.
The soak tests above keep their own generator and checks. **Evidence.**
`TestRunLong`, `TestLeakFails` and `TestLeakReleased`
(`streamtest/streamtest_test.go`); `Example` (`streamtest/example_test.go`).

## Concurrency boundaries

**Current contract.**

- `Next` is **safe for concurrent use** by multiple goroutines. Concurrent
  callers observe a single, deterministic, complete token stream; no token is
  duplicated or lost.
- `AddRule` is **not** safe for concurrent use with `Next`. All rules should be
  added **before** tokenization begins. (The engine guards the rule table with a
  lock and rebuilds its processor when a rule is added, so a concurrent
  `AddRule`+`Next` does not race in memory, but the *policy* is: register first,
  then tokenize. The committed concurrency tests exercise `Next`-only
  concurrency as the supported pattern.)
- A scanner created over a **shared cursor** (via `NewWithCursor`) should call
  `Release` before handing the cursor to another consumer. While the scanner holds
  its context-window checkpoints, a finite-retention cursor keeps that input
  reserved, and the other consumer hits `ErrRetentionExceeded` sooner than
  expected. After `Release`, `Context` returns `ErrPositionOutOfBuffer` until the
  next token commits. `Release` does not close the underlying reader and the
  scanner remains usable. A mode driver does this on every switch; see
  [Lexical modes](layers.md#lexical-modes-modes).

**Evidence.** `TestScannerConcurrentAccess` (4 goroutines, 5000 tokens, exact
count, no error); `TestScannerConcurrentDeterminism` (concurrent `Next` yields a
deterministic stream); `TestPanickingRuleConcurrentDeterminism` (the same
with a rule that panics in every identifier token);
`TestScannerConcurrentAddRuleAndNext` (concurrent `AddRule`+`Next` is
memory-race-clean, run under `-race`);
`TestScannerReleaseHandsOffSharedCursor` (after `Release`, another consumer reads
the remainder through the same finite-retention cursor).
