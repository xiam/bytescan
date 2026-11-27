package tests

import (
	"fmt"
	"io"
	mrand "math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
)

// The tests in this file cover the lookahead record of #125
// (docs/specs/resumable-lexing.md, "The lookahead record"):
// Scanner.NextLookahead returns each token with a bytescan.Lookahead, the
// bytes that the rules were shown after the end of the token (After) and
// whether the rules were shown the end of input (ReadEnd). The reach of a
// token is its end byte offset plus After.

// reachToken is one token of a text with its lookahead record.
type reachToken struct {
	typ   bytescan.TokenType
	text  string
	start int // byte offset
	end   int // byte offset
	la    bytescan.Lookahead
}

func (r reachToken) reach() int { return r.end + r.la.After }

func (r reachToken) String() string {
	mark := ""
	if r.la.ReadEnd {
		mark = " end"
	}
	return fmt.Sprintf("%s %q [%d,%d) reach %d%s", r.typ, r.text, r.start, r.end, r.reach(), mark)
}

// lexReach lexes every token of sc with NextLookahead.
func lexReach(t *testing.T, sc *bytescan.Scanner) []reachToken {
	t.Helper()
	var toks []reachToken
	for i := 0; ; i++ {
		require.Less(t, i, 100000, "NextLookahead did not stop")
		tok, la, err := sc.NextLookahead()
		if err == io.EOF {
			require.Nil(t, tok)
			require.Equal(t, bytescan.Lookahead{}, la, "the record of the EOF call")
			return toks
		}
		require.NoError(t, err)
		span := tok.Span()
		toks = append(toks, reachToken{
			typ:   tok.Type(),
			text:  tok.Text(),
			start: span.Start().ByteOffset(),
			end:   span.End().ByteOffset(),
			la:    la,
		})
	}
}

// lexReachRuleSetA lexes text with rule set A (addRuleSetA) and returns every
// token with its record.
func lexReachRuleSetA(t *testing.T, text string) []reachToken {
	t.Helper()
	sc := bytescan.New(strings.NewReader(text))
	addRuleSetA(sc)
	return lexReach(t, sc)
}

func reachLines(toks []reachToken) []string {
	out := make([]string, len(toks))
	for i, tok := range toks {
		out[i] = tok.String()
	}
	return out
}

