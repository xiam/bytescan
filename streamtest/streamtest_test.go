package streamtest

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
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
	return newTwoScannersOn(cursor.NewReader(r), release)
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

// defaultGenerator returns a generator of lineRecord with the default budget
// of a run without -streamtest.long, as if built without WithBudget, so that
// Run applies the defaults of an implicit budget. Under -streamtest.long it
// keeps that budget instead of LongBudget.
func defaultGenerator(seed int64) *Generator {
	g := NewGenerator(seed, lineRecord, WithBudget(defaultBudget))
	g.explicitBudget = false
	return g
}

// withBaselineAfter sets the token progress before the baseline sample, as
// the race detector does for a stream with the default budget.
func withBaselineAfter(n int64) Option {
	return func(c *config) {
		c.baselineAfter = n
	}
}

// samplePoint is where Run took a heap sample: the offset of the last token
// and the bytes read from the source.
type samplePoint struct {
	pos, read int64
}

// withSamples records every heap sample of a run in dst.
func withSamples(dst *[]samplePoint) Option {
	return func(c *config) {
		c.onSample = func(pos, read int64) {
			*dst = append(*dst, samplePoint{pos, read})
		}
	}
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return b
}

// TestGenerator checks that a seed gives the same stream, that the stream
// reaches its budget and ends at a record boundary, and that reads of any size
// see the same bytes.
func TestGenerator(t *testing.T) {
	const budget = 64 << 10
	a := NewGenerator(1, lineRecord, WithBudget(budget))
	got := readAll(t, a)

	require.Falsef(t, int64(len(got)) <= budget, "stream is %d bytes, want more than the budget %d", len(got), budget)
	if got[len(got)-1] != '\n' {
		require.FailNowf(t, "unexpected test state", "stream does not end at a record boundary: %q", got[len(got)-16:])
	}
	last := got[:len(got)-1]
	{
		n := len(got) - 1 - bytes.LastIndexByte(last, '\n')
		require.Falsef(t, int64(len(got)-n) > budget, "stream has %d bytes before its last record, want at most the budget %d", len(got)-n, budget)
	}
	if lines := bytes.Count(got, []byte("\n")); lines != a.Records() {
		require.FailNowf(t, "unexpected test state", "stream has %d lines, the generator reports %d records", lines, a.Records())
	}
	if a.Bytes() != int64(len(got)) {
		require.FailNowf(t, "unexpected test state", "generator reports %d bytes, read %d", a.Bytes(), len(got))
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
		require.NoError(t, err)
	}
	require.True(t, bytes.Equal(small.Bytes(), got), "a 1-byte reader read a different stream")

	other := readAll(t, NewGenerator(2, lineRecord, WithBudget(budget)))
	require.False(t, bytes.Equal(other, got), "seeds 1 and 2 gave the same stream")

	if d := NewGenerator(1, lineRecord); d.Budget() != DefaultBudget() {
		require.FailNowf(t, "unexpected test state", "default budget is %d, want %d", d.Budget(), DefaultBudget())
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
		require.FailNowf(t, "unexpected test state", "budget is %d, want %d", c.Budget(), budget)
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
		require.Falsef(t, in.Offset < prev, "injection %d at %d is before the end of the previous one at %d", n, in.Offset, prev)
		stripped = append(stripped, got[prev:in.Offset]...)
		run := got[in.Offset : in.Offset+int64(in.Len)]
		require.Truef(t, strings.Trim(string(run), garbage) == "", "injection %d at %d is %q, want bytes of %q", n, in.Offset, run, garbage)
		rest := string(got[in.Offset+int64(in.Len):])
		if !strings.HasPrefix(rest, cleanLines[in.Record]) {
			require.FailNowf(t, "unexpected test state", "injection %d is not followed by record %d %q", n, in.Record, cleanLines[in.Record])
		}
		prev = in.Offset + int64(in.Len)
	}
	stripped = append(stripped, got[prev:]...)

	if n == 0 || n != c.Injected() {
		require.FailNowf(t, "unexpected test state", "NextInjection returned %d runs, Injected reports %d", n, c.Injected())
	}
	require.True(t, bytes.Equal(stripped, clean), "the corrupted stream without its garbage runs is not the clean stream")
	t.Logf("%d garbage runs in %d records", n, len(cleanLines)-1)
}

