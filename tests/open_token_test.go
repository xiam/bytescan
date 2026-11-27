package tests

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
)

// The tests in this file cover the one symbol stack: nextToken keeps
// no copy of the token's symbols and reads the winner from the processor's
// stack, which also holds the synthetic EOF symbol. Each test checks that the
// length guards count the stack without that symbol and that a token reads
// only its own symbols.

// newPendingRule returns an engine probe that never accepts: it continues over
// every real symbol and rejects the EOF symbol, so it keeps every token open
// until the end of input. It breaks the practice of settling early in
// docs/rule-authoring.md on purpose.
func newPendingRule() bytescan.Rule {
	var step bytescan.Rule
	step = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		return step, bytescan.StateContinue
	}
	return step
}

// newAcceptEOFRule returns an engine probe that continues over every real
// symbol and accepts the EOF symbol itself, with no pushback. The VM discards
// that accept, because it counts the EOF symbol.
func newAcceptEOFRule() bytescan.Rule {
	var step bytescan.Rule
	step = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateAccept
		}
		return step, bytescan.StateContinue
	}
	return step
}

// newPushBackEOFRule returns an engine probe that continues over every real
// symbol and, on the EOF symbol, pushes it back and accepts: a match of every
// real symbol read, which is zero symbols on empty input.
func newPushBackEOFRule() bytescan.Rule {
	var step bytescan.Rule
	step = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				return nil, bytescan.StateAccept
			}, bytescan.StatePushBack
		}
		return step, bytescan.StateContinue
	}
	return step
}

// TestOneStackEOFOnlyInput: on empty input the stack holds only the EOF
// symbol. Whatever the rules return for it, no real symbol was read, so Next
// returns io.EOF, again on every later call, and commits no token.
func TestOneStackEOFOnlyInput(t *testing.T) {
	cases := []struct {
		name string
		rule bytescan.Rule
	}{
		{name: "rule rejects the EOF symbol", rule: newASCIILettersRule()},
		{name: "rule stays open until EOF", rule: newPendingRule()},
		{name: "rule accepts the EOF symbol", rule: newAcceptEOFRule()},
		{name: "rule pushes the EOF symbol back and accepts", rule: newPushBackEOFRule()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lx := bytescan.New(strings.NewReader(""))
			lx.MustAddRule(lexTypeWord, tc.rule)

			for i := 0; i < 3; i++ {
				lex, err := lx.Next()
				assert.Nil(t, lex)
				assert.ErrorIs(t, err, io.EOF)
			}
		})
	}
}

// TestOneStackUnknownAtEOF: a token decided on the EOF symbol, with no match,
// is one UNKNOWN rune. The stack then holds that rune, the runes after it and
// the EOF symbol; the token is the first real symbol only, with its own text,
// span and Context, and the EOF symbol is never part of it.
func TestOneStackUnknownAtEOF(t *testing.T) {
	cases := []struct {
		name  string
		other bytescan.Rule
	}{
		{name: "other rule stays open until EOF", other: newPendingRule()},
		{name: "other rule accepts the EOF symbol", other: newAcceptEOFRule()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// "€" is 3 bytes and "😀" is 4, so the byte and rune offsets differ.
			input := "ab€😀"
			lx := bytescan.New(strings.NewReader(input))
			lx.MustAddRule(lexTypeWord, newASCIILettersRule())
			lx.MustAddRule("OTHER", tc.other)

			want := []struct {
				typ                  bytescan.TokenType
				text                 string
				byteStart, byteEnd   int
				runeStart, runeCount int
			}{
				{lexTypeWord, "ab", 0, 2, 0, 2},
				{bytescan.TokenTypeUnknown, "€", 2, 5, 2, 1},
				{bytescan.TokenTypeUnknown, "😀", 5, 9, 3, 1},
			}

			for _, w := range want {
				lex, err := lx.Next()
				require.NoError(t, err)
				assert.Equal(t, w.typ, lex.Type())
				assert.Equal(t, w.text, lex.Text())
				assert.Equal(t, w.runeStart, lex.Offset())
				assert.Equal(t, w.runeCount, lex.Len())
				assert.Equal(t, w.byteStart, lex.Span().Start().ByteOffset())
				assert.Equal(t, w.byteEnd, lex.Span().End().ByteOffset())

				ctx, err := lx.Context(lex, 0, 0)
				require.NoError(t, err)
				assert.Equal(t, w.text, ctx)
			}

			lex, err := lx.Next()
			assert.Nil(t, lex)
			assert.ErrorIs(t, err, io.EOF)
		})
	}
}

// transientCursor is a cursor.Reader whose read at byte offset failAt
// fails once. The read does not move the cursor, so the next read returns the
// rune at failAt. Embedding keeps the Context and Remark capabilities.
type transientCursor struct {
	*cursor.Reader
	failAt int
	failed bool
}

var errTransient = errors.New("transient read error")

func (tc *transientCursor) ReadLocatedRune() (cursor.LocatedRune, error) {
	if !tc.failed && tc.Cursor().ByteOffset() == tc.failAt {
		tc.failed = true
		return cursor.LocatedRune{}, errTransient
	}
	return tc.Reader.ReadLocatedRune()
}

