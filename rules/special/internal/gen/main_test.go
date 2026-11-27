package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGeneratedTableIsCurrent: the committed emoji_table.go is the output of
// gen on the committed emoji-data.txt, byte for byte, so the table and the
// data cannot drift apart without "go generate".
func TestGeneratedTableIsCurrent(t *testing.T) {
	in, err := os.Open("data/emoji-data.txt")
	require.NoError(t, err)
	defer in.Close()
	version, sets, err := parseEmojiData(in)
	require.NoError(t, err)
	want, err := render(version, sets)
	require.NoError(t, err)
	got, err := os.ReadFile("../../emoji_table.go")
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, want), "emoji_table.go is not the output of gen on data/emoji-data.txt; run go generate ./rules/special/...")
}
