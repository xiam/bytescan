package octet_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

const typeR = bytescan.TokenType("R")

// allBytes returns the 256 byte values in order.
func allBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// lexBytes lexes in with rule alone through the byte adapter and returns each
// token as its type and bytes, until the end of input.
func lexBytes(t *testing.T, rule bytescan.Rule, in []byte) []string {
	t.Helper()
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(in)))
	lx.MustAddRule(typeR, rule)
	var out []string
	for i := 0; ; i++ {
		require.LessOrEqual(t, i, len(in), "stream of % x does not end", in)
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		out = append(out, fmt.Sprintf("%s:% x", lex.Type(), bytesource.Bytes(lex.Text())))
	}
}

// classStream is the expected stream of a one-byte class over in: R for each
// byte in the class, UNKNOWN for each other byte.
func classStream(in []byte, in2 func(byte) bool) []string {
	out := make([]string, 0, len(in))
	for _, b := range in {
		typ := bytescan.TokenTypeUnknown
		if in2(b) {
			typ = typeR
		}
		out = append(out, fmt.Sprintf("%s:%02x", typ, b))
	}
	return out
}

// TestAnyMatchesEveryByte: Any matches each of the 256 byte values as one
// token.
func TestAnyMatchesEveryByte(t *testing.T) {
	in := allBytes()
	assert.Equal(t, classStream(in, func(byte) bool { return true }), lexBytes(t, octet.Any(), in))
}

// TestSetIsExhaustive: Set(b) matches the byte b and no other byte, for every
// byte value b, and a set of several bytes matches exactly its bytes.
func TestSetIsExhaustive(t *testing.T) {
	in := allBytes()
	for v := 0; v < 256; v++ {
		b := byte(v)
		require.Equal(t, classStream(in, func(c byte) bool { return c == b }), lexBytes(t, octet.Set(b), in), "Set(0x%02X)", b)
	}

	set := []byte{0x00, 0x0A, 0x7F, 0x80, 0xC0, 0xDB, 0xFF}
	member := func(c byte) bool { return bytes.IndexByte(set, c) >= 0 }
	assert.Equal(t, classStream(in, member), lexBytes(t, octet.Set(set...), in))
}

// TestRangeIsExhaustive: Range(lo, hi) matches exactly the bytes from lo to
// hi over all 256 byte values, for every lo and for hi at lo, lo+1 and the
// boundaries 0x7F, 0x80 and 0xFF.
func TestRangeIsExhaustive(t *testing.T) {
	in := allBytes()
	for v := 0; v < 256; v++ {
		lo := byte(v)
		his := map[byte]bool{lo: true, 0x7F: true, 0x80: true, 0xFF: true}
		if lo < 0xFF {
			his[lo+1] = true
		}
		for hi := range his {
			if hi < lo {
				continue
			}
			want := classStream(in, func(c byte) bool { return c >= lo && c <= hi })
			require.Equal(t, want, lexBytes(t, octet.Range(lo, hi), in), "Range(0x%02X, 0x%02X)", lo, hi)
		}
	}
}

// TestLiteralIsExhaustive: Literal matches every byte value, alone and
// between two other bytes, and does not match the byte before or after it.
func TestLiteralIsExhaustive(t *testing.T) {
	in := allBytes()
	for v := 0; v < 256; v++ {
		b := byte(v)
		require.Equal(t, classStream(in, func(c byte) bool { return c == b }), lexBytes(t, octet.Literal([]byte{b}), in), "Literal(%02x)", b)

		lit := []byte{0x47, b, 0xFF}
		got := lexBytes(t, octet.Literal(lit), []byte{0x47, b, 0xFF, 0x47, b ^ 0x01, 0xFF})
		require.Equal(t, []string{
			fmt.Sprintf("R:47 %02x ff", b),
			"UNKNOWN:47", fmt.Sprintf("UNKNOWN:%02x", b^0x01), "UNKNOWN:ff",
		}, got, "Literal(% x)", lit)
	}
}

// TestLiteralMatchesCoreLiteralOfText: Literal(b) is the rule that
// core.Literal(bytesource.Text(b)) is, the form it replaces in the examples.
func TestLiteralMatchesCoreLiteralOfText(t *testing.T) {
	sig := []byte("\xff\xd8\xff\xe1PK\x03\x04\x00\x80")
	in := append(append([]byte{}, sig...), sig[:5]...)
	assert.Equal(t,
		lexBytes(t, core.Literal(bytesource.Text(string(sig))), in),
		lexBytes(t, octet.Literal(sig), in))
}

