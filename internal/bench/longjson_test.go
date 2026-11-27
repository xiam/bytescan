package bench

import (
	"encoding/json"
	"testing"
)

func TestLongJSONCase(t *testing.T) {
	c, ok := findCase("longjson")
	if !ok {
		t.Fatal("longjson case is missing")
	}
	in := input(t, c)
	if !json.Valid(in) {
		t.Fatal("longjson input is invalid JSON")
	}
	l := Control
	if tagged() {
		l = Instrumented
	}
	res, err := lex(c, l, in, nop, nop)
	if err != nil {
		t.Fatal(err)
	}
	// Nine tokens per object, one comma between objects, and two brackets.
	const want = 512*9 + 511 + 2
	if res.Tokens != want {
		t.Fatalf("longjson: got %d tokens, want %d", res.Tokens, want)
	}
}
