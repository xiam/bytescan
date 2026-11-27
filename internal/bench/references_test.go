package bench

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/internal/lextest"
	"github.com/xiam/bytescan/streamtest"
)

// refInput returns the input of a text case for the tests: the stream case
// over the first 1 MiB of its generator, as in TestCasesLex. ok is false for
// the code case under the race detector, which TestCasesLex skips too.
func refInput(t *testing.T, c Case) (in []byte, ok bool) {
	t.Helper()
	switch {
	case c.name == "stream":
		in, err := generate(streamtest.NewGenerator(1, lineRecord, streamtest.WithBudget(1<<20)))
		require.NoError(t, err)
		return in, true
	case c.name == "code" && lextest.RaceDetector:
		return nil, false
	}
	return input(t, c), true
}

// TestReferencesLex checks the outside references on every text case. The
// regexp lexer gives the same tokens as bytescan: the same type, at the same
// byte offset, in the same order. text/scanner lexes the whole input and
// gives the same count in two runs. slip has no reference.
func TestReferencesLex(t *testing.T) {
	for _, c := range cases() {
		if c.name == "slip" {
			{
				refs := references(c)
				assert.Truef(t, refs == nil, "slip has the references %v, want none", refs)
			}
			continue
		}
		wantRefs := []tool{textScanner, regexpLexer}
		if c.name == "json" {
			wantRefs = append(wantRefs, jsonDecoder)
		}
		{
			got := references(c)
			require.Truef(t, slices.Equal(got, wantRefs), "%s: references %v, want %v", c.name, got, wantRefs)
		}
		in, ok := refInput(t, c)
		if !ok {
			continue
		}

		// The tokens of bytescan, in the control lane.
		type token struct {
			typ    bytescan.TokenType
			offset int
		}
		var want []token
		sc := bytescan.NewWithCursor(c.newCursor(bytes.NewReader(in)))
		rules := c.rules()
		for _, rr := range rules {
			sc.MustAddRule(rr.typ, rr.rule)
		}
		for {
			tok, err := sc.Next()
			if err == io.EOF {
				break
			}
			require.NoErrorf(t, err, "%s: %v", c.name, err)
			want = append(want, token{tok.Type(), tok.ByteOffset()})
		}

		// The tokens of the regexp lexer.
		{
			n := len(tokenClasses[c.name])
			require.Truef(t, n == len(rules), "%s: %d token classes for %d rules", c.name, n, len(rules))
		}
		re := regexp.MustCompile(refPattern(c))
		re.Longest()
		var got []token
		for pos := 0; pos < len(in); {
			loc := re.FindSubmatchIndex(in[pos:])
			require.Falsef(t, loc == nil || loc[1] == 0, "%s: the regexp lexer matches nothing at byte %d", c.name, pos)
			got = append(got, token{rules[class(loc)].typ, pos})
			pos += loc[1]
		}
		assert.Truef(t, len(got) == len(want), "%s: the regexp lexer gives %d tokens, bytescan %d", c.name, len(got), len(want))
		for i := 0; i < min(len(got), len(want)); i++ {
			if got[i] != want[i] {
				assert.Failf(t, "unexpected test state", "%s: token %d is %v, bytescan gives %v", c.name, i, got[i], want[i])
				break
			}
		}

		// Run each reference through refLex twice. The counts do not depend
		// on the run.
		for _, tl := range references(c) {
			first, err := refLex(c, tl, in, nop, nop)
			require.NoErrorf(t, err, "%s, %s: %v", c.name, tl, err)
			second, err := refLex(c, tl, in, nop, nop)
			require.NoErrorf(t, err, "%s, %s: %v", c.name, tl, err)
			assert.Falsef(t, first.Tokens == 0 || first != second, "%s, %s: counted %+v, then %+v", c.name, tl, first, second)
			assert.Falsef(t, tl == regexpLexer && (first.Tokens != uint64(len(want)) || first.Errors != 0), "%s, regexp: counted %+v, want %d tokens and no error", c.name, first, len(want))
		}
	}
}

