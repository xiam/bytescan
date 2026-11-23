package meter

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
)

// readStep is one scripted result of scriptReader.Read.
type readStep struct {
	data string
	err  error
}

// scriptReader returns its steps in order, one for each Read call, and io.EOF
// after the last one. A step's data must fit the caller's buffer.
type scriptReader struct{ steps []readStep }

func (r *scriptReader) Read(p []byte) (int, error) {
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	s := r.steps[0]
	r.steps = r.steps[1:]
	return copy(p, s.data), s.err
}

// TestReaderPassesResultsThrough reads a reader that returns short reads, an
// empty read, an error with data, and data with io.EOF. Every result comes
// back unchanged, and the counts are the bytes and the calls.
func TestReaderPassesResultsThrough(t *testing.T) {
	errBroken := errors.New("broken")
	steps := []readStep{
		{"ab", nil},
		{"", nil},
		{"cde", errBroken},
		{"f", io.EOF},
		{"", io.EOF},
	}
	var c Counts
	r := NewReader(&scriptReader{steps: append([]readStep(nil), steps...)}, &c)

	var got bytes.Buffer
	for i, s := range steps {
		p := make([]byte, 8)
		n, err := r.Read(p)
		require.Equal(t, len(s.data), n, "step %d", i)
		require.True(t, err == s.err, "step %d: error %v, want %v", i, err, s.err)
		got.Write(p[:n])
	}
	assert.Equal(t, "abcdef", got.String())
	assert.Equal(t, Counts{Bytes: 6, Reads: 5}, c)
}

// sourceStep is one scripted result of scriptSource.Next.
type sourceStep struct {
	tok *bytescan.Token
	err error
}

// scriptSource returns its steps in order, one for each Next call, and io.EOF
// after the last one.
type scriptSource struct{ steps []sourceStep }

func (s *scriptSource) Next() (*bytescan.Token, error) {
	if len(s.steps) == 0 {
		return nil, io.EOF
	}
	st := s.steps[0]
	s.steps = s.steps[1:]
	return st.tok, st.err
}

// lexWords returns the tokens of the words and spaces of input, with their
// spans.
func lexWords(t *testing.T, input string) []*bytescan.Token {
	t.Helper()
	sc := bytescan.New(strings.NewReader(input))
	sc.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	sc.MustAddRule("SPACE", core.Literal(" "))
	var toks []*bytescan.Token
	for {
		tok, err := sc.Next()
		if err == io.EOF {
			return toks
		}
		require.NoError(t, err)
		toks = append(toks, tok)
	}
}

// TestSourcePassesResultsThrough reads a source that returns tokens,
// bytescan.ErrMoreInput, an error, a nil token with a nil error and io.EOF.
// Every result comes back unchanged. Only the tokens with a nil error count.
func TestSourcePassesResultsThrough(t *testing.T) {
	errBroken := errors.New("broken")
	words := lexWords(t, "one three")
	require.Len(t, words, 3)
	steps := []sourceStep{
		{words[0], nil},
		{nil, bytescan.ErrMoreInput},
		{words[2], nil},
		{nil, errBroken},
		{nil, nil},
		{bytescan.NewToken("WORD", []rune("nospan"), 8), nil},
		{nil, io.EOF},
	}
	var c Counts
	src := NewSource(&scriptSource{steps: append([]sourceStep(nil), steps...)}, &c)

	for i, s := range steps {
		tok, err := src.Next()
		require.True(t, tok == s.tok, "step %d: token %v, want %v", i, tok, s.tok)
		require.True(t, err == s.err, "step %d: error %v, want %v", i, err, s.err)
	}
	// "nospan" has no span, so it counts as a token of 0 bytes.
	assert.Equal(t, Counts{Tokens: 3, TokenBytes: 8}, c)
}

// TestNilArguments checks that the constructors refuse a nil argument.
func TestNilArguments(t *testing.T) {
	var c Counts
	assert.Panics(t, func() { NewReader(nil, &c) })
	assert.Panics(t, func() { NewReader(strings.NewReader(""), nil) })
	assert.Panics(t, func() { NewSource(nil, &c) })
	assert.Panics(t, func() { NewSource(&scriptSource{}, nil) })
}

// newWordScanner returns a scanner of lowercase words and single spaces over
// r, read through a Reader, and the Source that wraps the scanner. Both count
// in c.
func newWordScanner(r io.Reader, c *Counts) *Source {
	sc := bytescan.NewWithCursor(cursor.NewReader(NewReader(r, c)))
	sc.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	sc.MustAddRule("SPACE", core.Literal(" "))
	return NewSource(sc, c)
}

// drain calls Next until it returns an error, and returns that error.
func drain(t *testing.T, src bytescan.Source) error {
	t.Helper()
	for i := 0; ; i++ {
		require.Less(t, i, 1<<20, "the source does not end")
		if _, err := src.Next(); err != nil {
			return err
		}
	}
}

