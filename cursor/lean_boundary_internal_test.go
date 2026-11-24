package cursor

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/xiam/bytescan/internal/leanread"
)

// boundaryRing is the number of checkpoint slots of the twin test below. It
// is the number of slots of the scanner (bytescan.markSlots).
const boundaryRing = 4

// TestLeanBoundaryMatchesRemarkAndRelease runs a scanner's marks over twin
// readers. On one reader, each token is marked with Mark and committed with
// AdvanceRelease, as the scanner does on the lean read path. On the other, it
// is marked with Remark and Pos, read with ReadLocatedRune, and committed with
// Release, as the scanner does on the public read path. Both keep a ring of
// boundaryRing checkpoints and release the oldest one at each commit. After
// each step, the positions, the errors, the cursor, the retained bytes, the
// epoch and the state of every checkpoint must be the same on both readers.
// At some boundaries, both readers reset to a checkpoint of the ring.
func TestLeanBoundaryMatchesRemarkAndRelease(t *testing.T) {
	for _, src := range leanSources() {
		for i, text := range leanInputs {
			for _, batch := range []int{1, 3, 16} {
				name := fmt.Sprintf("%s/%d/batch%d", src.name, i, batch)
				t.Run(name, func(t *testing.T) {
					boundaryTwin(t, src.make(text), src.make(text), batch, int64(i*29+batch))
				})
			}
		}
	}
}

func boundaryTwin(t *testing.T, a, b *Reader, batch int, seed int64) {
	t.Helper()

	lean, ok := leanread.Of(a)
	if !ok {
		t.Fatal("leanread.Of(*Reader) reports no lean read path")
	}
	boundary, ok := lean.(leanread.Boundary[Pos, *Checkpoint])
	if !ok {
		t.Fatal("the lean read path of *Reader has no boundary operations")
	}

	rng := rand.New(rand.NewSource(seed))
	dst := make([]leanread.Rune, batch)
	var ringA, ringB [boundaryRing]*Checkpoint
	next := 0

	compare := func(step string) {
		t.Helper()
		if got, want := a.Cursor(), b.Cursor(); got != want {
			t.Fatalf("%s: cursor %+v, want %+v", step, got, want)
		}
		if got, want := a.RetainedBytes(), b.RetainedBytes(); got != want {
			t.Fatalf("%s: retained %d bytes, want %d", step, got, want)
		}
		if got, want := a.epoch.Load(), b.epoch.Load(); got != want {
			t.Fatalf("%s: epoch %d, want %d", step, got, want)
		}
		for k := range ringA {
			if got, want := ringA[k].Active(), ringB[k].Active(); got != want {
				t.Fatalf("%s: slot %d active %v, want %v", step, k, got, want)
			}
		}
	}

	// mark arms the in-flight slot on both readers.
	mark := func() {
		t.Helper()
		var posA, posB Pos
		if ringA[next] == nil {
			ringA[next], ringB[next] = a.Checkpoint(), b.Checkpoint()
			posA, posB = ringA[next].Pos(), ringB[next].Pos()
		} else {
			var errA error
			posA, errA = boundary.Mark(ringA[next])
			errB := b.Remark(ringB[next])
			if errText(errA) != errText(errB) {
				t.Fatalf("Mark: %v, Remark: %v", errA, errB)
			}
			if errB == nil {
				posB = ringB[next].Pos()
				if got := ringA[next].Pos(); got != posA {
					t.Fatalf("Mark returned %+v, Pos %+v", posA, got)
				}
			}
		}
		if posA != posB {
			t.Fatalf("marked %+v, want %+v", posA, posB)
		}
		compare("mark")
	}

	// abandon ends a token that committed nothing, as the scanner does: both
	// readers reset to the in-flight mark and release it.
	abandon := func() {
		t.Helper()
		errA, errB := ringA[next].Reset(), ringB[next].Reset()
		if errText(errA) != errText(errB) {
			t.Fatalf("abandon: Reset %v, want %v", errA, errB)
		}
		_ = ringA[next].Release()
		_ = ringB[next].Release()
		compare("abandon")
	}

	for steps, retries := 0, 0; steps < 200; steps++ {
		mark()

		n, at, errA := lean.Peek(0, dst)
		if errA != nil {
			_, errB := b.ReadLocatedRune()
			if errText(errA) != errText(errB) {
				t.Fatalf("Peek: %v, ReadLocatedRune: %v", errA, errB)
			}
			abandon()
			if errors.Is(errA, ErrMoreInput) && retries < 50 {
				retries++
				continue
			}
			return
		}

		old := (next + 1) % boundaryRing
		if at.Partial {
			// The scanner moves over a rune that a Peek decoded from an
			// incomplete sequence with Seek, and commits with Release.
			if _, err := a.Seek(int64(dst[0].Size), io.SeekCurrent); err != nil {
				t.Fatalf("Seek over a partial rune: %v", err)
			}
			if _, err := b.ReadLocatedRune(); err != nil {
				t.Fatalf("ReadLocatedRune of a partial rune: %v", err)
			}
			_ = ringA[old].Release()
			_ = ringB[old].Release()
		} else {
			k := 1 + rng.Intn(n)
			bytes := 0
			runes := make([]rune, k)
			for j := 0; j < k; j++ {
				bytes += dst[j].Size
				runes[j] = dst[j].Rune
				if _, err := b.ReadLocatedRune(); err != nil {
					t.Fatalf("ReadLocatedRune %d of %d: %v", j, k, err)
				}
			}
			got, err := boundary.AdvanceRelease(bytes, extentOf(runes), ringA[old])
			if err != nil {
				t.Fatalf("AdvanceRelease(%d): %v", bytes, err)
			}
			_ = ringB[old].Release()
			if want := b.Cursor(); got != want {
				t.Fatalf("AdvanceRelease(%d) returned %+v, want %+v", bytes, got, want)
			}
		}
		next = old
		compare("commit")

		if rng.Intn(5) == 0 {
			// A caller resets to a committed mark at the boundary.
			k := rng.Intn(boundaryRing)
			if ringA[k] != nil {
				errA, errB := ringA[k].Reset(), ringB[k].Reset()
				if errText(errA) != errText(errB) {
					t.Fatalf("Reset of slot %d: %v, want %v", k, errA, errB)
				}
				compare("reset")
			}
		}
	}
}

