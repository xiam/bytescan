package statelex

import (
	"fmt"
	mrand "math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan/modes"
)

// The benchmarks of this file measure the latency of one edit of the lexer
// with state, on texts of growing sizes, under rule set A and rule set B of
// docs/specs/resumable-lexing.md. The harness of internal/bench runs them with
// -kind statelex and reads the metrics that they report.
//
// Each operation is one edit. The timer covers only the call of Apply or
// SetText. The work that puts the text back for the next operation runs with
// the timer stopped. Besides the time, each benchmark reports, as the mean of
// its operations, the size of the change and the three work amounts that the
// work tests count: the bytes of the held text that were copied, the held
// tokens that were visited, and the length in bytes of the run that was lexed
// again.

// latencySizes are the sizes of the generated texts. Each text ends at the
// first line boundary at or past its size. From the smallest to the largest,
// the size grows 64 times, as in TestWorkOfASmallEdit.
var latencySizes = []struct {
	name string
	size int
}{
	{"64KiB", 64 << 10},
	{"512KiB", 512 << 10},
	{"4MiB", 4 << 20},
}

// latencySets are the two rule sets and the generator of their texts.
var latencySets = []struct {
	name string
	set  func() *modes.ModeSet
	line func(rng *mrand.Rand) string
}{
	{"A", func() *modes.ModeSet { return modeSet(ruleSetA()) }, lineA},
	{"B", ruleSetB, lineB},
}

// latencySeed is the seed of every generated text.
const latencySeed = 1

// latencyWord returns a word of one to eight ASCII letters.
func latencyWord(rng *mrand.Rand) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 1+rng.Intn(8))
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

// lineA returns one line of rule set A: an assignment of an expression of
// words, numbers and strings, sometimes with a comment. Every string and every
// comment is closed on its line.
func lineA(rng *mrand.Rand) string {
	var sb strings.Builder
	sb.WriteString(latencyWord(rng))
	sb.WriteString(" = ")
	for i, n := 0, 1+rng.Intn(4); i < n; i++ {
		if i > 0 {
			sb.WriteString([]string{" + ", " * ", " / ", " == "}[rng.Intn(4)])
		}
		switch rng.Intn(4) {
		case 0:
			fmt.Fprintf(&sb, "%d", rng.Intn(1000))
		case 1:
			fmt.Fprintf(&sb, "%d.%d", rng.Intn(100), rng.Intn(100))
		case 2:
			fmt.Fprintf(&sb, "%q", latencyWord(rng)+" "+latencyWord(rng))
		default:
			sb.WriteString(latencyWord(rng))
		}
	}
	if rng.Intn(4) == 0 {
		sb.WriteString(" /* " + latencyWord(rng) + " */")
	}
	sb.WriteString("\n")
	return sb.String()
}

