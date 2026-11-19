package cursor_test

import (
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan/cursor"
)

// countingReader counts the bytes that a cursor reads from its source.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// posOf returns the byte offset, rune offset, line and column of p.
func posOf(p cursor.Pos) [4]int {
	return [4]int{p.ByteOffset(), p.RuneOffset(), p.Line(), p.Column()}
}

// readTo reads in rune by rune up to the byte offset target, which is a rune
// boundary, and returns the position there.
func readTo(t *testing.T, in string, target int) cursor.Pos {
	t.Helper()

	tr := cursor.New(strings.NewReader(in))
	for tr.Cursor().ByteOffset() < target {
		_, _, err := tr.ReadRune()
		require.NoError(t, err)
	}
	require.Equal(t, target, tr.Cursor().ByteOffset())
	return tr.Cursor()
}

// TestSeekForwardReadsFromSource pins that a forward Seek to a position that
// the buffer does not hold yet reads the bytes in between from the source.
// The source gives one byte for each Read, so the buffer holds only the bytes
// read so far. After the seek, the source has given at least the bytes up to
// the target, and the position (byte offset, rune offset, line and column)
// equals the position after reading the same bytes rune by rune.
func TestSeekForwardReadsFromSource(t *testing.T) {
	const in = "hé\nllo wörld\nxyz 世界\nend"

	for _, tc := range []struct {
		name   string
		whence int
		target int // byte offset in in, a rune boundary
	}{
		{"SeekStart to the second line", io.SeekStart, strings.Index(in, "wörld")},
		{"SeekStart across two lines", io.SeekStart, strings.Index(in, "界")},
		{"SeekCurrent to the last line", io.SeekCurrent, strings.Index(in, "end")},
		{"SeekStart to the end of input", io.SeekStart, len(in)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, tc.target == len(in) || utf8.RuneStart(in[tc.target]))

			src := &countingReader{r: strings.NewReader(in)}
			tr := cursor.NewWithCapacity(iotest.OneByteReader(src), 64)

			r, _, err := tr.ReadRune()
			require.NoError(t, err)
			require.Equal(t, 'h', r)
			from := tr.Cursor().ByteOffset()

			// The buffer does not hold the target yet.
			require.Less(t, src.n, tc.target, "bytes read from the source before the seek")

			offset := int64(tc.target)
			if tc.whence == io.SeekCurrent {
				offset -= int64(from)
			}
			got, err := tr.Seek(offset, tc.whence)
			require.NoError(t, err)
			require.Equal(t, int64(tc.target), got)

			require.GreaterOrEqual(t, src.n, tc.target, "bytes read from the source after the seek")
			require.Equal(t, posOf(readTo(t, in, tc.target)), posOf(tr.Cursor()))

			rest, err := io.ReadAll(tr)
			require.NoError(t, err)
			require.Equal(t, in[tc.target:], string(rest))
		})
	}
}

// seekFixtureOf returns a reader with a buffer of 16 bytes over in, after it
// has read the first 100 bytes and peeked at the next rune. in holds lines of
// 10 bytes and is at least 101 bytes long. The start of the stream has left
// the buffer. The buffer holds bytes before and after the cursor, so a failed
// seek has bytes on both sides to move over.
func seekFixtureOf(t *testing.T, in string) *cursor.Reader {
	t.Helper()

	tr := cursor.NewWithCapacity(strings.NewReader(in), 16)
	_, err := io.ReadFull(tr, make([]byte, 96))
	require.NoError(t, err)
	for i := 0; i < 4; i++ {
		_, _, err = tr.ReadRune()
		require.NoError(t, err)
	}
	_, err = tr.PeekRune()
	require.NoError(t, err)
	require.Equal(t, 100, tr.Cursor().ByteOffset())

	// The buffer holds the byte before the cursor.
	_, err = tr.Seek(-1, io.SeekCurrent)
	require.NoError(t, err)
	r, _, err := tr.ReadRune()
	require.NoError(t, err)
	require.Equal(t, rune(in[99]), r)
	return tr
}