// TestJSONDecoderEvents compares the two APIs through EOF on the exact
// benchmark input. The lexer exposes separators and whitespace as tokens,
// and keeps scalar values in their source spelling.
func TestJSONDecoderEvents(t *testing.T) {
	c, _ := findCase("json")
	in := input(t, c)

	scanner := bytescan.NewWithCursor(c.newCursor(bytes.NewReader(in)))
	for _, rr := range c.rules() {
		scanner.MustAddRule(rr.typ, rr.rule)
	}
	decoder := json.NewDecoder(bytes.NewReader(in))
	var events uint64
	for {
		tok, err := scanner.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		var want json.Token
		switch tok.Type() {
		case "WHITESPACE", "NAME_SEPARATOR", "VALUE_SEPARATOR":
			continue
		case "BEGIN_ARRAY", "BEGIN_OBJECT", "END_ARRAY", "END_OBJECT":
			want = json.Delim(tok.Text()[0])
		case "STRING", "NUMBER", "TRUE", "FALSE", "NULL":
			if err := json.Unmarshal([]byte(tok.Text()), &want); err != nil {
				require.FailNowf(t, "unexpected test state", "decode lexer token at byte %d: %v", tok.ByteOffset(), err)
			}
		default:
			require.FailNowf(t, "unexpected test state", "unexpected lexer token %s at byte %d", tok.Type(), tok.ByteOffset())
		}
		got, err := decoder.Token()
		if err != nil || got != want {
			require.FailNowf(t, "unexpected test state", "event %d at byte %d: got %v (%v), want %v", events, tok.ByteOffset(), got, err, want)
		}
		events++
	}
	{
		_, err := decoder.Token()
		require.Truef(t, err == io.EOF, "decoder did not reach EOF with the lexer: %v", err)
	}
	{
		got, err := refLex(c, jsonDecoder, in, nop, nop)
		require.Falsef(t, err != nil || got != (refResult{Tokens: events}), "timed reference counted %+v (%v), want %d events and no errors", got, err, events)
	}
}

// TestRefLexRejects checks that regexp and jsondecoder stop on invalid
// input, and that refLex refuses an unknown reference.
func TestRefLexRejects(t *testing.T) {
	c, _ := findCase("large")
	{
		_, err := refLex(c, regexpLexer, []byte("a ="), nop, nop)
		assert.Falsef(t, err == nil, "the regexp lexer accepted %q", "a =")
	}
	jsonCase, _ := findCase("json")
	{
		_, err := refLex(jsonCase, jsonDecoder, []byte("[}"), nop, nop)
		assert.False(t, err == nil, "the JSON decoder accepted mismatched delimiters")
	}
	{
		_, err := refLex(c, "lex", []byte("a"), nop, nop)
		assert.Falsef(t, err == nil, "refLex accepted an unknown reference")
	}
}

// TestParseRefRun checks the reading of the output of one run of a
// reference: its benchmark line, its GOMAXPROCS and its "reference" line.
func TestParseRefRun(t *testing.T) {
	c, _ := findCase("json")
	out := []byte("lane: control\ngoos: linux\n" +
		"BenchmarkReference/json/regexp-12 \t       1\t 515617639 ns/op\t164998920 B/op\t  792798 allocs/op\n" +
		"--- BENCH: BenchmarkReference/json/regexp-12\n" +
		"    bench_test.go:140: reference {\"Tokens\":792586,\"Errors\":0}\nPASS\n")
	res, procs, err := parseRefRun(out, c, regexpLexer)
	require.NoError(t, err)
	assert.Falsef(t, res != (refResult{Tokens: 792586}) || procs != 12, "parseRefRun = %+v, %d, want 792586 tokens and GOMAXPROCS 12", res, procs)
	{
		_, _, err := parseRefRun(out, c, textScanner)
		assert.Falsef(t, err == nil, "parseRefRun accepted the line of another tool")
	}
	{
		_, _, err := parseRefRun([]byte("lane: control\nPASS\n"), c, regexpLexer)
		assert.Falsef(t, err == nil, "parseRefRun accepted an output without a benchmark line")
	}
}
