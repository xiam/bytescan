package tests

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
)

// The tests in this file check the work of the scanner at the boundary between
// two tokens: the marks of the context window, the commit, and what a caller
// sees of the cursor between two calls of Next. Each test runs one script
// twice: over a *cursor.Reader, whose scanner takes the lean read path and
// its boundary operations, and over the same input through boundaryCursor,
// whose scanner takes the public read path, with Remark, Pos and Release. Every
// token, Lookahead record, context, error, cursor position and retained byte
// count that the script records must be the same on both runs.

// boundaryCursor embeds a *cursor.Reader. It is not a *cursor.Reader, so a
// scanner over it takes the public read path and the public checkpoint
// methods.
type boundaryCursor struct{ *cursor.Reader }

// boundaryRun is one run of a script: a cursor, a way to build scanners over
// it on one of the two read paths, and the log the script writes.
type boundaryRun struct {
	t    *testing.T
	lean bool
	cur  *cursor.Reader
	toks []*bytescan.Token
	log  []string
}

// scanner returns a new scanner over the cursor of the run, with
// boundaryRules.
func (r *boundaryRun) scanner() *bytescan.Scanner {
	var c bytescan.Cursor = r.cur
	if !r.lean {
		c = boundaryCursor{r.cur}
	}
	sc := bytescan.NewWithCursor(c)
	boundaryRules(sc)
	return sc
}

// boundaryRules adds rules with and without a lookahead past the token, so
// some commits carry symbols to the next token and some do not.
func boundaryRules(sc *bytescan.Scanner) {
	sc.MustAddRule(lexTypeWord, newASCIILettersRule())
	sc.MustAddRule(lexTypeSpace, newWhitespaceRule())
	sc.MustAddRule("FLOAT", newUnsignedFloatRule())
	sc.MustAddRule(lexTypeNumber, newUnsignedIntegerRule())
}

// next calls NextLookahead on sc and logs the result, the context of the
// last tokens, the cursor and the retained bytes. It returns the error.
func (r *boundaryRun) next(sc *bytescan.Scanner) error {
	tok, la, err := sc.NextLookahead()
	if err != nil {
		r.log = append(r.log, fmt.Sprintf("error %v at %+v retained %d", err, r.cur.Cursor(), r.cur.RetainedBytes()))
		return err
	}
	r.toks = append(r.toks, tok)
	line := fmt.Sprintf("%s %q %v %d-%d %+v cursor %+v retained %d",
		tok.Type(), tok.Text(), tok.Span(), tok.Span().Start().ByteOffset(), tok.Span().End().ByteOffset(),
		la, r.cur.Cursor(), r.cur.RetainedBytes())
	for back := 0; back < 5 && back < len(r.toks); back++ {
		ctx, cerr := sc.Context(r.toks[len(r.toks)-1-back], 3, 3)
		if cerr != nil {
			ctx = "error: " + cerr.Error()
		}
		line += fmt.Sprintf(" ctx[-%d] %q", back, ctx)
	}
	r.log = append(r.log, line)
	return nil
}

// drain calls next on sc until it returns an error, and returns that error.
func (r *boundaryRun) drain(sc *bytescan.Scanner) error {
	for i := 0; i < 1000; i++ {
		if err := r.next(sc); err != nil {
			return err
		}
	}
	require.FailNow(r.t, "drain: no error after 1000 tokens")
	return nil
}

// runBoundaryScript runs script on both read paths over cursors that newCursor
// builds, and compares the logs.
func runBoundaryScript(t *testing.T, newCursor func() *cursor.Reader, script func(r *boundaryRun)) {
	t.Helper()
	lean := &boundaryRun{t: t, lean: true, cur: newCursor()}
	public := &boundaryRun{t: t, lean: false, cur: newCursor()}
	script(lean)
	script(public)
	require.NotEmpty(t, public.log, "the script logged nothing")
	assert.Equal(t, public.log, lean.log, "the lean read path must log what the public read path logs")
}

// boundaryInput has words, runs of spaces and newlines, integers, floats whose
// rule reads one symbol past the token, multi-byte runes and an unknown rune.
const boundaryInput = "alpha 12 3.5x\n\n beta 7..9 gamma\t1.\nδέλτα 42 ε .5 zz"

// TestBoundaryRetentionBudget checks the retention budget at the boundary: with
// a budget that fits the window of short tokens only, a long token fails with
// cursor.ErrRetentionExceeded at the same point on both paths, consumes
// nothing, and fails again on a second call.
func TestBoundaryRetentionBudget(t *testing.T) {
	inputs := []string{
		"a b c d e f g h i j k",
		"ab cd ef ghijklmnop qr st",
		"a 1 b 2.5 c 3 dddddddddd e",
		boundaryInput,
	}
	for _, budget := range []int{4, 6, 9, 12, 20} {
		for i, input := range inputs {
			t.Run(fmt.Sprintf("budget%d/%d", budget, i), func(t *testing.T) {
				runBoundaryScript(t, func() *cursor.Reader {
					return cursor.NewReader(strings.NewReader(input), cursor.WithMaxRetained(budget))
				}, func(r *boundaryRun) {
					sc := r.scanner()
					err := r.drain(sc)
					if !errors.Is(err, io.EOF) {
						_ = r.next(sc)
					}
				})
			})
		}
	}
}

