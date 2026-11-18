# cursor

Package `cursor` reads text or bytes and always knows where it is. For every
character it reads, it tells you the line, the column and the byte offset. It
can also mark a place, read ahead, and go back to the mark.

The cursor works on its own. It is part of the bytescan module, but it imports
only the Go standard library, and you do not need the scanner to use it.

## Example: text

Read two lines and print where each character is.

```go
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan/cursor"
)

func main() {
	// Read some text, one character (rune) at a time.
	r := cursor.New(strings.NewReader("hi\n世界"))

	for {
		lr, err := r.ReadLocatedRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}

		// Every rune knows where it came from.
		pos := lr.Pos()
		fmt.Printf("%q line %d, column %d, byte %d\n",
			lr.Rune(), pos.Line(), pos.Column(), pos.ByteOffset())
	}
}
```

Output:

```
'h' line 1, column 0, byte 0
'i' line 1, column 1, byte 1
'\n' line 1, column 2, byte 2
'世' line 2, column 0, byte 3
'界' line 2, column 1, byte 6
```

The column counts characters. The byte offset counts bytes: `世` takes three.

## Example: bytes

Read the first bytes of a PNG file, then go back and read the first one again.

```go
package main

import (
	"bytes"
	"fmt"

	"github.com/xiam/bytescan/cursor"
)

func main() {
	// The first bytes of a PNG file.
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

	// In byte mode, every byte is one symbol and every offset is a file offset.
	r := cursor.NewByteReader(bytes.NewReader(data))

	// Mark the start. Release lets go of the mark when we are done.
	mark := r.Checkpoint()
	defer mark.Release()

	// Read four bytes, go back to the mark, and read the first byte again.
	for i := 0; i < 4; i++ {
		lr, _ := r.ReadLocatedRune()
		fmt.Printf("byte %d: %#02x\n", lr.Pos().ByteOffset(), lr.Rune())
	}
	mark.Reset()
	lr, _ := r.ReadLocatedRune()
	fmt.Printf("again, byte %d: %#02x\n", lr.Pos().ByteOffset(), lr.Rune())
}
```

Output:

```
byte 0: 0x89
byte 1: 0x50
byte 2: 0x4e
byte 3: 0x47
again, byte 0: 0x89
```

Both examples run as tests in [`example_readme_test.go`](example_readme_test.go).

## Learn more

- [Using the cursor](../docs/cursor.md): checkpoints, a memory limit, reusing
  checkpoints, rune sources, byte mode and the limits of `Seek`.
- [The cursor contract](../docs/specs/cursor.md): the exact behaviour, with
  the tests that check it.
- The package documentation: `go doc github.com/xiam/bytescan/cursor`.

## License

MIT License. See [LICENSE.md](../LICENSE.md).
