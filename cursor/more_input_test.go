package cursor_test

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan/cursor"
)

// The tests in this file cover cursor.ErrMoreInput (#124): a source returns it
// when it has no input now. The reader passes it on, does not keep it, and
// does not read a cut multi-byte character as U+FFFD.

// step is one answer of a stepSource: the bytes of one Read and its error.
type step struct {
	data string
	err  error
}

// stepSource answers each Read with the next step. When no step is left it
// returns cursor.ErrMoreInput, or io.EOF after end.
type stepSource struct {
	steps []step
	ended bool
	reads int
}

func (s *stepSource) feed(data string) { s.steps = append(s.steps, step{data: data}) }

func (s *stepSource) Read(p []byte) (int, error) {
	s.reads++
	if len(s.steps) == 0 {
		if s.ended {
			return 0, io.EOF
		}
		return 0, cursor.ErrMoreInput
	}
	st := s.steps[0]
	n := copy(p, st.data)
	if n < len(st.data) {
		s.steps[0].data = st.data[n:]
		return n, nil
	}
	s.steps = s.steps[1:]
	return n, st.err
}

func requireRune(t *testing.T, r *cursor.Reader, want rune, size, byteOff, runeOff int) {
	t.Helper()
	lr, err := r.ReadLocatedRune()
	require.NoError(t, err)
	assert.Equal(t, want, lr.Rune())
	assert.Equal(t, size, lr.Size())
	assert.Equal(t, byteOff, lr.Pos().ByteOffset())
	assert.Equal(t, runeOff, lr.Pos().RuneOffset())
}

// TestMoreInputCutRuneStaysHeld checks that a multi-byte character cut at the
// end of the input so far is not read as U+FFFD: the read returns the signal,
// the cursor does not move, and the character is read whole once its last
// byte arrives.
func TestMoreInputCutRuneStaysHeld(t *testing.T) {
	src := &stepSource{}
	r := cursor.NewReader(src)
	src.feed("h\xc3")

	requireRune(t, r, 'h', 1, 0, 0)

	for i := 0; i < 2; i++ {
		_, err := r.ReadLocatedRune()
		require.True(t, err == cursor.ErrMoreInput, "got %v", err)
		pos := r.Cursor()
		assert.Equal(t, 1, pos.ByteOffset())
		assert.Equal(t, 1, pos.RuneOffset())
		assert.Equal(t, 1, pos.Column())
	}

	src.feed("\xa9x")
	requireRune(t, r, 'é', 2, 1, 1)
	requireRune(t, r, 'x', 1, 3, 2)

	src.ended = true
	_, err := r.ReadLocatedRune()
	assert.Equal(t, io.EOF, err)
}

// TestMoreInputNotSticky checks that the reader does not keep the signal, as
// it keeps another error of the source: the next read asks the source again.
func TestMoreInputNotSticky(t *testing.T) {
	src := &stepSource{}
	r := cursor.NewReader(src)

	_, _, err := r.ReadRune()
	require.Equal(t, cursor.ErrMoreInput, err)
	reads := src.reads

	src.feed("a")
	ch, _, err := r.ReadRune()
	require.NoError(t, err)
	assert.Equal(t, 'a', ch)
	assert.Greater(t, src.reads, reads)

	// Another error of the source stays: it is returned again without a read.
	failure := errors.New("source failed")
	src.steps = append(src.steps, step{err: failure}, step{data: "b"})
	_, _, err = r.ReadRune()
	require.ErrorIs(t, err, failure)
	_, _, err = r.ReadRune()
	assert.ErrorIs(t, err, failure)
}

// TestMoreInputWithData checks a source that returns bytes and the signal from
// one Read: the bytes that complete a rune are read first.
func TestMoreInputWithData(t *testing.T) {
	src := &stepSource{steps: []step{{data: "a\xc3", err: cursor.ErrMoreInput}}}
	r := cursor.NewReader(src)

	requireRune(t, r, 'a', 1, 0, 0)
	_, err := r.ReadLocatedRune()
	require.Equal(t, cursor.ErrMoreInput, err)

	src.steps = append(src.steps, step{data: "\xa9", err: cursor.ErrMoreInput})
	requireRune(t, r, 'é', 2, 1, 1)
}

