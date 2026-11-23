//go:build bench

package counters_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/counters"
)

// The tests in this file run in the build with the tag bench. Each one runs a
// small input and checks that every counter equals the value that the comment
// derives from the code of the cursor or the scanner.

func cursorCounters(t *testing.T, r *cursor.Reader) counters.Cursor {
	t.Helper()
	c, ok := counters.OfCursor(r)
	if !ok {
		t.Fatal("OfCursor returned false in the build with the tag bench")
	}
	return c
}

func scannerCounters(t *testing.T, sc *bytescan.Scanner) counters.Scanner {
	t.Helper()
	s, ok := counters.OfScanner(sc)
	if !ok {
		t.Fatal("OfScanner returned false in the build with the tag bench")
	}
	return s
}

func readRunes(t *testing.T, r *cursor.Reader, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, _, err := r.ReadRune(); err != nil {
			t.Fatalf("ReadRune %d: %v", i, err)
		}
	}
}

func TestOfWrongValues(t *testing.T) {
	var nilReader *cursor.Reader
	var nilScanner *bytescan.Scanner
	for _, v := range []any{nil, 1, nilReader, bytescan.New(strings.NewReader(""))} {
		if _, ok := counters.OfCursor(v); ok {
			t.Errorf("OfCursor(%T) returned true", v)
		}
	}
	for _, v := range []any{nil, 1, nilScanner, cursor.New(strings.NewReader(""))} {
		if _, ok := counters.OfScanner(v); ok {
			t.Errorf("OfScanner(%T) returned true", v)
		}
	}
}

