package bench

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestColdCases lexes each cold case in the lane of the build (control in
// the normal build, instrumented with the tag bench): every input has 64
// bytes and lexes without an unknown token, and findCase finds the case.
func TestColdCases(t *testing.T) {
	l := Control
	if tagged() {
		l = Instrumented
	}
	for _, c := range coldCases() {
		in := input(t, c)
		assert.Truef(t, len(in) == 64, "%s: %d bytes, want 64", c.name, len(in))
		res, err := lex(c, l, in, func() {}, func() {})
		require.NoErrorf(t, err, "%s: %v", c.name, err)
		assert.Falsef(t, res.Tokens == 0, "%s: no token", c.name)
		{
			_, ok := findCase(c.name)
			assert.Truef(t, ok, "findCase(%q) finds nothing", c.name)
		}
	}
}