// TestMoreInputBulkRead checks Read: the bytes that arrived are returned
// first, with no error, and the signal comes when none is left. The carried
// bytes of a cut character are not flushed on the signal.
func TestMoreInputBulkRead(t *testing.T) {
	src := &stepSource{}
	r := cursor.NewReader(src)
	src.feed("ab\xc3")

	p := make([]byte, 8)
	n, err := r.Read(p)
	require.NoError(t, err)
	assert.Equal(t, "ab\xc3", string(p[:n]))

	n, err = r.Read(p)
	require.True(t, err == cursor.ErrMoreInput, "got %v", err)
	assert.Zero(t, n)
	pos := r.Cursor()
	assert.Equal(t, 3, pos.ByteOffset())
	assert.Equal(t, 2, pos.RuneOffset(), "the carry is not flushed")
	assert.Equal(t, 2, pos.Column())

	src.feed("\xa9")
	n, err = r.Read(p)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	pos = r.Cursor()
	assert.Equal(t, 4, pos.ByteOffset())
	assert.Equal(t, 3, pos.RuneOffset())
	assert.Equal(t, 3, pos.Column())

	src.steps = append(src.steps, step{data: "cd", err: cursor.ErrMoreInput})
	n, err = r.Read(p)
	require.NoError(t, err)
	assert.Equal(t, "cd", string(p[:n]))
}

// TestMoreInputByteMode checks the signal on a byte-mode reader.
func TestMoreInputByteMode(t *testing.T) {
	src := &stepSource{}
	r := cursor.NewByteReader(src)
	src.feed("\xc3")

	requireRune(t, r, 0xc3, 1, 0, 0)
	_, err := r.ReadLocatedRune()
	require.Equal(t, cursor.ErrMoreInput, err)

	src.steps = append(src.steps, step{data: "\xa9", err: cursor.ErrMoreInput})
	requireRune(t, r, 0xa9, 1, 1, 1)
}

// TestMoreInputRuneSource checks the signal from an io.RuneReader source.
func TestMoreInputRuneSource(t *testing.T) {
	src := &runeSteps{runes: []rune("é")}
	r := cursor.NewRuneReader(src)

	requireRune(t, r, 'é', 2, 0, 0)
	_, err := r.ReadLocatedRune()
	require.Equal(t, cursor.ErrMoreInput, err)

	src.runes = append(src.runes, 'x')
	requireRune(t, r, 'x', 1, 2, 1)
}

type runeSteps struct{ runes []rune }

func (s *runeSteps) ReadRune() (rune, int, error) {
	if len(s.runes) == 0 {
		return 0, 0, cursor.ErrMoreInput
	}
	ch := s.runes[0]
	s.runes = s.runes[1:]
	return ch, len(string(ch)), nil
}

// TestMoreInputSeek checks a forward Seek over a source that returns bytes and
// the signal from one Read: a target inside those bytes succeeds on the first
// call, with the position that reading the bytes gives. A target past them
// fails with the signal and does not move the cursor.
func TestMoreInputSeek(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal error
	}{
		{"plain", cursor.ErrMoreInput},
		{"wrapped", fmt.Errorf("live source: %w", cursor.ErrMoreInput)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &stepSource{steps: []step{{data: "abcd", err: tc.signal}}}
			r := cursor.NewReader(src)

			off, err := r.Seek(2, io.SeekStart)
			require.NoError(t, err)
			assert.Equal(t, int64(2), off)
			assert.Equal(t, 2, r.Cursor().RuneOffset())
			assert.Equal(t, 2, r.Cursor().Column())

			off, err = r.Seek(1, io.SeekCurrent)
			require.NoError(t, err)
			assert.Equal(t, int64(3), off)

			// The target is past the bytes that arrived.
			_, err = r.Seek(2, io.SeekCurrent)
			require.ErrorIs(t, err, cursor.ErrMoreInput)
			assert.Equal(t, 3, r.Cursor().ByteOffset())
			_, err = r.Seek(5, io.SeekStart)
			require.ErrorIs(t, err, cursor.ErrMoreInput)
			assert.Equal(t, 3, r.Cursor().ByteOffset())

			requireRune(t, r, 'd', 1, 3, 3)
		})
	}
}

// TestMoreInputCheckpointReplay checks that a checkpoint marked before the
// signal replays the same input after it, together with the new input.
func TestMoreInputCheckpointReplay(t *testing.T) {
	src := &stepSource{}
	r := cursor.NewReader(src)
	src.feed("ab\xe2\x9c")

	c := r.Checkpoint()
	requireRune(t, r, 'a', 1, 0, 0)
	requireRune(t, r, 'b', 1, 1, 1)
	_, err := r.ReadLocatedRune()
	require.Equal(t, cursor.ErrMoreInput, err)
	require.NoError(t, c.Reset())

	src.feed("\x93")
	requireRune(t, r, 'a', 1, 0, 0)
	requireRune(t, r, 'b', 1, 1, 1)
	requireRune(t, r, '✓', 3, 2, 2)
	require.NoError(t, c.Commit())
}
