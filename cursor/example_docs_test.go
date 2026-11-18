package cursor_test

import (
	"fmt"
	"strings"

	"github.com/xiam/bytescan/cursor"
)

// The examples in this file hold the code of docs/cursor.md, so that every
// code block of that page compiles and runs. Keep the two in step: a change to
// one is a change to the other.

// Example_readAhead is the read-ahead example of docs/cursor.md.
func Example_readAhead() {
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
	// Output:
	// "hello 世界\n"
	// 'h' at byte 0
}

// Example_remark is the checkpoint-reuse example of docs/cursor.md.
func Example_remark() {
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
	// Output:
	// a marked at byte 0
	// b marked at byte 1
	// c marked at byte 2
}
