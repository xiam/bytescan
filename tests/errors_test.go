package tests

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
)

// TestScannerErrorIdentities checks how each error the root package returns is
// matched. A failure that the package decides matches its exported sentinel
// with errors.Is. An error that the package does not decide keeps its
// identity: the end of input is io.EOF itself, and an error from the reader,
// the cursor still matches the original error.
func TestScannerErrorIdentities(t *testing.T) {
	letters := func(input string) *bytescan.Scanner {
		lx := bytescan.New(strings.NewReader(input))
		lx.MustAddRule(lexTypeWord, newASCIILettersRule())
		return lx
	}

	testCases := []struct {
		name string
		// run returns the error under test.
		run func(t *testing.T) error
		// is lists the errors that errors.Is must match.
		is []error
		// isNot lists the errors that errors.Is must not match.
		isNot []error
		// eof requires err == io.EOF.
		eof bool
	}{
		{
			name: "AddRule duplicate type",
			run: func(t *testing.T) error {
				lx := letters("abc")
				return lx.AddRule(lexTypeWord, newASCIILettersRule())
			},
			is: []error{bytescan.ErrDuplicateRule},
		},
		{
			name: "AddRule empty type",
			run: func(t *testing.T) error {
				return letters("abc").AddRule("", newASCIILettersRule())
			},
			is: []error{bytescan.ErrEmptyRuleType},
		},
		{
			name: "AddRule nil rule",
			run: func(t *testing.T) error {
				return letters("abc").AddRule("NIL", nil)
			},
			is: []error{bytescan.ErrNilRule},
		},
		{
			name: "Next with no rules",
			run: func(t *testing.T) error {
				_, err := bytescan.New(strings.NewReader("abc")).Next()
				return err
			},
			is: []error{bytescan.ErrNoRules},
		},
		{
			name: "Context of a nil token",
			run: func(t *testing.T) error {
				_, err := letters("abc").Context(nil, 1, 1)
				return err
			},
			is: []error{bytescan.ErrNilToken},
		},
		{
			name: "Context on a cursor without retained text",
			run: func(t *testing.T) error {
				lx := bytescan.NewWithCursor(&plainCursor{inner: cursor.NewReader(strings.NewReader("abc"))})
				lx.MustAddRule(lexTypeWord, newASCIILettersRule())
				lex, err := lx.Next()
				require.NoError(t, err)
				_, err = lx.Context(lex, 0, 0)
				return err
			},
			is: []error{bytescan.ErrContextUnsupported},
		},
		{
			name: "end of input",
			run: func(t *testing.T) error {
				lx := letters("abc")
				_, err := lx.Next()
				require.NoError(t, err)
				_, err = lx.Next()
				require.True(t, err == io.EOF, "first probe: %v", err)
				_, err = lx.Next()
				return err
			},
			eof: true,
		},
		{
			name: "end of input after Release",
			run: func(t *testing.T) error {
				lx := letters("")
				_, err := lx.Next()
				require.True(t, err == io.EOF, "before Release: %v", err)
				lx.Release()
				_, err = lx.Next()
				return err
			},
			eof: true,
		},
		{
			name: "token over WithMaxTokenBytes",
			run: func(t *testing.T) error {
				lx := bytescan.NewWithOptions(strings.NewReader("abcdef"), bytescan.WithMaxTokenBytes(3))
				lx.MustAddRule(lexTypeWord, newASCIILettersRule())
				_, err := lx.Next()
				return err
			},
			is: []error{bytescan.ErrTokenTooLarge, cursor.ErrRetentionExceeded},
		},
		{
			name: "token over the cursor's retention budget",
			run: func(t *testing.T) error {
				cur := cursor.NewReader(strings.NewReader("abcdef"), cursor.WithMaxRetained(3))
				lx := bytescan.NewWithCursor(cur)
				lx.MustAddRule(lexTypeWord, newASCIILettersRule())
				_, err := lx.Next()
				return err
			},
			is:    []error{cursor.ErrRetentionExceeded},
			isNot: []error{bytescan.ErrTokenTooLarge},
		},
		{
			name: "injected cursor error",
			run: func(t *testing.T) error {
				cur := &transientCursor{Reader: cursor.NewReader(strings.NewReader("abc")), failAt: 1}
				lx := bytescan.NewWithCursor(cur)
				lx.MustAddRule(lexTypeWord, newASCIILettersRule())
				_, err := lx.Next()
				return err
			},
			is: []error{errTransient},
		},
		{
			name: "Context error from the cursor",
			run: func(t *testing.T) error {
				lx := letters("abc")
				lex, err := lx.Next()
				require.NoError(t, err)
				lx.Release()
				_, err = lx.Context(lex, 0, 0)
				return err
			},
			is: []error{cursor.ErrPositionOutOfBuffer},
		},
	}

	sentinels := []error{
		bytescan.ErrDuplicateRule,
		bytescan.ErrEmptyRuleType,
		bytescan.ErrNilRule,
		bytescan.ErrNoRules,
		bytescan.ErrNilToken,
		bytescan.ErrContextUnsupported,
		bytescan.ErrTokenTooLarge,
		bytescan.ErrInternal,
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			require.Error(t, err)

			if tc.eof {
				assert.True(t, err == io.EOF, "want io.EOF itself, got %v", err)
				for _, s := range sentinels {
					assert.NotErrorIs(t, err, s)
				}
				return
			}

			assert.NotErrorIs(t, err, io.EOF)
			for _, want := range tc.is {
				assert.ErrorIs(t, err, want)
			}
			for _, notWant := range tc.isNot {
				assert.NotErrorIs(t, err, notWant)
			}

			// A package-owned failure matches exactly one sentinel.
			matched := 0
			for _, s := range sentinels {
				if errors.Is(err, s) {
					matched++
				}
			}
			owned := false
			for _, want := range tc.is {
				for _, s := range sentinels {
					if want == s {
						owned = true
					}
				}
			}
			if owned {
				assert.Equal(t, 1, matched, "a package-owned failure matches one sentinel")
			} else {
				assert.Zero(t, matched, "a foreign error matches no root sentinel")
			}
		})
	}
}

// TestScannerNextAfterRelease checks that Release cannot fail the scanner: the
// next call to Next lexes the rest of the input.
func TestScannerNextAfterRelease(t *testing.T) {
	lx := bytescan.New(strings.NewReader("ab cd"))
	lx.MustAddRule(lexTypeWord, newASCIILettersRule())
	lx.MustAddRule(lexTypeSpace, newSpaceRule())

	lex, err := lx.Next()
	require.NoError(t, err)
	assert.Equal(t, "ab", lex.Text())

	lx.Release()

	var texts []string
	for {
		lex, err := lx.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		texts = append(texts, lex.Text())
	}
	assert.Equal(t, []string{" ", "cd"}, texts)
}
