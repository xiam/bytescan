package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"unicode/utf8"

	"github.com/xiam/bytescan/streamtest"
)

// TestCJKInputPinned checks that the input of the case cjk is the output of
// cjkRecord from seed 7 four times in a row, and that this output has the
// size and the SHA-256 of the input that the comparisons of #163 measured.
func TestCJKInputPinned(t *testing.T) {
	doc, err := io.ReadAll(streamtest.NewGenerator(7, cjkRecord, streamtest.WithBudget(cjkProseBudget)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(doc)
	if len(doc) != 524628 || hex.EncodeToString(sum[:]) != "1bf8d987fb0e71644d25831d140b87e534c0414711d995034815fae194066c96" {
		t.Fatalf("cjkRecord writes %d bytes with SHA-256 %x, want 524628 bytes with SHA-256 1bf8d987fb0e71644d25831d140b87e534c0414711d995034815fae194066c96", len(doc), sum)
	}
	c, ok := findCase("cjk")
	if !ok {
		t.Fatal(`findCase("cjk") finds nothing`)
	}
	if in := input(t, c); !bytes.Equal(in, bytes.Repeat(doc, 4)) {
		t.Fatalf("the input of cjk is not the prose four times: %d bytes", len(in))
	}
}

// TestCJKCase lexes the case cjk in the lane of the build (control in the
// normal build, instrumented with the tag bench), with no unknown token, and
// checks that most of its runes are above U+00FF.
func TestCJKCase(t *testing.T) {
	c, _ := findCase("cjk")
	in := input(t, c)
	l := Control
	if tagged() {
		l = Instrumented
	}
	res, err := lex(c, l, in, func() {}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tokens == 0 {
		t.Fatal("no token")
	}
	runes, wide := 0, 0
	for _, r := range string(in) {
		runes++
		if r > 0xFF {
			wide++
		}
	}
	if !utf8.Valid(in) || wide < runes*9/10 {
		t.Errorf("%d of %d runes are above U+00FF, want at least 90 percent", wide, runes)
	}
	t.Logf("%d bytes, %d runes, %d above U+00FF, %d tokens", len(in), runes, wide, res.Tokens)
}
