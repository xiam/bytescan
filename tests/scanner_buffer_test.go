package tests

import (
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check the buffer and resource limits: tokens larger
// than the initial buffer, compaction, the maximum buffer size and pushback
// depth, and rules that try to exhaust the stack or memory.

// TestScannerTokenLargerThanInitialBuffer verifies the internal buffer can grow
// to accommodate a token larger than its initial capacity.
func TestScannerTokenLargerThanInitialBuffer(t *testing.T) {
	const (
		lexTypeInteger    = bytescan.TokenType("INT")
		lexTypeWhitespace = bytescan.TokenType("WHITESPACE")
		initialBufferSize = 4096
		largeTokenSize    = initialBufferSize * 2
	)

	largeToken := strings.Repeat("1", largeTokenSize)
	input := largeToken + " "

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeInteger, unsignedIntegerRule)
	lx.MustAddRule(lexTypeWhitespace, whitespaceRule)

	lex, err := lx.Next()
	require.NoError(t, err)
	require.NotNil(t, lex)
	assert.Equal(t, lexTypeInteger, lex.Type())
	assert.Equal(t, largeToken, lex.Text())
	assert.Equal(t, largeTokenSize, lex.Len())
	assert.Equal(t, 0, lex.Offset())

	lex, err = lx.Next()
	require.NoError(t, err)
	require.NotNil(t, lex)
	assert.Equal(t, lexTypeWhitespace, lex.Type())
	assert.Equal(t, " ", lex.Text())

	_, err = lx.Next()
	require.Equal(t, io.EOF, err)
}

// TestScannerWithLargeInputOfManySmallTokens checks for resource exhaustion issues,
// such as a buffer that grows indefinitely without compaction.
func TestScannerWithLargeInputOfManySmallTokens(t *testing.T) {
	const (
		lexTypeIdentifier = bytescan.TokenType("ID")
		lexTypeWhitespace = bytescan.TokenType("WS")
		numTokens         = 100_000
	)

	var b strings.Builder
	b.Grow(numTokens * 2)
	for i := 0; i < numTokens; i++ {
		b.WriteString("a ")
	}
	input := b.String()

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeIdentifier, identifierRule)
	lx.MustAddRule(lexTypeWhitespace, whitespaceRule)

	tokenCount := 0
	for {
		_, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		tokenCount++
	}

	assert.Equal(t, numTokens*2, tokenCount, "Did not process the expected number of tokens")
}

// TestScannerBufferCompaction verifies that the internal buffer compacts after
// processing large tokens, preventing unbounded memory growth.
func TestScannerBufferCompaction(t *testing.T) {
	const (
		lexTypeID         = bytescan.TokenType("ID")
		lexTypeWhitespace = bytescan.TokenType("WS")
		longTokenSize     = 1 << 20
		numShortTokens    = 5_000
	)

	var b strings.Builder
	b.WriteString(strings.Repeat("a", longTokenSize))
	b.WriteString(" ")
	b.WriteString(strings.Repeat("b ", numShortTokens))
	input := b.String()

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeID, identifierRule)
	lx.MustAddRule(lexTypeWhitespace, whitespaceRule)

	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	totalTokensProcessed := 0

	lex, err := lx.Next()
	require.NoError(t, err)
	assert.Equal(t, longTokenSize, lex.Len())
	totalTokensProcessed++

	_, err = lx.Next() // whitespace
	require.NoError(t, err)
	totalTokensProcessed++

	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	memAfterLargeToken := m2.Alloc

	for i := 0; i < numShortTokens; i++ {
		_, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		totalTokensProcessed++
	}

	runtime.GC()
	var m3 runtime.MemStats
	runtime.ReadMemStats(&m3)
	memAfterSmallTokens := m3.Alloc

	// The memory usage should not grow significantly after processing small tokens
	// if the buffer was properly compacted.
	memGrowth := int64(0)
	if memAfterSmallTokens > memAfterLargeToken {
		memGrowth = int64(memAfterSmallTokens - memAfterLargeToken)
	}

	// Allow some growth but it should be much less than the large token size.
	maxAllowedGrowth := int64(longTokenSize / 2)
	assert.LessOrEqual(t, memGrowth, maxAllowedGrowth,
		"Memory grew by %d bytes after processing small tokens, suggesting buffer wasn't compacted", memGrowth)

	for {
		_, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		totalTokensProcessed++
	}

	expectedTokenCount := 1 + 1 + (numShortTokens * 2)
	assert.Equal(t, expectedTokenCount, totalTokensProcessed)
}