// TestBoundaryCallerCheckpoint checks a checkpoint that a caller takes on the
// shared cursor between two tokens: the scanner goes on, the caller resets to
// the checkpoint, and the scanner lexes the same input again from there. It
// also checks a caller that moves the cursor back between two tokens.
func TestBoundaryCallerCheckpoint(t *testing.T) {
	for at := 0; at < 6; at++ {
		t.Run(fmt.Sprintf("after%d", at), func(t *testing.T) {
			runBoundaryScript(t, func() *cursor.Reader {
				return cursor.NewReader(strings.NewReader(boundaryInput))
			}, func(r *boundaryRun) {
				sc := r.scanner()
				for i := 0; i < at; i++ {
					require.NoError(t, r.next(sc))
				}
				cp := r.cur.Checkpoint()
				for i := 0; i < 3; i++ {
					require.NoError(t, r.next(sc))
				}
				require.NoError(t, cp.Reset())
				require.NoError(t, cp.Release())
				for i := 0; i < 3; i++ {
					require.NoError(t, r.next(sc))
				}
				_, err := r.cur.Seek(-1, io.SeekCurrent)
				r.log = append(r.log, fmt.Sprintf("seek back: %v", err))
				_ = r.drain(sc)
			})
		})
	}
}

// TestBoundaryModeSwitch checks two scanners, one per mode, that share one
// cursor and switch between tokens, with Release on every switch and without
// it.
func TestBoundaryModeSwitch(t *testing.T) {
	input := strings.Repeat("ab  cd 1.5 ", 20)
	for _, release := range []bool{true, false} {
		t.Run(fmt.Sprintf("release=%v", release), func(t *testing.T) {
			runBoundaryScript(t, func() *cursor.Reader {
				return cursor.NewReader(strings.NewReader(input), cursor.WithMaxRetained(64))
			}, func(r *boundaryRun) {
				words := r.scanner()
				spaces := r.scanner()
				for i := 0; ; i++ {
					sc := words
					if i%2 == 1 {
						sc = spaces
					}
					if err := r.next(sc); err != nil {
						break
					}
					if release {
						sc.Release()
					}
				}
			})
		})
	}
}

// TestBoundaryMoreInput checks bytescan.ErrMoreInput at a boundary and inside
// a token: the input arrives in chunks that end at every byte offset of a
// short text, so some chunks end exactly at the end of a token and some end
// inside one, or inside a multi-byte rune.
func TestBoundaryMoreInput(t *testing.T) {
	const input = "ab 1.5 cd\né 22 x"
	for cut := 1; cut < len(input); cut++ {
		t.Run(fmt.Sprintf("cut%d", cut), func(t *testing.T) {
			sources := map[*cursor.Reader]*feedSource{}
			runBoundaryScript(t, func() *cursor.Reader {
				src := &feedSource{}
				cur := cursor.NewReader(src)
				sources[cur] = src
				return cur
			}, func(r *boundaryRun) {
				s := sources[r.cur]
				sc := r.scanner()
				s.feed(input[:cut])
				err := r.drain(sc)
				require.ErrorIs(t, err, bytescan.ErrMoreInput)
				s.feed(input[cut:])
				s.end()
				err = r.drain(sc)
				require.ErrorIs(t, err, io.EOF)
			})
		})
	}
}

// TestBoundaryConcurrentScanners runs scanners on separate cursors in parallel,
// each on its own goroutine, and checks that each gets the tokens of a scanner
// that runs alone. Run with -race: the scanners share no state, and the
// boundary operations of each cursor take only its own locks.
func TestBoundaryConcurrentScanners(t *testing.T) {
	input := strings.Repeat(boundaryInput+" ", 50)

	lexAllText := func(cur *cursor.Reader) ([]string, error) {
		sc := bytescan.NewWithCursor(cur)
		boundaryRules(sc)
		var out []string
		for {
			tok, err := sc.Next()
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			if err != nil {
				return out, err
			}
			out = append(out, fmt.Sprintf("%s %q %v", tok.Type(), tok.Text(), tok.Span()))
		}
	}

	want, err := lexAllText(cursor.NewReader(strings.NewReader(input)))
	require.NoError(t, err)

	const goroutines = 8
	results := make([][]string, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var cur *cursor.Reader
			if g%2 == 0 {
				cur = cursor.NewReader(strings.NewReader(input))
			} else {
				cur = cursor.NewReader(strings.NewReader(input), cursor.WithMaxRetained(256))
			}
			results[g], errs[g] = lexAllText(cur)
		}(g)
	}
	wg.Wait()

	for g := 0; g < goroutines; g++ {
		require.NoError(t, errs[g], "goroutine %d", g)
		assert.Equal(t, want, results[g], "goroutine %d", g)
	}
}
