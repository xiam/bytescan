# Guide: escape-framed bytes

SLIP (RFC 1055) frames packets on a serial line with a delimiter byte, and
escapes the delimiter inside the data with a two-byte pair. Every token is
decided by its bytes, so one scanner with four rules lexes it: no driver, no
mode and no length.

## Rules

Read the bytes through `bytesource.NewCursor` and match them with the rules
of `rules/octet` and `rules/core`:

- **The delimiter:** `octet.Set(0xC0)`.
- **The data:** `core.RuneRunMax(dataByte, n)`, a bounded run of every byte
  except the delimiter and the escape byte. Build the class from
  `bytesource.AnyByte`.
- **Each escape pair:** one `octet.Literal`, with its own type: `DB DC`
  stands for a data byte C0, and `DB DD` for a data byte DB.

## Registration order

No order is needed. The rules start with different bytes, except the two
escape pairs, which differ in their second byte.

```go
data := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
lx.MustAddRule("END", octet.Set(0xC0))
lx.MustAddRule("DATA", core.RuneRunMax(data, 1000))
lx.MustAddRule("ESC_END", octet.Literal([]byte{0xDB, 0xDC}))
lx.MustAddRule("ESC_ESC", octet.Literal([]byte{0xDB, 0xDD}))
```

The code is in `Example_binary` in
[`example_readme_test.go`](../../example_readme_test.go), the binary example of
the [README](../../README.md#example-binary).

## Traps

- **Use `bytesource.Text` for bytes of 0x80 or above.** A Go string such as
  `"\xC0"` is not valid UTF-8: `core.RuneSet("\xC0")` decodes it to U+FFFD and
  never matches the byte. Write `core.RuneSet(bytesource.Text("\xC0\xDB"))`,
  or use the rules of `rules/octet`, which take bytes.
- **Bound the data run.** A stream that has lost its delimiters would grow one
  `DATA` token without bound. With `RuneRunMax` it comes out in pieces.
- **Leave a lone escape byte as `UNKNOWN`.** An ESC followed by any other byte
  matches no rule: the ESC is `UNKNOWN`, and the byte after it is lexed on its
  own, so a delimiter after a lone ESC still ends the frame.
- **Do not unescape in the scanner.** The consumer turns `ESC_END` into C0 and
  `ESC_ESC` into DB when it rebuilds the packet.
- **The guard counts a byte of 0x80 or above as two bytes** with the current
  cursor. Set it to twice the largest token in bytes
  ([operations.md](operations.md)).

## Tests to write

- A golden test in hex with frames that hold each escape pair, empty frames,
  a lone escape byte, and an escape byte at the end of the input.
- A long-run test with `streamtest.Run` and a test of a stream with no
  delimiter: bounded `DATA` pieces and no error.

## Examples

[slip](https://github.com/xiam/bytescan-examples/tree/main/streams/slip): a delimiter byte and byte
stuffing, with byte rules.