// TestLookaheadKnownReach checks the reach of every token of texts of the
// edit cases of docs/specs/resumable-lexing.md, under rule set A. The
// expected values follow from the rules. A run (WORD, WS, the digits of
// NUMBER) reads one symbol past its end to see that it ended. OP reads one
// more symbol after "=", because "==" is also an OP. NUMBER reads "." and one
// more symbol to try a fraction. STRING and COMMENT read until they close, or
// until a newline (STRING) or the end of the text (COMMENT) rejects them. NL
// and the closing symbol of STRING and COMMENT decide without reading past
// their end. DIRECTIVE, the rule that addRuleSetA adds, rejects the first
// symbol of every token here, because no text holds '#'.
func TestLookaheadKnownReach(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want []string
	}{
		{"A12 a token that read far ahead", "12.x\n", []string{
			`NUMBER "12" [0,2) reach 4`,
			`OP "." [2,3) reach 3`,
			`WORD "x" [3,4) reach 5`,
			`NL "\n" [4,5) reach 5`,
		}},
		{"A31 an unclosed comment start", "a /* x\nb\n", []string{
			`WORD "a" [0,1) reach 2`,
			`WS " " [1,2) reach 3`,
			`OP "/" [2,3) reach 9 end`,
			`OP "*" [3,4) reach 4`,
			`WS " " [4,5) reach 6`,
			`WORD "x" [5,6) reach 7`,
			`NL "\n" [6,7) reach 7`,
			`WORD "b" [7,8) reach 9`,
			`NL "\n" [8,9) reach 9`,
		}},
		{"A23 no final newline", "foo", []string{
			`WORD "foo" [0,3) reach 3 end`,
		}},
		{"A2 two operators", "a = = b\n", []string{
			`WORD "a" [0,1) reach 2`,
			`WS " " [1,2) reach 3`,
			`OP "=" [2,3) reach 4`,
			`WS " " [3,4) reach 5`,
			`OP "=" [4,5) reach 6`,
			`WS " " [5,6) reach 7`,
			`WORD "b" [6,7) reach 8`,
			`NL "\n" [7,8) reach 8`,
		}},
		{"A3 a number and a fraction", "12 .5\n", []string{
			`NUMBER "12" [0,2) reach 3`,
			`WS " " [2,3) reach 4`,
			`OP "." [3,4) reach 4`,
			`NUMBER "5" [4,5) reach 6`,
			`NL "\n" [5,6) reach 6`,
		}},
		{"A15 an unclosed string", "x = \"hi there\n", []string{
			`WORD "x" [0,1) reach 2`,
			`WS " " [1,2) reach 3`,
			`OP "=" [2,3) reach 4`,
			`WS " " [3,4) reach 5`,
			`UNKNOWN "\"" [4,5) reach 14`,
			`WORD "hi" [5,7) reach 8`,
			`WS " " [7,8) reach 9`,
			`WORD "there" [8,13) reach 14`,
			`NL "\n" [13,14) reach 14`,
		}},
		{"A33 a string open at the end", "x = \"ab", []string{
			`WORD "x" [0,1) reach 2`,
			`WS " " [1,2) reach 3`,
			`OP "=" [2,3) reach 4`,
			`WS " " [3,4) reach 5`,
			`UNKNOWN "\"" [4,5) reach 7 end`,
			`WORD "ab" [5,7) reach 7 end`,
		}},
		{"A19 a closed comment", "a /* x */ b\nc\n", []string{
			`WORD "a" [0,1) reach 2`,
			`WS " " [1,2) reach 3`,
			`COMMENT "/* x */" [2,9) reach 9`,
			`WS " " [9,10) reach 11`,
			`WORD "b" [10,11) reach 12`,
			`NL "\n" [11,12) reach 12`,
			`WORD "c" [12,13) reach 14`,
			`NL "\n" [13,14) reach 14`,
		}},
		{"A30 the last token reads nothing past its end", "a = 1\n", []string{
			`WORD "a" [0,1) reach 2`,
			`WS " " [1,2) reach 3`,
			`OP "=" [2,3) reach 4`,
			`WS " " [3,4) reach 5`,
			`NUMBER "1" [4,5) reach 6`,
			`NL "\n" [5,6) reach 6`,
		}},
		{"A35 the empty text", "", []string{}},
		// The example of "The four steps": the decoder looks at the byte
		// after \xc3 to know that the character is not complete, but the
		// rules are shown one U+FFFD symbol of one byte. So the reach of
		// UNKNOWN [2,3) is 3.
		{"broken bytes", "ni\xc3o\n", []string{
			`WORD "ni" [0,2) reach 3`,
			`UNKNOWN "�" [2,3) reach 3`,
			`WORD "o" [3,4) reach 5`,
			`NL "\n" [4,5) reach 5`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, reachLines(lexReachRuleSetA(t, tc.text)), "text %q", tc.text)
		})
	}
}

// reachTexts returns the generated texts of the tests in this file: pieces
// of rule set A from a fixed seed (tokenStartPieces), so every run checks the
// same texts.
func reachTexts(seed int64, n int) []string {
	rng := mrand.New(mrand.NewSource(seed))
	texts := make([]string, n)
	for i := range texts {
		texts[i] = tokenStartText(rng, 1, 12)
	}
	return texts
}

// brokenPieces are the pieces of tokenStartPieces plus broken UTF-8: a first
// byte of two bytes and of three bytes, a continuation byte, and the first two
// bytes of a three-byte character. A suffix can complete such a character.
var brokenPieces = append(append([]string(nil), tokenStartPieces...),
	"\xc3", "\xe2", "\xa9", "\xe2\x82")

// brokenText returns a text of 1 to 12 pieces of brokenPieces.
func brokenText(rng *mrand.Rand, minPieces, maxPieces int) string {
	var sb strings.Builder
	n := minPieces + rng.Intn(maxPieces-minPieces+1)
	for i := 0; i < n; i++ {
		sb.WriteString(brokenPieces[rng.Intn(len(brokenPieces))])
	}
	return sb.String()
}

// incompleteBefore reports whether the three bytes before offset r of text
// hold the first byte of a UTF-8 character that is not complete before r.
// It is the condition of the clause for broken bytes in step 1 of "The four
// steps": such a byte is one U+FFFD symbol of one byte, but bytes after r can
// complete the character, and then the bytes before r decode to other
// symbols.
func incompleteBefore(text string, r int) bool {
	for i := max(0, r-3); i < r; i++ {
		if text[i] >= 0xc0 && !utf8.FullRuneInString(text[i:r]) {
			return true
		}
	}
	return false
}

