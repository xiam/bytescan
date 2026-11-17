package octet

import (
	"fmt"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/core"
)

// MaxFixed is the largest n of Fixed and Record. Building one of them holds
// one copy of a one-byte rule per counted byte, so its memory grows with n up
// to this bound: building Fixed(MaxFixed) allocates about 150 KiB, and
// Record(sig, MaxFixed) about twice that. A longer field is read by a driver
// (see the package documentation).
const MaxFixed = 1000

// byteRunes returns the runes that the byte adapter reads for the bytes b,
// in order: the text of bytesource.Text for b.
func byteRunes(b []byte) []rune {
	return []rune(bytesource.Text(string(b)))
}

// class returns the class of the bytes b.
func class(b []byte) core.Class {
	return core.RuneSet(string(byteRunes(b)))
}

// Any returns a rule for one byte of any value: [\x00-\xff]. It accepts that
// byte and stops. At end of input it matches nothing.
func Any() bytescan.Rule {
	return core.Rune(bytesource.AnyByte)
}

// Set returns a rule for one byte whose value is one of bs: [bs...]. It
// accepts that byte and stops. At end of input it matches nothing. It panics
// if bs is empty.
func Set(bs ...byte) bytescan.Rule {
	if len(bs) == 0 {
		panic("octet.Set: no byte")
	}
	return core.Rune(class(bs))
}

// Range returns a rule for one byte whose value is from lo to hi, both
// included: [lo-hi]. It accepts that byte and stops. At end of input it
// matches nothing. It panics if lo is above hi.
func Range(lo, hi byte) bytescan.Rule {
	if lo > hi {
		panic(fmt.Sprintf("octet.Range: lo 0x%02X is above hi 0x%02X", lo, hi))
	}
	r := byteRunes([]byte{lo, hi})
	return core.Rune(core.RuneRange(r[0], r[1]))
}

// Literal returns a rule for the bytes b, in order: b as a byte string, for
// every byte value. Literal([]byte{0xFF, 0xD8}) matches the two bytes FF D8.
// It accepts at the last byte of b and stops. At end of input before that
// byte it matches nothing. It panics if b is empty.
//
// Literal does not keep b: a change to b after the call does not change the
// rule.
func Literal(b []byte) bytescan.Rule {
	if len(b) == 0 {
		panic("octet.Literal: b must not be empty")
	}
	return core.Literal(bytesource.Text(string(b)))
}

// Fixed returns a rule for exactly n bytes of any value: [\x00-\xff]{n}. It
// accepts at the n-th byte and stops. At end of input before the n-th byte it
// matches nothing, so a field cut by the end of the input is not a match. It
// panics if n is below 1 or above MaxFixed.
func Fixed(n int) bytescan.Rule {
	mustCount("Fixed", n, 1)
	return fixed(n)
}

// fixed returns the rule of Fixed(n) for a valid n.
func fixed(n int) bytescan.Rule {
	if n == 1 {
		return Any()
	}
	return core.Repeat(Any(), n, n)
}

// Record returns a rule for the bytes sig followed by exactly n bytes of any
// value: sig[\x00-\xff]{n}, with sig as a byte string. It is the fixed part
// of a record that starts with a signature, such as a ZIP local file header.
// It accepts at the n-th byte after sig, or at the last byte of sig when n is
// 0, and stops. At end of input before then it matches nothing. It panics if
// sig is empty, or if n is negative or above MaxFixed.
//
// Record does not keep sig: a change to sig after the call does not change
// the rule.
func Record(sig []byte, n int) bytescan.Rule {
	if len(sig) == 0 {
		panic("octet.Record: sig must not be empty")
	}
	mustCount("Record", n, 0)
	if n == 0 {
		return Literal(sig)
	}
	return core.Sequence(Literal(sig), fixed(n))
}

// Uvarint returns a rule for an unsigned variable-length integer field of 1
// to maxBytes bytes, in the LEB128 layout: every byte but the last has its
// high bit (0x80) set, and the last byte has it clear:
//
//	[\x80-\xff]{0,maxBytes-1}[\x00-\x7f]
//
// The MQTT remaining length is Uvarint(4), and a Protocol Buffers varint of
// 64 bits is Uvarint(10). The rule matches the field only; it does not decode
// the value or check that the value fits a size. It accepts at the first byte
// without the high bit and stops. When maxBytes bytes in a row have the high
// bit set, or the input ends before a byte without it, it matches nothing.
// It panics if maxBytes is below 1.
//
// Its construction cost is the same for every maxBytes.
func Uvarint(maxBytes int) bytescan.Rule {
	if maxBytes < 1 {
		panic(fmt.Sprintf("octet.Uvarint: maxBytes is %d, want at least 1", maxBytes))
	}
	r := byteRunes([]byte{0x00, 0x7F, 0x80, 0xFF})
	last := core.Rune(core.RuneRange(r[0], r[1]))
	if maxBytes == 1 {
		return last
	}
	more := core.RuneRange(r[2], r[3])
	return core.Sequence(core.Optional(core.RuneRunMax(more, maxBytes-1)), last)
}

// mustCount panics unless n is from least to MaxFixed.
func mustCount(name string, n, least int) {
	if n < least || n > MaxFixed {
		panic(fmt.Sprintf("octet.%s: n is %d, want %d to %d", name, n, least, MaxFixed))
	}
}
