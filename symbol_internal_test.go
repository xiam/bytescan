package bytescan

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSymbolSize: a Symbol is at most 8 bytes. The stack of an open token holds
// one Symbol per input rune, so this size sets most of the memory a long token
// holds.
func TestSymbolSize(t *testing.T) {
	assert.LessOrEqual(t, unsafe.Sizeof(Symbol{}), uintptr(8))
}

// TestNewSymbolDropsUndefinedFlags: NewSymbol keeps only the defined flags. A
// bit above them, or the unused bit 0, is dropped, so the symbol equals one
// built without it and every accessor reports the same values.
func TestNewSymbolDropsUndefinedFlags(t *testing.T) {
	defined := []uint{FlagNone, FlagEOF, FlagBOF, FlagEOL, FlagBOL, FlagEOF | FlagBOF | FlagEOL | FlagBOL}
	undefined := []uint{1, 1 << 5, 1 << 7, 1 << 8, 1 << 31, ^flagMask}

	for _, def := range defined {
		want := NewSymbol('x', def)
		for _, extra := range undefined {
			got := NewSymbol('x', def|extra)
			assert.Equal(t, want, got, "flags %#x | %#x", def, extra)
			assert.Equal(t, want.IsEOF(), got.IsEOF())
			assert.Equal(t, want.IsBOF(), got.IsBOF())
			assert.Equal(t, want.IsEOL(), got.IsEOL())
			assert.Equal(t, want.IsBOL(), got.IsBOL())
		}
		assert.Equal(t, def&FlagEOF != 0, want.IsEOF())
		assert.Equal(t, def&FlagBOF != 0, want.IsBOF())
		assert.Equal(t, def&FlagEOL != 0, want.IsEOL())
		assert.Equal(t, def&FlagBOL != 0, want.IsBOL())
		assert.Equal(t, 'x', want.Rune())
		assert.Equal(t, 0, want.Size())
	}
}

// TestReadSymbolSizes: the packed size field holds the byte size of every rune
// width the scanner reads, from 1 to 4 bytes, and of an invalid byte.
func TestReadSymbolSizes(t *testing.T) {
	lx := New(strings.NewReader("aé€\U0001F600\xff"))
	for _, want := range []int{1, 2, 3, 4, 1} {
		sym, isEOF, err := lx.readSymbol()
		require.NoError(t, err)
		require.False(t, isEOF)
		assert.Equal(t, want, sym.Size(), "rune %q", sym.Rune())
	}
	sym, isEOF, err := lx.readSymbol()
	require.NoError(t, err)
	assert.True(t, isEOF)
	assert.True(t, sym.IsEOF())
	assert.Equal(t, 0, sym.Size())
}
