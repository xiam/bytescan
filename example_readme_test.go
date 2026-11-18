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
	lx := bytescan.New(strings.NewReader("say hello\nto the world"))

	// "say" is both a keyword and a word: the rule added first wins.
	lx.MustAddRule("KEYWORD", core.Keyword("say", core.UnicodeLetter))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WHITESPACE", base.Whitespace())
	lx.MustAddRule("NEWLINE", base.Newline())

	for {
		tok, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		if tok.Type() == "KEYWORD" || tok.Type() == "WORD" {
			fmt.Printf("line %d, column %d: %s %q\n",
				tok.Line(), tok.Column(), tok.Type(), tok.Text())
		}
	}
	// Output:
	// line 1, column 0: KEYWORD "say"
	// line 1, column 4: WORD "hello"
	// line 2, column 0: WORD "to"
	// line 2, column 3: WORD "the"
	// line 2, column 7: WORD "world"
}

// Example_binary is the second README example: the body of its main function.
func Example_binary() {
	frames := []byte{0x01, 0x02, 0xDB, 0xDC, 0x03, 0xC0, 0xDB, 0xDD, 0xFF, 0xC0}

	// bytesource lets the scanner read bytes: each byte is one symbol.
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(frames)))

	// DATA is any byte but C0 and DB, at most 1000 bytes in one token.
	data := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	lx.MustAddRule("END", octet.Set(0xC0))
	lx.MustAddRule("DATA", core.RuneRunMax(data, 1000))
	lx.MustAddRule("ESC_END", octet.Literal([]byte{0xDB, 0xDC}))
	lx.MustAddRule("ESC_ESC", octet.Literal([]byte{0xDB, 0xDD}))

	for {
		tok, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		fmt.Println(bytesource.Hex(tok))
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
