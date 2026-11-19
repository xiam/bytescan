package cursor_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan/cursor"
)

// readAll reads tr rune by rune to the end and returns the runes with their
// positions.
func readAll(t *testing.T, tr *cursor.Reader) ([]rune, [][4]int) {
	t.Helper()

	var (
		runes []rune
		pos   [][4]int
	)
	for {
		lr, err := tr.ReadLocatedRune()
		if err == io.EOF {
			return runes, pos
		}
		require.NoError(t, err)
		runes = append(runes, lr.Rune())
		pos = append(pos, posOf(lr.Pos()))
	}
}

// TestResetStartReportsPositionsFromTheRestart pins that ResetStart reads the
// new source from its first byte and reports positions counted from there:
// byte offset 0, rune offset 0, line 1 and column 0. The reader read part of an
// old source first, with multi-byte runes and newlines, so every coordinate of
// the old source is non-zero at the reset. After the reset the runes and the
// positions equal those of a new reader over the same source, in text mode and
// in byte mode.
func TestResetStartReportsPositionsFromTheRestart(t *testing.T) {
	const (
		old  = "héllo\nwörld\n世界 and more"
		text = "x = 1\nwörd\n世"
	)

	for _, tc := range []struct {
		name  string
		build func(io.Reader) *cursor.Reader
	}{
		{"NewReader", func(r io.Reader) *cursor.Reader { return cursor.NewReader(r) }},
		{"NewReader small capacity", func(r io.Reader) *cursor.Reader { return cursor.NewReader(r, cursor.WithCapacity(4)) }},
		{"NewByteReader", func(r io.Reader) *cursor.Reader { return cursor.NewByteReader(r) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := tc.build(strings.NewReader(old))
			for i := 0; i < 15; i++ {
				_, err := tr.ReadLocatedRune()
				require.NoError(t, err)
			}
			require.NotZero(t, tr.Cursor().ByteOffset())
			require.NotEqual(t, 1, tr.Cursor().Line())

			for _, s := range []cursor.Start{{}, {InText: true}, {InText: true, LineStart: true}} {
				require.NoError(t, tr.ResetStart(strings.NewReader(text), s))
				require.Equal(t, [4]int{0, 0, 1, 0}, posOf(tr.Cursor()))
				require.True(t, tr.Cursor().IsStart())

				gotRunes, gotPos := readAll(t, tr)
				wantRunes, wantPos := readAll(t, tc.build(strings.NewReader(text)))
				require.Equal(t, wantRunes, gotRunes)
				require.Equal(t, wantPos, gotPos)
			}
		})
	}
}

// TestResetStartDropsTheOldSource pins that nothing of the old source survives
// a reset: buffered bytes, a sticky read error and a partial UTF-8 sequence.
func TestResetStartDropsTheOldSource(t *testing.T) {
	t.Run("buffered bytes", func(t *testing.T) {
		tr := cursor.New(strings.NewReader("abcdef"))
		_, err := tr.ReadLocatedRune()
		require.NoError(t, err)

		require.NoError(t, tr.ResetStart(strings.NewReader("xy"), cursor.Start{InText: true}))
		runes, _ := readAll(t, tr)
		require.Equal(t, []rune("xy"), runes)
	})

	t.Run("sticky read error", func(t *testing.T) {
		errBoom := errors.New("boom")
		tr := cursor.New(io.MultiReader(strings.NewReader("a"), iotest.ErrReader(errBoom)))
		_, err := tr.ReadLocatedRune()
		require.NoError(t, err)
		_, err = tr.ReadLocatedRune()
		require.ErrorIs(t, err, errBoom)

		require.NoError(t, tr.ResetStart(strings.NewReader("ok"), cursor.Start{InText: true}))
		runes, _ := readAll(t, tr)
		require.Equal(t, []rune("ok"), runes)
	})

	t.Run("partial UTF-8 sequence", func(t *testing.T) {
		tr := cursor.New(strings.NewReader("é"))
		p := make([]byte, 1)
		n, err := tr.Read(p)
		require.NoError(t, err)
		require.Equal(t, 1, n)

		require.NoError(t, tr.ResetStart(strings.NewReader("ab"), cursor.Start{InText: true}))
		require.Equal(t, [4]int{0, 0, 1, 0}, posOf(tr.Cursor()))
		runes, pos := readAll(t, tr)
		require.Equal(t, []rune("ab"), runes)
		require.Equal(t, [][4]int{{0, 0, 1, 0}, {1, 1, 1, 1}}, pos)
	})

	t.Run("rune source", func(t *testing.T) {
		tr := cursor.NewRuneReader(strings.NewReader("abc"))
		_, err := tr.ReadLocatedRune()
		require.NoError(t, err)

		require.NoError(t, tr.ResetStart(strings.NewReader("wö"), cursor.Start{InText: true}))
		runes, pos := readAll(t, tr)
		require.Equal(t, []rune("wö"), runes)
		require.Equal(t, [][4]int{{0, 0, 1, 0}, {1, 1, 1, 1}}, pos)
	})
}

