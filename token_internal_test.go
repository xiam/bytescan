package bytescan

import (
	"strings"
	"testing"
)

func TestScannerSharesSingleASCIIRuneText(t *testing.T) {
	// Keep tokens from separate scanners alive across subsequent reads.
	const input = "\x00;\x7f\u0080"
	scan := func(typ TokenType) []*Token {
		sc := New(strings.NewReader(input))
		sc.MustAddRule(typ, func(Symbol) (Rule, State) { return nil, StateAccept })
		var tokens []*Token
		for range input {
			tok, err := sc.Next()
			if err != nil {
				t.Fatal(err)
			}
			tokens = append(tokens, tok)
		}
		return tokens
	}
	left, right := scan("LEFT"), scan("RIGHT")
	for i, r := range []rune(input) {
		a, b := left[i], right[i]
		if a.Text() != string(r) || b.Text() != string(r) || a.Type() != "LEFT" || b.Type() != "RIGHT" {
			t.Fatalf("rune %U: got %q (%s) and %q (%s)", r, a.Text(), a.Type(), b.Text(), b.Type())
		}
		if shared := &a.text[0] == &b.text[0]; shared != (r < 128) {
			t.Errorf("rune %U: shared text = %t", r, shared)
		}
		renamed := a.WithType("RENAMED")
		if renamed.Text() != a.Text() || renamed.Span() != a.Span() || a.Type() != "LEFT" {
			t.Errorf("rune %U: relabeling changed the original type, text or span", r)
		}
	}
}
