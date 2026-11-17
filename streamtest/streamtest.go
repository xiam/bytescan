// Package streamtest turns a scanner into a long-running stream test: it checks
// that the scanner reads a stream of any length in constant memory, and that it
// keeps lexing after corrupt data.
//
// A test builds a Generator from a record function and a seed. The generator
// is an io.Reader that writes records on the fly until it reaches a byte
// budget, so the stream is never held in memory. Corrupt wraps a generator and
// inserts a garbage run between records at a given rate, and reports where it
// did so. Prefix puts fixed bytes, such as a warm-up built with Records or one
// hand-made record, before a generated stream. Run lexes the stream to the
// end with any bytescan.Source, counts the tokens by type, samples the live
// heap, and logs bytes, tokens, elapsed time and throughput:
//
//	func record(rng *rand.Rand, i int, dst []byte) []byte {
//		return fmt.Appendf(dst, "key%d=%d\n", i, rng.Intn(1000))
//	}
//
//	func TestLong(t *testing.T) {
//		gen := streamtest.NewGenerator(1, record)
//		res := streamtest.Run(t, newScanner, gen)
//		if res.Counts["NEWLINE"] != int64(gen.Records()) {
//			t.Fatalf("lexed %d NEWLINE, want %d", res.Counts["NEWLINE"], gen.Records())
//		}
//	}
//
// Two smaller helpers cover the short tests next to a long one. LexAll reads
// a bytescan.Source to io.EOF and returns its tokens. LexRetained does the
// same over a cursor whose retention is bounded, which proves that a driver
// releases its scanners before it reads a long field from the cursor;
// LexRetainedBytes does it over a byte-mode cursor, for binary input.
//
// # Budget
//
// The default budget is 512 KiB, or 256 KiB under the race detector, which
// lexes about 0.1 MiB/s. The race budget is four cursor buffers, so that
// about a dozen samples follow the moved baseline (see Baseline). At those
// sizes a default run takes well under 3 s with and without -race. The
// -streamtest.long test flag raises the budget to LongBudget (1 GiB). A 1 GiB
// run takes many minutes, and go test stops a test binary at 10 minutes by
// default, so run it with -timeout 0:
//
//	go test -timeout 0 -run Long ./path/to/package -streamtest.long
//
// WithBudget sets a different budget for one generator. A budget set with
// WithBudget is explicit, even when its value equals the default.
//
// # Baseline
//
// Run samples the live heap (runtime.GC, then runtime.MemStats.HeapAlloc) every
// time the tokens have advanced another sampling interval into the stream,
// budget/16 (at least 4 KiB) by default, and once more at the end of the
// stream. The first sample is the baseline. The test fails as soon as a later
// sample exceeds the baseline by more than the ceiling, and it fails if the
// run takes fewer than two samples, because then nothing was checked.
//
// Under the race detector, a run over a stream with the implicit default
// budget (a Generator built without WithBudget, a Corrupter over one, or a
// Prefix of either, in a run without -streamtest.long) takes its baseline
// later: at the first sample after the tokens have passed one
// cursor.Reader buffer, 64 KiB of the stream. Progress is the offset of the tokens,
// not the bytes read from the source, because the cursor reads ahead of the
// scanner. Up to that point the cursor holds the whole stream, and its line
// table grows with every line (16 B per line, about 32 to 37 KiB for the
// self-test's lines). Once the stream passes the buffer, the cursor trims the
// table but keeps its capacity, so that memory stays live for the rest of the
// run. A baseline taken before it counts the table as growth, and under host
// load each new runtime thread adds about 5 KiB more, which took clean runs
// past the race ceiling. With the baseline after the first buffer, the table
// is part of the baseline. A stream too short to give a sample after this
// baseline fails the run. Every other run, and every run without the race
// detector, takes its first sample as the baseline.
//
// # Ceiling
//
// The default ceiling is DefaultCeiling(budget): budget/2, but at least 48
// KiB and at most 16 MiB. That is 256 KiB for the default budget without the
// race detector and 16 MiB for -streamtest.long. Under the race detector, a
// run over a stream with the implicit default budget and no WithCeiling uses
// a fixed ceiling of 48 KiB instead: the value that DefaultCeiling gave the
// former 64 KiB race budget. Every explicit budget keeps the DefaultCeiling
// value, under the race detector too: WithBudget(DefaultBudget()) gets
// DefaultCeiling(256 KiB), which is 128 KiB, and so does a test that passes
// WithCeiling(DefaultCeiling(DefaultBudget())).
//
// The floor of DefaultCeiling exists because a cursor.Reader trims only
// once its first 64 KiB buffer is full: on a stream that fits in one buffer,
// the line table of that buffer grows whether the scanner leaks or not. The
// value was fixed by running the self-tests of this package 20 times in a row,
// with and without -race: a clean line scanner stays under it, and the same
// scanner with a second scanner that is never released (so its checkpoint pins
// the stream) exceeds it. A scanner that holds more by design, such as a queue
// of tokens, sets its own ceiling with WithCeiling.
//
// # Per-token guard
//
// A stream test builds every scanner with a per-token guard: bytescan
// WithMaxTokenBytes, through NewWithOptions for one scanner or
// NewWithCursorOptions for each scanner of a mode driver. The guard is a little
// above the largest bounded token: TextGuard on a UTF-8 cursor, ByteGuard on
// a cursor of the bytesource package, which reads one rune per byte. A rule
// that scans without bound then fails Run with an error that wraps
// cursor.ErrRetentionExceeded, instead of growing the heap until the
// ceiling catches it.
package streamtest

