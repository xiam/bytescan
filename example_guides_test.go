package bytescan_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
	"github.com/xiam/bytescan/rules/special"
	"github.com/xiam/bytescan/statelex"
)

// The examples in this file hold the code of the guides under docs/guides, so
// that every code block of a guide compiles and runs. Each guide names the
// function that holds its code. Keep the two in step: a change to one is a
// change to the other. printAll is in example_source_test.go.

// Example_guideCode is the code of docs/guides/programming-language.md.
func Example_guideCode() {
	lx := bytescan.New(strings.NewReader("if x >= 10 { return x }\nreturned"))

	// "if" and "return" are keywords and identifiers of the same length:
	// the rule registered first wins the tie, so KEYWORD comes first.
	// Keywords matches a word only when no identifier part follows it, so
	// "returned" is an identifier.
	lx.MustAddRule("KEYWORD", core.Keywords("if", "else", "return"))
	lx.MustAddRule("IDENTIFIER", base.ASCIIIdentifier())
	lx.MustAddRule("NUMBER", base.Integer())
	// Every operator in one rule: the longest match picks ">=" over ">".
	lx.MustAddRule("OPERATOR", core.AnyLiteral("==", ">=", "<=", "=", ">", "<"))
	// Brackets get their own types, so a filter can count them.
	lx.MustAddRule("LBRACE", core.Literal("{"))
	lx.MustAddRule("RBRACE", core.Literal("}"))
	lx.MustAddRule("WHITESPACE", base.Whitespace())
	lx.MustAddRule("NEWLINE", base.Newline())

	printAll(bytescan.Skip(lx, "WHITESPACE"))
	// Output:
	// 1:0 KEYWORD "if"
	// 1:3 IDENTIFIER "x"
	// 1:5 OPERATOR ">="
	// 1:8 NUMBER "10"
	// 1:11 LBRACE "{"
	// 1:13 KEYWORD "return"
	// 1:20 IDENTIFIER "x"
	// 1:22 RBRACE "}"
	// 1:23 NEWLINE "\n"
	// 2:0 IDENTIFIER "returned"
}

// Example_guideData is the code of docs/guides/data-format.md.
func Example_guideData() {
	lx := bytescan.New(strings.NewReader(`{"a": [-1.5e3, "\u00e9"], "b": 01}`))

	// One literal per structural token.
	lx.MustAddRule("BEGIN_OBJECT", core.Literal("{"))
	lx.MustAddRule("END_OBJECT", core.Literal("}"))
	lx.MustAddRule("BEGIN_ARRAY", core.Literal("["))
	lx.MustAddRule("END_ARRAY", core.Literal("]"))
	lx.MustAddRule("NAME_SEPARATOR", core.Literal(":"))
	lx.MustAddRule("VALUE_SEPARATOR", core.Literal(","))

	// A string with the escape grammar of the format.
	plain := core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not()
	escape := core.Sequence(
		core.Literal(`\`),
		core.Choice(
			core.Rune(core.RuneSet(`"\/bfnrt`)),
			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
		),
	)
	lx.MustAddRule("STRING", base.QuotedWith('"', plain, escape))

	// A strict number: no leading zero, so "01" is two numbers, and the
	// sign composed in front.
	noLeadingZeros := base.NoLeadingZeros()
	lx.MustAddRule("NUMBER", core.Sequence(
		core.Optional(core.Literal("-")),
		core.Choice(base.Integer(noLeadingZeros), base.Float(noLeadingZeros)),
	))
	lx.MustAddRule("WHITESPACE", core.RuneRun(core.RuneSet(" \t\n\r")))

	printAll(bytescan.Skip(lx, "WHITESPACE"))
	// Output:
	// 1:0 BEGIN_OBJECT "{"
	// 1:1 STRING "\"a\""
	// 1:4 NAME_SEPARATOR ":"
	// 1:6 BEGIN_ARRAY "["
	// 1:7 NUMBER "-1.5e3"
	// 1:13 VALUE_SEPARATOR ","
	// 1:15 STRING "\"\\u00e9\""
	// 1:23 END_ARRAY "]"
	// 1:24 VALUE_SEPARATOR ","
	// 1:26 STRING "\"b\""
	// 1:29 NAME_SEPARATOR ":"
	// 1:31 NUMBER "0"
	// 1:32 NUMBER "1"
	// 1:33 END_OBJECT "}"
}

