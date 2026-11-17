# Operations: bounds, release, retention and soak tests

A scanner that reads a stream that may never end must hold constant memory and
keep going after corrupt data. This page covers the four tools for that: the
per-token guard, `Release`, the cursor's retention budget, and soak tests
with [`streamtest`](../../streamtest). The contract is in
[specs/resources-and-streams.md](../specs/resources-and-streams.md).

## The per-token guard: `WithMaxTokenBytes`

While the scanner assembles a token it keeps every byte of it, and every byte a
rule reads ahead. `bytescan.WithMaxTokenBytes(n)` bounds that. A token that
would pass the bound makes `Next` return an error that matches
`bytescan.ErrTokenTooLarge` (and `cursor.ErrRetentionExceeded`) with
`errors.Is`, and the scanner consumes nothing.

```go
in := strings.Repeat("a", 25)

// An unbounded run on a scanner with a guard of 16 bytes.
lx := bytescan.NewWithOptions(strings.NewReader(in), bytescan.WithMaxTokenBytes(16))
lx.MustAddRule("RUN", core.RuneRun(core.ASCIILetter))
_, err := lx.Next()
fmt.Println(errors.Is(err, bytescan.ErrTokenTooLarge))

// The same input with a bounded run: pieces of at most 10 runes.
lx = bytescan.NewWithOptions(strings.NewReader(in), bytescan.WithMaxTokenBytes(16))
lx.MustAddRule("RUN", core.RuneRunMax(core.ASCIILetter, 10))
printAll(lx)
```

The code is `Example_guideGuard` in
[`example_guides_test.go`](../../example_guides_test.go). `printAll` is in
[`example_source_test.go`](../../example_source_test.go).

- **The guard is a safety net, not a stop.** Bound every rule of a stream with
  `core.RuneRunMax` or a counted repeat, so that on input that never ends a
  record the scanner keeps producing bounded pieces. The guard then fires only
  for a rule that scans without bound.
- **Set it a little above the largest bounded token:**
  `streamtest.TextGuard` (4,096 bytes) on a UTF-8 cursor,
  `streamtest.ByteGuard` (1,024 bytes) on a `bytesource.NewCursor` cursor,
  which counts one for each byte. Only a cursor built with
  `cursor.NewRuneReader` over `bytesource.NewReader` counts a byte of 0x80 or
  above as two bytes; use `bytesource.NewCursor` instead.
- **Build it into your constructor**, not only into your tests, so that the
  program runs with the same guard as the tests. Use
  `bytescan.NewWithOptions` for one scanner and
  `bytescan.NewWithCursorOptions` for each scanner over a shared cursor.