// TestRunLong lexes a clean stream of the default budget, or of 1 GiB with
// -streamtest.long, and checks the token counts against the records.
func TestRunLong(t *testing.T) {
	gen := NewGenerator(1, lineRecord)
	res := Run(t, newLineScanner, gen)
	if got := res.Counts["NEWLINE"]; got != int64(gen.Records()) {
		require.FailNowf(t, "unexpected test state", "lexed %d NEWLINE, want one per record (%d)", got, gen.Records())
	}
	if got := res.Counts["EQUALS"]; got != int64(gen.Records()) {
		require.FailNowf(t, "unexpected test state", "lexed %d EQUALS, want one per record (%d)", got, gen.Records())
	}
	{
		got := res.Counts[bytescan.TokenTypeUnknown]
		require.Truef(t, got == 0, "lexed %d UNKNOWN in a clean stream", got)
	}
	if res.Bytes != gen.Bytes() {
		require.FailNowf(t, "unexpected test state", "Run read %d bytes, the generator wrote %d", res.Bytes, gen.Bytes())
	}
	require.Falsef(t, res.Samples < 2, "took %d heap samples, want at least 2", res.Samples)
}

// TestLeakFails proves that the ceiling catches growth: a scanner whose idle
// second scanner is never released pins the stream and fails at the default
// budget, ceiling and baseline.
func TestLeakFails(t *testing.T) {
	_, err := run(newLeakyScanner, defaultGenerator(1))
	require.ErrorIsf(t, err, ErrCeiling, "got error %v, want ErrCeiling", err)
	t.Log(err)
}

// TestLeakReleased is TestLeakFails with the idle scanner released: the same
// stream stays under the ceiling.
func TestLeakReleased(t *testing.T) {
	Run(t, newReleasedScanner, defaultGenerator(1))
}

// TestRunCorrupt lexes a corrupted stream and checks, as the tokens arrive,
// that each garbage run lexes as UNKNOWN tokens and that the next token is
// the first WORD of the record that follows it.
func TestRunCorrupt(t *testing.T) {
	c := Corrupt(defaultGenerator(3), 4, 0.05, Garbage(garbage, 1, 16))

	var (
		cur     Injection
		have    bool
		unknown int64
	)
	check := func(l *bytescan.Token) {
		off := int64(l.ByteOffset())
		if !have || off >= cur.Offset+int64(cur.Len) {
			if have && off == cur.Offset+int64(cur.Len) && l.Type() != "WORD" {
				require.FailNowf(t, "unexpected test state", "after the garbage at %d: got %s %q, want WORD", cur.Offset, l.Type(), l.Text())
			}
			cur, have = c.NextInjection()
		}
		inside := have && off >= cur.Offset && off < cur.Offset+int64(cur.Len)
		if inside != (l.Type() == bytescan.TokenTypeUnknown) {
			require.FailNowf(t, "unexpected test state", "at %d: got %s %q, garbage run here: %v", off, l.Type(), l.Text(), inside)
		}
		if inside {
			unknown++
		}
	}
	// The garbage runs are read ahead of the tokens, so the callback can
	// always find the run that a token falls in.
	res := Run(t, newLineScanner, c, WithTokenFunc(check))
	if res.Counts[bytescan.TokenTypeUnknown] != unknown || unknown == 0 {
		require.FailNowf(t, "unexpected test state", "lexed %d UNKNOWN, %d inside garbage runs", res.Counts[bytescan.TokenTypeUnknown], unknown)
	}
	t.Logf("%d garbage runs, %d UNKNOWN tokens", c.Injected(), unknown)
}