// Example_guideTerminal is the code of docs/guides/terminal-stream.md.
func Example_guideTerminal() {
	// The byte classes of ECMA-48, defined first.
	control := core.RuneRange(0x00, 0x1F).Or(core.RuneSet("\x7F"))
	parameter := core.RuneRange(0x30, 0x3F)
	intermediate := core.RuneRange(0x20, 0x2F)
	final := core.RuneRange(0x40, 0x7E)

	lx := bytescan.New(strings.NewReader("\x1b[1;31mred\x1b[0m \x1bplain\n"))
	lx.MustAddRule("TEXT", core.RuneRun(control.Not()))
	lx.MustAddRule("LF", core.Literal("\n"))
	// A control sequence starts with ESC [. A bare ESC matches no rule, so it
	// is UNKNOWN.
	lx.MustAddRule("CSI", core.Sequence(
		core.Literal("\x1b["),
		core.ZeroOrMore(core.Rune(parameter)),
		core.ZeroOrMore(core.Rune(intermediate)),
		core.Rune(final),
	))

	// The plain text: a filter drops every type that is not text.
	printAll(bytescan.Skip(lx, "CSI", bytescan.TokenTypeUnknown))
	// Output:
	// 1:7 TEXT "red"
	// 1:14 TEXT " "
	// 1:16 TEXT "plain"
	// 1:21 LF "\n"
}

// Example_guideText is the first code block of docs/guides/human-text.md.
func Example_guideText() {
	lx := bytescan.New(strings.NewReader("Read 1,250.5 pages at https://example.org/a 👍🏽!"))

	// URL and Emoji come before Word: a tie of the same length goes to the
	// rule registered first.
	lx.MustAddRule("URL", special.URL())
	lx.MustAddRule("EMOJI", special.Emoji())
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("NUMBER", base.TextNumber(',', '.'))
	lx.MustAddRule("PUNCTUATION", base.Punctuation())
	lx.MustAddRule("SYMBOL", base.Symbol())
	lx.MustAddRule("WHITESPACE", base.Whitespace())
	lx.MustAddRule("NEWLINE", base.Newline())

	printAll(bytescan.Skip(lx, "WHITESPACE"))
	// Output:
	// 1:0 WORD "Read"
	// 1:5 NUMBER "1,250.5"
	// 1:13 WORD "pages"
	// 1:19 WORD "at"
	// 1:22 URL "https://example.org/a"
	// 1:44 EMOJI "👍🏽"
	// 1:46 PUNCTUATION "!"
}

// Example_guideScripts is the second code block of docs/guides/human-text.md.
func Example_guideScripts() {
	lx := bytescan.New(strings.NewReader("私は東京tokyoのコーヒーが好き。"))

	// One script run per script, and no Word: Word would take the whole
	// mixed-script text as one longer match.
	lx.MustAddRule("HAN", base.ScriptRun("Han"))
	lx.MustAddRule("HIRAGANA", base.ScriptRun("Hiragana"))
	lx.MustAddRule("KATAKANA", base.ScriptRun("Katakana", core.RuneSet("ー")))
	lx.MustAddRule("LATIN", base.ScriptRun("Latin"))
	lx.MustAddRule("PUNCTUATION", base.Punctuation())

	printAll(lx)
	// Output:
	// 1:0 HAN "私"
	// 1:1 HIRAGANA "は"
	// 1:2 HAN "東京"
	// 1:4 LATIN "tokyo"
	// 1:9 HIRAGANA "の"
	// 1:10 KATAKANA "コーヒー"
	// 1:14 HIRAGANA "が"
	// 1:15 HAN "好"
	// 1:16 HIRAGANA "き"
	// 1:17 PUNCTUATION "。"
}