import (
	"flag"
	"testing"
)

const (
	// LongBudget is the budget with the -streamtest.long flag: 1 GiB.
	LongBudget = 1 << 30

	// maxDefaultCeiling caps the default ceiling, so a -streamtest.long run
	// is held to a fixed amount of growth however long the stream is.
	maxDefaultCeiling = 16 << 20

	// minDefaultCeiling is the floor of the default ceiling: three quarters of
	// the 64 KiB buffer of a cursor.Reader. A cursor trims only once its
	// first buffer is full, so on a stream no longer than one buffer the heap
	// grows with the line table of that buffer (16 B per line, about 37 KiB
	// for the self-test's lines) whether the scanner leaks or not. Retained
	// input past the buffer doubles it, which adds 64 KiB.
	minDefaultCeiling = 48 << 10

	// raceDefaultCeiling is the ceiling of a run under the race detector
	// over a stream with the default budget and no WithCeiling: the value
	// DefaultCeiling gave the former 64 KiB race budget.
	raceDefaultCeiling = 48 << 10

	// cursorBuffer is the buffer of a cursor.Reader (bytescan/cursor).
	// Under the race detector, a run over a stream with the default budget
	// takes its baseline at the first sample after the tokens have passed
	// this much of the stream, once the cursor's line table holds a full
	// buffer of lines.
	cursorBuffer = 64 << 10

	// TextGuard is the per-token guard of a stream test on a UTF-8 cursor,
	// in bytes: a little above a bounded token of 1000 runes.
	TextGuard = 4096

	// ByteGuard is the per-token guard of a stream test on a cursor of
	// bytesource.NewCursor, in bytes: a little above a bounded token of 1000
	// bytes. That cursor counts one for each byte, whatever its value.
	ByteGuard = 1024
)

// long is the -streamtest.long test flag. It is registered only in test
// binaries: a test binary that imports the package gains the flag, and a
// program does not. The name is qualified with the package name, so it does
// not collide with a -long flag of the test that imports the package.
var long = func() *bool {
	if testing.Testing() {
		return flag.Bool("streamtest.long", false, "lex streams of at least 1 GiB (run with -timeout 0)")
	}
	return new(bool)
}()

// Long reports whether the test binary runs with the -streamtest.long flag.
func Long() bool {
	return *long
}

// DefaultBudget returns the stream budget in bytes: LongBudget with
// -streamtest.long, otherwise 512 KiB, or 256 KiB under the race detector.
// A Generator built without WithBudget has this budget. Under the race
// detector, Run gives a stream with that implicit budget a fixed 48 KiB
// default ceiling and a baseline after the first cursor buffer; see the
// package documentation.
func DefaultBudget() int64 {
	if Long() {
		return LongBudget
	}
	return defaultBudget
}

// DefaultCeiling returns the default heap ceiling for a stream of budget
// bytes: budget/2, but at least 48 KiB and at most 16 MiB, with and without
// the race detector. Run uses it for every stream except, under the race
// detector, a stream with the implicit default budget and no WithCeiling,
// which gets 48 KiB. A caller that computes DefaultCeiling(DefaultBudget())
// itself gets the formula value, 128 KiB under the race detector, not 48 KiB.
func DefaultCeiling(budget int64) int64 {
	return min(max(budget/2, minDefaultCeiling), maxDefaultCeiling)
}

// usesDefaultBudget reports whether src has the default budget of a run
// without -streamtest.long because no option set its budget: a Generator
// built without WithBudget, a Corrupter over one, or a Prefix of either.
func usesDefaultBudget(src Source) bool {
	d, ok := src.(interface{ usesDefaultBudget() bool })
	return ok && d.usesDefaultBudget()
}

// defaultCeiling returns the ceiling of a run over src with no WithCeiling.
func defaultCeiling(src Source) int64 {
	if raceDetector && usesDefaultBudget(src) {
		return raceDefaultCeiling
	}
	return DefaultCeiling(src.Budget())
}

// defaultBaselineAfter returns how far the tokens of src must have advanced,
// in bytes, before Run takes its baseline sample.
func defaultBaselineAfter(src Source) int64 {
	if raceDetector && usesDefaultBudget(src) {
		return cursorBuffer
	}
	return 0
}
