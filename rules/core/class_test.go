package core_test

import (
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"

	"github.com/xiam/bytescan/rules/core"
)

// TestClassFromFunc: a func(rune) bool converts to a Class, and the
// conversion composes like any other class.
func TestClassFromFunc(t *testing.T) {
	isVowel := func(r rune) bool {
		switch r {
		case 'a', 'e', 'i', 'o', 'u':
			return true
		}
		return false
	}
	vowel := core.Class(isVowel)
	assert.True(t, vowel('e'))
	assert.False(t, vowel('b'))
	assert.True(t, vowel.Not()('b'))
	assert.True(t, core.Class(unicode.IsDigit).Or(vowel)('٣'))
}

// TestClassRuneSetIsImmutable: RuneSet copies its argument's runes, so a class
// built from a string does not depend on anything that changes later.
func TestClassRuneSetIsImmutable(t *testing.T) {
	b := []byte("ab")
	c := core.RuneSet(string(b))
	b[0] = 'x'
	assert.True(t, c('a'))
	assert.False(t, c('x'))
}
