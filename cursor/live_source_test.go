package cursor_test

import (
	"io"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan/cursor"
)

// The tests in this file cover a live source: a source that has delivered some
// input and then blocks without an error or io.EOF. The cursor must return the
// runes that input holds without reading the source again (#116, #117).
//
// The tests do not use sleeps. Each live source reports on its blocked channel
// when the cursor asks it for input it does not have yet. Every result is sent
// before the next call into the cursor, so when the blocked signal arrives the
// results channel holds every rune the cursor returned before it blocked. A
// signal that arrives before the expected runes is the bug, and the test fails
// at once, with the count. The timeout only stops a test that hangs.

// liveTimeout bounds a test that would otherwise hang. A correct cursor never
// reaches it.
const liveTimeout = 10 * time.Second

// liveReader is a live io.Reader. Read returns the next chunk sent on chunks.
// When no chunk is ready, Read reports on blocked and then waits for one.
// Closing chunks ends the stream with io.EOF.
type liveReader struct {
	chunks  chan []byte
	blocked chan struct{}
	rest    []byte
}

func newLiveReader() *liveReader {
	return &liveReader{
		chunks:  make(chan []byte, 16),
		blocked: make(chan struct{}, 1),
	}
}

func (s *liveReader) Read(p []byte) (int, error) {
	if len(s.rest) == 0 {
		var (
			chunk []byte
			ok    bool
		)
		select {
		case chunk, ok = <-s.chunks:
		default:
			signal(s.blocked)
			chunk, ok = <-s.chunks
		}
		if !ok {
			return 0, io.EOF
		}
		s.rest = chunk
	}

	n := copy(p, s.rest)
	s.rest = s.rest[n:]

	return n, nil
}

// liveRunes is a live io.RuneReader. ReadRune returns the next rune sent on
// runes. When no rune is ready, ReadRune reports on blocked and then waits for
// one. Closing runes ends the stream with io.EOF. calls counts the ReadRune
// calls.
type liveRunes struct {
	runes   chan rune
	blocked chan struct{}
	calls   int
}

func newLiveRunes() *liveRunes {
	return &liveRunes{
		runes:   make(chan rune, 64),
		blocked: make(chan struct{}, 1),
	}
}

func (s *liveRunes) ReadRune() (rune, int, error) {
	s.calls++

	var (
		r  rune
		ok bool
	)
	select {
	case r, ok = <-s.runes:
	default:
		signal(s.blocked)
		r, ok = <-s.runes
	}
	if !ok {
		return 0, 0, io.EOF
	}

	return r, utf8.RuneLen(r), nil
}

// signal reports on ch without blocking the source.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// liveRune is one rune a cursor returned, with the position it was read from.
type liveRune struct {
	r    rune
	size int
	pos  cursor.Pos
}

func located(lr cursor.LocatedRune, err error) (liveRune, error) {
	return liveRune{r: lr.Rune(), size: lr.Size(), pos: lr.Pos()}, err
}

// liveRead is one rune read from a cursor on its own goroutine.
type liveRead struct {
	lr  liveRune
	err error
}

// readLive reads n runes with read on a new goroutine and returns them. It
// fails the test as soon as the source blocks before the n runes arrived, and
// reports how many did.
func readLive(t *testing.T, n int, blocked <-chan struct{}, read func() (liveRune, error)) []liveRune {
	t.Helper()

	results := make(chan liveRead, n)
	go func() {
		for i := 0; i < n; i++ {
			lr, err := read()
			results <- liveRead{lr: lr, err: err}
			if err != nil {
				return
			}
		}
	}()

	got := make([]liveRune, 0, n)
	timeout := time.After(liveTimeout)
	for len(got) < n {
		select {
		case res := <-results:
			require.NoError(t, res.err)
			got = append(got, res.lr)
		case <-blocked:
			// Every rune returned before the source blocked is already in
			// results.
			for len(results) > 0 {
				res := <-results
				require.NoError(t, res.err)
				got = append(got, res.lr)
			}
			require.Falsef(t, len(got) < n, "the source blocked after %d of %d runes: the cursor held back input that had arrived", len(got), n)
		case <-timeout:
			require.FailNowf(t, "unexpected test state", "timed out after %d of %d runes", len(got), n)
		}
	}

	return got
}

func readLocated(c *cursor.Reader) func() (liveRune, error) {
	return func() (liveRune, error) { return located(c.ReadLocatedRune()) }
}

func runesOf(lrs []liveRune) string {
	out := make([]rune, len(lrs))
	for i, lr := range lrs {
		out[i] = lr.r
	}
	return string(out)
}

// TestLiveReaderReturnsArrivedRunes covers R1: in text mode, a live io.Reader
// that delivered "say hello\n" yields all 10 runes. Before #117 the cursor asked
// for 4 buffered bytes per rune and returned 7.
func TestLiveReaderReturnsArrivedRunes(t *testing.T) {
	const msg = "say hello\n"

	reads := map[string]func(*cursor.Reader) func() (liveRune, error){
		"ReadRune": func(c *cursor.Reader) func() (liveRune, error) {
			return func() (liveRune, error) {
				pos := c.Cursor()
				r, size, err := c.ReadRune()
				return liveRune{r: r, size: size, pos: pos}, err
			}
		},
		"ReadLocatedRune": func(c *cursor.Reader) func() (liveRune, error) {
			return readLocated(c)
		},
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			src := newLiveReader()
			t.Cleanup(func() { close(src.chunks) })
			src.chunks <- []byte(msg)

			c := cursor.NewReader(src)
			got := readLive(t, utf8.RuneCountInString(msg), src.blocked, read(c))
			assert.Equal(t, msg, runesOf(got))

			last := got[len(got)-1]
			assert.Equal(t, 9, last.pos.ByteOffset())
			assert.Equal(t, 9, last.pos.RuneOffset())
		})
	}
}

