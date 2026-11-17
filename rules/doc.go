// Package rules is the index of the reusable bytescan rules. It holds no
// code; the rules live in four subpackages, one per tier:
//
//   - core (github.com/xiam/bytescan/rules/core): the building blocks.
//     Character classes, primitive rules (Literal, Keyword, Rune, RuneRun,
//     ...), combinators (Sequence, Choice, Optional, ZeroOrMore, OneOrMore,
//     Repeat, FollowedBy, NotFollowedBy) and the PushBack helper. It may import
//     only bytescan.
//   - base (github.com/xiam/bytescan/rules/base): common tokens of
//     programming languages (Identifier, Integer, Float, Quoted, LineComment,
//     ...) and of human text (Word, TextNumber, Punctuation, ScriptRun, ...),
//     composed only from core rules. It may import bytescan and core.
//   - special (github.com/xiam/bytescan/rules/special): formats with their
//     own external specification: URL, Email and Emoji, composed from core
//     rules. It may import bytescan, core and base.
//   - octet (github.com/xiam/bytescan/rules/octet): byte values of binary
//     formats (Any, Set, Range, Literal, Fixed, Record, Uvarint), composed
//     from core rules for input read through the bytesource adapter. It may
//     import bytescan, core and bytesource.
//
// Rules match the symbols of a stream: the runes of text, or the bytes of
// binary data read through bytesource, one rune per byte. The classes,
// primitives and combinators of core work on both; base and special describe
// tokens of text, and octet describes byte values.
//
// The import direction is core, then base, then special; an import in the
// other direction is a defect. Octet imports core and bytesource, not base
// or special, and no other tier imports it. No package under rules imports
// Go's regexp package; the tests that use regexp as an oracle live in the
// repository's tests package.
//
// Every exported constructor returns a bytescan.Rule and every combinator
// accepts a bytescan.Rule, so rules of every tier and hand-written rules
// compose and register on a scanner the same way. See docs/rules.md for the
// tier model and docs/rule-authoring.md for the standard that every rule
// follows.
package rules
