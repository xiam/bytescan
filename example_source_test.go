package bytescan_test

import (
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// newWordScanner returns a scanner for words, spaces and line breaks.
func newWordScanner(text string) *bytescan.Scanner {
	lx := bytescan.New(strings.NewReader(text))
	lx.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	lx.MustAddRule("SPACE", core.RuneRun(core.RuneSet(" ")))
	lx.MustAddRule("NL", core.Literal("\n"))
	return lx
}

// printAll writes one line per token of src until io.EOF.
func printAll(src bytescan.Source) {
	for {
		l, err := src.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("%d:%d %s %q\n", l.Line(), l.Column(), l.Type(), l.Text())
	}
}

func ExampleSkip() {
	src := bytescan.Skip(newWordScanner("one two\nthree"), "SPACE", "NL")
	printAll(src)
	// Output:
	// 1:0 WORD "one"
	// 1:4 WORD "two"
	// 2:0 WORD "three"
}

// relabel is a filter that turns every NL into a NEWLINE.
type relabel struct {
	src bytescan.Source
}

func (f relabel) Next() (*bytescan.Token, error) {
	l, err := f.src.Next()
	if err != nil {
		return nil, err
	}
	if l.Type() == "NL" {
		return l.WithType("NEWLINE"), nil
	}
	return l, nil
}

func ExampleToken_WithType() {
	printAll(bytescan.Skip(relabel{newWordScanner("one\ntwo")}, "SPACE"))
	// Output:
	// 1:0 WORD "one"
	// 1:3 NEWLINE "\n"
	// 2:0 WORD "two"
}

func ExampleNewEmptyToken() {
	lx := newWordScanner("one")
	l, _ := lx.Next()
	// An END token with no text where the last token ends.
	end := bytescan.NewEmptyToken("END", l.Span().End())
	fmt.Printf("%d:%d %s %q %d\n", end.Line(), end.Column(), end.Type(), end.Text(), end.Len())
	// Output:
	// 1:3 END "" 0
}
