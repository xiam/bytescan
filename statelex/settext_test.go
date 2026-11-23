package statelex

import (
	mrand "math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// setTextEdit returns the edit that SetText applies when the lexer holds old
// and gets text. It reads both texts whole, byte by byte: it is the reference
// for the comparison of SetText, which reads the held text in place, one
// chunk of the rope at a time.
func setTextEdit(old, text string) Edit {
	pre := 0
	for pre < len(old) && pre < len(text) && old[pre] == text[pre] {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(text)-pre && old[len(old)-1-suf] == text[len(text)-1-suf] {
		suf++
	}
	start, end := pre, len(old)-suf
	if k, _, inside := charAt([]byte(old), start); inside {
		start = k
	}
	if k, size, inside := charAt([]byte(old), end); inside {
		end = k + size
	}
	return Edit{Start: start, End: end, Text: []byte(text[start : len(text)-(len(old)-end)])}
}

// requireSetTextAsApply calls SetText(text) on a and applies the reference
// edit of setTextEdit on b, which holds the same text with the same rope. It
// checks that both give the same change, the same tokens and mode states, the
// same work amounts and the same re-lexed run, and that a holds text.
func requireSetTextAsApply(t *testing.T, a, b *Lexer, text, label string) {
	t.Helper()
	old := heldText(a)
	require.Equal(t, old, heldText(b), label)
	chA, err := a.SetText([]byte(text))
	require.NoError(t, err, label)
	chB, err := b.Apply(setTextEdit(old, text))
	require.NoError(t, err, label)
	// The queries below count work too, so the work is checked first.
	require.Equal(t, b.work, a.work, label)
	require.Equal(t, b.lastRun, a.lastRun, label)
	require.Equal(t, chB, chA, label)
	require.Equal(t, text, heldText(a), label)
	require.Equal(t, heldStates(b), heldStates(a), label)
	require.Equal(t, b.ModeAtEnd(), a.ModeAtEnd(), label)
}

// TestSetTextCases checks SetText on texts of many chunks: a change at the
// start, in the middle and at the end, an empty new text, an identical text,
// and texts that differ only in length. Each gives the change, the tokens and
// the work amounts of the edit that a whole-text comparison finds.
func TestSetTextCases(t *testing.T) {
	line := "x = 1 /* ñó */\n"
	base := strings.Repeat(line, 600) // about 9 KiB: nine chunks of the rope
	mid := len(base) / 2
	cases := []struct {
		name, before, after string
	}{
		{"start", base, "y" + base[1:]},
		{"middle", base, base[:mid] + "zz" + base[mid+3:]},
		{"end", base, base[:len(base)-2] + "2\n"},
		{"empty new text", base, ""},
		{"empty held text", "", base},
		{"identical", base, base},
		{"longer", base, base + line},
		{"shorter", base, base[:len(base)-len(line)]},
		{"longer at the start", base, line + base},
		{"shorter at the start", base, base[len(line):]},
		// ñ and ó share their first byte: the middle widens to the whole
		// character.
		{"inside a character", base, strings.Replace(base, "ñ", "ó", 1)},
		{"inside the last character", base, strings.Replace(base, "ó", "ñ", -1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newLexer(t, ruleSetA(), c.before)
			b := newLexer(t, ruleSetA(), c.before)
			requireSetTextAsApply(t, a, b, c.after, c.name)
		})
	}
}

// TestSetTextGenerated makes random edits through SetText on two lexers in
// step, after earlier edits have left chunks of many sizes, and checks each
// one against the reference edit.
func TestSetTextGenerated(t *testing.T) {
	pieces := []string{"x", " = ", "1", "\n", "ñ", "ó", "\xc3", "\"s\"", "/* c */", "€"}
	rng := mrand.New(mrand.NewSource(7))
	text := strings.Repeat("x = 1 ñ\n", 400)
	a := newLexer(t, ruleSetA(), text)
	b := newLexer(t, ruleSetA(), text)
	for i := 0; i < 400; i++ {
		// Replace a random byte range with random pieces. The range may
		// split a character: SetText takes any new text.
		n := len(text)
		s := rng.Intn(n + 1)
		e := s + rng.Intn(min(n-s, 40)+1)
		var sb strings.Builder
		for k := rng.Intn(4); k > 0; k-- {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		next := text[:s] + sb.String() + text[e:]
		requireSetTextAsApply(t, a, b, next, "edit")
		text = next
	}
}
