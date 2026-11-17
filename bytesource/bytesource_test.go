package bytesource

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// allBytes holds every byte value once.
func allBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestTextBytes(t *testing.T) {
	b := allBytes()
	text := Text(string(b))
	if n := len([]rune(text)); n != 256 {
		t.Fatalf("Text gives %d runes, want 256", n)
	}
	if got := Bytes(text); !bytes.Equal(got, b) {
		t.Errorf("Bytes(Text(b)) = % x, want % x", got, b)
	}
}

// TestOffsets checks that a token's Offset and Len are byte offsets and
// lengths, also after bytes of 0x80 and above and after 0x0A.
func TestOffsets(t *testing.T) {
	in := []byte{0xFF, 0x0A, 0x80, 'A', 'B', 0xFF, 0xD8}
	lx := bytescan.NewWithCursor(NewCursor(bytes.NewReader(in)))
	lx.MustAddRule("AB", core.Literal("AB"))
	lx.MustAddRule("SOI", core.Literal(Text("\xff\xd8")))
	var got []string
	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, Hex(l))
	}
	want := []string{
		"0 UNKNOWN 1 ff",
		"1 UNKNOWN 1 0a",
		"2 UNKNOWN 1 80",
		"3 AB 2 41 42",
		"5 SOI 2 ff d8",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestHexLong(t *testing.T) {
	l := bytescan.NewTokenFromString("DATA", Text(string(allBytes()[:20])), 7)
	want := "7 DATA 20 00 01 02 03 04 05 06 07 08 09 0a 0b 0c 0d 0e 0f ..."
	if got := Hex(l); got != want {
		t.Errorf("Hex = %q, want %q", got, want)
	}
}

func TestPeekRead(t *testing.T) {
	cur := NewCursor(bytes.NewReader([]byte{1, 2, 3, 0xFE, 5}))
	p, err := Peek(cur, 2)
	if err != nil || !bytes.Equal(p, []byte{1, 2}) {
		t.Fatalf("Peek = % x, %v", p, err)
	}
	l, err := Read(cur, "A", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := Hex(l); got != "0 A 4 01 02 03 fe" {
		t.Errorf("Read = %q", got)
	}
	if p, _ := Peek(cur, 3); !bytes.Equal(p, []byte{5}) {
		t.Errorf("Peek at the end = % x, want 05", p)
	}
	l, _ = Read(cur, "B", 3)
	if got := Hex(l); got != "4 B 1 05" {
		t.Errorf("short Read = %q", got)
	}
	if l, err := Read(cur, "C", 1); l != nil || err != nil {
		t.Errorf("Read at the end = %v, %v; want nil, nil", l, err)
	}
}

func TestFields(t *testing.T) {
	cur := NewCursor(bytes.NewReader([]byte("abcdefg")))
	f := NewFields(cur)
	var then []string
	f.Push(
		Field{Type: "EMPTY", Len: 0, Then: func(b []byte) { then = append(then, "empty") }},
		Field{Type: "A", Len: 2, Then: func(b []byte) {
			then = append(then, string(b))
			f.Push(Field{Type: "C", Len: 1})
		}},
		Field{Type: "B", Len: 1},
	)
	var got []string
	for {
		l, err := f.NextField()
		if err != nil {
			t.Fatal(err)
		}
		if l == nil {
			break
		}
		got = append(got, Hex(l))
	}
	want := "0 A 2 61 62|2 B 1 63|3 C 1 64"
	if strings.Join(got, "|") != want {
		t.Errorf("fields = %q, want %q", strings.Join(got, "|"), want)
	}
	if strings.Join(then, "|") != "empty|ab" {
		t.Errorf("Then calls = %q", then)
	}

	// A field cut by the end of the input drops the rest of the queue.
	f.Push(Field{Type: "D", Len: 5}, Field{Type: "E", Len: 1})
	l, _ := f.NextField()
	if got := Hex(l); got != "4 D 3 65 66 67" {
		t.Errorf("short field = %q", got)
	}
	if l, _ := f.NextField(); l != nil {
		t.Errorf("after a short field: %q, want nil", Hex(l))
	}
}
