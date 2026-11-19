# Guide: one format nested in another

A streaming API that sends JSON documents as Server-Sent Events carries one
format inside the values of another. The outer scanner classifies the event
stream; the text of its `data` values is a second stream, and an inner scanner
classifies that stream as JSON. A JSON token can cross the line breaks of the
outer layer, so the inner scanner must read the joined text, not each value on
its own.

## Two cursors joined by a pull pipe

- **The outer layer** is a scanner or a driver over its own cursor.
- **The pipe** is an `io.Reader`. Its `Read` calls `Next` on the outer layer
  until it gets a token whose text belongs to the inner stream, and returns
  that text.
- **The inner layer** is a scanner over a second cursor that reads the pipe.

No goroutine runs: the inner cursor calls `Read` from inside the inner
scanner's `Next`, and `Read` calls the outer `Next`. That is allowed, because
the two scanners do not share a cursor.

```go
type pipe struct {
	outer bytescan.Source
	hold  string // text to return before the next outer token
}

func (p *pipe) Read(b []byte) (int, error) {
	for p.hold == "" {
		l, err := p.outer.Next()
		if err != nil {
			return 0, err
		}
		switch l.Type() {
		case "BODY":
			p.hold = l.Text()
		case "CLOSE":
			p.hold = " "
		}
	}
	n := copy(b, p.hold)
	p.hold = p.hold[n:]
	return n, nil
}
```

The pipe is `pipe` in
[`example_guides_test.go`](../../example_guides_test.go). The two layers:

```go
// The outer layer: bodies in angle brackets, one per line.
outer := bytescan.New(strings.NewReader("<12 apples>\n<3 pears>\n"))
outer.MustAddRule("OPEN", core.Literal("<"))
outer.MustAddRule("CLOSE", core.Literal(">"))
// A small bound, so that the body "12 apples" comes in two pieces.
outer.MustAddRule("BODY", core.RuneRunMax(core.RuneSet("<>\n").Not(), 8))
outer.MustAddRule("NEWLINE", core.Literal("\n"))

// The inner layer reads the text of the bodies through the pipe, on a
// cursor of its own.
inner := bytescan.New(bufio.NewReader(&pipe{outer: outer}))
inner.MustAddRule("NUMBER", core.RuneRunMax(core.ASCIIDigit, 20))
inner.MustAddRule("WORD", core.RuneRunMax(core.ASCIILetter, 64))
inner.MustAddRule("SPACE", core.Literal(" "))

// Offsets are local to the inner stream.
for {
	l, err := inner.Next()
	if err == io.EOF {
		break
	}
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if l.Type() != "SPACE" {
		fmt.Println(l.Offset(), l.Type(), l.Text())
	}
}
```

