package cursor

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/xiam/bytescan/internal/leanread"
)

// The tests of this package run Advance with the check of the extent on, so a
// wrong position from an extent fails the test that made it.
func init() {
	leanread.Check = true
}

// extentOf is the extent of runes, as the scanner computes it for a token
// (bytescan.leanExtent).
func extentOf(runes []rune) leanread.Extent {
	ext := leanread.Extent{Runes: len(runes)}
	for i, r := range runes {
		if r == '\n' {
			ext.Lines++
			ext.Column = len(runes) - i - 1
		}
	}
	return ext
}

// TestLeanAdvanceMatchesReadLocatedRune runs the twin readers of
// TestLeanPeekMatchesReadLocatedRune with Advance as the move over each token:
// the position that Advance returns, the cursor after it, after a step back
// and after a reset to the checkpoint must be the same as on the reader that
// reads each rune with ReadLocatedRune. The step back and the reset rewind the
// line table that Advance wrote.
func TestLeanAdvanceMatchesReadLocatedRune(t *testing.T) {
	for _, src := range leanSources() {
		for i, text := range leanInputs {
			for _, batch := range []int{1, 3, 16} {
				name := fmt.Sprintf("%s/%d/batch%d", src.name, i, batch)
				t.Run(name, func(t *testing.T) {
					leanTwin(t, src.make(text), src.make(text), batch, int64(i*31+batch), true)
				})
			}
		}
	}
}

// TestLeanAdvanceCheck checks that, with leanread.Check set, Advance fails on
// an extent that gives another position than the bytes do.
func TestLeanAdvanceCheck(t *testing.T) {
	wrong := []leanread.Extent{
		{Runes: 4, Lines: 1, Column: 1},
		{Runes: 5, Lines: 0},
		{Runes: 5, Lines: 1, Column: 1},
		{Runes: 5, Lines: 1, Column: 3},
	}
	for _, ext := range wrong {
		r := NewReader(strings.NewReader("ab\ncd e"))
		lean, _ := leanread.Of(r)
		dst := make([]leanread.Rune, 8)
		if _, _, err := lean.Peek(0, dst); err != nil {
			t.Fatal(err)
		}
		_, err := lean.(leanread.Advancer[Pos]).Advance(5, ext)
		if !errors.Is(err, errAdvanceMismatch) {
			t.Errorf("Advance(5, %+v) = %v, want errAdvanceMismatch", ext, err)
		}
	}

	r := NewReader(strings.NewReader("ab\ncd e"))
	lean, _ := leanread.Of(r)
	dst := make([]leanread.Rune, 8)
	if _, _, err := lean.Peek(0, dst); err != nil {
		t.Fatal(err)
	}
	got, err := lean.(leanread.Advancer[Pos]).Advance(5, leanread.Extent{Runes: 5, Lines: 1, Column: 2})
	want := Pos{byteOffset: 5, runeOffset: 5, line: 2, column: 2}
	if err != nil || got != want {
		t.Errorf("Advance(5) = %+v, %v; want %+v", got, err, want)
	}
}

// TestLeanAdvanceIsSeekOutsideTheBuffer checks that Advance over bytes that
// the buffer does not hold, or over no byte, does what Seek does on a twin
// reader, with the same errors.
func TestLeanAdvanceIsSeekOutsideTheBuffer(t *testing.T) {
	text := "héllo\nwörld"
	for _, n := range []int{0, 1, 3, 7, len(text), len(text) + 1} {
		a := NewReader(strings.NewReader(text))
		b := NewReader(strings.NewReader(text))
		lean, _ := leanread.Of(a)
		runes := []rune(text[:min(n, len(text))])
		got, errA := lean.(leanread.Advancer[Pos]).Advance(n, extentOf(runes))
		_, errB := b.Seek(int64(n), io.SeekCurrent)
		if errText(errA) != errText(errB) {
			t.Fatalf("n=%d: Advance error %v, Seek error %v", n, errA, errB)
		}
		if a.Cursor() != b.Cursor() {
			t.Fatalf("n=%d: cursor after Advance %+v, after Seek %+v", n, a.Cursor(), b.Cursor())
		}
		if errA == nil && got != b.Cursor() {
			t.Fatalf("n=%d: Advance returned %+v, want %+v", n, got, b.Cursor())
		}
	}
}
