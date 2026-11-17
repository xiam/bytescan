# Composition (`rules/`)

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This document records the capabilities that let a consumer **describe** a scanner
from reusable parts rather than hand-rolling automata, and the composition
contract of the combinators in `rules/core`. The engine keeps the same
execution contract throughout (maximal munch, registration-order ties,
committed spans, progress guarantee; see the
[rule and VM contract](rule-vm-contract.md)). The tier model and the import
rules are in [docs/rules.md](../rules.md). Capabilities that live above
`Next()`, such as lexical modes and post-match actions, are in
[Layers above `Next()`](layers.md).

## Contents

- [Construction capabilities](#construction-capabilities)
- [Composition and ambiguity](#composition-and-ambiguity)

## Construction capabilities

Each capability carries its own status. A **Current contract** capability has
shipped in the `rules/` packages and cites the tests that pin it; the exact API
is the Go documentation of each package. An **Intended capability** records
only the semantics: it approves **no** method signature, **no** data structure
and **no** implementation roadmap, and nothing in it is available API.

- **Reusable literals and character classes. Current contract.** `rules/core`
  matches an exact literal (`Literal`), a literal under Unicode simple case
  folding (`LiteralIgnoreCase`), one of several literals (`AnyLiteral`), a
  literal that no rune of a class follows (`Keyword`), one rune of a class
  (`Rune`) and a run of runes of a class (`RuneRun`). It also matches one of
  several literals under simple case folding (`AnyLiteralIgnoreCase`) and the
  longest of several words, with or without case folding, that no rune of a
  guard class follows (`Keywords`, `KeywordsIgnoreCase` and their `WithGuard`
  forms; evidence: `TestKeywords` and `TestKeywordsMatchKeywordChoice` in
  `rules/core/keywords_test.go`). A class is the type
  `core.Class`, with ASCII classes, a set (`RuneSet`), a range (`RuneRange`)
  and the operations `Not`, `Or` and `And`. Evidence:
  `TestCoreMatchesRegexpOracle` and `TestCoreClassesMatchRegexpOracle`
  (`tests/core_oracle_test.go`, the regexp oracle), `TestKeyword`
  (`rules/core/lookahead_test.go`), and `TestConstructorPanics`
  (`rules/core/construct_test.go`, invalid arguments panic at construction).
- **Unicode categories. Current contract.** The classes `UnicodeLetter`,
  `UnicodeMark`, `UnicodeNumber`, `UnicodePunct`, `UnicodeSymbol` and
  `UnicodeSeparator` are the Unicode general categories, and `UnicodeTables`
  and `UnicodeScript` build a class from Unicode tables and scripts, so a rule
  can say "any letter" without an ASCII-only hand-written test. Evidence:
  `TestCoreClassesMatchRegexpOracle` (`\pL`, `\pM`, `\pN`, `\pP`, `\pS`,
  `\pZ`, `\p{Greek}`, `\p{Han}` and `\p{Lu}` rows).
- **Sequence, alternatives, repetition and optional. Current contract.**
  `Sequence`, `Choice`, `ZeroOrMore`, `OneOrMore`, `Repeat` and `Optional`
  express "A then B", "A or B", "zero or more A", "one or more A", "between
  min and max A" and "optional A", with the semantics under
  [Composition and ambiguity](#composition-and-ambiguity). Evidence:
  `TestCoreCombinatorsMatchRegexpOracle`
  (`tests/core_combinator_oracle_test.go`),
  `TestRepeatOfRuneMatchesClassRepeatOracle` and
  `TestRuneRunMatchesOneOrMoreRune` (`tests/core_oracle_test.go`), and
  `TestRepeatAcceptsBoundaryCounts` (`rules/core/construct_test.go`).
- **Reuse of a rule value. Current contract.** One rule value, built once, can
  be registered on several scanners and composed into several rules, because a
  rule keeps its state in the `Rule` values it returns. Evidence:
  `TestRuleReuse` (`rules/core/construct_test.go`) and `TestCombinatorReuse`
  (`rules/core/combinator_test.go`).
- **A lookahead helper for hand-written rules. Current contract.**
  `core.PushBack(n, state)` builds the one pushback chain with which a
  hand-written rule settles on a match shorter than what it has read, as
  practice 5 of the [rule-authoring standard](../rule-authoring.md) requires.
  Evidence: `TestPushBack` (`rules/core/lookahead_test.go`).
- **Named rules. Intended capability.** A rule that can be referenced by name,
  so that a rule can refer to another rule, or to itself, before that rule is
  built. A Go variable that holds a rule value is reuse, not this capability:
  a combinator takes built rules only.
- **Common tokens and specified formats. Current contract.** `rules/base`
  provides programming-language tokens (whitespace, line breaks, identifiers,
  integers, decimals and floats, quoted and raw strings, line and block
  comments) and human-text tokens (`Word`, `TextNumber`, `Punctuation`,
  `PunctuationRun`, `Symbol`, `ScriptRun`). `rules/special` provides `URL`
  (an RFC 3986 subset), `Email` (the WHATWG HTML valid email address) and
  `Emoji` (the UTS #51 sequence grammar). Each constructor comment documents
  its expression or grammar and its deviations from any specification; `Word`
  approximates UAX #29, and `Emoji` checks the shape of a sequence, not its
  RGI membership. Evidence: `TestBaseCodeRulesMatchRegexpOracle`
  (`tests/base_code_oracle_test.go`), `TestBaseTextMatchesRegexpOracle`
  (`tests/base_text_oracle_test.go`), `TestSpecialMatchesRegexpOracle`
  (`tests/special_oracle_test.go`), `TestCodeScanner`
  (`rules/base/code_test.go`), `TestTextMultilingualStreams`
  (`rules/base/text_test.go`), `TestEmojiRGISequences` and
  `TestSpecialWithBaseRules` (`rules/special/special_test.go`).
- **Byte values. Current contract.** `rules/octet` matches the bytes of an
  input read through `bytesource`: one byte (`Any`, `Set`, `Range`), a byte
  sequence (`Literal`), exactly `n` bytes (`Fixed`), a signature followed by
  exactly `n` bytes (`Record`), and an unsigned LEB128-style integer field
  (`Uvarint`). Every rule is a composition of `rules/core`, so it registers
  and composes like any other rule; `n` is at most `octet.MaxFixed`. A field
  whose length is stored in the data is read by a driver, not by a rule; see
  [Byte reads](layers.md#byte-reads-bytesource). Evidence:
  `TestOctetMatchesRegexpOracle` and `TestOctetClassesMatchRegexpOracle`
  (`tests/octet_oracle_test.go`), `TestRulesCompose`,
  `TestFixedBuildCostIsBounded` and `TestConstructorPanics`
  (`rules/octet/octet_test.go`).

## Composition and ambiguity

**Current contract** for the combinators of `rules/core` (`Sequence`, `Choice`,
`Optional`, `ZeroOrMore`, `OneOrMore`, `Repeat`, `FollowedBy`,
`NotFollowedBy`). This was open design question 8, closed by the owner decision
of 2026-11-13 ([decision log](design-principles.md#owner-decisions)).

- *Inside a composed rule, leftmost-longest.* A composed rule matches the
  longest text that any way through its parts matches, as Go's `regexp`
  with `Longest()`; an alternation does not depend on the order of its
  rules. Evidence: `TestCoreCombinatorsMatchRegexpOracle`
  (`tests/core_combinator_oracle_test.go`, 33 rules, 127,474 inputs, the
  regexp oracle) and `TestEngineMatchesReference`
  (`rules/core/engine_internal_test.go`, an independent evaluator, nested
  and nullable lookahead included).
- *Between registered rules, unchanged.* A composed rule is one registered
  rule: the VM picks the longest match among registered rules and breaks a
  tie by registration order. Evidence: `TestCombinatorTies`
  (`rules/core/combinator_test.go`).
- *Settling.* A composed rule settles with delayed acceptance: it accepts
  when everything read matches, and otherwise ends with one terminal
  pushback chain to its longest match, as in
  [Lookahead combined with a longer match](rule-vm-contract.md#lookahead-combined-with-a-longer-match).
  It never returns an unsupported
  [chain ending](rule-vm-contract.md#chain-endings). Evidence:
  `TestLookaheadDelayedAcceptance` (`rules/core/combinator_test.go`), whose
  composed `abcd(?=ef)|abcdefgh` gives the streams of the hand-written rule.
- *Lookahead.* `FollowedBy(r, ahead)` is `r(?=ahead)` and
  `NotFollowedBy(r, ahead)` is `r(?!ahead)`; the end of input stops every
  pending lookahead without a match. Evidence: `TestFollowedBy`,
  `TestNotFollowedBy`. A negative lookahead decides only after every
  negative lookahead inside it has decided. Evidence: `TestNestedLookahead`.
- *Hand-written parts.* A rule that `rules/core` did not build composes as an
  opaque part: it runs as the VM runs a registered rule, its own pushback
  chains included, and only the ends it reports count — the accepts that
  grow its match. Evidence: `TestSequenceWithPushBackChain`,
  `TestOpaqueRuleIsAtomic`, `TestWrappedCoreRuleIsOpaque`.
- *Empty text.* A part can match the empty text (`Optional`, `ZeroOrMore`,
  `Repeat` with `min = 0`), and a repetition never starts an iteration
  twice at one position, so it ends, also when a lookahead inside the
  repeated part decides late. A composed rule registered alone still
  matches only non-empty text, under
  [Progress and zero-length behavior](rule-vm-contract.md#progress-and-zero-length-behavior).
  Evidence: `TestNullableRuleAlone`, `TestNullableRepetitionWithLookahead`.
- *State.* A composed rule keeps its per-token state in the `Rule` values
  it returns ([principle 1](design-principles.md#principle-1-stateless-stack-model));
  one rule can serve several scanners at once, and a `Rule` value from the
  middle of a token may be called again, also concurrently. The option of a
  VM that holds a composed rule's state is closed (question 9 in the
  [decision log](design-principles.md#question-9)). Evidence:
  `TestCombinatorReuse`.
- *Cost.* Each combinator's doc comment states its leaf calls per input
  symbol, pinned by `TestCombinatorCost`. Runs of core parts in the same
  state are merged; runs of opaque parts are not, and neither are the runs
  of a lookahead from different ends of its rule. So overlapping repetition
  of a hand-written rule, and a lookahead that overlaps a repetition, cost
  time quadratic in the token length. Evidence: `TestOpaqueZeroOrMoreIsNotMerged`,
  `TestLookaheadIsNotMerged`.
