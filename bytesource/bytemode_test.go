package bytesource

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/streamtest"
)

// highBytes returns the bytes 0x80 to 0xFF, repeated reps times.
func highBytes(reps int) []byte {
	b := make([]byte, 0, 128*reps)
	for i := 0; i < reps; i++ {
		for c := 0x80; c <= 0xFF; c++ {
			b = append(b, byte(c))
		}
	}
	return b
}

// lexBytes lexes in on a NewCursor cursor with the given rules and options
// and returns its tokens.
func lexBytes(t *testing.T, in []byte, rules func(*bytescan.Scanner), opts ...bytescan.Option) []*bytescan.Token {
	t.Helper()
	lx := bytescan.NewWithCursorOptions(NewCursor(bytes.NewReader(in)), opts...)
	rules(lx)
	return streamtest.LexAll(t, lx)
}

// TestHighBytePositions checks that on a stream of the bytes 0x80 to 0xFF
// every token of a NewCursor cursor reports its file offset in Offset,
// RuneOffset and ByteOffset, and its length in bytes in Len and Span.Bytes.
func TestHighBytePositions(t *testing.T) {
	in := highBytes(8)
	ls := lexBytes(t, in, func(lx *bytescan.Scanner) {
		// Runs of up to 7 bytes of 0x80 to 0x9F, and every other byte alone
		// (UNKNOWN), so the tokens have different lengths.
		lx.MustAddRule("RUN", core.RuneRunMax(core.RuneRange(0x80, 0x9F), 7))
	})
	off := 0
	runs := 0
	for i, l := range ls {
		n := len(Bytes(l.Text()))
		if l.Type() == "RUN" {
			runs++
		}
		if l.Offset() != off || l.RuneOffset() != off || l.ByteOffset() != off {
			require.FailNowf(t, "unexpected test state", "token %d (%s): Offset %d, RuneOffset %d, ByteOffset %d; want the file offset %d", i, l.Type(), l.Offset(), l.RuneOffset(), l.ByteOffset(), off)
		}
		if l.Len() != n || l.Span().Bytes() != n || l.Span().Runes() != n {
			require.FailNowf(t, "unexpected test state", "token %d (%s): Len %d, Span.Bytes %d, Span.Runes %d; want its length in bytes %d", i, l.Type(), l.Len(), l.Span().Bytes(), l.Span().Runes(), n)
		}
		if got := Bytes(l.Text()); !bytes.Equal(got, in[off:off+n]) {
			require.FailNowf(t, "unexpected test state", "token %d (%s): bytes % x, want % x", i, l.Type(), got, in[off:off+n])
		}
		off += n
	}
	require.Truef(t, off == len(in), "the tokens cover %d bytes, want %d", off, len(in))
	require.False(t, runs == 0, "no RUN token: the test does not cover tokens longer than one byte")
}

// TestHighByteGuard checks that WithMaxTokenBytes counts one for each byte of
// a NewCursor cursor: a token of n bytes of 0x80 to 0xFF passes a guard of n
// and fails a guard of n-1 with an error that wraps cursor.ErrRetentionExceeded.
func TestHighByteGuard(t *testing.T) {
	const n = 500
	in := highBytes(4)[:n]
	rules := func(lx *bytescan.Scanner) {
		lx.MustAddRule("HIGH", core.RuneRunMax(core.RuneRange(0x80, 0xFF), 1000))
	}

	ls := lexBytes(t, in, rules, bytescan.WithMaxTokenBytes(n))
	require.Falsef(t, len(ls) != 1 || ls[0].Type() != "HIGH" || ls[0].Len() != n || ls[0].Span().Bytes() != n, "with a guard of %d: got %d tokens, want one HIGH token of %d bytes", n, len(ls), n)

	lx := bytescan.NewWithCursorOptions(NewCursor(bytes.NewReader(in)), bytescan.WithMaxTokenBytes(n-1))
	rules(lx)
	_, err := lx.Next()
	require.ErrorIsf(t, err, cursor.ErrRetentionExceeded, "with a guard of %d: err = %v, want an error that wraps cursor.ErrRetentionExceeded", n-1, err)
	require.ErrorIsf(t, err, bytescan.ErrTokenTooLarge, "with a guard of %d: err = %v, want an error that wraps bytescan.ErrTokenTooLarge", n-1, err)
}