// TestTooFewSamples checks that a sampling interval longer than the stream
// fails the run instead of passing without a heap check.
func TestTooFewSamples(t *testing.T) {
	const budget = 16 << 10
	_, err := run(newLineScanner, NewGenerator(1, lineRecord, WithBudget(budget)), WithSampleEvery(4*budget))
	require.Falsef(t, err == nil || !strings.Contains(err.Error(), "no growth was checked"), "got error %v, want the too-few-samples error", err)
}

// TestCeilingContract checks the default ceiling: DefaultCeiling keeps its
// formula for every budget, and only under the race detector does a stream
// with the implicit default budget and no WithCeiling get 48 KiB.
func TestCeilingContract(t *testing.T) {
	for _, c := range []struct{ budget, want int64 }{
		{1, 48 << 10},
		{64 << 10, 48 << 10},
		{96 << 10, 48 << 10},
		{192 << 10, 96 << 10},
		{256 << 10, 128 << 10},
		{512 << 10, 256 << 10},
		{LongBudget, 16 << 20},
	} {
		{
			got := DefaultCeiling(c.budget)
			assert.Truef(t, got == c.want, "DefaultCeiling(%d) = %d, want %d", c.budget, got, c.want)
		}
	}

	wantBudget, implicit := int64(512<<10), int64(256<<10)
	if raceDetector {
		wantBudget, implicit = 256<<10, 48<<10
	}
	require.Truef(t, defaultBudget == wantBudget, "default budget is %d, want %d", defaultBudget, wantBudget)
	if !Long() && DefaultCeiling(DefaultBudget()) != DefaultCeiling(wantBudget) {
		assert.Failf(t, "unexpected test state", "DefaultCeiling(DefaultBudget()) = %d, want the formula value %d", DefaultCeiling(DefaultBudget()), DefaultCeiling(wantBudget))
	}

	// An implicit budget gets 48 KiB under the race detector and the formula
	// value of its budget without it; an explicit one always gets the formula.
	head := []byte("a=b\n")
	for _, c := range []struct {
		name     string
		src      Source
		implicit bool
	}{
		{"implicit", defaultGenerator(1), true},
		{"corrupted implicit", Corrupt(defaultGenerator(1), 2, 0.1, Garbage(garbage, 1, 4)), true},
		{"prefixed implicit", Prefix(defaultGenerator(1), head), true},
		{"explicit, equal to the default", NewGenerator(1, lineRecord, WithBudget(defaultBudget)), false},
		{"prefixed explicit", Prefix(NewGenerator(1, lineRecord, WithBudget(defaultBudget)), head), false},
		{"explicit 64 KiB", NewGenerator(1, lineRecord, WithBudget(64<<10)), false},
		{"explicit 1 MiB", NewGenerator(1, lineRecord, WithBudget(1<<20)), false},
	} {
		want, wantAfter := DefaultCeiling(c.src.Budget()), int64(0)
		if raceDetector && c.implicit {
			want, wantAfter = 48<<10, cursorBuffer
		}
		{
			got := defaultCeiling(c.src)
			assert.Truef(t, got == want, "%s: default ceiling %d, want %d", c.name, got, want)
		}
		{
			got := defaultBaselineAfter(c.src)
			assert.Truef(t, got == wantAfter, "%s: baseline after %d bytes, want %d", c.name, got, wantAfter)
		}
	}

	// The long run keeps the formula ceiling and the first-sample baseline.
	saved := *long
	*long = true
	g := NewGenerator(1, lineRecord)
	*long = saved
	if g.Budget() != LongBudget || defaultCeiling(g) != 16<<20 || defaultBaselineAfter(g) != 0 {
		assert.Failf(t, "unexpected test state", "long run: budget %d, ceiling %d, baseline after %d; want %d, %d, 0", g.Budget(), defaultCeiling(g), defaultBaselineAfter(g), LongBudget, 16<<20)
	}

	// Run enforces the implicit default, and its baseline waits for the first
	// cursor buffer under the race detector only. The leaky scanner stops the
	// run at its first sample after the baseline, so the check is short.
	var samples []samplePoint
	res, err := run(newLeakyScanner, defaultGenerator(1), withSamples(&samples))
	require.ErrorIsf(t, err, ErrCeiling, "got error %v, want ErrCeiling", err)
	assert.Truef(t, res.Ceiling == implicit, "Run enforced a ceiling of %d, want %d", res.Ceiling, implicit)
	{
		first := samples[0].pos
		assert.Truef(t, (first >= cursorBuffer) == raceDetector, "baseline at %d bytes of tokens; race detector: %v", first, raceDetector)
	}
	res, _ = run(newLeakyScanner, defaultGenerator(1), WithCeiling(1<<10))
	assert.Truef(t, res.Ceiling == 1<<10, "WithCeiling(1 KiB): Run enforced a ceiling of %d", res.Ceiling)
}