// Example_guideLines is the code of docs/guides/line-protocol.md.
func Example_guideLines() {
	in := "event: tick\ndata: 0123456789\n"
	lx := bytescan.NewWithOptions(strings.NewReader(in), bytescan.WithMaxTokenBytes(4096))

	// A name only at the start of a line. At the start of "data", NAME and
	// VALUE match the same text, and NAME wins because it comes first.
	lx.MustAddRule("NAME", core.AtLineStart(core.RuneRunMax(core.ASCIILetter, 64)))
	lx.MustAddRule("COLON", core.Literal(":"))
	lx.MustAddRule("SPACE", core.Literal(" "))
	// Every run is bounded: a value longer than 4 runes comes out in pieces.
	lx.MustAddRule("VALUE", core.RuneRunMax(core.RuneSet("\n: ").Not(), 4))
	lx.MustAddRule("EOL", core.Literal("\n"))

	printAll(lx)
	// Output:
	// 1:0 NAME "event"
	// 1:5 COLON ":"
	// 1:6 SPACE " "
	// 1:7 VALUE "tick"
	// 1:11 EOL "\n"
	// 2:0 NAME "data"
	// 2:4 COLON ":"
	// 2:5 SPACE " "
	// 2:6 VALUE "0123"
	// 2:10 VALUE "4567"
	// 2:14 VALUE "89"
	// 2:16 EOL "\n"
}

// Example_guideSync is the code of docs/guides/sync-marker.md.
func Example_guideSync() {
	const size = 4 // the packet size, sync byte included
	in := []byte{
		0x01, 0x47, 0x02, // noise, with a 0x47 that no packet confirms
		0x47, 0xA0, 0xA1, 0xA2, // a packet
		0x47, // the next packet starts
	}

	// A 0x47 is SYNC only when the byte one packet later is 0x47 too.
	sync := octet.Set(0x47)
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(in)))
	lx.MustAddRule("SYNC", core.FollowedBy(sync, core.Sequence(octet.Fixed(size-1), sync)))

	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println(bytesource.Hex(l))
		if l.Type() == "SYNC" {
			break // a driver now switches to its header scanner
		}
	}
	// Output:
	// 0 UNKNOWN 1 01
	// 1 UNKNOWN 1 47
	// 2 UNKNOWN 1 02
	// 3 SYNC 1 47
}

// pipe is an io.Reader whose Read calls Next on the outer source and returns
// the text of its BODY tokens, unchanged, and a space at each CLOSE, where a
// body ends. A long body comes in several BODY pieces, and the pipe joins
// them. It starts no goroutine: the inner scanner's cursor calls Read from
// inside the inner scanner's Next. The outer tokens of other types are
// dropped.
type pipe struct {
	outer bytescan.Source
	hold  string // text to return before the next outer token
}

func (p *pipe) Read(b []byte) (int, error) {
	for p.hold == "" {
		l, err := p.outer.Next()
		if err != nil {
			return 0, err
		}
		switch l.Type() {
		case "BODY":
			p.hold = l.Text()
		case "CLOSE":
			p.hold = " "
		}
	}
	n := copy(b, p.hold)
	p.hold = p.hold[n:]
	return n, nil
}

// Example_guideNested is the code of docs/guides/nested-formats.md.
func Example_guideNested() {
	// The outer layer: bodies in angle brackets, one per line.
	outer := bytescan.New(strings.NewReader("<12 apples>\n<3 pears>\n"))
	outer.MustAddRule("OPEN", core.Literal("<"))
	outer.MustAddRule("CLOSE", core.Literal(">"))
	// A small bound, so that the body "12 apples" comes in two pieces.
	outer.MustAddRule("BODY", core.RuneRunMax(core.RuneSet("<>\n").Not(), 8))
	outer.MustAddRule("NEWLINE", core.Literal("\n"))

	// The inner layer reads the text of the bodies through the pipe, on a
	// cursor of its own.
	inner := bytescan.New(bufio.NewReader(&pipe{outer: outer}))
	inner.MustAddRule("NUMBER", core.RuneRunMax(core.ASCIIDigit, 20))
	inner.MustAddRule("WORD", core.RuneRunMax(core.ASCIILetter, 64))
	inner.MustAddRule("SPACE", core.Literal(" "))

	// Offsets are local to the inner stream.
	for {
		l, err := inner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		if l.Type() != "SPACE" {
			fmt.Println(l.Offset(), l.Type(), l.Text())
		}
	}
	// Output:
	// 0 NUMBER 12
	// 3 WORD apples
	// 10 NUMBER 3
	// 12 WORD pears
}

