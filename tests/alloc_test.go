package tests

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
)

// endlessInput is an io.Reader that repeats pat forever: a long-lived input
// with no end, so an allocation test measures the steady state only.
type endlessInput struct {
	pat []byte
	off int
}

func (e *endlessInput) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		k := copy(p[n:], e.pat[e.off:])
		n += k
		e.off = (e.off + k) % len(e.pat)
	}
	return n, nil
}

// allocLetters and allocSpace are hand-written rules that allocate nothing:
// top-level functions with no captured state.
func allocLetters(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if r := s.Rune(); r >= 'a' && r <= 'z' {
		return allocLetters, bytescan.StateAccept
	}
	return nil, bytescan.StateReject
}

func allocSpace(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if s.Rune() == ' ' {
		return allocSpace, bytescan.StateAccept
	}
	return nil, bytescan.StateReject
}

// allocShape is a rule set and an input cycle of tokens tokens.
type allocShape struct {
	name   string
	cycle  string
	tokens int
	rules  func(lx *bytescan.Scanner)
	// max is the ceiling in allocations per token. exact asks for the
	// ceiling exactly.
	max   float64
	exact bool
}

// allocsPerToken lexes the endless repetition of shape.cycle and returns the
// steady-state allocations per token, averaged over whole input cycles.
func allocsPerToken(t *testing.T, shape allocShape) float64 {
	t.Helper()

	lx := bytescan.New(bufio.NewReader(&endlessInput{pat: []byte(shape.cycle)}))
	shape.rules(lx)

	cycle := func() {
		for i := 0; i < shape.tokens; i++ {
			if _, err := lx.Next(); err != nil {
				t.Fatalf("Next: %v", err)
			}
		}
	}
	for i := 0; i < 1000; i++ {
		cycle() // warm up: buffers and checkpoint slots reach their steady size
	}
	return testing.AllocsPerRun(1000, cycle) / float64(shape.tokens)
}

// TestScannerAllocationsPerToken covers the steady-state allocations of a
// token. A token of at most 8 runes keeps its text in the same allocation as
// its Token, so a hand-written rule set costs exactly one allocation per
// token: the Token itself. The literal rules of rules/core build their states when
// they are constructed, so a step allocates nothing and core.Literal costs no
// more than a hand-written rule.
func TestScannerAllocationsPerToken(t *testing.T) {
	shapes := []allocShape{
		{
			name: "hand-written", cycle: "ab ", tokens: 2, max: 1, exact: true,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("WORD", allocLetters)
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.Literal", cycle: "ab ", tokens: 2, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("WORD", core.Literal("ab"))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.LiteralIgnoreCase", cycle: "aB ", tokens: 2, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("WORD", core.LiteralIgnoreCase("ab"))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.AnyLiteral", cycle: "ab abc ", tokens: 4, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("WORD", core.AnyLiteral("ab", "abc", "x"))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.Keyword", cycle: "if ", tokens: 2, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("IF", core.Keyword("if", core.ASCIIWord))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.AnyLiteralIgnoreCase", cycle: "aB ABC ", tokens: 4, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("WORD", core.AnyLiteralIgnoreCase("ab", "abc", "x"))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.Keywords", cycle: "if else elseif ", tokens: 6, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("KEYWORD", core.Keywords("if", "else", "elseif"))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
		{
			name: "core.KeywordsIgnoreCase", cycle: "IF Else elseIF ", tokens: 6, max: 1,
			rules: func(lx *bytescan.Scanner) {
				lx.MustAddRule("KEYWORD", core.KeywordsIgnoreCase("if", "else", "elseif"))
				lx.MustAddRule("SPACE", allocSpace)
			},
		},
	}

	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			got := allocsPerToken(t, shape)
			t.Logf("%s on %q: %.2f allocations per token", shape.name, shape.cycle, got)
			if shape.exact {
				assert.Equal(t, shape.max, got, "allocations per token")
				return
			}
			assert.LessOrEqual(t, got, shape.max, "allocations per token")
		})
	}
}

// TestScannerTokenTextStable covers the inline text of a short token: a
// returned token's text and span stay correct and unchanged after later Next
// calls, for a token of 1 rune, of 8 runes (the longest stored inline), and
// of 9 runes (the shortest stored apart). The tokens are multi-byte, so the
// span's byte and rune offsets differ.
func TestScannerTokenTextStable(t *testing.T) {
	words := []string{"é", "ñandúñan", "ñandúñand"}
	input := strings.Join(words, " ") + " "

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("WORD", newUnicodeLettersRule())
	lx.MustAddRule("SPACE", allocSpace)

	type want struct {
		text                string
		runeOffset, byteOff int
		endRune, endByte    int
		token               *bytescan.Token
	}
	var got []want
	runeOff, byteOff := 0, 0
	for {
		l, err := lx.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		w := want{
			text:       l.Text(),
			runeOffset: runeOff,
			byteOff:    byteOff,
			token:      l,
		}
		runeOff += len([]rune(l.Text()))
		byteOff += len(l.Text())
		w.endRune, w.endByte = runeOff, byteOff
		got = append(got, w)
	}

	var texts []string
	var lens []int
	for _, w := range got {
		if w.token.Type() == "WORD" {
			texts = append(texts, w.text)
			lens = append(lens, w.token.Len())
		}
	}
	require.Equal(t, words, texts)
	require.Equal(t, []int{1, 8, 9}, lens)

	// Every token is checked after the whole input is lexed, so each one has
	// seen every later Next call.
	for _, w := range got {
		l := w.token
		assert.Equal(t, w.text, l.Text())
		assert.Equal(t, len([]rune(w.text)), l.Len())
		assert.Equal(t, w.runeOffset, l.Offset())
		assert.Equal(t, w.runeOffset, l.Span().Start().RuneOffset())
		assert.Equal(t, w.byteOff, l.Span().Start().ByteOffset())
		assert.Equal(t, w.endRune, l.Span().End().RuneOffset())
		assert.Equal(t, w.endByte, l.Span().End().ByteOffset())
	}
}