// TestBaselineRule checks the moved baseline: it is the first sample after
// the tokens, not the bytes read, have passed the boundary; it follows a
// custom sampling interval; and a stream too short to give a sample after it
// fails the run.
func TestBaselineRule(t *testing.T) {
	const budget = 96 << 10
	// A large reader buffer reads far ahead of the tokens.
	newAheadScanner := func(r io.Reader) *bytescan.Scanner {
		lx := bytescan.NewWithOptions(bufio.NewReaderSize(r, 2*cursorBuffer), bytescan.WithMaxTokenBytes(TextGuard))
		addLineRules(lx)
		return lx
	}

	var early []samplePoint
	{
		_, err := run(newAheadScanner, NewGenerator(1, lineRecord, WithBudget(budget)), WithCeiling(1<<30), withSamples(&early))
		require.NoError(t, err)
	}
	if early[0].pos >= cursorBuffer || early[0].read < cursorBuffer {
		require.FailNowf(t, "unexpected test state", "without the rule, the first sample is at %d bytes of tokens and %d bytes read; want the reader past %d bytes before the tokens", early[0].pos, early[0].read, cursorBuffer)
	}

	var moved []samplePoint
	{
		_, err := run(newAheadScanner, NewGenerator(1, lineRecord, WithBudget(budget)), WithCeiling(1<<30), withBaselineAfter(cursorBuffer), withSamples(&moved))
		require.NoError(t, err)
	}
	every := int64(budget / 16)
	{
		first := moved[0].pos
		require.Falsef(t, first < cursorBuffer || first >= cursorBuffer+every, "baseline at %d bytes of tokens, want the first sample at or after %d, before %d", first, cursorBuffer, cursorBuffer+every)
	}
	require.Falsef(t, len(moved) < 2 || len(moved) >= len(early), "took %d samples with the rule and %d without", len(moved), len(early))

	// A custom interval: the baseline is the first sample of that interval
	// after the boundary, and the samples keep the interval.
	const interval = 5000
	var custom []samplePoint
	{
		_, err := run(newLineScanner, NewGenerator(1, lineRecord, WithBudget(budget)), WithCeiling(1<<30), WithSampleEvery(interval), withBaselineAfter(cursorBuffer), withSamples(&custom))
		require.NoError(t, err)
	}
	{
		first := custom[0].pos
		require.Falsef(t, first < cursorBuffer || first >= cursorBuffer+interval, "with a %d-byte interval, baseline at %d bytes of tokens, want from %d to %d", interval, first, cursorBuffer, cursorBuffer+interval)
	}
	for i := 1; i < len(custom)-1; i++ {
		{
			gap := custom[i].pos - custom[i-1].pos
			require.Falsef(t, gap <= 0 || gap >= 2*interval, "samples %d and %d are %d bytes of tokens apart, want about %d", i-1, i, gap, interval)
		}
	}

	// Too short: a stream that ends before the boundary, and one with no
	// sampling interval after it, fail instead of passing unchecked.
	for _, c := range []struct {
		name  string
		src   Source
		every int64
	}{
		{"ends before the boundary", NewGenerator(1, lineRecord, WithBudget(32<<10)), 0},
		{"no interval after the boundary", NewGenerator(1, lineRecord, WithBudget(96<<10)), 1 << 20},
	} {
		_, err := run(newLineScanner, c.src, WithSampleEvery(c.every), withBaselineAfter(cursorBuffer))
		require.Falsef(t, err == nil || !strings.Contains(err.Error(), "no growth was checked") || !strings.Contains(err.Error(), "one cursor buffer"), "%s: got error %v, want the baseline error", c.name, err)
	}
}

