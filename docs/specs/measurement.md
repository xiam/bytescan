# Measurement

Part of the [core contract](core-contract.md). Statuses follow the
[status convention](core-contract.md#status-convention).

This document is the contract for measuring how fast bytescan reads and lexes:
what is measured, which build carries the instruments, how a number is
reported, and which cases every number is stated for. The measurement tools
are internal to this module. They are not API.

Each section states its status. Every section is a **Current contract**: the
metering reader and the metering source
([The metering reader and the metering source](#the-metering-reader-and-the-metering-source)),
the build tag, the engine counters, the way the harness reads them, and the
harness in `internal/bench` are built at `master`. The owner decided each
choice on 2026-11-19, except five that the owner decided on 2026-11-20:
the constructors and the fields of `meter.Counts`, the names and the formats
of the harness, the resolution floor of the spread, the names and the
format of the statelex report, and the outside references. The
[decision log](design-principles.md#owner-decisions) records them. The owner
approved the build tag, the package names, the type and function names, the
counter names, the report format and the reference cases that this document
gives. Every other name or data structure is not approved; a code sketch
without that mark is an illustration.

This document states no measured speed. A measured rate, a measured cost and
a measured result live only in the reports under `internal-docs/benchmarks/`,
which the release does not export. The numbers in this document are
parameters of the method: run counts, a spread limit and the sizes of the
inputs.

## Contents

- [Why the method keeps the measurement honest](#why-the-method-keeps-the-measurement-honest)
- [The build tag](#the-build-tag)
- [The packages and the names](#the-packages-and-the-names)
- [The metering reader and the metering source](#the-metering-reader-and-the-metering-source)
- [The engine counters](#the-engine-counters)
  - [Derived quantities](#derived-quantities)
  - [How the harness reads the counters](#how-the-harness-reads-the-counters)
- [The method](#the-method)
  - [The baseline machine](#the-baseline-machine)
  - [Lanes](#lanes)
  - [Runs, the estimator and the spread](#runs-the-estimator-and-the-spread)
  - [The rate and the costs](#the-rate-and-the-costs)
- [The reference cases](#the-reference-cases)
- [The outside references](#the-outside-references)
- [The report format](#the-report-format)
  - [A filled example](#a-filled-example)
- [The statelex report](#the-statelex-report)

## Why the method keeps the measurement honest

**Current contract** (owner decision, 2026-11-19; each item is cited in its
section below). Measuring can add
processing time, and a measurement that changes what it measures is not a
measurement. The method keeps the instruments from changing the engine in
three ways:

1. **A build tag.** The engine counters exist only in a build with the tag
   `bench` ([The build tag](#the-build-tag)). The normal build has no counter,
   no increment and no check on the hot path. Every change that adds an
   instrument proves that the normal build did not change.
2. **Counters, not clocks.** Inside the engine, the instruments count events.
   They never read the clock. The harness owns the clock: it measures one
   elapsed interval for one complete run, from before the first `Next` to after
   the last one returns. A count costs one add on a path that already holds a
   lock. A clock read inside the engine would add its own cost to every event
   that it timed.
3. **The measured cost of the instruments.** Every case runs in three lanes
   that differ only in the instruments ([Lanes](#lanes)). Every report states
   two costs, each with its uncertainty: the cost of the wrappers and the cost
   of the counters. The headline rate comes from the lane without any
   instrument.

## The build tag

**Current contract** (owner decision, 2026-11-19). The build tag that
enables the engine counters is `bench`.

- In each package that counts, `cursor` and the root package `bytescan`, one
  file holds the counting code under `//go:build bench`, and one file holds
  empty substitutes under `//go:build !bench`. The two files are named
  `counters_on.go` and `counters_off.go`. The repository already uses this
  pattern for the race detector: `internal/lextest` (`race_on.go`,
  `race_off.go`) and `streamtest` (`budget_race.go`, `budget_norace.go`)
  (Current contract; source symbols `lextest.RaceDetector` and
  `streamtest.defaultBudget`, each defined once in each file of its pair;
  no test yet).
- A call site in the hot path calls a method of a counter field. In the normal
  build the method has an empty body on a type of size zero, and the compiler
  inlines the call to nothing.
- The counter field is never the last field of its struct. Go pads a struct
  whose last field has size zero, so that field would make `Scanner` or
  `Reader` larger in the normal build.
- The proof that the normal build is unchanged: a test lists the Go files of
  each package in the normal build (`go list -f '{{.GoFiles}}'`) and fails if a
  file of the tagged build is among them; the inlining decisions of `bytescan`
  and `cursor` (`go build -gcflags=-m`) are the same as before the change; the
  allocation ceilings of `tests/alloc_test.go` do not change; and the
  benchmarks run before and after the change, interleaved, show no difference
  beyond their spread. The tests are `TestNormalBuildHasNoCounters`
  (`internal/counters`), `TestCountersAddNothingToReader` (`cursor`) and
  `TestCountersAddNothingToScanner` (the root package). The inlining
  decisions and the benchmarks are compared in the merge request of the
  change.

A build tag applies to every package of a build, so a program that imports
bytescan and builds with its own `-tags bench` also builds bytescan's counters;
the owner accepted that risk on 2026-11-19.

## The packages and the names

**Current contract** (owner decision, 2026-11-19; source symbols `bench.Case`,
`bench.Lane` and `bench.Report`; test `TestCaseInputs`). The measurement code
lives in three packages under `internal/`, so no other module can import it.

| Package | Built by | Holds |
| --- | --- | --- |
| `internal/meter` | the metering wrappers | `meter.Reader`, `meter.Source`, `meter.Counts`, `meter.Throughput`, and the function `func Rates(c Counts, elapsed time.Duration) (Throughput, error)` |
| `internal/counters` | the engine counters | `counters.Cursor` and `counters.Scanner` (the snapshot types), `counters.OfCursor(any) (Cursor, bool)` and `counters.OfScanner(any) (Scanner, bool)` |
| `internal/bench` | the harness | `bench.Case`, `bench.Lane` with the values `Control`, `Wrapped` and `Instrumented`, `bench.Report`, the copied rule sets, the corpus under `internal/bench/testdata/`, and the runner `internal/bench/cmd/benchreport` |

The counter types cannot live in `internal/meter`. The tagged files of
`bytescan` import the package of the counter types, and `internal/meter`
imports `bytescan` to wrap a `bytescan.Source`. One package for both would be
an import cycle.

The result type of `meter.Rates` is `meter.Throughput` (owner decision,
2026-11-19). It has two fields, `BytesPerSecond` and `TokensPerSecond`, both
`float64`. The type is not named `Rates`, because a Go package cannot declare a
function and a type with the same name.

**Current contract** (owner decision, 2026-11-20, in the words "Approve as
is"). The constructors of the wrappers and the fields of `meter.Counts` are
(source symbols `meter.NewReader`, `meter.NewSource` and `meter.Counts`):

- `func NewReader(r io.Reader, c *Counts) *Reader`;
- `func NewSource(src bytescan.Source, c *Counts) *Source`;
- the fields of `meter.Counts`: `Bytes`, `Reads`, `Tokens` and `TokenBytes`,
  each a `uint64`.

The two wrappers share one `*Counts` that the caller owns. A snapshot is a copy
of that `Counts`.

**Current contract** (owner decision, 2026-11-20, in the words "Approve
all"). The names and the formats of the harness are (source symbols
`bench.Main`, `BenchmarkCase` and `runMain`; tests `TestCaseInputs` and
`TestReportHasEveryFieldOfTheFormat`):

- `func Main() int` in `internal/bench`, the runner that
  `internal/bench/cmd/benchreport` calls;
- the benchmark `BenchmarkCase/<case>`, one sub-benchmark for each case, and
  its test flag `-lane` (`control`, `wrapped` or `instrumented`);
- the flags of the runner: `-kind` (`baseline`, `statelex` or
  `bottlenecks`), `-out`, `-runs` (10 by default, at least 6) and `-cases`;
- the raw files `raw/YYYY-MM-DD-<sha12>/<case>-<lane>.txt`, with
  `superseded/` for the first set of runs of a case that was rerun, and one
  `lane: <lane>` line for each run in a raw file;
- the corpus files `testdata/golang_source.json.gz` and
  `testdata/golang_source.LICENSE`;
- the label "no interval (∞)" for a lane that `benchstat` gives no interval.

The owner also approved, in the same words, that the reference cases build
their scanners without a `bufio` layer and without the per-token guard of
the source scanners
([The reference cases](#the-reference-cases)).

## The metering reader and the metering source

**Current contract** (owner decision, 2026-11-19), except the last item.

- **What they count.** `meter.Reader` wraps an `io.Reader`. It counts the
  bytes that pass through it and the `Read` calls. `meter.Source` wraps a
  `bytescan.Source`. It counts the tokens and the bytes of the tokens that
  leave `Next`. Both pass every result through unchanged: `io.EOF`,
  `bytescan.ErrMoreInput` and every error (tests
  `TestReaderPassesResultsThrough` and `TestSourcePassesResultsThrough` in
  `internal/meter`).
- **What they read.** The caller reads a `meter.Counts` snapshot at a boundary
  it chooses, for example after the last `Next` returned `io.EOF` (test
  `TestRatesBufferedTail`).
- **No clock.** The wrappers hold no clock. `meter.Rates` takes a snapshot and
  an elapsed interval that the caller measured, and returns a
  `meter.Throughput` with bytes per second and tokens per second. An interval
  of zero gives an error, never a division by zero (tests
  `TestRatesDataWithEOF`, `TestRatesBufferedTail`, `TestRatesEmptyInput` and
  `TestRatesPauseAndResume`).
- **What they cost.** One update of the counts for each `Read` call (its
  bytes and the call), not for each byte, and one update for each token (the
  token and its bytes) (source symbols `Reader.Read` and `Source.Next` in
  `internal/meter`). The cost is measured for every reference case in every
  report: the wrapper cost
  ([The rate and the costs](#the-rate-and-the-costs)).
  `BenchmarkWrapperCost` in `internal/meter` also compares the two lanes, on
  inputs of the same shapes.

## The engine counters

**Current contract** (owner decision, 2026-11-19). Under the build tag, the
engine keeps the fourteen counters below (types `counters.Scanner` and
`counters.Cursor`; the tests of `internal/counters/counters_on_test.go`, such
as `TestScannerCounters` and `TestCursorReplays`, check each counter against a
value derived for a small input). Each counter is a plain `uint64`
field. It is incremented under a lock that the method already holds:
`Scanner.Next` holds the scanner's lock for the whole token, and each place
of `cursor.Reader` that a cursor counter names (a read from the source, a
compaction, a growth, a checkpoint reset, a locked public method) runs with
the reader's lock held. So a counter needs no atomic operation and no new
lock. The highest-frequency counters (`Symbols`,
`RuleCalls`, `Locks`) cost about one add per symbol.

Scanner counters (root package: `Scanner.nextToken` and the rules processor):

| Counter | Counts | Where it is incremented |
| --- | --- | --- |
| `Symbols` | symbols passed to the rules processor, the synthetic end-of-input symbol included | just before each symbol is passed to the processor |
| `RuleCalls` | calls of a rule by the VM, pushback calls included | the rule loop of the processor, summed in a local and added once per symbol |
| `Pushbacks` | rule calls caused by `StatePushBack` | the pushback loop of the processor |
| `MaxLive` | the largest number of rules still active after one step | the end of each step of the processor |
| `Tokens`, `LongTokens` | committed tokens; tokens longer than the inline text of a token, whose text gets its own array | the commit of a token |
| `Unknown` | tokens that no rule matched | the branch that emits `TokenTypeUnknown` |
| `Abandons` | calls that commit no token, by cause: the end of input, `ErrMoreInput`, an error | the path that rewinds to the token start |
| `StepBacks`, `StepBackBytes` | commits that seek back over bytes the rules read past the token, and those bytes | the commit, when the rules read past the token and the scanner does not keep those symbols for the next token |

Cursor counters (`cursor.Reader`):

| Counter | Counts | Where it is incremented |
| --- | --- | --- |
| `Fills`, `FillBytes` | reads from the source, and the bytes they returned | each read from the source |
| `Compactions`, `CompactBytes` | shifts of the buffer, and the bytes moved | each compaction |
| `Grows`, `MaxBuffer` | growths of the buffer, and the largest buffer allocation: the initial buffer, then the size after each growth | the construction of the reader, and each growth |
| `Replays`, `ReplayBytes` | rewinds to a checkpoint, and the bytes rewound | each checkpoint reset |
| `Locks` | entries into the public methods of the reader, and of its checkpoints, that take the reader's lock | each such method |
| `FloorScans` | checkpoints visited to find the oldest byte the reader must keep | the loop over the active checkpoints |

`MaxBuffer` is the size of the buffer, not the retained size. The retained
region is always inside the buffer, so `MaxBuffer` is an upper bound on the
retained size. On a case whose buffer never grows, it is the size of the
initial buffer. The exact retained size (`Reader.RetainedBytes`) is not
counted: finding it visits every active checkpoint under its own lock, which
would add that cost to every rune.

Allocations are not engine counters. The harness reports allocations and
bytes allocated per operation (`testing.B.ReportAllocs`) for every lane.

The rules inside a combinator are not visible to the VM: it sees one call for
a whole combinator. The calls inside an opaque rule are measured with
`pprof`, not with a counter.

### Derived quantities

**Current contract** (owner decision, 2026-11-19; source symbols `mapLookups`
and `resetWalks` in `internal/bench`). A quantity that follows
from other counts is computed in the report, not counted:

- **map lookups by token type**: the processor keeps the state of each
  registered rule in a slice in the order of registration
  (`rulesProcessor.states`). It steps the rules for every symbol, and chooses
  the longest match for every decision, by rule number, without a keyed
  lookup. So the keyed lookups are 0 for one scanner. The construction of the
  processor is not counted: it reads each rule from the map of the scanner
  once;
- **reset walks**: before every call after the first, the processor resets
  its state by walking every rule once, without a keyed lookup. The walks are
  `Tokens + Abandons − 1` for one scanner, each over all its rules;
- **rule calls per symbol**: `RuleCalls / Symbols`, pushback calls included,
  so it can exceed the number of rules;
- **active rules per step**: `(RuleCalls − Pushbacks) / Symbols`: each rule
  that is active at the start of a step is called once with the symbol, and
  every other call is a pushback call;
- **symbols per token**: `Symbols / Tokens`;
- **bytes per token**: from the token bytes of `meter.Source` in the wrapped
  lane;
- **re-read share**: `StepBackBytes` divided by the bytes of the input;
- **locks per symbol**: `Locks / Symbols`.

### How the harness reads the counters

**Current contract** (owner decision, 2026-11-19; `TestNoCountersInTheNormalBuild`,
`TestOfWrongValues`, `TestCoreDoesNotImportMeasurement`). The snapshot types
`counters.Cursor` and `counters.Scanner` exist in `internal/counters` in both
builds. In the normal build, `bytescan` and `cursor` import nothing from
`internal/counters`, and `OfCursor` and `OfScanner` return false. In the build
with the tag, the tagged file of `cursor` sets, from its `init` function, the
function that `OfCursor` calls to read a `*cursor.Reader`; the tagged file of
`bytescan` does the same for `OfScanner` and a `*bytescan.Scanner`. So no
public package exports a new name in either build, and `go list -deps` of the
root package, `cursor`, `modes`, `rules` and `bytesource` names no package of
the measurement code in the normal build.

## The method

### The baseline machine

**Current contract** (owner decision, 2026-11-19; the report header names the
machine). Every report comes from
the shared runner. The shared runner is not quiet, so every value in a report
carries its spread.

### Lanes

**Current contract** (owner decision, 2026-11-19; source symbol `lex` in
`internal/bench`; tests `TestCasesLex` and `TestLaneNeedsItsBuild`). Every
reference case runs in three lanes:

| Lane | Build | Wrappers | Engine counters |
| --- | --- | --- | --- |
| **control** | normal | no | no |
| **wrapped** | normal | `meter.Reader` and `meter.Source` | no |
| **instrumented** | tag `bench` | `meter.Reader` and `meter.Source` | yes |

The rule set, the input, the consumed output and the timed boundaries are the
same in the three lanes. The input is in memory before the timer starts, so no
lane measures the file system or a generator. The harness owns the elapsed
interval of every run.

### Runs, the estimator and the spread

**Current contract** (owner decision, 2026-11-19; source symbols `rounds`,
`decide` and `aboveLimit` in `internal/bench`; test `TestSkewedSample`).

- **Runs.** Each lane of each case runs **10 times**. The runs of the three
  lanes are interleaved, so a slow period of the machine falls on every lane
  alike. The instrumented lane is a separate test binary, and the
  interleaving alternates the binaries. On a text case, the outside
  references run in the same rounds
  ([The outside references](#the-outside-references)).
- **Estimator.** The estimator is the **median** of the samples, as
  `benchstat` reports it. No report presents an arithmetic mean.
- **Interval.** The interval of a lane is `benchstat`'s 95 percent
  confidence interval of the median, stated as a percentage of the median.
  `benchstat` gives no such interval below 6 samples, so the run count is
  above that minimum.
- **Spread.** The reported spread of a lane is the larger of its interval and
  **5 percent** of the median (owner decision, 2026-11-20, in the words "5%
  resolution floor"; source symbol `reportedSpread`; test `TestSpreadFloor`).
  No difference under 5 percent is claimed between reports. The costs keep
  `benchstat`'s comparison of two lanes inside one report.
  The floor is there because the shared runner drifts between two reports by
  more than the interval inside one report.
- **Spread limit and rerun.** The spread limit is **5 percent**. When the
  interval of a lane is above it, the harness reruns that case once with
  **20** runs per lane. If the interval is still above 5 percent, the report
  marks the case **inconclusive** and gives no rate for it. The rule uses the
  interval, not the reported spread.
- **The check of the procedure.** Two reports of the same commit on the same
  machine have control medians that differ by less than their reported
  spread, or the case is marked inconclusive. This is a check of the
  procedure, not a statistical guarantee.

### The rate and the costs

**Current contract** (owner decision, 2026-11-19; source symbols `rateOf` and
`formatDelta` in `internal/bench`; tests `TestSkewedSample` and
`TestDeltaNeverZero`).

- **The rate** of a case is the bytes of its input divided by the median time
  per operation of the **control** lane, in **MB/s**: 10^6 bytes per second,
  the unit of `go test -bench`. It is the headline rate of the case.
- **The wrapper cost** is the wrapped lane against the control lane. **The
  counter cost** is the instrumented lane against the wrapped lane. Each cost
  is `benchstat`'s comparison of the two medians, with its p-value. A
  comparison that `benchstat` reports as not significant is reported as "not
  significant", never as zero.
- **The intake rate and the token rate** come from the wrapped lane: the
  counts of `meter.Reader` and `meter.Source` over the harness interval.
- **The counters** come from the instrumented lane. They do not depend on
  timing, so every run of a case gives the same counts; the harness checks
  that.

## The reference cases

**Current contract** (owner decisions, 2026-11-19; source symbol `cases` in
`internal/bench`; tests `TestCaseInputs`, `TestGeneratedInputs` and
`TestCasesLex`). Every rate is stated for one of five reference cases. A rule set from outside this module is copied
into `internal/bench`, with a comment that names its source, because
`internal/bench` cannot import a `main` package of bytescan-examples or a test
file of `tests/`.

| Case | Shape | Rule set and its source | Input | Size |
| --- | --- | --- | --- | --- |
| `json` | a real document | the JSON scanner of bytescan-examples, `json/scanner.go` | `golang_source.json` from the JSON test data of the Go source tree (`src/encoding/json/internal/jsontest/testdata/`, Go 1.25.4), 1,940,472 bytes, SHA-256 `23e8e3541eac3570958d6d430fc82867874be78a435580279b20f1efe5a6169f`; committed gzip-compressed under `internal/bench/testdata/` with the Go licence notice; read twice in a row | 3,880,944 bytes |
| `code` | token-dense | the seven hand-written rules of `BenchmarkComplexTokens` (`tests/bench_test.go`; the rules are in `tests/scanner_rules_test.go`): FLOAT, STRING, COMMENT, ID, WS, SYMBOL and KEYWORD | SQL-like statements generated from those token classes, with varied identifiers, numbers, strings and comments, from seed 1 | 4 MiB, ending at the first statement boundary past it |
| `slip` | binary frames | the SLIP scanner of bytescan-examples, `streams/slip/scanner.go`: END, DATA, ESC_END and ESC_ESC over a byte cursor | the record function of the SLIP example's stream test (`streams/slip/stream_test.go`), from seed 55 | 4 MiB, ending at the first frame boundary past it |
| `stream` | a long stream | the line scanner of the `streamtest` self-test (`addLineRules` in `streamtest/streamtest_test.go`): WORD, EQUALS, SPACE and NEWLINE | `lineRecord` of the same file, from seed 1, generated into memory before the timer starts | 64 MiB, ending at the first line boundary past it |
| `large` | large tokens | the hand-written identifier rule of `BenchmarkLargeToken` and the whitespace rule of `tests/scanner_rules_test.go` | four tokens of 1 MiB of `a`, separated by single spaces | 4 MiB and 3 bytes |

- Every case builds its scanner with `bytescan.NewWithCursor` over
  `cursor.NewReader`, or over `bytesource.NewCursor` for `slip`, with no
  `bufio` layer and no `WithMaxTokenBytes` guard (owner decision,
  2026-11-20; source symbols `lex` and `cases`). The rule sets are those of
  the table.
- A generated input uses a `streamtest.Generator` with the seed and the budget
  in the table, so the same seed always gives the same input. A generator ends
  its stream with the first record that takes it past the budget (Current
  contract; source symbol `streamtest.Generator`).
- The `stream` case is generated before the timer, not lexed with
  `streamtest.Run`: `Run` triggers garbage collections and samples the heap
  during the run, and the generator draws random numbers for every byte. Both
  would be inside the measured interval.
- SLIP shows every byte to the rules. It was chosen over MPEG-TS, whose driver
  reads most of each packet with the cursor's `Read`, so it would measure the
  cursor more than the rule VM.

## The outside references

**Current contract** (owner decision, 2026-11-20, in the words "Approve as
proposed"; source symbols `references`, `tokenClasses`, `refLex` and
`BenchmarkReference` in `internal/bench`; tests `TestReferencesLex`,
`TestRefLexRejects` and `TestReferenceSummary`). Two lexers from outside
bytescan run on the input of each text case, so a reader can see the scale of
a rate. They are not lanes: a reference has none of the instruments of the
lanes, and `bench.Lane` keeps its three values.

| Tool | What it is |
| --- | --- |
| `textscanner` | `text/scanner` of the standard library, with its default mode: the tokens of Go, white space and comments skipped |
| `regexp` | one regular expression of the standard library: an anchored alternation of the token classes of the case, one group for each rule in the order of the rule set, with leftmost-longest matching (`Regexp.Longest`), matched again at the end of each token |

- **The text cases** are `json`, `code`, `stream` and `large`. `slip` has no
  reference, because its input is binary.
- **The benchmark** is `BenchmarkReference/<case>/<tool>`. It reads the input
  of the case from memory, and its timer covers the interval from before the
  first token to after the last one, as that of `BenchmarkCase`. The setup of
  a reference, such as the compilation of the regular expression, is outside
  it.
- **The rounds.** The references run in the rounds of their case, after the
  three lanes, in the normal build. The first run of a round rotates over the
  five runs, so no run always comes first.
- **The spread.** The 5 percent floor, the spread limit and the rerun rule
  apply to a reference as to a lane: a reference above the limit makes its
  case rerun, and then inconclusive.
- **The raw files** are `<case>-ref-<tool>.txt`, in the raw directory of the
  report.
- **What the references do not do.** `textscanner` has fixed token classes,
  returns no token for white space and comments, and reports a byte sequence
  that is not a Go token as an error and goes on; so its tokens are not
  bytescan's tokens. The `regexp` lexer gives the same tokens as bytescan:
  `TestReferencesLex` checks the types and the byte offsets on `json`, `code`,
  `large` and the first 1 MiB of `stream`, and the runner checks the token
  count on every whole input. It needs the whole input in one byte slice and
  does not build a token with its text and position. The report states this
  in a fixed paragraph, so that the reader does not take the numbers as the
  same work.

## The report format

**Current contract** (owner decision, 2026-11-19; source symbol
`Report.WriteMarkdown`; test `TestSkewedSample`).

- **Where.** A report is a Markdown file in `internal-docs/benchmarks/`, named
  `<kind>-YYYY-MM-DD.md`, where `<kind>` is `baseline`, `statelex` or
  `bottlenecks`. The raw `go test -bench` output of every lane is committed
  beside it in `internal-docs/benchmarks/raw/YYYY-MM-DD-<sha12>/`, where
  `<sha12>` is the first twelve characters of the commit. Each case and lane
  has one file, `<case>-<lane>.txt`, and each run in it starts with the
  configuration line `lane: <lane>`. The first set of runs of a case that was
  rerun is kept in the subdirectory `superseded/`, and the report does not
  use it. `benchstat -col lane` over the files of the raw directory gives the
  same medians as the report.
- **The header** names the commit, the machine (host name, CPU model, number
  of logical CPUs, kernel), the load of the machine at the start and at the
  end, the Go version and `GOMAXPROCS`, the `benchstat` version, the method
  (runs per lane, interleaving, spread limit, rerun count), the command, and
  the directory of the raw samples.
- **Each case** has its rule set, its input, its size, its token count, the
  bytes per token, and its status: conclusive, conclusive after a rerun, or
  inconclusive. A lane table gives, for each lane, the median time per
  operation, the reported spread with its interval when the floor applies,
  or "no interval (∞)" when `benchstat` gives none, the rate from the median, the allocations per
  operation and the bytes allocated per operation. A cost table gives the
  wrapper cost and the counter cost with their p-values and sample counts.
  The wrapped lane gives the intake rate, the token rate and the `Read` calls.
  A counter table gives the fourteen counters of the instrumented lane and
  the derived quantities. A text case then has a table of the outside
  references: for each tool, the median time per operation, the reported
  spread, the rate from the median, the allocations and the bytes allocated
  per operation, its tokens and its errors; and the fixed paragraph on what
  each tool does and does not do
  ([The outside references](#the-outside-references)).
- **No number of a report goes into a public document**: the README, `docs/`
  and `docs/specs/` hold none. Publishing a measured number is an owner
  decision.

### A filled example

Every value in angle quotes is a placeholder that the runner fills.

```markdown
# Baseline report, ‹YYYY-MM-DD›

| Field | Value |
| --- | --- |
| Commit | `‹sha12›` (`master`) |
| Machine | shared runner `‹host name›`, `‹CPU model›`, ‹n› logical CPUs, Linux `‹kernel›` |
| Load | ‹1-minute load average› at the start, ‹1-minute load average› at the end |
| Go | `go‹version› linux/amd64`, GOMAXPROCS ‹n› |
| benchstat | `golang.org/x/perf ‹version›` |
| Method | ‹runs› runs per lane, lanes interleaved; spread limit ‹limit› percent; rerun once with ‹rerun runs› |
| Command | `go run ./internal/bench/cmd/benchreport` |
| Raw samples | `raw/‹YYYY-MM-DD›-‹sha12›/` |

## Case ‹name›

Rule set ‹rule set›; input ‹source›, ‹bytes› bytes, ‹tokens› tokens
(‹bytes per token› bytes per token). Status: **‹conclusive | conclusive after a rerun | inconclusive›**.

| Lane | Median time/op | Spread (95% CI of the median, at least 5 %) | Rate from the median | allocs/op | B/op |
| --- | --- | --- | --- | --- | --- |
| control | ‹T_c› | ± ‹s_c› % | **‹R_c› MB/s** | ‹A_c› | ‹B_c› |
| wrapped | ‹T_w› | ± ‹s_w› % | ‹R_w› MB/s | ‹A_w› | ‹B_w› |
| instrumented | ‹T_i› | ± ‹s_i› % | ‹R_i› MB/s | ‹A_i› | ‹B_i› |

Headline: ‹R_c› MB/s, ± ‹s_c› %, control lane.

| Cost | Delta of the medians | p-value | Samples |
| --- | --- | --- | --- |
| wrapper (wrapped against control) | ‹d_w› % | ‹p_w› | ‹n_c›, ‹n_w› |
| counters (instrumented against wrapped) | ‹d_i› % or not significant | ‹p_i› | ‹n_w›, ‹n_i› |

Wrapped lane, over the harness interval: intake ‹MB/s›, ‹tokens per second›
tokens per second, ‹Read calls› `Read` calls of ‹mean bytes› bytes on average.

Counters, instrumented lane (the same in every run):

| Counter | Value | Derived |
| --- | --- | --- |
| Symbols | ‹S› | — |
| RuleCalls | ‹RC› | ‹RC / S› rule calls per symbol |
| Pushbacks | ‹P› | ‹(RC − P) / S› active rules per step |
| MaxLive | ‹ML› | — |
| Tokens / LongTokens | ‹TK› / ‹LT› | ‹S / TK› symbols per token |
| Unknown | ‹U› | — |
| Abandons (end of input / ErrMoreInput / error) | ‹a› / ‹b› / ‹c› | — |
| StepBacks / StepBackBytes | ‹SB› / ‹SBB› | ‹SBB / bytes› re-read share |
| Fills / FillBytes | ‹F› / ‹FB› | ‹FB / F› bytes per fill |
| Compactions / CompactBytes | ‹C› / ‹CB› | — |
| Grows / MaxBuffer | ‹G› / ‹MB› | — |
| Replays / ReplayBytes | ‹RP› / ‹RPB› | — |
| Locks | ‹L› | ‹L / S› locks per symbol |
| FloorScans | ‹FS› | — |
| Map lookups by token type | (derived) | 0 |
| Reset walks | (derived) | ‹TK + a + b + c − 1›, each over ‹rules› rules |

Outside references, on the same input in the same rounds:

| Tool | Median time/op | Spread (95% CI of the median, at least 5 %) | Rate from the median | allocs/op | B/op | Tokens | Errors |
| --- | --- | --- | --- | --- | --- | --- | --- |
| textscanner | ‹T_t› | ± ‹s_t› % | ‹R_t› MB/s | ‹A_t› | ‹B_t› | ‹TK_t› | ‹E_t› |
| regexp | ‹T_r› | ± ‹s_r› % | ‹R_r› MB/s | ‹A_r› | ‹B_r› | ‹TK_r› | ‹E_r› |

‹the fixed paragraph on what each tool does and does not do›
```

A case marked inconclusive keeps its lane table and its spreads, and its rate
column says "inconclusive" instead of a rate. The same holds for its table of
the outside references. A case that is not a text case has no table of the
outside references.

## The statelex report

**Current contract** (owner decision, 2026-11-20, in the words "Approve as
proposed"; source symbols `BenchmarkEdit` in `statelex`, and `editCases`,
`reportEdits` and `writeEdits` in `internal/bench`; tests
`TestLatencyShapes`, `TestEditCasesMatchTheBenchmarks` and
`TestStatelexReportHasEveryField`). The report of the kind `statelex`
measures the latency of one edit of the lexer with state
([resumable lexing](resumable-lexing.md#the-lexer-with-state)). It keeps the
method above, with the differences below.

- **The benchmark.** `BenchmarkEdit/<set>/<shape>/<size>` is in a test file
  of package `statelex`, so it reads the work counters that the work tests
  read, and no package exports a new name. The sets are `A` and `B`, the rule
  sets of the [edit cases](resumable-lexing.md#edit-cases). The shapes are
  `load`, `key-start`, `key-middle`, `key-end`, `backspace-start`,
  `backspace-middle`, `backspace-end`, `selection-middle`, `quote-start`,
  `unquote-start` and `settext-middle`. The sizes are `64KiB`, `512KiB` and
  `4MiB`, and each text is generated from seed 1.
- **One operation is one edit.** The timer covers only the call of `Apply`
  or `SetText`. The edit that puts the text back runs with the timer stopped.
- **The metrics.** Each benchmark reports, with `testing.B.ReportMetric`,
  `removed-tokens/op` and `inserted-tokens/op` (the size of the change), and
  `copied-bytes/op`, `visited-tokens/op` and `relexed-bytes/op` (the work
  amounts).
- **One lane.** The work counters of `statelex` are not under the build tag,
  so every run is in the lane `control`. The report has no wrapper cost and
  no counter cost.
- **The runs.** Each run is one process with `-test.benchtime 200ms`. A
  round runs every case once, and the first case of a round rotates. The
  estimator, the spread, the floor and the rerun rule are those of
  [Runs, the estimator and the spread](#runs-the-estimator-and-the-spread).
- **The raw files** are `raw/YYYY-MM-DD-<sha12>/<set>-<shape>-<size>-control.txt`.
  The runner option `-cases` takes the same names, such as
  `A-key-start-64KiB`.
- **The layout.** The header is the header of the format above. Then the
  report has one table for each set and shape, with one row for each size,
  and no rate in MB/s.
