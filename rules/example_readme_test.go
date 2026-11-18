package rules_test

import (
	"fmt"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/special"
)

// The example in this file holds the code of rules/README.md, so that the
// code block of that README compiles and runs. Keep the two in step: a change
// to one is a change to the other.

// Example is the example of rules/README.md: the body of a main function.
func Example() {
	lx := bytescan.New(strings.NewReader("read https://go.dev 2 times"))
	lx.MustAddRule("URL", special.URL())
	lx.MustAddRule("NUMBER", base.Integer())
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("SPACE", base.Whitespace())

	// The loop stops at the first error: io.EOF at the end of the input.
	for tok, err := lx.Next(); err == nil; tok, err = lx.Next() {
		if tok.Type() != "SPACE" {
			fmt.Printf("%s %q\n", tok.Type(), tok.Text())
		}
	}
	// Output:
	// WORD "read"
	// URL "https://go.dev"
	// NUMBER "2"
	// WORD "times"
}
