# bytescan

`bytescan` reads a stream of data and classifies what it finds. The stream can
be text — source code, prose in any script, a log, a terminal session — or
binary — a file format or a network protocol. You describe each kind of data
with a rule: what a keyword is, what a word is, what a frame delimiter or a
file signature is. The engine reads the stream once, from the front, and hands
you back a stream of **tokens**: each piece of the input, the label you gave
its kind, and its exact position in the source.

That is the whole job: read and classify. The engine does not parse. It checks
no grammar, builds no tree and decodes no value; what the classified stream
means is up to you, the consumer. The same engine serves many scanners — a
highlighter, a SQL tokenizer, a log reader, a reader of ZIP archives, a
protocol monitor — each defined by its own rules.

The canonical prose specification — the contract for what the engine does today,
what it is intended to become, and what is still an open design question — lives
in [docs/specs/core-contract.md](docs/specs/core-contract.md), the index of
the specification documents. This README presents the capabilities and
limitations and two runnable examples; the spec is the reference for exact
behavior. The reusable rules are described in
[docs/rules.md](docs/rules.md).

bytescan scans streams of text or binary data into classified tokens; what you
do with them, including parsing, is up to you.

## Renamed from textlexer

This project was called textlexer, with the module path
`github.com/xiam/textlexer`. From v0.3.0 the module is
`github.com/xiam/bytescan` and the root package is `bytescan`. The rename also
changed these identifiers; there are no aliases for the old names:

| textlexer v0.2.x | bytescan v0.3.0 |
|---|---|
| `textlexer.TextLexer` | `bytescan.Scanner` |
| `Lexeme` | `Token` |
| `LexemeType` | `TokenType` |
| `LexemeTypeUnknown` | `TokenTypeUnknown` |
| `NewLexeme`, `NewLexemeFromString`, `NewLexemeWithSpan`, `NewEmptyLexeme` | `NewToken`, `NewTokenFromString`, `NewTokenWithSpan`, `NewEmptyToken` |
| `ErrNilLexeme` | `ErrNilToken` |
| `streamtest.WithLexemeFunc`, `streamtest.Result.Lexemes` | `streamtest.WithTokenFunc`, `streamtest.Result.Tokens` |

The constructors (`New`, `NewWithOptions`, `NewWithCursor`,
`NewWithCursorOptions`) keep their names and return `*bytescan.Scanner`. Error
messages start with `bytescan:` instead of `textlexer:`; the sentinel errors
keep their names, so `errors.Is` matches as before. Behaviour is unchanged.

To migrate:

1. Replace the import path `github.com/xiam/textlexer` with
   `github.com/xiam/bytescan` (and `textlexer.` with `bytescan.`), and rename
   the identifiers in the table.
2. Run `go get github.com/xiam/bytescan@v0.3.0` and `go mod tidy`.

The old module path is not maintained. `go get github.com/xiam/textlexer@latest`
fails with `module declares its path as: github.com/xiam/bytescan but was
required as: github.com/xiam/textlexer`, which names the new path. Pinned
versions, `@v0.2.5` and earlier, keep building.

## Capabilities today

- **Text and binary through one engine.** A scanner reads symbols from a
  positioned cursor. For text the cursor decodes UTF-8, and a symbol is a rune.
  For binary data the byte adapter [`bytesource`](bytesource) gives the cursor
  each byte as one symbol, so the same rules, combinators and scanner classify
  bytes. A field whose length is stored in the data is read by a driver with
  `bytesource.Read` or the `bytesource.Fields` queue. `Read` allocates for the
  bytes it reads, not for the length the data declares, and `Fields` can cut
  a long field into pieces of a bounded size.