// TestLiveReaderReturnsCompleteSequences covers R1 for the other complete
// sequences: a multi-byte rune that arrived whole, and an invalid byte, which
// decodes to utf8.RuneError of size 1.
func TestLiveReaderReturnsCompleteSequences(t *testing.T) {
	src := newLiveReader()
	t.Cleanup(func() { close(src.chunks) })
	src.chunks <- []byte("é\xffa")

	c := cursor.NewReader(src)
	got := readLive(t, 3, src.blocked, readLocated(c))

	assert.Equal(t, 'é', got[0].r)
	assert.Equal(t, 2, got[0].size)
	assert.Equal(t, utf8.RuneError, got[1].r)
	assert.Equal(t, 1, got[1].size)
	assert.Equal(t, 2, got[1].pos.ByteOffset())
	assert.Equal(t, 'a', got[2].r)
	assert.Equal(t, 3, got[2].pos.ByteOffset())
	assert.Equal(t, 2, got[2].pos.RuneOffset())
}

// TestLiveReaderWaitsForIncompleteSequence covers R2: a live io.Reader that
// delivered only the first byte of a 2-byte rune yields no rune until the
// second byte arrives.
//
// The check that no rune came out uses no timer: the source reports on blocked
// from inside Read, and the read call cannot return while Read blocks. The only
// bounded wait is liveTimeout, which stops a test that hangs.
func TestLiveReaderWaitsForIncompleteSequence(t *testing.T) {
	const msg = "aé"

	src := newLiveReader()
	t.Cleanup(func() { close(src.chunks) })
	src.chunks <- []byte(msg[:2])

	c := cursor.NewReader(src)
	got := readLive(t, 1, src.blocked, readLocated(c))
	require.Equal(t, "a", runesOf(got))

	results := make(chan liveRead, 1)
	go func() {
		lr, err := located(c.ReadLocatedRune())
		results <- liveRead{lr: lr, err: err}
	}()

	select {
	case <-src.blocked:
	case res := <-results:
		require.FailNowf(t, "unexpected test state", "the cursor returned %q from a proper prefix of a 2-byte sequence", res.lr.r)
	case <-time.After(liveTimeout):
		require.FailNow(t, "timed out waiting for the cursor to read the source")
	}
	require.Empty(t, results, "no rune is returned while the sequence is incomplete")

	src.chunks <- []byte(msg[2:])

	var res liveRead
	select {
	case res = <-results:
	case <-time.After(liveTimeout):
		require.FailNow(t, "timed out waiting for the completed rune")
	}
	require.NoError(t, res.err)
	assert.Equal(t, 'é', res.lr.r)
	assert.Equal(t, 2, res.lr.size)
	assert.Equal(t, 1, res.lr.pos.ByteOffset())
	assert.Equal(t, 1, res.lr.pos.RuneOffset())
	assert.Equal(t, 1, res.lr.pos.Line())
	assert.Equal(t, 1, res.lr.pos.Column())

	cur := c.Cursor()
	assert.Equal(t, 3, cur.ByteOffset())
	assert.Equal(t, 2, cur.RuneOffset())
}

// TestLiveReaderPeeksArrivedRune covers R1 for PeekRune: with fewer than 4
// bytes buffered, a peek returns the rune and does not consume it.
func TestLiveReaderPeeksArrivedRune(t *testing.T) {
	const msg = "ab"

	src := newLiveReader()
	t.Cleanup(func() { close(src.chunks) })
	src.chunks <- []byte(msg)

	c := cursor.NewReader(src)

	peekThenRead := func() (liveRune, error) {
		peeked, err := located(c.PeekRune())
		if err != nil {
			return peeked, err
		}
		read, err := located(c.ReadLocatedRune())
		if err != nil {
			return read, err
		}
		assert.Truef(t, peeked == read, "peeked %v, read %v", peeked, read)
		return peeked, nil
	}

	got := readLive(t, len(msg), src.blocked, peekThenRead)
	assert.Equal(t, msg, runesOf(got))
}

// TestLiveRuneSourceReturnsArrivedRunes covers R3: a cursor over a live
// rune-only source with the default capacity yields every rune the source
// delivered, and calls ReadRune once per rune returned. Before #117 the cursor
// filled its whole buffer first and returned none.
func TestLiveRuneSourceReturnsArrivedRunes(t *testing.T) {
	const msg = "say hello\n"

	src := newLiveRunes()
	t.Cleanup(func() { close(src.runes) })
	for _, r := range msg {
		src.runes <- r
	}

	c := cursor.NewRuneReader(src)
	got := readLive(t, utf8.RuneCountInString(msg), src.blocked, readLocated(c))
	assert.Equal(t, msg, runesOf(got))
	assert.Equal(t, utf8.RuneCountInString(msg), src.calls, "one ReadRune call per rune returned")

	peeked := make(chan liveRead, 1)
	go func() {
		lr, err := located(c.PeekRune())
		peeked <- liveRead{lr: lr, err: err}
	}()
	select {
	case <-src.blocked:
	case res := <-peeked:
		require.FailNowf(t, "unexpected test state", "peek returned %v without a rune from the source", res)
	case <-time.After(liveTimeout):
		require.FailNow(t, "timed out waiting for the cursor to read the source")
	}

	src.runes <- '🦄'
	select {
	case res := <-peeked:
		require.NoError(t, res.err)
		assert.Equal(t, '🦄', res.lr.r)
		assert.Equal(t, 10, res.lr.pos.ByteOffset())
	case <-time.After(liveTimeout):
		require.FailNow(t, "timed out waiting for the peeked rune")
	}
}
