# Guide: text in a human language

Prose needs a small rule set: words, numbers, punctuation, symbols and
layout. The rules of `rules/base` are Unicode-aware, so the same set works
for English, Spanish, Arabic or Korean. Scripts written without spaces, such
as Japanese, need another rule set: one script run per script, and no word
rule.

## Rules

- **Words:** `base.Word()`, a run of letters and combining marks, with
  apostrophes and hyphens inside a word. An Arabic word with its vowel marks
  is one word, and so is a Korean word in either encoding (precomposed or
  conjoining jamo).
- **Numbers:** `base.TextNumber(sep, decimal)` with the separators of the
  language: `TextNumber(',', '.')` for English, `TextNumber('.', ',')` for
  Spanish. `base.TextDigits(class)` sets other digits, such as Arabic-Indic.
- **Punctuation:** `base.Punctuation()`, one mark per token, including the
  marks of other scripts (`،`, `؟`, `。`). `base.PunctuationRun()` takes a
  run.
- **Symbols:** `base.Symbol()`, for `$`, `+`, `©` and the like.
- **Layout:** `base.Whitespace()` and `base.Newline()`.
- **Scripts without spaces:** `base.ScriptRun(script, extra...)`, one rule
  per script, such as `Han`, `Hiragana`, `Katakana` and `Latin` for Japanese,
  in place of `base.Word`. A run of one script is the nearest lexical unit
  without a dictionary (see below).
- **Web text:** `special.URL()`, `special.Email()` and `special.Emoji()`.

## Registration order

Register `special.URL` and `special.Emoji` before `base.Word`. Each of them
can match the same text as a word, and a tie goes to the rule registered
first. The rules of the base set (`Word`, `TextNumber`, `Punctuation`,
`Whitespace`, `Newline`) do not overlap.

Registration order breaks only a tie of equal length: the longest match wins
first. So a rule registered before `Word` wins only where `Word` stops at the
same place.

```go
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
```

The code is `Example_guideText` in
[`example_guides_test.go`](../../example_guides_test.go). `printAll` is in
[`example_source_test.go`](../../example_source_test.go).

## Scripts written without spaces

`base.Word` takes letters of every script, so on `私は東京tokyo` it is one
word, longer than any script run, and it wins whatever the order. A
`ScriptRun` before `Word` cannot cut it. For such text, register one script
run per script and leave `Word` out; the runs do not overlap, so no order is
needed:

```go
lx := bytescan.New(strings.NewReader("私は東京tokyoのコーヒーが好き。"))

// One script run per script, and no Word: Word would take the whole
// mixed-script text as one longer match.
lx.MustAddRule("HAN", base.ScriptRun("Han"))
lx.MustAddRule("HIRAGANA", base.ScriptRun("Hiragana"))
lx.MustAddRule("KATAKANA", base.ScriptRun("Katakana", core.RuneSet("ー")))
lx.MustAddRule("LATIN", base.ScriptRun("Latin"))
lx.MustAddRule("PUNCTUATION", base.Punctuation())

printAll(lx)
```

The code is `Example_guideScripts` in
[`example_guides_test.go`](../../example_guides_test.go). `printAll` is in
[`example_source_test.go`](../../example_source_test.go).

## Traps

- **`base.Word` is not Unicode word segmentation.** It approximates UAX #29:
  a word does not stop where the script changes (`東京tokyo` is one word),
  holds no digit, and uses no dictionary. A run of Han or Thai text with no
  spaces is one word. Use the script-run rule set for such scripts.
- **The prolonged sound mark `ー` is in the Common script.** Pass it as an
  extra class to the katakana run, so that `コーヒー` is one run:
  `base.ScriptRun("Katakana", core.RuneSet("ー"))`.
- **Number separators are per language.** `1.250` is one thousand two
  hundred fifty in Spanish and one and a quarter in English. Pick the
  separators of the text, not of the program.
- **Emoji checks the shape only.** `special.Emoji` matches any well-formed
  UTS #51 sequence, including one outside the recommended set. `URL` and
  `Email` also check the lexical shape only.

## Tests to write

- A golden test with a realistic paragraph, and one case each for
  contractions, hyphens, numbers with separators, punctuation of the script,
  and mixed scripts.
- For a script with two encodings, the same text in each encoding gives the
  same token types.

## Examples

[words](https://github.com/xiam/bytescan-examples/tree/main/words) (registration order between
overlapping rules), [english](https://github.com/xiam/bytescan-examples/tree/main/english),
[spanish](https://github.com/xiam/bytescan-examples/tree/main/spanish),
[arabic](https://github.com/xiam/bytescan-examples/tree/main/arabic),
[japanese](https://github.com/xiam/bytescan-examples/tree/main/japanese) and
[korean](https://github.com/xiam/bytescan-examples/tree/main/korean).