// Example_guideGuard is the code of docs/guides/operations.md.
func Example_guideGuard() {
	in := strings.Repeat("a", 25)

	// An unbounded run on a scanner with a guard of 16 bytes.
	lx := bytescan.NewWithOptions(strings.NewReader(in), bytescan.WithMaxTokenBytes(16))
	lx.MustAddRule("RUN", core.RuneRun(core.ASCIILetter))
	_, err := lx.Next()
	fmt.Println(errors.Is(err, bytescan.ErrTokenTooLarge))

	// The same input with a bounded run: pieces of at most 10 runes.
	lx = bytescan.NewWithOptions(strings.NewReader(in), bytescan.WithMaxTokenBytes(16))
	lx.MustAddRule("RUN", core.RuneRunMax(core.ASCIILetter, 10))
	printAll(lx)
	// Output:
	// true
	// 1:0 RUN "aaaaaaaaaa"
	// 1:10 RUN "aaaaaaaaaa"
	// 1:20 RUN "aaaaa"
}

// Example_guideSpans is the code of docs/guides/how-it-works.md.
func Example_guideSpans() {
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

// typed is the input of a REPL in Example_guideMoreInput: the lines typed so
// far. Read never blocks. It returns cursor.ErrMoreInput when no typed input
// is left, and io.EOF after the user ends the session.
type typed struct {
	buf   []byte
	ended bool
}

func (t *typed) Read(p []byte) (int, error) {
	if len(t.buf) == 0 {
		if t.ended {
			return 0, io.EOF
		}
		return 0, cursor.ErrMoreInput
	}
	n := copy(p, t.buf)
	t.buf = t.buf[n:]
	return n, nil
}

// Example_guideMoreInput is the code of the section "Ask for more input" of
// docs/guides/operations.md.
func Example_guideMoreInput() {
	in := &typed{}
	lx := bytescan.NewWithCursor(cursor.NewReader(in))
	lx.MustAddRule("STRING", base.QuotedMultiline('"', '\\'))
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WS", base.Whitespace())
	lx.MustAddRule("NL", base.Newline())

	fed, lexed := 0, 0
	prompt := "> "
	for _, line := range []string{"say \"hello\n", "world\"\n", "bye\n"} {
		fmt.Print(prompt, line) // the user types a line
		in.buf = append(in.buf, line...)
		fed += len(line)

		for {
			tok, err := lx.Next()
			if err == bytescan.ErrMoreInput {
				break // no token can be decided until the next line
			}
			if err != nil {
				fmt.Println("error:", err)
				return
			}
			lexed = tok.Span().End().ByteOffset()
			fmt.Printf("  %s %q\n", tok.Type(), tok.Text())
		}

		// A token is still open when typed bytes are not in a token yet.
		prompt = "> "
		if lexed < fed {
			prompt = "... "
		}
	}

	in.ended = true // the user ends the session
	for {
		tok, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("  %s %q\n", tok.Type(), tok.Text())
	}
	// Output:
	// > say "hello
	//   WORD "say"
	//   WS " "
	// ... world"
	//   STRING "\"hello\nworld\""
	//   NL "\n"
	// > bye
	//   WORD "bye"
	//   NL "\n"
}

// Example_guideEdit is the first code block of
// docs/guides/text-that-changes.md: build the lexer, load a text, make one
// edit and read the change.
func Example_guideEdit() {
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
	fmt.Println("loaded:", ch.Inserted)

	// The user deletes the space: "foo bar" becomes "foobar".
	ch, err = lx.Apply(statelex.Delete(3, 4))
	if err != nil {
		panic(err)
	}
	fmt.Println("removed:", ch.Removed)
	fmt.Println("inserted:", ch.Inserted)
	fmt.Println("shift:", ch.Shift())
	// Output:
	// loaded: [{WORD 0 3} {WS 3 4} {WORD 4 7} {NL 7 8}]
	// removed: [{WORD 0 3} {WS 3 4} {WORD 4 7}]
	// inserted: [{WORD 0 6}]
	// shift: -1
}

// Example_guideHighlighter is the second code block of
// docs/guides/text-that-changes.md: a highlighter that keeps its own list of
// painted tokens and updates it from each change.
func Example_guideHighlighter() {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("OP", core.AnyLiteral("=", "+")).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Rule("NL", core.Literal("\n"))

	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}

	// The highlighter keeps the tokens it painted, in order.
	var painted []statelex.Token

	// update applies one change: drop the painted tokens in [Start, OldEnd),
	// paint the inserted tokens, and move every later token by Shift().
	update := func(ch statelex.Change) {
		var next []statelex.Token
		for _, tok := range painted {
			if tok.End <= ch.Start {
				next = append(next, tok)
			}
		}
		next = append(next, ch.Inserted...)
		for _, tok := range painted {
			if tok.Start >= ch.OldEnd {
				tok.Start += ch.Shift()
				tok.End += ch.Shift()
				next = append(next, tok)
			}
		}
		painted = next
		if len(ch.Removed) > 0 {
			fmt.Printf("  unpaint [%d,%d)\n", ch.Start, ch.OldEnd)
		}
		for _, tok := range ch.Inserted {
			fmt.Printf("  paint %s %q\n", tok.Type, lx.Text(tok.Start, tok.End))
		}
	}

	edits := []statelex.Edit{
		statelex.Insert(0, []byte("total = alpha + beta\n")), // load the text
		statelex.Delete(12, 13),                              // a backspace after "alpha"
		statelex.Delete(6, 15),                               // delete a selection
		{Start: 6, End: 6, Text: []byte("= gamma ")},         // paste
	}
	for _, e := range edits {
		ch, err := lx.Apply(e)
		if err != nil {
			panic(err)
		}
		fmt.Printf("%q\n", lx.Text(0, lx.Len()))
		update(ch)
	}

	// The painted tokens are the tokens of the whole text.
	fmt.Println(fmt.Sprint(painted) == fmt.Sprint(lx.Tokens(0, lx.Len())))
	// Output:
	// "total = alpha + beta\n"
	//   paint WORD "total"
	//   paint WS " "
	//   paint OP "="
	//   paint WS " "
	//   paint WORD "alpha"
	//   paint WS " "
	//   paint OP "+"
	//   paint WS " "
	//   paint WORD "beta"
	//   paint NL "\n"
	// "total = alph + beta\n"
	//   unpaint [8,13)
	//   paint WORD "alph"
	// "total beta\n"
	//   unpaint [6,15)
	// "total = gamma beta\n"
	//   paint OP "="
	//   paint WS " "
	//   paint WORD "gamma"
	//   paint WS " "
	// true
}