// seekFixture returns seekFixtureOf over 20 lines of 10 bytes.
func seekFixture(t *testing.T) (*cursor.Reader, string) {
	t.Helper()

	in := strings.Repeat("abcdefghi\n", 20)
	return seekFixtureOf(t, in), in
}

// seekOffset returns the offset argument that makes Seek aim at the stream
// offset target for whence. io.SeekEnd counts from the end of the buffered
// bytes; a seek to that end and back gives its stream offset. The cursor of tr
// must be at a rune boundary.
func seekOffset(t *testing.T, tr *cursor.Reader, target int64, whence int) int64 {
	t.Helper()

	before := tr.Cursor()
	switch whence {
	case io.SeekCurrent:
		return target - int64(before.ByteOffset())
	case io.SeekEnd:
		end, err := tr.Seek(0, io.SeekEnd)
		require.NoError(t, err)
		_, err = tr.Seek(int64(before.ByteOffset()), io.SeekStart)
		require.NoError(t, err)
		require.Equal(t, posOf(before), posOf(tr.Cursor()))
		return target - end
	}
	return target
}

// requireNotMoved checks that tr is still at before, and that the next rune
// is the rune of in at that position.
func requireNotMoved(t *testing.T, tr *cursor.Reader, in string, before cursor.Pos) {
	t.Helper()

	require.Equal(t, posOf(before), posOf(tr.Cursor()), "Cursor after the failed seek")
	pos := tr.Pos()
	require.Equal(t, [3]int{before.ByteOffset(), before.Line(), before.Column()},
		[3]int{pos.Offset(), pos.Line(), pos.Column()}, "Pos after the failed seek")

	r, _, err := tr.ReadRune()
	require.NoError(t, err)
	want, _ := utf8.DecodeRuneInString(in[before.ByteOffset():])
	require.Equal(t, want, r, "next rune after the failed seek")
}

// TestSeekWhenceTargets checks each whence against each class of target: before
// stream offset 0, at or after offset 0 but before the buffer start, inside the
// buffer, forward past the buffer capacity, and after the end of the input. The
// targets sit at the edges of their class. Seek computes the absolute target
// first, so the class alone decides the result, whatever the whence. A failed
// seek does not move the cursor. A seek inside the buffer reaches the same
// position as reading the input up to the target.
func TestSeekWhenceTargets(t *testing.T) {
	const negativePosition = "cursor: negative position"

	long := strings.Repeat("abcdefghi\n", 20)
	short := strings.Repeat("abcdefghi\n", 10) + "ab"

	type target struct {
		name string
		in   string
		// at returns the stream offset of the target, from the buffer
		// start.
		at func(start int64) int64
		// wantErr is the error of the seek; nil means success. errNegative
		// means negativePosition.
		wantErr error
	}
	errNegative := errors.New(negativePosition)

	targets := []target{
		{"before offset 0", long, func(int64) int64 { return -1 }, errNegative},
		{"at offset 0, before the buffer start", long, func(int64) int64 { return 0 }, cursor.ErrSeekOutOfBuffer},
		{"just before the buffer start", long, func(s int64) int64 { return s - 1 }, cursor.ErrSeekOutOfBuffer},
		{"at the buffer start", long, func(s int64) int64 { return s }, nil},
		{"inside the buffer, forward", long, func(int64) int64 { return 103 }, nil},
		{"forward past the buffer capacity", long, func(s int64) int64 { return s + 17 }, cursor.ErrSeekOutOfBuffer},
		{"after the end of input", short, func(int64) int64 { return int64(len(short)) + 1 }, cursor.ErrSeekOutOfBuffer},
	}

	for _, whence := range []struct {
		name  string
		value int
	}{
		{"SeekStart", io.SeekStart},
		{"SeekCurrent", io.SeekCurrent},
		{"SeekEnd", io.SeekEnd},
	} {
		for _, tc := range targets {
			t.Run(whence.name+" "+tc.name, func(t *testing.T) {
				tr := seekFixtureOf(t, tc.in)
				before := tr.Cursor()
				start := int64(bufferStart(t, tr, tc.in))
				_, err := tr.Seek(int64(before.ByteOffset()), io.SeekStart)
				require.NoError(t, err)
				require.Equal(t, posOf(before), posOf(tr.Cursor()))
				require.Greater(t, start, int64(1), "the start of the stream left the buffer")
				target := tc.at(start)
				if tc.in == short {
					require.LessOrEqual(t, target-start, int64(16),
						"the target after the end fits the buffer capacity")
				}

				offset := seekOffset(t, tr, target, whence.value)

				got, err := tr.Seek(offset, whence.value)
				switch {
				case tc.wantErr == nil:
					require.NoError(t, err)
					require.Equal(t, target, got)
					require.Equal(t, posOf(readTo(t, tc.in, int(target))), posOf(tr.Cursor()))
					rest, err := io.ReadAll(tr)
					require.NoError(t, err)
					require.Equal(t, tc.in[target:], string(rest))
					return
				case tc.wantErr == errNegative:
					require.EqualError(t, err, negativePosition)
					require.NotErrorIs(t, err, cursor.ErrSeekOutOfBuffer)
				default:
					require.ErrorIs(t, err, tc.wantErr)
				}
				require.Equal(t, int64(0), got)
				requireNotMoved(t, tr, tc.in, before)
			})
		}
	}
}

