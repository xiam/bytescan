# Guides: choose a technique

`bytescan` reads a stream and classifies what it finds. This page helps you
pick how to build a scanner for your format. Each guide below covers one kind
of format: the rules to use, the order to register them in, the traps, the
tests to write, and the examples that show it. The example programs live in
a separate repository,
[bytescan-examples](https://github.com/xiam/bytescan-examples). Every guide
ends at the token stream; what the tokens mean is up to you.

## The five techniques

Every example scanner is one of these five, or two of them together.
Start with the first one and move down only when a question below says so.

| Technique | Use it when | API | Reference |
| --- | --- | --- | --- |
| **Rules only** | One rule set classifies every token, wherever it is. | `bytescan.New`, the rules of `rules/core`, `rules/base`, `rules/special`, `rules/octet` | [json](https://github.com/xiam/bytescan-examples/tree/main/json), [slip](https://github.com/xiam/bytescan-examples/tree/main/streams/slip) |
| **A filter** | A token depends on the tokens before it, not on the characters: inserted semicolons, `INDENT` and `DEDENT`, dropped comments. | `bytescan.Source`, `bytescan.Skip`, `Token.WithType`, `bytescan.NewEmptyToken` | [go](https://github.com/xiam/bytescan-examples/tree/main/go), [python](https://github.com/xiam/bytescan-examples/tree/main/python), [ansi](https://github.com/xiam/bytescan-examples/tree/main/ansi) |
| **A mode driver** | The same characters classify differently in different contexts: inside a string, after an opener, at one place in a line. | [`modes`](../../modes): `modes.New`, `Use`, `WithSwitch`, `modes.Cache`; `modes.NewModeSet` for a stack of mode names | [lua](https://github.com/xiam/bytescan-examples/tree/main/lua), [typescript](https://github.com/xiam/bytescan-examples/tree/main/typescript), [sse](https://github.com/xiam/bytescan-examples/tree/main/streams/sse) |
| **A driver read** | The data says how long the next field is: a length prefix, an offset, a size in a header. | [`bytesource`](../../bytesource): `Fields`, `Field.MaxPiece`, `Read`, `Peek` | [mqtt](https://github.com/xiam/bytescan-examples/tree/main/streams/mqtt), [zip](https://github.com/xiam/bytescan-examples/tree/main/binary/zip) |
| **Piped scanners** | The text of some tokens is a stream in another format. | two cursors and an `io.Reader` between them | [ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson) |

## Questions that pick the technique

1. **Is the input text or bytes?** Text goes to `bytescan.New`. Bytes go
   through `bytesource.NewCursor` and `bytescan.NewWithCursor`, and the rules
   of `rules/octet` match byte values.
2. **Can a rule see everything that decides a token?** A rule sees the runes
   of the token, a little look-ahead, and whether a rune starts a line
   (`core.AtLineStart`). If that is enough, use rules only.
3. **Does the decision depend on earlier tokens, but not on how the next
   characters are cut?** Use a filter above `Next()`. A filter adds, drops or
   relabels tokens; it never changes where a token starts or ends.
4. **Do the next characters need different rules?** Use a mode driver: one
   scanner per mode over one cursor.
5. **Does a number in the data give the length of what follows?** Use a driver
   read: the rules lex the header, and the driver reads the field.
6. **Is one format carried inside the values of another?** Pipe two scanners.
7. **Does a user edit the text after it was lexed, in an editor or a REPL?**
   Write the rules as a `modes.ModeSet`, with rules only or with modes, and
   hold the text in the lexer with state ([`statelex`](../../statelex)). See
   [text-that-changes.md](text-that-changes.md).

## Guides by use case

| You have | Guide | Technique |
| --- | --- | --- |
| A programming language | [programming-language.md](programming-language.md) | rules, a filter, a mode driver |
| A data format such as JSON | [data-format.md](data-format.md) | rules only |
| A terminal or ANSI stream | [terminal-stream.md](terminal-stream.md) | rules and a type filter |
| Text in a human language | [human-text.md](human-text.md) | rules only |
| A line-oriented text protocol (SSE, NMEA, NATS) | [line-protocol.md](line-protocol.md) | rules, or a mode driver per place in a line |
| A length-prefixed binary protocol (MQTT, WebSocket, syslog octet counting) | [length-prefixed.md](length-prefixed.md) | a driver read |
| Fixed-size packets with a sync marker (MPEG-TS) | [sync-marker.md](sync-marker.md) | a mode driver and a driver read |
| Escape-framed bytes (SLIP) | [escape-framed.md](escape-framed.md) | rules only |
| One format nested in another (JSON inside SSE) | [nested-formats.md](nested-formats.md) | piped scanners |
| Text that a user edits (an editor, a REPL) | [text-that-changes.md](text-that-changes.md) | the lexer with state |

[operations.md](operations.md) covers what every scanner of a stream needs:
the per-token guard (`WithMaxTokenBytes`), `Release`, retention, and soak
tests with [`streamtest`](../../streamtest). [how-it-works.md](how-it-works.md)
explains the parts of bytescan, the constructors, token positions and the
text around a token.

## Rules that hold for every technique

- **The longest match wins; a tie goes to the rule registered first.**
  Register a specific rule (a keyword, a URL) before the general rule that
  also matches it (an identifier, a word).
- **One rule per token type.** Combine several forms of one type with
  `core.Choice`, for example
  `core.Choice(base.Float(), base.Decimal(), base.Integer())`.
- **Input that no rule matches is `UNKNOWN`, one rune or byte at a time.** The
  scanner keeps going after it. Do not report bad input from a scanner.
- **Bound every run on a stream that may not end.** `core.RuneRunMax` lexes a
  long run as several pieces, so no token grows with the stream.

## Where to go next

- [rules.md](../rules.md): what each rule tier holds.
- [rule-authoring.md](../rule-authoring.md): how to write a rule of your own.
- [bytescan-examples](https://github.com/xiam/bytescan-examples): the
  example programs, in a separate repository; its README is the catalogue of
  examples, grouped by use case.
- [specs/layers.md](../specs/layers.md): the contract of filters, modes and
  byte reads.

Every Go code block in these guides compiles. The guides name the function
that holds each block, most of them in
[`example_guides_test.go`](../../example_guides_test.go), where `go test`
runs it.
