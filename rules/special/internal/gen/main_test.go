package main

import (
	"bytes"
	"os"
	"testing"
)

// TestGeneratedTableIsCurrent: the committed emoji_table.go is the output of
// gen on the committed emoji-data.txt, byte for byte, so the table and the
// data cannot drift apart without "go generate".
func TestGeneratedTableIsCurrent(t *testing.T) {
	in, err := os.Open("data/emoji-data.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	version, sets, err := parseEmojiData(in)
	if err != nil {
		t.Fatal(err)
	}
	want, err := render(version, sets)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../emoji_table.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("emoji_table.go is not the output of gen on data/emoji-data.txt; run go generate ./rules/special/...")
	}
}