// TestByteLineColumn checks that Line and Column of a NewCursor cursor advance
// on the byte 0x0A only and count bytes, also after bytes of 0x80 and above.
func TestByteLineColumn(t *testing.T) {
	in := []byte{0x80, 0xFF, 0x0A, 0xC3, 0xA9, 0x0D, 0x0A, 0x0A, 0xE2, 0x82, 0xAC, 0x85}
	ls := lexBytes(t, in, func(lx *bytescan.Scanner) { lx.MustAddRule("BYTE", core.Rune(AnyByte)) })
	type pos struct{ line, column int }
	want := []pos{
		{1, 0}, {1, 1}, {1, 2}, // 80 FF 0A
		{2, 0}, {2, 1}, {2, 2}, {2, 3}, // C3 A9 0D 0A: valid UTF-8 é still counts two bytes
		{3, 0},                         // 0A
		{4, 0}, {4, 1}, {4, 2}, {4, 3}, // E2 82 AC 85: U+0085 (NEL) does not end a line
	}
	require.Truef(t, len(ls) == len(want), "got %d tokens, want one per byte (%d)", len(ls), len(want))
	for i, l := range ls {
		if got := (pos{l.Line(), l.Column()}); got != want[i] {
			assert.Failf(t, "unexpected test state", "byte %d (%02x): line %d, column %d; want line %d, column %d", i, in[i], got.line, got.column, want[i].line, want[i].column)
		}
	}
}

// TestByteContext checks that Scanner.Context on a NewCursor cursor returns the
// raw input bytes around a token, counted in bytes, and that Text of them
// compares with the token text.
func TestByteContext(t *testing.T) {
	in := []byte{'A', 0xFF, 0x80, 'B'}
	lx := bytescan.NewWithCursor(NewCursor(bytes.NewReader(in)))
	lx.MustAddRule("HIGH", core.RuneRunMax(core.RuneRange(0x80, 0xFF), 10))
	lx.MustAddRule("BYTE", core.Rune(AnyByte))
	var high *bytescan.Token
	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if l.Type() == "HIGH" {
			high = l
		}
	}
	require.False(t, high == nil, "no HIGH token")
	ctx, err := lx.Context(high, 0, 0)
	require.NoError(t, err)
	assert.Truef(t, ctx == "\xff\x80", "Context(tok, 0, 0) = %q, want the raw bytes \"\\xff\\x80\"", ctx)
	if Text(ctx) != high.Text() {
		assert.Failf(t, "unexpected test state", "Text(Context) = %q, want the token text %q", Text(ctx), high.Text())
	}
	ctx, err = lx.Context(high, 1, 1)
	require.NoError(t, err)
	{
		want := string(in)
		assert.Truef(t, ctx == want, "Context(tok, 1, 1) = %q, want %q", ctx, want)
	}
}

// lenField is a driver for a stream of one 1-byte length, in units of 256
// bytes, then that many bytes of data read by Read. When release is false the
// driver does not release its scanner before it reads the field, so the
// scanner's checkpoint pins the field.
type lenField struct {
	cur     bytescan.Cursor
	lx      *bytescan.Scanner
	release bool
	field   int
}

func newLenField(cur bytescan.Cursor, release bool) *lenField {
	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule("LEN", core.Rune(AnyByte))
	return &lenField{cur: cur, lx: lx, release: release}
}

func (d *lenField) Next() (*bytescan.Token, error) {
	if d.field > 0 {
		n := d.field
		d.field = 0
		return Read(d.cur, "DATA", n)
	}
	l, err := d.lx.Next()
	if err != nil {
		return nil, err
	}
	if d.release {
		d.lx.Release()
	}
	d.field = 256 * int(Bytes(l.Text())[0])
	return l, nil
}

// TestLexRetainedBytes checks that LexRetainedBytes passes with a retention
// limit of n for a field of n bytes of 0x80 to 0xFF that a released driver
// reads, and fails when the driver does not release its scanner.
func TestLexRetainedBytes(t *testing.T) {
	const n = 16 * 256
	in := append([]byte{n / 256}, highBytes(n/128)...)
	released := func(cur bytescan.Cursor) *lenField { return newLenField(cur, true) }
	leaky := func(cur bytescan.Cursor) *lenField { return newLenField(cur, false) }

	ls := streamtest.LexRetainedBytes(t, bytes.NewReader(in), n, released)
	require.Falsef(t, len(ls) != 2 || ls[1].Type() != "DATA" || ls[1].Len() != n || ls[1].Span().Bytes() != n || ls[1].ByteOffset() != 1, "released driver: got %d tokens, want LEN and one DATA token of %d bytes at byte offset 1", len(ls), n)

	msg := fails(t, func(tb testing.TB) {
		streamtest.LexRetainedBytes(tb, bytes.NewReader(in), n, leaky)
	})
	require.Truef(t, strings.Contains(msg, cursor.ErrRetentionExceeded.Error()), "LexRetainedBytes without the Release: got %q, want the retention error", msg)
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
