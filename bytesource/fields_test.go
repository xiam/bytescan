package bytesource

import (
	"bytes"
	"errors"
	"io"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
)

// TestFieldsNotSource checks that *Fields does not have the method set of
// bytescan.Source: an empty queue returns a nil token with a nil error,
// which a Source must never do.
func TestFieldsNotSource(t *testing.T) {
	{
		_, ok := any((*Fields)(nil)).(bytescan.Source)
		require.False(t, ok, "*Fields satisfies bytescan.Source; its step method must not be named Next")
	}
	f := NewFields(NewCursor(strings.NewReader("abc")))
	for i := 0; i < 2; i++ {
		{
			l, err := f.NextField()
			require.Falsef(t, l != nil || err != nil, "empty queue: NextField = %v, %v; want nil, nil", l, err)
		}
	}
}

// step is one result of NextField: the hex line of its token, or "nil".
func step(t *testing.T, f *Fields) string {
	t.Helper()
	l, err := f.NextField()
	if err != nil {
		require.NoErrorf(t, err, "NextField: %v", err)
	}
	if l == nil {
		return "nil"
	}
	return Hex(l)
}

// steps calls NextField until it returns a nil token and returns the hex
// line of each token.
func steps(t *testing.T, f *Fields) []string {
	t.Helper()
	var got []string
	for {
		s := step(t, f)
		if s == "nil" {
			return got
		}
		got = append(got, s)
	}
}

