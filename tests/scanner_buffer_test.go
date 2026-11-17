package tests

import (
	"fmt"
	"io"
	"runtime"
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