- **Reusable rules in four tiers.** The packages under `rules/` build rules
  for you; every constructor returns a `bytescan.Rule`
  ([tier model](docs/rules.md)):
  - [`rules/core`](rules/core): character classes (ASCII classes, Unicode
    general categories and scripts, sets and ranges), primitives (`Literal`,
    `LiteralIgnoreCase`, `AnyLiteral`, `AnyLiteralIgnoreCase`, `Keyword`, the
    keyword sets `Keywords` and `KeywordsIgnoreCase`, `Rune`, `RuneRun`,
    the bounded run `RuneRunMax`), combinators (`Sequence`, `Choice`,
    `Optional`, `ZeroOrMore`, `OneOrMore`, `Repeat`, `FollowedBy`,
    `NotFollowedBy`), the line anchor `AtLineStart` and the `PushBack` helper.
    Composition is leftmost-longest inside a composed rule.
  - [`rules/base`](rules/base): common tokens of programming languages
    (whitespace, line breaks, identifiers, integers and floats with options
    for the form of a language, quoted and raw strings, strings with a
    checked escape grammar and escaped string bodies, comments) and of
    human text (`Word`, `TextNumber`, punctuation, symbols, script runs),
    composed from core.
  - [`rules/special`](rules/special): `URL` (an RFC 3986 subset), `Email`
    (the WHATWG HTML valid email address) and `Emoji` (the UTS #51 sequence
    grammar).
  - [`rules/octet`](rules/octet): byte values of binary formats: one byte
    (`Any`, `Set`, `Range`), fixed fields (`Literal`, `Fixed`, `Record`) and
    variable-length integers (`Uvarint`), for input read through `bytesource`.
- **Rules you write in Go.** A rule is a state-transition function, so any
  matching logic expressible in Go is expressible as a rule: literals,
  character or byte predicates, Unicode-aware matching, and positional
  decisions (beginning of line, end of line, end of input) via the symbol's
  flags. Hand-written rules and package rules compose: every combinator
  accepts any `bytescan.Rule`. The [rule-authoring standard](docs/rule-authoring.md)
  says how to write one.
- **Longest match, with deterministic tie breaking.** If two rules both match,
  the longer match wins; if they match the same length, the rule registered
  first wins. Input that no rule matches comes out as `UNKNOWN` tokens, so
  the stream goes on after data the rules do not describe.
- **Layers above `Next()`.** Anything that yields tokens one at a time is a
  `bytescan.Source`, and the engine stays a pure classifier under it:
  - **Filters.** `bytescan.Skip` drops tokens by type, `Token.WithType`
    relabels one, and `bytescan.NewEmptyToken` makes a synthetic token,
    such as an inserted semicolon or an `INDENT`.
  - **Lexical modes.** The [`modes`](modes) package runs several scanners in
    turn over one shared cursor, one per mode: it releases the outgoing scanner
    on every switch, runs one scanner at a time, and caches a scanner per key,
    such as a bracket level or a terminator.
  - **Drivers for binary formats.** A driver reads a header with rules and
    the fields it announces with `bytesource.Fields`.
  - **Piped scanners.** The text that one scanner classifies can be the input of
    another, as JSON inside Server-Sent Events in the
    [ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson) example.
- **Full source positions.** Every token carries a half-open span with byte
  offset, rune offset, 1-based line, and 0-based rune column at both ends.
  Through `bytesource`, `Offset` and `Len` are the byte offset and length in
  the input.
- **Safe to drive concurrently.** Multiple goroutines can call `Next()` on one
  scanner; the token stream stays deterministic and complete.
