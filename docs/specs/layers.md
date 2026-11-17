# Layers above `Next()`

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

The VM keeps no state across tokens
([principle 1](design-principles.md#principle-1-stateless-stack-model)) and
performs no action ([principle 4](design-principles.md#principle-4-the-vm-only-reports-matches)).
Everything that depends on earlier tokens, or that acts on a token, lives in
a layer above `Next()`: a filter, a mode driver, or a driver that reads fields
whose length is stored in the data. This document states the public seam for
those layers and the packages built on it.

## Contents

- [Filters](#filters)
- [Lexical modes (`modes`)](#lexical-modes-modes)
- [Byte reads (`bytesource`)](#byte-reads-bytesource)
- [Core/extension boundary](#coreextension-boundary)
- [Post-match actions](#post-match-actions)
- [No-match handling and recovery](#no-match-handling-and-recovery)

## Filters

**Current contract.** The layer above `Next()` has a public seam. `Source` is
the interface `Next() (*Token, error)`: each call returns a token and a nil
error, or a nil token and an error, with `io.EOF` itself at the end of the
stream. `*Scanner` satisfies it. `Skip(src, types...)` is a `Source` that
drops the named types and passes every other token and every error unchanged;
it allocates nothing per token. `(*Token).WithType` relabels a token with its
text and span unchanged, and `NewEmptyToken` builds a synthetic token with no
text at a position. These helpers do not change what `Next()` returns. Whether
the core adds a different token-stream abstraction stays open
([question 2](open-questions.md#question-2)).

**Evidence.** `source.go`, `token.go`; `TestScannerIsSource`, `TestSkip`,
`TestSkipOverScanner`, `TestSkipAllocations`, `TestWithType` and
`TestNewEmptyToken` (`tests/source_test.go`); `ExampleSkip`,
`ExampleToken_WithType` and `ExampleNewEmptyToken`
(`example_source_test.go`).

**Current contract — synthetic tokens.** A token that stands for no input,
such as an inserted semicolon or an `INDENT`, comes from a filter, never from
the VM. The VM refuses a zero-length match
([Progress and zero-length behavior](rule-vm-contract.md#progress-and-zero-length-behavior)),
so every token `Next()` returns covers a non-empty source range, and a
token with empty text is one a filter made. A filter builds it with
`NewEmptyToken`. This was open design question 11; see the
[decision log](design-principles.md#question-11). Evidence:
`TestNewEmptyToken`, `ExampleNewEmptyToken`; outside this repository, the Go
example inserts `SEMICOLON` tokens (`semicolons` in
[go](https://github.com/xiam/bytescan-examples/tree/main/go)) and the Python example inserts `INDENT` and `DEDENT`
tokens (`lineFilter` in [python](https://github.com/xiam/bytescan-examples/tree/main/python)), each pinned by its
`TestGolden` in bytescan-examples.

## Lexical modes (`modes`)

**Current contract.** A consumer switches the active rule set on position or a
prior token (for example, inside a string versus outside one), so the same
symbol can classify differently in different states. The `modes` package does
it above `Next()`: a `modes.Driver` holds one active scanner out of several built
over one shared cursor (`NewWithCursor` or `NewWithCursorOptions`). `Use`
switches the active scanner and calls `Release` on the outgoing one, and does
nothing when the scanner is already active; a `SwitchFunc` picks the next scanner
after each token. `Next` and `Use` hold one mutex, so no two scanners on the
cursor run at the same time. `Driver` is a `Source`: it returns the tokens and
errors of the active scanner unchanged, with `io.EOF` itself at the end. A switch
between warmed scanners allocates nothing. `modes.Cache` builds one scanner per key
on first use and never drops one. Evidence: `TestDriverSwitch`,
`TestDriverUseReleases`, `TestDriverSwitchAllocs`, `TestDriverConcurrentNext`,
`TestCache` and `Example` (`modes/`); outside this repository, the drivers of
the Lua, TypeScript and Ruby examples, of the five binary examples and of every
stream example with more than one scanner run on it
([lua](https://github.com/xiam/bytescan-examples/tree/main/lua),
[streams/sse/ssedriver](https://github.com/xiam/bytescan-examples/tree/main/streams/sse/ssedriver) and the others
listed in the
[bytescan-examples README](https://github.com/xiam/bytescan-examples#modes-several-scanners-over-one-cursor)).

**Reconciled with principle 1.** The VM never holds a mode, and
`rulesProcessor.Reset` returns every rule to the `Rule` it was registered with
before each token, so a mode that depends on a prior token cannot live in the
`Rule` chain. The package is built only on `NewWithCursor` and `Release` (see
[Concurrency boundaries](resources-and-streams.md#concurrency-boundaries));
after a commit the cursor sits exactly at the end of the token (the commit
block of `Scanner.nextToken` steps the cursor back over the speculative
overshoot). The engine is unchanged.

For example, switching to a "string-interpolation" rule set inside
`"...${ ... }..."` and back out is state carried across tokens, not just within
one: a `modes.Driver` moves the shared cursor from one mode's scanner to
another's. Context within one token needs no mode; see scenario 3 under
[Illustrative scenarios](consumer-contract.md#illustrative-scenarios).

## Byte reads (`bytesource`)

**Current contract — byte streams.** The public package `bytesource` is the
byte adapter: its cursor reads each byte of the input as one rune, U+0000 to
U+00FF, so `Offset()`, `RuneOffset()` and `Len()` of a token are byte offsets
and byte lengths in the input. With the current cursor, `ByteOffset()` and
`Span().Bytes()` count the UTF-8 encoding of those runes and `Line()` and
`Column()` advance at every 0x0A byte, so they are not positions in the input;
this is a limit of the current implementation. The owner chose a byte mode in
`textreader` to remove it (option A; see the
[decision log](design-principles.md#owner-decisions)). A driver reads a
field whose length is in the data with `bytesource.Fields`, whole or in pieces
of at most `MaxPiece` bytes; the queue is stepped with `NextField` and is not a
`Source`. The rules that match byte values are in `rules/octet`; see
[Construction capabilities](composition.md#construction-capabilities).

**Evidence.** `bytesource/bytesource.go`, `bytesource/fields.go`;
`TestOffsets`, `TestFields`, `TestFieldsNotSource`, `TestFieldsChunked` and
`TestFieldsHugeDeclaredLength` (`bytesource/bytesource_test.go`,
`bytesource/fields_test.go`); `ExampleFields` (`bytesource/example_test.go`).

## Core/extension boundary

**Current contract.** Language-specific policies that are *not* about
recognizing text — indentation and formatting rules, language-specific error
messages, recovery strategies tied to a particular grammar — live in an
**extension** layer on top of the core, not in the core itself. The core
provides the recognition and classification machinery; an extension applies a
particular language's policies. The seam is a `Source` layered above `Next()`:
a filter or a driver, as under [Filters](#filters) and
[Lexical modes](#lexical-modes-modes). Indentation is the shipped case: the
Python example's `lineFilter` emits `INDENT` and `DEDENT` above `Next()`.
This was open design question 12; see the
[decision log](design-principles.md#question-12). Evidence: as under
[Filters](#filters).

## Post-match actions

**Intended capability.** The ability to attach a post-match action (decode a
number, unescape a string, compute a value) that runs **only** for the token
the engine actually commits — not for the losing candidates the maximal-munch
process rejected. This is the intended home for transformed or decoded values,
and it is the resolution path for the
[matched-content question](consumer-contract.md#matched-content-versus-transformed-or-decoded-values).
**Reconciled with principle 4.** The VM performs no action. An action runs in
a consumer or builder layer above `Next()`, on the token the VM reports. A
token returned by `Next()` is already the committed winner, so the "only for
the committed token" property holds by construction in that layer. The shape
of that layer is [question 3](open-questions.md#question-3).

The toolkit is also intended to offer named rules, so that a consumer can
describe a scanner declaratively in native Go instead of hand-rolling automata;
see [Construction capabilities](composition.md#construction-capabilities).

## No-match handling and recovery

**Current contract.** The VM reports `UNKNOWN` when no rule matches
([principle 4](design-principles.md#principle-4-the-vm-only-reports-matches));
the fallback inside the VM is fixed to one-rune `UNKNOWN`. A filter above
`Next()` can already act on that report with the helpers under
[Filters](#filters): `Skip` drops `UNKNOWN` tokens, and `WithType` relabels
any token. Evidence: `TestSkip` ("unknown skipped"), `TestWithType`.

**Open design questions 6 and 10.** Whether the toolkit ships a dedicated
no-match hook or a structured recovery layer, and its shape; see
[open questions](open-questions.md#question-6).