// TestOneStackFailedNextThenRetry: a Next that fails in the middle of a token
// leaves the symbols it read on the stack. The retry starts a new token, so it
// reads only its own symbols: the tokens are those of a run without the
// failure.
func TestOneStackFailedNextThenRetry(t *testing.T) {
	const input = "abcdef gh"

	newScanner := func(failAt int) *bytescan.Scanner {
		lx := bytescan.NewWithCursor(&transientCursor{
			Reader: cursor.NewReader(strings.NewReader(input)),
			failAt: failAt,
		})
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		lx.MustAddRule(lexTypeSpace, newSpaceRule())
		return lx
	}

	// maxCalls bounds every loop below, so a scanner that never reaches io.EOF
	// fails the test instead of growing without bound.
	const maxCalls = 32

	var want []string
	for lx, calls := newScanner(-1), 0; ; calls++ {
		require.Less(t, calls, maxCalls, "Next never returned io.EOF")
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		want = append(want, tokenKeys([]*bytescan.Token{lex})...)
	}
	require.Equal(t, []string{`WORD|"abcdef"|0`, `SPACE|" "|6`, `WORD|"gh"|7`}, want)

	// The failure hits the first token after four symbols, the last token
	// after one, and the read of the end of input.
	for _, failAt := range []int{4, 8, len(input)} {
		lx := newScanner(failAt)

		var got []*bytescan.Token
		failures := 0
		for calls := 0; ; calls++ {
			require.Less(t, calls, maxCalls, "failAt %d: Next never returned io.EOF", failAt)
			lex, err := lx.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				require.ErrorIs(t, err, errTransient)
				failures++
				require.Equal(t, 1, failures, "only one read fails")
				continue
			}
			got = append(got, lex)
		}

		assert.Equal(t, 1, failures, "failAt %d", failAt)
		assert.Equal(t, want, tokenKeys(got), "failAt %d", failAt)
	}
}

const (
	// openTokenBytes is the input size of TestOpenTokenLiveHeap: one
	// unterminated quote of 1 MiB.
	openTokenBytes = 1 << 20

	// openTokenLivePerByte is the most live heap, in bytes per input byte, that
	// one open token of openTokenBytes may hold: the value measured with the
	// current scanner at this input size (5.08 B per byte with the race
	// detector, 5.04 without), plus 20%.
	openTokenLivePerByte = 6.1

	// openTokenHeapLimit stops the probe once the heap (live and garbage)
	// passes it above the baseline, so a regression fails fast instead of
	// exhausting the machine.
	openTokenHeapLimit = 256 << 20
)

// newLiveHeapProbe returns an engine probe that never accepts. It continues over
// every real symbol and rejects the EOF symbol, like newPendingRule. At its n-th
// symbol it collects garbage and stores the live heap in *live. Every 64 Ki
// symbols it checks the heap, and it retires itself once the heap passes
// limit, setting *over. It keeps a count across symbols on purpose, which
// docs/rule-authoring.md asks a rule not to do.
func newLiveHeapProbe(n int, limit uint64, live *uint64, over *bool) bytescan.Rule {
	count := 0
	var step bytescan.Rule
	step = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		count++
		if count == n || count%(64<<10) == 0 {
			if count == n {
				runtime.GC()
			}
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > limit {
				*over = true
				return nil, bytescan.StateReject
			}
			if count == n {
				*live = ms.HeapAlloc
			}
		}
		return step, bytescan.StateContinue
	}
	return step
}

// TestOpenTokenLiveHeap covers the memory one open token holds, with a
// live-heap measurement. The input is an unterminated base.Quoted
// string of 1 MiB, so the token stays open to the end of input. A probe rule
// collects garbage at the last rune and reads the live heap above a baseline
// taken after garbage collection, before Next. Live heap is reproducible
// between equal runs; peak heap varies with GC timing, so it is not asserted.
func TestOpenTokenLiveHeap(t *testing.T) {
	input := `"` + strings.Repeat("a", openTokenBytes-1)

	var live uint64
	var over bool

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("STRING", base.Quoted('"', '\\'))

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baseline := ms.HeapAlloc

	lx.MustAddRule("PROBE", newLiveHeapProbe(len(input), baseline+openTokenHeapLimit, &live, &over))

	lex, err := lx.Next()
	require.NoError(t, err)
	require.Falsef(t, over, "heap passed %d MiB above the baseline before the last rune", openTokenHeapLimit>>20)
	require.NotZero(t, live, "the probe did not reach the last rune")

	// No rule matches, so the first token is the quote as one UNKNOWN rune.
	assert.Equal(t, bytescan.TokenTypeUnknown, lex.Type())
	assert.Equal(t, `"`, lex.Text())

	perByte := float64(live-baseline) / float64(len(input))
	t.Logf("one open token of %d bytes: %.2f MiB live, %.2f B per input byte (bound %.2f, race detector: %v)",
		len(input), float64(live-baseline)/(1<<20), perByte, openTokenLivePerByte, raceDetector)
	assert.LessOrEqual(t, perByte, openTokenLivePerByte, "live heap per input byte")
}
