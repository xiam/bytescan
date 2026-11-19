package modes

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// stackOf builds a stack from the bottom name to the top name.
func stackOf(names ...string) Stack {
	var s Stack
	for _, n := range names {
		s = s.push(n)
	}
	return s
}

func TestStackEqual(t *testing.T) {
	for _, tc := range []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"code"}, []string{"code"}, true},
		{[]string{"code", "str"}, []string{"code", "str"}, true},
		{[]string{"code", "str", "code"}, []string{"code", "str", "code"}, true},
		{nil, []string{"code"}, false},
		{[]string{"code"}, []string{"str"}, false},
		{[]string{"code", "str"}, []string{"str", "code"}, false},
		{[]string{"code"}, []string{"code", "code"}, false},
		{[]string{"code", "str"}, []string{"code"}, false},
		{[]string{"a", "b", "c"}, []string{"x", "b", "c"}, false},
	} {
		// Two stacks built apart share no frame.
		a, b := stackOf(tc.a...), stackOf(tc.b...)
		assert.Equal(t, tc.want, a.Equal(b), "%v Equal %v", tc.a, tc.b)
		assert.Equal(t, tc.want, b.Equal(a), "%v Equal %v", tc.b, tc.a)
		assert.True(t, a.Equal(a), "%v Equal itself", tc.a)
	}

	// Stacks that share lower frames compare by their names too.
	base := stackOf("code", "str")
	assert.True(t, base.push("x").Equal(base.push("x")))
	assert.False(t, base.push("x").Equal(base.push("y")))
	assert.True(t, base.push("x").pop().Equal(base))
}

func TestStackTopLen(t *testing.T) {
	var zero Stack
	assert.Equal(t, "", zero.Top())
	assert.Equal(t, 0, zero.Len())

	s := stackOf("code", "str")
	assert.Equal(t, "str", s.Top())
	assert.Equal(t, 2, s.Len())

	// pop removes the top, and leaves the last mode in place.
	p := s.pop()
	assert.Equal(t, "code", p.Top())
	assert.Equal(t, 1, p.Len())
	assert.True(t, p.pop().Equal(p))
	assert.True(t, zero.pop().Equal(zero))

	// The source of a push or a pop does not change.
	assert.Equal(t, "str", s.Top())
	assert.Equal(t, 2, s.Len())
}
