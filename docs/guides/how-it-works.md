# How the scanner works

This page explains the parts of bytescan, how to create a scanner, and what a
token tells you about where it came from. The exact contract is in the
[specification](../specs/core-contract.md); this page links to it section by
section.

## The parts

- **The scanner** (`bytescan.Scanner`) decides what each piece of input is. It
  runs every rule over the input, keeps the longest match, gives a tie to the
  rule registered first, and returns input that no rule matches as `UNKNOWN`.
  It builds the tokens. It keeps no state for a rule and only reports what
  matched ([VM design principles](../specs/design-principles.md#vm-design-principles)).
- **The cursor** ([`cursor`](../../cursor)) does the reading. It decodes
  UTF-8, tracks positions, keeps the input a checkpoint needs, and replays a
  marked position exactly. The scanner owns none of this
  ([scanner/consumer boundary](../specs/consumer-contract.md#scannerconsumer-boundary)).
  The cursor imports only the standard library, so you can use it on its own
  ([using the cursor](../cursor.md)).
- **The byte adapter** ([`bytesource`](../../bytesource)) builds a byte-mode
  cursor, `cursor.NewByteReader`, that reads each byte as one symbol, U+0000
  to U+00FF, and reports file offsets. The scanner reads it the way it reads
  text ([byte reads](../specs/layers.md#byte-reads-bytesource)).
- **Layers above `Next()`** add what depends on earlier tokens: filters,
  lexical modes, drivers that read length-prefixed fields, and piped
  scanners. The [technique chooser](README.md) says which one to use.

## Creating a scanner

- `bytescan.New(r)` takes an `io.RuneReader`. A source that is also an
  `io.Reader`, such as `strings.Reader` or `bufio.Reader`, is read as a byte
  stream, so no second UTF-8 decoder runs. A source that is only an
  `io.RuneReader` is read rune by rune.
- `bytescan.NewWithOptions(r, opts...)` does the same and takes options, such
  as the per-token guard `WithMaxTokenBytes` ([operations](operations.md)).
- `bytescan.NewWithCursor(c)` takes a cursor you built, for example
  `bytesource.NewCursor(r)` for bytes. Any type with the four methods of
  `bytescan.Cursor` works.
- `bytescan.NewWithCursorOptions(c, opts...)` takes a cursor and options. Use
  it when several scanners share one cursor, as the [`modes`](../../modes)
  package does ([a scanner over a shared cursor](../specs/resources-and-streams.md#a-scanner-over-a-shared-cursor)).

## Where a token came from

Every token reports the span of input it covers:

```go
span := tok.Span()

fmt.Println(span.Start().ByteOffset()) // byte offset of the first byte
fmt.Println(span.Start().RuneOffset()) // rune offset of the first rune
fmt.Println(span.Start().Line())       // 1-based line
fmt.Println(span.Start().Column())     // 0-based rune column
fmt.Println(span.End().ByteOffset())   // position just past the token
fmt.Println(span.Bytes())              // width in bytes
fmt.Println(span.Runes())              // width in runes
```

`Example_guideSpans` in
[`example_guides_test.go`](../../example_guides_test.go) runs these lines on
the token `two` of the input `"one\ntwo"`.

- `tok.ByteOffset()`, `tok.RuneOffset()`, `tok.Line()` and `tok.Column()` are
  shorthands for the start of the span.
- `tok.Offset()` counts runes, so it equals `RuneOffset()`. Use
  `ByteOffset()` when you need bytes.
- A position is where the token starts and ends, not where the scanner stopped
  reading. A rule that looks ahead past its match does not move the next
  token's span.
- Through `bytesource`, every position is a position in the input: the offsets
  are file offsets, `Span.Bytes` is the length in bytes, the line advances on
  each 0x0A byte, and the column counts bytes.

The exact rules are in
[source spans](../specs/consumer-contract.md#source-spans) and
[byte versus rune units](../specs/consumer-contract.md#byte-versus-rune-units).

## The text around a token

`scanner.Context(tok, before, after)` returns the input around a token, for an
error message. The scanner keeps the input of its most recent tokens, so the
context can reach back before `tok`. `before` stops at the oldest kept input.
Once later tokens push `tok` out, its input is released and `Context` returns
`cursor.ErrPositionOutOfBuffer`. A `Next` that returns no token, because it
failed or reached the end, releases nothing. On a byte-mode cursor the context
holds the raw bytes
([token lifetime](../specs/consumer-contract.md#token-lifetime-versus-diagnostic-context-lifetime)).

## Memory for each token

While the scanner reads a token it holds a checkpoint on the cursor, so it can
go back if no rule matches. The checkpoints come from a fixed pool, one per
kept token plus the token being read. When the cursor can re-arm a released
checkpoint (a cursor with a `Remark` method, such as `cursor.Reader`), the
scanner reuses the pool and allocates no checkpoint per token. Another cursor
gets a fresh checkpoint for each token. The per-token guard and long streams
are in [operations](operations.md).

## Errors and goroutines

- The errors the scanner decides are sentinel errors, such as
  `bytescan.ErrDuplicateRule` and `bytescan.ErrTokenTooLarge`. Match them with
  `errors.Is`
  ([errors](../specs/consumer-contract.md#unmatched-input-eof-and-errors)).
- Several goroutines may call `Next()` on one scanner. The token stream stays
  deterministic and complete
  ([concurrency](../specs/resources-and-streams.md#concurrency-boundaries)).
