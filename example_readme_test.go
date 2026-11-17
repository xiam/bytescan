package bytescan_test

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

// The examples in this file hold the code of the README, so that every code
// block of the README compiles and runs. Keep the two in step: a change to one
// is a change to the other.

// Example_text is the first README example: the body of its main function.
func Example_text() {
	// The text we want to classify.
	myText := "say hello to the world"

	// 1. Create a new scanner that reads our text.
	lx := bytescan.New(strings.NewReader(myText))

	// 2. Add rules for what we want to find. The order matters: "say" is
	//    both a keyword and a word, and the rule added first wins the tie.
	lx.MustAddRule("KEYWORD", core.Keyword("say", core.UnicodeLetter))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WHITESPACE", base.Whitespace())

	// 3. Loop and get the next labeled piece until we reach the end.
	for {
		token, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}

		// Skip over whitespace.
		if token.Type() == "WHITESPACE" {
			continue
		}

		// Print the piece we found!
		fmt.Printf("Found: [%s] \"%s\"\n", token.Type(), token.Text())
	}
	// Output:
	// Found: [KEYWORD] "say"
	// Found: [WORD] "hello"
	// Found: [WORD] "to"
	// Found: [WORD] "the"
	// Found: [WORD] "world"
}

// Example_binary is the second README example: the body of its main function.
func Example_binary() {
	// Two SLIP frames (RFC 1055). The byte C0 ends a frame. Inside a frame,
	// the pair DB DC stands for a data byte C0, and DB DD for a data byte DB.
	frames := []byte{
		0x01, 0x02, 0xDB, 0xDC, 0x03, 0xC0,
		0xDB, 0xDD, 0xFF, 0xC0,
	}

	// 1. Create a new scanner that reads the bytes through the byte adapter:
	//    each byte is one symbol.
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(frames)))

	// 2. Add a rule for each kind of byte sequence. A data run is any byte
	//    but C0 and DB, and at most 1000 bytes long, so a stream that never
	//    ends a frame still comes out in bounded pieces.
	data := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	lx.MustAddRule("END", octet.Set(0xC0))
	lx.MustAddRule("DATA", core.RuneRunMax(data, 1000))
	lx.MustAddRule("ESC_END", octet.Literal([]byte{0xDB, 0xDC}))
	lx.MustAddRule("ESC_ESC", octet.Literal([]byte{0xDB, 0xDD}))

	// 3. Print the byte offset, type, length and bytes of every token.
	for {
		token, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		fmt.Println(bytesource.Hex(token))
	}
	// Output:
	// 0 DATA 2 01 02
	// 2 ESC_END 2 db dc
	// 4 DATA 1 03
	// 5 END 1 c0
	// 6 ESC_ESC 2 db dd
	// 8 DATA 1 ff
	// 9 END 1 c0
}

// Example_spans holds the token-span accessors that the README lists.
func Example_spans() {
	lx := bytescan.New(strings.NewReader("one\ntwo"))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("NEWLINE", base.Newline())

	var tok *bytescan.Token
	for i := 0; i < 3; i++ {
		var err error
		if tok, err = lx.Next(); err != nil {
			panic(err)
		}
	}

	span := tok.Span()

	fmt.Println(span.Start().ByteOffset()) // byte offset of the first byte
	fmt.Println(span.Start().RuneOffset()) // rune offset of the first rune
	fmt.Println(span.Start().Line())       // 1-based line
	fmt.Println(span.Start().Column())     // 0-based rune column
	fmt.Println(span.End().ByteOffset())   // position just past the token
	fmt.Println(span.Bytes())              // width in bytes
	fmt.Println(span.Runes())              // width in runes
	// Output:
	// 4
	// 4
	// 2
	// 0
	// 7
	// 3
	// 3
}