// checkRates checks the rates of c over the interval that the caller
// measured, and that a zero or negative interval gives an error and no rate.
func checkRates(t *testing.T, c Counts, elapsed time.Duration) {
	t.Helper()
	got, err := Rates(c, elapsed)
	require.NoError(t, err)
	assert.Equal(t, float64(c.Bytes)/elapsed.Seconds(), got.BytesPerSecond)
	assert.Equal(t, float64(c.Tokens)/elapsed.Seconds(), got.TokensPerSecond)

	for _, d := range []time.Duration{0, -time.Second} {
		got, err := Rates(c, d)
		assert.Error(t, err, "interval %v", d)
		assert.Equal(t, Throughput{}, got, "interval %v", d)
	}
}

// TestRatesDataWithEOF lexes an input that the reader returns in one Read
// call, together with io.EOF. The bytes of that one call count, and the rates
// cover the interval that the test supplies.
func TestRatesDataWithEOF(t *testing.T) {
	const input = "ab cd ef"
	var c Counts
	src := newWordScanner(&scriptReader{steps: []readStep{{input, io.EOF}}}, &c)
	require.Equal(t, io.EOF, drain(t, src))

	assert.Equal(t, uint64(len(input)), c.Bytes)
	// Every later Read of the script returns 0 and io.EOF, so all the bytes
	// came from the first call. The cursor may read again after io.EOF, and
	// each such call counts.
	assert.GreaterOrEqual(t, c.Reads, uint64(1))
	assert.Equal(t, uint64(5), c.Tokens)
	assert.Equal(t, uint64(len(input)), c.TokenBytes)

	elapsed := 2 * time.Second
	checkRates(t, c, elapsed)
	got, err := Rates(c, elapsed)
	require.NoError(t, err)
	assert.Equal(t, Throughput{BytesPerSecond: 4, TokensPerSecond: 2.5}, got)
}

// TestRatesBufferedTail lexes an input that the cursor reads in full before
// the first token: every later token comes from its buffer, after the last
// Read. The byte count stops early, the token count goes on, and the rates
// use the whole interval.
func TestRatesBufferedTail(t *testing.T) {
	input := strings.Repeat("word ", 1000)
	var c Counts
	src := newWordScanner(strings.NewReader(input), &c)

	_, err := src.Next()
	require.NoError(t, err)
	first := c
	assert.Equal(t, uint64(len(input)), first.Bytes, "the cursor buffers the whole input at once")
	assert.Equal(t, uint64(1), first.Tokens)

	require.Equal(t, io.EOF, drain(t, src))
	assert.Equal(t, first.Bytes, c.Bytes)
	assert.Equal(t, uint64(2000), c.Tokens, "the tokens of the tail count after the last byte was read")
	assert.Equal(t, uint64(len(input)), c.TokenBytes)
	assert.Equal(t, uint64(1), first.Tokens, "the snapshot did not change")

	elapsed := 500 * time.Millisecond
	checkRates(t, c, elapsed)
	got, err := Rates(c, elapsed)
	require.NoError(t, err)
	assert.Equal(t, Throughput{BytesPerSecond: 10000, TokensPerSecond: 4000}, got)
}

// TestRatesEmptyInput lexes an empty input: nothing counts but the Read
// calls, and the rates are zero, not an error.
func TestRatesEmptyInput(t *testing.T) {
	var c Counts
	src := newWordScanner(strings.NewReader(""), &c)
	require.Equal(t, io.EOF, drain(t, src))

	assert.Equal(t, uint64(0), c.Bytes)
	assert.Equal(t, uint64(0), c.Tokens)
	assert.Equal(t, uint64(0), c.TokenBytes)
	assert.NotZero(t, c.Reads)

	checkRates(t, c, time.Millisecond)
	got, err := Rates(c, time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, Throughput{}, got)
}

// feedReader returns the input fed so far. With none, it returns
// cursor.ErrMoreInput, or io.EOF after end.
type feedReader struct {
	buf   []byte
	ended bool
}

func (r *feedReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		if r.ended {
			return 0, io.EOF
		}
		return 0, cursor.ErrMoreInput
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// TestRatesPauseAndResume lexes a live input that pauses: Next returns
// bytescan.ErrMoreInput through the Source, the caller feeds more input, and
// Next resumes. The counts hold both parts, and the rates use the whole
// interval, the pause included.
func TestRatesPauseAndResume(t *testing.T) {
	feed := &feedReader{buf: []byte("ab cd ")}
	var c Counts
	src := newWordScanner(feed, &c)

	var paused bool
	for i := 0; i < 100 && !paused; i++ {
		_, err := src.Next()
		if err != nil {
			require.True(t, err == bytescan.ErrMoreInput, "error %v, want bytescan.ErrMoreInput", err)
			paused = true
		}
	}
	require.True(t, paused)
	atPause := c
	assert.Equal(t, uint64(6), atPause.Bytes)
	assert.Equal(t, uint64(4), atPause.Tokens)

	feed.buf = append(feed.buf, "ef gh"...)
	feed.ended = true
	require.Equal(t, io.EOF, drain(t, src))

	assert.Equal(t, uint64(11), c.Bytes)
	assert.Equal(t, uint64(7), c.Tokens)
	assert.Equal(t, uint64(11), c.TokenBytes)
	assert.Greater(t, c.Reads, atPause.Reads)

	elapsed := 4 * time.Second
	checkRates(t, c, elapsed)
	got, err := Rates(c, elapsed)
	require.NoError(t, err)
	assert.Equal(t, Throughput{BytesPerSecond: 2.75, TokensPerSecond: 1.75}, got)
}
