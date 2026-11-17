package bytescan

const (
	FlagNone uint = 0         // FlagNone indicates no special positional flags.
	FlagEOF  uint = 1 << iota // FlagEOF indicates the symbol is the last in the input stream.
	FlagBOF                   // FlagBOF indicates the symbol is at the beginning of the input stream.
	FlagEOL                   // FlagEOL indicates the symbol is at the end of a line.
	FlagBOL                   // FlagBOL indicates the symbol is at the beginning of a line.
)

// flagMask holds every flag the package defines. A Symbol keeps only these
// bits: NewSymbol drops every other bit of its flags argument, so two symbols
// built with the same rune and the same defined flags are equal.
const flagMask = FlagEOF | FlagBOF | FlagEOL | FlagBOL

// The packed flags field must hold every defined flag. The conversion fails to
// compile if flagMask does not fit in a uint8.
const _ = uint8(flagMask)

// Symbol represents one rune of the input with contextual information about
// its position in the stream. This allows lexical rules to make decisions
// based not only on the rune itself but also on its positional context. Over
// the byte adapter of package bytesource, each rune is one byte, U+0000 to
// U+00FF.
//
// A Symbol is 8 bytes: the stack of a long open token holds one per input
// rune, so its size sets most of the memory the token holds.
type Symbol struct {
	r rune

	// flags holds the defined flags only (flagMask).
	flags uint8

	// size is the number of bytes the rune occupies in the source text: 1 to 4
	// for a rune read by the scanner (utf8.UTFMax is 4, and an invalid byte is
	// read as one rune of size 1), and zero for a symbol built with NewSymbol.
	// A uint8 holds 0 to 255, so every size fits.
	size uint8
}

// NewSymbol creates a new Symbol with the specified rune and position flags.
// Multiple flags can be combined using a bitwise OR. Only the flags the package
// defines (FlagEOF, FlagBOF, FlagEOL and FlagBOL) are kept; any other bit of
// flags is dropped.
func NewSymbol(r rune, flags uint) Symbol {
	return Symbol{
		r:     r,
		flags: uint8(flags & flagMask),
	}
}

// Size returns the number of bytes the symbol occupies in the source text. A
// Symbol built with NewSymbol reports zero.
func (s Symbol) Size() int {
	return int(s.size)
}

// IsEOF returns true if this symbol is at the end of the input stream.
func (s Symbol) IsEOF() bool {
	return (uint(s.flags) & FlagEOF) != 0
}

// IsBOF returns true if this symbol is at the beginning of the input stream.
func (s Symbol) IsBOF() bool {
	return (uint(s.flags) & FlagBOF) != 0
}

// IsEOL returns true if this symbol is at the end of a line.
func (s Symbol) IsEOL() bool {
	return (uint(s.flags) & FlagEOL) != 0
}

// IsBOL returns true if this symbol is at the beginning of a line.
func (s Symbol) IsBOL() bool {
	return (uint(s.flags) & FlagBOL) != 0
}

// Rune returns the underlying rune value of this symbol.
func (s Symbol) Rune() rune {
	return s.r
}

// String returns a string representation of the Symbol's rune.
func (s Symbol) String() string {
	return string(s.r)
}
