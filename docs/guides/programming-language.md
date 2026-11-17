# Guide: a programming language

Source code is text, and most of its tokens are decided by their characters:
keywords, identifiers, numbers, strings, operators, comments. Rules lex those.
A few constructs need more than a rule can see, and the language reference
says which: a token that depends on the token before it goes in a filter, and
a body whose rules change goes in a mode driver.

## Rules

- **Keywords:** `core.Keywords(words...)` matches a word only when no ASCII
  word rune follows it, so `returned` is not the keyword `return`. Use
  `core.KeywordsWithGuard(class, words...)` when identifiers continue with
  other runes, and `core.KeywordsIgnoreCaseWithGuard` for a language such as
  SQL.
- **Identifiers:** `base.ASCIIIdentifier()`, `base.UnicodeIdentifier()`, or
  `base.Identifier(start, rest)` with the classes of the reference.
- **Numbers:** `base.Integer`, `base.Float`, `base.HexInteger`,
  `base.HexFloat`, `base.OctalInteger`, `base.BinaryInteger`, with the
  number options of `rules/base` for digit separators, prefixes and suffixes.
- **Strings:** `base.Quoted`, `base.QuotedMultiline`, `base.Raw`, and
  `base.QuotedWith` when the escapes must be checked. `base.EscapedRun` lexes
  the body of a string that a mode driver reads in pieces.
- **Operators:** all of them in one `core.AnyLiteral`. The longest match
  then picks `>=` over `>` and `**=` over `**`.
- **Comments:** `base.LineComment(prefix)` and `base.BlockComment(open,
  close)`.

## Registration order

Register the keyword rule before the identifier rule. A keyword is also an
identifier of the same length, and a tie goes to the rule registered first.
Nowhere else should the order matter: write a comment in your constructor
that names the one tie it breaks.

```go
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
```

The code is `Example_guideCode` in
[`example_guides_test.go`](../../example_guides_test.go). `printAll` is in
[`example_source_test.go`](../../example_source_test.go).

Give brackets their own types (`LBRACE`, `RBRACE`) when a filter or a mode
driver counts them. One `PUNCTUATION` type for every bracket makes the count
read the text of each token.

## Filters: logic on the token stream

A filter is plain Go code that wraps a `bytescan.Source` and is itself a
`Source`. Use one when the reference defines the behaviour on tokens:

- **Semicolon insertion** (Go): after the last token of a line, insert a
  `bytescan.NewEmptyToken` of type `SEMICOLON`. See
  [`go/semicolons.go`](https://github.com/xiam/bytescan-examples/blob/main/go/semicolons.go).
- **Indentation** (Python): keep a stack of indentation levels and emit
  `INDENT` and `DEDENT` as empty tokens. See
  [`python/filter.go`](https://github.com/xiam/bytescan-examples/blob/main/python/filter.go).
- **Dropping layout or comments:** `bytescan.Skip(src, types...)`.
- **Relabeling:** `Token.WithType`, such as a physical line break that ends
  a logical line.

A filter never judges the program. It adds, drops or relabels tokens by the
lexical rules of the reference and never reports that a program is invalid.

## Mode drivers: bodies with other rules

Use a mode driver from [`modes`](../../modes) when the characters after a
token need other rules:

- **Long brackets** (Lua `[==[ … ]==]`): the closer depends on the opener.
  Build one scanner per level with `modes.Cache`. See
  [lua](https://github.com/xiam/bytescan-examples/tree/main/lua).
- **Regular expression or division** (TypeScript): `/` after an operand is a
  division, elsewhere it starts a regular expression. The `SwitchFunc`
  remembers whether the last significant token allows a regular expression
  and picks the code scanner for that case from a `modes.Cache`. A template
  literal with `${ … }` pushes a mode on a stack. See
  [typescript](https://github.com/xiam/bytescan-examples/tree/main/typescript).
- **Interpolation, percent literals and heredocs** (Ruby). See
  [ruby](https://github.com/xiam/bytescan-examples/tree/main/ruby).

`modes.Driver` calls `Release` on the outgoing scanner at every switch and runs
one scanner at a time. Build each scanner once and reuse it. The small example in
[`modes/example_test.go`](../../modes/example_test.go) shows a driver with a
`SwitchFunc` and a `Cache`.

## Traps

- A keyword rule without a guard matches the start of an identifier:
  `core.Literal("if")` matches the `if` of `iffy`, and only a longer
  identifier match hides it. Use `core.Keywords`, which never matches there.
- `core.Keyword` and `core.Keywords` are case-sensitive. The `IgnoreCase`
  forms use simple case folding, so `ß` does not match `ss`.
- A mode driver that builds a new scanner for each token is about 30 times
  slower, and a scanner that is dropped without `Release` pins the input
  behind it.
- Only the driver knows that a construct is unterminated: every scanner returns
  a plain `io.EOF` at the end. Lex the construct to the end of the input and
  stop normally.

## Tests to write

- A golden test: committed inputs and their expected tokens, including one
  case per token form and the near-misses (`breakx`, `.5`, `1.`).
- For a filter, the same golden files without the dropped lines, so the filter
  needs no golden files of its own.
- For a mode driver, a tiling test: the text of the tokens joins to the
  input, on every committed input.
- When the reference has a scanner you can call, compare with it. The
  [go](https://github.com/xiam/bytescan-examples/tree/main/go) example checks its tokens against
  `go/scanner`.

## Examples

[go](https://github.com/xiam/bytescan-examples/tree/main/go), [python](https://github.com/xiam/bytescan-examples/tree/main/python),
[sql](https://github.com/xiam/bytescan-examples/tree/main/sql), [lua](https://github.com/xiam/bytescan-examples/tree/main/lua),
[typescript](https://github.com/xiam/bytescan-examples/tree/main/typescript),
[ruby](https://github.com/xiam/bytescan-examples/tree/main/ruby),
[whitespace](https://github.com/xiam/bytescan-examples/tree/main/whitespace) (the inverted case: layout is
the only token), and [hello](https://github.com/xiam/bytescan-examples/tree/main/hello) (a keyword before
the word rule).
