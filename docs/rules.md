# Rule tiers

`bytescan/rules` holds reusable rules in four tiers. Each tier is one Go
package, so the imports of a package show which tiers it depends on. The
package `bytescan/rules` itself holds only the package documentation, the
tier index.

| Package | Tier | Holds | May import |
| --- | --- | --- | --- |
| [`rules/core`](../rules/core) | Core | Character classes, primitive rules, combinators and the `PushBack` helper | `bytescan` |
| [`rules/base`](../rules/base) | Base | Common tokens of programming languages and of human text | `bytescan`, `core` |
| [`rules/special`](../rules/special) | Special | Formats with their own external specification | `bytescan`, `core`, `base` |
| [`rules/octet`](../rules/octet) | Octet | Byte values of binary formats and binary protocols | `bytescan`, `core`, `bytesource` |

Every exported constructor in each tier returns a `bytescan.Rule`, and every
combinator accepts a `bytescan.Rule`. A rule from any tier can be registered on
a scanner directly, and it composes with rules of the other tiers and with
hand-written rules.

Rules match the symbols of a stream, and a stream can be text or binary data.
For text a symbol is a rune. For binary data the byte adapter
[`bytesource`](../bytesource) gives the scanner each byte as one rune, U+0000 to
U+00FF, so the classes, primitives and combinators of core match bytes too.
Base and special describe tokens of text; octet describes byte values.

## What belongs in each tier

### Core

Core holds the building blocks that every other rule is made of:

- **Classes.** The `Class` type (`func(rune) bool`) with `Not`, `Or` and `And`;
  the ASCII classes (`ASCIIDigit`, `ASCIIOctalDigit`, `ASCIIBinaryDigit`,
  `ASCIILetter`, `ASCIIIdentifierStart`, `ASCIIWord`, `ASCIISpace`, ...);
  `AnyRune`, every rune; the Unicode general categories (`UnicodeLetter`,
  `UnicodeMark`, ...); and the class constructors `RuneSet`, `RuneRange`,
  `UnicodeTables` and `UnicodeScript`.
- **Primitives.** `Literal`, `LiteralIgnoreCase`, `AnyLiteral`,
  `AnyLiteralIgnoreCase`, `Keyword`, the keyword sets (`Keywords`,
  `KeywordsIgnoreCase`, `KeywordsWithGuard` and
  `KeywordsIgnoreCaseWithGuard`), `Rune`, `RuneRun` and `RuneRunMax`. A
  keyword set is the one way to declare the reserved words of a language:
  any of its words, matched only when no rune of the guard class follows,
  and the longest such word wins. The guard is `ASCIIWord` unless a
  `WithGuard` form takes another class, such as the identifier parts of the
  language. Register the set before the identifier rule, which matches a
  reserved word with the same length. The `IgnoreCase` forms fold as
  `LiteralIgnoreCase` does, Unicode simple case folding. `RuneRunMax(c, n)`
  is the bounded run `[c]{1,n}`: it counts in one state, so it costs the same
  to build for every `n` and has no limit of 1000, where
  `Repeat(Rune(c), 1, n)` holds one copy of `Rune(c)` per count. Use it to
  bound a run in a stream that may never end it.