// TestCursorFillsAndLocks reads "abc" through a buffer of 16 bytes. The first
// ReadRune fills the buffer with the 3 bytes in one read. The next two runes
// are in the buffer. The fourth ReadRune reads the source again, which returns
// io.EOF and no byte. So 2 fills of 3 bytes, 4 locks, a buffer of 16 bytes
// that never grows, and no compaction: the read position stays below the
// buffer size minus utf8.UTFMax. Without a checkpoint and a retention limit,
// the floor search visits no checkpoint.
func TestCursorFillsAndLocks(t *testing.T) {
	r := cursor.NewReader(strings.NewReader("abc"), cursor.WithCapacity(16))
	readRunes(t, r, 3)
	if _, _, err := r.ReadRune(); err != io.EOF {
		t.Fatalf("ReadRune at the end = %v, want io.EOF", err)
	}

	want := counters.Cursor{Fills: 2, FillBytes: 3, MaxBuffer: 16, Locks: 4}
	if got := cursorCounters(t, r); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestCursorCompactions reads "abcdefghij" rune by rune through a buffer of 8
// bytes:
//
//   - rune 1 fills 8 bytes; runes 2 to 5 are in the buffer;
//   - rune 6 has 3 bytes left, fewer than utf8.UTFMax, at read index 5: the
//     buffer compacts and moves the 3 bytes "fgh";
//   - rune 9 fills the last 2 bytes "ij";
//   - rune 10 has 1 byte left at read index 4: the buffer compacts and moves
//     the byte "j";
//   - the eleventh ReadRune reads io.EOF from the source.
//
// So 3 fills of 10 bytes, 2 compactions of 4 bytes, and 11 locks.
func TestCursorCompactions(t *testing.T) {
	r := cursor.NewReader(strings.NewReader("abcdefghij"), cursor.WithCapacity(8))
	readRunes(t, r, 10)
	if _, _, err := r.ReadRune(); err != io.EOF {
		t.Fatalf("ReadRune at the end = %v, want io.EOF", err)
	}

	want := counters.Cursor{
		Fills: 3, FillBytes: 10,
		Compactions: 2, CompactBytes: 4,
		MaxBuffer: 8,
		Locks:     11,
	}
	if got := cursorCounters(t, r); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestCursorGrowsAndFloorScans reads "abcdef" through a buffer of 4 bytes, the
// smallest one, under a checkpoint at offset 0. The checkpoint keeps every
// byte, so no compaction moves a byte, and every call of the compaction visits
// the one checkpoint:
//
//   - runes 1 to 4: the first fills 4 bytes; each one compacts once, because
//     the read index plus utf8.UTFMax reaches the buffer size (4 visits);
//   - rune 5 compacts 3 times: before the fill, inside it, and once more when
//     the rune loop checks the 2 bytes that the fill read; the full buffer
//     grows to 8 bytes, and the fill reads "ef" (7 visits);
//   - rune 6 compacts once (8 visits);
//   - the seventh ReadRune compacts once and reads io.EOF (9 visits).
//
// So 3 fills of 6 bytes, 1 growth to 8 bytes, 9 floor scans, and 8 locks with
// the Checkpoint call.
func TestCursorGrowsAndFloorScans(t *testing.T) {
	r := cursor.NewReader(strings.NewReader("abcdef"), cursor.WithCapacity(4))
	r.Checkpoint()
	readRunes(t, r, 6)
	if _, _, err := r.ReadRune(); err != io.EOF {
		t.Fatalf("ReadRune at the end = %v, want io.EOF", err)
	}

	want := counters.Cursor{
		Fills: 3, FillBytes: 6,
		Grows: 1, MaxBuffer: 8,
		Locks:      8,
		FloorScans: 9,
	}
	if got := cursorCounters(t, r); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestCursorRetentionFloorScans arms two checkpoints on a reader with a
// retention limit. A ReadRune then checks the headroom, which visits both
// checkpoints, and RetainedBytes visits both again: 4 floor scans. The buffer
// of 16 bytes never compacts. Locks: 2 Checkpoint calls, 1 ReadRune and 1
// RetainedBytes.
func TestCursorRetentionFloorScans(t *testing.T) {
	r := cursor.NewReader(strings.NewReader("abc"), cursor.WithCapacity(16), cursor.WithMaxRetained(100))
	r.Checkpoint()
	r.Checkpoint()
	readRunes(t, r, 1)
	if got := r.RetainedBytes(); got != 1 {
		t.Fatalf("RetainedBytes = %d, want 1", got)
	}

	want := counters.Cursor{Fills: 1, FillBytes: 3, MaxBuffer: 16, Locks: 4, FloorScans: 4}
	if got := cursorCounters(t, r); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestCursorReplays reads 4 runes of "abcdef" after a checkpoint, resets,
// reads 2 runes and resets again: 2 replays that rewind 4 and 2 bytes. A
// Release ends the checkpoint. Locks: Checkpoint, 4 + 2 ReadRune calls, 2
// Reset calls and Release.
func TestCursorReplays(t *testing.T) {
	r := cursor.NewReader(strings.NewReader("abcdef"), cursor.WithCapacity(16))
	cp := r.Checkpoint()
	readRunes(t, r, 4)
	if err := cp.Reset(); err != nil {
		t.Fatal(err)
	}
	readRunes(t, r, 2)
	if err := cp.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := cp.Release(); err != nil {
		t.Fatal(err)
	}

	want := counters.Cursor{
		Fills: 1, FillBytes: 6,
		MaxBuffer: 16,
		Replays:   2, ReplayBytes: 6,
		Locks: 10,
	}
	if got := cursorCounters(t, r); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// countingRuneReader counts the calls of ReadRune.
type countingRuneReader struct {
	r     *strings.Reader
	reads int
}

func (c *countingRuneReader) ReadRune() (rune, int, error) {
	c.reads++
	return c.r.ReadRune()
}

// TestCursorFillsOfARuneSource reads "a😀" from a rune source through a buffer
// of 4 bytes under a checkpoint. The first fill reads 'a' (1 byte). The second
// fill reads 😀, which does not fit the 3 bytes left, so the reader holds it
// back. The buffer then grows, and the next fill writes the held rune (4
// bytes) without reading the source. So after two runes the source was read 2
// times and 5 bytes were written. The third ReadRune reads io.EOF from the
// source: 3 reads, still 5 bytes.
func TestCursorFillsOfARuneSource(t *testing.T) {
	src := &countingRuneReader{r: strings.NewReader("a😀")}
	r := cursor.NewRuneReader(src, cursor.WithCapacity(4))
	r.Checkpoint()
	readRunes(t, r, 2)

	c := cursorCounters(t, r)
	if c.Fills != 2 || c.FillBytes != 5 || src.reads != 2 {
		t.Fatalf("after 2 runes: Fills %d, FillBytes %d, source reads %d; want 2, 5, 2", c.Fills, c.FillBytes, src.reads)
	}

	if _, _, err := r.ReadRune(); err != io.EOF {
		t.Fatalf("ReadRune at the end = %v, want io.EOF", err)
	}
	c = cursorCounters(t, r)
	if c.Fills != 3 || c.FillBytes != 5 || src.reads != 3 {
		t.Fatalf("at the end: Fills %d, FillBytes %d, source reads %d; want 3, 5, 3", c.Fills, c.FillBytes, src.reads)
	}
}

// aRun matches a+: it accepts each 'a' and stays active, and rejects any other
// symbol.
func aRun(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.Rune() == 'a' {
		return aRun, bytescan.StateAccept
	}
	return nil, bytescan.StateReject
}

// cRun matches c+, as aRun matches a+.
func cRun(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.Rune() == 'c' {
		return cRun, bytescan.StateAccept
	}
	return nil, bytescan.StateReject
}

// literal returns a rule that matches the single rune r.
func literal(r rune) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.Rune() == r {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
}

// aBeforeB matches an 'a' that a 'b' follows. It reads the 'b' and excludes it
// from the match with one pushback.
func aBeforeB(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.Rune() != 'a' {
		return nil, bytescan.StateReject
	}
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.Rune() != 'b' {
			return nil, bytescan.StateReject
		}
		return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			return nil, bytescan.StateAccept
		}, bytescan.StatePushBack
	}, bytescan.StateContinue
}

func lexAll(t *testing.T, sc *bytescan.Scanner) []string {
	t.Helper()
	var texts []string
	for {
		tok, err := sc.Next()
		if err == io.EOF {
			return texts
		}
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, string(tok.Type())+":"+tok.Text())
	}
}

// TestScannerCounters lexes "aa b" with the rules A (a+), A2 (a+) and SP (one
// space):
//
//   - "aa": the first 'a' calls 3 rules and leaves A and A2 active; the
//     second 'a' calls 2; the space calls 2 and retires both. A wins the tie.
//     The rules read 1 byte past the token. The scanner carries that symbol
//     to the next token, so the commit does not step back.
//   - " ": 1 symbol, 3 calls.
//   - "b": 1 symbol, 3 calls; no rule matches, so the token is UNKNOWN.
//   - the end of input: the EOF symbol, 3 calls, and the call returns io.EOF.
//
// So 6 symbols, 16 rule calls, no pushback, at most 2 live rules, 3 tokens, all
// short, 1 unknown, 1 abandon at the end of input, and no step back.
// TestScannerStepBacks counts the step back on a cursor without the lean read
// path.
func TestScannerCounters(t *testing.T) {
	sc := bytescan.New(strings.NewReader("aa b"))
	sc.MustAddRule("A", aRun)
	sc.MustAddRule("A2", aRun)
	sc.MustAddRule("SP", literal(' '))

	got := lexAll(t, sc)
	if want := []string{"A:aa", "SP: ", "UNKNOWN:b"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tokens %q, want %q", got, want)
	}

	want := counters.Scanner{
		Symbols:   6,
		RuleCalls: 16,
		MaxLive:   2,
		Tokens:    3,
		Unknown:   1,
		Abandons:  counters.Abandons{EndOfInput: 1},
	}
	if got := scannerCounters(t, sc); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// wrappedCursor embeds a *cursor.Reader. It is not a *cursor.Reader, so the
// scanner reads it rune by rune (ReadLocatedRune) and has no lean read path.
type wrappedCursor struct{ *cursor.Reader }

// TestScannerStepBacks lexes "aa b" with the rules of TestScannerCounters
// over a cursor without the lean read path. The rules read 1 byte past "aa",
// and the commit steps back over it: 1 step back of 1 byte. Every other
// counter is the one TestScannerCounters derives.
func TestScannerStepBacks(t *testing.T) {
	sc := bytescan.NewWithCursor(wrappedCursor{cursor.NewReader(strings.NewReader("aa b"))})
	sc.MustAddRule("A", aRun)
	sc.MustAddRule("A2", aRun)
	sc.MustAddRule("SP", literal(' '))

	got := lexAll(t, sc)
	if want := []string{"A:aa", "SP: ", "UNKNOWN:b"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tokens %q, want %q", got, want)
	}

	want := counters.Scanner{
		Symbols:   6,
		RuleCalls: 16,
		MaxLive:   2,
		Tokens:    3,
		Unknown:   1,
		Abandons:  counters.Abandons{EndOfInput: 1},
		StepBacks: 1, StepBackBytes: 1,
	}
	if got := scannerCounters(t, sc); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestScannerPushbacksAndLongTokens lexes "ab" and ten 'c' with the rules AB
// (an 'a' that a 'b' follows), B (one 'b') and C (c+):
//
//   - "a": the 'a' calls 3 rules and leaves AB active; the 'b' calls AB, which
//     pushes back once, so 2 calls of which 1 is a pushback call. The 'b'
//     is carried to the next token, so the commit does not step back.
//   - "b": 1 symbol, 3 calls.
//   - ten 'c': the first calls 3 rules, the next 9 call C only, and the EOF
//     symbol calls C once. The token has 10 runes, more than the 8 runes of
//     the inline text, so it is a long token.
//   - the end of input: the EOF symbol, 3 calls, and the call returns io.EOF.
//
// So 15 symbols, 24 rule calls, 1 pushback, at most 1 live rule, 3 tokens of
// which 1 is long, 1 abandon at the end of input, and no step back.
func TestScannerPushbacksAndLongTokens(t *testing.T) {
	sc := bytescan.New(strings.NewReader("ab" + strings.Repeat("c", 10)))
	sc.MustAddRule("AB", aBeforeB)
	sc.MustAddRule("B", literal('b'))
	sc.MustAddRule("C", cRun)

	got := lexAll(t, sc)
	if want := []string{"AB:a", "B:b", "C:" + strings.Repeat("c", 10)}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tokens %q, want %q", got, want)
	}

	want := counters.Scanner{
		Symbols:    15,
		RuleCalls:  24,
		Pushbacks:  1,
		MaxLive:    1,
		Tokens:     3,
		LongTokens: 1,
		Abandons:   counters.Abandons{EndOfInput: 1},
	}
	if got := scannerCounters(t, sc); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

var errBoom = errors.New("boom")

// stepSource returns "a", then cursor.ErrMoreInput, then errBoom.
type stepSource struct{ step int }

func (s *stepSource) Read(p []byte) (int, error) {
	s.step++
	switch s.step {
	case 1:
		return copy(p, "a"), nil
	case 2:
		return 0, cursor.ErrMoreInput
	default:
		return 0, errBoom
	}
}

// TestScannerAbandons lexes a source that gives "a", then has no input now,
// then fails, with the rule A (a+):
//
//   - the first Next reads 'a' (1 symbol, 1 call; A stays active), then the
//     source returns ErrMoreInput: the call abandons and the reset rewinds 1
//     byte.
//   - the second Next reads 'a' again from the buffer (1 symbol, 1 call), then
//     the source fails: the call abandons with an error, and the reset rewinds
//     1 byte.
//
// The cursor reads the source 3 times and gets 1 byte.
func TestScannerAbandons(t *testing.T) {
	r := cursor.NewReader(&stepSource{}, cursor.WithCapacity(16))
	sc := bytescan.NewWithCursor(r)
	sc.MustAddRule("A", aRun)

	if _, err := sc.Next(); err != bytescan.ErrMoreInput {
		t.Fatalf("first Next: %v, want ErrMoreInput", err)
	}
	if _, err := sc.Next(); !errors.Is(err, errBoom) {
		t.Fatalf("second Next: %v, want %v", err, errBoom)
	}

	want := counters.Scanner{
		Symbols:   2,
		RuleCalls: 2,
		MaxLive:   1,
		Abandons:  counters.Abandons{MoreInput: 1, Error: 1},
	}
	if got := scannerCounters(t, sc); got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	c := cursorCounters(t, r)
	if c.Fills != 3 || c.FillBytes != 1 || c.Replays != 2 || c.ReplayBytes != 2 {
		t.Fatalf("cursor counters %+v: want 3 fills of 1 byte and 2 replays of 2 bytes", c)
	}
}
