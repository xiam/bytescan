package tests

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
)

// The tests in this file check that Next and AddRule are safe to call from
// many goroutines and that concurrent scanners give the same stream as one scanner.

// TestScannerConcurrentAccess ensures that the scanner is goroutine-safe. Run with -race.
func TestScannerConcurrentAccess(t *testing.T) {
	const (
		lexTypeWord = bytescan.TokenType("WORD")
		numWords    = 5000
	)

	input := strings.Repeat("word ", numWords)
	lx := bytescan.New(strings.NewReader(input))

	lx.MustAddRule(lexTypeWord, newLiteralRule("word "))

	var wg sync.WaitGroup
	numGoroutines := 4
	wg.Add(numGoroutines)

	resultsChan := make(chan *bytescan.Token, numWords)
	errorsChan := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for {
				lex, err := lx.Next()
				if err == io.EOF {
					return
				}
				if err != nil {
					errorsChan <- err
					return
				}
				resultsChan <- lex
			}
		}()
	}

	wg.Wait()
	close(resultsChan)
	close(errorsChan)

	for err := range errorsChan {
		t.Errorf("Received unexpected error from Next(): %v", err)
	}

	tokenCount := 0
	for range resultsChan {
		tokenCount++
	}

	assert.Equal(t, numWords, tokenCount, "The total number of tokens from all goroutines is incorrect")
}

// TestScannerConcurrentDeterminism verifies that concurrent access produces deterministic results.
func TestScannerConcurrentDeterminism(t *testing.T) {
	const (
		lexTypeID = bytescan.TokenType("ID")
		numTokens = 100
	)

	var b strings.Builder
	for i := 0; i < numTokens; i++ {
		b.WriteString(fmt.Sprintf("id%d ", i))
	}
	input := b.String()

	for run := 0; run < 3; run++ {
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeID, identifierRule)
		lx.MustAddRule("WS", whitespaceRule)

		var mu sync.Mutex
		tokens := make([]*bytescan.Token, 0, numTokens*2)

		var wg sync.WaitGroup
		numGoroutines := 4
		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				for {
					lex, err := lx.Next()
					if err == io.EOF {
						return
					}
					require.NoError(t, err)

					mu.Lock()
					tokens = append(tokens, lex)
					mu.Unlock()
				}
			}()
		}

		wg.Wait()

		// The tokens were collected concurrently, so their order in the slice is not
		// guaranteed. We must sort them by their offset in the original input string
		// to restore the deterministic order before verification.
		sort.Slice(tokens, func(i, j int) bool {
			return tokens[i].Offset() < tokens[j].Offset()
		})

		assert.Equal(t, numTokens*2, len(tokens), "Run %d: incorrect token count", run)

		for i := 0; i < numTokens; i++ {
			idToken := tokens[i*2]
			wsToken := tokens[i*2+1]

			expectedID := fmt.Sprintf("id%d", i)
			assert.Equal(t, lexTypeID, idToken.Type(), "Run %d, Token %d: wrong type", run, i)
			assert.Equal(t, expectedID, idToken.Text(), "Run %d, Token %d: wrong text", run, i)
			assert.Equal(t, bytescan.TokenType("WS"), wsToken.Type(), "Run %d, Token %d: wrong whitespace type", run, i)
		}
	}
}

// TestScannerConcurrentAddRuleAndNext checks for race conditions when AddRule
// is called while another goroutine is calling Next. Run with -race.
func TestScannerConcurrentAddRuleAndNext(t *testing.T) {
	const (
		lexTypeA = bytescan.TokenType("A")
		lexTypeB = bytescan.TokenType("B")
		lexTypeC = bytescan.TokenType("C")
	)

	input := strings.Repeat("abc", 1000)
	lx := bytescan.New(strings.NewReader(input))
	lx.MustAddRule(lexTypeA, newLiteralRule("a"))

	lexingStarted := make(chan struct{})
	addRuleDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	var raceError error
	var mu sync.Mutex

	go func() {
		defer wg.Done()
		// Signal after the first Next has returned, so that AddRule starts
		// only once the processor exists. Whether AddRule then overlaps a
		// later Next is up to the scheduler; -race reports a race either way.
		signal := sync.OnceFunc(func() { close(lexingStarted) })
		defer signal()
		for {
			_, err := lx.Next()
			signal()
			if err == io.EOF {
				return
			}
			if err != nil {
				mu.Lock()
				if raceError == nil {
					raceError = err
				}
				mu.Unlock()
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		defer close(addRuleDone)
		<-lexingStarted

		err := lx.AddRule(lexTypeB, newLiteralRule("b"))
		if err != nil {
			mu.Lock()
			if raceError == nil {
				raceError = err
			}
			mu.Unlock()
		}

		err = lx.AddRule(lexTypeC, newLiteralRule("c"))
		if err != nil {
			mu.Lock()
			if raceError == nil {
				raceError = err
			}
			mu.Unlock()
		}
	}()

	wg.Wait()
	assert.NoError(t, raceError, "Encountered error during concurrent operations")
}