// TestSeekOutOfBufferDoesNotMove pins the targets that fail with
// ErrSeekOutOfBuffer, for every whence: a target at or after offset 0 but
// before the start of the buffer, offset 0 among them, and a forward target
// that the buffer cannot hold. The cursor is at a rune boundary, and the
// failed call does not move it (TestSeekFailedForwardKeepsSplitRunePending
// pins the case of a cursor inside a rune).
func TestSeekOutOfBufferDoesNotMove(t *testing.T) {
	for _, tc := range []struct {
		name string
		// target is the stream offset of the target.
		target int64
		whence int
	}{
		{"SeekStart to 0 after the start left the buffer", 0, io.SeekStart},
		{"SeekStart before the buffer start", 10, io.SeekStart},
		{"SeekCurrent to 0 after the start left the buffer", 0, io.SeekCurrent},
		{"SeekCurrent before the buffer start", 10, io.SeekCurrent},
		{"SeekEnd to 0 after the start left the buffer", 0, io.SeekEnd},
		{"SeekEnd before the buffer start", 10, io.SeekEnd},
		{"SeekStart forward past the buffer capacity", 150, io.SeekStart},
		{"SeekCurrent forward past the buffer capacity", 150, io.SeekCurrent},
		{"SeekEnd forward past the buffer capacity", 150, io.SeekEnd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, in := seekFixture(t)
			offset := seekOffset(t, tr, tc.target, tc.whence)
			before := tr.Cursor()

			got, err := tr.Seek(offset, tc.whence)
			require.ErrorIs(t, err, cursor.ErrSeekOutOfBuffer)
			require.Equal(t, int64(0), got)
			requireNotMoved(t, tr, in, before)
		})
	}

	t.Run("SeekCurrent after the end of input", func(t *testing.T) {
		const in = "abc\ndef"
		tr := cursor.NewWithCapacity(strings.NewReader(in), 64)
		_, _, err := tr.ReadRune()
		require.NoError(t, err)
		before := tr.Cursor()

		_, err = tr.Seek(int64(len(in)), io.SeekCurrent)
		require.ErrorIs(t, err, cursor.ErrSeekOutOfBuffer)
		requireNotMoved(t, tr, in, before)
	})

	t.Run("SeekCurrent past the largest int64", func(t *testing.T) {
		tr, in := seekFixture(t)
		before := tr.Cursor()

		_, err := tr.Seek(math.MaxInt64, io.SeekCurrent)
		require.ErrorIs(t, err, cursor.ErrSeekOutOfBuffer)
		requireNotMoved(t, tr, in, before)
	})
}

