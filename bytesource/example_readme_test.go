package bytesource_test

import (
	"bytes"
	"fmt"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/octet"
)

// The example in this file holds the code of bytesource/README.md, so that
// the code block of that README compiles and runs. Keep the two in step: a
// change to one is a change to the other.

// Example is the example of bytesource/README.md: the body of a main
// function.
func Example() {
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
	// Output:
	// 0 LEN 1 03
	// 1 DATA 3 61 62 63
	// 4 LEN 1 00
	// 5 LEN 1 02
	// 6 DATA 2 68 69
}