func TestFieldsChunked(t *testing.T) {
	f := NewFields(NewCursor(strings.NewReader("abcdefghijXY")))
	var events []string
	returned := 0
	f.Push(
		Field{Type: "MSG", Len: 10, MaxPiece: 4, Done: func() {
			events = append(events, "done after "+string(rune('0'+returned)))
			f.Push(Field{Type: "TAIL", Len: 1})
		}},
		Field{Type: "NEXT", Len: 1},
	)
	var got []string
	for {
		s := step(t, f)
		if s == "nil" {
			break
		}
		returned++
		got = append(got, s)
	}
	want := []string{
		"0 MSG 4 61 62 63 64",
		"4 MSG 4 65 66 67 68",
		"8 MSG 2 69 6a",
		"10 NEXT 1 58",
		"11 TAIL 1 59",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		assert.Failf(t, "unexpected test state", "tokens\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Done is called once, after two pieces were returned and before the
	// third, the last one, was; the field it pushed comes after NEXT.
	assert.Truef(t, strings.Join(events, "|") == "done after 2", "Done calls = %q, want [done after 2]", events)
}

func TestFieldsChunkedExactPieces(t *testing.T) {
	f := NewFields(NewCursor(strings.NewReader("abcdefgh")))
	done := 0
	f.Push(Field{Type: "MSG", Len: 8, MaxPiece: 4, Done: func() { done++ }})
	got := steps(t, f)
	assert.Falsef(t, strings.Join(got, "|") != "0 MSG 4 61 62 63 64|4 MSG 4 65 66 67 68" || done != 1, "tokens %q, Done %d times; want two pieces of 4 and Done once", got, done)
}

func TestFieldsZeroLength(t *testing.T) {
	f := NewFields(NewCursor(strings.NewReader("ab")))
	var calls []string
	f.Push(
		Field{Type: "Z", Len: 0, MaxPiece: 4, Done: func() { calls = append(calls, "zero") }},
		Field{Type: "N", Len: -3, MaxPiece: 4, Done: func() { calls = append(calls, "negative") }},
		Field{Type: "U", Len: 0, Then: func(b []byte) { calls = append(calls, "then "+string(b)) }, Done: func() { calls = append(calls, "udone") }},
		Field{Type: "A", Len: 2, MaxPiece: 4},
	)
	got := steps(t, f)
	assert.Truef(t, strings.Join(got, "|") == "0 A 2 61 62", "tokens = %q, want only A", got)
	assert.Truef(t, strings.Join(calls, "|") == "zero|negative|then |udone", "callbacks = %q", calls)
}

// TestFieldsChunkedTruncated covers a chunked field cut by the end of the
// input inside a piece: the bytes read so far are the last piece, Done is not
// called and the rest of the queue is dropped.
func TestFieldsChunkedTruncated(t *testing.T) {
	f := NewFields(NewCursor(strings.NewReader("abcdef")))
	done := false
	f.Push(
		Field{Type: "MSG", Len: 10, MaxPiece: 4, Done: func() { done = true }},
		Field{Type: "NEXT", Len: 1, Done: func() { done = true }},
	)
	got := steps(t, f)
	assert.Truef(t, strings.Join(got, "|") == "0 MSG 4 61 62 63 64|4 MSG 2 65 66", "tokens = %q", got)
	assert.False(t, done, "Done was called for a truncated field or a dropped one")
	{
		s := step(t, f)
		assert.Truef(t, s == "nil", "after the queue was dropped: %s, want nil", s)
	}
}

// TestFieldsChunkedEndAtBoundary covers input that ends exactly where a piece
// would start: the next step returns a nil token and drops the queue.
func TestFieldsChunkedEndAtBoundary(t *testing.T) {
	f := NewFields(NewCursor(strings.NewReader("abcd")))
	done := false
	f.Push(
		Field{Type: "MSG", Len: 10, MaxPiece: 4, Done: func() { done = true }},
		Field{Type: "NEXT", Len: 1},
	)
	{
		s := step(t, f)
		require.Truef(t, s == "0 MSG 4 61 62 63 64", "first piece = %s", s)
	}
	{
		s := step(t, f)
		require.Truef(t, s == "nil", "at the end of the input: %s, want nil", s)
	}
	assert.False(t, done, "Done was called for a truncated field")
	assert.Truef(t, len(f.queue)-f.head == 0, "queue holds %d fields after the end of the input, want 0", len(f.queue)-f.head)
	// A field pushed after the drop is read from where the input is: at its
	// end, so it gives nothing.
	f.Push(Field{Type: "LATE", Len: 1, MaxPiece: 4})
	{
		s := step(t, f)
		assert.Truef(t, s == "nil", "late field = %s, want nil", s)
	}
}

// TestFieldsHugeDeclaredLength reads a field that declares 2^31 bytes over a
// short stream, chunked and unchunked: the field ends with the input and
// nothing is allocated for the declared length.
func TestFieldsHugeDeclaredLength(t *testing.T) {
	// 2^31 does not fit an int on a 32-bit platform; there the test uses the
	// largest int, 2^31-1, instead.
	declared := int64(1) << 31
	huge := int(min(declared, int64(math.MaxInt)))
	for _, piece := range []int{0, 64 << 10} {
		f := NewFields(NewCursor(strings.NewReader("abc")))
		f.Push(Field{Type: "BIG", Len: huge, MaxPiece: piece}, Field{Type: "NEXT", Len: 1})
		var allocs uint64
		var got []string
		func() {
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			got = steps(t, f)
			runtime.ReadMemStats(&after)
			allocs = after.TotalAlloc - before.TotalAlloc
		}()
		assert.Truef(t, strings.Join(got, "|") == "0 BIG 3 61 62 63", "MaxPiece %d: tokens = %q", piece, got)
		assert.Falsef(t, allocs > 1<<20, "MaxPiece %d: %d bytes allocated for a 3-byte field", piece, allocs)
	}
}

// TestFieldsChunkedThen checks that a field with both MaxPiece and Then is an
// error when its turn comes, and that the step reads nothing.
func TestFieldsChunkedThen(t *testing.T) {
	cur := NewCursor(strings.NewReader("abcdef"))
	f := NewFields(cur)
	called := false
	f.Push(
		Field{Type: "A", Len: 1},
		Field{Type: "BAD", Len: 4, MaxPiece: 2, Then: func([]byte) { called = true }},
	)
	{
		s := step(t, f)
		require.Truef(t, s == "0 A 1 61", "first field = %s", s)
	}
	for i := 0; i < 2; i++ {
		l, err := f.NextField()
		require.Falsef(t, !errors.Is(err, ErrChunkedThen) || l != nil, "NextField = %v, %v; want nil, ErrChunkedThen", l, err)
	}
	assert.False(t, called, "Then was called")
	{
		p, _ := Peek(cur, 5)
		assert.Truef(t, string(p) == "bcdef", "the cursor moved: next bytes %q, want \"bcdef\"", p)
	}
}

// TestFieldsReuse checks that a queue that drains and fills again, as a
// driver does once per header, keeps its storage: the steady state allocates
// only the tokens.
func TestFieldsReuse(t *testing.T) {
	in := bytes.Repeat([]byte("ab"), 1000)
	f := NewFields(NewCursor(bytes.NewReader(in)))
	run := func() {
		f.Push(Field{Type: "A", Len: 1}, Field{Type: "B", Len: 1, MaxPiece: 4})
		for {
			l, err := f.NextField()
			require.NoError(t, err)
			if l == nil {
				return
			}
		}
	}
	run()
	queueCap := cap(f.queue)
	for i := 0; i < 100; i++ {
		run()
	}
	assert.Truef(t, cap(f.queue) == queueCap, "queue storage grew from %d to %d fields", queueCap, cap(f.queue))
}

// TestFieldsPushWhileQueued checks that fields pushed while others wait are
// read in order, also after the storage is compacted.
func TestFieldsPushWhileQueued(t *testing.T) {
	f := NewFields(NewCursor(strings.NewReader("abcdefghij")))
	f.Push(Field{Type: "1", Len: 1}, Field{Type: "2", Len: 1})
	var got []string
	for _, typ := range []bytescan.TokenType{"3", "4", "5", "6", "7", "8", "9", "10"} {
		got = append(got, step(t, f))
		f.Push(Field{Type: typ, Len: 1})
	}
	got = append(got, steps(t, f)...)
	want := "0 1 1 61|1 2 1 62|2 3 1 63|3 4 1 64|4 5 1 65|5 6 1 66|6 7 1 67|7 8 1 68|8 9 1 69|9 10 1 6a"
	if strings.Join(got, "|") != want {
		assert.Failf(t, "unexpected test state", "order = %q", strings.Join(got, "|"))
	}
}

// pattern is an endless reader of the bytes 0x00 to 0xFF in order.
type pattern struct{ n byte }

func (p *pattern) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = p.n
		p.n++
	}
	return len(b), nil
}

