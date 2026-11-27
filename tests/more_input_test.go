package tests

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/modes"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/streamtest"
)

// The tests in this file cover "need more input" (#124): a source returns
// cursor.ErrMoreInput when it has no input now, and Next returns
// bytescan.ErrMoreInput after the tokens that the rules decided.

// feedSource is an io.Reader that never blocks. Read returns the input fed so
// far. When it has none, Read returns cursor.ErrMoreInput, or io.EOF after
// end is called. reads counts the calls to Read.
type feedSource struct {
	buf   []byte
	ended bool
	reads atomic.Int64
}

func (s *feedSource) feed(p string) { s.buf = append(s.buf, p...) }

func (s *feedSource) end() { s.ended = true }

func (s *feedSource) Read(p []byte) (int, error) {
	s.reads.Add(1)
	if len(s.buf) == 0 {
		if s.ended {
			return 0, io.EOF
		}
		return 0, cursor.ErrMoreInput
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// eofProbe counts the EOF symbols that the rules of a scanner are shown.
type eofProbe struct{ seen atomic.Int64 }

// wrap returns r with every step observed: each symbol with the EOF flag
// counts in p.seen. The rule's decisions do not change.
func (p *eofProbe) wrap(r bytescan.Rule) bytescan.Rule {
	if r == nil {
		return nil
	}
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			p.seen.Add(1)
		}
		next, st := r(s)
		return p.wrap(next), st
	}
}

// addMoreInputRules registers the rule set of the tests in this file: words,
// spaces, newlines, a quoted string that may not cross a line, a block
// comment and a float. Every rule is wrapped by probe when probe is not nil.
func addMoreInputRules(sc *bytescan.Scanner, probe *eofProbe) {
	wrap := func(r bytescan.Rule) bytescan.Rule {
		if probe == nil {
			return r
		}
		return probe.wrap(r)
	}
	sc.MustAddRule("STRING", wrap(base.Quoted('"', '\\')))
	sc.MustAddRule("COMMENT", wrap(base.BlockComment("/*", "*/")))
	sc.MustAddRule("FLOAT", wrap(base.Float()))
	sc.MustAddRule("WORD", wrap(base.Word()))
	sc.MustAddRule("WS", wrap(base.Whitespace()))
	sc.MustAddRule("NL", wrap(base.Newline()))
}

// tokenLine renders a token with its type, its text and its span.
func tokenLine(tok *bytescan.Token) string {
	s := tok.Span()
	return fmt.Sprintf("%s %q @%d:%d+%d [%d,%d)", tok.Type(), tok.Text(), tok.Line(), tok.Column(), s.Runes(), s.Start().ByteOffset(), s.End().ByteOffset())
}

// scanWhole returns the tokens of one scan of input over a plain reader.
func scanWhole(t *testing.T, input string) []string {
	t.Helper()
	sc := bytescan.NewWithCursor(cursor.NewReader(strings.NewReader(input)))
	addMoreInputRules(sc, nil)
	var out []string
	for {
		tok, err := sc.Next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, tokenLine(tok))
	}
}

// drainMore calls Next until it returns an error and returns the tokens and that
// error.
func drainMore(t *testing.T, src bytescan.Source) ([]string, error) {
	t.Helper()
	res := drainMoreResult(src)
	require.NoError(t, res.failure)
	return res.tokens, res.err
}

type moreDrainResult struct {
	tokens  []string
	err     error // terminal scanner error, which callers may expect
	failure error // invariant failure for the owning test goroutine
}

func drainMoreResult(src bytescan.Source) moreDrainResult {
	var res moreDrainResult
	for i := 0; ; i++ {
		if i >= 10000 {
			res.failure = fmt.Errorf("Next did not stop after %d calls", i)
			return res
		}
		tok, err := src.Next()
		if err != nil {
			res.err = err
			if tok != nil {
				res.failure = fmt.Errorf("Next returned token %v alongside error %v", tok, err)
			}
			return res
		}
		res.tokens = append(res.tokens, tokenLine(tok))
	}
}