// TestLeanAdvanceReleaseFailureKeepsOld checks that AdvanceRelease that fails
// moves nothing and releases nothing: the retention limit refuses the move,
// and the old checkpoint stays active.
func TestLeanAdvanceReleaseFailureKeepsOld(t *testing.T) {
	r := NewReader(strings.NewReader("abcdefghij"), WithMaxRetained(4))
	old := r.Checkpoint()
	lean, _ := leanread.Of(r)
	dst := make([]leanread.Rune, 4)
	if _, _, err := lean.Peek(0, dst); err != nil {
		t.Fatal(err)
	}
	before := r.Cursor()
	// The buffer holds six bytes or more, and four is the limit.
	if held := r.w - r.r; held < 6 {
		t.Fatalf("the buffer holds %d bytes, want 6 or more", held)
	}
	_, err := lean.(leanread.Boundary[Pos, *Checkpoint]).AdvanceRelease(6, leanread.Extent{Runes: 6}, old)
	if !errors.Is(err, ErrRetentionExceeded) {
		t.Fatalf("AdvanceRelease(6) = %v, want ErrRetentionExceeded", err)
	}
	if got := r.Cursor(); got != before {
		t.Errorf("cursor %+v after a failed AdvanceRelease, want %+v", got, before)
	}
	if !old.Active() {
		t.Error("a failed AdvanceRelease released the old checkpoint")
	}
}

// TestLeanAdvanceReleaseOldStates checks that AdvanceRelease moves the cursor,
// and does not fail, with an old checkpoint that is nil, already released, or
// active on another reader. It releases only a checkpoint that is active on
// its own reader.
func TestLeanAdvanceReleaseOldStates(t *testing.T) {
	other := NewReader(strings.NewReader("xyz"))
	onOther := other.Checkpoint()
	released := other.Checkpoint()
	if err := released.Release(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		old  *Checkpoint
	}{
		{"nil", nil},
		{"released", released},
		{"on another reader", onOther},
	} {
		r := NewReader(strings.NewReader("ab\ncd"))
		lean, _ := leanread.Of(r)
		dst := make([]leanread.Rune, 8)
		if _, _, err := lean.Peek(0, dst); err != nil {
			t.Fatal(err)
		}
		got, err := lean.(leanread.Boundary[Pos, *Checkpoint]).AdvanceRelease(4, leanread.Extent{Runes: 4, Lines: 1, Column: 1}, tc.old)
		want := Pos{byteOffset: 4, runeOffset: 4, line: 2, column: 1}
		if err != nil || got != want {
			t.Errorf("%s: AdvanceRelease(4) = %+v, %v; want %+v", tc.name, got, err, want)
		}
	}
	if !onOther.Active() {
		t.Error("AdvanceRelease released a checkpoint of another reader")
	}
	if released.Active() {
		t.Error("a released checkpoint became active")
	}
}

// TestLeanMarkFailsAsRemark checks that Mark refuses what Remark refuses,
// with the same errors, and leaves the checkpoint as it was.
func TestLeanMarkFailsAsRemark(t *testing.T) {
	r := NewReader(strings.NewReader("abc"))
	lean, _ := leanread.Of(r)
	boundary := lean.(leanread.Boundary[Pos, *Checkpoint])

	if _, err := boundary.Mark(nil); !errors.Is(err, ErrCheckpointReleased) {
		t.Errorf("Mark(nil) = %v, want ErrCheckpointReleased", err)
	}
	active := r.Checkpoint()
	if _, err := boundary.Mark(active); !errors.Is(err, ErrCheckpointActive) {
		t.Errorf("Mark(active) = %v, want ErrCheckpointActive", err)
	}
	if !active.Active() {
		t.Error("a refused Mark released the checkpoint")
	}
	if got, want := r.epoch.Load(), uint64(1); got != want {
		t.Errorf("epoch %d after a refused Mark, want %d", got, want)
	}
}
