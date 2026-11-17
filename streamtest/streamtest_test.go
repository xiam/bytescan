package streamtest

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/textreader"
)

// lineRecord writes a line of the form "word=word word ...\n", with words of
// lower-case letters and digits.
func lineRecord(rng *rand.Rand, i int, dst []byte) []byte {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	word := func() {
		n := 1 + rng.Intn(12)
		for j := 0; j < n; j++ {
			dst = append(dst, letters[rng.Intn(len(letters))])
		}
	}
	word()
	dst = append(dst, '=')
	word()
	for n := rng.Intn(6); n > 0; n-- {
		dst = append(dst, ' ')
		word()
	}
	return append(dst, '\n')
}

// garbage is the alphabet of the injected runs: no rule of the line scanner
// matches any of its bytes, so each one lexes as UNKNOWN.
const garbage = "#%&*~"

// addLineRules registers the rules of the line scanner. WORD is bounded at 1000
// runes, as a stream example requires; no two rules match the same text.
func addLineRules(lx *bytescan.Scanner) {
	lx.MustAddRule("WORD", core.Repeat(core.Rune(core.RuneRange('a', 'z').Or(core.RuneRange('0', '9'))), 1, 1000))
	lx.MustAddRule("EQUALS", core.Literal("="))
	lx.MustAddRule("SPACE", core.Literal(" "))
	lx.MustAddRule("NEWLINE", core.Literal("\n"))
}

// newLineScanner is the clean scanner of the self-test, with the per-token guard.
func newLineScanner(r io.Reader) *bytescan.Scanner {
	lx := bytescan.NewWithOptions(bufio.NewReader(r), bytescan.WithMaxTokenBytes(TextGuard))
	addLineRules(lx)
	return lx
}

// twoScanners is a scanner and a second scanner on the same cursor that read one
// token and then stay idle, as a mode driver's scanner of an unused mode does.
type twoScanners struct {
	main, idle *bytescan.Scanner
}

func (d *twoScanners) Next() (*bytescan.Token, error) {
	return d.main.Next()
}

// newTwoScanners builds a twoScanners over r. When release is false the idle scanner
// is never released, so its checkpoint pins all the input read after it: the
// deliberate leak of the self-test.
func newTwoScanners(r io.Reader, release bool) *twoScanners {
	return newTwoScannersOn(textreader.NewReader(r), release)
}

// newTwoScannersOn is newTwoScanners over the cursor cur.
func newTwoScannersOn(cur bytescan.Cursor, release bool) *twoScanners {
	d := &twoScanners{
		main: bytescan.NewWithCursorOptions(cur, bytescan.WithMaxTokenBytes(TextGuard)),
		idle: bytescan.NewWithCursorOptions(cur, bytescan.WithMaxTokenBytes(TextGuard)),
	}
	addLineRules(d.main)
	addLineRules(d.idle)
	if _, err := d.idle.Next(); err != nil {
		panic(err)
	}
	if release {
		d.idle.Release()
	}
	return d
}

