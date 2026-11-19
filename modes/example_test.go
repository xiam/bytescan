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

// The modes of a small language with strings, and the mode state at the start
// of each token. Each rule carries its action: a quote in code pushes the
// string mode, and a quote in a string pops it. The driver applies the
// actions; there is no switching code. A saved state is a copy, and a scan
// that starts with it at the same place goes on as the first scan did.
func ExampleModeSet() {
	word := core.RuneRun(core.RuneRange('a', 'z'))
	quote := core.Literal(`"`)

	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", word).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Push("QUOTE", quote, "str")
	ms.Mode("str").
		Rule("TEXT", core.RuneRun(core.RuneSet(`"`).Not())).
		Pop("QUOTE", quote)

	text := `say "hi" now`
	d, err := ms.Driver(cursor.New(strings.NewReader(text)), modes.Stack{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	var (
		saved modes.Stack
		at    int
	)
	for {
		st := d.Stack() // the mode state at the start of the next token
		l, err := d.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("%-5s %-6q top=%s depth=%d\n", l.Type(), l.Text(), st.Top(), st.Len())
		if l.Text() == "hi" {
			saved, at = st, l.Offset()
		}
	}

	// Start again at "hi", in the state saved there.
	d, err = ms.Driver(cursor.New(strings.NewReader(text[at:])), saved)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	l, _ := d.Next()
	fmt.Println("again:", l.Type(), l.Text(), d.Stack().Equal(saved))

	// Output:
	// WORD  "say"  top=code depth=1
	// WS    " "    top=code depth=1
	// QUOTE "\""   top=code depth=1
	// TEXT  "hi"   top=str depth=2
	// QUOTE "\""   top=str depth=2
	// WS    " "    top=code depth=1
	// WORD  "now"  top=code depth=1
	// again: TEXT hi true
}