The code is `Example_guideNested` in
[`example_guides_test.go`](../../example_guides_test.go). This pipe drops the
outer tokens. To return both layers, queue the outer tokens in the pipe and
return them before the inner token whose read caused them, as the
[ssejson driver](https://github.com/xiam/bytescan-examples/blob/main/streams/ssejson/driver.go) does.

## One format nested in another on one cursor

When the inner format is a context of the outer text, such as a string in
code, no second cursor is needed. Use a mode set of package
[`modes`](../../modes): each mode is a list of rules, and each rule says what
happens after its token. `Rule` stays in the mode, `Push` enters a named mode,
and `Pop` goes back to the mode before. The driver keeps the modes on a stack,
so a format can nest in another as deep as the text goes.

`Stack` returns the mode state at the start of the next token. Save it with an
assignment, and compare two states with `Equal`. To go on from a token, give
the saved state to `Driver`, with a cursor at the token.

```go
word := core.RuneRun(core.RuneRange('a', 'z'))
quote := core.Literal(`"`)

ms := modes.NewModeSet("code")
ms.Mode("code").
	Rule("WORD", word).
	Rule("WS", core.RuneRun(core.RuneSet(" "))).
	Push("QUOTE", quote, "str")
ms.Mode("str").
	Rule("TEXT", core.RuneRun(core.RuneSet(`"`).Not())).
	Pop("QUOTE", quote)

text := `say "hi" now`
d, err := ms.Driver(cursor.New(strings.NewReader(text)), modes.Stack{})
if err != nil {
	fmt.Println("error:", err)
	return
}

var (
	saved modes.Stack
	at    int
)
for {
	st := d.Stack() // the mode state at the start of the next token
	l, err := d.Next()
	if err == io.EOF {
		break
	}
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%-5s %-6q top=%s depth=%d\n", l.Type(), l.Text(), st.Top(), st.Len())
	if l.Text() == "hi" {
		saved, at = st, l.Offset()
	}
}

// Start again at "hi", in the state saved there.
d, err = ms.Driver(cursor.New(strings.NewReader(text[at:])), saved)
if err != nil {
	fmt.Println("error:", err)
	return
}
l, _ := d.Next()
fmt.Println("again:", l.Type(), l.Text(), d.Stack().Equal(saved))
```

The output:

```text
WORD  "say"  top=code depth=1
WS    " "    top=code depth=1
QUOTE "\""   top=code depth=1
TEXT  "hi"   top=str depth=2
QUOTE "\""   top=str depth=2
WS    " "    top=code depth=1
WORD  "now"  top=code depth=1
again: TEXT hi true
```

The code is `ExampleModeSet` in
[`modes/example_test.go`](../../modes/example_test.go). A pop in the bottom
mode does nothing, and the scan stays in that mode. The exact contract is
[The mode state](../specs/resumable-lexing.md#the-mode-state).

## Rules

Each layer has the rules of its own format: see the guide for each format,
such as [line-protocol.md](line-protocol.md) and
[data-format.md](data-format.md). Give the types of one layer a prefix
(`JSON_`) when a consumer reads both layers from one stream.

## Traps

- **Inner offsets are local to the inner stream.** Above, `apples` is at
  offset 3 of the joined text, not of the input. To map an inner token back
  to the input, record the outer tokens the pipe read.
- **Bound the queue.** The outer stream can carry no inner text for a long
  time, and a pipe that queues outer tokens then grows without bound. When
  the queue is full, the ssejson pipe stops reading the outer layer and
  returns a keep-alive of line feeds, which the inner layer lexes as line
  ends, so the inner scanner returns and the driver can drain the queue.
- **The layers interleave by read order, not by position.** The inner cursor
  reads ahead before it decodes a rune, so the outer layer runs ahead of the
  inner one. A combined stream returns the outer tokens in the order the pipe
  read them.
- **Each cursor follows the rules of a mode driver separately.** Release the
  outgoing scanner at every switch on each cursor, and never run two scanners of
  one cursor at the same time.
- **Every token of both layers must be bounded**, so that the queue bound
  holds in bytes and not only in tokens.
- **Join the pieces of one value unchanged.** A bounded outer rule cuts a
  long value into pieces. Add nothing between them: a separator belongs only
  where the outer value ends. Above, the body `12 apples` comes in the pieces
  `12 apple` and `s`, and the inner scanner still reads the word `apples`.
- **Return at most `len(b)` bytes from `Read`**, and keep the rest for the
  next call.

## Tests to write

- A golden test of the combined stream, with inner tokens that cross the
  outer line breaks and pieces.
- A test that the inner layer alone, run on the joined text, gives the same
  inner tokens.
- A long-run test with `streamtest.Run`, with a ceiling for the queue set by
  `streamtest.WithCeiling` when the queue holds more than the default allows
  (see [operations.md](operations.md)).
- A test of a long outer stream with no inner text, which proves the queue
  bound.

## Examples

[ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson): JSON inside Server-Sent
Events, two cursors, a bounded queue, and a combined driver.
