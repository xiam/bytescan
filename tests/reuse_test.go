package tests

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
)

// TestScannerMarkReuseLongSequence covers a long stream of tokens on a finite
// cursor whose budget is exactly the context window (markWindow+1 tokens). A
// cursor with the Remark capability re-arms a bounded set of mark slots, so the
// retained region stays bounded and the whole stream lexes. If the scanner leaked
// an extra active mark — the reuse path growing beyond the window — the budget
// would be exceeded and the stream would stop early.
func TestScannerMarkReuseLongSequence(t *testing.T) {
	const n = 512

	// Budget is exactly the window: three committed tokens plus the in-flight
	// token, each one byte. A fifth active mark would push the retained region
	// to five bytes and trip the limit.
	cur := cursor.NewReader(strings.NewReader(strings.Repeat("a", n)),
		cursor.WithMaxRetained(4))

	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule(lexTypeWord, newExactRuneRule('a'))

	got := 0
	for {
		lex, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		assert.Equal(t, "a", lex.Text())
		got++
	}

	assert.Equal(t, n, got, "the full stream must lex without a retention error")
}

// TestScannerMarkEvictionExact covers the exact three-token window: after enough
// tokens commit, the three most recent remain readable and every earlier one is
// evicted, with the retained region clamped to exactly those three.
func TestScannerMarkEvictionExact(t *testing.T) {
	const input = "a b c d e f"

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	lexes := lexAll(t, lx)
	require.Len(t, lexes, 11)

	// After the final commit the window holds the three most recent tokens
	// (d, " ", e); everything earlier has fallen out and its input is released.
	for i := 8; i < 11; i++ {
		_, err := lx.Context(lexes[i], 0, 0)
		assert.NoError(t, err, "token %d (%q) is inside the window", i, lexes[i].Text())
	}
	for i := 0; i < 8; i++ {
		_, err := lx.Context(lexes[i], 0, 0)
		assert.ErrorIs(t, err, cursor.ErrPositionOutOfBuffer,
			"token %d (%q) has fallen out of the window", i, lexes[i].Text())
	}

	// The retained region is exactly the window: a wider `before` clamps to the
	// oldest retained token (e) and never reaches the evicted d or c.
	ctx, err := lx.Context(lexes[10], 5, 0)
	require.NoError(t, err)
	assert.Equal(t, "e f", ctx, "before reaches exactly to the oldest retained token")

	ctx, err = lx.Context(lexes[8], 0, 8)
	require.NoError(t, err)
	assert.Equal(t, "e f", ctx, "after reaches to the end of the stream")
}

// TestScannerMarkReuseAfterFailures covers a failed Next (a token that grows past
// the cursor's finite retention): the call commits nothing, the previous
// token's context survives, and the failure is repeatable without leaking a
// mark or corrupting the window.
func TestScannerMarkReuseAfterFailures(t *testing.T) {
	// "ccccc" grows past the budget while "ab " is committed.
	cur := cursor.NewReader(strings.NewReader("ab ccccc"), cursor.WithMaxRetained(6))

	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	first, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, "ab", first.Text())

	second, err := lx.Next()
	require.NoError(t, err)
	require.Equal(t, " ", second.Text())

	// The next token exceeds the retention budget, so it fails and consumes
	// nothing. Repeated failures must keep the previous context intact and not
	// leak a mark that would widen the retained region.
	for i := 0; i < 3; i++ {
		lex, err := lx.Next()
		require.Error(t, err)
		assert.Nil(t, lex)
		assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)

		ctx, err := lx.Context(second, 0, 0)
		require.NoError(t, err)
		assert.Equal(t, " ", ctx, "a failed Next must not release the previous context")
	}
}

// TestScannerMarkReuseAfterRelease covers Release followed by further tokens: the
// scanner must drop its window, then re-arm the mark slots and keep tokenizing
// correctly with the exact window restored.
func TestScannerMarkReuseAfterRelease(t *testing.T) {
	const input = "a b c d e f"

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	// Commit six tokens (a, " ", b, " ", c, " "), then release the window.
	lexes := make([]*bytescan.Token, 0, 11)
	for i := 0; i < 6; i++ {
		lex, err := lx.Next()
		require.NoError(t, err)
		lexes = append(lexes, lex)
	}

	lx.Release()

	// Release drops every mark, so no previously committed token resolves any
	// more.
	for i := 0; i < 6; i++ {
		_, err := lx.Context(lexes[i], 0, 0)
		assert.ErrorIs(t, err, cursor.ErrPositionOutOfBuffer, "token %d must be dropped by Release", i)
	}

	// Further tokens still lex. Release left every slot holding a released
	// checkpoint, so the next commits re-arm those values in place and the
	// ring is back in steady state at once.
	for i := 6; i < 11; i++ {
		lex, err := lx.Next()
		require.NoError(t, err)
		lexes = append(lexes, lex)
	}

	// The window is restored exactly around the last three commits (e, " ", f);
	// the two oldest post-Release marks (d, " ") are evicted.
	for i := 6; i < 8; i++ {
		_, err := lx.Context(lexes[i], 0, 0)
		assert.ErrorIs(t, err, cursor.ErrPositionOutOfBuffer, "token %d is evicted", i)
	}
	for i := 8; i < 11; i++ {
		_, err := lx.Context(lexes[i], 0, 0)
		assert.NoError(t, err, "token %d is inside the restored window", i)
	}

	ctx, err := lx.Context(lexes[10], 5, 0)
	require.NoError(t, err)
	assert.Equal(t, "e f", ctx, "the window is restored around the new tokens")
}

