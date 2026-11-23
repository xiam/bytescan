package meter_test

// This file holds the rule sets and the input generators of the wrapper-cost
// benchmark (BenchmarkWrapperCost). They stand in for the reference cases of
// docs/specs/measurement.md until the harness builds those cases. Each rule
// set is copied, and its comment names its source.

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"strings"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
	"github.com/xiam/bytescan/streamtest"
)

// costCase is one case of the wrapper-cost benchmark.
type costCase struct {
	name string
	// input returns the input of the case. It runs before the timer.
	input func() []byte
	// newCursor returns the cursor that the scanner reads r through.
	newCursor func(r io.Reader) bytescan.Cursor
	// addRules registers the rule set of the case.
	addRules func(sc *bytescan.Scanner)
}

var costCases = []costCase{
	{name: "json", input: jsonInput, newCursor: textCursor, addRules: addJSONRules},
	{name: "code", input: codeInput, newCursor: textCursor, addRules: addCodeRules},
	{name: "slip", input: slipInput, newCursor: byteCursor, addRules: addSLIPRules},
	{name: "stream", input: streamInput, newCursor: textCursor, addRules: addLineRules},
	{name: "large", input: largeInput, newCursor: textCursor, addRules: addLargeRules},
}

func textCursor(r io.Reader) bytescan.Cursor { return cursor.NewReader(r) }

func byteCursor(r io.Reader) bytescan.Cursor { return bytesource.NewCursor(r) }

// json: the rules of the JSON scanner of bytescan-examples (json/scanner.go),
// over a generated JSON document of about the size of the reference input.

func addJSONRules(sc *bytescan.Scanner) {
	sc.MustAddRule("BEGIN_ARRAY", core.Literal("["))
	sc.MustAddRule("BEGIN_OBJECT", core.Literal("{"))
	sc.MustAddRule("END_ARRAY", core.Literal("]"))
	sc.MustAddRule("END_OBJECT", core.Literal("}"))
	sc.MustAddRule("NAME_SEPARATOR", core.Literal(":"))
	sc.MustAddRule("VALUE_SEPARATOR", core.Literal(","))
	sc.MustAddRule("TRUE", core.Literal("true"))
	sc.MustAddRule("FALSE", core.Literal("false"))
	sc.MustAddRule("NULL", core.Literal("null"))

	unescaped := core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not()
	escape := core.Sequence(
		core.Literal(`\`),
		core.Choice(
			core.Rune(core.RuneSet(`"\/bfnrt`)),
			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
		),
	)
	sc.MustAddRule("STRING", base.QuotedWith('"', unescaped, escape))

	noLeadingZeros := base.NoLeadingZeros()
	sc.MustAddRule("NUMBER", core.Sequence(
		core.Optional(core.Literal("-")),
		core.Choice(base.Integer(noLeadingZeros), base.Float(noLeadingZeros)),
	))
	sc.MustAddRule("WHITESPACE", core.RuneRun(core.RuneSet(" \t\n\r")))
}

// jsonInput returns an indented JSON array of records, from seed 1, that ends
// with the first record past 3,880,944 bytes.
func jsonInput() []byte {
	const size = 3880944
	rng := rand.New(rand.NewSource(1))
	var b bytes.Buffer
	b.WriteString("[\n")
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteString(",\n")
		}
		writeJSONRecord(&b, rng, 1, 0)
	}
	b.WriteString("\n]\n")
	return b.Bytes()
}

func writeJSONRecord(b *bytes.Buffer, rng *rand.Rand, indent, depth int) {
	pad := strings.Repeat("  ", indent)
	fmt.Fprintf(b, "%s{\n", pad)
	fmt.Fprintf(b, "%s  \"id\": %d,\n", pad, rng.Intn(1000000))
	fmt.Fprintf(b, "%s  \"name\": \"%s\",\n", pad, randomWord(rng, "abcdefghijklmnopqrstuvwxyz _-", 3, 24))
	fmt.Fprintf(b, "%s  \"path\": \"%s\\/%s\\u00e9\",\n", pad, randomWord(rng, "abcdefghij", 1, 8), randomWord(rng, "klmnopqrst", 1, 8))
	fmt.Fprintf(b, "%s  \"score\": %d.%de-%d,\n", pad, rng.Intn(1000)-500, rng.Intn(1000), rng.Intn(10))
	fmt.Fprintf(b, "%s  \"active\": %t,\n", pad, rng.Intn(2) == 0)
	fmt.Fprintf(b, "%s  \"parent\": null,\n", pad)
	fmt.Fprintf(b, "%s  \"tags\": [", pad)
	for j, n := 0, rng.Intn(5); j < n; j++ {
		if j > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "\"%s\"", randomWord(rng, "abcdefghijklmnopqrstuvwxyz", 2, 10))
	}
	b.WriteString("],\n")
	fmt.Fprintf(b, "%s  \"children\": [", pad)
	if depth < 2 {
		for j, n := 0, rng.Intn(3); j < n; j++ {
			if j > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n")
			writeJSONRecord(b, rng, indent+2, depth+1)
		}
	}
	fmt.Fprintf(b, "]\n%s}", pad)
}

