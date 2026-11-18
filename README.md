# bytescan

bytescan reads a stream of text or bytes and tells you what each piece is.
You describe the pieces with rules: a keyword, a word, a number, a frame
marker, a file signature. bytescan hands back each piece with its label and
its exact place in the input. It does not parse: what the pieces mean is up
to you.

One engine reads source code, prose in any script, logs, terminal output,
file formats and network protocols. It can read a stream that never ends,
in bounded memory.

You get each piece while the input is still arriving, without waiting for the
end of it. `Next` returns a piece once the rules can see where it ends, which
is often one symbol after it.

## Install

```sh
go get github.com/xiam/bytescan
```

## Example: text

Find the keyword and the words in two lines of text, and where each one is.

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
	lx := bytescan.New(strings.NewReader("say hello\nto the world"))

	// "say" is both a keyword and a word: the rule added first wins.
	lx.MustAddRule("KEYWORD", core.Keyword("say", core.UnicodeLetter))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WHITESPACE", base.Whitespace())
	lx.MustAddRule("NEWLINE", base.Newline())

	for {
		tok, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		if tok.Type() == "KEYWORD" || tok.Type() == "WORD" {
			fmt.Printf("line %d, column %d: %s %q\n",
				tok.Line(), tok.Column(), tok.Type(), tok.Text())
		}
	}
}
```

Output:

```
line 1, column 0: KEYWORD "say"
line 1, column 4: WORD "hello"
line 2, column 0: WORD "to"
line 2, column 3: WORD "the"
line 2, column 7: WORD "world"
```

## Example: binary

Split two [SLIP](https://www.rfc-editor.org/rfc/rfc1055) frames into their
parts. SLIP sends packets over a serial line. The byte C0 ends a frame. Inside
a frame, the pair DB DC stands for the byte C0, and DB DD for the byte DB.

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
	frames := []byte{0x01, 0x02, 0xDB, 0xDC, 0x03, 0xC0, 0xDB, 0xDD, 0xFF, 0xC0}

	// bytesource lets the scanner read bytes: each byte is one symbol.
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(frames)))

	// DATA is any byte but C0 and DB, at most 1000 bytes in one token.
	data := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	lx.MustAddRule("END", octet.Set(0xC0))
	lx.MustAddRule("DATA", core.RuneRunMax(data, 1000))
	lx.MustAddRule("ESC_END", octet.Literal([]byte{0xDB, 0xDC}))
	lx.MustAddRule("ESC_ESC", octet.Literal([]byte{0xDB, 0xDD}))

	for {
		tok, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		fmt.Println(bytesource.Hex(tok))
	}
}
```

Output (byte offset, type, length, bytes):

```
0 DATA 2 01 02
2 ESC_END 2 db dc
4 DATA 1 03
5 END 1 c0
6 ESC_ESC 2 db dd
8 DATA 1 ff
9 END 1 c0
```

Text and bytes use the same engine, the same kind of rules and the same loop.
Both examples run as tests in [`example_readme_test.go`](example_readme_test.go).

## When to use it

Use bytescan when text or bytes arrive as a stream, and your program must
label pieces of it by their shape before all of it has arrived. For example:

- A model's streamed response, sent as JSON inside Server-Sent Events: [ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson).
- A live log, read as the messages come in: [syslog](https://github.com/xiam/bytescan-examples/tree/main/streams/syslog).
- The output of a running program, with its color codes: [ansi](https://github.com/xiam/bytescan-examples/tree/main/ansi).

## What you can build

Each item links to one of about thirty working programs in
[bytescan-examples](https://github.com/xiam/bytescan-examples).

- A tokenizer for a programming language or a data format: [json](https://github.com/xiam/bytescan-examples/tree/main/json), [sql](https://github.com/xiam/bytescan-examples/tree/main/sql), [go](https://github.com/xiam/bytescan-examples/tree/main/go), [python](https://github.com/xiam/bytescan-examples/tree/main/python).
- A word splitter for text in any language: [english](https://github.com/xiam/bytescan-examples/tree/main/english), [japanese](https://github.com/xiam/bytescan-examples/tree/main/japanese), [arabic](https://github.com/xiam/bytescan-examples/tree/main/arabic).
- A reader of binary files: [zip](https://github.com/xiam/bytescan-examples/tree/main/binary/zip), [jpeg](https://github.com/xiam/bytescan-examples/tree/main/binary/jpeg), [wav](https://github.com/xiam/bytescan-examples/tree/main/binary/wav).
- A monitor for a network protocol that never stops: [mqtt](https://github.com/xiam/bytescan-examples/tree/main/streams/mqtt), [websocket](https://github.com/xiam/bytescan-examples/tree/main/streams/websocket), [syslog](https://github.com/xiam/bytescan-examples/tree/main/streams/syslog).
- A reader of terminal output with color codes: [ansi](https://github.com/xiam/bytescan-examples/tree/main/ansi).
- One scanner inside another, such as JSON inside Server-Sent Events: [ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson).

## Packages at a glance

| Package | What it does |
| --- | --- |
| [`bytescan`](https://pkg.go.dev/github.com/xiam/bytescan) | The scanner: it runs your rules and returns tokens. |
| [`cursor`](cursor/README.md) | Reads text or bytes and tracks the position of every read. It works on its own. |
| [`bytesource`](bytesource/README.md) | Lets the scanner read bytes, and reads fields whose length is in the data. |
| [`modes`](https://pkg.go.dev/github.com/xiam/bytescan/modes) | Switches between rule sets, for example inside and outside a string. See [the guides](docs/guides/README.md). |
| [`rules`](rules/README.md) | Ready-made rules in four packages: `core` (building blocks), `base` (common tokens of code and text), `special` (URLs, email addresses, emoji) and `octet` (byte values). |
| [`streamtest`](https://pkg.go.dev/github.com/xiam/bytescan/streamtest) | Tests that your scanner reads a long stream in bounded memory. See [long streams](docs/guides/operations.md). |

## Learn more

- [Choose a technique](docs/guides/README.md): start here to build a scanner
  for your own format.
- [The rules](docs/rules.md) and [how to write your own](docs/rule-authoring.md).
- [Long streams, bounds and memory](docs/guides/operations.md).
- [How the scanner works](docs/guides/how-it-works.md): positions, context and what
  each part does.
- [The specification](docs/specs/core-contract.md): the exact contract.
- [All documentation](docs/README.md).

## License

MIT License. See [LICENSE.md](LICENSE.md).