// plainCursor wraps a *cursor.Reader and exposes only the four-method
// Cursor interface — deliberately not the Remark capability. It models an
// injected cursor without reuse support, which must keep the existing
// allocating behavior.
type plainCursor struct {
	inner *cursor.Reader
}

func (p *plainCursor) ReadLocatedRune() (cursor.LocatedRune, error) {
	return p.inner.ReadLocatedRune()
}

func (p *plainCursor) Cursor() cursor.Pos { return p.inner.Cursor() }

func (p *plainCursor) Checkpoint() *cursor.Checkpoint {
	return p.inner.Checkpoint()
}

func (p *plainCursor) Seek(offset int64, whence int) (int64, error) {
	return p.inner.Seek(offset, whence)
}

// TestScannerMarkFallbackWithoutRemark covers an injected cursor that lacks the
// Remark capability: the scanner must still tokenize correctly and keep the
// existing allocating behavior — allocating a checkpoint per token rather than
// re-arming a bounded set of slots.
func TestScannerMarkFallbackWithoutRemark(t *testing.T) {
	const input = "a b c d e f g h"

	newFallback := func() *bytescan.Scanner {
		lx := bytescan.NewWithCursor(&plainCursor{inner: cursor.NewReader(strings.NewReader(input))})
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		lx.MustAddRule(lexTypeSpace, newSpaceRule())
		return lx
	}
	newReuse := func() *bytescan.Scanner {
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		lx.MustAddRule(lexTypeSpace, newSpaceRule())
		return lx
	}

	// Behavior is unchanged: the fallback lexes the identical stream to the
	// reuse path.
	assert.Equal(t, tokenKeys(lexAll(t, newReuse())), tokenKeys(lexAll(t, newFallback())),
		"a cursor without the Remark capability must lex identically to one with it")

	// Warm both to steady state (past the first pass over the four slots), so
	// the reuse path is re-arming slots and the fallback path is allocating.
	fallback := newFallback()
	reuse := newReuse()
	for i := 0; i < 4; i++ {
		_, err := fallback.Next()
		require.NoError(t, err, "fallback warm %d", i)
		_, err = reuse.Next()
		require.NoError(t, err, "reuse warm %d", i)
	}

	// Measure the allocations of one further, steady-state token on each. The
	// fallback must still allocate a checkpoint per token; the reuse path
	// re-arms a slot and does not.
	fbAllocs := testing.AllocsPerRun(1, func() {
		_, err := fallback.Next()
		require.NoError(t, err)
	})
	reAllocs := testing.AllocsPerRun(1, func() {
		_, err := reuse.Next()
		require.NoError(t, err)
	})

	assert.Greater(t, fbAllocs, reAllocs,
		"a cursor without the Remark capability must still allocate a checkpoint per token (fallback=%d, reuse=%d)",
		fbAllocs, reAllocs)
}

// modeSwitchDriver alternates two scanners over one shared cursor, the documented
// mode-driver pattern: the word scanner takes one token, then the space scanner
// takes one, and so on. With release set, each scanner calls Release before it
// hands the cursor to the other one (the release-on-switch rule).
type modeSwitchDriver struct {
	words, spaces *bytescan.Scanner
	release       bool
}

func newModeSwitchDriver(cur bytescan.Cursor, release bool) *modeSwitchDriver {
	words := bytescan.NewWithCursor(cur)
	words.MustAddRule(lexTypeWord, newASCIILettersRule())

	spaces := bytescan.NewWithCursor(cur)
	spaces.MustAddRule(lexTypeSpace, newSpaceRule())

	return &modeSwitchDriver{words: words, spaces: spaces, release: release}
}

// cycle lexes one word and one space, switching scanners twice.
func (d *modeSwitchDriver) cycle(t *testing.T) {
	for _, lx := range []*bytescan.Scanner{d.words, d.spaces} {
		_, err := lx.Next()
		require.NoError(t, err)
		if d.release {
			lx.Release()
		}
	}
}