- **Combinators.** `Sequence`, `Choice`, `Optional`, `ZeroOrMore`, `OneOrMore`,
  `Repeat`, `FollowedBy` and `NotFollowedBy`, and the line anchor
  `AtLineStart`, which matches a rule only at the beginning of a line (the
  symbol's beginning-of-line flag). Composition is leftmost-longest
  inside the composed rule; see
  [Composition and ambiguity](specs/composition.md#composition-and-ambiguity)
  in the core contract.
- **Lookahead helper.** `PushBack`, the one way a hand-written rule builds a
  pushback chain.

**What a core rule costs.** A rule built only from the primitives of core,
with no lookahead and no opaque part, runs on a table of states. After the
table is built, each step on a rune up to U+00FF is one lookup. Runes above
U+00FF go through a cache of bounded size. A composed rule with a lookahead
(`FollowedBy`, `NotFollowedBy`, or `Keyword` and the keyword sets as a part)
or with an opaque part runs on the engine, which steps the parts of the rule
for each symbol. A rule whose table would be too large also keeps the engine,
with the same tokens. The *Cost* entry of [Composition and
ambiguity](specs/composition.md#composition-and-ambiguity) gives the details
and the tests.

**The first token pays for the tables.** A rule builds its table the first
time it reads a symbol, and the scanner shows the first symbol of a token to
every registered rule. So the first token with new rule values pays for the
tables of all of them that run on a table. Build your rules once and reuse
them for every input: one table serves every scanner that runs the rule. For a
program that lexes only one short input, with rules built for it, the builds
can cost more than the whole input would cost on the engine.

**An opaque rule inside a repetition.** An opaque rule is a rule that
`rules/core` did not build, such as a hand-written rule. A core rule that you
wrap in a function of your own is opaque too (`TestWrappedCoreRuleIsOpaque`).
A composed rule with an opaque part runs on the engine, not on a table
(`TestTableRulesTabled`). An opaque rule inside `ZeroOrMore` or `OneOrMore` can
cost time that grows with the square of the token length. This happens when
the rule can end at many positions, such as a hand-written run of letters. On
the engine, the combinators merge two runs of a core rule that are in the same
state, but they cannot merge the runs of an opaque rule. Build the repeated
part from `rules/core` instead, for example `core.RuneRun(core.ASCIIWord)`.
The *Cost* entry of [Composition and
ambiguity](specs/composition.md#composition-and-ambiguity) states this cost,
and `TestOpaqueZeroOrMoreIsNotMerged` (`rules/core/cost_internal_test.go`)
checks it.

Core contains the only hand-written state functions under `rules/`. A rule
belongs in core when it is a general building block that does not describe a
token of any domain.

### Base

Base holds the common tokens of programming languages (`Whitespace`, `Newline`,
`Identifier`, the integer and float rules, `Quoted`, `Raw`, `LineComment`,
`BlockComment`, ...) and of human text (`Word`, `TextNumber`, `Punctuation`,
`PunctuationRun`, `Symbol`, `ScriptRun`).

A base rule is a composition of core rules and contains no hand-written state
function. A base rule makes no choice that belongs to one language: where
languages differ, the rule takes the most common form and says so in its doc
comment. A scanner for a language with a different form composes its own rule
from core.

The number rules of base show how a base rule serves several languages:
options adapt the most common form to a language without a rule per
language. `Integer`, `HexInteger`, `OctalInteger`, `BinaryInteger`, `Decimal`,
`Float` and `HexFloat` take `NumberOption`s, and `Exponent` is the exponent
alone for a scanner that composes its own mantissa. Without options each rule
matches the form that its doc comment gives. The options compose, and a rule
ignores an option that does not change it:

| Option | Effect | Example |
| --- | --- | --- |
| `DigitSeparator(sep)` | one `sep` between two digits | `1_000` |
| `SeparatorAfterPrefix()` | with a separator, one also after `0x`, `0o` or `0b` | `0x_FF` |
| `OptionalIntegerPart()` | no digits before the point | `.5` |
| `OptionalFractionPart()` | no digits after the point | `1.` |
| `NoLeadingZeros()` | the digits before the point are `0\|[1-9][0-9]*` | `01` is `0` and `1` |
| `OptionalExponent()` | a `HexFloat` with a point needs no exponent | `0x1.8` |
| `Suffixes(s...)` | the number ends with one of `s`; `""` makes the suffix optional | `1u`, `2j` |

For example, the unsigned number of JSON is
`core.Choice(base.Integer(base.NoLeadingZeros()), base.Float(base.NoLeadingZeros()))`,
and the float of Python is
`base.Float(base.DigitSeparator('_'), base.OptionalIntegerPart(), base.OptionalFractionPart())`.
A form that no option gives, such as the octal `017` of Go or the run of
zeros of Python, is composed from core beside the base rules
(`tests/base_number_oracle_test.go` composes the numbers of Go, Python, Lua,
JSON and SQL).

The human-text rules adapt to a language the same way. `TextNumber(sep,
decimal)` takes the separators of the language, and the option
`TextDigits(class)` replaces its ASCII digits, so the Arabic number
`١٬٢٣٤٫٥٠` is
`base.TextNumber('٬', '٫', base.TextDigits(core.RuneRange('٠', '٩')))`.
`ScriptRun(script, extra...)` takes extra classes that may continue a run of
the script but never start it: the prolonged sound mark `ー` is in the Common
script, and `base.ScriptRun("Katakana", core.RuneSet("ー"))` keeps it inside
`コーヒー`. A call without the new arguments matches what it matched before.

The string rules of base take three forms, from the simplest to the most
specific:

| Rule | Use it when | Example |
| --- | --- | --- |
| `Quoted(quote, escape)`, `QuotedMultiline` | an escape rune makes the next rune part of the string, whatever that rune is | `"say \"hi\""`, SQL `'it''s'` |
| `QuotedWith(quote, plain, escape)` | the language defines its escape sequences, and an invalid one must not lex as a string | JSON and Go strings |
| `EscapedRun(stop, escape, openers...)` | a mode driver lexes the body, because the body contains an interpolation or the string ends at a context-dependent delimiter | template literal text before `${`, Ruby string text before `#{` |

`QuotedWith` takes the runes that may appear unescaped as a class and the
escape sequence as a rule, so the JSON string is
`base.QuotedWith('"', plain, escape)` with the escape grammar of RFC 8259
([json](https://github.com/xiam/bytescan-examples/tree/main/json) in bytescan-examples). `EscapedRun` matches only the text of a body: it
stops before a rune of `stop` and before each opener, and an escaped stop rune
or opener is text. The driver registers the opener and the closing delimiter
as their own rules and switches scanners on them
([typescript](https://github.com/xiam/bytescan-examples/tree/main/typescript)).
`Raw` and `RawMultiline` are for text with no escape.

### Special

Special holds formats that have their own external specification: `URL`
(RFC 3986), `Email` (the WHATWG HTML valid email address) and `Emoji` (UTS #51).
Each constructor comment names its specification and lists the deviations from
it. A special rule checks the lexical shape of a token only.

A special rule is a composition of core rules and contains no hand-written
state function. Special may also compose base rules.

### Octet

Octet holds the rules of binary data, for a scanner that reads its input
through the byte adapter `bytesource.NewCursor`. Each rule matches byte
values, and its doc comment gives the expression in the notation of a byte
string, where `[\x80-\xff]` is one byte from 0x80 to 0xFF:

- **One byte.** `Any`, `Set(bytes...)` and `Range(lo, hi)`.
- **Fixed fields.** `Literal(b)` matches the bytes of `b` for every byte
  value, where `core.Literal("\xff")` decodes the Go string as UTF-8 and never
  matches the byte FF. `Fixed(n)` matches exactly `n` bytes, and
  `Record(sig, n)` a signature followed by exactly `n` bytes: the fixed part
  of a header. A field cut by the end of the input does not match.
- **Variable-length integers.** `Uvarint(maxBytes)` matches a LEB128-style
  field of at most `maxBytes` bytes, such as the remaining length of MQTT. It
  does not decode the value.

The package is named `octet` so that it does not shadow the standard `bytes`
and `encoding/binary` packages in the files that import it.

An octet rule is a composition of core rules and contains no hand-written
state function, so it composes with every core combinator. `Fixed` and
`Record` hold one copy of a one-byte rule per counted byte: `n` is at most
`octet.MaxFixed` (1000). A field whose length is stored in the data is not a
rule: a stateless rule cannot carry the length, so a driver reads the field
with `bytesource.Read` or `bytesource.Fields`, which also bound each token.

### Choosing a tier

When a rule could live in two tiers, put it in the lower-commitment tier only
if most scanners of both domains need it. A format with its own external
specification goes in special. A token that is specific to one language goes
in the consumer's own code, composed from core and base.

## Import rules

- `rules/core` may import only `bytescan` and the standard library.
- `rules/base` may import `bytescan`, `rules/core` and the standard library.
- `rules/special` may import `bytescan`, `rules/core`, `rules/base` and the
  standard library.
- `rules/octet` may import `bytescan`, `rules/core`, `bytesource` and the
  standard library. No other tier imports `rules/octet`, and `rules/octet`
  imports no other tier.
- No package under `rules/` imports a module outside this repository.
  Test files may also import the test-assertion module that the
  repository's other tests use, and the shared test helpers in
  `internal/lextest`.
- **No file under `rules/` imports Go's `regexp` package**, test files
  included. Go's `regexp` is a test oracle only.
- **Oracle tests live in `tests/`.** A test that checks a rule against the
  regular expression its doc comment documents lives in the `tests/` package:
  `tests/core_oracle_test.go`, `tests/core_combinator_oracle_test.go`,
  `tests/base_code_oracle_test.go`, `tests/base_number_oracle_test.go`,
  `tests/base_string_oracle_test.go`, `tests/base_text_oracle_test.go`,
  `tests/special_oracle_test.go` and `tests/octet_oracle_test.go`. Tests that
  need no oracle live next to the rules they check.

An import in the other direction, for example `rules/core` importing
`rules/base`, is a defect.

## Long tokens and memory

A scanner holds memory in proportion to its longest open token: the live heap
grows linearly with the bytes of the open token, and the peak is higher while
the stack grows (see the package documentation of `bytescan`).
`bytescan.WithMaxTokenBytes` is the only hard bound on it, and it is fail-stop: a token that would exceed
the bound makes `Next` return an error wrapping
`cursor.ErrRetentionExceeded`. A scanner over a shared cursor takes the bound
through `NewWithCursorOptions`.

The base rules whose read-ahead can be rejected — `Quoted`, `QuotedMultiline`,
`QuotedWith`, `Raw`, `RawMultiline`, `BlockComment`, `Decimal`, `Float`, `HexFloat` and a
number rule with a required suffix — read a long run to its end before they
reject it, for example an unterminated string or a digit run with no `.`. The scanner can then read the same run more than once.
Set `WithMaxTokenBytes` when such input can come from an untrusted source.

## Authoring

Every rule under `rules/` follows the
[rule-authoring standard](rule-authoring.md). Core follows it directly. Base,
special and octet follow it through core: they contain no hand-written state
function, so each of their rules inherits the named states, the end-of-input
behaviour and the single pushback chain of the core rules it is composed from.
Each constructor comment gives the regular expression or grammar of the rule
and its behaviour at end of input.

Programs built only from rules of these packages are in the separate
[bytescan-examples](https://github.com/xiam/bytescan-examples) repository.
