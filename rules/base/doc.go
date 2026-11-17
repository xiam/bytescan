// Package base provides common tokens of programming languages and of human
// text, built only from the rules of package core.
//
// Every constructor in this package returns a bytescan.Rule, so a base rule
// can be registered on a scanner directly and composes with core rules and with
// hand-written rules. A base rule is a composition of core rules: the package
// contains no hand-written state function and follows docs/rule-authoring.md
// through package core, so each rule inherits the leftmost-longest
// composition, the delayed acceptance and the end-of-input
// behaviour that package core documents. Each constructor comment gives the
// regular expression of the rule and its behaviour at end of input. Neither
// the package nor its tests use Go's regexp package; the repository's tests/
// package uses regexp only as a test oracle for each documented expression.
//
// A base rule makes no choice that belongs to one language. Where languages
// differ, the rule takes the most common form and says so, and a scanner for a
// language with a different form composes its own rule from core rules.
//
// # Programming-language tokens
//
// Whitespace and Newline split layout: Whitespace matches a run of horizontal
// whitespace, and Newline matches one line break, "\r\n" included. Identifier
// matches a start rune and continuation runes of two classes;
// ASCIIIdentifier and UnicodeIdentifier are the common presets.
//
// Integer matches decimal digits, and HexInteger, OctalInteger and
// BinaryInteger match digits after the prefixes 0x, 0o and 0b. Decimal
// matches digits with a fraction, and Float matches a number with a fraction
// or an exponent or both. HexFloat matches a hexadecimal mantissa with a
// binary exponent, and Exponent matches an exponent alone, for a scanner that
// composes its own mantissa. No number rule matches a sign: a sign is an
// operator in most languages.
//
// Without options the number rules take the most common form. Options
// compose to give the form of a language: DigitSeparator allows a separator
// between digits, as in 1_000, and SeparatorAfterPrefix one after the prefix,
// as in 0x_FF; OptionalIntegerPart and OptionalFractionPart accept ".5" and
// "1."; NoLeadingZeros rejects "01"; OptionalExponent lets a HexFloat end
// without an exponent; Suffixes ends a number with a suffix, as in 1u or 2j.
// For example, Integer(NoLeadingZeros()) and Float(NoLeadingZeros()) are the
// unsigned numbers of JSON, and Float(DigitSeparator('_'),
// OptionalIntegerPart(), OptionalFractionPart()) is the float of Python.
//
// Quoted matches text between two quote runes with an escape rune, and Raw
// matches text between two delimiters with no escape. QuotedWith matches a
// quoted string whose escape sequences must match a given rule, such as the
// string of JSON. EscapedRun matches the body text of a string that a mode
// driver lexes: it stops before a stop rune and before an opener such as
// "${", and an escape sequence is part of the text. LineComment matches a
// comment from a prefix to the end of the line, and BlockComment matches a
// comment between two delimiters. Quoted and Raw stop at a line break;
// QuotedMultiline, RawMultiline and BlockComment match across line breaks.
// Raw, RawMultiline and BlockComment end at the first close delimiter, so a
// block comment does not nest.
//
// # Human-text tokens
//
// Word, TextNumber, Punctuation, PunctuationRun, Symbol and ScriptRun match
// the tokens of natural-language text. They use Unicode general categories
// and scripts, so they work for any script that separates words with spaces
// or punctuation.
//
// Word approximates the word boundaries of Unicode Standard Annex #29 (UAX
// #29, "Unicode Text Segmentation") and does not implement it. A word is a
// run of letters and combining marks, with inner apostrophes and hyphens. The
// differences from UAX #29 include these:
//
//   - A word holds no digit, so "mp3" is the word "mp" and a number.
//   - A word holds no inner full stop, colon or other joiner that UAX #29
//     allows, so "e.g" is two words.
//   - A word does not stop where the script changes: "東京tokyo" and the
//     Japanese "東京は" are each one word.
//   - No dictionary is used: a run of Han or Thai text with no spaces is one
//     word, and ScriptRun matches the same run as one token of its script.
//
// No rule normalizes its input. A combining mark extends the word before it,
// so the NFC form "é" (U+00E9) and the NFD form "e" + U+0301 are each one
// word, but they are different texts. Sentence splitting and grapheme
// clusters are out of scope.
//
// Where a language needs more, the human-text rules take it as an argument
// rather than a rule per language: ScriptRun takes extra classes that may
// continue a run, such as the prolonged sound mark "ー" of katakana, and
// TextNumber takes its separators and the option TextDigits, for digits such
// as the Arabic-Indic "٠" to "٩".
//
// Several rules can match the same text, and between registered rules the
// scanner picks the longest match and breaks a tie by registration order.
// Register ScriptRun before Word for a script whose runs should get the
// script's token type; a run that letters of another script follow is still
// a longer Word. Register Punctuation or PunctuationRun, not both, for one
// token type of punctuation, as both match a single punctuation rune.
package base