// drainMoreAsync always notifies its caller when the worker exits. Assertions
// belong to the caller, including the check that the worker sent a result.
func drainMoreAsync(src bytescan.Source) <-chan moreDrainResult {
	done := make(chan moreDrainResult, 1)
	go func() {
		defer close(done)
		done <- drainMoreResult(src)
	}()
	return done
}

type moreFailureSource struct {
	tok   *bytescan.Token
	err   error
	calls int
	limit int
}

func (s *moreFailureSource) Next() (*bytescan.Token, error) {
	s.calls++
	// A missing helper bound must fail the test without leaving a runaway worker.
	if s.calls > s.limit {
		return nil, io.EOF
	}
	return s.tok, s.err
}

// Both invariant failures must reach the owner through the same worker path
// as the live-source test, rather than exiting before its result send.
func TestDrainMoreWorkerFailureCompletion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		calls   int
		failure string
	}{
		{"token with error", io.EOF, 1, "alongside error"},
		{"no terminal error within bound", nil, 10000, "Next did not stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &moreFailureSource{tok: bytescan.NewTokenFromString("A", "a", 0), err: tc.err, limit: tc.calls}
			done := drainMoreAsync(src)
			select {
			case res, ok := <-done:
				require.True(t, ok, "worker exited without a result")
				require.ErrorContains(t, res.failure, tc.failure)
				assert.True(t, res.err == tc.err, "terminal error identity")
				assert.Equal(t, tc.calls, src.calls)
				select {
				case _, ok := <-done:
					assert.False(t, ok, "worker sent an extra result")
				case <-time.After(5 * time.Second):
					require.FailNow(t, "worker did not close its completion channel")
				}
			case <-time.After(5 * time.Second):
				require.FailNow(t, "worker did not report its invariant failure")
			}
		})
	}
}

// moreInputInputs is the set of inputs of the split test. It holds an open
// string, an open comment, a token that needs one more symbol to end, a
// multi-byte character that a split can cut in the middle, and the empty
// input.
var moreInputInputs = []string{
	"",
	"foo",
	"foo\n",
	"say \"hello world\" now\n",
	"say \"open",
	"a /* comment */ b\n",
	"a /* open",
	"pi 3.14 2.5e10\n",
	"héllo wörld ✓ 𝄞\n",
	"日本\n語",
}

// TestMoreInputSplit is the split test of #124. For every byte split point of
// every input, the scanner receives the first part. It returns the complete
// tokens and then ErrMoreInput. It then receives the second part and the
// declared end. The tokens of both steps together equal the tokens of one
// scan of the whole input.
func TestMoreInputSplit(t *testing.T) {
	for _, input := range moreInputInputs {
		want := scanWhole(t, input)
		for cut := 0; cut <= len(input); cut++ {
			t.Run(fmt.Sprintf("%q/%d", input, cut), func(t *testing.T) {
				src := &feedSource{}
				sc := bytescan.NewWithCursor(cursor.NewReader(src))
				addMoreInputRules(sc, nil)

				src.feed(input[:cut])
				first, err := drainMore(t, sc)
				require.Equal(t, bytescan.ErrMoreInput, err)

				// A second call without new input signals again.
				again, err := drainMore(t, sc)
				require.Equal(t, bytescan.ErrMoreInput, err)
				require.Empty(t, again)

				src.feed(input[cut:])
				src.end()
				second, err := drainMore(t, sc)
				require.Equal(t, io.EOF, err)

				assert.Equal(t, want, append(first, second...))
			})
		}
	}
}

