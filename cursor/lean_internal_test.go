package cursor

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/xiam/bytescan/internal/leanread"
)

// leanInputs are the texts of the lean read path tests: ASCII, multi-byte
// runes, invalid UTF-8, line breaks at every place, and runs longer than one
// batch.
var leanInputs = []string{
	"",
	"a",
	"\n",
	"\n\n\na\n",
	"abc def\nghi\r\njkl",
	"héllo wörld\n世界\n🙂🙂 x",
	"\xff\xe2\x28\xa1 \xf0\x9f\x98 a\xc3",
	"\xe4\xb8\x96\n\xe7\x95\x8c",
	strings.Repeat("abcdefghij", 7) + "\n" + strings.Repeat("é", 40) + "\n",
	strings.Repeat("\n", 40) + "z",
}

// leanSource builds one of two identical sources for the twin readers.
type leanSource struct {
	name string
	make func(text string) *Reader
}

// moreInputReader returns ErrMoreInput once after every chunk of n bytes.
type moreInputReader struct {
	data    []byte
	n       int
	pending bool
}

func (m *moreInputReader) Read(p []byte) (int, error) {
	if m.pending {
		m.pending = false
		return 0, ErrMoreInput
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	k := min(m.n, len(p), len(m.data))
	copy(p, m.data[:k])
	m.data = m.data[k:]
	m.pending = true
	return k, nil
}

// zeroReadReader returns its data n bytes at a time, with an empty read that
// has a nil error after every chunk. The reader turns such a read into
// io.ErrNoProgress, which is not sticky.
type zeroReadReader struct {
	data []byte
	n    int
	zero bool
}

func (z *zeroReadReader) Read(p []byte) (int, error) {
	if z.zero {
		z.zero = false
		return 0, nil
	}
	if len(z.data) == 0 {
		return 0, io.EOF
	}
	k := min(z.n, len(p), len(z.data))
	copy(p, z.data[:k])
	z.data = z.data[k:]
	z.zero = true
	return k, nil
}

// failingReader returns its data and then a source error.
type failingReader struct {
	data []byte
}

var errLeanSource = errors.New("lean test: source failed")

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, errLeanSource
	}
	k := copy(p, f.data)
	f.data = f.data[k:]
	return k, nil
}

func leanSources() []leanSource {
	return []leanSource{
		{"text", func(s string) *Reader { return NewReader(strings.NewReader(s)) }},
		{"text-cap4", func(s string) *Reader { return NewReader(strings.NewReader(s), WithCapacity(4)) }},
		{"text-cap5-onebyte", func(s string) *Reader {
			return NewReader(iotest.OneByteReader(strings.NewReader(s)), WithCapacity(5))
		}},
		{"text-half", func(s string) *Reader { return NewReader(iotest.HalfReader(strings.NewReader(s))) }},
		{"rune", func(s string) *Reader { return NewRuneReader(strings.NewReader(s), WithCapacity(4)) }},
		{"byte", func(s string) *Reader { return NewByteReader(strings.NewReader(s), WithCapacity(4)) }},
		{"retained", func(s string) *Reader { return NewReader(strings.NewReader(s), WithMaxRetained(9)) }},
		{"more-input", func(s string) *Reader { return NewReader(&moreInputReader{data: []byte(s), n: 3}) }},
		{"more-input-byte", func(s string) *Reader {
			return NewByteReader(&moreInputReader{data: []byte(s), n: 2})
		}},
		{"zero-read", func(s string) *Reader { return NewReader(&zeroReadReader{data: []byte(s), n: 1}) }},
		{"zero-read-3", func(s string) *Reader { return NewReader(&zeroReadReader{data: []byte(s), n: 3}) }},
		{"zero-read-byte", func(s string) *Reader { return NewByteReader(&zeroReadReader{data: []byte(s), n: 1}) }},
		{"failing", func(s string) *Reader { return NewReader(&failingReader{data: []byte(s)}, WithCapacity(6)) }},
		{"reset-start", func(s string) *Reader {
			r := NewReader(strings.NewReader("ignored"))
			if err := r.ResetStart(strings.NewReader(s), Start{InText: true}); err != nil {
				panic(err)
			}
			return r
		}},
	}
}

// errText reports err for a comparison: two errors match when they have the
// same text.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestLeanPeekMatchesReadLocatedRune reads each input twice: through Peek on
// one reader, and through ReadLocatedRune on a twin reader over the same
// source. Like the scanner, both hold a checkpoint at the start of each
// "token". The lean reader moves only with a Seek over the runes it peeked.
// After a random number of runes, both readers end the token: they step back
// over a random overshoot, or they reset to the checkpoint and read the token
// again. Each rune, its size, the position at which a Peek starts, every
// error and every position after a move must be the same on both readers.
func TestLeanPeekMatchesReadLocatedRune(t *testing.T) {
	for _, src := range leanSources() {
		for i, text := range leanInputs {
			for _, batch := range []int{1, 3, 16} {
				name := fmt.Sprintf("%s/%d/batch%d", src.name, i, batch)
				t.Run(name, func(t *testing.T) {
					leanTwin(t, src.make(text), src.make(text), batch, int64(i*31+batch), false)
				})
			}
		}
	}
}

