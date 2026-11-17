// Package special provides rules for the tokens inside text that have their
// own external specification: URL (RFC 3986), Email (the WHATWG HTML "valid
// email address") and Emoji (UTS #51, Unicode Emoji).
//
// Every constructor in this package returns a bytescan.Rule, so a special rule
// can be registered on a scanner directly and composes with core rules, base
// rules and hand-written rules. A special rule is a composition of core rules:
// the package contains no hand-written state function and follows
// docs/rule-authoring.md through package core, so each rule inherits the
// leftmost-longest composition, the delayed acceptance and the end-of-input
// behaviour that package core documents. Neither the package nor its tests use
// Go's regexp package; the repository's tests/ package uses regexp only as a
// test oracle for each documented expression.
//
// Each constructor comment names the specification of its rule, gives the
// grammar or regular expression that the rule implements, and lists the
// deviations from the specification. A rule checks the lexical shape of a
// token only: URL does not resolve a host, Email does not check that a domain
// exists, and Emoji does not check that a platform can display a sequence.
//
// # Emoji data
//
// Emoji uses the Emoji, Emoji_Presentation, Emoji_Modifier and
// Emoji_Modifier_Base properties of the Unicode emoji data of version
// UnicodeEmojiVersion. The tables are generated into emoji_table.go by
// internal/gen from the copy of emoji-data.txt in internal/gen/data, which
// also holds the Unicode license; "go generate ./rules/special/..." reproduces
// the file byte for byte without network access. A character is looked up by
// a binary search over the ranges of a table, so Emoji is linear in the
// length of a sequence.
//
// # Registration
//
// The rules overlap with the rules of package base: the scheme of a URL and the
// local part of an address are words, and many emoji are base.Symbol runes.
// Between registered rules the scanner picks the longest match and breaks a tie
// by registration order, so a scanner that registers URL, Email and Emoji
// together with base rules gets the URL, the address and the whole emoji
// sequence. Register Emoji before base.Symbol so that a single emoji rune
// gets the emoji token type.
package special
