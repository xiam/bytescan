# Guide: a line-oriented text protocol

Server-Sent Events, NMEA 0183 and the control lines of NATS are text, one
record per line, on a stream that may never end. Each line has a few fields
separated by delimiters. The question that picks the technique is whether the
place of a field in the line changes its type.

## One scanner, or one scanner per place

- **The place does not change a type:** one scanner with context-free rules.
  NMEA 0183 is like this: `$` starts a sentence, `,` separates fields, `*`
  and two hex digits are the checksum, and every field is a run of the same
  bytes.
- **The place in the line decides only one thing, at the line start:**
  `core.AtLineStart(rule)` matches only at the start of a line. One scanner is
  still enough.
- **The place changes the type in more ways:** a mode driver with one scanner
  per place. In SSE a name comes before the colon and a value after it, and
  the same characters are a name or a value. The SSE driver keeps its
  position in the line and picks the scanner for it. The NATS driver does the
  same for each field of a control line.

## Rules

- **Delimiters:** one `core.Literal` or `core.Rune` each, with its own type
  (`COLON`, `COMMA`, `EOL`).
- **Fields:** `core.RuneRunMax(class, n)`, a run of the field's characters
  of at most `n` runes. Define the class as everything except the delimiters.
- **Line ends:** write the line end of the protocol, such as `\r?\n` for
  NMEA. `base.Newline` also takes a lone `\r`, which most protocols do not.
- **A guard:** build the scanner with `bytescan.NewWithOptions(r,
  bytescan.WithMaxTokenBytes(n))`, a little above the largest bounded
  token (see [operations.md](operations.md)).

## Registration order

When a field and a name match the same text, the rule registered first wins.
In NMEA, `ADDRESS` (five upper-case letters) comes before `FIELD`. Below,
`NAME` comes before `VALUE`: on `data`, both match four runes.

```go
in := "event: tick\ndata: 0123456789\n"
lx := bytescan.NewWithOptions(strings.NewReader(in), bytescan.WithMaxTokenBytes(4096))

// A name only at the start of a line. At the start of "data", NAME and
// VALUE match the same text, and NAME wins because it comes first.
lx.MustAddRule("NAME", core.AtLineStart(core.RuneRunMax(core.ASCIILetter, 64)))
lx.MustAddRule("COLON", core.Literal(":"))
lx.MustAddRule("SPACE", core.Literal(" "))
// Every run is bounded: a value longer than 4 runes comes out in pieces.
lx.MustAddRule("VALUE", core.RuneRunMax(core.RuneSet("\n: ").Not(), 4))
lx.MustAddRule("EOL", core.Literal("\n"))

printAll(lx)
```

The code is `Example_guideLines` in
[`example_guides_test.go`](../../example_guides_test.go). `printAll` is in
[`example_source_test.go`](../../example_source_test.go).

## Traps

- **Bound every run, and expect pieces.** On a stream that has lost its
  delimiters, an unbounded run grows with the stream. `RuneRunMax` emits
  several tokens of the same type in a row instead, and the consumer joins
  them. The value `0123456789` above is three `VALUE` tokens.
- **Resynchronise at the line start.** After corrupt data the next delimiter
  or line end puts the scanner back in step. A context-free scanner needs nothing
  more. A mode driver goes back to its line-start mode at every line end.
- **Do not check the checksum or the field count.** That is validation, not
  lexing.
- **A driver keeps its state; a rule cannot.** The position in the line
  lives in the driver, and its `SwitchFunc` reads it to pick the next scanner.

## Tests to write

- A golden test with a realistic stream, one case per record form, and the
  broken cases: a missing delimiter, a cut line, noise between lines.
- A long-run test with `streamtest.Run`, and a corruption test with
  `streamtest.Corrupt` that checks the first token after each garbage run
  (see [operations.md](operations.md)).
- A test of an undelimited run: input with no delimiter gives bounded pieces
  and no error.

## Examples

[nmea](https://github.com/xiam/bytescan-examples/tree/main/streams/nmea) (one scanner, context-free),
[sse](https://github.com/xiam/bytescan-examples/tree/main/streams/sse) (a mode driver in its own
package, [`ssedriver`](https://github.com/xiam/bytescan-examples/tree/main/streams/sse/ssedriver)), and
[nats](https://github.com/xiam/bytescan-examples/tree/main/streams/nats) (control lines with
byte-counted payloads; see also [length-prefixed.md](length-prefixed.md)).