// TestMoreInputSplitRuneSource runs the split test over a rune source
// (cursor.NewRuneReader). The split points are rune boundaries, because a
// rune source cannot cut a character.
func TestMoreInputSplitRuneSource(t *testing.T) {
	for _, input := range moreInputInputs {
		want := scanWhole(t, input)
		runes := []rune(input)
		for cut := 0; cut <= len(runes); cut++ {
			t.Run(fmt.Sprintf("%q/%d", input, cut), func(t *testing.T) {
				src := &feedRuneSource{}
				sc := bytescan.NewWithCursor(cursor.NewRuneReader(src))
				addMoreInputRules(sc, nil)

				src.feed(runes[:cut])
				first, err := drainMore(t, sc)
				require.Equal(t, bytescan.ErrMoreInput, err)

				src.feed(runes[cut:])
				src.ended = true
				second, err := drainMore(t, sc)
				require.Equal(t, io.EOF, err)

				assert.Equal(t, want, append(first, second...))
			})
		}
	}
}

// feedRuneSource is the io.RuneReader form of feedSource.
type feedRuneSource struct {
	buf   []rune
	ended bool
}

func (s *feedRuneSource) feed(p []rune) { s.buf = append(s.buf, p...) }

func (s *feedRuneSource) ReadRune() (rune, int, error) {
	if len(s.buf) == 0 {
		if s.ended {
			return 0, 0, io.EOF
		}
		return 0, 0, cursor.ErrMoreInput
	}
	r := s.buf[0]
	s.buf = s.buf[1:]
	return r, len(string(r)), nil
}