- **Set it for untrusted input even on short documents.** The rules that can
  reject (`Quoted`, `BlockComment`, `Float` and the others listed in
  [rules.md](../rules.md#long-tokens-and-memory)) read a long run to its end
  before they decide, and the scanner can read the same run more than once.

## `Release`

A scanner keeps checkpoints on its cursor for its last tokens, so that
`Context` can show the text around them. While it is idle, those checkpoints
pin all the input that is read after them. `Scanner.Release` drops them; the
scanner stays usable.

- **A mode driver releases the outgoing scanner at every switch.**
  `modes.Driver.Use` does it for you.
- **A driver that reads the cursor itself releases its scanner first**: before
  `bytesource.Read`, `bytesource.Peek` or `Fields.NextField`.
- **Never drop a scanner without releasing it**, and never build a scanner per
  token. Build one per mode, or one per key with `modes.Cache`, and reuse it.
- **A scanner on its own cursor needs no `Release`.** Its checkpoints move
  forward with every token.

## Retention

The cursor keeps the input that its checkpoints need. A cursor built by
`bytescan.NewWithOptions` gets a retention budget that fits the guard. A
cursor you build yourself takes one with `cursor.WithMaxRetained(n)`, and
every scanner on it shares that budget. A read past the budget fails with
`cursor.ErrRetentionExceeded`.

That makes the budget a test: `streamtest.LexRetained(t, rr, n, newScanner)`
lexes a short input over a cursor that retains at most `n` bytes. Feed it a
field longer than `n`. If the driver does not release its scanner before it
reads the field, the cursor fails, and so does the test:

```go
func TestReleaseBeforeRead(t *testing.T) {
	ls := streamtest.LexRetained(t, bytesource.NewReader(bytes.NewReader(bigPublish())), 1<<10, func(cur bytescan.Cursor) *driver {
		return newDriver(cur, bytescan.WithMaxTokenBytes(tokenGuard))
	})
	if n := len(ls); n != 8 {
		t.Errorf("lexed %d tokens, want 8", n)
	}
}
```

The code is `TestReleaseBeforeRead` in
[`streams/mqtt/scanner_test.go`](https://github.com/xiam/bytescan-examples/blob/main/streams/mqtt/scanner_test.go).

For binary input, `streamtest.LexRetainedBytes(t, r, n, newScanner)` takes the
`io.Reader` itself and builds the byte-mode cursor of `bytesource.NewCursor`
with the budget, so `n` counts input bytes. `LexRetained` over
`bytesource.NewReader` encodes the runes as UTF-8 and retains two bytes for
each byte of 0x80 or above.

## Soak tests with `streamtest`

`streamtest` turns a scanner into a long-running stream test:

- `NewGenerator(seed, record, opts...)` is an `io.Reader` that writes records
  on the fly until a byte budget: 512 KiB by default, 256 KiB under `-race`.
  `WithBudget` sets an explicit budget.
- `Corrupt(gen, seed, rate, garbage)` inserts garbage between records and
  reports each run through `NextInjection`. `Garbage(alphabet, min, max)`
  builds runs from an alphabet that no rule matches.
- `Prefix(src, head...)` puts fixed bytes first: a warm-up from `Records`, or
  the largest record the format allows.
- `Run(t, newScanner, src, opts...)` lexes the stream with any
  `bytescan.Source`, counts tokens by type, and fails when the live heap
  grows past the ceiling. `WithTokenFunc` sees each token as it arrives;
  `WithCeiling` sets a ceiling for a scanner that holds more by design.

```go
func TestLongRun(t *testing.T) {
	gen := streamtest.NewGenerator(1, sentenceRecord)
	res := streamtest.Run(t, newScanner, gen)
	records := int64(gen.Records())
	for _, typ := range []bytescan.TokenType{start, address, checksum, eol} {
		if got := res.Counts[typ]; got != records {
			t.Errorf("lexed %d %s, want one per sentence (%d)", got, typ, records)
		}
	}
	if got := res.Counts[bytescan.TokenTypeUnknown]; got != 0 {
		t.Errorf("lexed %d UNKNOWN in a clean stream", got)
	}
	if res.Bytes != gen.Bytes() {
		t.Errorf("Run read %d bytes, the generator wrote %d", res.Bytes, gen.Bytes())
	}
}
```

The code is `TestLongRun` in
[`streams/nmea/stream_test.go`](https://github.com/xiam/bytescan-examples/blob/main/streams/nmea/stream_test.go).
[`streamtest/example_test.go`](../../streamtest/example_test.go) shows a
generator and a corrupter without a test.

`Run` samples the live heap at intervals of the stream (budget/16 by default,
`WithSampleEvery` sets another) and compares each sample with the first one,
the baseline. The default ceiling is `DefaultCeiling(budget)`: budget/2, at
least 48 KiB and at most 16 MiB.

Under `-race` and without `-streamtest.long`, a stream with the implicit
default budget (a generator built without `WithBudget`, or a `Corrupt` or
`Prefix` over one) is checked in a different way:

- The baseline is the first sample after the tokens have passed 64 KiB of the
  stream, one `cursor.Reader` buffer. Up to that point the cursor's line
  table grows with every line. After it, the cursor trims the table but keeps
  its capacity, so about 32 to 37 KiB stays live. An earlier baseline counted
  that table as growth, and on a loaded host each new runtime thread added
  about 5 KiB, which failed clean runs. A stream too short to give a sample
  after this baseline fails the run.
- The ceiling is a fixed 48 KiB unless `WithCeiling` sets one.

A stream with an explicit budget keeps the first sample as its baseline and
`DefaultCeiling` of its budget, also when that budget equals the default:
`WithBudget(streamtest.DefaultBudget())` and
`WithCeiling(streamtest.DefaultCeiling(streamtest.DefaultBudget()))` both give
128 KiB under `-race`, not 48 KiB.

Run the long version with the `-streamtest.long` flag, which raises the
budget to 1 GiB. That takes many minutes, so turn off the test timeout:

```sh
go test -timeout 0 -run Long ./path/to/package -streamtest.long
```

Throughput is logged, not asserted. Under the race detector a scanner reads
about 0.1 MiB/s, so keep the default budgets for `go test -race`. The
harness self-test has a known, open flake under `-race`.

## A checklist for a stream scanner

1. Every rule is bounded; a long run comes out in pieces.
2. Every scanner is built with the guard, in the constructor.
3. Every driver releases a scanner before it switches away or reads the cursor.
4. A test with input that never ends a record gives bounded pieces and no
   error.
5. A `LexRetained` test (`LexRetainedBytes` for binary input) proves the
   release, for every driver that reads fields.
6. A `Run` test on a generated stream, and a corruption test with `Corrupt`.

## Examples

The stream examples under [streams](https://github.com/xiam/bytescan-examples/tree/main/streams) apply
this checklist; [nmea](https://github.com/xiam/bytescan-examples/tree/main/streams/nmea),
[mqtt](https://github.com/xiam/bytescan-examples/tree/main/streams/mqtt) and
[ssejson](https://github.com/xiam/bytescan-examples/tree/main/streams/ssejson) show each tool.