// TestFieldsChunkedMemory reads a chunked field of 64 MiB (8 MiB under the
// race detector) and checks that the live heap stays within a small multiple
// of the piece size: the queue holds one piece, not the field.
func TestFieldsChunkedMemory(t *testing.T) {
	const piece = 64 << 10
	size := 64 << 20
	if raceDetector {
		size = 8 << 20
	}
	if testing.Short() {
		size = 4 << 20
	}
	f := NewFields(NewCursor(io.LimitReader(&pattern{}, int64(size))))
	done := 0
	f.Push(Field{Type: "DATA", Len: size, MaxPiece: piece, Done: func() { done++ }})

	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc

	// A piece is held as runes of 4 bytes and as UTF-8 text of up to 2 bytes
	// per byte; the cursor buffers a little more.
	const ceiling = 16 * piece
	var read, peak uint64
	for i := 0; ; i++ {
		l, err := f.NextField()
		if err != nil {
			t.Fatal(err)
		}
		if l == nil {
			break
		}
		if l.Len() > piece {
			t.Fatalf("piece of %d bytes, want at most %d", l.Len(), piece)
		}
		read += uint64(l.Len())
		if i%32 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > base {
				peak = max(peak, ms.HeapAlloc-base)
			}
			if peak > 256<<20 {
				t.Fatalf("live heap %d bytes above the start after %d bytes", peak, read)
			}
		}
	}
	require.Falsef(t, read != uint64(size) || done != 1, "read %d bytes and called Done %d times, want %d bytes and once", read, done, size)
	assert.Falsef(t, peak > ceiling, "live heap rose %d bytes while reading %d bytes in pieces of %d, want at most %d", peak, size, piece, ceiling)
	t.Logf("read %d bytes in pieces of %d; live heap peak %d bytes above the start", size, piece, peak)
}