// TestScannerMaximumLimits tests boundary conditions for various scanner limits.
func TestScannerMaximumLimits(t *testing.T) {
	t.Run("Maximum buffer size", func(t *testing.T) {
		const maxBufferSize = 1024 * 1024 // 1MB
		largeToken := strings.Repeat("a", maxBufferSize)
		input := largeToken + " end"

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("ID", identifierRule)
		lx.MustAddRule("WS", whitespaceRule)

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, maxBufferSize, lex.Len())

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("WS"), lex.Type())

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, "end", lex.Text())
	})

	t.Run("Maximum pushback depth", func(t *testing.T) {
		const maxPushback = 1000
		input := strings.Repeat("a", maxPushback+10)

		// deepPushbackRule continues over every 'a' and, at end of input,
		// excludes the last `exclude` symbols of the stack. The stack then holds
		// maxPushback+10 'a's plus the EOF symbol.
		//
		// Engine probe: with exclude above the stack depth it passes the bottom
		// of the stack on purpose (breaks practices 1, 2 and 6).
		deepPushbackRule := func(exclude int) bytescan.Rule {
			var loop bytescan.Rule
			loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
				if s.Rune() == 'a' {
					return loop, bytescan.StateContinue
				}
				if s.IsEOF() {
					return excludeLast(exclude, bytescan.StateAccept)
				}
				return nil, bytescan.StateReject
			}
			return loop
		}

		// A chain of maxPushback+1 pushbacks (the EOF symbol and 1000 'a's)
		// runs to its accept and leaves the first 10 'a's.
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("DEEP_PUSHBACK", deepPushbackRule(maxPushback+1))

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("DEEP_PUSHBACK"), lex.Type())
		assert.Equal(t, strings.Repeat("a", 10), lex.Text())
		assert.Equal(t, 0, lex.Offset())

		// One pushback more than the stack holds passes its bottom, so the VM
		// retires the rule (processor.go:110) and no rule matches the first 'a'.
		lx = bytescan.New(strings.NewReader(input))
		lx.MustAddRule("DEEP_PUSHBACK", deepPushbackRule(maxPushback+12))

		lex, err = lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenTypeUnknown, lex.Type())
		assert.Equal(t, "a", lex.Text())
		assert.Equal(t, 0, lex.Offset())
	})

	t.Run("Maximum number of concurrent scanners", func(t *testing.T) {
		const numRules = 10000
		input := "test"

		lx := bytescan.New(strings.NewReader(input))

		// Add many rules that all start matching 't'.
		for i := 0; i < numRules; i++ {
			ruleText := fmt.Sprintf("t%d", i)
			ruleType := bytescan.TokenType(fmt.Sprintf("RULE_%d", i))
			lx.MustAddRule(ruleType, newLiteralRule(ruleText))
		}
		lx.MustAddRule("TEST", newLiteralRule("test"))

		lex, err := lx.Next()
		require.NoError(t, err)
		assert.Equal(t, bytescan.TokenType("TEST"), lex.Type())
		assert.Equal(t, "test", lex.Text())
	})
}

// TestScannerResourceExhaustion tests the scanner's handling of resource-intensive scenarios.
func TestScannerResourceExhaustion(t *testing.T) {
	t.Run("Stack Depth Exhaustion", func(t *testing.T) {
		// This test verifies that the scanner's processing loop is iterative and does not
		// exhaust the call stack when processing a rule with a very long chain of
		// StateContinue transitions.
		const maxDepth = 10000

		// The rule carries its depth in the Rule values it returns and never
		// calls itself, so it is stack-safe. This tests the scanner's loop, not
		// the Go runtime's stack limit.

		input := strings.Repeat("a", maxDepth)
		lx := bytescan.New(strings.NewReader(input))
		// The first Next invokes the rule once per symbol and the EOF probe
		// once more, so the run takes maxDepth+1 invocations. The budget
		// doubles that; a scanner stuck in a loop exceeds it and fails the test.
		budget := 2 * (maxDepth + 1)
		lx.MustAddRule("DEEP", boundedRule(t, budget, newARunOfLengthRule(maxDepth)))

		// Next runs on the test goroutine, so a panic fails the test. A Go
		// stack overflow is fatal and cannot be recovered in any case.
		lex, err := lx.Next()
		require.NoError(t, err)
		require.NotNil(t, lex)
		assert.Equal(t, maxDepth, lex.Len())

		_, err = lx.Next()
		require.Equal(t, io.EOF, err)
	})

	t.Run("Memory Compaction Under Pressure", func(t *testing.T) {
		// Alternate between large and small tokens to test memory compaction.
		var b strings.Builder
		for i := 0; i < 100; i++ {
			b.WriteString(strings.Repeat("a", 10000))
			b.WriteString(" ")
			for j := 0; j < 100; j++ {
				b.WriteString("b ")
			}
		}
		input := b.String()

		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule("ID", identifierRule)
		lx.MustAddRule("WS", whitespaceRule)

		runtime.GC()
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		initialMem := m1.Alloc

		tokenCount := 0
		for {
			_, err := lx.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			tokenCount++

			if tokenCount%1000 == 0 {
				runtime.GC()
				var m2 runtime.MemStats
				runtime.ReadMemStats(&m2)
				memGrowth := int64(0)
				if m2.Alloc > initialMem {
					memGrowth = int64(m2.Alloc - initialMem)
				}
				// Memory growth should be bounded, not linear with input.
				maxExpectedGrowth := int64(100 * 1024 * 1024) // 100MB max
				assert.LessOrEqual(t, memGrowth, maxExpectedGrowth,
					"Memory grew by %d bytes, suggesting a leak", memGrowth)
			}
		}
		assert.Greater(t, tokenCount, 10000, "Should process many tokens")
	})
}

