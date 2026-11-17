# Guide: a length-prefixed binary protocol

MQTT, WebSocket and syslog over TCP with octet counting put the length of a
field in front of it. A rule cannot count to a number it reads, so the work
is split: the rules lex the fixed header and the length, and a driver decodes
the length and reads the field from the cursor.

## The split

1. **Rules lex the header and the length.** Use the rules of `rules/octet`
   over a `bytesource.NewCursor`: `octet.Any()` or `octet.Fixed(n)` for a
   fixed header, `octet.Uvarint(maxBytes)` for a variable-length integer
   (MQTT's remaining length). A decimal length in text, as in syslog, is a
   bounded digit run.
2. **The driver decodes the length.** It reads the bytes of the length token
   with `bytesource.Bytes(l.Text())` and computes the value.
3. **The driver releases the scanner and queues the field.** It calls `Release`
   on the scanner (or switches away from it on a `modes.Driver`), then pushes a
   `bytesource.Field` with the length on a `bytesource.Fields` queue.
4. **The driver reads the field in pieces.** `NextField` returns one token
   per piece of at most `MaxPiece` bytes, then a nil token when the queue is
   empty, and the driver goes back to its scanner.

The driver, not the queue, is the `bytescan.Source` of the stream.

```go
type records struct {
	lx     *bytescan.Scanner
	fields *bytesource.Fields
}

func newRecords(r io.Reader) *records {
	cur := bytesource.NewCursor(r)
	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule("LEN", core.Rune(bytesource.AnyByte))
	return &records{lx: lx, fields: bytesource.NewFields(cur)}
}

// Next returns the next queued piece of data, or else the next length.
func (d *records) Next() (*bytescan.Token, error) {
	if l, err := d.fields.NextField(); l != nil || err != nil {
		return l, err
	}
	// The queue is empty: back to the scanner.
	l, err := d.lx.Next()
	if err != nil {
		return nil, err
	}
	if l.Type() == "LEN" {
		d.lx.Release()
		n := int(bytesource.Bytes(l.Text())[0])
		d.fields.Push(bytesource.Field{Type: "DATA", Len: n, MaxPiece: 2})
	}
	return l, nil
}

var _ bytescan.Source = (*records)(nil)
```

The code is the `records` driver of `ExampleFields` in
[`bytesource/example_test.go`](../../bytesource/example_test.go): a 1-byte
length, then that many bytes, read in pieces of at most 2 bytes.

## Registration order

A header scanner usually has one rule per scanner, so no tie arises. When a
protocol has several scanners, one per place (header, length, bad length in
MQTT), run them on a `modes.Driver` and give each scanner the rules of its place
only.

## Traps

- **Never allocate from a declared length.** A corrupt or hostile length can
  say 4 GiB. `bytesource.Read` allocates for the bytes it reads, not for the
  length, and a chunked field (`MaxPiece` above 0) holds one piece at a time.
  Use a piece size such as 64 KiB.
- **Release before you read.** A scanner keeps checkpoints on the cursor for its
  last tokens. If it is not released, they pin the field that the driver
  reads, and a cursor with a retention budget fails. `modes.Driver.Use`
  releases the outgoing scanner for you.
- **Peek when the length is part of the field.** `bytesource.Peek(cur, n)`
  reads ahead without moving the cursor, as for an MQTT topic length or a BMP
  header size.
- **A length can be larger than an int.** WebSocket's 64-bit length does not
  fit a `Field.Len` on every platform. Queue such a field as several fields
  in a row.
- **Without a sync marker there is no resynchronisation.** After a corrupt
  length the driver frames packets wherever the corrupt bytes put them. Say
  so in your documentation; do not guess a frame start.
- **`Offset` and `Len` are the byte positions.** Through `bytesource`,
  `ByteOffset`, `Line` and `Column` are not positions in the input, and the
  token guard counts a byte of 0x80 or above as two bytes. These are current
  limits of the cursor.

## Tests to write

- A golden test of each packet type, in hex (`bytesource.Hex` prints one line
  per token), with the cut cases: a length that runs past the end of the
  input, a length of 0.
- A release test: `streamtest.LexRetained` lexes over a cursor that retains
  at most `n` bytes, with a field longer than `n`. It fails when the driver
  does not release its scanner before it reads.
- A test of the largest length the protocol allows, put before a generated
  stream with `streamtest.Prefix`, which proves that memory follows the piece
  size.
- A long-run test with `streamtest.Run` (see [operations.md](operations.md)).

## Examples

[mqtt](https://github.com/xiam/bytescan-examples/tree/main/streams/mqtt) (a variable-length remaining
length), [websocket](https://github.com/xiam/bytescan-examples/tree/main/streams/websocket) (a 64-bit
length in int-sized fields), [syslog](https://github.com/xiam/bytescan-examples/tree/main/streams/syslog)
(a decimal length prefix), [nats](https://github.com/xiam/bytescan-examples/tree/main/streams/nats) (text
control lines with byte-counted payloads), and the binary files
[zip](https://github.com/xiam/bytescan-examples/tree/main/binary/zip),
[bmp](https://github.com/xiam/bytescan-examples/tree/main/binary/bmp),
[wav](https://github.com/xiam/bytescan-examples/tree/main/binary/wav),
[jpeg](https://github.com/xiam/bytescan-examples/tree/main/binary/jpeg) and
[mp3](https://github.com/xiam/bytescan-examples/tree/main/binary/mp3), which read the fields their headers
announce the same way.