// modeSwitchInput is long enough for the warm-up and every measured cycle.
var modeSwitchInput = strings.Repeat("ab ", 4096)

// TestScannerReleaseOnSwitchAllocatesNoCheckpoint covers a mode driver that calls
// Release on every switch. Release keeps the released checkpoints in their
// slots, so the next Next re-arms them in place: a switch with Release costs no
// allocation more than a switch without it.
func TestScannerReleaseOnSwitchAllocatesNoCheckpoint(t *testing.T) {
	measure := func(release bool) float64 {
		d := newModeSwitchDriver(cursor.NewReader(strings.NewReader(modeSwitchInput)), release)

		// Warm up past the first pass over the mark slots of both scanners, so
		// every slot holds a checkpoint value.
		for i := 0; i < 8; i++ {
			d.cycle(t)
		}

		return testing.AllocsPerRun(200, func() { d.cycle(t) })
	}

	without := measure(false)
	with := measure(true)

	assert.LessOrEqual(t, with, without,
		"Release on every switch must not allocate more than no Release (with=%v, without=%v allocations per cycle)",
		with, without)
}

// TestScannerReleaseOnSwitchRetention covers the retained input across Release on
// every switch: each Release still returns every checkpoint the scanner holds to
// the cursor, so the cursor retains nothing after it, as with a fresh scanner.
func TestScannerReleaseOnSwitchRetention(t *testing.T) {
	cur := cursor.NewReader(strings.NewReader(modeSwitchInput))
	d := newModeSwitchDriver(cur, false)

	for i := 0; i < 16; i++ {
		for _, lx := range []*bytescan.Scanner{d.words, d.spaces} {
			_, err := lx.Next()
			require.NoError(t, err)
			require.Greater(t, cur.RetainedBytes(), 0, "cycle %d: a committed token holds its context", i)

			lx.Release()
			assert.Zero(t, cur.RetainedBytes(), "cycle %d: Release returns all retained input to the cursor", i)
		}
	}
}

// TestScannerReleaseOnSwitchWithoutRemark covers Release on every switch over a
// cursor that lacks the Remark capability: the scanners still lex the stream
// correctly, and each token still allocates a fresh checkpoint.
func TestScannerReleaseOnSwitchWithoutRemark(t *testing.T) {
	const cycles = 64

	lexCycles := func(cur bytescan.Cursor) []string {
		d := newModeSwitchDriver(cur, true)
		got := make([]string, 0, 2*cycles)
		for i := 0; i < cycles; i++ {
			for _, lx := range []*bytescan.Scanner{d.words, d.spaces} {
				lex, err := lx.Next()
				require.NoError(t, err)
				got = append(got, string(lex.Type())+":"+lex.Text())
				lx.Release()
			}
		}
		return got
	}

	plain := lexCycles(&plainCursor{inner: cursor.NewReader(strings.NewReader(modeSwitchInput))})
	reuse := lexCycles(cursor.NewReader(strings.NewReader(modeSwitchInput)))
	assert.Equal(t, reuse, plain, "a cursor without the Remark capability must lex identically")
	assert.Equal(t, "WORD:ab", plain[0])
	assert.Equal(t, "SPACE: ", plain[1])

	measure := func(cur bytescan.Cursor) float64 {
		d := newModeSwitchDriver(cur, true)
		for i := 0; i < 8; i++ {
			d.cycle(t)
		}
		return testing.AllocsPerRun(200, func() { d.cycle(t) })
	}

	fbAllocs := measure(&plainCursor{inner: cursor.NewReader(strings.NewReader(modeSwitchInput))})
	reAllocs := measure(cursor.NewReader(strings.NewReader(modeSwitchInput)))
	assert.Greater(t, fbAllocs, reAllocs,
		"a cursor without the Remark capability must still allocate a checkpoint per token (fallback=%v, reuse=%v)",
		fbAllocs, reAllocs)
}

// BenchmarkModeSwitch measures one mode-driver cycle (two switches) with and
// without Release on every switch. Both report the same allocations per cycle.
func BenchmarkModeSwitch(b *testing.B) {
	for _, release := range []bool{false, true} {
		name := "NoRelease"
		if release {
			name = "Release"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			input := strings.Repeat("ab ", b.N+1)
			d := newModeSwitchDriver(cursor.NewReader(strings.NewReader(input)), release)
			scanners := []*bytescan.Scanner{d.words, d.spaces}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, lx := range scanners {
					if _, err := lx.Next(); err != nil {
						b.Fatal(err)
					}
					if release {
						lx.Release()
					}
				}
			}
		})
	}
}
