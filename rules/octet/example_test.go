package octet_test

import (
	"bytes"
	"fmt"
	"io"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

// A scanner of a small binary stream with three kinds of item: a record, the
// signature CA FE and 2 bytes; a length, the tag byte 10 and a varint of 1 to
// 4 bytes; and a separator, the byte 00 or FF. Each rule starts with its own
// bytes, so no two rules match the same text.
func Example() {
	in := []byte{
		0xCA, 0xFE, 0x01, 0xFF, // a record
		0x10, 0x96, 0x01, // a length: the varint 150
		0xFF,             // a separator
		0xCA, 0xFE, 0x02, // a record cut by the end of the input
	}

	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(in)))
	lx.MustAddRule("RECORD", octet.Record([]byte{0xCA, 0xFE}, 2))
	lx.MustAddRule("LENGTH", core.Sequence(octet.Literal([]byte{0x10}), octet.Uvarint(4)))
	lx.MustAddRule("SEP", octet.Set(0x00, 0xFF))

	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(bytesource.Hex(l))
	}
	// Output:
	// 0 RECORD 4 ca fe 01 ff
	// 4 LENGTH 3 10 96 01
	// 7 SEP 1 ff
	// 8 UNKNOWN 1 ca
	// 9 UNKNOWN 1 fe
	// 10 UNKNOWN 1 02
}
