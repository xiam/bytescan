# Lexing text that changes

This guide is for a program that edits text and needs its tokens after every
edit: a REPL that colors the line as the user types, or an editor that
highlights a document. It shows how to use the lexer with state, the package
[`statelex`](https://pkg.go.dev/github.com/xiam/bytescan/statelex).

## Contents

- [When to use it](#when-to-use-it)
- [The smallest program](#the-smallest-program)
- [What to do with a change](#what-to-do-with-a-change)
- [Pure rules](#pure-rules)
- [Modes, and a change that runs to the end](#modes-and-a-change-that-runs-to-the-end)
- [An open string or comment at the end](#an-open-string-or-comment-at-the-end)
- [What bytescan does not do](#what-bytescan-does-not-do)

## When to use it

A plain scanner (`bytescan.New` and `Next`) reads a stream once, from the
start to the end. Use it when the input arrives and never changes: a file, a
log, a network protocol, the output of a program. The
[technique chooser](README.md) covers those cases.

Use the lexer with state when the text changes after it was lexed. The user
types a letter, deletes a selection, or pastes over everything. You tell the
lexer what changed in the text. The lexer tells you what changed in the
tokens. You do not lex the whole text again.

The lexer keeps its own copy of the text and its tokens. After each edit, its
tokens are the tokens that a plain scanner gives for the whole new text. The
tests `TestGeneratedEdits` and `TestGeneratedEditsModes` in
[`statelex`](../../statelex) check this after many generated edits.

## The smallest program

Build the lexer from a set of modes. A rule set without modes is a set with
one mode. Load the text, make one edit, and read the change.

```go
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
```

It prints:

```text
loaded: [{WORD 0 3} {WS 3 4} {WORD 4 7} {NL 7 8}]
removed: [{WORD 0 3} {WS 3 4} {WORD 4 7}]
inserted: [{WORD 0 6}]
shift: -1
```

The code is `Example_guideEdit` in
[`example_guides_test.go`](../../example_guides_test.go).

- **Loading a text is one edit.** A new lexer holds an empty text. Insert the
  whole text at offset 0. The change then lists every token.
- **An edit is a byte range and new bytes.** `statelex.Edit{Start, End, Text}`
  says that the bytes `[Start, End)` became `Text`. `statelex.Insert` and
  `statelex.Delete` build the two common edits. `Lexer.SetText` takes the
  whole new text, for an editor that reports only that.
- **Offsets are bytes.** An edit must not start or end inside a UTF-8
  character; `Apply` then returns `statelex.ErrSplitRune` and changes
  nothing. `Lexer.Offset` turns a line and a column into a byte offset.
- **A token is a type and a byte range.** A `statelex.Token` has no text. Read
  its bytes with `Lexer.Text(tok.Start, tok.End)`.

## What to do with a change

One edit gives one `statelex.Change`. It says three things:

1. The old tokens in `Removed` are gone. They covered the old bytes
   `[Start, OldEnd)`.
2. The new tokens in `Inserted` take their place. They cover the new bytes
   `[Start, NewEnd)`.
3. Every old token from `OldEnd` on moves by `Shift()` bytes.

A change leaves out the tokens that did not change at its two ends. So a
small edit gives a small change, and an edit that changes no token gives an
empty change.

A highlighter keeps its own list of painted tokens. For each change it
unpaints the removed range, paints the inserted tokens, and moves the rest.
It reads no other token from the lexer.

```go
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
```

It prints:

```text
"total = alpha + beta\n"
  paint WORD "total"
  paint WS " "
  paint OP "="
  paint WS " "
  paint WORD "alpha"
  paint WS " "
  paint OP "+"
  paint WS " "
  paint WORD "beta"
  paint NL "\n"
"total = alph + beta\n"
  unpaint [8,13)
  paint WORD "alph"
"total beta\n"
  unpaint [6,15)
"total = gamma beta\n"
  paint OP "="
  paint WS " "
  paint WORD "gamma"
  paint WS " "
true
```

The code is `Example_guideHighlighter` in
[`example_guides_test.go`](../../example_guides_test.go).

- **The backspace repaints one word.** `alpha` becomes `alph`. The tokens
  after it only move.
- **A deletion can insert nothing.** Deleting `= alph + ` removes six tokens
  and inserts none. The space before the deleted bytes is the same token as
  before, so the change leaves it out.
- **The list stays right.** After every edit, the painted list equals the
  tokens of the whole text (`Lexer.Tokens`).

To paint only the visible part of a large text, ask the lexer for it:
`Lexer.Tokens(start, end)` returns the tokens that overlap a range, and
`Lexer.Text(start, end)` returns its bytes. Then the highlighter needs no copy
of the text.

## Pure rules

The lexer with state works only with pure rules. A pure rule keeps its
progress only in the `Rule` values it returns. It writes no captured
variable, and it changes nothing outside itself. The rules of `rules/core` and
`rules/base` are pure.

The lexer cannot check that a rule is pure. An impure rule gives wrong tokens
without an error. The reason: after an edit, the lexer starts again in the
middle of the text, and a captured variable still holds what it saw before.

The rule `TITLE` below marks the first word of the text. The first version
remembers in a captured variable that it saw a word. The second version reads
the BOF flag of its first symbol (`Symbol.IsBOF`), which is set only at the
start of the text.

```go
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
```

It prints:

```text
[{WORD 0 3} {WS 3 4} {WORD 4 9} {WS 9 10} {WORD 10 15}]
[{TITLE 0 3} {WS 3 4} {WORD 4 9} {WS 9 10} {WORD 10 15}]
```

The code is `Example_guidePureRules` in
[`example_guides_test.go`](../../example_guides_test.go).

The user types `big ` before `hello`. With the impure rule, the new first
word is a `WORD`, because the variable says that a word was seen. A plain
scan of `big hello world` makes it a `TITLE`. With the pure rule, the lexer
gives the same tokens as a plain scan.

Keep a fact that one token passes to the next in the mode state instead (see
the next section). To write a pure rule of your own, see
[practice 4](../rule-authoring.md#practice-4-state-lives-in-returned-rules)
of the rule-authoring guide.

## Modes, and a change that runs to the end

The lexer takes its rules as a `modes.ModeSet`: named modes, and in each mode
rules that stay, push a mode or pop. The lexer keeps the mode state, a stack
of mode names, at the start of every token. After an edit it starts again in
the state it saved there.

An edit can change the mode of every later token. An opening quote turns the
rest of the text into a string. Then the change runs to the end of the text.

```go
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
```

It prints:

```text
the change covers [4,12) of a text of 12 bytes
removed: [{WORD 4 6} {WS 6 7} {WORD 7 10} {NL 10 11}]
inserted: [{QUOTE 4 5} {TEXT 5 12}]
inserted: [{TEXT 5 7} {QUOTE 7 8} {WS 8 9} {WORD 9 12} {NL 12 13}]
```

The code is `Example_guideModes` in
[`example_guides_test.go`](../../example_guides_test.go).

The lexer stops early only where the new scan meets an old token at the same
place of the unchanged text, in the same mode state. After the opening quote,
every later token is in another mode state, so no old token matches. The
closing quote also changes the mode of every later token, so its change also
runs to the end. The exact test is in
[How an edit is re-lexed](../specs/resumable-lexing.md#how-an-edit-is-re-lexed).

## An open string or comment at the end

A REPL must decide what to do when the user presses Enter: run the input, or
print a continuation prompt and wait for the next line. It continues while a
string or a comment is open.

The lexer shows the rules the end of its text as the end of input. So every
byte belongs to a token, and an open string or comment is not an error. Two
queries tell the REPL that the input is not complete:

- `Lexer.ModeAtEnd` returns the mode state after the last token. A string
  that a `Push` opened is still on the stack.
- `Lexer.Unfinished` returns the start of the first token whose rules read
  the end of the text. A block comment with no end reads to the end of the
  text.

```go
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
```

It prints:

```text
> say "hello
... world"
run "say \"hello\nworld\"\n"
> a /* note
... end */
run "a /* note\nend */\n"
```

The code is `Example_guidePrompt` in
[`example_guides_test.go`](../../example_guides_test.go).

- **The string** opens a mode, so `ModeAtEnd().Len()` is 2 until the closing
  quote pops it.
- **The comment** is one rule of one mode, so the mode state does not show it.
  `Unfinished` shows it.
- **After a run**, the REPL deletes its input, and the lexer is empty again.

When the text ends with a whole character, `Unfinished` reports the place
where a plain scanner that reads the same text as a stream would ask for more
input. A REPL that reads a stream and holds no
lexer uses that signal, `bytescan.ErrMoreInput`, directly; see
[Ask for more input](operations.md#ask-for-more-input).

Whether the input is complete as a statement, such as balanced brackets, is a
question for a parser. It belongs to the REPL.

## What bytescan does not do

The lexer with state handles the tokens. The rest of the document belongs to
your program:

- **Lines.** The lexer keeps no line structure. `Lexer.Position` and
  `Lexer.Offset` convert between a byte offset and a line and a column when
  you ask.
- **Undo.** The lexer keeps no history. To undo an edit, apply the edit that
  reverses it.
- **Selection and cursors.** The lexer knows no selection. Several cursors are
  several edits, one after the other.
- **Colors.** The lexer gives token types. Your program picks the colors.
- **Parsing.** The lexer gives tokens, not a syntax tree.

## Where to go next

- [Resumable lexing](../specs/resumable-lexing.md): the exact contract of the
  lexer with state, with a table of edit cases.
- [The `modes` package](https://pkg.go.dev/github.com/xiam/bytescan/modes):
  mode sets and the mode stack.
- [Ask for more input](operations.md#ask-for-more-input): "need more input"
  for a scanner that reads a stream.