// lineB returns one line of rule set B: words and quoted strings. Every
// string is closed on its line, so every line starts in the state [code].
func lineB(rng *mrand.Rand) string {
	var sb strings.Builder
	for i, n := 0, 2+rng.Intn(5); i < n; i++ {
		if i > 0 {
			sb.WriteString(" ")
		}
		if rng.Intn(3) == 0 {
			sb.WriteString(`"` + latencyWord(rng) + " " + latencyWord(rng) + `"`)
		} else {
			sb.WriteString(latencyWord(rng))
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// latencyText returns the lines of line from the fixed seed, up to the first
// line boundary at or past size.
func latencyText(line func(*mrand.Rand) string, size int) string {
	rng := mrand.New(mrand.NewSource(latencySeed))
	var sb strings.Builder
	for sb.Len() < size {
		sb.WriteString(line(rng))
	}
	return sb.String()
}

// wordAt returns the start of the first WORD token of at least two bytes that
// starts at or after off. A WORD token is in the mode code in both rule sets.
func wordAt(tb testing.TB, lx *Lexer, off int) int {
	tb.Helper()
	for _, tok := range lx.Tokens(off, lx.Len()) {
		if tok.Type == "WORD" && tok.Start >= off && tok.End-tok.Start >= 2 {
			return tok.Start
		}
	}
	tb.Fatalf("no word of two bytes at or after %d", off)
	return 0
}

// lineAt returns the start of the first line at or after off.
func lineAt(text string, off int) int {
	if off == 0 {
		return 0
	}
	i := strings.IndexByte(text[off-1:], '\n')
	if i < 0 {
		return len(text)
	}
	return off + i
}

// latencyShape is one edit shape. For a lexer that holds text, edit returns
// the timed edit and the untimed edit that undoes it.
type latencyShape struct {
	name string
	edit func(tb testing.TB, lx *Lexer, text string) (do, undo func() (Change, error))
}

func applyEdit(lx *Lexer, e Edit) func() (Change, error) {
	return func() (Change, error) { return lx.Apply(e) }
}

// keyAt is a key typed inside a word, one byte after its start.
func keyAt(place func(n int) int) func(testing.TB, *Lexer, string) (func() (Change, error), func() (Change, error)) {
	return func(tb testing.TB, lx *Lexer, text string) (func() (Change, error), func() (Change, error)) {
		p := wordAt(tb, lx, place(len(text))) + 1
		return applyEdit(lx, Insert(p, []byte("y"))), applyEdit(lx, Delete(p, p+1))
	}
}

// backspaceAt is a backspace inside a word: it deletes the second byte of
// the word.
func backspaceAt(place func(n int) int) func(testing.TB, *Lexer, string) (func() (Change, error), func() (Change, error)) {
	return func(tb testing.TB, lx *Lexer, text string) (func() (Change, error), func() (Change, error)) {
		p := wordAt(tb, lx, place(len(text))) + 1
		removed := []byte(text[p : p+1])
		return applyEdit(lx, Delete(p, p+1)), applyEdit(lx, Insert(p, removed))
	}
}

func nearStart(int) int  { return 0 }
func inMiddle(n int) int { return n / 2 }
func nearEnd(n int) int  { return n - 256 }

// latencyShapes are the edit shapes of the ticket. The load is measured
// apart, because each of its operations needs an empty lexer.
var latencyShapes = []latencyShape{
	{"key-start", keyAt(nearStart)},
	{"key-middle", keyAt(inMiddle)},
	{"key-end", keyAt(nearEnd)},
	{"backspace-start", backspaceAt(nearStart)},
	{"backspace-middle", backspaceAt(inMiddle)},
	{"backspace-end", backspaceAt(nearEnd)},
	// Three whole lines deleted from the middle. Each line closes its
	// strings and its comments, so the selection keeps the mode state.
	{"selection-middle", func(tb testing.TB, lx *Lexer, text string) (func() (Change, error), func() (Change, error)) {
		start := lineAt(text, len(text)/2)
		end := lineAt(text, lineAt(text, lineAt(text, start+1)+1)+1)
		removed := []byte(text[start:end])
		return applyEdit(lx, Delete(start, end)), applyEdit(lx, Insert(start, removed))
	}},
	// The edit with no sync point under rule set B: an opening quote at the
	// start of the text flips the mode state of every later token.
	{"quote-start", func(tb testing.TB, lx *Lexer, text string) (func() (Change, error), func() (Change, error)) {
		return applyEdit(lx, Insert(0, []byte(`"`))), applyEdit(lx, Delete(0, 1))
	}},
	// Its removal: the lexer holds the text after the quote, and the edit
	// deletes the quote.
	{"unquote-start", func(tb testing.TB, lx *Lexer, text string) (func() (Change, error), func() (Change, error)) {
		if _, err := lx.Apply(Insert(0, []byte(`"`))); err != nil {
			tb.Fatal(err)
		}
		return applyEdit(lx, Delete(0, 1)), applyEdit(lx, Insert(0, []byte(`"`)))
	}},
	// SetText with the whole text, in which one letter in the middle
	// changed.
	{"settext-middle", func(tb testing.TB, lx *Lexer, text string) (func() (Change, error), func() (Change, error)) {
		p := wordAt(tb, lx, len(text)/2)
		changed := []byte(text)
		changed[p] = 'a' + (changed[p]-'a'+1)%26
		original := []byte(text)
		return func() (Change, error) { return lx.SetText(changed) },
			func() (Change, error) { return lx.SetText(original) }
	}},
}

// latencyWork sums the change and the work of each timed edit.
type latencyWork struct {
	removed, inserted, copied, visited, relexed int
}

func (w *latencyWork) add(lx *Lexer, ch Change) {
	w.removed += len(ch.Removed)
	w.inserted += len(ch.Inserted)
	w.copied += lx.work.textBytes
	w.visited += lx.work.tokens
	w.relexed += lx.lastRun.end - lx.lastRun.restart
}

func (w *latencyWork) report(b *testing.B) {
	n := float64(b.N)
	b.ReportMetric(float64(w.removed)/n, "removed-tokens/op")
	b.ReportMetric(float64(w.inserted)/n, "inserted-tokens/op")
	b.ReportMetric(float64(w.copied)/n, "copied-bytes/op")
	b.ReportMetric(float64(w.visited)/n, "visited-tokens/op")
	b.ReportMetric(float64(w.relexed)/n, "relexed-bytes/op")
}

// BenchmarkEdit measures one edit of each shape, for each rule set and each
// size: BenchmarkEdit/<set>/<shape>/<size>.
func BenchmarkEdit(b *testing.B) {
	for _, set := range latencySets {
		b.Run(set.name, func(b *testing.B) {
			b.Run("load", func(b *testing.B) {
				for _, sz := range latencySizes {
					b.Run(sz.name, func(b *testing.B) {
						benchLoad(b, set.set, []byte(latencyText(set.line, sz.size)))
					})
				}
			})
			for _, shape := range latencyShapes {
				b.Run(shape.name, func(b *testing.B) {
					for _, sz := range latencySizes {
						b.Run(sz.name, func(b *testing.B) {
							benchShape(b, set.name, set.set, set.line, sz.size, shape)
						})
					}
				})
			}
		})
	}
}

// benchLoad measures the load: one edit of an empty lexer that inserts the
// whole text.
func benchLoad(b *testing.B, set func() *modes.ModeSet, text []byte) {
	b.ReportAllocs()
	var w latencyWork
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		lx, err := New(set())
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		ch, err := lx.Apply(Insert(0, text))
		if err != nil {
			b.Fatal(err)
		}
		w.add(lx, ch)
	}
	b.StopTimer()
	w.report(b)
}

// latencyCache holds the lexer of the last benchmark of benchShape and its
// two edits. The testing package calls a benchmark function once for each
// b.N that it tries, and loading a text of 4 MiB takes much longer than the
// benchtime, so the lexer is loaded once for each benchmark in a process. The
// untimed edit puts the text back after each operation, so every call starts
// from the same text.
var latencyCache struct {
	key      string
	lx       *Lexer
	do, undo func() (Change, error)
}

// benchShape measures the timed edit of shape on a lexer that holds the text
// of set and size, and undoes it with the timer stopped after each
// operation.
func benchShape(b *testing.B, setName string, set func() *modes.ModeSet, line func(*mrand.Rand) string, size int, shape latencyShape) {
	key := fmt.Sprintf("%s/%s/%d", setName, shape.name, size)
	if latencyCache.key != key {
		latencyCache.key, latencyCache.lx = "", nil
		text := latencyText(line, size)
		lx, err := New(set())
		if err != nil {
			b.Fatal(err)
		}
		if _, err := lx.Apply(Insert(0, []byte(text))); err != nil {
			b.Fatal(err)
		}
		do, undo := shape.edit(b, lx, text)
		latencyCache.key, latencyCache.lx, latencyCache.do, latencyCache.undo = key, lx, do, undo
	}
	lx, do, undo := latencyCache.lx, latencyCache.do, latencyCache.undo
	b.ReportAllocs()
	var w latencyWork
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch, err := do()
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		w.add(lx, ch)
		if _, err := undo(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.StopTimer()
	w.report(b)
}

// TestLatencyShapes checks the edits of the benchmarks on small texts of both
// rule sets: after the timed edit, the lexer holds the tokens and the mode
// states of a scan of the new text; after the untimed edit, it holds the
// first text again. Only the opening quote and its removal under rule set B
// re-lex to the end of the text.
func TestLatencyShapes(t *testing.T) {
	for _, set := range latencySets {
		text := latencyText(set.line, 4<<10)
		for _, shape := range latencyShapes {
			t.Run(set.name+"/"+shape.name, func(t *testing.T) {
				lx := newLexerModes(t, set.set(), text)
				do, undo := shape.edit(t, lx, text)
				before := heldText(lx)
				for i := 0; i < 3; i++ {
					_, err := do()
					require.NoError(t, err)
					after := heldText(lx)
					require.NotEqual(t, before, after)
					want, end := scanModes(t, set.set(), after)
					requireSameStates(t, lx, want, end, shape.name)
					noSync := set.name == "B" && strings.HasSuffix(shape.name, "quote-start")
					require.Equal(t, !noSync, lx.lastRun.synced)

					_, err = undo()
					require.NoError(t, err)
					require.Equal(t, before, heldText(lx))
				}
			})
		}
	}
}

// newLexerModes returns a lexer for ms that holds text.
func newLexerModes(t testing.TB, ms *modes.ModeSet, text string) *Lexer {
	t.Helper()
	lx, err := New(ms)
	require.NoError(t, err)
	_, err = lx.Apply(Insert(0, []byte(text)))
	require.NoError(t, err)
	return lx
}