// TestMoreInputCompleteTokens pins the examples of the specification: the
// text "foo" gives no token and the signal, because the word rule still
// waits; the text "foo\n" gives WORD foo and NL, and then the signal.
func TestMoreInputCompleteTokens(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"foo", nil},
		{"foo\n", []string{`WORD "foo"`, `NL "\n"`}},
		{"say \"hel", []string{`WORD "say"`, `WS " "`}},
		{"x /* open", []string{`WORD "x"`, `WS " "`}},
		{"h\xc3", nil},
		{"ab h\xc3", []string{`WORD "ab"`, `WS " "`}},
	} {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			src := &feedSource{}
			sc := bytescan.NewWithCursor(cursor.NewReader(src))
			addMoreInputRules(sc, nil)

			src.feed(tc.input)
			var got []string
			for {
				tok, err := sc.Next()
				if err != nil {
					require.Equal(t, bytescan.ErrMoreInput, err)
					break
				}
				got = append(got, fmt.Sprintf("%s %q", tok.Type(), tok.Text()))
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMoreInputDistinctSignal checks that the signal is a value of its own:
// it is not io.EOF, and errors.Is matches it against no other exported error
// of the module, in either direction. It also checks that Next returns it
// unwrapped, even when the source wraps it, and that the call returns
// without waiting on the source.
func TestMoreInputDistinctSignal(t *testing.T) {
	require.Same(t, cursor.ErrMoreInput, bytescan.ErrMoreInput)

	others := map[string]error{
		"io.EOF":                         io.EOF,
		"io.ErrUnexpectedEOF":            io.ErrUnexpectedEOF,
		"io.ErrNoProgress":               io.ErrNoProgress,
		"bytescan.ErrDuplicateRule":      bytescan.ErrDuplicateRule,
		"bytescan.ErrEmptyRuleType":      bytescan.ErrEmptyRuleType,
		"bytescan.ErrNilRule":            bytescan.ErrNilRule,
		"bytescan.ErrNoRules":            bytescan.ErrNoRules,
		"bytescan.ErrNilToken":           bytescan.ErrNilToken,
		"bytescan.ErrContextUnsupported": bytescan.ErrContextUnsupported,
		"bytescan.ErrTokenTooLarge":      bytescan.ErrTokenTooLarge,
		"bytescan.ErrInternal":           bytescan.ErrInternal,
		"cursor.ErrRetentionExceeded":    cursor.ErrRetentionExceeded,
		"cursor.ErrCheckpointReleased":   cursor.ErrCheckpointReleased,
		"cursor.ErrCheckpointRewound":    cursor.ErrCheckpointRewound,
		"cursor.ErrCheckpointActive":     cursor.ErrCheckpointActive,
		"cursor.ErrPositionOutOfBuffer":  cursor.ErrPositionOutOfBuffer,
		"cursor.ErrBufferTooSmall":       cursor.ErrBufferTooSmall,
		"cursor.ErrInvalidUTF8":          cursor.ErrInvalidUTF8,
		"cursor.ErrSeekOutOfBuffer":      cursor.ErrSeekOutOfBuffer,
		"bytesource.ErrChunkedThen":      bytesource.ErrChunkedThen,
		"streamtest.ErrCeiling":          streamtest.ErrCeiling,
	}
	for name, other := range others {
		assert.NotErrorIs(t, bytescan.ErrMoreInput, other, "ErrMoreInput matches %s", name)
		assert.NotErrorIs(t, other, bytescan.ErrMoreInput, "%s matches ErrMoreInput", name)
	}

	t.Run("returns without waiting", func(t *testing.T) {
		src := &feedSource{}
		sc := bytescan.NewWithCursor(cursor.NewReader(src))
		addMoreInputRules(sc, nil)
		src.feed("say \"hel")

		done := drainMoreAsync(sc)
		select {
		case res, ok := <-done:
			require.True(t, ok, "worker exited without a result")
			require.NoError(t, res.failure)
			// The identity, not errors.Is: Next returns the value unwrapped.
			assert.True(t, res.err == bytescan.ErrMoreInput, "got %v", res.err)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "Next blocked on a source that returns ErrMoreInput")
		}
	})

	t.Run("a wrapped signal from the source", func(t *testing.T) {
		src := wrappedSignalSource{}
		sc := bytescan.NewWithCursor(cursor.NewReader(src))
		addMoreInputRules(sc, nil)
		tok, err := sc.Next()
		assert.Nil(t, tok)
		assert.True(t, err == bytescan.ErrMoreInput, "got %v", err)
	})

	t.Run("not sticky", func(t *testing.T) {
		src := &feedSource{}
		sc := bytescan.NewWithCursor(cursor.NewReader(src))
		addMoreInputRules(sc, nil)

		_, err := sc.Next()
		require.Equal(t, bytescan.ErrMoreInput, err)
		reads := src.reads.Load()

		src.feed("ok\n")
		tok, err := sc.Next()
		require.NoError(t, err)
		assert.Equal(t, `WORD "ok" @1:0+2 [0,2)`, tokenLine(tok))
		assert.Greater(t, src.reads.Load(), reads, "the cursor asked the source again")
	})
}

// wrappedSignalSource returns ErrMoreInput wrapped in another error.
type wrappedSignalSource struct{}

func (wrappedSignalSource) Read([]byte) (int, error) {
	return 0, fmt.Errorf("live source: %w", cursor.ErrMoreInput)
}

// TestMoreInputNoFalseEnd checks that no rule is shown a symbol with the EOF
// flag before the source returns io.EOF, for every split point of every input
// of the split test. After the end is declared, the rules are shown it as
// today.
func TestMoreInputNoFalseEnd(t *testing.T) {
	for _, input := range moreInputInputs {
		for cut := 0; cut <= len(input); cut++ {
			t.Run(fmt.Sprintf("%q/%d", input, cut), func(t *testing.T) {
				probe := &eofProbe{}
				src := &feedSource{}
				sc := bytescan.NewWithCursor(cursor.NewReader(src))
				addMoreInputRules(sc, probe)

				src.feed(input[:cut])
				_, err := drainMore(t, sc)
				require.Equal(t, bytescan.ErrMoreInput, err)
				src.feed(input[cut:])
				_, err = drainMore(t, sc)
				require.Equal(t, bytescan.ErrMoreInput, err)
				assert.Zero(t, probe.seen.Load(), "a rule saw EOF before the end was declared")

				src.end()
				_, err = drainMore(t, sc)
				require.Equal(t, io.EOF, err)
				assert.Positive(t, probe.seen.Load(), "the rules saw the declared end")
			})
		}
	}
}

// TestMoreInputTrueEnd checks that after the source returns io.EOF, the
// scanner decides the last token as it does today and returns io.EOF, and
// returns io.EOF again on every later call. An open string gives the tokens
// that its shorter matches allow, as on a plain reader.
func TestMoreInputTrueEnd(t *testing.T) {
	for _, input := range []string{"say \"open", "a /* open", "foo", "3.", "x\xc3"} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			src := &feedSource{}
			sc := bytescan.NewWithCursor(cursor.NewReader(src))
			addMoreInputRules(sc, nil)

			src.feed(input)
			first, err := drainMore(t, sc)
			require.Equal(t, bytescan.ErrMoreInput, err)

			src.end()
			second, err := drainMore(t, sc)
			require.True(t, err == io.EOF, "got %v", err)
			assert.Equal(t, scanWhole(t, input), append(first, second...))

			tok, err := sc.Next()
			assert.Nil(t, tok)
			assert.True(t, err == io.EOF, "got %v", err)
		})
	}
}

