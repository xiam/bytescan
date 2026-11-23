package bench

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"strings"

	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/streamtest"
)

// cases returns the five reference cases of docs/specs/measurement.md, "The
// reference cases", in the order of the contract.
func cases() []Case {
	return []Case{
		{
			name:      "json",
			ruleSet:   "the JSON scanner of bytescan-examples (`json/scanner.go`)",
			source:    "`golang_source.json` (Go 1.25.4), read twice in a row",
			rules:     jsonRules,
			newCursor: textCursor,
			input:     jsonInput,
		},
		{
			name:      "code",
			ruleSet:   "the seven hand-written rules of `BenchmarkComplexTokens` (`tests/bench_test.go`)",
			source:    "SQL-like statements generated from seed 1",
			rules:     codeRules,
			newCursor: textCursor,
			input:     codeInput,
		},
		{
			name:      "slip",
			ruleSet:   "the SLIP scanner of bytescan-examples (`streams/slip/scanner.go`)",
			source:    "SLIP frames generated from seed 55",
			rules:     slipRules,
			newCursor: bytesource.NewCursor,
			input:     slipInput,
		},
		{
			name:      "stream",
			ruleSet:   "the line scanner of the `streamtest` self-test (`addLineRules`)",
			source:    "`lineRecord` lines generated from seed 1",
			rules:     lineRules,
			newCursor: textCursor,
			input:     streamInput,
		},
		{
			name:      "large",
			ruleSet:   "the identifier rule of `BenchmarkLargeToken` and the whitespace rule (`tests/scanner_rules_test.go`)",
			source:    "four tokens of 1 MiB of `a`, separated by single spaces",
			rules:     largeRules,
			newCursor: textCursor,
			input:     largeInput,
		},
	}
}

func textCursor(r io.Reader) *cursor.Reader { return cursor.NewReader(r) }

// json: golang_source.json from src/encoding/json/internal/jsontest/testdata/
// of Go 1.25.4, committed gzip-compressed with the Go licence notice
// (testdata/golang_source.LICENSE).

//go:embed testdata/golang_source.json.gz
var golangSourceGz []byte

const (
	golangSourceSize   = 1940472
	golangSourceSHA256 = "23e8e3541eac3570958d6d430fc82867874be78a435580279b20f1efe5a6169f"
)

// jsonInput returns golang_source.json twice in a row, after it checks the
// size and the SHA-256 of the file.
func jsonInput() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(golangSourceGz))
	if err != nil {
		return nil, fmt.Errorf("bench: golang_source.json.gz: %w", err)
	}
	doc, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("bench: golang_source.json.gz: %w", err)
	}
	sum := sha256.Sum256(doc)
	if len(doc) != golangSourceSize || hex.EncodeToString(sum[:]) != golangSourceSHA256 {
		return nil, fmt.Errorf("bench: golang_source.json has %d bytes and SHA-256 %x, want %d bytes and %s",
			len(doc), sum, golangSourceSize, golangSourceSHA256)
	}
	in := make([]byte, 0, 2*len(doc))
	return append(append(in, doc...), doc...), nil
}

// code: SQL-like statements of the token classes of the code rule set, with
// varied identifiers, numbers, strings and comments.

// codeRecord appends one statement.
func codeRecord(rng *rand.Rand, _ int, dst []byte) []byte {
	const idStart = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
	const idRest = idStart + "0123456789"
	id := func() string {
		return randomWord(rng, idStart, 1, 1) + randomWord(rng, idRest, 0, 11)
	}
	b := bytes.NewBuffer(dst)
	b.WriteString("SELECT")
	for j, n := 0, 1+rng.Intn(4); j < n; j++ {
		b.WriteString(" " + id())
	}
	fmt.Fprintf(b, " FROM %s WHERE %s = ", id(), id())
	switch rng.Intn(3) {
	case 0:
		fmt.Fprintf(b, "%d.%d", rng.Intn(100000), rng.Intn(1000))
	case 1:
		fmt.Fprintf(b, ".%d", rng.Intn(1000))
	default:
		fmt.Fprintf(b, "'%s'", randomWord(rng, "abcdefghijklmnopqrstuvwxyz \n", 0, 20))
	}
	if rng.Intn(3) == 0 {
		fmt.Fprintf(b, " + (%d.5 * %s)", rng.Intn(100), id())
	}
	if rng.Intn(4) == 0 {
		fmt.Fprintf(b, " /* %s */", randomWord(rng, "abcdefghijklmnopqrstuvwxyz *\n", 0, 40))
	}
	b.WriteString(";\n")
	return b.Bytes()
}

func randomWord(rng *rand.Rand, alphabet string, minLen, maxLen int) string {
	n := minLen + rng.Intn(maxLen-minLen+1)
	w := make([]byte, n)
	for i := range w {
		w[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(w)
}

func codeInput() ([]byte, error) {
	return generate(streamtest.NewGenerator(1, codeRecord, streamtest.WithBudget(4<<20)))
}

// slip: the record function of the SLIP example's stream test
// (bytescan-examples, streams/slip/stream_test.go), without its counts. It
// draws the same random numbers in the same order, so it writes the same
// frames.
func slipRecord(rng *rand.Rand, _ int, dst []byte) []byte {
	if rng.Intn(8) == 0 {
		dst = append(dst, slipEnd)
	}
	n := rng.Intn(300)
	if rng.Intn(16) == 0 {
		n = 1000 + rng.Intn(2000)
	}
	for j := 0; j < n; j++ {
		var c byte
		switch rng.Intn(16) {
		case 0:
			c = slipEnd
		case 1:
			c = slipEsc
		default:
			c = byte(rng.Intn(256))
		}
		switch c {
		case slipEnd:
			dst = append(dst, slipEsc, slipEscEnd)
		case slipEsc:
			dst = append(dst, slipEsc, slipEscEsc)
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, slipEnd)
}

func slipInput() ([]byte, error) {
	return generate(streamtest.NewGenerator(55, slipRecord, streamtest.WithBudget(4<<20)))
}

// stream: lineRecord of streamtest/streamtest_test.go, copied.
func lineRecord(rng *rand.Rand, _ int, dst []byte) []byte {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	word := func() {
		n := 1 + rng.Intn(12)
		for j := 0; j < n; j++ {
			dst = append(dst, letters[rng.Intn(len(letters))])
		}
	}
	word()
	dst = append(dst, '=')
	word()
	for n := rng.Intn(6); n > 0; n-- {
		dst = append(dst, ' ')
		word()
	}
	return append(dst, '\n')
}

func streamInput() ([]byte, error) {
	return generate(streamtest.NewGenerator(1, lineRecord, streamtest.WithBudget(64<<20)))
}

func generate(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// large: four tokens of 1 MiB of 'a', separated by single spaces.
func largeInput() ([]byte, error) {
	tok := strings.Repeat("a", 1<<20)
	return []byte(strings.Join([]string{tok, tok, tok, tok}, " ")), nil
}
