# rules

A rule describes one kind of piece: a word, a number, a URL, a byte value.
bytescan comes with ready-made rules in four packages. Every rule has the same
type, so rules from different packages mix on one scanner, and you can build
your own rule from `core`.

| Package | What it holds |
| --- | --- |
| [`core`](core) | The building blocks: characters, literals, keywords, sequences, choices and repeats. Every other rule is made of them. |
| [`base`](base) | Common tokens of code and of human text: whitespace, identifiers, numbers, strings, comments, words. |
| [`special`](special) | Tokens with their own standard: URLs, email addresses and emoji. |
| [`octet`](octet) | Byte values for binary data: one byte, fixed fields, variable-length integers. Use it with [`bytesource`](../bytesource/README.md). |

## Example: a URL, a number and words

```go
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
```

Output:

```
WORD "read"
URL "https://go.dev"
NUMBER "2"
WORD "times"
```

The longest match wins, so the URL is one token, not the word `https`. The
example runs, with its imports, as a test in
[`example_readme_test.go`](example_readme_test.go).

## Learn more

- [The rule tiers](../docs/rules.md): what each package holds, and how to choose.
- [Write your own rule](../docs/rule-authoring.md): the standard every rule follows.
- [How rules compose](../docs/specs/composition.md): the exact contract.
- The package documentation lists every rule: `go doc github.com/xiam/bytescan/rules/base`,
  and the same for `core`, `special` and `octet`.