// TestSeekNegativePositionDoesNotMove pins the targets that fail with the
// error "cursor: negative position", which is not ErrSeekOutOfBuffer: a target
// before stream offset 0, for every whence. The failed call does not move the
// cursor.
func TestSeekNegativePositionDoesNotMove(t *testing.T) {
	for _, tc := range []struct {
		name string
		// target is the stream offset of the target.
		target int64
		whence int
	}{
		{"SeekStart before offset 0", -1, io.SeekStart},
		{"SeekCurrent before offset 0", -1, io.SeekCurrent},
		{"SeekEnd before offset 0", -1, io.SeekEnd},
		{"SeekCurrent far before offset 0", -1000, io.SeekCurrent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, in := seekFixture(t)
			offset := seekOffset(t, tr, tc.target, tc.whence)
			before := tr.Cursor()

			got, err := tr.Seek(offset, tc.whence)
			require.EqualError(t, err, "cursor: negative position")
			require.NotErrorIs(t, err, cursor.ErrSeekOutOfBuffer)
			require.Equal(t, int64(0), got)
			requireNotMoved(t, tr, in, before)
		})
	}
}

// splitCase is a reader that stopped inside a multi-byte UTF-8 sequence.
type splitCase struct {
	ascii  int    // ASCII bytes before the multi-byte rune
	rest   string // the multi-byte rune and what follows it
	split  int    // bytes of the rune read before the seek
	retain int    // WithMaxRetained under a checkpoint; 0 means none
}

// open returns the input and a reader that has read the ASCII bytes and the
// first split bytes of the rune. Its cursor counts no rune for those bytes yet.
func (c splitCase) open(t *testing.T) (string, *cursor.Reader) {
	t.Helper()

	in := strings.Repeat("a", c.ascii) + c.rest
	opts := []cursor.Option{cursor.WithCapacity(16)}
	if c.retain > 0 {
		opts = append(opts, cursor.WithMaxRetained(c.retain))
	}
	tr := cursor.NewReader(strings.NewReader(in), opts...)
	if c.retain > 0 {
		cp := tr.Checkpoint()
		t.Cleanup(func() { _ = cp.Release() })
	}
	_, err := io.ReadFull(tr, make([]byte, c.ascii+c.split))
	require.NoError(t, err)

	require.Equal(t, [4]int{c.ascii + c.split, c.ascii, 1, c.ascii}, posOf(tr.Cursor()),
		"a split read counts no rune for the bytes of the split rune yet")
	return in, tr
}

// TestSeekFailedForwardKeepsSplitRunePending pins that a failed Seek does not
// move the cursor when a Read stopped inside a multi-byte UTF-8 sequence. The
// byte offset, the rune offset, the line and the column stay the same, and the
// split sequence stays pending: the Read of its last bytes counts it as one
// rune. The test shows it for forward seeks that fail with ErrSeekOutOfBuffer
// (past the capacity and after the end of input) and with
// ErrRetentionExceeded, and for backward seeks that fail.
func TestSeekFailedForwardKeepsSplitRunePending(t *testing.T) {
	const negativePosition = "cursor: negative position"

	for _, tc := range []struct {
		name string
		splitCase
		offset int64
		whence int
		// wantErr is the error of the seek; nil means negativePosition.
		wantErr error
	}{
		{"forward SeekStart past the capacity, 1 of 2 bytes", splitCase{0, "éx", 1, 0}, 20, io.SeekStart, cursor.ErrSeekOutOfBuffer},
		{"forward SeekCurrent past the capacity, 2 of 3 bytes", splitCase{0, "世x", 2, 0}, 20, io.SeekCurrent, cursor.ErrSeekOutOfBuffer},
		{"forward SeekEnd past the capacity, 1 of 2 bytes", splitCase{0, "éx", 1, 0}, 20, io.SeekEnd, cursor.ErrSeekOutOfBuffer},
		{"forward SeekCurrent after the end of input", splitCase{0, "éx", 1, 0}, 5, io.SeekCurrent, cursor.ErrSeekOutOfBuffer},
		{"forward SeekCurrent after the end of input, 2 of 3 bytes", splitCase{0, "世x", 2, 0}, 5, io.SeekCurrent, cursor.ErrSeekOutOfBuffer},
		{"forward SeekCurrent past the retention limit", splitCase{0, "éx", 1, 2}, 2, io.SeekCurrent, cursor.ErrRetentionExceeded},
		{"forward SeekCurrent past the retention limit, 2 of 3 bytes", splitCase{0, "世x", 2, 3}, 2, io.SeekCurrent, cursor.ErrRetentionExceeded},
		{"backward SeekStart before the buffer start", splitCase{40, "éx", 1, 0}, 0, io.SeekStart, cursor.ErrSeekOutOfBuffer},
		{"backward SeekCurrent before the buffer start", splitCase{40, "éx", 1, 0}, -40, io.SeekCurrent, cursor.ErrSeekOutOfBuffer},
		{"backward SeekCurrent before offset 0", splitCase{0, "éx", 1, 0}, -5, io.SeekCurrent, nil},
		{"backward SeekStart before offset 0", splitCase{0, "éx", 1, 0}, -1, io.SeekStart, nil},
		{"backward SeekEnd before offset 0", splitCase{0, "éx", 1, 0}, -50, io.SeekEnd, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, tr := tc.open(t)
			before := tr.Cursor()

			got, err := tr.Seek(tc.offset, tc.whence)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.EqualError(t, err, negativePosition)
			}
			require.Equal(t, int64(0), got)
			require.Equal(t, posOf(before), posOf(tr.Cursor()), "Cursor after the failed seek")

			// The Read of the last bytes of the rune counts it as one rune.
			runeLen := len(in) - tc.ascii - 1
			_, err = io.ReadFull(tr, make([]byte, runeLen-tc.split))
			require.NoError(t, err)
			require.Equal(t, [4]int{tc.ascii + runeLen, tc.ascii + 1, 1, tc.ascii + 1}, posOf(tr.Cursor()),
				"Cursor after the Read that completes the rune")
		})
	}
}