// TestMoreInputMaxTokenBytes checks that WithMaxTokenBytes still fails an
// unfinished token that grows past the bound, and that an unfinished token
// within the bound gives the signal.
func TestMoreInputMaxTokenBytes(t *testing.T) {
	newScanner := func(src *feedSource) *bytescan.Scanner {
		sc := bytescan.NewWithCursorOptions(cursor.NewReader(src), bytescan.WithMaxTokenBytes(4))
		addMoreInputRules(sc, nil)
		return sc
	}

	src := &feedSource{}
	sc := newScanner(src)
	src.feed(`"ab`)
	_, err := sc.Next()
	require.Equal(t, bytescan.ErrMoreInput, err)

	src.feed(`cdef`)
	_, err = sc.Next()
	assert.ErrorIs(t, err, bytescan.ErrTokenTooLarge)
	assert.ErrorIs(t, err, cursor.ErrRetentionExceeded)
	assert.NotErrorIs(t, err, bytescan.ErrMoreInput)
}

// TestMoreInputInsideALongToken covers the signal inside a long token. The
// run of more than 1 Mi runes of TestLongTokenPushBackAcrossTheWholeToken
// arrives in three parts that cut it at byte offsets inside runes, and each
// part ends inside the open token: Next returns ErrMoreInput and the token
// starts over on the next call. The tokens and what the pushback chains are
// shown equal those of the whole input in one read.
func TestMoreInputInsideALongToken(t *testing.T) {
	run := longTokenText(longTokenRunes)
	input := run + ";" + run

	var p halfBackProbe
	src := &feedSource{}
	sc := bytescan.NewWithCursor(cursor.NewReader(src))
	sc.MustAddRule("HALF", newHalfBackRule(&p))
	sc.MustAddRule("SEMI", newLiteralRule(";"))

	var lines, texts []string
	drain := func() error {
		for {
			tok, err := sc.Next()
			if err != nil {
				require.Nil(t, tok)
				return err
			}
			lines = append(lines, longTokenLine(tok))
			texts = append(texts, tok.Text())
		}
	}

	// Each cut is one byte past a multi-byte rune's first byte, so each part
	// ends inside a rune.
	cuts := []int{len(run) / 3, 2 * len(run) / 3, len(run)}
	for i := range cuts[:2] {
		for !utf8.RuneStart(run[cuts[i]]) || run[cuts[i]] < utf8.RuneSelf {
			cuts[i]++
		}
		cuts[i]++
	}
	prev := 0
	for _, cut := range cuts {
		src.feed(run[prev:cut])
		prev = cut
		require.Equal(t, bytescan.ErrMoreInput, drain())
		require.Empty(t, lines, "a token of the open run was returned")
	}

	src.feed(";" + run)
	require.Equal(t, bytescan.ErrMoreInput, drain())
	src.end()
	require.Equal(t, io.EOF, drain())

	half, shown := wantHalfBack([]rune(run))
	var wantTypes, wantTexts []string
	for i := range half {
		wantTypes, wantTexts = append(wantTypes, "HALF"), append(wantTexts, half[i])
	}
	wantTypes, wantTexts = append(wantTypes, "SEMI"), append(wantTexts, ";")
	for i := range half {
		wantTypes, wantTexts = append(wantTypes, "HALF"), append(wantTexts, half[i])
	}

	assert.Equal(t, longTokenWant(t, input, wantTypes, wantTexts), lines)
	assert.True(t, slices.Equal(wantTexts, texts), "token texts")
	assert.Equal(t, 2*shown, p.shown, "symbols shown by a pushback")
	assert.Zero(t, p.mismatches, "shown symbols that differ from the symbols read")
}

