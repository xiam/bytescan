# Cursor contract

This document specifies the positioned cursor, the package
[`github.com/xiam/bytescan/cursor`](../../cursor): positions, checkpoints and
replay, retention and its bound, checkpoint reuse, rune sources, byte mode, and
the limits of the basic reader. It uses the
[status convention](core-contract.md#status-convention) of the core contract.
The how-to page for the same topics is [docs/cursor.md](../cursor.md); the
scanner's use of the cursor is in
[resources-and-streams.md](resources-and-streams.md). Tests cited without a
path are in `cursor/`.

## Contents

- [Independence](#independence)
- [Positions](#positions)
- [Checkpoints and replay](#checkpoints-and-replay)
- [Retention and invalidation](#retention-and-invalidation)
- [Bounding retained input](#bounding-retained-input)
- [Reusing checkpoints](#reusing-checkpoints)
- [Rune sources](#rune-sources)
- [Byte mode](#byte-mode)
- [Limits of the basic reader](#limits-of-the-basic-reader)

## Independence

**Current contract.** The non-test files of `cursor` import only the standard
library and `cursor/position`. A program can use the cursor without the
scanner (`TestImportIndependence`).

## Positions

**Current contract.** A `Pos` is an immutable position: a 0-based byte offset,
a 0-based rune offset, a 1-based line and a 0-based column that counts runes
since the last newline, so a multi-byte rune moves the column by one and the
byte offset by its size (`Pos`; `TestCursorPositions`,
`TestCursorTracksNewlinesAndRunes`, `Example_text` in
`example_readme_test.go`). `Reader.Pos()` returns a `position.Position` whose
`Offset` counts bytes and whose `Column` counts runes (`TestReader`).

**Current contract — a start in the middle of a text** (owner decision,
2026-11-18, [question 19](design-principles.md#question-19)).
`func (r *Reader) ResetStart(src io.Reader, s Start) error` resets a reader
onto a new source. The reader then reports positions counted from that
start: byte offset 0, rune offset 0, line 1, column 0
(`TestResetStartReportsPositionsFromTheRestart`). `type Start struct{
InText, LineStart bool }` carries the two facts that the scanner turns into
the BOF and BOL flags of the first symbol. `func (r *Reader) Start() Start`
returns the stored `Start` (owner decision, 2026-11-18).
A reset drops the buffered input, the sticky read error and any partial UTF-8
sequence of the old source, keeps the options and byte mode, and releases
every active checkpoint (`TestResetStartDropsTheOldSource`,
`TestResetStartReportsPositionsFromTheRestart`,
`TestResetStartReleasesCheckpoints`, `TestResetStartLeavesNoRetentionLimit`).
A multi-byte character that the old source cut with `ErrMoreInput` is dropped
too, and no "more input" condition survives the reset, because `ErrMoreInput`
is not sticky (`TestResetStartDropsAHeldCutRune`). A reader built with
`NewRuneReader` reads the new source as UTF-8 bytes
(`TestResetStartDropsTheOldSource`, subtest `rune source`). An empty source
reads `io.EOF` at offset 0 (`TestResetStartEmptySource`).
`ResetStart` fails only on a nil source, and then it changes nothing
(`TestResetStartRefusesANilSource`).

**Current contract — a reader that is never reset.** A reader that is never
reset starts at offset 0, line 1, column 0, as the first statement of this
section says, and its `Start` is `Start{LineStart: true}`,
the start of a whole text (`TestResetStartStoresTheStart`). What the scanner
does with the flags is in
[Starting a scan in the middle](resumable-lexing.md#starting-a-scan-in-the-middle).

**Current contract.** A `Span` is an immutable half-open range built with
`NewSpan`. A `LocatedRune`, returned by `ReadLocatedRune`, is a rune with the
position it was read from. `PeekRune` reports the rune at the cursor without
consuming it (`TestPeekRuneDoesNotConsume`).

**Current contract.** `Cursor()` and `Pos()` report the logical position, never
the physical read-ahead: bytes pulled into the buffer for look-ahead are
invisible until they are read. `SpanText` and `Context` read the retained input
without moving the cursor (`TestNonConsumingCallsDoNotMoveTheCursor`,
`TestSpanTextAndContextAreNonMutating`). `SpanText` refuses a reversed span
(`TestSpanTextRejectsReversedSpan`).

## Checkpoints and replay

**Current contract.** `Checkpoint()` marks the logical position. `Reset()`
returns the cursor to the mark and replays the input exactly, across
multi-byte runes, newlines and the end of input (`TestCheckpointReplayIsExact`,
`TestCheckpointResetMidRuneMark`, `TestCheckpointResetWithSplitReads`). A
checkpoint may be reset any number of times until it is committed or
released.

**Current contract.** `Commit()` accepts the current position and drops the
retained input. It refuses with `ErrCheckpointRewound` while the logical cursor
is behind the mark, because the replayed range is not decided yet. `Release()`
abandons the checkpoint wherever the cursor is, with no such precondition.
After `Commit` or `Release`, `Reset`, `Commit` and `Release` return
`ErrCheckpointReleased`, and `Active` reports false
(`TestCheckpointCommitAndRelease`). `Pos` still returns the marked position
(`Checkpoint.Pos`; **no test yet** for `Pos` after release).

**Current contract.** Checkpoints nest. The reader keeps the input from the
oldest active checkpoint onward; releasing an inner checkpoint does not release
the input an outer one still needs (`TestCheckpointNesting`).

**Current contract.** The reader is safe for concurrent use
(`TestConcurrentCursorUse`).

## Retention and invalidation

**Current contract.** While at least one checkpoint is active, the reader
retains every byte from the oldest active checkpoint onward, so a reset can
always replay, also after the buffer is trimmed
(`TestTrimKeepsCheckpointResetExact`). The seek boundary is the physical
buffer, not the checkpoint: `Seek` moves only within buffered data
(`TestTrimKeepsUnreadAndSeekExact`). An active checkpoint keeps the bytes it
needs from being discarded, but bytes before the mark may also still be
buffered, so a `Seek` can land before the mark; `Commit` then refuses with
`ErrCheckpointRewound` (`TestCheckpointCommitAndRelease`).

**Current contract.** Retention ends when the last relevant checkpoint is
committed or released. Until then, `SpanText` and `Context` can read spans of
the retained region. Afterwards the same call returns `ErrPositionOutOfBuffer`,
even if the bytes still sit in the buffer. The spans that can be read back are
the ones built from located runes read while a checkpoint was open
(`TestSpanInvalidatedWhenRetentionEnds`, `TestSpanTextOutsideRetainedInput`,
`TestContextStopsAtRetentionFloor`).

## Bounding retained input

**Current contract.** `WithMaxRetained(n)` caps the retained region: the bytes
from the oldest active checkpoint to the logical cursor, which is what a
`Reset` replays. Zero, the default, means no bound (`WithMaxRetained`;
`TestRetentionLimitIsExplicit`).

**Current contract.** The limit applies only while a checkpoint is active. A
reader with no checkpoint retains nothing extra and pays no cost
(`TestRetentionLimitWithoutCheckpoint`).

**Current contract.** The limit is enforced where the cursor would advance. A
read that would exceed it fails with `ErrRetentionExceeded`, moves the cursor
by nothing and consumes nothing (`TestRetentionBudgetEnforcedOnConsumption`).
The budget is judged against the bytes that would really advance the cursor,
not the size of the caller's buffer: a large `Read` still returns the bytes
that are available, and only a request that would advance past the budget is
refused whole (`TestBulkReadBudgetUsesActualAvailability`,
`TestBulkReadBudgetIsAtomic`). A rune that does not fit the remaining budget is
refused whole, never half-decoded (`TestRetentionBudgetRejectsRuneLargerThanBudget`,
`TestRetentionLimitAllowsRuneThatFits`).

**Current contract.** Read-ahead in front of the cursor is bounded by the
buffer capacity (`WithCapacity`), not by this limit, and it stays bounded while
the cursor cannot advance. Without a limit, a checkpoint held across a long
read-ahead grows the buffer as needed, which is what makes a long token
recoverable (`TestLongReadAheadGrowsRetainedBuffer`,
`TestSeekForwardAcrossGrownBuffer`).

## Reusing checkpoints

**Current contract.** `Checkpoint` allocates a new value on every call.
`Remark` re-arms a released checkpoint value at the current logical position,
so a caller that owns a fixed set of slots makes a new mark without an
allocation. The allocation cost is bounded by the number of slots, not by the
number of marks (`TestRemarkReusesTheSameValue`,
`TestRemarkRecordsTheCurrentCursor`, `TestRemarkRearmIsAllocationFree`,
`TestRemarkRepeatedRearmSteadyState`).

**Current contract.** Re-arming is opt-in and additive: `Checkpoint` and the
constructors are unchanged, and a caller that never calls `Remark` sees no
difference.

**Current contract.** Only a released value may be re-armed. A checkpoint that
is active, on this reader or another, is refused with `ErrCheckpointActive` and
left untouched. A nil value is refused with `ErrCheckpointReleased`
(`TestRemarkRefusesActiveOnThisReader`,
`TestRemarkRefusesAValueActiveOnAnotherReader`, `TestRemarkRefusesNil`).

**Current contract.** A released value carries no reader reference, so it may
be re-armed on any reader. The reader pointer is claimed by compare-and-swap,
so one value is active on at most one reader at a time
(`TestRemarkAdoptsAValueReleasedFromAnotherReader`,
`TestConcurrentRemarkAdoption`).

**Current contract.** After a successful `Remark` the value behaves like a
fresh mark: `Pos` reports the marked position, `Reset` replays the input read
since the mark, and `Commit` or `Release` ends the retention. Retention floors
at the oldest active checkpoint as usual, and `WithMaxRetained` applies the
same way (`TestRemarkAfterCommit`, `TestRemarkNestsWithFreshCheckpoints`,
`TestRemarkRetentionFloorsAtTheOldestActiveMark`,
`TestRemarkRetentionLimitAppliesLikeFreshCheckpoints`,
`TestRemarkAtStreamStartAndEOF`, `TestRemarkReplaysMalformedUTF8`).

**Current contract.** A reusable checkpoint is one shared mutable slot.
Re-arming changes what every alias of the value refers to: a reference kept
from before `Release` regains `Pos`, `Reset`, `Commit` and `Release` authority
over the new mark. The reader cannot tell generations of a slot apart and
cannot detect this misuse. The caller keeps every reference to a released slot
under one owner (`TestRemarkAliasRegainsAuthority`).

## Rune sources

**Current contract.** `NewRuneReader` wraps an `io.RuneReader` in the same
cursor. It encodes the runes back into the buffer, so byte offsets stay
meaningful, and it decodes nothing, because the caller's rune reader already
did. `New` and `NewReader` read an `io.Reader` as a byte stream with one UTF-8
decoder (`TestNewRuneReaderTracksPositions`, `TestRuneReaderCheckpointReplay`,
`TestRuneReaderKeepsRuneAtBufferBoundary`).

**Current contract.** A rune read does not wait for input the rune does not
need, so a live source (one that has delivered input and then blocks without an
error or `io.EOF`) yields every rune it has delivered. In text mode over an
`io.Reader`, `ReadRune`, `ReadLocatedRune` and `PeekRune` return the next rune
as soon as the buffered bytes hold a complete sequence in the sense of
`utf8.FullRune`, which includes an invalid byte that decodes to
`utf8.RuneError` of size 1. A proper prefix of a multi-byte sequence reads more,
and at `io.EOF` it decodes as before. Over a rune source, a rune read calls the
source's `ReadRune` only for the rune it returns and does not wait for the
buffer to fill. A scanner over a live source therefore returns every token its rules can
decide from the input delivered so far (`TestLiveReaderReturnsArrivedRunes`,
`TestLiveReaderReturnsCompleteSequences`,
`TestLiveReaderWaitsForIncompleteSequence`, `TestLiveReaderPeeksArrivedRune`,
`TestLiveRuneSourceReturnsArrivedRunes`;
`TestScannerReturnsTokensFromLiveSource` in `tests/`).

**Current contract.** An error from the underlying source is sticky: a replay
from a checkpoint reproduces the same error, not whatever the source would
report on a second pass (`TestReaderErrorReplaysDeterministically`,
`TestRuneReaderErrorReplays`).

**Current contract — "no input now"** (owner decision, 2026-11-18,
[question 16](design-principles.md#question-16)). The package
defines a sentinel, `ErrMoreInput`, that a source returns when it has no
input now. A source that waits inside `Read` instead still blocks the read,
as the contract above says. Unlike the other errors of a source,
`ErrMoreInput` is not sticky: the next read asks the source again
(`TestMoreInputNotSticky`). A read that gets it returns it and does not move
the cursor. A proper prefix of a multi-byte sequence stays held when the
source returns it, and is not decoded as at `io.EOF`
(`TestMoreInputCutRuneStaysHeld`, `TestMoreInputCheckpointReplay`). Bytes
that arrive together with the signal are read first: a rune read returns the
rune they complete (`TestMoreInputWithData`, `TestMoreInputByteMode`), and a
bulk `Read` returns them with no error and returns the signal on a later call
when no byte is left. The bulk `Read` does not settle the carried bytes of a
cut character on the signal (`TestMoreInputBulkRead`). A forward `Seek`
uses the bytes that arrive with the signal: it fails with the signal only
when they do not reach the target, and then it does not move the cursor
(`TestMoreInputSeek`). A rune source returns
the signal from `ReadRune` (`TestMoreInputRuneSource`). The reader matches
the signal with `errors.Is`. See
[Need more input](resumable-lexing.md#need-more-input).

## Byte mode

**Current contract.** `NewByteReader` builds the same cursor for binary input.
It takes the options of `NewReader` and decodes nothing: each byte is one rune,
U+0000 to U+00FF, of size 1 (`TestByteReaderEveryByteValue`,
`TestByteReaderOptionsAndEmptyInput`).

**Current contract.** In byte mode, `ByteOffset` and `RuneOffset` are both the
file offset. The byte 0x0A ends a line, and `Column` counts the bytes since the
last 0x0A, so a protocol that mixes text lines and binary fields keeps its line
positions (`TestByteReaderLinesEndOnlyAtLineFeed`). `Read`, `SpanText` and
`Context` return the raw input bytes (`TestByteReaderContextReturnsRawBytes`,
`TestByteReaderReadMixedWithReadRune`, `TestByteReaderLargeRead`).

**Current contract.** In byte mode, `Seek`, `UnreadRune`, `Checkpoint`,
`Reset`, `Remark` and `WithMaxRetained` work on file offsets, and a checkpoint
over n bytes retains n bytes (`TestByteReaderCheckpointReset`,
`TestByteReaderRemark`, `TestByteReaderRetentionCountsFileBytes`,
`TestByteReaderRandomOperations`).

**Current contract.** A byte read returns as soon as a byte is available; it
does not wait for more input (`TestByteReaderDoesNotWaitForMoreBytes`).

## Limits of the basic reader

**Current contract.** A forward `Seek` to a position that the buffer does not
hold yet reads the bytes in between from the source, so `Seek` does not move
only within the buffer. It counts those bytes into the position: the position
after the seek equals the position after reading the same bytes
(`TestSeekForwardReadsFromSource`; `TestReader`, subtest `seeker`). Unlike
`os.File.Seek`, `Seek` cannot reach an arbitrary position of the underlying
stream, and it never moves the underlying `io.Reader` (`Reader.Seek`).

**Current contract.** A `Seek` to a target that the buffer cannot hold fails.
`Seek` first computes the absolute stream offset of the target, for every
`whence`. The error depends only on that target
(`TestSeekWhenceTargets`; `Reader.Seek`):

- A target before stream offset 0 fails with the error "cursor: negative
  position", for every `whence`. This error is not `ErrSeekOutOfBuffer`
  (`TestSeekNegativePositionDoesNotMove`; `TestReaderEdgeCases`, subtest
  `Seek_Errors`).
- A target at or after offset 0 but before the start of the buffer fails with
  `ErrSeekOutOfBuffer`, for every `whence`. So `Seek(0, io.SeekStart)` fails
  once the start of the stream has left the buffer
  (`TestSeekOutOfBufferDoesNotMove`).
- A forward target that the buffer cannot hold fails with
  `ErrSeekOutOfBuffer`, and so does a target after the end of the input
  (`TestSeekOutOfBufferDoesNotMove`, `TestReaderEdgeCases`).
- While a checkpoint holds input, a forward target past the retention limit
  fails with `ErrRetentionExceeded`
  (`TestSeekFailedForwardKeepsSplitRunePending`).
- A forward target that needs bytes from a source that fails has two
  outcomes. With no unread byte in the buffer, `Seek` returns the source
  error, wrapped. With unread bytes in the buffer, the reader serves those
  bytes first: `Seek` fails with `ErrSeekOutOfBuffer`, and a later read
  returns the source error
  (`TestSeekFailedOnSourceErrorKeepsSplitRunePending`; `Reader.fillAtLeast`).

**Current contract.** A failed `Seek` does not move the position, for every
`whence` and every error. The byte offset, the rune offset, the line and the
column stay the same (`TestSeekWhenceTargets`,
`TestSeekOutOfBufferDoesNotMove`, `TestSeekNegativePositionDoesNotMove`,
`TestSeekFailedOnSourceErrorKeepsSplitRunePending`).
This also holds after a `Read` that stopped inside a multi-byte UTF-8
sequence. The bytes already read of that sequence stay pending, and the
`Read` of its last bytes counts it as one rune
(`TestSeekFailedForwardKeepsSplitRunePending`). After a failed `Seek`, the
next successful `Read`, `ReadRune` or `Seek` gives the same position as it
gives without the failed `Seek` (`TestSeekFailedThenNextOperation`). To reach
a forward target, a failed `Seek` can still have read ahead from the source
into the buffer. That read-ahead does not change the position.

**Current contract.** A `Seek` that moves settles a pending partial sequence
first: each byte already read of that sequence counts as one rune. A `Seek`
to the current position does nothing, and the sequence stays pending
(`TestSeekFailedThenNextOperation`, `TestNonConsumingCallsDoNotMoveTheCursor`).

**Current contract.** `UnreadRune` undoes only the most recent `ReadRune`. A
second `UnreadRune` without a read in between fails with
`bufio.ErrInvalidUnreadRune`; `Seek` is the tool for moving further back
(`TestReader`, `TestReaderEdgeCases`, `TestPeekEndsUnreadAuthority`).

**Current contract.** The text constructors (`New`, `NewReader`,
`NewRuneReader`) assume UTF-8. They read any byte stream, but their line and
column counts are exact only for valid UTF-8; `NewByteReader` counts bytes
instead (`TestReaderEdgeCases`, `TestByteReaderEveryByteValue`).