// leanTwin runs the twin readers of TestLeanPeekMatchesReadLocatedRune. With
// advance set, the lean reader ends a token with leanread.Advancer.Advance and
// the extent of the runes it peeked, as the scanner does, instead of a Seek,
// unless a Peek of the token reported Partial.
func leanTwin(t *testing.T, a, b *Reader, batch int, seed int64, advance bool) {
	t.Helper()

	lean, ok := leanread.Of(a)
	if !ok {
		t.Fatal("leanread.Of(*Reader) reports no lean read path")
	}
	advancer, ok := lean.(leanread.Advancer[Pos])
	if !ok {
		t.Fatal("the lean read path of *Reader has no Advance")
	}
	var peeked []rune

	rng := rand.New(rand.NewSource(seed))
	dst := make([]leanread.Rune, batch)

	ca, cb := a.Checkpoint(), b.Checkpoint()
	skip, runes := 0, 0
	var sizes []int
	resets := 0
	partial := false

	// commit ends the token on both readers: the lean reader moves over the
	// runes it peeked, both step back over an overshoot of whole runes, and
	// both mark the next token.
	commit := func() {
		t.Helper()
		// As the scanner does (Scanner.leanAdvance): one Seek over the
		// token, or one Seek per rune after a Peek reported Partial.
		moves := []int{skip}
		if partial {
			moves = sizes
		}
		if advance && !partial {
			moves = nil
			got, err := advancer.Advance(skip, extentOf(peeked))
			if skip == 0 {
				// A move of no byte is a Seek that does nothing.
				if err != nil || got != b.Cursor() {
					t.Fatalf("Advance(0) = %+v, %v; want %+v", got, err, b.Cursor())
				}
			} else if err != nil {
				t.Fatalf("lean Advance(%d): %v", skip, err)
			} else if want := b.Cursor(); got != want {
				t.Fatalf("Advance(%d) returned %+v, public cursor %+v", skip, got, want)
			}
		}
		for _, m := range moves {
			if m == 0 {
				continue
			}
			if _, err := a.Seek(int64(m), io.SeekCurrent); err != nil {
				t.Fatalf("lean Seek(+%d): %v", m, err)
			}
		}
		if got, want := a.Cursor(), b.Cursor(); got != want {
			t.Fatalf("after the token: lean cursor %+v, public cursor %+v", got, want)
		}
		back := 0
		if len(sizes) > 1 {
			for k := rng.Intn(len(sizes)); k > 0; k-- {
				back += sizes[len(sizes)-k]
			}
		}
		if back > 0 {
			_, errA := a.Seek(int64(-back), io.SeekCurrent)
			_, errB := b.Seek(int64(-back), io.SeekCurrent)
			if errText(errA) != errText(errB) {
				t.Fatalf("step back %d: lean %v, public %v", back, errA, errB)
			}
		}
		if got, want := a.Cursor(), b.Cursor(); got != want {
			t.Fatalf("after the step back: lean cursor %+v, public cursor %+v", got, want)
		}
		_ = ca.Release()
		_ = cb.Release()
		if err := a.Remark(ca); err != nil {
			t.Fatal(err)
		}
		if err := b.Remark(cb); err != nil {
			t.Fatal(err)
		}
		skip, runes, sizes, partial, peeked = 0, 0, sizes[:0], false, peeked[:0]
	}

	// reset replays the token on both readers from its checkpoint.
	reset := func() {
		t.Helper()
		if err := ca.Reset(); err != nil {
			t.Fatal(err)
		}
		if err := cb.Reset(); err != nil {
			t.Fatal(err)
		}
		if got, want := a.Cursor(), b.Cursor(); got != want {
			t.Fatalf("after the reset: lean cursor %+v, public cursor %+v", got, want)
		}
		skip, runes, sizes, partial, peeked = 0, 0, sizes[:0], false, peeked[:0]
	}

	for steps := 0; steps < 10000; steps++ {
		n, at, errA := lean.Peek(skip, dst)

		if errA != nil {
			_, errB := b.ReadLocatedRune()
			if errText(errA) != errText(errB) {
				t.Fatalf("at skip %d: lean error %v, public error %v", skip, errA, errB)
			}
			wantAt := atOf(b.Cursor(), skip)
			if at != wantAt {
				t.Fatalf("at skip %d with error %v: lean at %+v, want %+v", skip, errA, at, wantAt)
			}
			switch {
			case errors.Is(errA, io.EOF):
				if _, _, err := lean.Peek(skip, dst); !errors.Is(err, io.EOF) {
					t.Fatalf("after the end: lean Peek returns %v, want io.EOF", err)
				}
				commit()
				return
			case errors.Is(errA, ErrMoreInput), errors.Is(errA, ErrRetentionExceeded):
				commit()
				continue
			case errors.Is(errA, io.ErrNoProgress):
				// Not sticky: the next read of the same rune asks the
				// source again, on both readers.
				continue
			default:
				// A source error is sticky on both readers.
				_, _, errA2 := lean.Peek(skip, dst)
				_, errB2 := b.ReadLocatedRune()
				if errText(errA2) != errText(errB2) || errText(errA2) != errText(errA) {
					t.Fatalf("repeated error: lean %v, public %v, first %v", errA2, errB2, errA)
				}
				return
			}
		}

		if n < 1 || n > len(dst) {
			t.Fatalf("Peek returned n = %d with a nil error", n)
		}
		for k := 0; k < n; k++ {
			lr, err := b.ReadLocatedRune()
			if err != nil {
				t.Fatalf("lean decoded %q, public read failed: %v", dst[k].Rune, err)
			}
			if k == 0 {
				if at.Partial && (dst[0].Rune != utf8.RuneError || dst[0].Size != 1) {
					t.Fatalf("at skip %d: Partial with %q/%d, want U+FFFD of size 1", skip, dst[0].Rune, dst[0].Size)
				}
				partial = partial || at.Partial
				at.Partial = false
				if want := atOf(lr.Pos(), skip); at != want {
					t.Fatalf("at skip %d: lean at %+v, want %+v (pos %+v)", skip, at, want, lr.Pos())
				}
			}
			if dst[k].Rune != lr.Rune() || dst[k].Size != lr.Size() {
				t.Fatalf("rune %d: lean %q/%d, public %q/%d", runes, dst[k].Rune, dst[k].Size, lr.Rune(), lr.Size())
			}
			skip += dst[k].Size
			sizes = append(sizes, dst[k].Size)
			peeked = append(peeked, dst[k].Rune)
			runes++
		}

		switch rng.Intn(6) {
		case 0, 1:
			commit()
		case 2:
			if resets < 50 {
				resets++
				reset()
			}
		}
	}
	t.Fatal("no end of input after 10000 steps")
}