// TestLookaheadReachLowerBound checks that a token that ends at offset e has
// a reach of at least e: After is never negative. A rule that must see the
// next symbol to know that the token ended gives a reach of e + 1 or more; the
// known values of TestLookaheadKnownReach hold such tokens, and so must the
// generated texts.
func TestLookaheadReachLowerBound(t *testing.T) {
	pastEnd := 0
	for _, text := range reachTexts(251, 2000) {
		for _, tok := range lexReachRuleSetA(t, text) {
			require.GreaterOrEqual(t, tok.la.After, 0, "text %q, token %s", text, tok)
			require.GreaterOrEqual(t, tok.reach(), tok.end, "text %q, token %s", text, tok)
			require.LessOrEqual(t, tok.reach(), len(text), "text %q, token %s", text, tok)
			if tok.la.After > 0 {
				pastEnd++
			}
		}
	}
	require.Positive(t, pastEnd, "no token read past its end")
}

// TestLookaheadReachIsEnough checks that the reach is enough to decide a
// token. A token's tokens before it are decided too, and each of them has its
// own reach, which can be larger (OP "/" in case A31 reads past the tokens
// after it). So the test takes, for the first i+1 tokens of a text, the
// largest reach R among them. When none of them read the end, and the three
// bytes before R hold no first byte of a character that is not complete
// before R (incompleteBefore), a second text that is equal up to offset R and
// different after it gives the same i+1 tokens with the same records. The
// second text is the first R bytes and then a generated suffix, or nothing.
// The texts and the suffixes hold broken UTF-8 too, so the condition is
// exercised; the subtest "broken bytes" shows why it is needed.
func TestLookaheadReachIsEnough(t *testing.T) {
	t.Run("generated", func(t *testing.T) {
		rng := mrand.New(mrand.NewSource(97))
		compared, farReach, excluded := 0, 0, 0
		for i := 0; i < 2000; i++ {
			var text string
			if i%2 == 0 {
				text = tokenStartText(rng, 1, 12)
			} else {
				text = brokenText(rng, 1, 12)
			}
			toks := lexReachRuleSetA(t, text)
			maxReach := 0
			for i, tok := range toks {
				if tok.la.ReadEnd {
					break
				}
				maxReach = max(maxReach, tok.reach())
				if incompleteBefore(text, maxReach) {
					excluded++
					break
				}

				suffix := ""
				if rng.Intn(4) > 0 {
					suffix = brokenText(rng, 1, 6)
				}
				if suffix == text[maxReach:] {
					continue
				}
				other := text[:maxReach] + suffix
				got := lexReachRuleSetA(t, other)
				require.GreaterOrEqual(t, len(got), i+1, "text %q, other %q", text, other)
				require.Equal(t, reachLines(toks[:i+1]), reachLines(got[:i+1]), "text %q, other %q", text, other)
				compared++
				if maxReach > tok.end {
					farReach++
				}
			}
		}
		require.GreaterOrEqual(t, compared, 5000, "tokens compared")
		require.Positive(t, farReach, "no compared token read past its end")
		require.Positive(t, excluded, "no text exercised the broken-bytes condition")
	})

	// A first byte of two bytes is one U+FFFD symbol of one byte. In "\xc3o"
	// the rules decide UNKNOWN [0,1) without reading past it: After is 0 and
	// the mark is not set, so R is 1. The text "\xc3\xa9" is equal up to
	// offset 1, but there the bytes decode to "é", and the first token is
	// UNKNOWN "é" [0,2). The reach counts symbols, not the bytes that the
	// decoder examined, so it does not cover this case; incompleteBefore is
	// true at offset 1.
	t.Run("broken bytes", func(t *testing.T) {
		old := lexReachRuleSetA(t, "\xc3o")
		require.Equal(t, `UNKNOWN "�" [0,1) reach 1`, old[0].String())
		require.True(t, incompleteBefore("\xc3o", old[0].reach()))
		require.Equal(t, `UNKNOWN "é" [0,2) reach 2`, lexReachRuleSetA(t, "\xc3\xa9")[0].String())
	})
}

// countEOF returns wrap for addRuleSetAWrapped: every rule step that is shown
// the EOF symbol adds one to seen.
func countEOF(seen *atomic.Int64) func(bytescan.Rule) bytescan.Rule {
	var wrap func(bytescan.Rule) bytescan.Rule
	wrap = func(r bytescan.Rule) bytescan.Rule {
		if r == nil {
			return nil
		}
		return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() {
				seen.Add(1)
			}
			next, st := r(s)
			return wrap(next), st
		}
	}
	return wrap
}