// BenchmarkScannerPerformance provides performance benchmarks for common scenarios.
func BenchmarkScannerPerformance(b *testing.B) {
	b.Run("SimpleTokens", func(b *testing.B) {
		input := strings.Repeat("abc def 123 ", 1000)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lx := bytescan.New(strings.NewReader(input))
			lx.MustAddRule("ID", identifierRule)
			lx.MustAddRule("INT", unsignedIntegerRule)
			lx.MustAddRule("WS", whitespaceRule)

			for {
				_, err := lx.Next()
				if err == io.EOF {
					break
				}
			}
		}
	})

	b.Run("ComplexRules", func(b *testing.B) {
		input := strings.Repeat("123.456 'string' /* comment */ identifier ", 1000)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lx := bytescan.New(strings.NewReader(input))
			lx.MustAddRule("FLOAT", unsignedFloatRule)
			lx.MustAddRule("STRING", newSingleQuotedStringRule())
			lx.MustAddRule("COMMENT", newSlashStarCommentRule())
			lx.MustAddRule("ID", identifierRule)
			lx.MustAddRule("WS", whitespaceRule)

			for {
				_, err := lx.Next()
				if err == io.EOF {
					break
				}
			}
		}
	})

	b.Run("LargeTokens", func(b *testing.B) {
		largeToken := strings.Repeat("a", 10000)
		input := strings.Repeat(largeToken+" ", 100)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lx := bytescan.New(strings.NewReader(input))
			lx.MustAddRule("ID", identifierRule)
			lx.MustAddRule("WS", whitespaceRule)

			for {
				_, err := lx.Next()
				if err == io.EOF {
					break
				}
			}
		}
	})

	b.Run("ManyRules", func(b *testing.B) {
		input := strings.Repeat("test ", 1000)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			lx := bytescan.New(strings.NewReader(input))

			for j := 0; j < 100; j++ {
				ruleType := bytescan.TokenType(fmt.Sprintf("RULE_%d", j))
				ruleText := fmt.Sprintf("keyword%d", j)
				lx.MustAddRule(ruleType, newLiteralRule(ruleText))
			}
			lx.MustAddRule("TEST", newLiteralRule("test"))
			lx.MustAddRule("WS", whitespaceRule)

			for {
				_, err := lx.Next()
				if err == io.EOF {
					break
				}
			}
		}
	})
}