// TestResetStartEmptySource pins that a reset onto an empty source, which is
// what a start at or after the end of a text gives, reads io.EOF at offset 0.
func TestResetStartEmptySource(t *testing.T) {
	tr := cursor.New(strings.NewReader("abc"))
	require.NoError(t, tr.ResetStart(strings.NewReader(""), cursor.Start{InText: true, LineStart: true}))

	_, err := tr.ReadLocatedRune()
	require.ErrorIs(t, err, io.EOF)
	require.Equal(t, [4]int{0, 0, 1, 0}, posOf(tr.Cursor()))
}

// TestResetStartRefusesANilSource pins that ResetStart fails on a nil source
// and changes nothing: the reader goes on reading the old source from where it
// was.
func TestResetStartRefusesANilSource(t *testing.T) {
	tr := cursor.New(strings.NewReader("abc"))
	_, err := tr.ReadLocatedRune()
	require.NoError(t, err)

	require.Error(t, tr.ResetStart(nil, cursor.Start{InText: true}))
	lr, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	require.Equal(t, 'b', lr.Rune())
	require.Equal(t, 1, lr.Pos().ByteOffset())
}

// TestResetStartReleasesCheckpoints pins that ResetStart releases every active
// checkpoint, because the input that it would replay is gone. After the reset
// the checkpoints are inactive, Reset and Commit return
// ErrCheckpointReleased, reading retains no input, and Remark re-arms a
// released checkpoint on the new source.
func TestResetStartReleasesCheckpoints(t *testing.T) {
	tr := cursor.New(strings.NewReader("abc"))
	outer := tr.Checkpoint()
	_, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	inner := tr.Checkpoint()
	_, err = tr.ReadLocatedRune()
	require.NoError(t, err)

	require.NoError(t, tr.ResetStart(strings.NewReader("xyz"), cursor.Start{InText: true}))
	for _, cp := range []*cursor.Checkpoint{outer, inner} {
		require.False(t, cp.Active())
		require.ErrorIs(t, cp.Reset(), cursor.ErrCheckpointReleased)
		require.ErrorIs(t, cp.Commit(), cursor.ErrCheckpointReleased)
	}
	lr, err := tr.ReadLocatedRune()
	require.NoError(t, err)
	require.Equal(t, 'x', lr.Rune())
	require.Zero(t, tr.RetainedBytes(), "no checkpoint holds input after the reset")

	require.NoError(t, tr.Remark(inner))
	require.Equal(t, [4]int{1, 1, 1, 1}, posOf(inner.Pos()))
	lr, err = tr.ReadLocatedRune()
	require.NoError(t, err)
	require.Equal(t, 'y', lr.Rune())
	require.Equal(t, 1, tr.RetainedBytes())
	require.NoError(t, inner.Reset())
	lr, err = tr.ReadLocatedRune()
	require.NoError(t, err)
	require.Equal(t, 'y', lr.Rune())
	require.NoError(t, inner.Commit())
}

// TestResetStartStoresTheStart pins Reader.Start: a reader that is never reset
// reports Start{LineStart: true}, the start of a whole text, and a reset stores
// the Start it was given, also when the reset is refused.
func TestResetStartStoresTheStart(t *testing.T) {
	for _, build := range []func(io.Reader) *cursor.Reader{
		cursor.New,
		func(r io.Reader) *cursor.Reader { return cursor.NewByteReader(r) },
	} {
		tr := build(strings.NewReader("abc"))
		require.Equal(t, cursor.Start{LineStart: true}, tr.Start())

		for _, s := range []cursor.Start{{}, {InText: true}, {InText: true, LineStart: true}, {LineStart: true}} {
			require.NoError(t, tr.ResetStart(strings.NewReader("x"), s))
			require.Equal(t, s, tr.Start())
		}

		require.Error(t, tr.ResetStart(nil, cursor.Start{InText: true}))
		require.Equal(t, cursor.Start{LineStart: true}, tr.Start(), "a refused reset changes nothing")
	}

	require.Equal(t, cursor.Start{LineStart: true}, cursor.NewRuneReader(strings.NewReader("a")).Start())
}

