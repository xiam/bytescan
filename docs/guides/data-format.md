# Guide: a data format such as JSON

A data format with a small, fixed token set needs rules only: no filter and no
mode driver. JSON is the model. Each structural character is a token, and
strings, numbers and the literal names are the rest.

## Rules

- **Structural tokens:** one `core.Literal` per token, each with its own type
  (`BEGIN_OBJECT`, `NAME_SEPARATOR`). Do not merge them into one
  `PUNCTUATION` type: the consumer would have to read the text again.
- **Literal names:** one `core.Literal` each (`true`, `false`, `null`).
- **Strings:** `base.QuotedWith(quote, plain, escape)` with the escape grammar
  of the format. `base.Quoted` accepts any escaped rune, which is wrong for a
  strict format.
- **Numbers:** compose the grammar of the format from `base.Integer` and
  `base.Float` with number options, such as `base.NoLeadingZeros()`. No base
  number rule takes a sign; compose it with `core.Optional`.
- **Whitespace:** the set of the format, such as `core.RuneRun(core.RuneSet("
  \t\n\r"))` for JSON. `base.Whitespace` takes `\v` and `\f` too and leaves
  out line breaks.

## Registration order

No order is needed: no two rules match the same text. Say so in a comment in
your constructor, and register the rules in the order of the specification.

```go
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
```

The code is `Example_guideData` in
[`example_guides_test.go`](../../example_guides_test.go).

## Traps

- **A scanner does not validate.** `{"a" 1]` lexes to well-formed tokens. The
  order of the tokens is the parser's job.
- **A strict number splits a near-miss.** `01` lexes as the numbers `0` and
  `1`, and `1.` as the number `1` and an `UNKNOWN` `.`. That is correct for a
  scanner: the consumer sees both pieces.
- **A bad escape ends the string match.** `"\q"` is not a `STRING`; its runes
  lex as other tokens or `UNKNOWN`, and the next `"` can open a string that
  takes the text after it. Write a test case for each kind of bad escape and
  read the result.
- **Strings and numbers can be long.** `QuotedWith`, `Float` and the other
  rules that can reject read a long run to its end before they decide. Set
  `bytescan.WithMaxTokenBytes` when the input can come from an untrusted
  source (see [operations.md](operations.md)).

## Tests to write

- A golden test with one realistic document, one case per token form, and
  the invalid cases: an unterminated string, a bad escape, the near-misses of
  each number form.
- A check of each composed rule against its regular expression on the same
  inputs, when the rule is not trivial. The oracle tests in
  [`tests/`](../../tests) show the method.

## Examples

[json](https://github.com/xiam/bytescan-examples/tree/main/json) is the reference example of this
repository. The JSON layer of
[ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson) is the same token set,
bounded for an endless stream.
