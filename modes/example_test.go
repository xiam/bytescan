package modes_test

import (
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/core"
)

// A string in this small language is quoted with ' or ", and the other quote
// is plain text inside it. Code mode lexes words, spaces and the opening
// quote. After an opening quote the driver switches to the string scanner for
// that quote, which lexes the body and the closing quote; after the closing
// quote it switches back to code mode. The cache builds one string scanner per
// quote, on first use.
func Example() {
	cur := cursor.New(strings.NewReader(`say "it's" and 'a "b"' twice "x"`))

	code := bytescan.NewWithCursor(cur)
	code.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	code.MustAddRule("SPACE", core.RuneRun(core.RuneSet(" ")))
	code.MustAddRule("OPEN", core.Rune(core.RuneSet(`"'`)))

	strs := modes.NewCache(cur, func(cur bytescan.Cursor, quote string) *bytescan.Scanner {
		lx := bytescan.NewWithCursor(cur)
		lx.MustAddRule("BODY", core.RuneRun(core.RuneSet(quote).Not()))
		lx.MustAddRule("CLOSE", core.Literal(quote))
		return lx
	})

	d := modes.New(cur, code, modes.WithSwitch(func(_ *bytescan.Scanner, l *bytescan.Token) *bytescan.Scanner {
		switch l.Type() {
		case "OPEN":
			return strs.Get(l.Text())
		case "CLOSE":
			return code
		}
		return nil
	}))

	for {
		l, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("%s %q\n", l.Type(), l.Text())
	}
	fmt.Println("string scanners:", strs.Len())

	// Output:
	// WORD "say"
	// SPACE " "
	// OPEN "\""
	// BODY "it's"
	// CLOSE "\""
	// SPACE " "
	// WORD "and"
	// SPACE " "
	// OPEN "'"
	// BODY "a \"b\""
	// CLOSE "'"
	// SPACE " "
	// WORD "twice"
	// SPACE " "
	// OPEN "\""
	// BODY "x"
	// CLOSE "\""
	// string scanners: 2
}
