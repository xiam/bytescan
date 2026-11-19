package statelex_test

import (
	"fmt"

	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/statelex"
)

// The implementer's side: build the lexer, load a text, make one edit, and
// apply the change to a list of tokens that the implementer keeps.
func Example() {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Rule("NL", core.Literal("\n"))
	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}

	// Loading the text is one edit of an empty lexer.
	ch, err := lx.Apply(statelex.Insert(0, []byte("foo bar\n")))
	if err != nil {
		panic(err)
	}
	toks := ch.Inserted
	fmt.Println(toks)

	// Delete the space: "foo bar" becomes "foobar".
	ch, err = lx.Apply(statelex.Delete(3, 4))
	if err != nil {
		panic(err)
	}
	fmt.Println("removed", ch.Removed, "inserted", ch.Inserted, "shift", ch.Shift())

	// Apply the change: drop the tokens in [Start, OldEnd), add the inserted
	// tokens, and move every later token by Shift().
	var next []statelex.Token
	for _, tok := range toks {
		if tok.End <= ch.Start {
			next = append(next, tok)
		}
	}
	next = append(next, ch.Inserted...)
	for _, tok := range toks {
		if tok.Start >= ch.OldEnd {
			tok.Start += ch.Shift()
			tok.End += ch.Shift()
			next = append(next, tok)
		}
	}
	toks = next
	fmt.Println(toks)

	// Output:
	// [{WORD 0 3} {WS 3 4} {WORD 4 7} {NL 7 8}]
	// removed [{WORD 0 3} {WS 3 4} {WORD 4 7}] inserted [{WORD 0 6}] shift -1
	// [{WORD 0 6} {NL 6 7}]
}