// TestRecords checks that Records returns the first records of the stream of
// the same seed, up to the first that reaches n bytes.
func TestRecords(t *testing.T) {
	const n = 4 << 10
	head := Records(5, lineRecord, n)
	stream := readAll(t, NewGenerator(5, lineRecord, WithBudget(64<<10)))
	require.Falsef(t, len(head) < n, "Records returned %d bytes, want at least %d", len(head), n)
	require.True(t, bytes.HasPrefix(stream, head), "Records is not a prefix of the generated stream of the same seed")
	{
		last := bytes.LastIndexByte(head[:len(head)-1], '\n')
		require.Falsef(t, last+1 >= n, "Records holds %d bytes before its last record, want fewer than %d", last+1, n)
	}
}

// TestPrefix checks that a prefixed Source reads its heads, then the stream,
// and that its budget counts the heads.
func TestPrefix(t *testing.T) {
	gen := NewGenerator(1, lineRecord, WithBudget(16<<10))
	clean := readAll(t, NewGenerator(1, lineRecord, WithBudget(16<<10)))
	src := Prefix(gen, []byte("a=b\n"), nil, []byte("c=d\n"))
	{
		got, want := src.Budget(), int64(8+16<<10)
		require.Truef(t, got == want, "budget is %d, want %d", got, want)
	}
	got := readAll(t, src)
	{
		want := append([]byte("a=b\nc=d\n"), clean...)
		require.True(t, bytes.Equal(got, want), "the prefixed stream is not the heads followed by the generated stream")
	}
	if src := Prefix(NewGenerator(1, lineRecord, WithBudget(16<<10))); src.Budget() != 16<<10 {
		require.FailNowf(t, "unexpected test state", "budget with no head is %d, want %d", src.Budget(), 16<<10)
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
		require.FailNowf(t, "unexpected test state", "tokens = %q, want %q", strings.Join(got, " "), want)
	}

	guarded := bytescan.NewWithOptions(strings.NewReader("a=b\n"+strings.Repeat("x", 2*TextGuard)), bytescan.WithMaxTokenBytes(TextGuard))
	guarded.MustAddRule("ANY", core.RuneRunMax(core.AnyRune, 2*TextGuard))
	msg := fails(t, func(tb testing.TB) { LexAll(tb, guarded) })
	require.Truef(t, strings.Contains(msg, "after 0 tokens"), "LexAll over a scanner that fails: got %q, want a failure after 0 tokens", msg)
}

// TestLexRetained proves the release check: over a cursor that retains 1 KiB,
// a scanner whose idle second scanner is released lexes a 64 KiB stream, and the
// same scanner without the Release fails with the retention error.
func TestLexRetained(t *testing.T) {
	input := readAll(t, NewGenerator(1, lineRecord, WithBudget(64<<10)))
	released := func(cur bytescan.Cursor) *twoScanners { return newTwoScannersOn(cur, true) }
	leaky := func(cur bytescan.Cursor) *twoScanners { return newTwoScannersOn(cur, false) }

	{
		ls := LexRetained(t, bufio.NewReader(bytes.NewReader(input)), 1<<10, released)
		require.False(t, len(ls) == 0, "no tokens")
	}
	msg := fails(t, func(tb testing.TB) {
		LexRetained(tb, bufio.NewReader(bytes.NewReader(input)), 1<<10, leaky)
	})
	require.Truef(t, strings.Contains(msg, cursor.ErrRetentionExceeded.Error()), "LexRetained without the Release: got %q, want the retention error", msg)
}
