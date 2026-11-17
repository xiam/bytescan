package bytesource_test

import (
	"bytes"
	"fmt"
	"io"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/core"
)

// records is a driver for a stream of records: a 1-byte length, then that
// many bytes of data, read in pieces of at most 2 bytes. It is the
// bytescan.Source of the stream; its Fields queue is not.
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

func ExampleFields() {
	in := []byte{3, 'a', 'b', 'c', 0, 1, 0xFF}
	src := newRecords(bytes.NewReader(in))
	for {
		l, err := src.Next()
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
	// 0 LEN 1 03
	// 1 DATA 2 61 62
	// 3 DATA 1 63
	// 4 LEN 1 00
	// 5 LEN 1 01
	// 6 DATA 1 ff
}