func newLeakyScanner(r io.Reader) *twoScanners    { return newTwoScanners(r, false) }
func newReleasedScanner(r io.Reader) *twoScanners { return newTwoScanners(r, true) }

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestGenerator checks that a seed gives the same stream, that the stream
// reaches its budget and ends at a record boundary, and that reads of any size
// see the same bytes.
func TestGenerator(t *testing.T) {
	const budget = 64 << 10
	a := NewGenerator(1, lineRecord, WithBudget(budget))
	got := readAll(t, a)

	if int64(len(got)) <= budget {
		t.Fatalf("stream is %d bytes, want more than the budget %d", len(got), budget)
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("stream does not end at a record boundary: %q", got[len(got)-16:])
	}
	last := got[:len(got)-1]
	if n := len(got) - 1 - bytes.LastIndexByte(last, '\n'); int64(len(got)-n) > budget {
		t.Fatalf("stream has %d bytes before its last record, want at most the budget %d", len(got)-n, budget)
	}
	if lines := bytes.Count(got, []byte("\n")); lines != a.Records() {
		t.Fatalf("stream has %d lines, the generator reports %d records", lines, a.Records())
	}
	if a.Bytes() != int64(len(got)) {
		t.Fatalf("generator reports %d bytes, read %d", a.Bytes(), len(got))
	}

	// A 1-byte reader sees the same stream.
	b := NewGenerator(1, lineRecord, WithBudget(budget))
	var small bytes.Buffer
	p := make([]byte, 1)
	for {
		n, err := b.Read(p)
		small.Write(p[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(small.Bytes(), got) {
		t.Fatal("a 1-byte reader read a different stream")
	}

	other := readAll(t, NewGenerator(2, lineRecord, WithBudget(budget)))
	if bytes.Equal(other, got) {
		t.Fatal("seeds 1 and 2 gave the same stream")
	}

	if d := NewGenerator(1, lineRecord); d.Budget() != DefaultBudget() {
		t.Fatalf("default budget is %d, want %d", d.Budget(), DefaultBudget())
	}
}

// TestCorrupt checks that every reported injection is a garbage run at its
// offset, followed by its record, and that the stream without the runs is the
// clean stream of the same seed.
func TestCorrupt(t *testing.T) {
	const budget = 64 << 10
	clean := readAll(t, NewGenerator(7, lineRecord, WithBudget(budget)))
	cleanLines := strings.SplitAfter(string(clean), "\n")

	c := Corrupt(NewGenerator(7, lineRecord, WithBudget(budget)), 8, 0.1, Garbage(garbage, 1, 16))
	got := readAll(t, c)
	if c.Budget() != budget {
		t.Fatalf("budget is %d, want %d", c.Budget(), budget)
	}

	var stripped []byte
	var prev int64
	var n int
	for {
		in, ok := c.NextInjection()
		if !ok {
			break
		}
		n++
		if in.Offset < prev {
			t.Fatalf("injection %d at %d is before the end of the previous one at %d", n, in.Offset, prev)
		}
		stripped = append(stripped, got[prev:in.Offset]...)
		run := got[in.Offset : in.Offset+int64(in.Len)]
		if strings.Trim(string(run), garbage) != "" {
			t.Fatalf("injection %d at %d is %q, want bytes of %q", n, in.Offset, run, garbage)
		}
		rest := string(got[in.Offset+int64(in.Len):])
		if !strings.HasPrefix(rest, cleanLines[in.Record]) {
			t.Fatalf("injection %d is not followed by record %d %q", n, in.Record, cleanLines[in.Record])
		}
		prev = in.Offset + int64(in.Len)
	}
	stripped = append(stripped, got[prev:]...)

	if n == 0 || n != c.Injected() {
		t.Fatalf("NextInjection returned %d runs, Injected reports %d", n, c.Injected())
	}
	if !bytes.Equal(stripped, clean) {
		t.Fatal("the corrupted stream without its garbage runs is not the clean stream")
	}
	t.Logf("%d garbage runs in %d records", n, len(cleanLines)-1)
}

// TestRunLong lexes a clean stream of the default budget, or of 1 GiB with
// -streamtest.long, and checks the token counts against the records.
func TestRunLong(t *testing.T) {
	gen := NewGenerator(1, lineRecord)
	res := Run(t, newLineScanner, gen)
	if got := res.Counts["NEWLINE"]; got != int64(gen.Records()) {
		t.Fatalf("lexed %d NEWLINE, want one per record (%d)", got, gen.Records())
	}
	if got := res.Counts["EQUALS"]; got != int64(gen.Records()) {
		t.Fatalf("lexed %d EQUALS, want one per record (%d)", got, gen.Records())
	}
	if got := res.Counts[bytescan.TokenTypeUnknown]; got != 0 {
		t.Fatalf("lexed %d UNKNOWN in a clean stream", got)
	}
	if res.Bytes != gen.Bytes() {
		t.Fatalf("Run read %d bytes, the generator wrote %d", res.Bytes, gen.Bytes())
	}
	if res.Samples < 2 {
		t.Fatalf("took %d heap samples, want at least 2", res.Samples)
	}
}

// TestLeakFails proves that the ceiling catches growth: a scanner whose idle
// second scanner is never released pins the stream and fails at the default
// budget and ceiling.
func TestLeakFails(t *testing.T) {
	_, err := run(newLeakyScanner, NewGenerator(1, lineRecord, WithBudget(defaultBudget)))
	if !errors.Is(err, ErrCeiling) {
		t.Fatalf("got error %v, want ErrCeiling", err)
	}
	t.Log(err)
}

// TestLeakReleased is TestLeakFails with the idle scanner released: the same
// stream stays under the ceiling.
func TestLeakReleased(t *testing.T) {
	Run(t, newReleasedScanner, NewGenerator(1, lineRecord, WithBudget(defaultBudget)))
}

// TestRunCorrupt lexes a corrupted stream and checks, as the tokens arrive,
// that each garbage run lexes as UNKNOWN tokens and that the next token is
// the first WORD of the record that follows it.
func TestRunCorrupt(t *testing.T) {
	c := Corrupt(NewGenerator(3, lineRecord, WithBudget(defaultBudget)), 4, 0.05, Garbage(garbage, 1, 16))

	var (
		cur     Injection
		have    bool
		unknown int64
	)
	check := func(l *bytescan.Token) {
		off := int64(l.ByteOffset())
		if !have || off >= cur.Offset+int64(cur.Len) {
			if have && off == cur.Offset+int64(cur.Len) && l.Type() != "WORD" {
				t.Fatalf("after the garbage at %d: got %s %q, want WORD", cur.Offset, l.Type(), l.Text())
			}
			cur, have = c.NextInjection()
		}
		inside := have && off >= cur.Offset && off < cur.Offset+int64(cur.Len)
		if inside != (l.Type() == bytescan.TokenTypeUnknown) {
			t.Fatalf("at %d: got %s %q, garbage run here: %v", off, l.Type(), l.Text(), inside)
		}
		if inside {
			unknown++
		}
	}
	// The garbage runs are read ahead of the tokens, so the callback can
	// always find the run that a token falls in.
	res := Run(t, newLineScanner, c, WithTokenFunc(check))
	if res.Counts[bytescan.TokenTypeUnknown] != unknown || unknown == 0 {
		t.Fatalf("lexed %d UNKNOWN, %d inside garbage runs", res.Counts[bytescan.TokenTypeUnknown], unknown)
	}
	t.Logf("%d garbage runs, %d UNKNOWN tokens", c.Injected(), unknown)
}

// TestTooFewSamples checks that a sampling interval longer than the stream
// fails the run instead of passing without a heap check.
func TestTooFewSamples(t *testing.T) {
	const budget = 16 << 10
	_, err := run(newLineScanner, NewGenerator(1, lineRecord, WithBudget(budget)), WithSampleEvery(4*budget))
	if err == nil || !strings.Contains(err.Error(), "no growth was checked") {
		t.Fatalf("got error %v, want the too-few-samples error", err)
	}
}

// TestRecords checks that Records returns the first records of the stream of
// the same seed, up to the first that reaches n bytes.
func TestRecords(t *testing.T) {
	const n = 4 << 10
	head := Records(5, lineRecord, n)
	stream := readAll(t, NewGenerator(5, lineRecord, WithBudget(64<<10)))
	if len(head) < n {
		t.Fatalf("Records returned %d bytes, want at least %d", len(head), n)
	}
	if !bytes.HasPrefix(stream, head) {
		t.Fatal("Records is not a prefix of the generated stream of the same seed")
	}
	if last := bytes.LastIndexByte(head[:len(head)-1], '\n'); last+1 >= n {
		t.Fatalf("Records holds %d bytes before its last record, want fewer than %d", last+1, n)
	}
}

// TestPrefix checks that a prefixed Source reads its heads, then the stream,
// and that its budget counts the heads.
func TestPrefix(t *testing.T) {
	gen := NewGenerator(1, lineRecord, WithBudget(16<<10))
	clean := readAll(t, NewGenerator(1, lineRecord, WithBudget(16<<10)))
	src := Prefix(gen, []byte("a=b\n"), nil, []byte("c=d\n"))
	if got, want := src.Budget(), int64(8+16<<10); got != want {
		t.Fatalf("budget is %d, want %d", got, want)
	}
	got := readAll(t, src)
	if want := append([]byte("a=b\nc=d\n"), clean...); !bytes.Equal(got, want) {
		t.Fatal("the prefixed stream is not the heads followed by the generated stream")
	}
	if src := Prefix(NewGenerator(1, lineRecord, WithBudget(16<<10))); src.Budget() != 16<<10 {
		t.Fatalf("budget with no head is %d, want %d", src.Budget(), 16<<10)
	}
}

// fatalTB records the message of the first Fatalf and stops the goroutine
// that called it, as testing.T does, so a test can check that a helper fails.
type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// fails runs fn with a fatalTB on its own goroutine and returns the message of
// its Fatalf, or "" if fn returned.
func fails(t *testing.T, fn func(testing.TB)) string {
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(tb)
	}()
	<-done
	return tb.msg
}

// TestLexAll checks that LexAll returns every token in order and fails on an
// error other than io.EOF.
func TestLexAll(t *testing.T) {
	ls := LexAll(t, newLineScanner(strings.NewReader("a=b c\n")))
	var got []string
	for _, l := range ls {
		got = append(got, string(l.Type())+":"+l.Text())
	}
	if want := "WORD:a EQUALS:= WORD:b SPACE:  WORD:c NEWLINE:\n"; strings.Join(got, " ") != want {
		t.Fatalf("tokens = %q, want %q", strings.Join(got, " "), want)
	}

	guarded := bytescan.NewWithOptions(strings.NewReader("a=b\n"+strings.Repeat("x", 2*TextGuard)), bytescan.WithMaxTokenBytes(TextGuard))
	guarded.MustAddRule("ANY", core.RuneRunMax(core.AnyRune, 2*TextGuard))
	msg := fails(t, func(tb testing.TB) { LexAll(tb, guarded) })
	if !strings.Contains(msg, "after 0 tokens") {
		t.Fatalf("LexAll over a scanner that fails: got %q, want a failure after 0 tokens", msg)
	}
}

// TestLexRetained proves the release check: over a cursor that retains 1 KiB,
// a scanner whose idle second scanner is released lexes a 64 KiB stream, and the
// same scanner without the Release fails with the retention error.
func TestLexRetained(t *testing.T) {
	input := readAll(t, NewGenerator(1, lineRecord, WithBudget(64<<10)))
	released := func(cur bytescan.Cursor) *twoScanners { return newTwoScannersOn(cur, true) }
	leaky := func(cur bytescan.Cursor) *twoScanners { return newTwoScannersOn(cur, false) }

	if ls := LexRetained(t, bufio.NewReader(bytes.NewReader(input)), 1<<10, released); len(ls) == 0 {
		t.Fatal("no tokens")
	}
	msg := fails(t, func(tb testing.TB) {
		LexRetained(tb, bufio.NewReader(bytes.NewReader(input)), 1<<10, leaky)
	})
	if !strings.Contains(msg, textreader.ErrRetentionExceeded.Error()) {
		t.Fatalf("LexRetained without the Release: got %q, want the retention error", msg)
	}
}
