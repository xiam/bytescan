package tests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

// The tests in this file check the rules of rules/octet against Go's regexp
// package, used only as a test oracle for the byte expression that each doc
// comment documents. The oracle reads the input as bytesource.Text does, so
// the expression [\x80-\xff] of a doc comment is the regexp [\x{80}-\x{ff}]
// over that text.

// octetTokenStream lexes in with rule alone through the byte adapter and
// returns every token, as its type and bytes, until the end of input.
func octetTokenStream(t *testing.T, rule bytescan.Rule, in []byte) []string {
	t.Helper()
	lx := bytescan.NewWithCursor(bytesource.NewCursor(bytes.NewReader(in)))
	lx.MustAddRule("R", rule)
	var stream []string
	for i := 0; ; i++ {
		require.LessOrEqual(t, i, len(in), "stream of % x does not end", in)
		lex, err := lx.Next()
		if errors.Is(err, io.EOF) {
			return stream
		}
		require.NoError(t, err, "input % x", in)
		stream = append(stream, fmt.Sprintf("%s:% x", lex.Type(), bytesource.Bytes(lex.Text())))
	}
}

// octetRegexpStream is the independent oracle of octetTokenStream: at each
// position it takes the longest match of re over the text of the bytes, or
// else one UNKNOWN byte.
func octetRegexpStream(re *regexp.Regexp, in []byte) []string {
	text := []rune(bytesource.Text(string(in)))
	var stream []string
	for pos := 0; pos < len(text); {
		if m := re.FindString(string(text[pos:])); m != "" {
			b := bytesource.Bytes(m)
			stream = append(stream, fmt.Sprintf("R:% x", b))
			pos += len(b)
			continue
		}
		stream = append(stream, fmt.Sprintf("UNKNOWN:%02x", byte(text[pos])))
		pos++
	}
	return stream
}

// octetInputs returns every byte string of one to three bytes over alphabet,
// and the strings of extra.
func octetInputs(alphabet []byte, extra ...[]byte) [][]byte {
	var inputs [][]byte
	var grow func(prefix []byte)
	grow = func(prefix []byte) {
		if len(prefix) > 0 {
			inputs = append(inputs, append([]byte{}, prefix...))
		}
		if len(prefix) == 3 {
			return
		}
		for _, b := range alphabet {
			grow(append(prefix, b))
		}
	}
	grow(nil)
	return append(inputs, extra...)
}

// TestOctetMatchesRegexpOracle: each rules/octet rule gives the same token
// stream as the byte expression its doc comment documents, on every string of
// up to three bytes over an alphabet that crosses 0x7F/0x80 and holds the
// bytes of each rule, and on longer inputs that reach each bound.
func TestOctetMatchesRegexpOracle(t *testing.T) {
	alphabet := []byte{0x00, 0x03, 0x04, 0x4B, 0x50, 0x7F, 0x80, 0xCA, 0xFE, 0xFF}
	extra := [][]byte{
		[]byte("PK\x03\x04\x00\x80\xff\x7fPK\x03\x04\x00"),
		{0x80, 0x80, 0x80, 0x7F, 0x80, 0x80, 0x80, 0x80, 0x7F, 0xFF, 0x00},
		bytes.Repeat([]byte{0xCA, 0xFE, 0x00}, 5),
	}

	rows := []struct {
		expr string
		rule bytescan.Rule
	}{
		{`[\x00-\xff]`, octet.Any()},
		{`[\x00\x7f\x80\xff]`, octet.Set(0x00, 0x7F, 0x80, 0xFF)},
		{`[\x7f-\x80]`, octet.Range(0x7F, 0x80)},
		{`[\xca-\xff]`, octet.Range(0xCA, 0xFF)},
		{`\xca\xfe`, octet.Literal([]byte{0xCA, 0xFE})},
		{`PK\x03\x04`, octet.Literal([]byte("PK\x03\x04"))},
		{`[\x00-\xff]{1}`, octet.Fixed(1)},
		{`[\x00-\xff]{2}`, octet.Fixed(2)},
		{`[\x00-\xff]{5}`, octet.Fixed(5)},
		{`PK\x03\x04[\x00-\xff]{2}`, octet.Record([]byte("PK\x03\x04"), 2)},
		{`\xca\xfe[\x00-\xff]{1}`, octet.Record([]byte{0xCA, 0xFE}, 1)},
		{`\xff`, octet.Record([]byte{0xFF}, 0)},
		{`[\x80-\xff]{0,0}[\x00-\x7f]`, octet.Uvarint(1)},
		{`[\x80-\xff]{0,1}[\x00-\x7f]`, octet.Uvarint(2)},
		{`[\x80-\xff]{0,3}[\x00-\x7f]`, octet.Uvarint(4)},
		{`\xca(?:[\x80-\xff]{0,2}[\x00-\x7f])`, core.Sequence(octet.Literal([]byte{0xCA}), octet.Uvarint(3))},
	}

	for _, row := range rows {
		t.Run(row.expr, func(t *testing.T) {
			re := regexp.MustCompile(`\A(?:` + row.expr + `)`)
			re.Longest()
			for _, in := range octetInputs(alphabet, extra...) {
				require.Equal(t, octetRegexpStream(re, in), octetTokenStream(t, row.rule, in), "input % x", in)
			}
		})
	}
}

// TestOctetClassesMatchRegexpOracle: Set, Range and Literal agree with the
// oracle for every byte value, alone and after each other byte value.
func TestOctetClassesMatchRegexpOracle(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for v := 0; v < 256; v++ {
		b := byte(v)
		expr := fmt.Sprintf(`\x{%x}`, b)
		re := regexp.MustCompile(`\A(?:` + expr + `)`)
		re.Longest()
		assert.Equal(t, octetRegexpStream(re, all), octetTokenStream(t, octet.Set(b), all), "Set(%02x)", b)
		assert.Equal(t, octetRegexpStream(re, all), octetTokenStream(t, octet.Literal([]byte{b}), all), "Literal(%02x)", b)

		re = regexp.MustCompile(fmt.Sprintf(`\A(?:[\x{%x}-\x{ff}])`, b))
		assert.Equal(t, octetRegexpStream(re, all), octetTokenStream(t, octet.Range(b, 0xFF), all), "Range(%02x, ff)", b)
		re = regexp.MustCompile(fmt.Sprintf(`\A(?:[\x00-\x{%x}])`, b))
		assert.Equal(t, octetRegexpStream(re, all), octetTokenStream(t, octet.Range(0x00, b), all), "Range(00, %02x)", b)
	}
}
