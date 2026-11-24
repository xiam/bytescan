package bench

import (
	"testing"
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
		if len(in) != 64 {
			t.Errorf("%s: %d bytes, want 64", c.name, len(in))
		}
		res, err := lex(c, l, in, func() {}, func() {})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Tokens == 0 {
			t.Errorf("%s: no token", c.name)
		}
		if _, ok := findCase(c.name); !ok {
			t.Errorf("findCase(%q) finds nothing", c.name)
		}
	}
}