- **Long-running streams in bounded memory.** An optional per-token byte bound
  caps how much source a single in-flight token may retain, and a diagnostic
  `Context` accessor reads the text around a recent token. The scanner reuses a
  fixed pool of checkpoint slots — one per retained token plus the token in
  flight — so steady-state tokenization stops allocating checkpoints: each
  committed token re-arms the slot its predecessor just vacated. Cursors that
  cannot re-arm a checkpoint (see `NewWithCursor`) keep the older, per-token
  allocation behavior. A soak test lexes long-running streams and checks that
  the live heap, the goroutine count and the allocations per token stay flat
  ([long-running streams](docs/specs/resources-and-streams.md#long-running-streams)).
  The [`streamtest`](streamtest) package runs the same kind of test on your
  own scanner: it generates a long stream of records on the fly, optionally
  with corrupt data between them, lexes it to the end and fails if the live
  heap grows past a ceiling.
- **Matchable errors.** Failures that the scanner decides are sentinel errors,
  such as `bytescan.ErrDuplicateRule` and `bytescan.ErrTokenTooLarge`, for
  `errors.Is`.

## Limitations today

- **One rule per token type.** A scanner accepts each token type name once;
  a second `AddRule` with the same name returns an error. To give several
  forms one type, combine them into one rule with `core.Choice`, for example
  `core.Choice(base.Float(), base.Decimal(), base.Integer())`.
- **Byte positions other than `Offset` and `Len` are not exact yet.** The
  byte adapter runs on the text cursor, which counts its other positions on a
  UTF-8 encoding of the symbols. For binary input, `ByteOffset` and
  `Span.Bytes` count a byte of 0x80 or above as two bytes, `Line` and
  `Column` advance at every 0x0A byte, and `WithMaxTokenBytes` charges two
  bytes for a byte of 0x80 or above. Use `Offset` and `Len`. A byte-exact
  cursor is planned (see Future work).
- **Case-insensitive keywords use simple case folding only.**
  `core.KeywordsIgnoreCase` and `core.AnyLiteralIgnoreCase` fold as
  `core.LiteralIgnoreCase` does: Unicode simple case folding, rune by rune, so
  `ß` does not match `ss`. `core.Keyword` itself is case-sensitive; a set of
  one word, `core.KeywordsIgnoreCase("select")`, is its case-insensitive form.
- **`base.Word` approximates UAX #29.** It does not implement Unicode word
  segmentation: a word does not stop where the script changes (`東京tokyo` is
  one word), holds no digit, and uses no dictionary, so a run of Han or Thai
  text with no spaces is one word. See the `rules/base` package documentation
  for the full list of differences.
- **`special.Emoji` checks the shape, not RGI membership.** It matches any
  well-formed UTS #51 emoji sequence, including one that is not in the
  recommended (RGI) set, and does not match an unqualified emoji. `URL` and
  `Email` also check the lexical shape only.
- **No named rules.** A rule cannot refer to another rule by name, or to
  itself, before that rule is built; a recursive construct such as nested
  parentheses needs a hand-written rule.
- **No state or post-match actions in the engine, by design.** The engine is a
  stateless stack machine that only reports matches (see the
  [VM design principles](docs/specs/design-principles.md#vm-design-principles)).
  Anything that depends on earlier tokens — a mode, a length read from a
  header, a filter — lives in a layer above `Next()`, as listed under
  Capabilities. Post-match actions are an intended capability of such a
  layer, not of the engine.
- **One flat label.** A token carries a single string-valued type. There is no
  kind/metadata API yet; expressing a broad category plus a specific kind is an
  intended capability with an open design question.
- **No parsing, by design.** The output is a stream of classified tokens,
  nothing more. Anything structural is the consumer's job.

**Future work.** On top of the current engine: named rules and post-match
actions in a layer above `Next()`; a byte mode in the cursor, so that every
position of a binary stream is a byte position; and eventually declarative
frontends (for example, a schema- or table-driven way of declaring rules) that compile down to the same engine. These are
direction, not commitments — the spec marks each as intended or open.

## Example: text

Find all the keywords and words in the phrase "say hello to the world", using
only the rules of `rules/core` and `rules/base`.

```go
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
)

func main() {
	// The text we want to classify.
	myText := "say hello to the world"

	// 1. Create a new scanner that reads our text.
	lx := bytescan.New(strings.NewReader(myText))

	// 2. Add rules for what we want to find. The order matters: "say" is
	//    both a keyword and a word, and the rule added first wins the tie.
	lx.MustAddRule("KEYWORD", core.Keyword("say", core.UnicodeLetter))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WHITESPACE", base.Whitespace())

	// 3. Loop and get the next labeled piece until we reach the end.
	for {
		token, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}

		// Skip over whitespace.
		if token.Type() == "WHITESPACE" {
			continue
		}

		// Print the piece we found!
		fmt.Printf("Found: [%s] \"%s\"\n", token.Type(), token.Text())
	}
}
```

#### Output:

```
Found: [KEYWORD] "say"
Found: [WORD] "hello"
Found: [WORD] "to"
Found: [WORD] "the"
Found: [WORD] "world"
```

## Example: binary

Classify the bytes of two SLIP frames
([RFC 1055](https://www.rfc-editor.org/rfc/rfc1055)), a framing for serial
lines: the byte C0 ends a frame, and two escape pairs stand for the bytes C0
and DB inside the data. The scanner reads the bytes through `bytesource` and
uses the rules of `rules/octet` and `rules/core`.

```go
package main

import (
	"bytes"
	"fmt"
	"io"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

func main() {
	// Two SLIP frames (RFC 1055). The byte C0 ends a frame. Inside a frame,
	// the pair DB DC stands for a data byte C0, and DB DD for a data byte DB.
	frames := []byte{
		0x01, 0x02, 0xDB, 0xDC, 0x03, 0xC0,
		0xDB, 0xDD, 0xFF, 0xC0,
	}

	// 1. Create a new scanner that reads the bytes through the byte adapter:
	//    each byte is one symbol.
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(frames)))

	// 2. Add a rule for each kind of byte sequence. A data run is any byte
	//    but C0 and DB, and at most 1000 bytes long, so a stream that never
	//    ends a frame still comes out in bounded pieces.
	data := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	lx.MustAddRule("END", octet.Set(0xC0))
	lx.MustAddRule("DATA", core.RuneRunMax(data, 1000))
	lx.MustAddRule("ESC_END", octet.Literal([]byte{0xDB, 0xDC}))
	lx.MustAddRule("ESC_ESC", octet.Literal([]byte{0xDB, 0xDD}))

	// 3. Print the byte offset, type, length and bytes of every token.
	for {
		token, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		fmt.Println(bytesource.Hex(token))
	}
}
```

#### Output:

```
0 DATA 2 01 02
2 ESC_END 2 db dc
4 DATA 1 03
5 END 1 c0
6 ESC_ESC 2 db dd
8 DATA 1 ff
9 END 1 c0
```

The code of both examples is also in
[`example_readme_test.go`](example_readme_test.go), where `go test` runs it.

## Examples by use case

About thirty example programs classify real formats. They live in a separate
repository, [bytescan-examples](https://github.com/xiam/bytescan-examples),
whose README is the catalogue of examples and the standard each one follows,
so this module holds only the library. To build a scanner for your own format,
start with the [technique chooser](docs/guides/README.md): it picks between
rules only, a filter, a mode driver, a driver read and piped scanners, and links
one guide per use case.

- **Programming languages and data formats:**
  [json](https://github.com/xiam/bytescan-examples/tree/main/json) (the reference example),
  [sql](https://github.com/xiam/bytescan-examples/tree/main/sql), [go](https://github.com/xiam/bytescan-examples/tree/main/go),
  [python](https://github.com/xiam/bytescan-examples/tree/main/python), [lua](https://github.com/xiam/bytescan-examples/tree/main/lua),
  [typescript](https://github.com/xiam/bytescan-examples/tree/main/typescript), [ruby](https://github.com/xiam/bytescan-examples/tree/main/ruby),
  [whitespace](https://github.com/xiam/bytescan-examples/tree/main/whitespace), and
  [hello](https://github.com/xiam/bytescan-examples/tree/main/hello), the text example above as a program
  that reads standard input.
- **Human-language text:** [words](https://github.com/xiam/bytescan-examples/tree/main/words),
  [english](https://github.com/xiam/bytescan-examples/tree/main/english), [spanish](https://github.com/xiam/bytescan-examples/tree/main/spanish),
  [arabic](https://github.com/xiam/bytescan-examples/tree/main/arabic), [japanese](https://github.com/xiam/bytescan-examples/tree/main/japanese),
  [korean](https://github.com/xiam/bytescan-examples/tree/main/korean).
- **Text mixed with control data:** [ansi](https://github.com/xiam/bytescan-examples/tree/main/ansi), terminal
  escape sequences.
- **Binary files:** [zip](https://github.com/xiam/bytescan-examples/tree/main/binary/zip),
  [bmp](https://github.com/xiam/bytescan-examples/tree/main/binary/bmp), [jpeg](https://github.com/xiam/bytescan-examples/tree/main/binary/jpeg),
  [wav](https://github.com/xiam/bytescan-examples/tree/main/binary/wav), [mp3](https://github.com/xiam/bytescan-examples/tree/main/binary/mp3).
- **Text protocols and logs on endless streams:**
  [sse](https://github.com/xiam/bytescan-examples/tree/main/streams/sse), [nmea](https://github.com/xiam/bytescan-examples/tree/main/streams/nmea),
  [syslog](https://github.com/xiam/bytescan-examples/tree/main/streams/syslog),
  [nats](https://github.com/xiam/bytescan-examples/tree/main/streams/nats).
- **Binary protocols on endless streams:**
  [mqtt](https://github.com/xiam/bytescan-examples/tree/main/streams/mqtt),
  [websocket](https://github.com/xiam/bytescan-examples/tree/main/streams/websocket),
  [mpegts](https://github.com/xiam/bytescan-examples/tree/main/streams/mpegts),
  [slip](https://github.com/xiam/bytescan-examples/tree/main/streams/slip).
- **Nested formats:** [ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson), JSON
  inside Server-Sent Events, two scanners piped together.

## Layering

`bytescan` owns lexical policy: rule state transitions, parallel rule
evaluation, maximal-munch selection, registration-order tie breaking, the
`UNKNOWN` fallback, and token construction. It owns no stream mechanics.
It applies the transitions a rule returns but keeps no state on a rule's
behalf, and it only reports what matched; see the
[VM design principles](docs/specs/design-principles.md#vm-design-principles).

Reading, buffering, positions, and replay come from a positioned cursor —
[`github.com/xiam/textreader`](https://github.com/xiam/textreader). The cursor
decodes UTF-8, tracks the logical position, retains the input a checkpoint needs,
and restores a marked position exactly. For binary input,
`bytesource.NewCursor` builds the same cursor over a reader that returns each
byte as one rune, U+0000 to U+00FF, and the scanner reads it as it reads text.

`New` accepts the same `io.RuneReader` it always did. A source that also
implements `io.Reader` is read as a byte stream by the cursor, so no extra UTF-8
decoder is introduced; a source that only implements `io.RuneReader` is read rune
by rune. `NewWithCursor` takes an already-built cursor instead, and
`NewWithCursorOptions` takes one together with options.

### Token spans

Every token reports the source span it covers:

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

`Example_spans` in [`example_readme_test.go`](example_readme_test.go) runs
these lines on the token `two` of the input `"one\ntwo"`.

`tok.ByteOffset()`, `tok.RuneOffset()`, `tok.Line()`, and `tok.Column()` are
shorthands for the start position. `Offset()` is unchanged and still counts
runes, so it equals `RuneOffset()`; use `ByteOffset()` when bytes are what you
need.

Positions come from the committed token, not from where the scanner stopped
reading: a rule that looks ahead past its match does not shift the next token's
span.

While a token is being assembled the scanner holds a checkpoint, so
`WithMaxTokenBytes(n)` bounds how many source bytes one token may retain. A token
that exceeds the bound fails with an error wrapping
`textreader.ErrRetentionExceeded` instead of growing without limit, and the
cursor is rewound to the token start, so a failed `Next` consumes nothing. The
default is unbounded. The bound is per scanner: a scanner over a shared cursor gets
it from `NewWithCursorOptions`, and the cursor's own retention budget
(`textreader.WithMaxRetained`) is shared by every scanner on that cursor.

`scanner.Context(tok, before, after)` returns the source text around a token,
including the input that precedes it. The scanner keeps a window of the most
recent tokens retained, so the context stays available while `tok` is inside
that window, and `before` is clamped to it. A `Next` that commits nothing — one
that fails, or the final EOF probe — releases nothing. Once later tokens push
`tok` out of the window, its retained input is released and calls return
`textreader.ErrPositionOutOfBuffer`.

The retained window is backed by a fixed pool of checkpoint slots (the window
width plus the in-flight token). When the cursor supports checkpoint re-arming,
a committed token's slot is released and the next token re-arms that same slot
in place, so the number of live checkpoints is bounded by the window and no
checkpoint is allocated once the pool is full. `NewWithCursor` accepts any
cursor that implements the four-method `Cursor` interface; a cursor that also
exposes `Remark` (as `textreader.NewReader` does) opts into the reuse path,
while a cursor without it falls back to allocating a fresh checkpoint per
token.

## Installation

```sh
go get github.com/xiam/bytescan
```

## License

MIT License. See the [LICENSE.md](LICENSE.md) file for details.