// TestResetStartLeavesNoRetentionLimit pins that after ResetStart released a
// checkpoint, the retention limit no longer applies: the limit applies only
// while a checkpoint is active, so a bulk Read larger than the limit returns
// every byte it asks for. The limit itself stays an option of the reader, and
// it applies to a checkpoint armed after the reset.
func TestResetStartLeavesNoRetentionLimit(t *testing.T) {
	tr := cursor.NewReader(strings.NewReader("abc"), cursor.WithMaxRetained(1))
	cp := tr.Checkpoint()
	require.NoError(t, tr.ResetStart(strings.NewReader("wxyz"), cursor.Start{InText: true}))
	require.False(t, cp.Active())

	p := make([]byte, 4)
	n, err := io.ReadFull(tr, p)
	require.NoError(t, err)
	require.Equal(t, 4, n)
	require.Equal(t, "wxyz", string(p))

	// The limit stays: it applies again to a new checkpoint.
	require.NoError(t, tr.ResetStart(strings.NewReader("ab"), cursor.Start{InText: true}))
	tr.Checkpoint()
	_, err = tr.ReadLocatedRune()
	require.NoError(t, err)
	_, err = tr.ReadLocatedRune()
	require.ErrorIs(t, err, cursor.ErrRetentionExceeded)
}

// TestResetStartDropsAHeldCutRune pins that ResetStart drops a multi-byte
// character that the old source cut with ErrMoreInput, and that no "more
// input" condition survives the reset. The cut character is held in the
// buffer after ReadLocatedRune, and in the partial UTF-8 carry after a bulk
// Read. After the reset the new source reads from offset 0, and a source that
// has input does not return ErrMoreInput.
func TestResetStartDropsAHeldCutRune(t *testing.T) {
	t.Run("ReadLocatedRune", func(t *testing.T) {
		src := &stepSource{}
		src.feed("h\xc3")
		tr := cursor.NewReader(src)
		requireRune(t, tr, 'h', 1, 0, 0)
		_, err := tr.ReadLocatedRune()
		require.ErrorIs(t, err, cursor.ErrMoreInput)

		require.NoError(t, tr.ResetStart(strings.NewReader("ab"), cursor.Start{InText: true}))
		runes, pos := readAll(t, tr)
		require.Equal(t, []rune("ab"), runes)
		require.Equal(t, [][4]int{{0, 0, 1, 0}, {1, 1, 1, 1}}, pos)
	})

	t.Run("bulk Read", func(t *testing.T) {
		src := &stepSource{}
		src.feed("h\xc3")
		tr := cursor.NewReader(src)
		p := make([]byte, 8)
		n, err := tr.Read(p)
		require.Equal(t, 2, n)
		if err != nil {
			require.ErrorIs(t, err, cursor.ErrMoreInput)
		}
		_, err = tr.Read(p)
		require.ErrorIs(t, err, cursor.ErrMoreInput)

		require.NoError(t, tr.ResetStart(strings.NewReader("ab"), cursor.Start{InText: true}))
		require.Equal(t, [4]int{0, 0, 1, 0}, posOf(tr.Cursor()))
		runes, pos := readAll(t, tr)
		require.Equal(t, []rune("ab"), runes)
		require.Equal(t, [][4]int{{0, 0, 1, 0}, {1, 1, 1, 1}}, pos)
	})

	t.Run("rune source", func(t *testing.T) {
		tr := cursor.NewRuneReader(&runeSteps{runes: []rune("é")})
		requireRune(t, tr, 'é', 2, 0, 0)
		_, err := tr.ReadLocatedRune()
		require.ErrorIs(t, err, cursor.ErrMoreInput)

		require.NoError(t, tr.ResetStart(strings.NewReader("x"), cursor.Start{InText: true}))
		runes, pos := readAll(t, tr)
		require.Equal(t, []rune("x"), runes)
		require.Equal(t, [][4]int{{0, 0, 1, 0}}, pos)
	})
}
