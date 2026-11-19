package statelex_test

import (
	"fmt"

	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/statelex"
)

// load returns a lexer of words, spaces and newlines that holds text.
func load(text string) *statelex.Lexer {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Rule("NL", core.Literal("\n"))
	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}
	if _, err := lx.Apply(statelex.Insert(0, []byte(text))); err != nil {
		panic(err)
	}
	return lx
}

// A highlighter reads the bytes of the visible part of the text.
func ExampleLexer_Text() {
	lx := load("foo bar\nbaz\n")
	fmt.Printf("%q\n", lx.Text(4, 11))
	fmt.Printf("%q\n", lx.Text(8, 100)) // clipped to Len()
	fmt.Printf("%q\n", lx.Text(5, 2))   // inverted: no byte
	// Output:
	// "bar\nbaz"
	// "baz\n"
	// ""
}

// A highlighter reads the tokens of the visible part of the text. A token that
// crosses an end of the range is returned whole.
func ExampleLexer_Tokens() {
	lx := load("foo bar\nbaz\n")
	fmt.Println(lx.Tokens(5, 9))
	// Output:
	// [{WORD 4 7} {NL 7 8} {WORD 8 11}]
}

// A REPL sees an open string through the mode state at the end of the text.
func ExampleLexer_ModeAtEnd() {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Push("QUOTE", core.Literal(`"`), "str")
	ms.Mode("str").
		Rule("TEXT", core.RuneRun(core.RuneSet(`"`).Not())).
		Pop("QUOTE", core.Literal(`"`))
	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}
	for _, text := range []string{`say "`, `say "hi"`} {
		if _, err := lx.SetText([]byte(text)); err != nil {
			panic(err)
		}
		fmt.Printf("%s: top mode %s, depth %d\n", text, lx.ModeAtEnd().Top(), lx.ModeAtEnd().Len())
	}
	// Output:
	// say ": top mode str, depth 2
	// say "hi": top mode code, depth 1
}

// A REPL sees an open comment through the first token whose rules read the end
// of the text.
func ExampleLexer_Unfinished() {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("OP", core.AnyLiteral("/", "*")).
		Rule("COMMENT", base.BlockComment("/*", "*/")).
		Rule("WS", core.RuneRun(core.RuneSet(" \n")))
	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}
	for _, text := range []string{"a /* x\nb\n", "a /* x */"} {
		if _, err := lx.SetText([]byte(text)); err != nil {
			panic(err)
		}
		fmt.Println(lx.Unfinished())
	}
	// Output:
	// 2 true
	// 0 false
}

// Position gives the line, from 1, and the rune column, from 0, of a byte
// offset.
func ExampleLexer_Position() {
	lx := load("foo\nñandú bar\n")
	line, column, err := lx.Position(11) // the space after "ñandú"
	fmt.Println(line, column, err)
	_, _, err = lx.Position(5) // the second byte of "ñ"
	fmt.Println(err)
	// Output:
	// 2 5 <nil>
	// statelex: offset splits a UTF-8 character: offset 5
}

// Offset gives the byte offset of a line and a rune column: an editor that
// counts lines and columns makes an edit with it.
func ExampleLexer_Offset() {
	lx := load("foo\nñandú bar\n")
	off, err := lx.Offset(2, 6) // "bar"
	fmt.Println(off, err)
	fmt.Printf("%q\n", lx.Text(off, off+3))
	_, err = lx.Offset(2, 10) // line 2 has 9 runes and its newline
	fmt.Println(err)
	// Output:
	// 12 <nil>
	// "bar"
	// statelex: out of range: line 2 has 9 columns, not 10
}
