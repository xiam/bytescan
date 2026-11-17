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
// releases its scanners before it reads a long field from the cursor.
//
// # Budget
//
// The default budget is 512 KiB, or 64 KiB under the race detector, which
// lexes about 0.1 MiB/s. At those sizes a default run takes well under 3 s
// with and without -race. The -streamtest.long test flag raises the budget
// to LongBudget (1 GiB). A 1 GiB run takes many minutes, and go test stops a
// test binary at 10 minutes by default, so run it with -timeout 0:
//
//	go test -timeout 0 -run Long ./path/to/package -streamtest.long
//
// WithBudget sets a different budget for one generator.
//
// # Ceiling
//
// Run samples the live heap (runtime.GC, then runtime.MemStats.HeapAlloc) every
// time the tokens have advanced another sampling interval into the stream,
// budget/16 (at least 4 KiB) by default, and once more at the end of the
// stream. The first sample is the baseline. The test fails as soon as a later
// sample exceeds the baseline by more than the ceiling, and it fails if the
// run takes fewer than two samples, because then nothing was checked.
//
// The default ceiling is DefaultCeiling(budget): budget/2, but at least 48
// KiB and at most 16 MiB. That is 256 KiB for the default budget, 48 KiB
// under the race detector and 16 MiB for -streamtest.long. The floor exists
// because a textreader cursor trims only once its first 64 KiB buffer is
// full: on a stream that fits in one buffer, the line table of that buffer
// grows whether the scanner leaks or not. The value was fixed by running the
// self-tests of this package 20 times in a row, with and without -race: a
// clean line scanner stays under it, and the same scanner with a second scanner
// that is never released (so its checkpoint pins the stream) exceeds it. A
// scanner that holds more by design, such as a queue of tokens, sets its own
// ceiling with WithCeiling.
//
// # Per-token guard
//
// A stream test builds every scanner with a per-token guard: bytescan
// WithMaxTokenBytes, through NewWithOptions for one scanner or
// NewWithCursorOptions for each scanner of a mode driver. The guard is a little
// above the largest bounded token: TextGuard on a UTF-8 cursor, ByteGuard on
// a cursor of the bytesource package, which reads one rune per byte. A rule
// that scans without bound then fails Run with an error that wraps
// textreader.ErrRetentionExceeded, instead of growing the heap until the
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
	// the 64 KiB buffer of a textreader cursor. A cursor trims only once its
	// first buffer is full, so on a stream no longer than one buffer the heap
	// grows with the line table of that buffer (16 B per line, about 37 KiB
	// for the self-test's lines) whether the scanner leaks or not. Retained
	// input past the buffer doubles it, which adds 64 KiB.
	minDefaultCeiling = 48 << 10

	// TextGuard is the per-token guard of a stream test on a UTF-8 cursor,
	// in bytes: a little above a bounded token of 1000 runes.
	TextGuard = 4096

	// ByteGuard is the per-token guard of a stream test on a bytesource
	// cursor, which reads one rune per byte, in bytes. The guard counts the UTF-8 length of
	// each rune, so a byte of 0x80 or above counts as 2; a test whose input
	// has such bytes uses twice this value.
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
// -streamtest.long, otherwise 512 KiB, or 64 KiB under the race detector.
func DefaultBudget() int64 {
	if Long() {
		return LongBudget
	}
	return defaultBudget
}

// DefaultCeiling returns the default heap ceiling for a stream of budget
// bytes: budget/2, but at least 48 KiB and at most 16 MiB.
func DefaultCeiling(budget int64) int64 {
	return min(max(budget/2, minDefaultCeiling), maxDefaultCeiling)
}
