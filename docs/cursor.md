# Using the cursor

The [`cursor`](../cursor) package reads text or bytes and tracks the exact
position of every read. This page shows how to use it on its own: reading
ahead and going back, limiting memory, reusing checkpoints, rune sources and
byte mode. The exact behaviour, with the tests that check it, is in the
[cursor contract](specs/cursor.md).

## When to use it

Use the cursor when you need to say where something is in the input:

- a lexer or a parser that reports the line and the column of an error;
- a reader of configuration files or templates that points at a mistake;
- any reader that must look ahead before it decides what it read.

Do not use it for random access to a large file (use `os.File`), or for fast
reading where you do not need positions. For binary data, use
[byte mode](#byte-mode), not the text constructors, which decode UTF-8.

## Reading

`cursor.New(r)` and `cursor.NewReader(r, opts...)` read an `io.Reader` as
UTF-8 text. The reader works like a buffered reader: `Read`, `ReadRune`,
`UnreadRune`, `Seek` and `Pos`. On top of that:

- `ReadLocatedRune` returns a rune with the position it was read from.
- `PeekRune` shows the next rune without reading it.
- `Cursor()` returns the position of the next read: byte offset, rune offset,
  1-based line and 0-based column. The column counts characters (runes), the
  byte offset counts bytes.
- `NewSpan(start, end)` builds a range of the input, and `SpanText` and
  `Context` return its text, and a little around it, without moving the
  cursor.

## Read ahead and go back

A checkpoint marks a place. You read ahead, then go back to the mark with
`Reset`, or keep what you read with `Commit`. `Release` drops the mark
wherever the cursor is.

```go
r := cursor.New(strings.NewReader("hello 世界\nnext line"))

// Mark the place, then read ahead to the end of the line.
mark := r.Checkpoint()
defer mark.Release()

first, _ := r.ReadLocatedRune()
last := first
for last.Rune() != '\n' {
	last, _ = r.ReadLocatedRune()
}

// The text of what we read, and a little of what is around it.
span := cursor.NewSpan(first.Pos(), last.End())
text, _ := r.SpanText(span)
fmt.Printf("%q\n", text)

// Go back to the mark: the next read starts at "h" again.
mark.Reset()
again, _ := r.ReadLocatedRune()
fmt.Printf("%q at byte %d\n", again.Rune(), again.Pos().ByteOffset())
```

Output:

```
"hello 世界\n"
'h' at byte 0
```

`Example_readAhead` in
[`cursor/example_docs_test.go`](../cursor/example_docs_test.go) runs this code.

Things to know:

- You can `Reset` as often as you like, until you `Commit` or `Release`.
  After that, `Reset`, `Commit` and `Release` return `ErrCheckpointReleased`.
  `Pos` still tells you where the mark was.
- `Commit` fails with `ErrCheckpointRewound` if the cursor is behind the mark.
- Checkpoints nest. The cursor keeps the input from the oldest open
  checkpoint, so releasing an inner one does not free what an outer one needs.
- While a checkpoint is open, the input from the mark on stays in the buffer,
  so `Seek` can always go back to the mark. `Seek` may also reach bytes before
  the mark that are still in the buffer; `Commit` fails while the cursor is
  before the mark.
- Once the last checkpoint is closed, the cursor stops keeping that input, and
  `SpanText` or `Context` on it returns `ErrPositionOutOfBuffer`.

## Limit the memory

An open checkpoint keeps every byte read since the mark. On a long stream,
set a limit, for example
`cursor.NewReader(src, cursor.WithCapacity(4096), cursor.WithMaxRetained(1<<20))`.

- The limit counts only while a checkpoint is open. Without a checkpoint the
  reader keeps nothing extra, so the default is no limit.
- A read that would go over the limit fails with `ErrRetentionExceeded` and
  reads nothing. The limit is checked against the bytes the read would really
  take, not the size of your buffer, so a large buffer alone is not refused.
- `WithCapacity` sets the buffer size, which bounds how far the reader reads
  ahead of the cursor.
- Without a limit, the buffer grows as needed, so a very long token can still
  be replayed.

## Reuse checkpoints

`Checkpoint` allocates a new value each time. A lexer that marks every token
can instead re-arm one value with `Remark`, after releasing it:

```go
r := cursor.New(strings.NewReader("abc"))

// One checkpoint value, used again for every mark.
slot := r.Checkpoint()
for {
	lr, err := r.ReadLocatedRune()
	if err != nil {
		break
	}
	fmt.Printf("%c marked at byte %d\n", lr.Rune(), slot.Pos().ByteOffset())

	// End this mark, then re-arm the same value at the cursor.
	if err := slot.Release(); err != nil {
		panic(err)
	}
	if err := r.Remark(slot); err != nil {
		panic(err)
	}
}
_ = slot.Release()
```

Output:

```
a marked at byte 0
b marked at byte 1
c marked at byte 2
```

`Example_remark` in
[`cursor/example_docs_test.go`](../cursor/example_docs_test.go) runs this code.

- Only a released checkpoint can be re-armed. An open one is refused with
  `ErrCheckpointActive`.
- A re-armed checkpoint works exactly like a new one.
- **One owner per slot.** Re-arming changes what every copy of the pointer
  refers to. If you keep an old reference after releasing the slot, that
  reference now controls the new mark, and the reader cannot detect it.

## Rune sources

`cursor.NewRuneReader(rr)` wraps an `io.RuneReader`. Use it when your source
already decodes runes; use `New` or `NewReader` when you have bytes, so that
only one decoder runs. If the source returns an error, replaying from a
checkpoint returns the same error again.

## Byte mode

`cursor.NewByteReader(r, opts...)` reads binary data. Each byte is one rune,
from U+0000 to U+00FF, and nothing is decoded.

- Every offset is a file offset.
- The byte 0x0A ends a line, and the column counts bytes since it, so a
  protocol that mixes text lines and binary fields keeps its line numbers.
- `Read`, `SpanText` and `Context` return the raw bytes.
- Checkpoints, `Seek`, `Remark` and `WithMaxRetained` count bytes.

The scanner uses byte mode through
[`bytesource.NewCursor`](../bytesource).

## Limits

- `Seek` moves only inside the reader's buffer. It cannot reach any place in
  the stream, as `os.File.Seek` can, and it does not move the underlying
  reader. `Seek(0, io.SeekStart)` fails once the start has left the buffer.
- `UnreadRune` undoes only the last `ReadRune`. To go further back, use `Seek`
  or a checkpoint.
- The text constructors count lines and columns correctly only for valid
  UTF-8. Byte mode counts bytes.
