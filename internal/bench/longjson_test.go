package bench

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLongJSONCase(t *testing.T) {
	c, ok := findCase("longjson")
	require.True(t, ok, "longjson case is missing")
	in := input(t, c)
	require.True(t, json.Valid(in), "longjson input is invalid JSON")
	l := Control
	if tagged() {
		l = Instrumented
	}
	res, err := lex(c, l, in, nop, nop)
	require.NoError(t, err)
	// Nine tokens per object, one comma between objects, and two brackets.
	const want = 512*9 + 511 + 2
	require.Truef(t, res.Tokens == want, "longjson: got %d tokens, want %d", res.Tokens, want)
}
