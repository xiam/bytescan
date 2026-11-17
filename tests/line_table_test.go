package tests

import (
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/base"
)

const (
	// newlineStreamLines is the input size of TestNewlineStreamLiveHeap, in
	// lines of one "\n" each: 256 Ki lines, or 128 Ki lines under the race
	// detector, which lexes several times slower. Both are well above the
	// 64 Ki lines that fill one cursor buffer, so the limit below tells
	// a bounded table from a growing one.
	newlineStreamLines     = 256 << 10
	newlineStreamLinesRace = 128 << 10

	// newlineStreamLiveLimit is the most live heap above the baseline that
	// the scanner may hold after it has lexed every line.
	//
	// textreader v0.4.0 keeps the line-table entries of its buffer only. Its
	// buffer holds 64 KiB, which is 64 Ki lines of one byte each, so on this
	// input the reader settles at about 1,119 KiB (the table entries of a full
	// buffer and the buffer itself), measured flat from 56 Ki to 320 Ki lines
	// with textreader v0.4.0. The limit is that bound plus about 37%.
	//
	// textreader v0.3.0 kept one entry per newline for the whole stream, about
	// 16.5 B per line: about 2.2 MiB at 128 Ki lines and 4.2 MiB at 256 Ki
	// lines, both above the limit.
	newlineStreamLiveLimit = 1536 << 10

	// newlineStreamHeapLimit stops the test once the heap (live and garbage)
	// passes it above the baseline, so a regression fails fast instead of
	// exhausting the machine.
	newlineStreamHeapLimit = 256 << 20
)

// TestNewlineStreamLiveHeap covers the per-line table of the cursor
// (trimmed in textreader v0.4.0, now bytescan/cursor): a stream of newlines must not grow the
// scanner's live heap with the number of lines. The test lexes every line with
// base.Newline, then collects garbage and reads the live heap above a baseline
// taken after garbage collection, before the first Next. It measures before
// the end of input, while the scanner still holds its reader.
func TestNewlineStreamLiveHeap(t *testing.T) {
	lines := newlineStreamLines
	if raceDetector {
		lines = newlineStreamLinesRace
	}
	input := strings.Repeat("\n", lines)

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("NEWLINE", base.Newline())

	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	baseline := ms.HeapAlloc

	for i := 0; i < lines; i++ {
		lex, err := lx.Next()
		require.NoError(t, err, "line %d", i)
		if lex.Type() != "NEWLINE" || lex.Text() != "\n" {
			t.Fatalf("line %d: got %s %q, want NEWLINE %q", i, lex.Type(), lex.Text(), "\n")
		}
		if i%(16<<10) == 0 {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > baseline+newlineStreamHeapLimit {
				t.Fatalf("line %d: heap passed %d MiB above the baseline", i, newlineStreamHeapLimit>>20)
			}
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&ms)
	live := int64(ms.HeapAlloc) - int64(baseline)

	lex, err := lx.Next()
	assert.Nil(t, lex)
	assert.ErrorIs(t, err, io.EOF)

	t.Logf("%d lines: %.1f KiB live above the baseline, %.2f B per line (limit %d KiB, race detector: %v)",
		lines, float64(live)/(1<<10), float64(live)/float64(lines), newlineStreamLiveLimit>>10, raceDetector)
	assert.Less(t, live, int64(newlineStreamLiveLimit), "live heap above the baseline after %d lines", lines)
}