// TestLookaheadReadEnd checks the "read the end" mark. A token carries the
// mark exactly when a rule was shown the EOF symbol while the token was
// decided; a probe around every rule tells. A token with the mark has its
// reach at the end of the text. When none of the first i+1 tokens carries the
// mark, appended bytes do not change them. In case A23 the text "foo" gives
// WORD foo with the mark, and appending "d" changes it.
func TestLookaheadReadEnd(t *testing.T) {
	t.Run("A23", func(t *testing.T) {
		require.Equal(t, []string{`WORD "foo" [0,3) reach 3 end`}, reachLines(lexReachRuleSetA(t, "foo")))
		require.Equal(t, []string{`WORD "food" [0,4) reach 4 end`}, reachLines(lexReachRuleSetA(t, "food")))
	})

	t.Run("probe", func(t *testing.T) {
		marked, unmarked := 0, 0
		for _, text := range reachTexts(331, 1500) {
			var seen atomic.Int64
			sc := bytescan.New(strings.NewReader(text))
			addRuleSetAWrapped(sc, countEOF(&seen))
			for {
				before := seen.Load()
				tok, la, err := sc.NextLookahead()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				shown := seen.Load() > before
				require.Equal(t, shown, la.ReadEnd, "text %q, token %s %q", text, tok.Type(), tok.Text())
				if la.ReadEnd {
					require.Equal(t, len(text), tok.Span().End().ByteOffset()+la.After, "text %q: a marked token reaches the end", text)
					marked++
				} else {
					unmarked++
				}
			}
		}
		require.Positive(t, marked, "no token carried the mark")
		require.Positive(t, unmarked, "every token carried the mark")
	})

	// The condition on broken bytes is the one of TestLookaheadReachIsEnough:
	// appended bytes can complete a character whose first byte is in the
	// three bytes before the largest reach.
	t.Run("append", func(t *testing.T) {
		rng := mrand.New(mrand.NewSource(332))
		compared, excluded := 0, 0
		for i := 0; i < 2000; i++ {
			var text string
			if i%2 == 0 {
				text = tokenStartText(rng, 1, 12)
			} else {
				text = brokenText(rng, 1, 12)
			}
			toks := lexReachRuleSetA(t, text)
			appended := text + brokenText(rng, 1, 6)
			got := lexReachRuleSetA(t, appended)
			maxReach := 0
			for i, tok := range toks {
				if tok.la.ReadEnd {
					break
				}
				maxReach = max(maxReach, tok.reach())
				if incompleteBefore(text, maxReach) {
					excluded++
					break
				}
				require.Equal(t, toks[i].String(), got[i].String(), "text %q, appended %q", text, appended)
				compared++
			}
		}
		require.GreaterOrEqual(t, compared, 5000, "tokens compared")
		require.Positive(t, excluded, "no text exercised the broken-bytes condition")
	})

	// "\xc3" gives UNKNOWN [0,1) without the mark: the rules decide it on the
	// U+FFFD symbol, before the EOF symbol. Appending "\xa9" completes "é",
	// and the token changes, although it did not read the end.
	t.Run("append completes a character", func(t *testing.T) {
		old := lexReachRuleSetA(t, "\xc3")
		require.Equal(t, `UNKNOWN "�" [0,1) reach 1`, old[0].String())
		require.True(t, incompleteBefore("\xc3", old[0].reach()))
		require.Equal(t, `UNKNOWN "é" [0,2) reach 2`, lexReachRuleSetA(t, "\xc3\xa9")[0].String())
	})

	// A source that returns cursor.ErrMoreInput does not show the rules the
	// end of input (TestMoreInputNoFalseEnd). The tokens decided from the
	// available input do not carry the mark, and the call that returns the
	// signal returns the zero record. The mark appears only after the source
	// returns io.EOF.
	t.Run("more input", func(t *testing.T) {
		src := &feedSource{}
		sc := bytescan.NewWithCursor(cursor.NewReader(src))
		addRuleSetA(sc)

		src.feed("foo\nba")
		tok, la, err := sc.NextLookahead()
		require.NoError(t, err)
		require.Equal(t, "foo", tok.Text())
		require.Equal(t, bytescan.Lookahead{After: 1}, la)
		tok, la, err = sc.NextLookahead()
		require.NoError(t, err)
		require.Equal(t, "\n", tok.Text())
		require.Equal(t, bytescan.Lookahead{}, la)
		tok, la, err = sc.NextLookahead()
		require.True(t, err == bytescan.ErrMoreInput, "err = %v", err)
		require.Nil(t, tok)
		require.Equal(t, bytescan.Lookahead{}, la)

		src.feed("r")
		src.end()
		tok, la, err = sc.NextLookahead()
		require.NoError(t, err)
		require.Equal(t, "bar", tok.Text())
		require.Equal(t, bytescan.Lookahead{ReadEnd: true}, la)
		_, la, err = sc.NextLookahead()
		require.Equal(t, io.EOF, err)
		require.Equal(t, bytescan.Lookahead{}, la)
	})
}

