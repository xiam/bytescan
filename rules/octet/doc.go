// Package octet provides rules that match byte values, for scanners of binary
// formats and binary protocols.
//
// The rules match the bytes of an input read through the byte adapter,
// bytesource.NewCursor or bytesource.NewReader. Each constructor states what
// it matches as a sequence of byte values, in the regular-expression
// notation of a byte string: [\x80-\xff] is one byte from 0x80 to 0xFF, and
// every byte value from 0x00 to 0xFF is matched the same way. A Go string
// such as "\xff" is a byte string here, never UTF-8 text.
//
// Every constructor returns a bytescan.Rule built from package core, so an
// octet rule registers on a scanner directly and composes with the core
// combinators and with core rules that match bytes through bytesource.Text
// and bytesource.AnyByte. The package contains no hand-written state
// function; each rule inherits the leftmost-longest composition and the
// end-of-input behaviour of package core.
//
// # Classes
//
// Any, Set and Range match one byte: any byte, one of a set of bytes, or one
// byte in a range of values.
//
// # Fixed fields
//
// Literal matches a sequence of bytes, such as a signature or a magic
// number. Fixed matches exactly n bytes of any value, and Record matches a
// signature followed by exactly n bytes: the fixed part of a header. A field
// cut by the end of the input does not match. A rule cannot carry a length
// that is stored in the data; a driver reads such a field with
// bytesource.Read or bytesource.Fields.
//
// # Variable-length integers
//
// Uvarint matches an unsigned LEB128-style integer field of at most maxBytes
// bytes: bytes with the high bit set, then one byte without it. It matches
// the field and does not decode its value.
//
// # Limits
//
// Fixed and Record hold one copy of a one-byte rule per counted byte, so
// building them costs memory in proportion to n, and n is at most MaxFixed.
// A longer field is read by a driver with bytesource.Read or
// bytesource.Fields, which also bounds the size of each token.
package octet