// TestSeekFailedOnSourceErrorKeepsSplitRunePending shows that a forward Seek
// that fails because the source fails does not move the cursor either, after
// a Read that stopped inside a multi-byte UTF-8 sequence. The error depends on
// what the buffer holds when the source fails. With no unread byte buffered,
// Seek returns the source error, wrapped. With unread bytes buffered, the
// reader serves those bytes first, so Seek fails with ErrSeekOutOfBuffer, and
// a later Read returns the source error.
func TestSeekFailedOnSourceErrorKeepsSplitRunePending(t *testing.T) {
	errSource := errors.New("source failed")

	t.Run("no unread byte buffered", func(t *testing.T) {
		src := io.MultiReader(strings.NewReader("a\xc3"), iotest.ErrReader(errSource))
		tr := cursor.NewWithCapacity(iotest.OneByteReader(src), 16)

		_, err := io.ReadFull(tr, make([]byte, 2))
		require.NoError(t, err)
		before := tr.Cursor()
		require.Equal(t, [4]int{2, 1, 1, 1}, posOf(before))

		got, err := tr.Seek(3, io.SeekCurrent)
		require.ErrorIs(t, err, errSource)
		require.Equal(t, int64(0), got)
		require.Equal(t, posOf(before), posOf(tr.Cursor()), "Cursor after the failed seek")
	})

	t.Run("unread bytes buffered", func(t *testing.T) {
		src := io.MultiReader(strings.NewReader("a\xc3\xa9"), iotest.ErrReader(errSource))
		tr := cursor.NewWithCapacity(iotest.OneByteReader(src), 16)

		_, err := io.ReadFull(tr, make([]byte, 2))
		require.NoError(t, err)
		before := tr.Cursor()
		require.Equal(t, [4]int{2, 1, 1, 1}, posOf(before))

		got, err := tr.Seek(3, io.SeekCurrent)
		require.ErrorIs(t, err, cursor.ErrSeekOutOfBuffer)
		require.NotErrorIs(t, err, errSource)
		require.Equal(t, int64(0), got)
		require.Equal(t, posOf(before), posOf(tr.Cursor()), "Cursor after the failed seek")

		// The Read of the last byte of "é" counts it as one rune. The next
		// Read returns the source error.
		_, err = io.ReadFull(tr, make([]byte, 1))
		require.NoError(t, err)
		require.Equal(t, [4]int{3, 2, 1, 2}, posOf(tr.Cursor()))
		_, err = tr.Read(make([]byte, 1))
		require.ErrorIs(t, err, errSource)
	})
}