// TestLiteralAndRecordCopyTheirBytes: a change to the slice after the call
// does not change the rule.
func TestLiteralAndRecordCopyTheirBytes(t *testing.T) {
	sig := []byte{0xCA, 0xFE}
	lit := octet.Literal(sig)
	rec := octet.Record(sig, 1)
	sig[0] = 0x00
	assert.Equal(t, []string{"R:ca fe"}, lexBytes(t, lit, []byte{0xCA, 0xFE}))
	assert.Equal(t, []string{"R:ca fe 00"}, lexBytes(t, rec, []byte{0xCA, 0xFE, 0x00}))
}

// TestFixedMatchesExactlyN: Fixed(n) splits a stream into pieces of exactly n
// bytes of any value, and leaves a shorter tail cut by the end of the input
// as UNKNOWN bytes.
func TestFixedMatchesExactlyN(t *testing.T) {
	in := allBytes()
	for _, n := range []int{1, 2, 3, 7, 100, 255, 256} {
		var want []string
		full := len(in) / n * n
		for i := 0; i < full; i += n {
			want = append(want, fmt.Sprintf("R:% x", in[i:i+n]))
		}
		for _, b := range in[full:] {
			want = append(want, fmt.Sprintf("UNKNOWN:%02x", b))
		}
		assert.Equal(t, want, lexBytes(t, octet.Fixed(n), in), "Fixed(%d)", n)
	}

	big := bytes.Repeat([]byte{0xFF, 0x00, 0x80}, octet.MaxFixed)
	got := lexBytes(t, octet.Fixed(octet.MaxFixed), big[:2*octet.MaxFixed+1])
	require.Len(t, got, 3)
	assert.Equal(t, fmt.Sprintf("R:% x", big[:octet.MaxFixed]), got[0])
	assert.Equal(t, fmt.Sprintf("R:% x", big[octet.MaxFixed:2*octet.MaxFixed]), got[1])
	assert.Equal(t, fmt.Sprintf("UNKNOWN:%02x", big[2*octet.MaxFixed]), got[2])
}

// TestRecordMatchesSignatureAndN: Record(sig, n) matches the signature and
// exactly n bytes after it, and nothing when the input ends first.
func TestRecordMatchesSignatureAndN(t *testing.T) {
	sig := []byte("PK\x03\x04")
	rec := octet.Record(sig, 3)

	assert.Equal(t, []string{"R:50 4b 03 04 ff 00 80", "UNKNOWN:ff"},
		lexBytes(t, rec, []byte("PK\x03\x04\xff\x00\x80\xff")))
	assert.Equal(t, []string{"UNKNOWN:50", "UNKNOWN:4b", "UNKNOWN:03", "UNKNOWN:04", "UNKNOWN:ff", "UNKNOWN:00"},
		lexBytes(t, rec, []byte("PK\x03\x04\xff\x00")))
	assert.Equal(t, []string{"UNKNOWN:50", "UNKNOWN:4b", "UNKNOWN:05", "UNKNOWN:04"},
		lexBytes(t, rec, []byte("PK\x05\x04")))
	assert.Equal(t, []string{"R:50 4b 03 04"}, lexBytes(t, octet.Record(sig, 0), sig))
}

// TestUvarintBounds: Uvarint(maxBytes) matches high-bit bytes then one
// byte without the high bit, at most maxBytes bytes in all, for every value
// of the first byte.
func TestUvarintBounds(t *testing.T) {
	for v := 0; v < 256; v++ {
		b := byte(v)
		got := lexBytes(t, octet.Uvarint(1), []byte{b})
		if b < 0x80 {
			require.Equal(t, []string{fmt.Sprintf("R:%02x", b)}, got)
			continue
		}
		require.Equal(t, []string{fmt.Sprintf("UNKNOWN:%02x", b)}, got)

		got = lexBytes(t, octet.Uvarint(2), []byte{b, 0x7F})
		require.Equal(t, []string{fmt.Sprintf("R:%02x 7f", b)}, got)
	}

	u4 := octet.Uvarint(4)
	assert.Equal(t, []string{"R:ff ff ff 7f"}, lexBytes(t, u4, []byte{0xFF, 0xFF, 0xFF, 0x7F}))
	assert.Equal(t, []string{"UNKNOWN:ff", "R:ff ff ff 7f"}, lexBytes(t, u4, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F}))
	assert.Equal(t, []string{"UNKNOWN:80", "UNKNOWN:80"}, lexBytes(t, u4, []byte{0x80, 0x80}))
	assert.Equal(t, []string{"R:00", "R:80 01"}, lexBytes(t, u4, []byte{0x00, 0x80, 0x01}))
}