// Example_guidePureRules is the third code block of
// docs/guides/text-that-changes.md: a rule that keeps a fact in a captured
// variable gives a wrong change, and a pure rule gives the right one.
func Example_guidePureRules() {
	word := core.RuneRun(core.ASCIILetter)

	// TITLE is the first word of the text. This rule remembers that it saw a
	// word in a captured variable, so it is not pure.
	seen := false
	impure := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if seen {
			return nil, bytescan.StateReject
		}
		seen = true
		return word(s)
	}

	// This rule reads the BOF flag of its first symbol instead: the flag is
	// set only at the start of the text. It is pure.
	pure := func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if !s.IsBOF() {
			return nil, bytescan.StateReject
		}
		return word(s)
	}

	for _, title := range []bytescan.Rule{impure, pure} {
		ms := modes.NewModeSet("text")
		ms.Mode("text").
			Rule("TITLE", title).
			Rule("WORD", word).
			Rule("WS", core.RuneRun(core.RuneSet(" ")))

		lx, err := statelex.New(ms)
		if err != nil {
			panic(err)
		}
		if _, err := lx.Apply(statelex.Insert(0, []byte("hello world"))); err != nil {
			panic(err)
		}
		// The user types a new first word.
		if _, err := lx.Apply(statelex.Insert(0, []byte("big "))); err != nil {
			panic(err)
		}
		fmt.Println(lx.Tokens(0, lx.Len()))
	}
	// Output:
	// [{WORD 0 3} {WS 3 4} {WORD 4 9} {WS 9 10} {WORD 10 15}]
	// [{TITLE 0 3} {WS 3 4} {WORD 4 9} {WS 9 10} {WORD 10 15}]
}