// TestMoreInputThroughSkipAndDriver checks that the signal passes through
// Skip and modes.Driver unchanged, with no code of their own for it, and that
// both continue after new input arrives.
func TestMoreInputThroughSkipAndDriver(t *testing.T) {
	t.Run("Skip", func(t *testing.T) {
		src := &feedSource{}
		sc := bytescan.NewWithCursor(cursor.NewReader(src))
		addMoreInputRules(sc, nil)
		s := bytescan.Skip(sc, "WS")

		src.feed("a b")
		got, err := drainMore(t, s)
		assert.True(t, err == bytescan.ErrMoreInput, "got %v", err)
		assert.Equal(t, []string{`WORD "a" @1:0+1 [0,1)`}, got)

		src.feed(" c")
		src.end()
		got, err = drainMore(t, s)
		assert.True(t, err == io.EOF, "got %v", err)
		assert.Equal(t, []string{`WORD "b" @1:2+1 [2,3)`, `WORD "c" @1:4+1 [4,5)`}, got)
	})

	t.Run("Driver", func(t *testing.T) {
		src := &feedSource{}
		cur := cursor.NewReader(src)

		code := bytescan.NewWithCursor(cur)
		code.MustAddRule("WORD", core.RuneRun(core.RuneRange('a', 'z')))
		code.MustAddRule("SPACE", core.RuneRun(core.RuneSet(" ")))
		code.MustAddRule("OPEN", core.Rune(core.RuneSet(`"`)))

		str := bytescan.NewWithCursor(cur)
		str.MustAddRule("BODY", core.RuneRun(core.RuneSet(`"`).Not()))
		str.MustAddRule("CLOSE", core.Literal(`"`))

		d := modes.New(cur, code, modes.WithSwitch(func(_ *bytescan.Scanner, l *bytescan.Token) *bytescan.Scanner {
			switch l.Type() {
			case "OPEN":
				return str
			case "CLOSE":
				return code
			}
			return nil
		}))

		src.feed(`say "hi`)
		got, err := drainMore(t, d)
		assert.True(t, err == bytescan.ErrMoreInput, "got %v", err)
		assert.Equal(t, []string{`WORD "say" @1:0+3 [0,3)`, `SPACE " " @1:3+1 [3,4)`, `OPEN "\"" @1:4+1 [4,5)`}, got)
		assert.Same(t, str, d.Active())

		src.feed(` there" x`)
		src.end()
		got, err = drainMore(t, d)
		assert.True(t, err == io.EOF, "got %v", err)
		assert.Equal(t, []string{
			`BODY "hi there" @1:5+8 [5,13)`,
			`CLOSE "\"" @1:13+1 [13,14)`,
			`SPACE " " @1:14+1 [14,15)`,
			`WORD "x" @1:15+1 [15,16)`,
		}, got)
	})
}
