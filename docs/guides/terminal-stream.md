# Guide: a terminal or ANSI stream

A terminal session is text mixed with control data: escape sequences that set
colours, move the cursor or set the window title (ECMA-48). The scanner keeps
the two apart, and a filter gives you the plain text.

## Rules

- **Define the byte classes of ECMA-48 first**, as `core.Class` values:
  the C0 controls (0x00 to 0x1F) and DEL, the parameter bytes (0x30 to
  0x3F), the intermediate bytes (0x20 to 0x2F) and the final bytes (0x40 to
  0x7E). Every rule then reads as the standard does.
- **Text:** `core.RuneRun(control.Not())`, a run of every rune that is not a
  control. Text above ASCII is text too.
- **Single controls:** one `core.Literal` each for the ones you care about
  (`\n`, `\r`, `\t`, BEL), and one rule for the rest of the C0 set.
- **Escape sequences:** one rule per kind, each a `core.Sequence` that starts
  with ESC: CSI (`ESC [`), OSC (`ESC ]`, ended by BEL or `ESC \`), and the
  other escape sequences.

## Registration order

Make the rules disjoint, so that no order is needed. The escape-sequence rules
all start with ESC; make them differ by their second rune. The rule for other
escape sequences must not take `[` or `]` as its final byte, or a cut-off CSI
would lex as an escape sequence.

```go
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
```

The code is `Example_guideTerminal` in
[`example_guides_test.go`](../../example_guides_test.go). `printAll` is in
[`example_source_test.go`](../../example_source_test.go).

## A filter for the plain text

`bytescan.Skip(src, types...)` drops tokens by type, so the plain text is
the scanner with every control type skipped. List the types to drop, including
`bytescan.TokenTypeUnknown`. The [ansi](https://github.com/xiam/bytescan-examples/tree/main/ansi)
example does this behind its `-strip` flag and checks the filter against its
golden files without the dropped lines.

## Traps

- **Leave a bare ESC as `UNKNOWN`.** An ESC that starts no sequence matches no
  rule. Do not add a rule for ESC alone: it would give a broken sequence a
  type that looks valid, and `UNKNOWN` is how the consumer sees that the data
  is broken.
- **The C0 rule must leave out ESC** and the controls that have their own
  rules, or two rules match the same byte.
- **An OSC can be long.** Its command string is a run of text runes; bound it
  with `core.RuneRunMax` or set `bytescan.WithMaxTokenBytes` when the stream
  is not trusted.
- **Do not decode a sequence.** `ESC[1;31m` is one `CSI` token. What its
  parameters mean is the consumer's job.

## Tests to write

- A golden test with a recorded session, one case per kind of sequence, and
  the broken cases: a bare ESC, a CSI cut by the end of the input, an OSC
  with no terminator.
- A test of the filter: its output equals the golden output without the
  lines of the dropped types.

## Examples

[ansi](https://github.com/xiam/bytescan-examples/tree/main/ansi): text mixed with ECMA-48 control
functions, and a strip filter.