// TestConstructorPanics: a constructor rejects an invalid argument when it
// is called.
func TestConstructorPanics(t *testing.T) {
	cases := map[string]func(){
		"Set()":              func() { octet.Set() },
		"Range(0x10, 0x0F)":  func() { octet.Range(0x10, 0x0F) },
		"Literal(nil)":       func() { octet.Literal(nil) },
		"Literal(empty)":     func() { octet.Literal([]byte{}) },
		"Fixed(0)":           func() { octet.Fixed(0) },
		"Fixed(-1)":          func() { octet.Fixed(-1) },
		"Fixed(MaxFixed+1)":  func() { octet.Fixed(octet.MaxFixed + 1) },
		"Record(nil, 1)":     func() { octet.Record(nil, 1) },
		"Record(sig, -1)":    func() { octet.Record([]byte{1}, -1) },
		"Record(sig, Max+1)": func() { octet.Record([]byte{1}, octet.MaxFixed+1) },
		"Uvarint(0)":         func() { octet.Uvarint(0) },
		"Uvarint(-1)":        func() { octet.Uvarint(-1) },
	}
	for name, construct := range cases {
		t.Run(name, func(t *testing.T) { assert.Panics(t, construct) })
	}
	assert.NotPanics(t, func() { octet.Range(0x80, 0x80) })
	assert.NotPanics(t, func() { octet.Fixed(octet.MaxFixed) })
	assert.NotPanics(t, func() { octet.Record([]byte{1}, octet.MaxFixed) })
}

// ruleSink keeps a built rule alive so that the compiler cannot drop the
// construction that a build-cost test measures.
var ruleSink bytescan.Rule

// buildBytes returns the heap bytes of one call of build, as an average over
// runs calls.
func buildBytes(build func() bytescan.Rule, runs int) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < runs; i++ {
		ruleSink = build()
	}
	runtime.ReadMemStats(&after)
	ruleSink = nil
	return (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
}

// fixedBuildCeiling is the documented bound on the memory that building
// Fixed(MaxFixed) or Record(sig, MaxFixed) allocates.
const fixedBuildCeiling = 1 << 20

// TestFixedBuildCostIsBounded: building Fixed(n) costs memory that grows with
// n, and n stops at MaxFixed, so the cost stops at fixedBuildCeiling.
// Uvarint costs the same for every maxBytes.
func TestFixedBuildCostIsBounded(t *testing.T) {
	small := buildBytes(func() bytescan.Rule { return octet.Fixed(2) }, 20)
	largest := buildBytes(func() bytescan.Rule { return octet.Fixed(octet.MaxFixed) }, 5)
	record := buildBytes(func() bytescan.Rule { return octet.Record([]byte("PK\x03\x04"), octet.MaxFixed) }, 5)
	t.Logf("Fixed(2): %d bytes; Fixed(%d): %d bytes; Record(sig, %d): %d bytes",
		small, octet.MaxFixed, largest, octet.MaxFixed, record)
	assert.LessOrEqual(t, largest, uint64(fixedBuildCeiling))
	assert.LessOrEqual(t, record, uint64(fixedBuildCeiling))

	u1 := buildBytes(func() bytescan.Rule { return octet.Uvarint(2) }, 50)
	for _, n := range []int{10, 1000, 1 << 20} {
		assert.Equal(t, u1, buildBytes(func() bytescan.Rule { return octet.Uvarint(n) }, 50), "Uvarint(%d)", n)
	}
}

// TestRulesCompose: octet rules compose with core combinators and with core
// rules over bytesource.Text.
func TestRulesCompose(t *testing.T) {
	rule := core.Sequence(
		octet.Literal([]byte{0xFF}),
		core.Choice(octet.Range(0xE0, 0xEF), octet.Set(0xD8, 0xD9)),
		core.Optional(octet.Fixed(2)),
	)
	assert.Equal(t, []string{"R:ff e1 00 10", "UNKNOWN:ff", "UNKNOWN:c0", "R:ff d8"},
		lexBytes(t, rule, []byte{0xFF, 0xE1, 0x00, 0x10, 0xFF, 0xC0, 0xFF, 0xD8}))
}