func randomWord(rng *rand.Rand, alphabet string, minLen, maxLen int) string {
	n := minLen + rng.Intn(maxLen-minLen+1)
	w := make([]byte, n)
	for i := range w {
		w[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(w)
}

// code: the seven hand-written rules of BenchmarkComplexTokens
// (tests/bench_test.go; the rules are copied from tests/scanner_rules_test.go
// below), over generated SQL-like statements.

func addCodeRules(sc *bytescan.Scanner) {
	sc.MustAddRule("FLOAT", newUnsignedFloatRule())
	sc.MustAddRule("STRING", newSingleQuotedStringRule())
	sc.MustAddRule("COMMENT", newSlashStarCommentRule())
	sc.MustAddRule("ID", newIdentifierRule())
	sc.MustAddRule("WS", newWhitespaceRule())
	sc.MustAddRule("SYMBOL", newSymbolRule())
	sc.MustAddRule("KEYWORD", newLiteralAlternationRule("SELECT", "FROM", "WHERE"))
}

// codeInput returns SQL-like statements of the token classes of the code rule
// set, with varied identifiers, numbers, strings and comments, from seed 1. It
// ends at the first statement boundary past 4 MiB.
func codeInput() []byte {
	const size = 4 << 20
	const idStart = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_"
	const idRest = idStart + "0123456789"
	rng := rand.New(rand.NewSource(1))
	id := func() string {
		return randomWord(rng, idStart, 1, 1) + randomWord(rng, idRest, 0, 11)
	}
	var b bytes.Buffer
	for b.Len() < size {
		b.WriteString("SELECT")
		for j, n := 0, 1+rng.Intn(4); j < n; j++ {
			b.WriteString(" " + id())
		}
		fmt.Fprintf(&b, " FROM %s WHERE %s = ", id(), id())
		switch rng.Intn(3) {
		case 0:
			fmt.Fprintf(&b, "%d.%d", rng.Intn(100000), rng.Intn(1000))
		case 1:
			fmt.Fprintf(&b, ".%d", rng.Intn(1000))
		default:
			fmt.Fprintf(&b, "'%s'", randomWord(rng, "abcdefghijklmnopqrstuvwxyz \n", 0, 20))
		}
		if rng.Intn(3) == 0 {
			fmt.Fprintf(&b, " + (%d.5 * %s)", rng.Intn(100), id())
		}
		if rng.Intn(4) == 0 {
			fmt.Fprintf(&b, " /* %s */", randomWord(rng, "abcdefghijklmnopqrstuvwxyz *\n", 0, 40))
		}
		b.WriteString(";\n")
	}
	return b.Bytes()
}

// slip: the rules of the SLIP scanner of bytescan-examples
// (streams/slip/scanner.go), over the frames of the record function of its
// stream test (streams/slip/stream_test.go), from seed 55, to 4 MiB.

const (
	slipEnd    = 0xC0
	slipEsc    = 0xDB
	slipEscEnd = 0xDC
	slipEscEsc = 0xDD
)

func addSLIPRules(sc *bytescan.Scanner) {
	dataByte := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	sc.MustAddRule("END", octet.Set(slipEnd))
	sc.MustAddRule("DATA", core.RuneRunMax(dataByte, 1000))
	sc.MustAddRule("ESC_END", octet.Literal([]byte{slipEsc, slipEscEnd}))
	sc.MustAddRule("ESC_ESC", octet.Literal([]byte{slipEsc, slipEscEsc}))
}

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

func slipInput() []byte {
	return generate(streamtest.NewGenerator(55, slipRecord, streamtest.WithBudget(4<<20)))
}

// stream: the line scanner of the streamtest self-test (addLineRules and
// lineRecord in streamtest/streamtest_test.go), from seed 1, to 64 MiB.

func addLineRules(sc *bytescan.Scanner) {
	sc.MustAddRule("WORD", core.Repeat(core.Rune(core.RuneRange('a', 'z').Or(core.RuneRange('0', '9'))), 1, 1000))
	sc.MustAddRule("EQUALS", core.Literal("="))
	sc.MustAddRule("SPACE", core.Literal(" "))
	sc.MustAddRule("NEWLINE", core.Literal("\n"))
}

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

func streamInput() []byte {
	return generate(streamtest.NewGenerator(1, lineRecord, streamtest.WithBudget(64<<20)))
}

func generate(r io.Reader) []byte {
	in, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return in
}

// large: the identifier rule of BenchmarkLargeToken and the whitespace rule
// (copied from tests/scanner_rules_test.go below), over four tokens of 1 MiB
// of 'a' separated by single spaces.

func addLargeRules(sc *bytescan.Scanner) {
	sc.MustAddRule("ID", newIdentifierRule())
	sc.MustAddRule("WS", newWhitespaceRule())
}

func largeInput() []byte {
	tok := strings.Repeat("a", 1<<20)
	return []byte(strings.Join([]string{tok, tok, tok, tok}, " "))
}

// The hand-written rules below are copied from tests/scanner_rules_test.go.

func excludeLast(n int, s bytescan.State) (bytescan.Rule, bytescan.State) {
	if n < 1 {
		panic("excludeLast: n must be at least 1")
	}
	return pushBackChain(n-1, s), bytescan.StatePushBack
}

func pushBackChain(n int, s bytescan.State) bytescan.Rule {
	return func(bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if n > 0 {
			return pushBackChain(n-1, s), bytescan.StatePushBack
		}
		return nil, s
	}
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isASCIIWord(r rune) bool {
	return r == '_' || isASCIIDigit(r) || isASCIILetter(r)
}

func isASCIIWhitespace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

func isASCIISymbol(r rune) bool {
	switch r {
	case '+', '-', '*', '/', '=', ';', '(', ')':
		return true
	}
	return false
}

func isAnyRune(rune) bool { return true }

func newRunePlusRule(inClass func(rune) bool) bytescan.Rule {
	var loop bytescan.Rule
	loop = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if inClass(s.Rune()) {
			return loop, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return loop
}

func newLiteralAlternationRule(literals ...string) bytescan.Rule {
	var candidates [][]rune
	for _, lit := range literals {
		if lit != "" {
			candidates = append(candidates, []rune(lit))
		}
	}
	return literalAlternationAt(candidates, 0)
}

func literalAlternationAt(candidates [][]rune, i int) bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}

		endsHere := false
		var longer [][]rune
		for _, c := range candidates {
			if c[i] != s.Rune() {
				continue
			}
			if len(c) == i+1 {
				endsHere = true
			} else {
				longer = append(longer, c)
			}
		}

		var next bytescan.Rule
		if len(longer) > 0 {
			next = literalAlternationAt(longer, i+1)
		}

		switch {
		case endsHere:
			return next, bytescan.StateAccept
		case next != nil:
			return next, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
}

func newWhitespaceRule() bytescan.Rule {
	return newRunePlusRule(isASCIIWhitespace)
}

func newIdentifierRule() bytescan.Rule {
	var start, rest bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if r := s.Rune(); isASCIILetter(r) || r == '_' {
			return rest, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	rest = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIWord(s.Rune()) {
			return rest, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
	return start
}

func newUnsignedFloatRule() bytescan.Rule {
	var start, integerPart, afterInitialRadix, afterIntegerRadix, fractionalPart bytescan.Rule

	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return integerPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			return afterInitialRadix, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	integerPart = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return integerPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			return afterIntegerRadix, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	afterIntegerRadix = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept)
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		if s.Rune() == '.' {
			return nil, bytescan.StateReject
		}
		return excludeLast(1, bytescan.StateAccept)
	}

	afterInitialRadix = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}

	fractionalPart = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return excludeLast(1, bytescan.StateAccept)
		}
		if isASCIIDigit(s.Rune()) {
			return fractionalPart, bytescan.StateContinue
		}
		return excludeLast(1, bytescan.StateAccept)
	}

	return start
}

func newSymbolRule() bytescan.Rule {
	return func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if isASCIISymbol(s.Rune()) {
			return nil, bytescan.StateAccept
		}
		return nil, bytescan.StateReject
	}
}

func newSingleQuotedStringRule() bytescan.Rule {
	var start, body bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '\'' {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	body = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '\'' {
			return nil, bytescan.StateAccept
		}
		if isAnyRune(s.Rune()) {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return start
}

func newSlashStarCommentRule() bytescan.Rule {
	var start, afterSlash, body, afterStar bytescan.Rule
	start = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '/' {
			return afterSlash, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterSlash = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '*' {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	body = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '*' {
			return afterStar, bytescan.StateContinue
		}
		if isAnyRune(s.Rune()) {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	afterStar = func(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
		if s.IsEOF() {
			return nil, bytescan.StateReject
		}
		if s.Rune() == '/' {
			return nil, bytescan.StateAccept
		}
		if isAnyRune(s.Rune()) {
			return body, bytescan.StateContinue
		}
		return nil, bytescan.StateReject
	}
	return start
}
