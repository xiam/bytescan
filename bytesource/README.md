# bytesource

Package `bytesource` lets bytescan read bytes instead of text. Each byte is
one symbol, so the same rules and the same loop work on binary files and
network protocols. Every position is a byte offset in the input.

When the data holds the length of a field, your code reads the length and
`bytesource` reads the field: a rule cannot count to a number it reads.

## Example: records with a length byte

Each record is a length byte, then that many bytes. The second record is empty.

```go
data := []byte{3, 'a', 'b', 'c', 0, 2, 'h', 'i'}
cur := bytesource.NewCursor(bytes.NewReader(data))
lx := bytescan.NewWithCursor(cur)
lx.MustAddRule("LEN", octet.Any())

// The loop stops at the first error: io.EOF at the end of the input.
for tok, err := lx.Next(); err == nil; tok, err = lx.Next() {
	fmt.Println(bytesource.Hex(tok))

	// The length byte says how many bytes follow. Release drops the input
	// the scanner holds, then Read takes the field from the same cursor.
	n := int(bytesource.Bytes(tok.Text())[0])
	lx.Release()
	field, err := bytesource.Read(cur, "DATA", n)
	if err != nil {
		break
	}
	if field != nil { // a length of 0 reads nothing
		fmt.Println(bytesource.Hex(field))
	}
}
```

Output (byte offset, type, length, bytes):

```
0 LEN 1 03
1 DATA 3 61 62 63
4 LEN 1 00
5 LEN 1 02
6 DATA 2 68 69
```

The example runs, with its imports, as a test in
[`example_readme_test.go`](example_readme_test.go).

## Learn more

- [A length-prefixed protocol](../docs/guides/length-prefixed.md): long fields read in pieces, with `Fields`.
- [Escape-framed bytes](../docs/guides/escape-framed.md) and [packets with a sync marker](../docs/guides/sync-marker.md).
- [Byte reads](../docs/specs/layers.md#byte-reads-bytesource): the exact contract.
- The package documentation: `go doc github.com/xiam/bytescan/bytesource`.