// TestLookaheadSymbolsNotBufferedBytes checks that the record counts the
// symbols shown to the rules, not the bytes that the cursor buffered. The
// records of every token are the same for cursors with different buffer
// capacities (cursor.WithCapacity), over a reader that returns all its bytes
// at once and over one that returns one byte for each call, and over a rune
// source.
func TestLookaheadSymbolsNotBufferedBytes(t *testing.T) {
	for _, text := range reachTexts(419, 400) {
		want := reachLines(lexReachRuleSetA(t, text))
		for _, capacity := range []int{1, 4, 7, 16, 64, 4096} {
			for _, oneByte := range []bool{false, true} {
				var r io.Reader = strings.NewReader(text)
				if oneByte {
					r = iotest.OneByteReader(r)
				}
				sc := bytescan.NewWithCursor(cursor.NewReader(r, cursor.WithCapacity(capacity)))
				addRuleSetA(sc)
				require.Equal(t, want, reachLines(lexReach(t, sc)), "text %q, capacity %d, one byte %v", text, capacity, oneByte)
			}
			sc := bytescan.NewWithCursor(cursor.NewRuneReader(runeOnly{strings.NewReader(text)}, cursor.WithCapacity(capacity)))
			addRuleSetA(sc)
			require.Equal(t, want, reachLines(lexReach(t, sc)), "text %q, capacity %d, rune source", text, capacity)
		}
	}
}

// runeOnly hides every method of its reader except ReadRune.
type runeOnly struct{ r io.RuneReader }

func (s runeOnly) ReadRune() (rune, int, error) { return s.r.ReadRune() }

// TestNextLookaheadMatchesNext checks that NextLookahead returns the tokens
// and the errors of Next: the record is the only difference.
func TestNextLookaheadMatchesNext(t *testing.T) {
	for _, text := range reachTexts(521, 500) {
		a := bytescan.New(strings.NewReader(text))
		addRuleSetA(a)
		b := bytescan.New(strings.NewReader(text))
		addRuleSetA(b)
		for {
			tokA, errA := a.Next()
			tokB, _, errB := b.NextLookahead()
			require.Equal(t, errA, errB, "text %q", text)
			if errA != nil {
				require.Nil(t, tokB)
				break
			}
			require.Equal(t, tokenLine(tokA), tokenLine(tokB), "text %q", text)
		}
	}

	sc := bytescan.New(strings.NewReader("x"))
	tok, la, err := sc.NextLookahead()
	require.ErrorIs(t, err, bytescan.ErrNoRules, "err = %v", err)
	require.Nil(t, tok)
	require.Equal(t, bytescan.Lookahead{}, la)
}

// TestLookaheadAfterRestart checks that the record does not depend on the
// coordinates of the scan. For every token boundary p of a text, a scan
// started at p with Reader.ResetStart (and the flags that startOf gives)
// returns the tokens of the whole scan from p on, moved by -p, with the same
// records.
func TestLookaheadAfterRestart(t *testing.T) {
	compared := 0
	for _, text := range reachTexts(613, 300) {
		whole := lexReachRuleSetA(t, text)
		for i, from := range whole {
			p := from.start
			cur := cursor.NewReader(strings.NewReader(""))
			require.NoError(t, cur.ResetStart(strings.NewReader(text[p:]), startOf(text, p)))
			sc := bytescan.NewWithCursor(cur)
			addRuleSetA(sc)
			got := lexReach(t, sc)

			want := make([]reachToken, 0, len(whole)-i)
			for _, tok := range whole[i:] {
				tok.start -= p
				tok.end -= p
				want = append(want, tok)
			}
			require.Equal(t, reachLines(want), reachLines(got), "text %q, restart at %d", text, p)
			compared++
		}
	}
	require.GreaterOrEqual(t, compared, 1000, "restarts compared")
}