// atOf is the leanread.At that Peek must report for a rune at pos. At skip 0
// the start of the stream is possible; after it, it is not.
func atOf(pos Pos, skip int) leanread.At {
	return leanread.At{Start: skip == 0 && pos.IsStart(), LineStart: pos.Column() == 0}
}

// TestLeanOf checks that only a *Reader has the lean read path. A type that
// embeds one keeps the public read path, so its own methods are called.
func TestLeanOf(t *testing.T) {
	if _, ok := leanread.Of(NewReader(strings.NewReader("a"))); !ok {
		t.Error("leanread.Of(*Reader) = false")
	}
	type wrapped struct{ *Reader }
	if _, ok := leanread.Of(wrapped{NewReader(strings.NewReader("a"))}); ok {
		t.Error("leanread.Of(a type that embeds *Reader) = true")
	}
	if _, ok := leanread.Of((*Reader)(nil)); ok {
		t.Error("leanread.Of(nil *Reader) = true")
	}
	if _, ok := leanread.Of(nil); ok {
		t.Error("leanread.Of(nil) = true")
	}
}

// TestLeanPeekDoesNotMove checks that Peek leaves the logical cursor, the
// UnreadRune state and the retained input as they were.
func TestLeanPeekDoesNotMove(t *testing.T) {
	r := NewReader(strings.NewReader("ab\ncdefgh"))
	lean, _ := leanread.Of(r)
	if _, _, err := r.ReadRune(); err != nil {
		t.Fatal(err)
	}
	before := r.Cursor()
	c := r.Checkpoint()
	retained := r.RetainedBytes()

	dst := make([]leanread.Rune, 8)
	n, at, err := lean.Peek(0, dst)
	if err != nil || n < 2 {
		t.Fatalf("Peek(0) = %d, %v; want at least 2 runes and nil", n, err)
	}
	if dst[0].Rune != 'b' || dst[1].Rune != '\n' {
		t.Fatalf("Peek(0) decoded %q, %q; want 'b', '\\n'", dst[0].Rune, dst[1].Rune)
	}
	if at != (leanread.At{}) {
		t.Errorf("Peek(0) at %+v, want neither the start of the stream nor of a line", at)
	}
	if _, at, _ = lean.Peek(2, dst); !at.LineStart || at.Start {
		t.Errorf("Peek(2) at %+v, want the start of a line", at)
	}
	if got := r.Cursor(); got != before {
		t.Errorf("Peek moved the cursor from %+v to %+v", before, got)
	}
	if got := r.RetainedBytes(); got != retained {
		t.Errorf("Peek changed the retained bytes from %d to %d", retained, got)
	}
	if err := c.Release(); err != nil {
		t.Fatal(err)
	}
	if err := r.UnreadRune(); err != nil {
		t.Errorf("UnreadRune after ReadRune and Peek: %v; Peek must not end the unread state", err)
	}
}
