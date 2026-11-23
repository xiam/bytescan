package bench

import (
	"bytes"
	"io"
	"regexp"
	"testing"

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
		if err != nil {
			t.Fatal(err)
		}
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
			if refs := references(c); refs != nil {
				t.Errorf("slip has the references %v, want none", refs)
			}
			continue
		}
		if got := references(c); len(got) != 2 || got[0] != textScanner || got[1] != regexpLexer {
			t.Fatalf("%s: references %v, want textscanner and regexp", c.name, got)
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
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			want = append(want, token{tok.Type(), tok.ByteOffset()})
		}

		// The tokens of the regexp lexer.
		if n := len(tokenClasses[c.name]); n != len(rules) {
			t.Fatalf("%s: %d token classes for %d rules", c.name, n, len(rules))
		}
		re := regexp.MustCompile(refPattern(c))
		re.Longest()
		var got []token
		for pos := 0; pos < len(in); {
			loc := re.FindSubmatchIndex(in[pos:])
			if loc == nil || loc[1] == 0 {
				t.Fatalf("%s: the regexp lexer matches nothing at byte %d", c.name, pos)
			}
			got = append(got, token{rules[class(loc)].typ, pos})
			pos += loc[1]
		}
		if len(got) != len(want) {
			t.Errorf("%s: the regexp lexer gives %d tokens, bytescan %d", c.name, len(got), len(want))
		}
		for i := 0; i < min(len(got), len(want)); i++ {
			if got[i] != want[i] {
				t.Errorf("%s: token %d is %v, bytescan gives %v", c.name, i, got[i], want[i])
				break
			}
		}

		// Both references through refLex, twice: the counts do not depend
		// on the run.
		for _, tl := range references(c) {
			first, err := refLex(c, tl, in, nop, nop)
			if err != nil {
				t.Fatalf("%s, %s: %v", c.name, tl, err)
			}
			second, err := refLex(c, tl, in, nop, nop)
			if err != nil {
				t.Fatalf("%s, %s: %v", c.name, tl, err)
			}
			if first.Tokens == 0 || first != second {
				t.Errorf("%s, %s: counted %+v, then %+v", c.name, tl, first, second)
			}
			if tl == regexpLexer && (first.Tokens != uint64(len(want)) || first.Errors != 0) {
				t.Errorf("%s, regexp: counted %+v, want %d tokens and no error", c.name, first, len(want))
			}
		}
	}
}

// TestRefLexRejects checks that the regexp lexer fails on a byte that no
// token class matches, and that refLex refuses an unknown reference.
func TestRefLexRejects(t *testing.T) {
	c, _ := findCase("large")
	if _, err := refLex(c, regexpLexer, []byte("a ="), nop, nop); err == nil {
		t.Errorf("the regexp lexer accepted %q", "a =")
	}
	if _, err := refLex(c, "lex", []byte("a"), nop, nop); err == nil {
		t.Errorf("refLex accepted an unknown reference")
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
	if err != nil {
		t.Fatal(err)
	}
	if res != (refResult{Tokens: 792586}) || procs != 12 {
		t.Errorf("parseRefRun = %+v, %d, want 792586 tokens and GOMAXPROCS 12", res, procs)
	}
	if _, _, err := parseRefRun(out, c, textScanner); err == nil {
		t.Errorf("parseRefRun accepted the line of another tool")
	}
	if _, _, err := parseRefRun([]byte("lane: control\nPASS\n"), c, regexpLexer); err == nil {
		t.Errorf("parseRefRun accepted an output without a benchmark line")
	}
}