func TestScannerBacktrackingAndBufferState(t *testing.T) {
	const (
		lexTypeFor = bytescan.TokenType("FOR")
		lexTypeID  = bytescan.TokenType("ID")
	)

	// This rule matches "for" but only if it's not followed by another letter.
	// It does not accept at 'r', because a later pushback could not withdraw that
	// accept. It continues and decides on the symbol after "for": a letter makes
	// it reject; any other symbol is pushed back and "for" is accepted.
	forKeywordRule := newKeywordRule("for", isASCIILetter)

	input := "for format"
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeFor, forKeywordRule)
	lx.MustAddRule(lexTypeID, newIdentifierRule())
	lx.MustAddRule("WS", newWhitespaceRule())

	// 1. "for" followed by space. The rule should accept "for" and push back the space.
	lex, err := lx.Next()
	require.NoError(t, err)
	assert.Equal(t, lexTypeFor, lex.Type())
	assert.Equal(t, "for", lex.Text())
	assert.Equal(t, 0, lex.Offset())

	// 2. Whitespace. Should be correctly processed from the backtracked buffer.
	lex, err = lx.Next()
	require.NoError(t, err)
	assert.Equal(t, " ", lex.Text())
	assert.Equal(t, 3, lex.Offset())

	// 3. "format". The keyword rule reads "for" and decides on the next
	//    symbol, 'm': a letter, so it rejects without having accepted. The ID
	//    rule wins with the longest match, "format".
	lex, err = lx.Next()
	require.NoError(t, err)
	assert.Equal(t, lexTypeID, lex.Type())
	assert.Equal(t, "format", lex.Text())
	assert.Equal(t, 4, lex.Offset())

	// 4. End of file.
	_, err = lx.Next()
	assert.Equal(t, io.EOF, err)
}

// longTokenRunes is the length, in runes, of the long tokens below: more than
// 1 Mi runes, so the stack of the token grows many times past its initial
// capacity.
const longTokenRunes = 1<<20 + 7

// longTokenText returns n runes that cycle through runes of one to four bytes
// and a newline, so that the symbols of a long token differ in rune, in size
// and in flags (FlagEOL, FlagBOL).
func longTokenText(n int) string {
	cycle := []rune{'a', 'é', '€', '𝄞', '\n', 'b'}
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteRune(cycle[i%len(cycle)])
	}
	return b.String()
}

// longTokenLine renders a token without its text, which can be long: its type,
// its rune count, its line and column, and its byte span.
func longTokenLine(tok *bytescan.Token) string {
	s := tok.Span()
	return fmt.Sprintf("%s +%d @%d:%d [%d,%d)", tok.Type(), s.Runes(), tok.Line(), tok.Column(), s.Start().ByteOffset(), s.End().ByteOffset())
}

// halfBackProbe counts what the pushback chains of newHalfBackRule are shown.
type halfBackProbe struct {
	shown      int // symbols shown by a pushback
	mismatches int // shown symbols that differ from the symbol read there
}

// newHalfBackRule returns an engine probe for one token of runes other than
// ';'. It continues over every such rune and keeps the symbols it reads. At
// the terminator, ';' or the EOF symbol, it pushes back over the terminator
// and then over the second half of the token, and accepts the first half (at
// least one rune). Each symbol a pushback shows is compared with the symbol
// read at that place of the stack. It keeps the symbols of the token on
// purpose, which docs/rule-authoring.md asks a rule not to do.
func newHalfBackRule(p *halfBackProbe) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		var seen []bytescan.Symbol
		var step bytescan.Rule
		step = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.IsEOF() || s.Rune() == ';' {
				if len(seen) == 0 {
					return nil, bytescan.StateReject
				}
				keep := max(1, len(seen)/2)
				return halfBackAt(p, seen, s, len(seen), keep), bytescan.StatePushBack
			}
			seen = append(seen, s)
			return step, bytescan.StateContinue
		}
		return step(s)
	}
}

// halfBackAt returns the pushback-chain callback that is shown the symbol at
// index i of the token: seen[i], or term when i is len(seen). At index keep
// it accepts, which matches seen[:keep]; above it, it pushes back once more.
func halfBackAt(p *halfBackProbe, seen []bytescan.Symbol, term bytescan.Symbol, i, keep int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		want := term
		if i < len(seen) {
			want = seen[i]
		}
		p.shown++
		if s != want {
			p.mismatches++
		}
		if i == keep {
			return nil, bytescan.StateAccept
		}
		return halfBackAt(p, seen, term, i-1, keep), bytescan.StatePushBack
	}
}

// wantHalfBack returns the texts of the tokens that newHalfBackRule makes of
// a run of runes, and the number of symbols its pushback chains are shown: each
// token is the first half of what is left, and its chain is shown the
// terminator and the second half.
func wantHalfBack(run []rune) (texts []string, shown int) {
	for len(run) > 0 {
		keep := max(1, len(run)/2)
		shown += 1 + len(run) - keep
		texts = append(texts, string(run[:keep]))
		run = run[keep:]
	}
	return texts, shown
}

// scanLong returns the tokens of src until io.EOF, rendered by longTokenLine,
// and their texts.
func scanLong(t *testing.T, src bytescan.Source) (lines, texts []string) {
	t.Helper()
	for {
		tok, err := src.Next()
		if err == io.EOF {
			return lines, texts
		}
		require.NoError(t, err)
		lines = append(lines, longTokenLine(tok))
		texts = append(texts, tok.Text())
	}
}

