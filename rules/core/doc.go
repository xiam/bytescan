// Package core provides the building blocks of bytescan rules: character
// classes, primitive rules, combinators and the PushBack helper for
// lookahead.
//
// A core rule matches runes. Over the byte adapter of package bytesource each
// rune is one byte, U+0000 to U+00FF, so the same rules match binary data:
// RuneRange(0x80, 0xFF) is a class of bytes, and Literal matches bytes
// written with bytesource.Text. Package octet builds byte rules from these.
//
// Every constructor in this package returns a bytescan.Rule, so a core rule
// can be registered on a scanner directly and composes with hand-written rules.
// The rules follow docs/rule-authoring.md: each one has named states, each
// scanning state handles the EOF symbol first and ends with an explicit
// reject, every pushback chain comes from PushBack, and a rule keeps its state
// only in the Rule values it returns and in immutable constructor parameters.
// One Rule value can therefore be registered on any number of scanners and is
// reused for every token.
//
// # Naming
//
// The names follow one scheme. They are full words, a name says what it
// matches, each thing is built one way, and repetition takes a rule. A class
// that contains only ASCII runes has the ASCII prefix (ASCIIDigit, ASCIIWord);
// a class built from a Unicode general category has the Unicode prefix
// (UnicodeLetter). AnyRune, which contains every rune, is the one predefined
// class with neither prefix. Exported names say what they build
// (core.Literal, core.RuneRun); the newXxxRule and isXxx names of practices 3
// and 8 of docs/rule-authoring.md apply to hand-written rules.
//
// # Classes
//
// A Class reports whether a rune belongs to a set. Classes compose with Not,
// Or and And, and any func(rune) bool converts to a Class. A class with the
// ASCII prefix is ASCII-only (ASCIIDigit, ASCIIOctalDigit, ASCIIBinaryDigit,
// ASCIILetter, ASCIIIdentifierStart, ASCIIWord, ASCIISpace, ...); a class with
// the Unicode prefix uses a Unicode general category (UnicodeLetter, ...);
// AnyRune contains every rune. RuneSet, RuneRange, UnicodeTables and
// UnicodeScript build further classes. A class is not a rule: Rune, RuneRun
// and RuneRunMax lift a class into a rule, and Repeat takes a rule, so a class
// is repeated as Repeat(Rune(c), min, max). A run of one to n runes of a
// class is RuneRunMax(c, n), which costs the same to build for every n.
//
// # Primitives
//
// Literal, LiteralIgnoreCase, AnyLiteral and AnyLiteralIgnoreCase match fixed
// text. Rune matches one rune of a class, RuneRun matches a run of runes of a
// class, and RuneRunMax matches a run of at most n runes of a class. Keyword
// matches a literal that no rune of a class follows, and the keyword sets
// Keywords, KeywordsIgnoreCase, KeywordsWithGuard and
// KeywordsIgnoreCaseWithGuard match the longest of several words that no rune
// of a guard class follows. Each constructor comment gives
// the regular expression of the rule and its behaviour at end of input.
// Neither the package nor its tests use Go's regexp package; the repository's
// tests/ package uses regexp only as a test oracle for each documented
// expression.
//
// # Combinators
//
// Sequence, Choice, Optional, ZeroOrMore, OneOrMore, Repeat, FollowedBy and
// NotFollowedBy compose rules into one rule. They accept and return
// bytescan.Rule, so a hand-written rule composes with core rules. AtLineStart
// restricts a rule to the beginning of a line.
// Composition is leftmost-longest inside the composed rule, as Go's regexp with
// Longest: the composed rule matches the longest text that any way through its
// parts matches, and an alternation does not depend on the order of its rules.
// Between registered rules the VM still picks the longest match and breaks a
// tie by registration order; a composed rule is one registered rule.
//
// A composed rule reads ahead while a part can still match and settles with
// delayed acceptance: it accepts when everything read so far matches, and
// otherwise ends with one terminal pushback chain to its longest match. It
// never returns an unsupported chain ending ("Rule states and pushback" in
// docs/specs/rule-vm-contract.md). A part that ends in its own pushback chain
// composes too: the next part starts where that chain ends.
//
// The rules of this package carry an internal structure that combinators
// compose, so two runs of a core part in the same state are merged and a
// composition of core rules without lookahead stays linear in the input.
// FollowedBy and NotFollowedBy start one run of their lookahead from each end
// of their rule, and runs of different lookaheads are not merged, so a
// lookahead that overlaps a repetition, as in
// FollowedBy(RuneRun(c), Sequence(RuneRun(c), x)), costs time quadratic in the token
// length. A rule that this
// package did not build composes as an opaque part, run as the VM runs a
// registered rule; so does AtLineStart, which reads the flags of a symbol.
// Only the ends that such a rule reports count: the accepts
// that grow its match. A hand-written rule that accepts "abcdefgh" therefore
// never offers the shorter "abcd" that its own lookahead branch could have
// matched. Overlapping repetition of an opaque part, such as ZeroOrMore of a
// hand-written run, costs time quadratic in the token length. Each
// combinator comment states its measured cost in leaf calls (calls to its
// parts) per input symbol.
//
// # Construction
//
// A constructor panics on an invalid argument, for example an empty literal
// or a nil class. That panic happens when the rule is built, never while a
// scanner runs it. A rule never produces an empty match: a composed rule that
// can match the empty text, such as Optional(r), matches only non-empty text when
// it is registered alone. A combinator calls each rule argument once at
// construction, with a symbol that the VM never produces, to recover the
// structure of a core rule; a rule that follows practice 6 of
// docs/rule-authoring.md ignores it.
//
// # Lookahead
//
// PushBack builds the one pushback chain with which a hand-written rule
// settles on a match shorter than what it has read. See practice 5 of
// docs/rule-authoring.md and "Rule states and pushback" in
// docs/specs/rule-vm-contract.md.
package core
