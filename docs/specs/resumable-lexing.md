# Resumable lexing and the lexer with state

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This document is the contract for lexing text that changes after it was
lexed: a line that a user types in a REPL, or a document open in an editor.
The user presses backspace, deletes a selection in the middle, or replaces
everything with a paste, and the tokens must follow without lexing the whole
text again.

Every piece of this document is available API:
[Need more input](#need-more-input), a **Current contract**;
[The mode state](#the-mode-state), a **Current contract**; the
position and the flags of
[Starting a scan in the middle](#starting-a-scan-in-the-middle), a **Current
contract**;
[The lookahead record](#the-lookahead-record), a **Current contract**; and the
lexer with state, a **Current contract** for a rule set with one mode and for
a set of several modes. Its queries and its lines and columns are a
**Current contract**.
Two points that the owner deferred are **Open design questions**: state that
is not a stack of mode names ([question 22](open-questions.md#question-22))
and a bound on the work of one edit
([question 27](open-questions.md#question-27)). The two example programs, a
REPL and a highlighter, are not part of this module
([What the examples must be able to do](#what-the-examples-must-be-able-to-do)).
A statement about the code at `master` is a **Current contract** with its
evidence. The decisions are in the
[decision log](design-principles.md#owner-decisions). How to use the lexer
with state is in the guide
[Lexing text that changes](../guides/text-that-changes.md). The owner
approved the names and signatures that a statement gives as approved
(questions 16, 18, 19, 20, 21 and 30, and questions 17, 23, 24, 25, 26, 28 and 29, all answered on 2026-11-18, and
`Reader.Start`, approved on 2026-11-18).
Every other name, signature or data structure is not approved; a code sketch
without that mark is an illustration.

## Contents

- [Scope and layers](#scope-and-layers)
- [What the engine does today](#what-the-engine-does-today)
- [The pieces](#the-pieces)
  - [Pure rules](#pure-rules)
  - [The mode state](#the-mode-state)
  - [Starting a scan in the middle](#starting-a-scan-in-the-middle)
  - [Need more input](#need-more-input)
  - [The lookahead record](#the-lookahead-record)
  - [The lexer with state](#the-lexer-with-state)
  - [The constructor](#the-constructor)
  - [The edit request](#the-edit-request)
  - [The end of the held text](#the-end-of-the-held-text)
  - [The change message](#the-change-message)
  - [The queries](#the-queries)
  - [Lines, columns and rune offsets](#lines-columns-and-rune-offsets)
  - [Errors](#errors)
  - [A bound on the work of one edit](#a-bound-on-the-work-of-one-edit)
- [How an edit is re-lexed](#how-an-edit-is-re-lexed)
  - [The result](#the-result)
  - [The four steps](#the-four-steps)
  - [Why the sync test is exact](#why-the-sync-test-is-exact)
  - [What the change contains](#what-the-change-contains)
- [Edit cases](#edit-cases)
  - [Notation](#notation)
  - [Rule set A](#rule-set-a)
  - [Deleting](#deleting)
  - [Inserting](#inserting)
  - [Replacing](#replacing)
  - [Strings and comments](#strings-and-comments)
  - [The end of the text](#the-end-of-the-text)
  - [Ten backspaces](#ten-backspaces)
  - [Rule set B: modes](#rule-set-b-modes)
  - [What the cases show](#what-the-cases-show)
- [What the examples must be able to do](#what-the-examples-must-be-able-to-do)
  - [The REPL](#the-repl)
  - [The highlighter](#the-highlighter)
- [Open design questions](#open-design-questions)

## Scope and layers

**Current contract — three layers** (owner decision, 2026-11-18). The work
is split into three layers:

1. **The engine**: the VM and the scanner loop (`Scanner.Next`). A consumer
   that reads a stream pays nothing for anything in this document.
2. **The rule definitions**: separate from the engine, and pure (see
   [Pure rules](#pure-rules)). A `modes.ModeSet` holds them
   ([The mode state](#the-mode-state)).
3. **The lexer with state**: the optional package `statelex`, above the
   engine. It keeps what was lexed, it accepts requests to change ranges
   that were already lexed, and it answers each request with what changed
   ([The lexer with state](#the-lexer-with-state)).

Between layers 1 and 3 the engine has three abilities: to start a scan at a
given position in a given state
([Starting a scan in the middle](#starting-a-scan-in-the-middle)), to report
"need more input" ([Need more input](#need-more-input)), and to record how
far it looked to decide a token
([The lookahead record](#the-lookahead-record)). Evidence: the evidence of
each of those sections; `TestCoreDoesNotImportStatelex` for the order of the
layers.

**Current contract — whose job it is** (owner decision, 2026-11-18). The
lexer state is bytescan's job. The code that uses bytescan, called the
implementer here, does not handle lexing details. It says "these bytes became
this" (`Lexer.Apply`), and it receives "this changed, update yourself"
(`Change`). Handling the document is not bytescan's job: the undo history,
the line structure, the selection, the display and the colors belong to the
implementer. The API of `statelex` has no name for an undo, a selection, a
display or a color ([The lexer with state](#the-lexer-with-state)). It keeps
no line structure: it computes a line and a column on request
([Lines, columns and rune offsets](#lines-columns-and-rune-offsets)). Evidence: `Example` in
`statelex/example_test.go`; the consumer of `TestGeneratedEdits`, which keeps
only a list of tokens and reads nothing else from the lexer.

**Current contract — a special case** (owner decision, 2026-11-18). Lexing
text that changes is a special case. A consumer that reads a stream and never
goes back does not pay for it: no extra memory for each token, and no slower
`Next()`. Each piece below states how it meets that constraint, under "How it
satisfies the constraint". Evidence: `TestCoreDoesNotImportStatelex`; the
allocation ceilings of `tests/alloc_test.go`, which the pieces did not
change. The two open questions of this document carry the same constraint.

**Current contract — packaging and build order** (owner decision,
2026-11-18, [question 30](design-principles.md#question-30)). The lexer with
state is an optional package of this module,
`github.com/xiam/bytescan/statelex` (the name is an owner decision of
2026-11-18, [question 23](design-principles.md#question-23)). The package
imports the core and `modes`. The core, `cursor`, `modes`, `rules` and
`bytesource` do not import it: `TestCoreDoesNotImportStatelex` runs
`go list -deps` on them. The four engine pieces were built first ("need more
input", the lookahead record, the start in the middle of a text, the mode
stack), and the lexer with state after them. **How it satisfies the constraint:** the core does not
import the optional package, so a stream consumer does not build it or link
it. The structure that holds the tokens, and the cost of a small edit, are
under [The lexer with state](#the-lexer-with-state).

**Reconciled with principle 1.** The VM keeps no state from one token to the
next ([principle 1](design-principles.md#principle-1-stateless-stack-model)).
Everything this document asks to be remembered (the text, the tokens, the
mode state at each token, how far each token looked) lives in the lexer with
state, above `Next()`, like the other [layers above `Next()`](layers.md).

**Reconciled with principle 5.** Lookahead belongs to the rule
([principle 5](design-principles.md#principle-5-lookahead-belongs-to-the-rule)).
The lookahead record only notes how far the scanner read for a token. The VM
gains no lookahead mechanism of its own.

The lexer with state keeps tokens, not a syntax tree. Parsing stays out of
scope ([Purpose and scope](core-contract.md#purpose-and-scope)).

## What the engine does today

These statements describe the code at `master`. They are the base that the
pieces of this document are built on.

**Current contract — a token is decided from its own start.** Before each
token, `rulesProcessor.Reset` (called from `Scanner.getProcessor`) returns
every rule to the `Rule` value it was registered with. A rule sees one
`Symbol` at a time: its rune, its size and four flags (`Symbol.Rune`,
`Symbol.Size`, `Symbol.IsBOF`, `Symbol.IsBOL`, `Symbol.IsEOL`,
`Symbol.IsEOF`). `symbolFlags` derives BOF from the position being the start
of the stream (`Pos.IsStart`) and BOL from rune column zero. On the lean read
path of a `*cursor.Reader`, `Scanner.leanFlags` and `Scanner.readSymbolLean`
derive the same flags: BOF and BOL of the first symbol of a token from the
position at the cursor, and BOL of a later symbol from a newline before it
(`TestLeanReadPathMatchesPublic`, `TestLeanReadPathReferenceCases`). After
`Reader.ResetStart`, the BOF and BOL flags at offset 0 come from the start
instead ([Starting a scan in the middle](#starting-a-scan-in-the-middle)). So, for rules
that keep their progress only in the `Rule` values they return, the token at
a position depends on three things only: the rule set, the symbols from the
token's start up to the last symbol the rules were shown, and the BOF and
BOL flags of those symbols. Nothing else that comes before the token's start
reaches a rule. Evidence: the conformance entries of
[principle 1](design-principles.md#principle-1-stateless-stack-model);
`TestScannerProcessor` "Context-Aware Rules (BOL)" (a rule reads the BOL
flag); `TestTokenDecidedFromItsStart` (the statement as a whole: pairs of
texts with different beginnings and the same ending, lexed with
[rule set A](#rule-set-a) and a rule that reads the BOL flag, give the same
tokens for the ending); `TestTokenDecidedFromItsStartBOLException` (a
different BOL flag at the start of the ending gives different tokens).

**Current contract — a rule that captures variables is not reset.** A rule
that keeps state in variables it captures "is not reset and is not undone
when a token fails" (the documentation of `Rule`, `rule.go`). The engine
does not know whether a rule is pure: a `Rule` is a function value. See
[practice 4](../rule-authoring.md#practice-4-state-lives-in-returned-rules)
of the rule-authoring guide.

**Current contract — a record of how far a token looked, on request.**
`Scanner.nextToken` commits a token so that the cursor sits at the token's
end. Over a `*cursor.Reader` it moves the cursor over the token only, and it
keeps the symbols it read past the token's end for the next token
(`Scanner.keepCarry`). Over another cursor it steps the cursor back over
them. Before the commit, it notes how far it read. `Scanner.NextLookahead` returns that record with the token
([The lookahead record](#the-lookahead-record)). `Scanner.Next` drops the
record. A `Token` reports its type, its text and its span (`Token.Type`,
`Token.Text`, `Token.Span`) and nothing about what was read after its end.
Evidence: `TestNextLookaheadMatchesNext` (both methods return the same tokens
and errors). See [Lexical modes](layers.md#lexical-modes-modes), which relies
on the same commit step.

**Current contract — the end of input is final.** When the source returns
`io.EOF`, the engine shows the rules a synthetic EOF symbol and decides the
token. A rule that still waits for input is retired, not reported: an
unterminated string gives the tokens its shorter matches allow, and then
`Next` returns `io.EOF`. A scanner whose source never returns
`cursor.ErrMoreInput` cannot tell "this token is unfinished" from "the input
ended here": for it, the end of the source is the end of input. Evidence:
[EOF handling](rule-vm-contract.md#eof-handling);
`TestStillActiveAtEOFRetiresRule`, `TestScannerEdgeCasesAtEOF`. A source that
has no input now can return `cursor.ErrMoreInput` instead; the rules then do
not see an end of input ([Need more input](#need-more-input)).

**Current contract — a live source blocks.** Over a source that has delivered
input and then waits inside `Read`, the scanner returns every token that its
rules can decide from the input delivered so far, and then the next call
blocks in the read. Evidence: `TestScannerReturnsTokensFromLiveSource`;
[Rune sources](cursor.md#rune-sources). So for a scanner whose source never
returns `cursor.ErrMoreInput`, "need more input" is either a blocked call or a
false end of input. A source that returns `cursor.ErrMoreInput` instead of
waiting does not block `Next` ([Need more input](#need-more-input)).

**Current contract — a scan starts where its cursor is, and a cursor starts
at zero.** A scanner built with `NewWithCursor` or `NewWithCursorOptions`
reads from the current position of its cursor. A cursor starts at byte
offset 0, line 1, column 0 (`TestCursorPositions`), and no constructor or
option of `bytescan/cursor` sets another start (`cursor.New`,
`cursor.NewReader`, `cursor.NewRuneReader`, `cursor.NewByteReader`,
`cursor.NewWithCapacity`; `cursor.WithCapacity`, `cursor.WithMaxRetained`).
This did not change. What changed: `Reader.ResetStart` resets a cursor onto a
source that begins in the middle of a text, and the scanner then gives the
first symbol the BOF and BOL flags of that start
([Starting a scan in the middle](#starting-a-scan-in-the-middle)).
`Reader.Seek` can move a cursor forward past the bytes read so far. The
bytes in between go through the buffer and are counted into the position
(`TestReader`, subtest `seeker`, "seek to start + 17"). When the buffer does
not hold them yet, `Seek` reads them from the source. The position after the
seek equals the position after reading the same bytes
(`TestSeekForwardReadsFromSource`). The move obeys the limits of the reader
([Limits of the basic reader](cursor.md#limits-of-the-basic-reader)).
`Seek` computes the absolute stream offset of the target for every `whence`,
and the error depends only on that target (`TestSeekWhenceTargets`):

- A target before stream offset 0 fails with the error "cursor: negative
  position", for every `whence` (`TestSeekNegativePositionDoesNotMove`).
- A target at or after offset 0 but before the start of the buffer fails with
  `ErrSeekOutOfBuffer`, for every `whence` (`TestSeekOutOfBufferDoesNotMove`).
- A forward target that the buffer cannot hold fails with
  `ErrSeekOutOfBuffer`, and so does a target after the end of the input
  (`TestSeekOutOfBufferDoesNotMove`; `TestReaderEdgeCases`, subtest
  `EmptyInput`).

A failed `Seek` does not move the cursor, for every `whence` and every error
(`TestSeekOutOfBufferDoesNotMove`, `TestSeekNegativePositionDoesNotMove`).
This also holds after a `Read` that stopped inside a multi-byte rune. The
bytes already read of that rune stay pending, and the next successful
operation counts them as it does without the failed `Seek`
(`TestSeekFailedForwardKeepsSplitRunePending`,
`TestSeekFailedThenNextOperation`).

While a checkpoint holds input, a move past the retention limit fails with
`ErrRetentionExceeded` (`TestByteReaderRetentionCountsFileBytes`). `Seek`
does not seek the underlying `io.Reader` (`Reader.Seek`). So `Seek` reaches a
position only through the text before it, and it gives a source no other
origin. A new cursor over the part of a text that begins in the middle
therefore reports offsets from 0, line 1 and column 0, and presents BOF and
BOL at its first symbol, unless it is reset with `ResetStart`, which gives the
first symbol the flags of the start.

**Current contract — with `modes.Driver`, the state that picks a mode is the
caller's.** A `modes.Driver` holds the active scanner, and a
`modes.SwitchFunc` returns the scanner for the next token. The state that
picks the mode belongs to the code that uses the driver (the package
documentation of `modes`). bytescan cannot see that state, so it cannot save
it or compare it. This did not change. Evidence: `TestDriverSwitch`;
[Lexical modes](layers.md#lexical-modes-modes).

**Current contract — with `modes.StackDriver`, bytescan holds the state.** A
`modes.ModeSet` gives the mode state the shape of a `modes.Stack`, and its
`modes.StackDriver` keeps the stack. The code that uses this driver keeps no
mode state of its own. bytescan can copy the stack and compare it. See
[The mode state](#the-mode-state).

**Current contract — one token can be bounded.** `WithMaxTokenBytes` fails a
token that grows past the bound with `ErrTokenTooLarge`, and the failed call
makes no partial progress. Evidence:
[Speculative lookahead limits](resources-and-streams.md#speculative-lookahead-limits);
`TestScannerMaxTokenBytes`, `TestScannerRetentionFailureConsumesNothing`.

## The pieces

Each piece below comes from the owner decisions of 2026-11-18. Each
statement of a piece carries its own status: a **Current contract** for what
is built, and an **Open design question** for a point that the owner
deferred. The questions are numbered in [open questions](open-questions.md),
and the [list at the end](#open-design-questions) names all of them.

### Pure rules

**Current contract.** The lexer with state supports pure rules only. A
pure rule keeps its progress only in the `Rule` values it returns: it writes
no captured variable and changes nothing outside itself. The package
documentation of `statelex` states this requirement. Everything that
must be remembered from one token to the next lives in the
[mode state](#the-mode-state), as a plain value that can be compared. A
scanner that reads a stream is not affected by this requirement.

The requirement is on the rule set. The engine cannot check it (see
[What the engine does today](#what-the-engine-does-today)), and an impure
rule under the lexer with state can give a wrong change without an error.

### The mode state

**Current contract.** bytescan defines the shape of the mode state: a
**stack of mode names** (`modes.Stack`). The rules of a mode say only "push
this mode", "pop" or "stay", and there is no other switching code. Because the
state is a plain value that bytescan defines, saving it is a copy. Evidence:
`TestRuleSetB`, `TestStackCopyAndCompare`.

- **Rejected:** a state type that the caller defines. bytescan could not
  copy it or compare it without the caller's help.
- **Deferred:** one extra value slot next to the stack, for a value such as
  a heredoc terminator or a raw-string delimiter. It waits for a real
  example that needs it ([question 22](open-questions.md#question-22)).

**Current contract.** The [sync test](#the-four-steps) of the lexer with
state compares two saved mode states with `Stack.Equal` (`Lexer.apply` in
`statelex/relex.go`). Evidence: `TestOneModeStack`, `TestModesNotASyncPoint`,
`TestEditCasesRuleSetB`. In `TestOneModeStack` a set with one mode pushes and
pops that mode, and a token at the same place with a shorter stack is not a
sync point. In `TestModesNotASyncPoint` (case [B1](#rule-set-b-modes)) a
token at the same place with the same type and another mode state is not a
sync point.

**Current contract — actions on the rules** (owner decision, 2026-11-18,
[question 21](design-principles.md#question-21)). The rule definitions say
"push", "pop" and "stay". A `modes.ModeSet` is a set of named modes
(`NewModeSet`, `ModeSet.Mode`). Each mode is a list of rules, and each rule
carries its action: `Mode.Rule` stays, `Mode.Push` pushes a named mode, and
`Mode.Pop` pops. There is no switching code to write. A rule that is in two
modes with two actions is declared in each of them. `modes.Driver`,
`modes.SwitchFunc` and `modes.Cache` did not change, and their tests pass
unchanged (`TestDriverSwitch`, `TestCache`).

The approved names and signatures (package `modes`):

```go
type Stack struct{ /* not visible */ }
func (s Stack) Top() string
func (s Stack) Len() int
func (s Stack) Equal(t Stack) bool

func NewModeSet(start string) *ModeSet
func (ms *ModeSet) Mode(name string) *Mode
func (m *Mode) Rule(typ bytescan.TokenType, r bytescan.Rule) *Mode              // stay
func (m *Mode) Push(typ bytescan.TokenType, r bytescan.Rule, next string) *Mode // push next
func (m *Mode) Pop(typ bytescan.TokenType, r bytescan.Rule) *Mode               // pop
func (ms *ModeSet) Driver(cur bytescan.Cursor, start Stack) (*StackDriver, error)
func (d *StackDriver) Stack() Stack
```

A `StackDriver` is a `bytescan.Source`, so it also has the `Next` method of
that interface.

Rule set B below, in this form (`ruleSetB` in
`modes/stackdriver_internal_test.go`):

```go
ms := modes.NewModeSet("code")
ms.Mode("code").
	Rule("WORD", word).Rule("WS", spaces).Rule("NL", newline).
	Push("QUOTE", quote, "str")
ms.Mode("str").
	Rule("TEXT", notQuote).
	Pop("QUOTE", quote)
```

**Current contract — the driver.** `ModeSet.Driver` builds one scanner per
mode over the cursor, and returns a `modes.StackDriver`. The behavior:

- `StackDriver.Next` reads the next token with the scanner of the mode at
  the top of the stack. Then it applies the action of the rule that made the
  token. A token that no rule of the mode made, such as an `UNKNOWN` token,
  leaves the stack unchanged. Evidence: `TestRuleSetB`,
  `TestStackDriverNesting`, `TestStackDriverEOF`.
- `StackDriver.Stack` returns the mode state at the start of the next token.
  For each "Before" text of [rule set B](#rule-set-b-modes), the state at the
  start of each token is the state that the table gives. Evidence:
  `TestRuleSetB`.
- A pop while only the bottom mode is on the stack does nothing: the stack
  stays the same, and `Next` returns no error. Evidence:
  `TestStackDriverPopLastMode`.
- An error from the scanner, including `io.EOF` itself, is returned
  unchanged, and the stack does not change. Evidence: `TestStackDriverEOF`.
- When the top mode changes, the driver calls `Release` on the scanner of
  the outgoing mode. Evidence: `TestStackDriverReleases`.
- `Next` and `Stack` hold one mutex. Evidence:
  `TestStackDriverConcurrentNext`.
- A change to the set after `Driver` does not change the driver. Evidence:
  `TestModeSetMode`.
- `Driver` returns an error when the start mode, a mode on the start stack or
  a mode that a `Push` names is not in the set. It returns an error for a
  `Push` or a `Pop` with the type `UNKNOWN`
  (`bytescan.TokenTypeUnknown`): a scanner gives that type to input that no
  rule matches, so the driver could not tell such a token from a match of the
  rule. A `Rule` with the type `UNKNOWN` stays, so it is accepted. It also
  returns the error of
  `bytescan.Scanner.AddRule` for a rule that `AddRule` rejects, such as two
  rules with one type in one mode. Evidence: `TestModeSetDriverErrors`.

**Current contract — the stack.** A `modes.Stack` is a value that never
changes. Its representation is not visible in the API. The behavior:

- A copy is an assignment. A copy does not change when the scan goes on.
  Evidence: `TestStackCopyAndCompare`.
- `Equal` reports whether two stacks hold the same names in the same order.
  Two stacks built apart with the same names are equal. Evidence:
  `TestStackEqual`, `TestStackCopyAndCompare`.
- `Top` returns the name at the top, and `Len` the number of names. The zero
  `Stack` holds no mode: `Top` returns `""` and `Len` returns 0. Evidence:
  `TestStackTopLen`.
- A driver starts in the state that it is given. The zero `Stack` starts in
  the start mode of the set. A scan that starts at a token boundary, in the
  state that a scan from the start of the text had there, gives the tokens
  and the states of that scan from the boundary on. Evidence:
  `TestStackDriverGivenStart`, `ExampleModeSet`.

**How it satisfies the constraint.** The switching code keeps no memory of its
own, because there is no switching code: the action is data on the rule. The
decision depends only on the mode at the top of the stack and the type of the
token. The rules of the tests are rules of `rules/core`, and none of them
keeps a variable from one token to the next. A scanner that uses no mode does
not use the new types, and the ceilings of `tests/alloc_test.go` did not
change. The lexer with state reads the mode definitions and drives the
scanners of its modes itself (see [The constructor](#the-constructor)).

### Starting a scan in the middle

**Current contract.** The engine can start a scan at a given position of
a text, in a given mode state. The tokens of that scan are the tokens that a
scan from the start of the text produces from that position on. The position
part is below (`TestStartAtEveryBoundary`). The mode state part is
[The mode state](#the-mode-state); the two parts together are at the end of
this section (`TestStartInsidePushedMode`).

**Current contract — positions from the restart point, and two start flags**
(owner decision, 2026-11-18,
[question 19](design-principles.md#question-19)). `Reader.ResetStart` resets
a cursor onto a new source, the text from offset `p`. The cursor reports
positions counted from the restart point: byte offset 0, rune offset 0, line 1
and column 0 at `p` (`TestResetStartReportsPositionsFromTheRestart`). The
`Start` value carries the two facts that a rule can see at the start:

- `InText`: the start is not offset 0 of the text, so the first symbol has no
  BOF flag;
- `LineStart`: the byte before the start is `\n` (or the start is offset 0),
  so the first symbol has the BOL flag.

The scanner reads the stored `Start` through `Reader.Start` and uses the two
flags for the symbol at offset 0 only: BOF is `!InText`, and BOL is
`LineStart` (`Scanner.readSymbol`, `startFlags` and `startCursor` in
`bytescan.go`; `TestStartFlags`). Every later symbol gets its flags from its position, as
before (`TestStartFlagsAreUsedForTheFirstSymbolOnly`). The lexer with state
adds `p` to the byte offsets of the tokens. Lines, columns and rune offsets in
the whole text are not part of the restart; the lexer with state computes them
on request ([Lines, columns and rune offsets](#lines-columns-and-rune-offsets)).

The names and signatures (package `cursor`). The owner approved `Start` and
`ResetStart` with the answer to
[question 19](design-principles.md#question-19) on 2026-11-18, and
`Reader.Start` in a separate owner decision on 2026-11-18:

```go
type Start struct {
	InText    bool
	LineStart bool
}
func (r *Reader) ResetStart(src io.Reader, s Start) error
func (r *Reader) Start() Start
```

The lexer with state, for each re-lex (an illustration):

```go
cur.ResetStart(textFrom(p), cursor.Start{InText: p > 0, LineStart: p == 0 || text[p-1] == '\n'})
```

**Current contract — a scan from every token boundary.** For every token
boundary `p` of a text, and for the end of the text, a scan started at `p`
with `ResetStart` gives the tokens that a scan of the whole text gives from
`p` on: the same types and texts, with offsets, lines and columns counted
from `p`. The first symbol of every token, and the EOF symbol, have the same
BOF and BOL flags in both scans. The texts have several lines, multi-byte
runes, every rule of [rule set A](#rule-set-a), a rule that reads the BOL
flag and a rule that reads the BOF flag. One cursor and one scanner serve
every restart of a text (`TestStartAtEveryBoundary`).

**Current contract — what a reset keeps and drops.** `ResetStart` drops the
buffered input, the sticky read error and any partial UTF-8 sequence of the
old source (`TestResetStartDropsTheOldSource`). It also drops a multi-byte
character that the old source cut with
[`ErrMoreInput`](#need-more-input), whether the buffer holds it or the
partial UTF-8 sequence does.
`ErrMoreInput` is not sticky, so no "more input" condition survives the reset.
A scanner that returned `ErrMoreInput` in the middle of a token drops that
token with the old source, and its next token starts at offset 0 of the new
source (`TestResetStartDropsAHeldCutRune`, `TestStartAfterMoreInput`). It
keeps the options of the reader (`WithCapacity`, `WithMaxRetained`) and byte
mode (`TestResetStartLeavesNoRetentionLimit`,
`TestResetStartReportsPositionsFromTheRestart`). A reader built with
`NewRuneReader` reads the new source as UTF-8 bytes (subtest `rune source`).
The reset releases every active checkpoint, because the input that it would
replay is gone; a released checkpoint can be re-armed with `Remark`, and a
scanner on the cursor keeps working (`TestResetStartReleasesCheckpoints`,
`TestResetStartLeavesNoRetentionLimit`, `TestStartAtEveryBoundary`).
`ResetStart` fails only on a nil source, and then it changes nothing
(`TestResetStartRefusesANilSource`, `TestResetStartStoresTheStart`).

**Current contract — a start that is not a token boundary, and a start at the
end.** `ResetStart` does not check that `p` is a token boundary; it cannot
know the boundaries. A start that is not a token boundary gives the tokens of
a scan of the text from `p` with the given flags (`TestStartNotAtABoundary`).
A start at or after the end of the text gives an empty source: `Next` returns
`io.EOF` at once, and the EOF symbol has the flags of the start
(`TestStartAtOrAfterTheEnd`, `TestResetStartEmptySource`). The lexer with
state starts only at token boundaries.

**The mode state of the start.** The mode state of the start is a
`modes.Stack`, given to `ModeSet.Driver`. That piece is a Current contract (see
[The mode state](#the-mode-state)). **Current contract** — the two parts
together. A re-lex of the lexer with state that
restarts at a token boundary inside pushed modes, in the mode state saved
there, gives the tokens and the mode states of a scan of the whole text from
its start. Evidence: `TestStartInsidePushedMode`, which restarts at three
boundaries in the state `[code paren str]`.

**How it satisfies the constraint.** At a token boundary `p`, the scan from
`p` sees the same bytes as a scan from the start of the text, and its first
symbol has the same flags: BOF only at offset 0, BOL exactly when the byte
before is `\n`. Every later symbol has the same column-zero test as in the
full scan, because the bytes after `p` are the same. So the tokens are the
same, with byte offsets moved by `p`. The cursor contract does not change for
other users: `ResetStart` and `Start` are new methods, a cursor that is never
reset starts at offset 0, line 1, column 0 as before, and its `Start` is
`Start{LineStart: true}`, which gives the symbol at offset 0 both flags as
before (`TestResetStartStoresTheStart`; the existing tests of `cursor/` and
`tests/`). A cursor without a `Start` method gives the symbol at offset 0 both
flags, as before (`startCursor`). A stream consumer reads the start flags only
for a symbol at offset 0. The restart costs the same at every `p`: the lexer
with state reads one byte to compute `LineStart`, and `ResetStart` reads
nothing.

**Current contract — the flags at a sync point** (owner decision,
2026-11-18, [question 18](design-principles.md#question-18)). The flags of
the restart point come from `cursor.Start` above (`Lexer.apply`). At a sync point, the flags
are part of the sync test: see [The four steps](#the-four-steps), step 3.

### Need more input

**Current contract**. A scanner
can report "need more input". It returns the complete tokens, and then a
distinct signal that the last token is unfinished. It does not block, and it
does not show "end of input" to the rules. A REPL uses the signal to print a
continuation prompt. Evidence: `Example_guideMoreInput` (a prompt loop, the
code of [Ask for more input](../guides/operations.md#ask-for-more-input)).

This piece is useful without any editing. It differs from both behaviors of
a scanner whose source never returns the signal: the false end of input (the
rules see EOF and settle for less) and the blocked call.

**Current contract — a sentinel from the source** (owner decision,
2026-11-18, [question 16](design-principles.md#question-16)):

- **The signal.** A source that has no input now returns the sentinel
  `cursor.ErrMoreInput` from `Read` (or `ReadRune`), instead of blocking. The
  cursor passes it on and does not keep it: unlike another error of the
  source, it is not returned again on the next read, and the next read asks
  the source again (`TestMoreInputNotSticky`; `TestMoreInputDistinctSignal`,
  subtest "not sticky", in `tests/`). `Next` returns the same value, not
  wrapped, in its error slot, as it returns `io.EOF`, also when the source
  wraps it (`Scanner.nextToken`; `TestMoreInputDistinctSignal`). Package
  `bytescan` gives it a second name, `bytescan.ErrMoreInput`. The signal is
  not `io.EOF`, and `errors.Is` matches it against no other exported error of
  the module (`TestMoreInputDistinctSignal`).
- **The true end.** The source declares the true end by returning `io.EOF`,
  as for any other source. Only then are the rules shown the end of input
  (`TestMoreInputNoFalseEnd`). The scanner then decides the last token as
  [EOF handling](rule-vm-contract.md#eof-handling) says, and `Next` returns
  `io.EOF` on that call and on every later call (`TestMoreInputTrueEnd`).
- **Complete tokens.** A token is complete when the rules decided it before
  the available input ran out: every rule was retired before the scanner
  needed one more symbol. Under this rule the text `foo` gives no token and
  the signal, because the word rule still waits; the text `foo⏎` gives
  `WORD foo` and `NL ⏎`, and then the signal (`TestMoreInputCompleteTokens`).
- **The unfinished token.** It is not returned. The scanner steps back to its
  start, as a failed `Next` does, and the next call lexes it again from its
  start with the new input. Symbols that the scanner kept from the token
  before are read again from the cursor
  (`TestCarryMoreInputAtTokenBoundary`). For every split point of a set of inputs, the
  tokens before the signal and the tokens after the rest of the input and the
  true end equal the tokens of one scan of the whole input
  (`TestMoreInputSplit` over an `io.Reader`, `TestMoreInputSplitRuneSource`
  over an `io.RuneReader`). The set holds an open string, an open comment, a
  token that needs one more symbol to end, multi-byte characters and the
  empty input. A multi-byte character cut at the end of the available input
  stays held in the cursor; it is not read as U+FFFD
  (`TestMoreInputCutRuneStaysHeld`, `TestMoreInputCheckpointReplay`). A bulk
  `Read` returns the bytes that arrived first and the signal when none is
  left, and it does not settle the carried bytes of a cut character on the
  signal (`TestMoreInputBulkRead`). `WithMaxTokenBytes` still fails an
  unfinished token that grows past the bound (`TestMoreInputMaxTokenBytes`).

The approved names (packages `cursor` and `bytescan`):

```go
// package cursor
var ErrMoreInput error

// package bytescan
var ErrMoreInput = cursor.ErrMoreInput
```

A prompt loop (an illustration; `Example_guideMoreInput` is a complete one):

```go
tok, err := sc.Next()
switch {
case err == bytescan.ErrMoreInput: // print a continuation prompt
case err == io.EOF: // the true end
}
```

A ready-made source that the caller writes into is not part of this answer.
It can be added later if the REPL example shows the need.

**How it satisfies the constraint.** The signal is a value of its own: it is
not `io.EOF`, and it does not match any other error of the packages. `Next`
does not block, as long as the source does not block; a source that blocks
still blocks, which is its own choice. The rules are shown the end of input
only when the source returns `io.EOF`, the true end. A scanner over a source
that never returns the sentinel behaves as before: the checks for the signal
run only on the path of an error from the source, every test that predates
the signal passes with no change to its assertions, and the allocation
ceilings of `tests/alloc_test.go` do not change. The signal passes through
`modes.Driver` and `Skip` unchanged, because both return an error of the
source unchanged (`TestMoreInputThroughSkipAndDriver`).

### The lookahead record

**Current contract**. For each
token, the engine can record how far the scanner looked when it decided the
token. This document calls that number the **reach** of the token:

- the reach is the end offset of the furthest byte that the rules were shown
  while the token was decided. It counts the symbols shown to the rules, not
  the bytes that the cursor buffered. The reach of every token is the same
  for every buffer capacity of the cursor (`cursor.WithCapacity`), over a
  reader that returns one byte for each call and over a rune source
  (`TestLookaheadSymbolsNotBufferedBytes`). A byte that starts a character
  which is not complete is one U+FFFD symbol of one byte, so the reach does
  not count the bytes after it that the decoder examined
  (`TestLookaheadKnownReach`, case "broken bytes");
- a token that ends at offset `e` has a reach of at least `e`
  (`TestLookaheadReachLowerBound`). A rule that must see the next symbol to
  know that the token ended gives a reach of `e + 1` or more: in `12.x⏎`
  ([case A12](#replacing)) the reach of `NUMBER 12` is 4
  (`TestLookaheadKnownReach`);
- when the rules were shown the end of the text, the token is recorded as
  having read the end of the text. An edit at the end of the text affects
  such a token. The mark is set exactly when a rule was shown the EOF symbol
  while the token was decided, and a token with the mark reaches the end of
  the text (`TestLookaheadReadEnd`, subtest "probe"). In `foo`
  ([case A23](#the-end-of-the-text)) `WORD foo` carries the mark, and the
  text `food` gives `WORD food` (`TestLookaheadReadEnd`, subtest "A23").

The known reaches of the tokens of the edit cases A2, A3, A12, A15, A19,
A23, A30, A31, A33 and A35 under [rule set A](#rule-set-a) are in
`TestLookaheadKnownReach`. In `a␣/*␣x⏎b⏎` ([case A31](#strings-and-comments))
the reach of `OP /` at offset 2 is the end of the text, and the token carries
the mark.

The reach is enough to decide the tokens, with one condition on broken
bytes. Take the first tokens of a text, up to a token `t`, and the largest
reach `R` among them. Two conditions hold:

- none of these tokens carries the mark;
- the three bytes before `R` hold no first byte of a UTF-8 character that is
  not complete before `R`. This is the condition of the clause for broken
  bytes in step 1 of [The four steps](#the-four-steps).

Then a second text that is equal up to offset `R` and different after it
gives the same tokens up to `t`, with the same records. Appended bytes do
not change these tokens either. Evidence: `TestLookaheadReachIsEnough`,
subtest "generated", and `TestLookaheadReadEnd`, subtest "append", over
generated texts from a fixed seed that hold broken UTF-8 too.

The tokens before `t` count, because a token can have a reach larger than
the tokens after it: in case A31 the reach of `OP /` is 9, and the reach of
`OP *` after it is 4.

The condition on broken bytes is needed because the reach counts symbols,
not the bytes that the decoder examined. In `\xc3o` the first token is
`UNKNOWN [0,1)`, one U+FFFD symbol, with `After` 0 and no mark, so `R` is 1.
The text `\xc3\xa9` is equal up to offset 1, but its first token is
`UNKNOWN é [0,2)` (`TestLookaheadReachIsEnough`, subtest "broken bytes").
In the same way, `\xa9` appended to `\xc3` completes `é` and changes the
token, although the token did not read the end (`TestLookaheadReadEnd`,
subtest "append completes a character").

The reach is what tells the lexer with state which tokens an edit can
change; see [The four steps](#the-four-steps).

**Current contract — a second method** (owner decision, 2026-11-18,
[question 20](design-principles.md#question-20)). A second scanner method
returns the token and its record together. The record holds one number, the
bytes that the rules were shown after the end of the token, and one mark,
"the rules were shown the end of input". The reach is the token's end plus
that number. `Next` does not change, and neither do `Token` nor `Source`.

The approved names and signatures (package `bytescan`):

```go
type Lookahead struct {
	After   int  // bytes shown to the rules after the end of the token
	ReadEnd bool // the rules were shown the end of input
}
func (sc *Scanner) NextLookahead() (*Token, Lookahead, error)
// Next is unchanged: func (sc *Scanner) Next() (*Token, error)
```

`NextLookahead` returns the tokens and the errors that `Next` returns, and it
consumes the same input (`TestNextLookaheadMatchesNext`). A call that returns
an error returns the zero `Lookahead`: `io.EOF`, `ErrNoRules`
(`TestNextLookaheadMatchesNext`) and `ErrMoreInput`
(`TestLookaheadReadEnd`, subtest "more input").

**Current contract — need more input.** A source that returns
`cursor.ErrMoreInput` does not show the rules the end of input
([Need more input](#need-more-input)), and a token that the rules had not
decided then is not returned. So a token that the rules decided from the
available input does not carry the mark, and its `After` counts only the
bytes that the source delivered. The mark appears only after the source
returns `io.EOF`. In `foo⏎ba` with no more input now, `WORD foo` has
`After` 1 and no mark, `NL ⏎` has `After` 0 and no mark, and then the call
returns `ErrMoreInput`. After `r` and the true end, `WORD bar` carries the
mark (`TestLookaheadReadEnd`, subtest "more input").

The number counts from the token's end, so it does not depend on the
coordinates of the scan (see [Starting a scan in the middle](#starting-a-scan-in-the-middle)),
and it stays the same when a token moves by `d` bytes. A scan started at a
token boundary with `cursor.Reader.ResetStart` returns the tokens of the
whole scan from there, moved, with the same records
(`TestLookaheadAfterRestart`). A mode driver
switches the scanner after a token. **Current contract:** the lexer with
state drives the scanners of its modes itself, from the mode definitions, and
calls `NextLookahead` on the scanner of the mode at the top of the mode state
(owner decision, 2026-11-18,
[question 23](design-principles.md#question-23); `Lexer.apply`; see
[The constructor](#the-constructor)). Evidence: `TestGeneratedEdits` for one
mode, `TestGeneratedEditsModes` for several modes. This document gives no
lookahead method to `modes.Driver` or to `modes.StackDriver`.

**How it satisfies the constraint.** The record is one number and one mark
for each token. The number is taken from the cursor position after the last
symbol that the rules were shown (`Scanner.nextToken` takes it from the
symbols read, before it commits the token), so it counts the symbols shown to the rules, not the bytes the
cursor buffered. The VM gains no lookahead mechanism: the scanner only notes
how far it read ([principle 5](design-principles.md#principle-5-lookahead-belongs-to-the-rule)).
`Next` and `NextLookahead` share `Scanner.nextToken`, and `Next` drops the
record. A `Token` does not grow, and `Next` keeps its signature, so a
consumer that does not ask pays nothing for each token: every test that
predates the record passes with no change to its assertions, and the
allocation ceilings of `tests/alloc_test.go` do not change. The record is
returned with its token by the same locked call (`Scanner.NextLookahead`), so
it stays correct when several goroutines call the scanner.

### The lexer with state

**Current contract.** The lexer with state (`statelex.Lexer`) holds:

- **its own copy of the text.** The implementer does not give bytescan a way
  to read the document. The implementer says "bytes A to B became this", and
  the lexer with state applies the edit to its copy. A way to read the text
  from the caller, for very large documents, may come later; it is not part
  of this contract;
- **the tokens** of that text;
- for each token, **the mode state at the token's start** and **the token's
  reach**.

Evidence: `TestGeneratedEdits`. After each edit, the held text is the edited
text, and the held tokens are the tokens of a scan of that text. Evidence for
the mode state: `TestGeneratedEditsModes`. After each edit under
[rule set B](#rule-set-b-modes) and under a set that pushes two levels, the
mode state saved at the start of each held token is the state that a new
`modes.StackDriver` has there in a scan of the whole text.

**Current contract — the restart unit is the token.** A re-lex starts at
a token boundary (step 1 of [The four steps](#the-four-steps)). A line end
is not a restart point. Evidence: the restart column of
`TestEditCasesRuleSetA`. A cache with one entry
for each line can be an example built on top; it is not part of bytescan.

**The API of the lexer with state** (owner decisions,
2026-11-18, questions [17](design-principles.md#question-17),
[23](design-principles.md#question-23),
[24](design-principles.md#question-24),
[25](design-principles.md#question-25),
[26](design-principles.md#question-26),
[28](design-principles.md#question-28) and
[29](design-principles.md#question-29)). The approved names and signatures
(package `statelex`):

```go
func New(ms *modes.ModeSet) (*Lexer, error)

type Edit struct {
	Start, End int
	Text       []byte
}
func Insert(at int, text []byte) Edit
func Delete(start, end int) Edit
func (lx *Lexer) Apply(e Edit) (Change, error)
func (lx *Lexer) SetText(text []byte) (Change, error)

type Change struct {
	Start, OldEnd, NewEnd int
	Removed, Inserted     []Token
}
func (c Change) Shift() int

type Token struct {
	Type       bytescan.TokenType
	Start, End int
}

func (lx *Lexer) Len() int
func (lx *Lexer) Text(start, end int) []byte
func (lx *Lexer) Tokens(start, end int) []Token
func (lx *Lexer) ModeAtEnd() modes.Stack
func (lx *Lexer) Unfinished() (start int, ok bool)
func (lx *Lexer) Position(offset int) (line, column int, err error)
func (lx *Lexer) Offset(line, column int) (int, error)

var ErrRange, ErrSplitRune error
```

**Current contract**: `New`, `Edit`, `Insert`, `Delete`,
`Apply`, `SetText`, `Change`, `Change.Shift`, `Token`, `Len`, `ErrRange` and
`ErrSplitRune`, with these signatures. `New` accepts a set of several modes,
with the same signature. **Current contract** as well:
`Text`, `Tokens`, `ModeAtEnd`, `Unfinished`, `Position` and `Offset`, with
these signatures. `Example` in `statelex/example_test.go` shows the
implementer's side: it builds the lexer, loads a text, makes one edit and
applies the change. `statelex/example_queries_test.go` has one `Example`
for each query.

The implementer's side (an illustration of the approved names):

```go
ms := modes.NewModeSet("code")
ms.Mode("code").Rule("WORD", word).Rule("WS", spaces).Rule("NL", newline)
lx, err := statelex.New(ms)

ch, err := lx.Apply(statelex.Insert(0, text)) // load the text (case A35)
ch, err = lx.Apply(statelex.Edit{Start: a, End: b, Text: newBytes})
// The old tokens ch.Removed are gone from [ch.Start, ch.OldEnd).
// The new tokens ch.Inserted fill [ch.Start, ch.NewEnd).
// Every later token moves by ch.Shift() bytes.
```

The sections below state each part of this API:
[the constructor](#the-constructor), [the edit request](#the-edit-request),
[the end of the held text](#the-end-of-the-held-text),
[the change message](#the-change-message), [the queries](#the-queries),
[lines, columns and rune offsets](#lines-columns-and-rune-offsets) and
[errors](#errors). A bound on the work of one edit is deferred
([A bound on the work of one edit](#a-bound-on-the-work-of-one-edit)). The
packaging and the token store are answered below and under
[Scope and layers](#scope-and-layers).

**Current contract — the token store** (owner decision, 2026-11-18,
[question 30](design-principles.md#question-30)). The lexer with state holds
its text and its tokens in balanced trees:

- **the text** in a rope: a balanced tree of chunks, where each node keeps the
  summed byte count, the summed newline count and the summed rune count of
  its chunks (`ropeNode` in
  `statelex/rope.go`);
- **the tokens** in a balanced tree. Each leaf keeps the type of a token, its
  length, its [lookahead record](#the-lookahead-record) and the saved mode
  stack at its start. Each node keeps the summed length of its tokens and the
  furthest reach among them.

No token stores an absolute offset: the position of a token is found by
going down the tree, so moving every later token by `d` bytes costs nothing.
The furthest reach in each node finds the first affected token without
walking back over the tokens before the edit; a token can read far ahead (in
[A31](#strings-and-comments) it reads to the end of the text). The trees are
written in this module, with no new dependency, and they are not visible in
the API (`statelex/rope.go`, `statelex/tokens.go`). Each tree is a treap:
random priorities from a fixed seed keep it balanced in expectation. Each node
of the token tree also keeps one mark, "a token below read the end of the
text", for step 1.

**Current contract — the cost of one edit.** The work of one edit has these
parts:

- **Finding.** The edit goes down one path of the rope to find the held
  bytes, and one path of the token tree to find the first affected token. A
  path has about O(log n) nodes, where n is the number of chunks or of
  tokens.
- **The held text.** The rope copies the new bytes and the removed bytes of
  the edit. It also copies at most one small chunk on each side of the edit,
  which it joins to the new bytes, so that many small edits do not leave many
  small chunks. A split of a chunk slices the chunk and copies no byte. It
  counts the newlines and the runes of the smaller half, and takes the
  counts of the other half from those of the chunk. The new chunks are
  counted once.
- **The held tokens.** The token tree visits the old tokens of the re-lexed
  run and builds the new ones. It splits and joins the tree along a few
  paths.

So the work that can grow is the size of the edit and the length of the
re-lexed run. No part of the work grows with the untouched text after the
edit: no token stores an absolute offset, so the tokens after the edit move
with no work, and the counts change only on the path of the edit. Evidence:
`TestWorkOfASmallEdit`. It makes one insertion and one
deletion near the start of texts of three sizes, and counters of the lexer
that are not exported check each part of this cost. The structure is not
visible in the API.

### The constructor

**Current contract — one constructor that takes a mode set** (owner
decision, 2026-11-18, [question 23](design-principles.md#question-23)).
`New(ms *modes.ModeSet) (*Lexer, error)` returns a lexer that holds an empty
text. A rule set without modes is a mode set with one mode. Loading a text is
one edit of an empty lexer (case [A35](#inserting)).

- `New` checks the mode set. It returns the error of `ModeSet.Driver` when
  the definitions cannot be lexed (`TestNewChecksTheModeSet`).
- `New` accepts a set of several modes (`TestNewChecksTheModeSet`,
  `TestEditCasesRuleSetB`). This is a Current contract.
- The lexer reads the modes, their rules and their actions through a bridge
  between `modes` and `statelex` that is not exported: the internal package
  `internal/modedef`, which `modes/bridge.go` fills. `modes` gains no
  exported name for it. A change to the set after `New` does not change the
  lexer (`TestNewChecksTheModeSet`).
- The lexer builds one scanner per mode over one cursor, and drives them
  itself. The scanner of the mode at the top of the mode state reads the next
  token with `NextLookahead`, so the lexer has the
  [lookahead record](#the-lookahead-record) of each token. The action of the
  rule that made the token changes the mode state, as in a
  `modes.StackDriver`. A token that no rule of the mode made leaves the state
  unchanged, and a pop of the last mode does nothing
  (`TestPopOfTheLastMode`). When the top mode changes, the lexer calls
  `Release` on the scanner of the outgoing mode (`Lexer.next` in
  `statelex/relex.go`).
- The saved mode state of a token is a `modes.Stack`: one pointer, and
  stacks share their lower frames, which never change. When a token does not
  push or pop, the token after it saves the same state, with no copy
  (`tokNode.stack`, set in `Lexer.apply`). `TestWorkOfASmallEditInModes` checks the cost of one edit
  inside a string of rule set B, as `TestWorkOfASmallEdit` does for one
  mode.

**How it satisfies the constraint.** The change does not depend on how the
lexer was built, so the constraint of
[question 23](design-principles.md#question-23) is met by the change message
below. The constructor adds one way to define rules, the one that `modes`
already has, and it exposes no part of the token store.

### The edit request

**Current contract — one edit for each call** (owner decision 9 of
2026-11-18: the edit is one primitive, and it is a value; and the owner
decisions of 2026-11-18 on
[question 23](design-principles.md#question-23) and
[question 26](design-principles.md#question-26)).

- An `Edit` says "the old bytes `[Start, End)` became `Text`". An insertion
  has `Start == End`. A deletion has an empty `Text`. A replacement has both.
  `Insert(at, text)` and `Delete(start, end)` return those values. The lexer
  copies `Text`.
- `Apply` takes one edit and returns one change. Several edits, such as one
  for each cursor of a multi-cursor editor, are a loop of the caller. Each
  edit uses the offsets of the text as the edit before it left it.
- The unit of an edit is the byte. A line and a rune column go through
  `Offset` (see [Lines, columns and rune offsets](#lines-columns-and-rune-offsets)).
- `Apply` refuses an edit with `ErrRange` when `Start < 0`, `End < Start` or
  `End > Len()` (`TestApplyErrRange`). It refuses an edit with `ErrSplitRune`
  when `Start` or `End` falls inside a valid UTF-8 character of the held text
  (`TestApplyErrSplitRune`). A refused edit changes nothing: the held text and
  the held tokens stay, and a later edit works (both tests;
  `TestGeneratedEdits`).
- `Text` may hold any bytes, invalid UTF-8 included. A byte that does not
  form a valid character is read as one U+FFFD symbol, as in a scan of a
  stream, so it gives the tokens that the rules make of that symbol (often an
  `UNKNOWN` token of one byte).
- An offset next to a byte that is not part of a valid character is a
  character boundary. So an edit can always remove or complete broken bytes.
  The re-lex handles broken bytes that are already in the text with one
  clause of step 1 (see [The four steps](#the-four-steps)). Evidence:
  `TestApplyAtCharacterBoundaries`, `TestBrokenBytesBeforeTheEdit`.

**Current contract — the whole new text.** `SetText(text)` serves an
editor that reports only the whole new text. It compares `text` with the held
text, drops the common start and the common end, and applies one edit for the
middle. It widens that middle to character boundaries of the held text, so it
never returns `ErrSplitRune`: `ñ` and `ó` share their first byte, and the
middle is the whole character, not its second byte. The comparison reads the
whole text, so the cost of `SetText` grows with the size of the text. The
re-lex after it is the same as for the equal `Apply`. Evidence:
`TestSetTextWidensToCharacters`; `TestGeneratedEdits`, in which some edits go
through `SetText`.

**How it satisfies the constraint of question 26.** The tokens after an edit
equal those of a scan of the new bytes from the start, because the held text
is exactly the new bytes and [The result](#the-result) holds for every
accepted edit. A refused edit changes neither the text nor the tokens, so the
held tokens still equal a scan of the held bytes. The clause of step 1 keeps
the restart exact when broken bytes before the edit decode differently after
it.

### The end of the held text

**Current contract — always the end of input** (owner decision,
2026-11-18, [question 17](design-principles.md#question-17)). At the end of
its text, the lexer with state shows the rules the end of input, always. So
every byte of the held text belongs to a token. The tokens at the end are
those of a scan of the whole text as a stream: an unclosed `"` gives
`UNKNOWN`, and an unclosed `/*` gives `OP` tokens
([rule set A](#rule-set-a)). Evidence: `TestEditCasesRuleSetA` (cases A23,
A31, A32 and A33); `TestGeneratedEdits`. A REPL sees
an open string or an open comment through the queries `ModeAtEnd` and
`Unfinished` (see [The queries](#the-queries); `TestQueriesREPL`).

**How it satisfies the constraint.** The tokens equal those of a scan of the
whole text under the same choice, because the choice is the one of a stream
scan that ends at the end of the text. An edit at the end of the text
re-lexes every token whose rules were shown the end of input: step 1 counts
such a token as affected (the `ReadEnd` mark of the
[lookahead record](#the-lookahead-record)).

### The change message

**Current contract.** The change message is the contract between the
lexer with state and the implementer. One edit re-lexes one continuous run
of tokens, so one edit gives one change:

> these old tokens are removed, these new tokens are inserted, and
> everything after moves by `d` bytes.

- The tokens that did not change at both ends of the re-lexed run are
  trimmed: they are not in the message (see
  [What the change contains](#what-the-change-contains)).
- The message carries a byte range and tokens with their type and their
  position. It carries no internal index of the lexer with state.

**Current contract — a complete value** (owner decision, 2026-11-18,
[question 23](design-principles.md#question-23)). `Apply` returns the change
as a `Change` value:

- the old bytes `[Start, OldEnd)` became the new bytes `[Start, NewEnd)`;
- `Removed` lists the old tokens in `[Start, OldEnd)`, with offsets in the
  old text, and `Inserted` lists the new tokens in `[Start, NewEnd)`, with
  offsets in the new text;
- every old token from `OldEnd` on moves by `Shift()` bytes, which is
  `NewEnd - OldEnd`;
- an empty change has no removed and no inserted token, and its `Shift()` is
  0. Its `Start`, `OldEnd` and `NewEnd` are equal: the place where the two
  trims meet (see [What the change contains](#what-the-change-contains)).

Evidence: `TestEditCasesRuleSetA` checks these bounds for every row, and
`TestGeneratedEdits` checks them after each generated edit. In
`TestGeneratedEdits`, a consumer keeps only a list of tokens, each a type and a
byte range. It applies each change and reads nothing else from the lexer.
After each edit, its list equals the tokens of a scan of the whole new text.
`TestModesChangeIsEnough` runs the same consumer under the mode sets of
`TestGeneratedEditsModes`.

A `Token` is a type and a byte range in the whole text. It carries no mode
state; the mode state at the end of the text is read through `ModeAtEnd`
(see [The queries](#the-queries)). A token has no identity that
survives an edit: it is its type and its range. A `Token` is not a
`bytescan.Token`: a re-lex reports positions from its restart point
([Starting a scan in the middle](#starting-a-scan-in-the-middle)), so the
lexer gives byte offsets in the whole text instead. A `Change` is a plain
value: the implementer can keep it, queue it, log it or compare it.

**How it satisfies the constraint of question 23.** One edit gives one change,
because `Apply` takes one edit. The change is enough: the tokens of a scan
cover every byte, so the removed tokens cover `[Start, OldEnd)` and the
inserted tokens cover `[Start, NewEnd)`. An implementer that drops its tokens
in `[Start, OldEnd)`, adds `Inserted` and moves the later tokens by
`Shift()` holds the tokens of the new text, and it reads no other token. The
change holds byte offsets and `Token` values only, so it exposes no part of
the token store.

### The queries

**Current contract — five queries** (owner decision, 2026-11-18,
[question 28](design-principles.md#question-28)). Besides
the change, the lexer answers the queries below. `Len` is a Current contract
(`Lexer.Len`; `TestApplyErrRange`).

| Query | Answer | Cost |
| --- | --- | --- |
| `Len()` | the number of bytes of the held text | O(1) |
| `Text(start, end)` | a copy of the held bytes in the range | O(log n), plus the bytes returned |
| `Tokens(start, end)` | every token that overlaps the range, whole | O(log n), plus the tokens returned |
| `ModeAtEnd()` | the mode state after the last token: the start mode alone for an empty text | O(1): the lexer keeps it after each edit |
| `Unfinished()` | the start of the first token whose rules were shown the end of input, and `true`; or `false` when no token read it (see below for a text that ends inside an incomplete character) | O(log n): each node of the token tree keeps one mark, "a token below read the end" |

The positions `Position` and `Offset` are under
[Lines, columns and rune offsets](#lines-columns-and-rune-offsets).

- A range is clipped to `[0, Len()]`. An empty or an inverted range gives no
  token and no byte. A query never fails (owner decision, 2026-11-18,
  [question 28](design-principles.md#question-28)). Evidence: `TestQueriesLimits`.
- `Tokens` returns every token that overlaps `[start, end)`, whole, with its
  full byte range: a token that crosses an end of the range is returned with
  its bytes outside the range (owner decision, 2026-11-18). Evidence:
  `TestQueriesLimits`, `ExampleLexer_Tokens`.
- When the text does not end inside an incomplete UTF-8 character,
  `Unfinished` reports the place where a scan with "need more input" at the
  end of the text would stop: the tokens before that place are complete under
  [Need more input](#need-more-input), and the token there is not.
- When the text ends inside an incomplete UTF-8 character, `Unfinished` also
  returns `true` (owner decision, 2026-11-18). It reports the start of the
  incomplete character, or the start of the token that holds that character,
  if that token starts earlier. A token
  whose rules read the end of input and that starts earlier still comes
  first. An incomplete character is one to three bytes at the end of the text
  that start with a first byte and that more bytes could complete: `\xc3`
  is one, `\xe2\x28` and `\xff` are not. Under [rule set A](#rule-set-a), for the
  texts `\xc3`, `foo\xc3` and `\xe2\x82`, no token has the `ReadEnd` mark, and
  `Unfinished` returns 0, 3 and 0, with `true`. A scan with "need more
  input" stops at offset 0 for `foo\xc3`, because `WORD foo` needs the next
  symbol to end. Evidence: `TestUnfinishedIncompleteCharacter`.
- A query for the token at an offset and a query for the mode state at an
  offset are not part of this milestone.
- No query takes or returns a node of the token store: the answers are
  bytes, `Token` values, a `modes.Stack` and offsets (`statelex/queries.go`).

Evidence that each query gives the value of a scan of the whole new text:
`TestQueriesAfterGeneratedEdits`. It applies many generated edits, from a
fixed seed, under [rule set A](#rule-set-a), [rule set B](#rule-set-b-modes)
and a set that pushes two levels. After each edit, `Text` and `Tokens` for
random, clipped, empty and inverted ranges, `ModeAtEnd` and `Unfinished`
equal the values of a scan of the whole new text from its start.

**The REPL.** A REPL decides between "run" and "continue" from the lexer
alone. In rule set B the line `say␣"` gives three complete tokens, and
`ModeAtEnd()` is `[code str]`: the string is open. For `say␣"hi"` it is
`[code]`. In rule set A the line `a␣/*␣x⏎b⏎` gives `OP /` at offset 2, whose
rules read the end of the text, so `Unfinished()` returns 2 and `true`: the
comment is open. Evidence: `TestQueriesREPL`, `ExampleLexer_ModeAtEnd`,
`ExampleLexer_Unfinished`.

**How it satisfies the constraint.** No query takes or returns a node of the
token store. A query for a small range finds its start by going down one path
of a tree, and then reads only what it returns, so it stays small work in a
large text. Evidence: `TestWorkOfASmallQuery`. It asks for the same small
range and the same offset in texts of three growing sizes, in which the text
and the tokens outside the range grow and the answer does not. Counters of
the lexer that are not exported report the held tokens, the nodes of the rope
and the bytes that each query visited. The counts stay inside the cost of the
table above, and they do not grow in proportion to the text outside the
range.

### Lines, columns and rune offsets

**Current contract — computed on request** (owner decision, 2026-11-18,
[question 25](design-principles.md#question-25)). The
rope of the held text also keeps, in each node, the number of newlines and
the number of runes of its chunks (`ropeNode` in `statelex/rope.go`). A rune
is a symbol of the decoder: a valid UTF-8 character, or one byte that does
not start a valid character. No valid character crosses the boundary of two
chunks, so the counts of a chunk do not depend on the chunks around it.
Evidence: `checkTrees`, called after the edits of `TestQueriesOverManyChunks`
and `TestGeneratedEdits`.

- `Position(offset)` returns the line, counted from 1, and the rune column,
  counted from 0, of a byte offset, as the cursor counts them today
  (`TestCursorPositions`). `Len()` is a place in the text.
- `Offset(line, column)` returns the byte offset of a line and a rune column.
  The column can be the number of runes of the line: the place of the
  newline that ends it, or `Len()` on the last line.
- `Position` and `Offset` return `ErrRange` for a place outside the text: an
  offset outside `[0, Len()]`, a line that the text does not have, a
  negative column, or a column past the end of its line (owner decision,
  2026-11-18).
- `Position` returns `ErrSplitRune` for an offset inside a valid multi-byte
  character, as `Apply` does for an edit (owner decision, 2026-11-19). A
  byte that does not form a valid character is a character boundary, so it
  is never refused, and it is one column.
- `Position` and `Offset` are exact inverses at every place they accept.
- Columns are rune columns. UTF-16 columns are not part of this milestone.
- The change and the queries carry byte offsets only. No token stores a line,
  a column or a rune offset.

Evidence: `TestQueriesAfterGeneratedEdits` checks `Position` at every offset
and `Offset` back at every place after each generated edit, against a cursor
over the whole new text. `TestCoordinatesAfterEdits` does so after an edit
that adds a newline, one that removes a newline and one that adds a
multi-byte character. `TestQueriesLimits` checks the limits.
`ExampleLexer_Position` and `ExampleLexer_Offset` show both.

**How it satisfies the constraint.** An edit updates the counts on its path
in the rope only: one path, plus the new bytes and the smaller half of a
chunk that a split slices. No token stores a coordinate, so a small edit near
the start of a large text stays small work (`TestWorkOfASmallEdit`). Reading
a coordinate goes down one or two paths of the rope and counts a part of one
chunk on each, in either direction (`TestWorkOfASmallQuery`).

### Errors

**Current contract — no option that can fail a re-lex** (owner decision,
2026-11-18, [question 29](design-principles.md#question-29)).

- The lexer takes no scanner option that can fail a token, such as
  `WithMaxTokenBytes` or a retention limit: `New` takes no option. Its text is
  already in memory, so such a bound would protect nothing. A rule panic is
  not an error: the VM recovers it ([Panics](rule-vm-contract.md#panics)).
- `Apply` and `SetText` check the edit before any work. `ErrRange` and
  `ErrSplitRune` (see [The edit request](#the-edit-request)) change nothing
  (`TestApplyErrRange`, `TestApplyErrSplitRune`).
- `New` checks the mode set and returns its error
  (`TestNewChecksTheModeSet`).
- A broken engine invariant during a re-lex is an internal error. The
  returned error wraps the existing `bytescan.ErrInternal`, so `errors.Is`
  matches it. The package adds no exported name for it (owner decision,
  2026-11-18).
  After it, every later call that can return an error returns the same error.
  Evidence: `TestInternalError`, with a test hook that simulates the broken
  invariant. `Position` and `Offset` also return it, for
  every input (`TestInternalErrorCoordinates`).

**How it satisfies the constraint.** After a refused edit, the held text and
the held tokens are those from before the edit, so they agree. No option can
fail a token, so no edit stops halfway with a text that its tokens do not
match. After an internal error, the lexer says that the whole text is not in
agreement: it refuses every later edit.

### A bound on the work of one edit

**Deferred by the owner** (2026-11-18,
[question 27](open-questions.md#question-27); the question stays open). An
edit that changes the mode state can have no sync point before the end of the
text (cases [B1 and B2](#rule-set-b-modes)). Such an edit re-lexes to the end
of the text: its work is the rest of the text. The lexer does not stop early
(`TestModesNotASyncPoint`).

The reason for the deferral: the examples of this milestone hold small
texts; the owner put optimization after the final feature set; and a bound
touches the change, the queries and the errors, so it is designed once, with
a real case of a large text.

## How an edit is re-lexed

### The result

**Current contract.** After an edit, the lexer with state holds exactly
the tokens that a scan of the whole new text from its start produces. The
change message is the difference between the tokens before and the tokens
after. The steps below are a way to reach that result with less work; they
never change the result. A test of the lexer with state can therefore
compare every change against two full scans. Evidence: `TestGeneratedEdits`
does that for many edits from a fixed seed, under three sets of pure rules:
rule set A, a set in which rules read far ahead, and a set in which rules
read the BOF and BOL flags. `TestGeneratedEditsModes` does it under
[rule set B](#rule-set-b-modes) and under a set that pushes two levels, and
it also compares the mode state at the start of each token with that of a
scan by a new `modes.StackDriver`.

### The four steps

**Current contract** (`Lexer.apply` in `statelex/relex.go`). An edit
replaces the old bytes `[A, B)` with `n` new bytes. `d = n - (B - A)` is the change in length; a later byte at old
offset `x` is at new offset `x + d`.

1. **Restart.** An old token is *unaffected* when its
   [reach](#the-lookahead-record) is at most `A`, and it did not read the end
   of the text or the edit is not at the end of the text. Every other old
   token that starts before `B` is *affected*. The re-lex restarts at the
   start of the first affected token, in the mode state saved at that
   token's start. When no token is affected, the re-lex restarts at the end
   of the last token that ends at or before `A`, or at the start of the text
   when there is no such token.

   **Broken bytes before the edit** (owner decision, 2026-11-18,
   [question 26](design-principles.md#question-26)). When the three bytes
   before `A` hold the first byte of a UTF-8 character that is not complete
   before `A`, step 1 uses the offset of that byte in place of `A`: in the
   test "the reach is at most `A`" and in the restart when no token is
   affected. The test for an edit at the end of the text still uses `A`.
   Evidence: `TestBrokenBytesBeforeTheEdit`.
2. **Lex forward** from the restart point, across the edit.
3. **Test for sync** at each token boundary of the re-lex that is at a new
   offset `p` at or after the end of the edit (`p >= A + n`). The boundaries
   are the restart point itself and the end of each new token. The test: did
   an old token start at the same place in the unchanged text, that is at
   old offset `p - d`, in a saved mode state equal to the current mode
   state, with the same BOF and BOL flags at its first symbol? The flags are
   computed from the text: BOF when the offset is 0, BOL when the offset is
   0 or the byte before it is `\n`. They are compared at new offset `p` in
   the new text and at old offset `p - d` in the old text (see
   [Why the sync test is exact](#why-the-sync-test-is-exact)).
4. **Stop** on the first yes. Every old token from that one on stays, moved
   by `d` bytes. When the answer is never yes, the re-lex runs to the end of
   the text.

Evidence: `TestEditCasesRuleSetA` and `TestEditCasesRuleSetB` check the
restart point and the sync point of every row of rule sets A and B through a
hook that is not exported. For each of
the ten backspaces, the re-lexed run is shorter than the text.

The restart point is never after `A`. So step 3 tests the restart point
itself only when the restart is at `A` and the edit inserts nothing
(`n = 0`). In case [A7](#deleting) the edit deletes `[0,6)` and the restart
is at offset 0. There the old token `WORD y` at old offset 6 starts at the
same place, in the same mode state, but its first symbol gains the BOF flag.
So the restart point is not a sync point: the re-lex lexes `WORD y [0,1)`
and syncs at the next boundary, new 1 = old 7. The new `WORD y [0,1)` has the
type, the moved range and the bytes of the old `WORD y [6,7)`, so the change
leaves it out (see [What the change contains](#what-the-change-contains)).

The first affected token is not always the token that holds offset `A`. A
token that ends before the edit can have read into it: in case
[A12](#replacing) the edit is at offset 3 and the restart is at offset 0,
two tokens earlier.

The clause for broken bytes exists because the reach counts symbols, not the
bytes that the UTF-8 decoder examined. A byte that starts a character which
is not complete is read as one U+FFFD symbol of one byte, but the decoder
looked at the next bytes to decide that. In `ni\xc3o⏎` the held tokens are
`WORD ni [0,2)`, `UNKNOWN [2,3)`, `WORD o [3,4)` and `NL ⏎ [4,5)`, and
`UNKNOWN [2,3)` has a reach of 3. An insertion of `\xb1` at 3 completes the
character `ñ`, and a scan of the new text gives `WORD niño [0,5)`. Without
the clause, step 1 restarts at 3 and misses the join. With it, `A` counts as
2, `UNKNOWN [2,3)` is affected, and the restart is at 0. An edit never
starts or ends inside a valid character (see
[The edit request](#the-edit-request)), so the clause matters only for
broken bytes.

The test of step 3 is not applied before the end of the edit. A boundary
inside the new bytes has no old counterpart. A boundary before `A` can be
followed by a token that reads into the edit, so the same place and the same
state do not prove the same token there: how far a token reads does not grow
steadily from one token to the next.

### Why the sync test is exact

**Current contract.** The sync test is exact, not a heuristic. Evidence:
`TestGeneratedEdits` and `TestGeneratedEditsModes` compare the held tokens
with a full scan after each edit. It rests
on the dependence stated under
[What the engine does today](#what-the-engine-does-today): with pure rules,
a token depends only on the rule set of the active mode, on the bytes from
its start to its reach, and on the BOF and BOL flags there. At a sync point
the new scan and the old scan are at the same place of the unchanged text,
in the same mode state, with the same bytes ahead. So they produce the same
tokens from there to the end of the text, and the mode state after each of
those tokens is the same too.

Two things can break that argument:

- **The positional flags.** The old token and the new token can sit at the
  same place of the unchanged text with different BOF or BOL flags. In case
  [A7](#deleting) the first token of the old second line becomes the first
  token of the text. **Current contract** (owner decision, 2026-11-18,
  [question 18](design-principles.md#question-18)): the flags of the first
  symbol are part of the sync test (step 3). Evidence:
  `TestFlagsDeleteFirstLine`, `TestFlagsInsertNewlineBeforeBoundary`,
  `TestFlagsRemoveNewlineBeforeBoundary`; cases A7, A26, A27 and A29 of
  `TestEditCasesRuleSetA`. **How it satisfies the
  constraint:** the BOF flag of a symbol depends only on its offset being 0,
  and the BOL flag only on the byte before it. At a candidate `p` after the
  end of the edit, every byte from `p` on is unchanged, so every symbol after
  the first one has the same flags in both scans. The first symbol is the
  only one whose flags can differ, and step 3 compares them. So the argument
  above holds for a rule that reads a flag. Nothing is stored for each token,
  and an edit that changes no flag syncs as early as before.
- **The end of the text.** What a token sees when its rules reach the end of
  the held text. **Current contract** (owner decision, 2026-11-18,
  [question 17](design-principles.md#question-17)): the rules see the end of
  input there, always (see [The end of the held text](#the-end-of-the-held-text)).
  **How it satisfies the constraint:** the old scan and the new scan show
  the rules the same end at the same place of the unchanged text, so the
  argument above holds up to the end of the text. A token that was decided
  by the end of the text has the `ReadEnd` mark, and step 1 re-lexes it when
  an edit is at the end.

### What the change contains

**Current contract.** The re-lexed run goes from the restart point to the
sync point, or to the end of the text. The change is that run with its
unchanged ends trimmed.

**Current contract — same type, same range, same bytes** (owner decision,
2026-11-18, [question 24](design-principles.md#question-24)). A token at
either end of the run is left out of the change when the old token and the
new token have the same type, the same range and the same bytes:

- **At the start**, the range is the same offsets in both texts. The trim
  does not stop at `A`: a token that touches the edited bytes is left out
  when its type, its range and its bytes did not change (case
  [A24](#replacing): `bar` replaced with `bar` gives an empty change). The
  restart point is therefore not visible in the change: a restart that is
  earlier than necessary gives the same change.
- **At the end**, nothing from the sync point on is in the change. Before the
  sync point, the range of the new token is the range of the old token moved
  by `d`. A token whose mode state at its start or whose BOF or BOL flags
  differ is left out too, because the change shows neither (cases
  [A7, A26](#deleting), [A27 and A29](#inserting);
  `TestModesTrimLeavesOutAChangedState` for the mode state). The same trim
  applies when the re-lex ran to the end of the text (case
  [A8](#replacing)).

What stays between the two trimmed ends is the change: its old tokens are
the "removed" tokens and its new tokens are the "inserted" tokens. One change
is one continuous run, so a token inside the run that is equal in both texts
stays in the change (in A8, `WS ␣ [3,4)`). A change can have no removed
token, no inserted token, or neither.

**Current contract — where an empty change sits** (owner decision,
2026-11-19).
When the two trims leave no token, the change is empty. It sits where the
trim at the start and the trim at the end meet: `Start`, `OldEnd` and
`NewEnd` are equal at that place, `Removed` and `Inserted` are empty, and
`Shift()` is 0. In case [A24](#replacing) the trim at the start leaves out
`WS ␣ [3,4)` and `WORD bar [4,7)`, so the place is offset 7. Evidence:
`TestEmptyChangePlace`; `trim` in `statelex/relex.go`.

Evidence for the trim: `TestEditCasesRuleSetA`
(cases A7, A8, A24, A25, A26, A27 and A29); `TestGeneratedEdits`, which checks
after each edit that the first and the last token of the change differ in
type, range or bytes, and that the change ends at or before the sync point.

**How it satisfies the constraint.** A token whose bytes changed is never
left out, because the trim compares the bytes. So an implementer that keeps
the text of a token learns that the text changed: in case A25, `bar`
replaced with `baz` gives `WORD bar [4,7)` removed and `WORD baz [4,7)`
inserted. A token is left out only when its type, its range and its bytes
are what the implementer already holds. The comparison reads only the bytes
of the trimmed tokens, inside the run.

## Edit cases

Each row states the change for one edit: the
removed tokens, the inserted tokens and the sync point. A row is the
difference that [The result](#the-result) defines, trimmed as
[What the change contains](#what-the-change-contains) states, with the end of
the held text as the end of input
([question 17](design-principles.md#question-17)).

The rows were computed with the engine at `master`, by lexing the text
before the edit and the text after the edit from the start and comparing the
two results, with rule sets built from the shipped rule constructors. The
rows A7, A26, A27 and A29 were computed again in the same way after the
owner's answer to [question 18](design-principles.md#question-18), with the
flags in the sync test; no other row of rule set A changed. After the owner's
answers to [question 17](design-principles.md#question-17) and
[question 24](design-principles.md#question-24), every row of rule sets A and
B was computed again in the same way, with the engine at `master`
(`ef4c0f0`) and the trim of question 24. Six rows changed: A7, A8, A24, A26,
A27 and A29. The rows that question 17 concerned did not change, because they
were already computed with the end of input.

**Current contract — rule set A.** `TestEditCasesRuleSetA` in
`statelex/table_test.go` holds every row of rule set A, in the notation of
this document: A1 to A35, the ten backspaces and the one deletion. For each
row it checks the removed tokens, the inserted tokens, the restart point and
the sync point, and it checks the held tokens against a scan of the new text.
`TestTenBackspacesInARow` applies the ten backspaces to one lexer, one after
the other.

**Current contract — rule set B**.
`TestEditCasesRuleSetB` in `statelex/modes_test.go` holds the rows B1 to B4
of [rule set B](#rule-set-b-modes), copied from the table, with the mode
state at the start of each token. For each row it checks the removed tokens
and the inserted tokens with their mode states, the restart point and the
sync point, and it checks the held tokens and their mode states against a
scan of the new text by a new `modes.StackDriver`.

### Notation

- `␣` is one space and `⏎` is one newline (`\n`). Every text is ASCII, so a
  byte offset is also a rune offset.
- A token is written `TYPE text [start,end)`, with byte offsets. A removed
  token has its offsets in the text before the edit. An inserted token has
  its offsets in the text after the edit.
- **Restart** is the offset that step 1 gives, computed from the measured
  reach of every old token. It explains the work; it is not part of the
  change.
- **Sync** is `new p = old q`: the sync point of step 4, as the new offset
  and the old offset of the first token from which every old token stays,
  with its type. "none" means that the re-lex ran to the end of the text.
  Tokens just before the sync point, or at the end of the text, can also be
  left out of the change by the trim of
  [What the change contains](#what-the-change-contains).
- `—` means no token.

### Rule set A

One scanner, no modes. The rules are listed in registration order.

| Type | Matches |
| --- | --- |
| `WORD` | one or more ASCII letters |
| `NUMBER` | one or more digits, then optionally `.` and one or more digits |
| `OP` | `==`, or one of `=`, `+`, `*`, `/`, `.` |
| `STRING` | `"`, then any characters except `"` and newline, then `"` |
| `COMMENT` | `/*`, then anything up to the next `*/`; it can span lines |
| `WS` | one or more spaces |
| `NL` | one newline |

A `"` or a `/*` that is never closed does not match `STRING` or `COMMENT`.
The engine then reports what the other rules give: `UNKNOWN` for the `"`,
and `OP` for the `/` and the `*`
([Unmatched input, EOF, and errors](consumer-contract.md#unmatched-input-eof-and-errors)).

### Deleting

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| A1 join two words | `foo␣bar⏎` | delete `[3,4)` | 0 | `WORD foo [0,3)`, `WS ␣ [3,4)`, `WORD bar [4,7)` | `WORD foobar [0,6)` | new 6 = old 7, `NL` |
| A2 join two operators | `a␣=␣=␣b⏎` | delete `[3,4)` | 2 | `OP = [2,3)`, `WS ␣ [3,4)`, `OP = [4,5)` | `OP == [2,4)` | new 4 = old 5, `WS` |
| A3 join a number | `12␣.5⏎` | delete `[2,3)` | 0 | `NUMBER 12 [0,2)`, `WS ␣ [2,3)`, `OP . [3,4)`, `NUMBER 5 [4,5)` | `NUMBER 12.5 [0,4)` | new 4 = old 5, `NL` |
| A4 delete a word between spaces | `one␣two␣three⏎` | delete `[4,7)` | 3 | `WS ␣ [3,4)`, `WORD two [4,7)`, `WS ␣ [7,8)` | `WS ␣␣ [3,5)` | new 5 = old 8, `WORD` |
| A5 delete inside a token | `hello␣world⏎` | delete `[2,4)` | 0 | `WORD hello [0,5)` | `WORD heo [0,3)` | new 3 = old 5, `WS` |
| A6 delete a selection across tokens | `a␣=␣b␣+␣c␣*␣d⏎` | delete `[5,9)` | 4 | `WS ␣ [5,6)`, `OP + [6,7)`, `WS ␣ [7,8)`, `WORD c [8,9)` | — | new 5 = old 9, `WS` |
| A7 delete a chunk at the beginning | `x␣=␣1⏎y␣=␣2⏎z␣=␣3⏎` | delete `[0,6)` | 0 | `WORD x [0,1)`, `WS ␣ [1,2)`, `OP = [2,3)`, `WS ␣ [3,4)`, `NUMBER 1 [4,5)`, `NL ⏎ [5,6)` | — | new 1 = old 7, `WS` |
| A26 join two lines | `a␣=␣1⏎b␣=␣2⏎` | delete `[5,6)` | 4 | `NL ⏎ [5,6)` | — | new 6 = old 7, `WS` |
| A34 delete everything | `x␣=␣1⏎` | delete `[0,6)` | 0 | `WORD x [0,1)`, `WS ␣ [1,2)`, `OP = [2,3)`, `WS ␣ [3,4)`, `NUMBER 1 [4,5)`, `NL ⏎ [5,6)` | — | none |

In A6 the deletion leaves `a␣=␣b␣*␣d⏎`. The space before `+` is removed and
the space before `*` stays, because the first boundary at or after the end
of the edit is the sync point.

### Inserting

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| A9 insert inside a word | `ab␣cd⏎` | insert `X` at 1 | 0 | `WORD ab [0,2)` | `WORD aXb [0,3)` | new 3 = old 2, `WS` |
| A10 an insertion splits a token | `foobar⏎` | insert `␣` at 3 | 0 | `WORD foobar [0,6)` | `WORD foo [0,3)`, `WS ␣ [3,4)`, `WORD bar [4,7)` | new 7 = old 6, `NL` |
| A11 insert new tokens between tokens | `a␣b⏎` | insert `+␣` at 2 | 1 | — | `OP + [2,3)`, `WS ␣ [3,4)` | new 4 = old 2, `WORD` |
| A27 split a line | `a␣=␣1␣b␣=␣2⏎` | replace `[5,6)` with `⏎` | 4 | `WS ␣ [5,6)` | `NL ⏎ [5,6)` | new 7 = old 7, `WS` |
| A29 insert a line at the beginning | `b␣=␣2⏎` | insert `a␣=␣1⏎` at 0 | 0 | — | `WORD a [0,1)`, `WS ␣ [1,2)`, `OP = [2,3)`, `WS ␣ [3,4)`, `NUMBER 1 [4,5)`, `NL ⏎ [5,6)` | new 7 = old 1, `WS` |
| A30 append a line at the end | `a␣=␣1⏎` | insert `b␣=␣2⏎` at 6 | 6 | — | `WORD b [6,7)`, `WS ␣ [7,8)`, `OP = [8,9)`, `WS ␣ [9,10)`, `NUMBER 2 [10,11)`, `NL ⏎ [11,12)` | none |
| A35 load a text into an empty lexer | (empty) | insert `x␣=␣1⏎` at 0 | 0 | — | `WORD x [0,1)`, `WS ␣ [1,2)`, `OP = [2,3)`, `WS ␣ [3,4)`, `NUMBER 1 [4,5)`, `NL ⏎ [5,6)` | none |

In A30 no old token is affected: the last token, `NL`, was decided without
reading past its own end, so the re-lex restarts at offset 6 and no old
token is lexed again.

### Replacing

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| A8 select all, then paste | `x␣=␣1⏎y␣=␣2⏎` | replace `[0,12)` with `say␣"hi"⏎` | 0 | the first eleven tokens, `[0,11)` | `WORD say [0,3)`, `WS ␣ [3,4)`, `STRING "hi" [4,8)` | none |
| A12 a token that read far ahead | `12.x⏎` | replace `[3,4)` with `5` | 0 | `NUMBER 12 [0,2)`, `OP . [2,3)`, `WORD x [3,4)` | `NUMBER 12.5 [0,4)` | new 4 = old 4, `NL` |
| A13 replace a selection | `a␣=␣b␣+␣c⏎` | replace `[4,9)` with `12` | 3 | `WORD b [4,5)`, `WS ␣ [5,6)`, `OP + [6,7)`, `WS ␣ [7,8)`, `WORD c [8,9)` | `NUMBER 12 [4,6)` | new 6 = old 9, `NL` |
| A24 replace a word with itself | `foo␣bar⏎` | replace `[4,7)` with `bar` | 3 | — | — | new 7 = old 7, `NL` |
| A25 same shape, new bytes | `foo␣bar⏎` | replace `[4,7)` with `baz` | 3 | `WORD bar [4,7)` | `WORD baz [4,7)` | new 7 = old 7, `NL` |

In A12 the old `NUMBER 12` ends at offset 2, and its rule read `.` and `x`
before it gave up on a fraction: its reach is 4. The edit at offset 3 is
inside that reach, so the restart is at offset 0.

In A24 and A25 the old `WORD` and the new `WORD` have the same type and the
same range. In A24 the bytes are the same too, so the change is empty. In A25
the bytes differ, so the token is in the change. In A8 the final `NL ⏎` is
the same in both texts, moved by `d = -3`, so the trim at the end leaves it
out; `WS ␣ [3,4)` is also in both texts, but it is inside the run, so it
stays in the change.

### Strings and comments

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| A14 type an opening quote | `x␣=␣hi␣there⏎` | insert `"` at 4 | 3 | — | `UNKNOWN " [4,5)` | new 5 = old 4, `WORD` |
| A15 type the closing quote | `x␣=␣"hi␣there⏎` | insert `"` at 13 | 4 | `UNKNOWN " [4,5)`, `WORD hi [5,7)`, `WS ␣ [7,8)`, `WORD there [8,13)` | `STRING "hi␣there" [4,14)` | new 14 = old 13, `NL` |
| A16 delete the closing quote | `x␣=␣"hi␣there"␣+␣y⏎` | delete `[13,14)` | 4 | `STRING "hi␣there" [4,14)` | `UNKNOWN " [4,5)`, `WORD hi [5,7)`, `WS ␣ [7,8)`, `WORD there [8,13)` | new 13 = old 14, `WS` |
| A17 delete the opening quote | `x␣=␣"hi␣there"␣+␣y⏎` | delete `[4,5)` | 3 | `STRING "hi␣there" [4,14)` | `WORD hi [4,6)`, `WS ␣ [6,7)`, `WORD there [7,12)`, `UNKNOWN " [12,13)` | new 13 = old 14, `WS` |
| A18 delete the first of two comment ends | `a␣/*␣x␣*/␣b␣/*␣y␣*/␣c⏎` | delete `[7,9)` | 2 | `COMMENT /*␣x␣*/ [2,9)`, `WS ␣ [9,10)`, `WORD b [10,11)`, `WS ␣ [11,12)`, `COMMENT /*␣y␣*/ [12,19)` | `COMMENT /*␣x␣␣b␣/*␣y␣*/ [2,17)` | new 17 = old 19, `WS` |
| A19 delete the only comment end | `a␣/*␣x␣*/␣b⏎c⏎` | delete `[7,9)` | 2 | `COMMENT /*␣x␣*/ [2,9)`, `WS ␣ [9,10)` | `OP / [2,3)`, `OP * [3,4)`, `WS ␣ [4,5)`, `WORD x [5,6)`, `WS ␣␣ [6,8)` | new 8 = old 10, `WORD` |
| A20 delete the comment start | `a␣/*␣x␣*/␣b⏎` | delete `[2,4)` | 1 | `WS ␣ [1,2)`, `COMMENT /*␣x␣*/ [2,9)` | `WS ␣␣ [1,3)`, `WORD x [3,4)`, `WS ␣ [4,5)`, `OP * [5,6)`, `OP / [6,7)` | new 7 = old 9, `WS` |
| A21 type a comment start before an existing end | `a␣x␣*/␣b⏎c⏎` | insert `/*␣` at 2 | 1 | `WORD x [2,3)`, `WS ␣ [3,4)`, `OP * [4,5)`, `OP / [5,6)` | `COMMENT /*␣x␣*/ [2,9)` | new 9 = old 6, `WS` |
| A22 type a comment start with no end | `a␣x␣b⏎c⏎` | insert `/*␣` at 2 | 1 | — | `OP / [2,3)`, `OP * [3,4)`, `WS ␣ [4,5)` | new 5 = old 2, `WORD` |
| A31 edit after an unclosed comment start | `a␣/*␣x⏎b⏎` | insert `z` at 8 | 2 | `WORD b [7,8)` | `WORD bz [7,9)` | new 9 = old 8, `NL` |
| A32 close the comment at the end | `a␣/*␣x⏎b⏎` | insert `␣*/` at 8 | 2 | `OP / [2,3)`, `OP * [3,4)`, `WS ␣ [4,5)`, `WORD x [5,6)`, `NL ⏎ [6,7)`, `WORD b [7,8)` | `COMMENT /*␣x⏎b␣*/ [2,11)` | new 11 = old 8, `NL` |

In A31 the old `OP /` at `[2,3)` was decided after the `COMMENT` rule read to
the end of the text and found no `*/`. Its reach is the end of the text, so
every later edit restarts at offset 2. The five tokens from offset 2 to
offset 7 come out the same and are trimmed, and the change is one token.
In A32 the same restart finds the new `*/`, and six tokens become one.

### The end of the text

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| A23 type at the end, no final newline | `foo` | insert `d` at 3 | 0 | `WORD foo [0,3)` | `WORD food [0,4)` | none |
| A28 type before the final newline | `x␣=␣fo⏎` | insert `o` at 6 | 4 | `WORD fo [4,6)` | `WORD foo [4,7)` | new 7 = old 6, `NL` |
| A33 close a string at the very end | `x␣=␣"ab` | insert `"` at 7 | 4 | `UNKNOWN " [4,5)`, `WORD ab [5,7)` | `STRING "ab" [4,8)` | none |

In A23 the old `WORD foo` was decided by the end of the text. The edit is at
the end of the text, so the token is affected even though no byte it read
has changed.

### Ten backspaces

The text is `total␣=␣alpha␣+␣beta⏎`. The user's cursor is after `alpha`, at
offset 13. Each backspace deletes the one byte before the cursor.

| Backspace | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `total␣=␣alpha␣+␣beta⏎` | delete `[12,13)` | 8 | `WORD alpha [8,13)` | `WORD alph [8,12)` | new 12 = old 13, `WS` |
| 2 | `total␣=␣alph␣+␣beta⏎` | delete `[11,12)` | 8 | `WORD alph [8,12)` | `WORD alp [8,11)` | new 11 = old 12, `WS` |
| 3 | `total␣=␣alp␣+␣beta⏎` | delete `[10,11)` | 8 | `WORD alp [8,11)` | `WORD al [8,10)` | new 10 = old 11, `WS` |
| 4 | `total␣=␣al␣+␣beta⏎` | delete `[9,10)` | 8 | `WORD al [8,10)` | `WORD a [8,9)` | new 9 = old 10, `WS` |
| 5 | `total␣=␣a␣+␣beta⏎` | delete `[8,9)` | 7 | `WS ␣ [7,8)`, `WORD a [8,9)`, `WS ␣ [9,10)` | `WS ␣␣ [7,9)` | new 9 = old 10, `OP` |
| 6 | `total␣=␣␣+␣beta⏎` | delete `[7,8)` | 6 | `WS ␣␣ [7,9)` | `WS ␣ [7,8)` | new 8 = old 9, `OP` |
| 7 | `total␣=␣+␣beta⏎` | delete `[6,7)` | 5 | `WS ␣ [5,6)`, `OP = [6,7)`, `WS ␣ [7,8)` | `WS ␣␣ [5,7)` | new 7 = old 8, `OP` |
| 8 | `total␣␣+␣beta⏎` | delete `[5,6)` | 0 | `WS ␣␣ [5,7)` | `WS ␣ [5,6)` | new 6 = old 7, `OP` |
| 9 | `total␣+␣beta⏎` | delete `[4,5)` | 0 | `WORD total [0,5)` | `WORD tota [0,4)` | new 4 = old 5, `WS` |
| 10 | `tota␣+␣beta⏎` | delete `[3,4)` | 0 | `WORD tota [0,4)` | `WORD tot [0,3)` | new 3 = old 4, `WS` |

The same ten bytes deleted as one selection give one change with the same
final tokens:

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| one deletion | `total␣=␣alpha␣+␣beta⏎` | delete `[3,13)` | 0 | `WORD total [0,5)`, `WS ␣ [5,6)`, `OP = [6,7)`, `WS ␣ [7,8)`, `WORD alpha [8,13)` | `WORD tot [0,3)` | new 3 = old 13, `WS` |

### Rule set B: modes

Two modes over one text. The mode state is written `[code]` or `[code str]`,
with the top of the stack last. Each token below is followed by the mode
state at its start.

| Mode | Type | Matches | After the token |
| --- | --- | --- | --- |
| `code` | `WORD` | one or more ASCII letters | stay |
| `code` | `WS` | one or more spaces | stay |
| `code` | `NL` | one newline | stay |
| `code` | `QUOTE` | `"` | push `str` |
| `str` | `TEXT` | one or more characters that are not `"` | stay |
| `str` | `QUOTE` | `"` | pop |

| Case | Before | Edit | Restart | Removed | Inserted | Sync |
| --- | --- | --- | --- | --- | --- | --- |
| B1 delete an opening quote | `say␣"hi"␣now␣"x"␣end⏎` | delete `[4,5)` | 3 | `QUOTE " [4,5) [code]`, `TEXT hi [5,7) [code str]`, `QUOTE " [7,8) [code str]`, `WS ␣ [8,9) [code]`, `WORD now [9,12) [code]`, `WS ␣ [12,13) [code]`, `QUOTE " [13,14) [code]`, `TEXT x [14,15) [code str]`, `QUOTE " [15,16) [code str]`, `WS ␣ [16,17) [code]`, `WORD end [17,20) [code]`, `NL ⏎ [20,21) [code]` | `WORD hi [4,6) [code]`, `QUOTE " [6,7) [code]`, `TEXT ␣now␣ [7,12) [code str]`, `QUOTE " [12,13) [code str]`, `WORD x [13,14) [code]`, `QUOTE " [14,15) [code]`, `TEXT ␣end⏎ [15,20) [code str]` | none |
| B2 type an opening quote | `say␣hi␣now⏎` | insert `"` at 4 | 3 | `WORD hi [4,6) [code]`, `WS ␣ [6,7) [code]`, `WORD now [7,10) [code]`, `NL ⏎ [10,11) [code]` | `QUOTE " [4,5) [code]`, `TEXT hi␣now⏎ [5,12) [code str]` | none |
| B3 edit inside a string | `say␣"hi"␣now⏎` | insert `p` at 6 | 5 | `TEXT hi [5,7) [code str]` | `TEXT hpi [5,8) [code str]` | new 8 = old 7, `QUOTE` |
| B4 close a string and open another | `say␣"hi␣now"␣end⏎` | insert `"␣"` at 7 | 5 | `TEXT hi␣now [5,11) [code str]` | `TEXT hi [5,7) [code str]`, `QUOTE " [7,8) [code str]`, `WS ␣ [8,9) [code]`, `QUOTE " [9,10) [code]`, `TEXT ␣now [10,14) [code str]` | new 14 = old 11, `QUOTE` |

In B1 the new `QUOTE` at `[6,7)` and the old `QUOTE` at `[7,8)` are at the
same place of the unchanged text (`6 = 7 - 1`), and they have the same type.
The mode state differs: `[code]` after the edit, `[code str]` before. So this
is not a sync point, and the re-lex is right to continue: the next token is
`TEXT ␣now␣`, where the old one was `WS ␣`. Every later quote is flipped in
the same way, and the re-lex runs to the end of the text.

In B4 the sync point is the closing quote: after the edit the scan is again
at a `"` in the state `[code str]`, as it was before.

### What the cases show

- Most edits change one to five tokens and reach a sync point one token
  after the edit.
- The restart can be before the edit: by two tokens in A12, and by five
  tokens in A31. A rule that reads far ahead makes a large reach.
- A change can have no removed token (A11, A14) or no inserted token (A6,
  A34).
- The sync point can be the restart point itself only when the edit inserts
  nothing and the flags there are equal. In A7 the restart point is at the
  same place as an old token, but the BOF flag differs, so one token is
  lexed again.
- The same place, the same type and the same mode state are not enough for
  a sync point when a flag changed: A7, A26, A27 and A29 sync one token
  later. The token that is lexed again has the same type, the same moved
  range and the same bytes, so the change leaves it out.
- A change can be empty: in A24 a word replaced with itself changes no token.
- An edit that changes the mode state can have no sync point before the end
  of the text (B1, B2). The work of such an edit is the rest of the text; a
  bound on that work is deferred
  ([question 27](open-questions.md#question-27)).
- The same place and the same type are not enough for a sync point. The mode
  state must be equal too (B1).
- Ten backspaces are ten small changes. No backspace lexes the whole text.

## What the examples must be able to do

Two example programs set the acceptance of the pieces above: a REPL and a
syntax highlighter. The programs belong to the separate
[bytescan-examples](https://github.com/xiam/bytescan-examples) repository,
not to this module. The statements below say what the pieces let such a
program do, and
the tests of this module back each one. The guide
[Lexing text that changes](../guides/text-that-changes.md) shows a small
highlighter and a small prompt loop.

### The REPL

**Current contract.** A REPL built on these pieces can do the following
without lexing code of its own:

1. **Follow the line as the user types it.** Each key is one edit and one
   change, as in [Ten backspaces](#ten-backspaces)
   (`TestTenBackspacesInARow`).
2. **Decide between "run" and "continue" when the user presses Enter.** If
   a string or a comment is not closed, the REPL prints a continuation prompt
   and waits for the next line. A REPL that reads a stream uses the
   [need more input](#need-more-input) piece. A REPL that holds its line in
   the lexer with state uses the queries `ModeAtEnd` and `Unfinished`
   ([The queries](#the-queries); `TestQueriesREPL`).
3. **Color the line**, in the same way as the highlighter below.

A REPL also asks a question that "need more input" does not answer. In rule
set B the line `say␣"` gives three complete tokens (`WORD`, `WS`, `QUOTE`)
and no unfinished token, but the mode state at the end is `[code str]`: the
string is open. The REPL reads the mode state at the end of the text with
`ModeAtEnd` (owner decision, 2026-11-18,
[question 28](design-principles.md#question-28)).

Whether a line is complete as a *statement* (balanced brackets, a finished
expression) is a parsing question. It belongs to the REPL, not to bytescan.

### The highlighter

**Current contract.** A highlighter built on these pieces can do the
following without lexing code of its own:

1. **Paint the whole text once.** Loading the text is one edit of an empty
   lexer, and its change holds every token ([A35](#inserting);
   `TestEditCasesRuleSetA`).
2. **Repaint only what changed.** For each edit it makes one request and
   receives one change. It removes the colors of the removed tokens, paints
   the inserted tokens, and moves everything after by `d` bytes. It reads no
   token outside the change (the consumer of `TestGeneratedEdits` and of
   `TestModesChangeIsEnough`).
3. **Handle the edits the owner named** on 2026-11-18: ten backspaces
   ([Ten backspaces](#ten-backspaces)), a selection deleted from the middle
   (A6), a chunk deleted from the beginning (A7), and select-all then paste
   (A8) (`TestEditCasesRuleSetA`, `TestTenBackspacesInARow`).
4. **Stay simple.** It never handles a rule, a mode, a reach or a restart
   point. It knows token types and byte positions (`Token`, `Change`).

A highlighter paints only the visible part of a large text with `Tokens` and
`Text` ([The queries](#the-queries); `ExampleLexer_Tokens`,
`ExampleLexer_Text`), so it keeps no second copy of the text. Staying
responsive after an edit that has no early sync point is deferred
([question 27](open-questions.md#question-27)).

## Open design questions

Every open question below is in [open questions](open-questions.md) with the
constraint that an answer must satisfy. No answer is chosen here.

| Question | About |
| --- | --- |
| [22](open-questions.md#question-22) | State that is not a stack of mode names (deferred by the owner) |
| [27](open-questions.md#question-27) | A bound on the work of one edit (deferred by the owner) |

The owner answered these questions on 2026-11-18. They are in the
[decision log](design-principles.md#closed-design-questions). Each answer is a
Current contract in the section of its piece above:

- questions 16, 20 and 21;
- question 19, for the position and the flags, and for a restart in a given
  mode state;
- questions 18, 23, 24, 26, 29 and 30, and the part of question 17 about the
  end of input, for a rule set with one mode and for a set of several modes;
- questions 25 and 28, and the part of question 17 about the queries.

| Question | About | Answer |
| --- | --- | --- |
| [16](design-principles.md#question-16) | Which tokens are complete under "need more input", and how the caller declares the true end of input | [A sentinel from the source](#need-more-input) |
| [18](design-principles.md#question-18) | The BOF and BOL flags at a restart and at a sync point | [The flags in the sync test](#why-the-sync-test-is-exact) |
| [19](design-principles.md#question-19) | How a scan is started at a position and in a state | [Positions from the restart point, and two start flags](#starting-a-scan-in-the-middle) |
| [20](design-principles.md#question-20) | The form and the cost of the lookahead record | [A second method](#the-lookahead-record) |
| [21](design-principles.md#question-21) | The mode stack and the `modes` package | [Actions on the rules](#the-mode-state) |
| [30](design-principles.md#question-30) | Packaging, the token store and the build order | [An optional package](#scope-and-layers), [balanced trees](#the-lexer-with-state) |
| [17](design-principles.md#question-17) | What the end of the held text means to the rules | [Always the end of input](#the-end-of-the-held-text) |
| [23](design-principles.md#question-23) | The edit request, the change, the constructor and the names | [The constructor](#the-constructor), [the edit request](#the-edit-request), [a complete value](#the-change-message) |
| [24](design-principles.md#question-24) | Which tokens count as unchanged in a change | [Same type, same range, same bytes](#what-the-change-contains) |
| [25](design-principles.md#question-25) | Lines, columns and rune offsets after an edit | [Computed on request](#lines-columns-and-rune-offsets) |
| [26](design-principles.md#question-26) | The unit of an edit, and an edit inside a multi-byte character | [Bytes, at character boundaries](#the-edit-request), [broken bytes in step 1](#the-four-steps) |
| [28](design-principles.md#question-28) | The queries the lexer with state answers | [Five queries](#the-queries) |
| [29](design-principles.md#question-29) | Errors while re-lexing | [No option that can fail a re-lex](#errors) |