// TestSeekFailedThenNextOperation shows that after a failed Seek, the next
// successful operation settles the pending split sequence exactly as it does
// without the failed Seek. The reader stopped inside "é" (1 of 2 bytes) or "世"
// (2 of 3 bytes). For each failure and each next operation, the test compares
// the position with a control reader that did not call the failed Seek.
func TestSeekFailedThenNextOperation(t *testing.T) {
	cases := []struct {
		name string
		splitCase
		offset  int64
		whence  int
		wantErr error
	}{
		{"past the capacity", splitCase{0, "éxyz", 1, 0}, 20, io.SeekCurrent, cursor.ErrSeekOutOfBuffer},
		{"after the end of input", splitCase{0, "世xyz", 2, 0}, 9, io.SeekCurrent, cursor.ErrSeekOutOfBuffer},
		{"past the retention limit", splitCase{0, "éxyz", 1, 5}, 5, io.SeekCurrent, cursor.ErrRetentionExceeded},
	}

	nexts := []struct {
		name string
		do   func(t *testing.T, tr *cursor.Reader, c splitCase)
	}{
		{"Read of the rest of the rune", func(t *testing.T, tr *cursor.Reader, c splitCase) {
			// c.rest is the multi-byte rune followed by "xyz".
			runeLen := len(c.rest) - 3
			_, err := io.ReadFull(tr, make([]byte, runeLen-c.split))
			require.NoError(t, err)
		}},
		{"ReadRune", func(t *testing.T, tr *cursor.Reader, _ splitCase) {
			_, _, err := tr.ReadRune()
			require.NoError(t, err)
		}},
		{"Seek forward by 1", func(t *testing.T, tr *cursor.Reader, _ splitCase) {
			_, err := tr.Seek(1, io.SeekCurrent)
			require.NoError(t, err)
		}},
		{"Seek backward by 1", func(t *testing.T, tr *cursor.Reader, _ splitCase) {
			_, err := tr.Seek(-1, io.SeekCurrent)
			require.NoError(t, err)
		}},
	}

	for _, tc := range cases {
		for _, next := range nexts {
			t.Run(tc.name+", then "+next.name, func(t *testing.T) {
				_, control := tc.open(t)
				next.do(t, control, tc.splitCase)

				_, tr := tc.open(t)
				_, err := tr.Seek(tc.offset, tc.whence)
				require.ErrorIs(t, err, tc.wantErr)
				next.do(t, tr, tc.splitCase)

				require.Equal(t, posOf(control.Cursor()), posOf(tr.Cursor()))

				rest, err := io.ReadAll(tr)
				require.NoError(t, err)
				want, err := io.ReadAll(control)
				require.NoError(t, err)
				require.Equal(t, string(want), string(rest))
			})
		}
	}

	t.Run("Read of the rest of the rune counts one rune", func(t *testing.T) {
		in, tr := cases[0].open(t)
		_, err := tr.Seek(cases[0].offset, cases[0].whence)
		require.ErrorIs(t, err, cases[0].wantErr)

		p := make([]byte, 2)
		_, err = io.ReadFull(tr, p)
		require.NoError(t, err)
		require.Equal(t, in[1:3], string(p))
		require.Equal(t, [4]int{3, 2, 1, 2}, posOf(tr.Cursor()))
	})

	t.Run("Seek that moves counts each pending byte as one rune", func(t *testing.T) {
		_, tr := cases[0].open(t)
		_, err := tr.Seek(cases[0].offset, cases[0].whence)
		require.ErrorIs(t, err, cases[0].wantErr)

		// The lead byte of "é" settles as one rune, and the seek moves over
		// the continuation byte, which counts as one more.
		got, err := tr.Seek(1, io.SeekCurrent)
		require.NoError(t, err)
		require.Equal(t, int64(2), got)
		require.Equal(t, [4]int{2, 2, 1, 2}, posOf(tr.Cursor()))
	})
}