// Example_guideModes is the fourth code block of
// docs/guides/text-that-changes.md: an opening quote changes the mode of every
// later token, so the change runs to the end of the text.
func Example_guideModes() {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Rule("NL", core.Literal("\n")).
		Push("QUOTE", core.Literal(`"`), "str")
	ms.Mode("str").
		Rule("TEXT", core.RuneRun(core.RuneSet(`"`).Not())).
		Pop("QUOTE", core.Literal(`"`))

	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}
	if _, err := lx.Apply(statelex.Insert(0, []byte("say hi now\n"))); err != nil {
		panic(err)
	}

	// The user types an opening quote before "hi".
	ch, err := lx.Apply(statelex.Insert(4, []byte(`"`)))
	if err != nil {
		panic(err)
	}
	fmt.Printf("the change covers [%d,%d) of a text of %d bytes\n", ch.Start, ch.NewEnd, lx.Len())
	fmt.Println("removed:", ch.Removed)
	fmt.Println("inserted:", ch.Inserted)

	// The user types the closing quote after "hi".
	ch, err = lx.Apply(statelex.Insert(7, []byte(`"`)))
	if err != nil {
		panic(err)
	}
	fmt.Println("inserted:", ch.Inserted)
	// Output:
	// the change covers [4,12) of a text of 12 bytes
	// removed: [{WORD 4 6} {WS 6 7} {WORD 7 10} {NL 10 11}]
	// inserted: [{QUOTE 4 5} {TEXT 5 12}]
	// inserted: [{TEXT 5 7} {QUOTE 7 8} {WS 8 9} {WORD 9 12} {NL 12 13}]
}

// Example_guidePrompt is the fifth code block of
// docs/guides/text-that-changes.md: a REPL that holds its input in the lexer
// and asks for another line while a string or a comment is open.
func Example_guidePrompt() {
	ms := modes.NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", core.RuneRun(core.ASCIILetter)).
		Rule("OP", core.AnyLiteral("/", "*")).
		Rule("COMMENT", base.BlockComment("/*", "*/")).
		Rule("WS", core.RuneRun(core.RuneSet(" "))).
		Rule("NL", core.Literal("\n")).
		Push("QUOTE", core.Literal(`"`), "str")
	ms.Mode("str").
		Rule("TEXT", core.RuneRun(core.RuneSet(`"`).Not())).
		Pop("QUOTE", core.Literal(`"`))

	lx, err := statelex.New(ms)
	if err != nil {
		panic(err)
	}

	prompt := "> "
	for _, line := range []string{"say \"hello\n", "world\"\n", "a /* note\n", "end */\n"} {
		fmt.Print(prompt, line) // the user types a line
		if _, err := lx.Apply(statelex.Insert(lx.Len(), []byte(line))); err != nil {
			panic(err)
		}

		// A string is open when the mode state at the end holds more than
		// the start mode. A comment is open when a token read the end of
		// the text.
		_, unfinished := lx.Unfinished()
		if lx.ModeAtEnd().Len() > 1 || unfinished {
			prompt = "... "
			continue
		}

		fmt.Printf("run %q\n", lx.Text(0, lx.Len()))
		if _, err := lx.Apply(statelex.Delete(0, lx.Len())); err != nil {
			panic(err)
		}
		prompt = "> "
	}
	// Output:
	// > say "hello
	// ... world"
	// run "say \"hello\nworld\"\n"
	// > a /* note
	// ... end */
	// run "a /* note\nend */\n"
}