// longTokenWant renders the expected tokens of input for longTokenLine: types
// and texts in order, which must cover input.
func longTokenWant(t *testing.T, input string, types, texts []string) []string {
	t.Helper()
	require.Len(t, types, len(texts))
	var lines []string
	runeOff, byteOff, line, col := 0, 0, 1, 0
	for i, text := range texts {
		require.True(t, strings.HasPrefix(input[byteOff:], text), "token %d does not follow the previous one", i)
		n := len([]rune(text))
		lines = append(lines, fmt.Sprintf("%s +%d @%d:%d [%d,%d)", types[i], n, line, col, byteOff, byteOff+len(text)))
		for _, r := range text {
			if r == '\n' {
				line, col = line+1, 0
			} else {
				col++
			}
		}
		runeOff += n
		byteOff += len(text)
	}
	require.Equal(t, len(input), byteOff, "the tokens do not cover the input")
	return lines
}

// TestLongTokenPushBackAcrossTheWholeToken covers a pushback chain over a
// long token: newHalfBackRule pushes back over half of a run of more than
// 1 Mi runes, and is shown every symbol it read there, unchanged, top first.
// One run ends at ';' and one at the end of input.
func TestLongTokenPushBackAcrossTheWholeToken(t *testing.T) {
	run := longTokenText(longTokenRunes)
	input := run + ";" + run

	var p halfBackProbe
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("HALF", newHalfBackRule(&p))
	lx.MustAddRule("SEMI", newLiteralRule(";"))
	lines, texts := scanLong(t, lx)

	half, shown := wantHalfBack([]rune(run))
	var wantTypes, wantTexts []string
	for i := range half {
		wantTypes, wantTexts = append(wantTypes, "HALF"), append(wantTexts, half[i])
	}
	wantTypes, wantTexts = append(wantTypes, "SEMI"), append(wantTexts, ";")
	for i := range half {
		wantTypes, wantTexts = append(wantTypes, "HALF"), append(wantTexts, half[i])
	}

	assert.Equal(t, longTokenWant(t, input, wantTypes, wantTexts), lines)
	assert.True(t, slices.Equal(wantTexts, texts), "token texts")
	assert.Equal(t, 2*shown, p.shown, "symbols shown by a pushback")
	assert.Zero(t, p.mismatches, "shown symbols that differ from the symbols read")
}

// newGiveUpRule returns an engine probe that continues over every rune but
// '?' and rejects at '?' and at the end of input. When the token starts with
// "xyz", it accepts after the 'z' first. So it reads to the end of a long
// token and gives up there, with or without an accept recorded earlier.
func newGiveUpRule() bytescan.Rule {
	var loop bytescan.Rule
	loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() || s.Rune() == '?' {
			return nil, bytescan.StateReject
		}
		return loop, bytescan.StateContinue
	}
	prefix := []rune("xyz")
	var at func(i int) bytescan.Rule
	at = func(i int) bytescan.Rule {
		return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
			if s.Rune() != prefix[i] {
				return loop(s)
			}
			if i == len(prefix)-1 {
				return loop, bytescan.StateAccept
			}
			return at(i + 1), bytescan.StateContinue
		}
	}
	return at(0)
}

// TestLongTokenRuleGivesUpAtTheEnd covers rules that read to the end of a long
// token and give up there. GIVEUP accepts "xyz" and then reads the run of more
// than 1 Mi runes that follows: the token is "xyz", decided at the end of the
// run. On the run itself GIVEUP has no accept and gives up at its end, and RUN
// matches the run. One run ends at '?' and one at the end of input.
func TestLongTokenRuleGivesUpAtTheEnd(t *testing.T) {
	run := longTokenText(longTokenRunes)
	input := "xyz" + run + "?" + "xyz" + run

	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule("GIVEUP", newGiveUpRule())
	lx.MustAddRule("RUN", newRunePlusRule(func(r rune) bool { return !strings.ContainsRune("xyz?", r) }))
	lx.MustAddRule("Q", newLiteralRule("?"))
	lines, texts := scanLong(t, lx)

	wantTypes := []string{"GIVEUP", "RUN", "Q", "GIVEUP", "RUN"}
	wantTexts := []string{"xyz", run, "?", "xyz", run}
	assert.Equal(t, longTokenWant(t, input, wantTypes, wantTexts), lines)
	assert.True(t, slices.Equal(wantTexts, texts), "token texts")
}
