package bytescan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, err)
			tokens = append(tokens, tok)
		}
		return tokens
	}
	left, right := scan("LEFT"), scan("RIGHT")
	for i, r := range []rune(input) {
		a, b := left[i], right[i]
		if a.Text() != string(r) || b.Text() != string(r) || a.Type() != "LEFT" || b.Type() != "RIGHT" {
			require.FailNowf(t, "unexpected test state", "rune %U: got %q (%s) and %q (%s)", r, a.Text(), a.Type(), b.Text(), b.Type())
		}
		{
			shared := &a.text[0] == &b.text[0]
			assert.Truef(t, shared == (r < 128), "rune %U: shared text = %t", r, shared)
		}
		renamed := a.WithType("RENAMED")
		assert.Falsef(t, renamed.Text() != a.Text() || renamed.Span() != a.Span() || a.Type() != "LEFT", "rune %U: relabeling changed the original type, text or span", r)
	}
}
