package cursor_test

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan/cursor"
)

// The examples in this file hold the code of cursor/README.md, so that every
// code block of that README compiles and runs. Keep the two in step: a change
// to one is a change to the other.

// Example_text is the text example of cursor/README.md: the body of its main
// function.
func Example_text() {
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
	// Output:
	// 'h' line 1, column 0, byte 0
	// 'i' line 1, column 1, byte 1
	// '\n' line 1, column 2, byte 2
	// '世' line 2, column 0, byte 3
	// '界' line 2, column 1, byte 6
}

// Example_bytes is the byte example of cursor/README.md: the body of its main
// function.
func Example_bytes() {
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
	// Output:
	// byte 0: 0x89
	// byte 1: 0x50
	// byte 2: 0x4e
	// byte 3: 0x47
	// again, byte 0: 0x89
}
