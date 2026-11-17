package modes_test

import (
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/textreader"
)

// newWordSpace returns a word scanner and a space scanner over cur.
func newWordSpace(cur bytescan.Cursor) (words, spaces *bytescan.Scanner) {
	words = bytescan.NewWithCursor(cur)
	words.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
	spaces = bytescan.NewWithCursor(cur)
	spaces.MustAddRule("SPACE", core.RuneRun(core.RuneSet(" ")))
	return words, spaces
}

// alternate returns a SwitchFunc that hands over to the other of a and b
// after every token.
func alternate(a, b *bytescan.Scanner) modes.SwitchFunc {
	return func(active *bytescan.Scanner, _ *bytescan.Token) *bytescan.Scanner {
		if active == a {
			return b
		}
		return a
	}
}

func drain(t *testing.T, src bytescan.Source) []string {
	t.Helper()
	var out []string
	for {
		l, err := src.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		require.NotNil(t, l)
		out = append(out, string(l.Type())+":"+l.Text())
	}
}

func TestDriverSwitch(t *testing.T) {
	cur := textreader.New(strings.NewReader("ab cd ef"))
	words, spaces := newWordSpace(cur)
	d := modes.New(cur, words, modes.WithSwitch(alternate(words, spaces)))

	assert.Same(t, cur, d.Cursor())
	assert.Same(t, words, d.Active())
	assert.Equal(t, []string{"WORD:ab", "SPACE: ", "WORD:cd", "SPACE: ", "WORD:ef"}, drain(t, d))

	// io.EOF is plain and repeats, and an error does not switch the mode.
	active := d.Active()
	l, err := d.Next()
	assert.Nil(t, l)
	assert.True(t, err == io.EOF, "err = %v, want io.EOF itself", err)
	assert.Same(t, active, d.Active())
}

func TestDriverWithoutSwitch(t *testing.T) {
	cur := textreader.New(strings.NewReader("ab cd"))
	words, spaces := newWordSpace(cur)
	d := modes.New(cur, words)

	// Without a SwitchFunc the driver switches only on Use, called before
	// Next.
	for _, step := range []struct {
		lx   *bytescan.Scanner
		want string
	}{{words, "ab"}, {spaces, " "}, {words, "cd"}} {
		d.Use(step.lx)
		l, err := d.Next()
		require.NoError(t, err)
		assert.Equal(t, step.want, l.Text())
		assert.Same(t, step.lx, d.Active())
	}
	_, err := d.Next()
	assert.Equal(t, io.EOF, err)
}

// TestDriverUseReleases checks that a switch releases the outgoing scanner, so
// the cursor retains no input after it, and that Use of the active scanner does
// not release it.
func TestDriverUseReleases(t *testing.T) {
	cur := textreader.New(strings.NewReader("ab cd ef gh"))
	words, spaces := newWordSpace(cur)
	d := modes.New(cur, words)

	_, err := d.Next()
	require.NoError(t, err)
	require.Greater(t, cur.RetainedBytes(), 0, "a committed token holds its context")

	d.Use(words)
	assert.Greater(t, cur.RetainedBytes(), 0, "Use of the active scanner must not release it")

	d.Use(spaces)
	assert.Zero(t, cur.RetainedBytes(), "Use must release the outgoing scanner")

	// The same holds for a switch that the SwitchFunc picks in Next.
	d = modes.New(cur, spaces, modes.WithSwitch(alternate(words, spaces)))
	for i := 0; i < 6; i++ {
		_, err := d.Next()
		require.NoError(t, err)
		assert.Zero(t, cur.RetainedBytes(), "token %d: the switch after it must release the outgoing scanner", i)
	}
}

// TestDriverSwitchAllocs checks that a switch between two warmed scanners
// allocates nothing, and that the driver adds no allocation to a token over
// the same scanners driven by hand.
func TestDriverSwitchAllocs(t *testing.T) {
	input := strings.Repeat("abc ", 4096)

	cur := textreader.New(strings.NewReader(input))
	words, spaces := newWordSpace(cur)
	d := modes.New(cur, words, modes.WithSwitch(alternate(words, spaces)))
	for i := 0; i < 16; i++ {
		_, err := d.Next()
		require.NoError(t, err)
	}

	use := testing.AllocsPerRun(200, func() {
		d.Use(spaces)
		d.Use(words)
	})
	assert.Zero(t, use, "a switch between warmed scanners must not allocate")

	driven := testing.AllocsPerRun(200, func() {
		for i := 0; i < 2; i++ {
			if _, err := d.Next(); err != nil {
				t.Fatal(err)
			}
		}
	})

	cur2 := textreader.New(strings.NewReader(input))
	words2, spaces2 := newWordSpace(cur2)
	for i := 0; i < 8; i++ {
		for _, lx := range []*bytescan.Scanner{words2, spaces2} {
			_, err := lx.Next()
			require.NoError(t, err)
			lx.Release()
		}
	}
	byHand := testing.AllocsPerRun(200, func() {
		for _, lx := range []*bytescan.Scanner{words2, spaces2} {
			if _, err := lx.Next(); err != nil {
				t.Fatal(err)
			}
			lx.Release()
		}
	})
	assert.LessOrEqual(t, driven, byHand,
		"the driver must not allocate more per cycle than the same switches by hand (driver=%v, by hand=%v)", driven, byHand)
}

// TestDriverConcurrentNext runs Next and Use from several goroutines. Under
// -race it fails if two scanners on the cursor run at the same time. Every rune
// is read exactly once, so the lengths of the texts add up to the input.
func TestDriverConcurrentNext(t *testing.T) {
	input := strings.Repeat("ab cd ", 500)
	cur := textreader.New(strings.NewReader(input))
	words, spaces := newWordSpace(cur)
	d := modes.New(cur, words, modes.WithSwitch(alternate(words, spaces)))

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for {
				if g == 0 {
					// A concurrent Use changes which scanner
					// reads next, but every rune is still read
					// exactly once.
					d.Use(words)
				}
				l, err := d.Next()
				if err == io.EOF {
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				total += len(l.Text())
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	assert.Equal(t, len(input), total)
}

func TestDriverNilScanner(t *testing.T) {
	cur := textreader.New(strings.NewReader(""))
	words, _ := newWordSpace(cur)
	assert.Panics(t, func() { modes.New(cur, nil) })
	d := modes.New(cur, words)
	assert.Panics(t, func() { d.Use(nil) })
}

func TestCache(t *testing.T) {
	cur := textreader.New(strings.NewReader(""))
	builds := 0
	c := modes.NewCache(cur, func(got bytescan.Cursor, key int) *bytescan.Scanner {
		assert.Same(t, cur, got)
		builds++
		lx := bytescan.NewWithCursor(got)
		lx.MustAddRule("X", core.Literal(strings.Repeat("x", key+1)))
		return lx
	})

	a := c.Get(1)
	b := c.Get(2)
	assert.NotSame(t, a, b)
	assert.Same(t, a, c.Get(1))
	assert.Same(t, b, c.Get(2))
	assert.Equal(t, 2, builds)
	assert.Equal(t, 2, c.Len())

	nilCache := modes.NewCache(cur, func(bytescan.Cursor, int) *bytescan.Scanner { return nil })
	assert.Panics(t, func() { nilCache.Get(0) })
}
