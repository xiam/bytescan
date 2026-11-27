package core_test

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/octet"
)

// The differential test of the table (form A of docs/specs/table-compiler.md,
// "The proof of equal tokens"): the table and the engine run the same rule
// set on the same input, and every token is compared.

// tableRule is one registered rule of a differential rule set.
type tableRule struct {
	typ  bytescan.TokenType
	rule bytescan.Rule
}

// tableSet is a rule set of the differential test, with the runes that its
// generated inputs are made of.
type tableSet struct {
	name     string
	rules    []tableRule
	alphabet string
	bytes    bool // the scanner reads through bytesource.NewCursor

	// tabled is the number of rules of the set that run on a table: pure
	// core rules built by a combinator, whose table is inside the budget.
	tabled int
}

// one returns the rule set of one rule.
func one(name string, r bytescan.Rule, alphabet string, tabled int) tableSet {
	return tableSet{name: name, rules: []tableRule{{"R", r}}, alphabet: alphabet, tabled: tabled}
}

// handRun is a hand-written rule for a run of 'x': an opaque part.
func handRun(s bytescan.Symbol) (bytescan.Rule, bytescan.State) {
	if !s.IsEOF() && s.Rune() == 'x' {
		return handRun, bytescan.StateAccept
	}
	return nil, bytescan.StateReject
}

// panicky is a class of the letters a and b that panics on '!'. It gives the
// same answer for the same rune, as practice 6 requires.
func panicky(r rune) bool {
	if r == '!' {
		panic("panicky: '!'")
	}
	return r == 'a' || r == 'b'
}

// overBudget returns a pure core rule whose table is over the budget: a
// rule that must remember the last n runes, (?:a|b)*a(?:a|b){n}, has 2^(n+1)
// states.
func overBudget() bytescan.Rule {
	ab := core.Rune(core.RuneSet("ab"))
	return core.Sequence(core.ZeroOrMore(ab), core.Literal("a"), core.Repeat(ab, 14, 14))
}

// differentialSets returns the rule sets of the differential test. A set is
// built anew for each call, so its tables are built anew too.
func differentialSets() []tableSet {
	jsonUnescaped := core.RuneSet(`"\`).Or(core.RuneRange(0x00, 0x1F)).Not()
	jsonEscape := core.Sequence(
		core.Literal(`\`),
		core.Choice(
			core.Rune(core.RuneSet(`"\/bfnrt`)),
			core.Sequence(core.Literal("u"), core.Repeat(core.Rune(core.ASCIIHexDigit), 4, 4)),
		),
	)
	noLeadingZeros := base.NoLeadingZeros()
	dataByte := bytesource.AnyByte.And(core.RuneSet(bytesource.Text("\xC0\xDB")).Not())
	ab := core.RuneSet("ab")

	return []tableSet{
		// The rule sets of the reference cases json, slip and stream
		// (internal/bench/rules.go), copied.
		{name: "json", alphabet: "{}[]:, \"\\u0-1.eE+tfnaé\x01", tabled: 3, rules: []tableRule{
			{"BEGIN_ARRAY", core.Literal("[")},
			{"BEGIN_OBJECT", core.Literal("{")},
			{"END_ARRAY", core.Literal("]")},
			{"END_OBJECT", core.Literal("}")},
			{"NAME_SEPARATOR", core.Literal(":")},
			{"VALUE_SEPARATOR", core.Literal(",")},
			{"TRUE", core.Literal("true")},
			{"FALSE", core.Literal("false")},
			{"NULL", core.Literal("null")},
			{"STRING", base.QuotedWith('"', jsonUnescaped, jsonEscape)},
			{"NUMBER", core.Sequence(
				core.Optional(core.Literal("-")),
				core.Choice(base.Integer(noLeadingZeros), base.Float(noLeadingZeros)),
			)},
			{"WHITESPACE", core.RuneRun(core.RuneSet(" \t\n\r"))},
		}},
		{name: "slip", bytes: true, alphabet: "\xC0\xDB\xDC\xDDa\x00\xFF", tabled: 1, rules: []tableRule{
			{"END", octet.Set(0xC0)},
			{"DATA", core.RuneRunMax(dataByte, 1000)},
			{"ESC_END", octet.Literal([]byte{0xDB, 0xDC})},
			{"ESC_ESC", octet.Literal([]byte{0xDB, 0xDD})},
		}},
		{name: "stream", alphabet: "a9= \nZ", tabled: 1, rules: []tableRule{
			{"WORD", core.Repeat(core.Rune(core.RuneRange('a', 'z').Or(core.RuneRange('0', '9'))), 1, 1000)},
			{"EQUALS", core.Literal("=")},
			{"SPACE", core.Literal(" ")},
			{"NEWLINE", core.Literal("\n")},
		}},
		{name: "short words", alphabet: "a9= \nZ", tabled: 2, rules: []tableRule{
			{"WORD", core.Repeat(core.Rune(core.RuneRange('a', 'z').Or(core.RuneRange('0', '9'))), 1, 3)},
			{"OTHER", core.RuneRun(core.RuneSet("= \nZ"))},
		}},

		// The two shapes that a careless construction gets wrong.
		one("same threads, different accepts",
			core.Choice(core.Sequence(core.ZeroOrMore(core.Literal("a")), core.Literal("b")), core.Literal("a")), "abc", 1),
		one("a literal beside AnyRune", core.Sequence(core.Literal("a"), core.Rune(core.AnyRune)), "abé", 1),
		{name: "literals only", alphabet: "abcsSkKſK", tabled: 3, rules: []tableRule{
			{"LIT", core.Sequence(core.Literal("ab"), core.Literal("c"))},
			{"ANY", core.Choice(core.AnyLiteral("a", "abc", "b"), core.Literal("ca"))},
			{"FOLD", core.Sequence(core.LiteralIgnoreCase("sk"), core.Optional(core.AnyLiteralIgnoreCase("s", "kk")))},
		}},

		// Ties between registered rules, broken by registration order.
		{name: "ties", alphabet: "if x", tabled: 4, rules: []tableRule{
			{"KEYWORD", core.Choice(core.Literal("if"), core.Literal("fi"))},
			{"WORD", core.RuneRun(core.RuneSet("ifx"))},
			{"SPACE", core.RuneRun(core.RuneSet(" "))},
			{"IF", core.Sequence(core.Literal("i"), core.Literal("f"))},
		}},

		// Classes with runes above U+00FF, which the engine steps.
		{name: "unicode", alphabet: "aéλ1 ж", tabled: 3, rules: []tableRule{
			{"WORD", core.RuneRun(core.UnicodeLetter)},
			{"MIXED", core.Sequence(core.Rune(core.RuneSet("aé")), core.RuneRun(core.RuneSet("éλ1")))},
			{"SPACE", core.RuneRunMax(core.ASCIISpace, 2)},
		}},

		// CJK prose and emoji: runes above U+00FF, whose steps the cache of
		// each table keeps. A literal of such runes leads to sets of threads
		// that the table does not have, which continue on the engine.
		{name: "cjk", alphabet: "日本語。，１2.5 \u3000％", tabled: 5, rules: []tableRule{
			{"NIHON", core.Sequence(core.Literal("日本"), core.Optional(core.Literal("語")))},
			{"WORD", core.RuneRun(core.UnicodeLetter)},
			{"NUMBER", core.Sequence(core.RuneRun(core.UnicodeNumber), core.Optional(core.Sequence(core.Literal("."), core.RuneRun(core.UnicodeNumber))))},
			{"PUNCT", core.RuneRun(core.UnicodePunct)},
			{"SPACE", core.RuneRun(core.UnicodeSeparator.Or(core.ASCIISpace))},
		}},
		{name: "emoji", alphabet: "😀👍🏽\u200da é", tabled: 3, rules: []tableRule{
			{"THUMB", core.Sequence(core.Literal("👍"), core.Optional(core.Rune(core.RuneRange(0x1F3FB, 0x1F3FF))))},
			{"EMOJI", core.Sequence(core.RuneRun(core.UnicodeSymbol), core.ZeroOrMore(core.Sequence(core.Literal("\u200d"), core.RuneRun(core.UnicodeSymbol))))},
			{"WORD", core.RuneRun(core.UnicodeLetter.Or(core.RuneSet(" ")))},
		}},

		// Counted repetition and options.
		{name: "counts", alphabet: "ab-", tabled: 4, rules: []tableRule{
			{"R2", core.Repeat(core.Rune(ab), 2, 5)},
			{"R0", core.Sequence(core.Literal("-"), core.Repeat(core.Literal("ab"), 0, 3), core.Optional(core.Literal("-")))},
			{"MAX", core.RuneRunMax(ab, 3)},
			{"MANY", core.OneOrMore(core.Sequence(core.Optional(core.Literal("a")), core.Literal("b")))},
		}},

		// A class that panics: the rule is retired for the token.
		one("panicking class", core.Sequence(core.Rune(core.RuneSet("ab")), core.RuneRun(panicky)), "ab!", 1),

		// Rules that are not pure core rules keep the engine. AtLineStart
		// hands its first symbol to its rule, which runs on its own table.
		{name: "fallback", alphabet: "xaf \n", tabled: 1, rules: []tableRule{
			{"HAND", core.Sequence(core.Literal("a"), handRun)},
			{"LINE", core.AtLineStart(core.RuneRun(core.RuneSet("af")))},
			{"LOOK", core.FollowedBy(core.RuneRun(core.RuneSet("f")), core.Literal(" "))},
			{"KEYWORD", core.Keyword("af", core.RuneSet("x"))},
			{"SPACE", core.Sequence(core.Optional(core.NotFollowedBy(core.Literal(" "), core.Literal("\n"))), core.RuneRun(core.RuneSet(" \n")))},
		}},

		// A pure core rule over the budget falls back to the engine.
		{name: "over the budget", alphabet: "ab ", tabled: 1, rules: []tableRule{
			{"OVER", overBudget()},
			{"AB", core.RuneRun(ab)},
		}},
	}
}

// traceSet lexes the parts of an input with the rule set ts, with the table
// or with the engine forced, and returns one line for each call of
// NextLookahead: the token (type, text, offsets, span, line and column), its
// lookahead record and the error. Each part after the first is fed after the
// scanner returns bytescan.ErrMoreInput.
func traceSet(t testing.TB, ts tableSet, engine bool, parts ...string) []string {
	t.Helper()
	lines, err := traceSetResult(ts, engine, parts...)
	require.NoError(t, err)
	return lines
}

// traceSetResult keeps helper failures separate from the recorded scanner
// errors. Workers return its error to the owning test before any assertion.
func traceSetResult(ts tableSet, engine bool, parts ...string) ([]string, error) {
	defer core.SetEngineForced(engine)()

	src := &partSource{}
	var cur *cursor.Reader
	if ts.bytes {
		cur = bytesource.NewCursor(src)
	} else {
		cur = cursor.NewReader(src)
	}
	sc := bytescan.NewWithCursor(cur)
	for _, r := range ts.rules {
		sc.MustAddRule(r.typ, r.rule)
	}

	var lines []string
	for i := 0; ; {
		if i < len(parts) {
			src.buf = append(src.buf, parts[i]...)
			i++
		}
		if i == len(parts) {
			src.ended = true
		}
		for {
			tok, la, err := sc.NextLookahead()
			if err != nil {
				lines = append(lines, fmt.Sprintf("error %v", err))
				if errors.Is(err, bytescan.ErrMoreInput) && i < len(parts) {
					break
				}
				return lines, nil
			}
			lines = append(lines, fmt.Sprintf("%s %q offset %d byte %d rune %d span %v line %d column %d after %d end %v",
				tok.Type(), tok.Text(), tok.Offset(), tok.ByteOffset(), tok.RuneOffset(), tok.Span(), tok.Line(), tok.Column(), la.After, la.ReadEnd))
			if len(lines) > 4*len(strings.Join(parts, ""))+8 {
				return lines, fmt.Errorf("set %s: too many tokens", ts.name)
			}
		}
	}
}

// partSource is an input that arrives in parts: it returns
// cursor.ErrMoreInput while it has no input and has not ended.
type partSource struct {
	buf   []byte
	ended bool
}

func (s *partSource) Read(p []byte) (int, error) {
	if len(s.buf) == 0 {
		if s.ended {
			return 0, io.EOF
		}
		return 0, cursor.ErrMoreInput
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// compareSet lexes the parts with the table and with the engine and
// reports a difference.
func compareSet(t testing.TB, ts tableSet, parts ...string) bool {
	t.Helper()
	want := traceSet(t, ts, true, parts...)
	got := traceSet(t, ts, false, parts...)
	if strings.Join(want, "\n") == strings.Join(got, "\n") {
		return true
	}
	assert.Failf(t, "unexpected test state", "set %s, input %q: the table and the engine differ\nengine:\n  %s\ntable:\n  %s", ts.name, parts, strings.Join(want, "\n  "), strings.Join(got, "\n  "))
	return false
}

// symbols returns the symbols of the inputs of ts: the runes of its
// alphabet, or its bytes when the set reads bytes.
func (ts tableSet) symbols() []string {
	var out []string
	if ts.bytes {
		for i := 0; i < len(ts.alphabet); i++ {
			out = append(out, ts.alphabet[i:i+1])
		}
		return out
	}
	for _, r := range ts.alphabet {
		out = append(out, string(r))
	}
	return out
}

// runes returns the symbols of s as the scanner of ts reads them: one rune
// for each byte when the set reads bytes (bytesource), its runes otherwise.
func (ts tableSet) runes(s string) []rune {
	if !ts.bytes {
		return []rune(s)
	}
	out := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = rune(s[i])
	}
	return out
}

// allStrings returns every string of the symbols of ts, from length 1 up to
// the length at which there are about 4000 of them.
func (ts tableSet) allStrings() []string {
	syms := ts.symbols()
	n, total := 0, 1
	for total*len(syms) <= 4000 && n < 8 {
		total *= len(syms)
		n++
	}
	var out []string
	level := []string{""}
	for l := 1; l <= n; l++ {
		var next []string
		for _, s := range level {
			for _, sym := range syms {
				next = append(next, s+sym)
			}
		}
		out = append(out, next...)
		level = next
	}
	return out
}

// TestTableMatchesEngine is the differential test of the table: for every
// rule set of differentialSets, every string of its alphabet up to a
// length, and longer inputs, lex the same tokens with the table and with
// the engine forced, also when the input arrives in parts
// (bytescan.ErrMoreInput).
func TestTableMatchesEngine(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	long := map[string][]string{
		"json":   {`{"a": [1, -0.5e+3, true, false, null, "x\"\u00e9\u12ab\\"], "b": {}}` + "\n", `[01, -, 1., "\u12", "é"]`},
		"slip":   {"\xC0abc\xDB\xDC\x00\xFF\xDB\xDD\xC0\xDBx\xC0" + strings.Repeat("z", 1500)},
		"stream": {"key=value a b\n" + strings.Repeat("w", 1200) + "=x\n"},
		"cjk":    {"\u3000日本語は、2026年に１２.５％増えた。日本で「語」を読む。" + strings.Repeat("語本日。", 300) + "\n"},
		"emoji":  {"a 😀\u200d👍🏽 é👍👍🏽🏽 " + strings.Repeat("😀\u200d😀", 200) + "\u200d"},
	}
	for _, ts := range differentialSets() {
		t.Run(ts.name, func(t *testing.T) {
			inputs := ts.allStrings()
			inputs = append(inputs, long[ts.name]...)
			for _, in := range inputs {
				if !compareSet(t, ts, in) {
					return
				}
			}
			// The inputs of at least 4 bytes again, in two parts, cut at
			// several places.
			for _, in := range inputs {
				if len(in) < 4 {
					continue
				}
				for cut := 1; cut < len(in); cut += 1 + len(in)/4 {
					if !compareSet(t, ts, in[:cut], in[cut:]) {
						return
					}
				}
			}
		})
	}
}

// TestTableRulesTabled checks which rules of each differential set run on a
// table. The VM shows the first symbol of a token to every rule, so one lex
// builds the table of every pure core rule built by a combinator. A rule
// whose table is over the budget keeps the engine, and every other rule
// builds no table.
func TestTableRulesTabled(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	for _, ts := range differentialSets() {
		builds, over, _ := core.TableBuilds()
		traceSet(t, ts, false, ts.alphabet[:1])
		builds2, over2, _ := core.TableBuilds()
		assert.Equal(t, ts.tabled, int(builds2-builds-(over2-over)), "set %s: rules on a table", ts.name)
		wantOver := 0
		if ts.name == "over the budget" {
			wantOver = 1
		}
		assert.Equal(t, wantOver, int(over2-over), "set %s: tables over the budget", ts.name)
	}
}

// stepTrace calls r with the runes of input, one Rule value after the
// other, and returns each verdict. After each step it also calls the Rule
// value before it again with the rune other, so a state that a later step
// changed would show.
func stepTrace(r bytescan.Rule, input []rune, other rune) []string {
	var out []string
	prev := r
	for _, c := range input {
		next, state := call(prev, bytescan.NewSymbol(c, bytescan.FlagNone))
		_, again := call(prev, bytescan.NewSymbol(other, bytescan.FlagNone))
		out = append(out, fmt.Sprintf("%q %v %v again %v", c, state, next != nil, again))
		if next == nil {
			return out
		}
		prev = next
	}
	_, state := call(prev, bytescan.NewSymbol(bytescan.RuneEOF, bytescan.FlagEOF))
	return append(out, fmt.Sprintf("EOF %v", state))
}

// call calls r with s as the VM does: a panic retires the rule, as a
// reject.
func call(r bytescan.Rule, s bytescan.Symbol) (next bytescan.Rule, state bytescan.State) {
	returned := false
	defer func() {
		if !returned {
			_ = recover()
			next, state = nil, bytescan.StateReject
		}
	}()
	next, state = r(s)
	returned = true
	return next, state
}

// TestTableStepsMatchEngine compares the verdicts of each rule of the
// differential sets, called directly, on the table and on the engine: on
// every string of the set's alphabet up to a length, with the ancestor of
// each step called again.
func TestTableStepsMatchEngine(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	for _, ts := range differentialSets() {
		t.Run(ts.name, func(t *testing.T) {
			other := ts.runes(ts.alphabet)[0]
			for _, s := range ts.allStrings() {
				in := ts.runes(s)
				for _, r := range ts.rules {
					restore := core.SetEngineForced(true)
					want := stepTrace(r.rule, in, other)
					restore()
					got := stepTrace(r.rule, in, other)
					if !assert.Equal(t, want, got, "rule %s on %q", r.typ, in) {
						return
					}
				}
			}
		})
	}
}

// TestTabledRuleReuse runs the scenarios of TestCombinatorReuse,
// TestCombinatorRetainedAncestor and TestCombinatorSharedRule on rules that
// run on a table: one registered rule on concurrent scanners, a mid-token
// Rule value called concurrently, and an ancestor called after its
// descendants advanced. The results are those of the engine.
func TestTabledRuleReuse(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	number := core.Sequence(
		core.Optional(core.Rune(core.RuneSet("+-"))),
		core.RuneRun(core.ASCIIDigit),
		core.Optional(core.Sequence(core.Literal("."), core.RuneRun(core.ASCIIDigit))),
	)
	word := core.Choice(core.Literal("if"), core.Literal("ifelse"), core.RuneRun(core.ASCIILetter))
	set := tableSet{name: "reuse", rules: []tableRule{
		{"NUMBER", number}, {"WORD", word}, {"SPACE", core.RuneRun(core.ASCIISpace)},
	}}
	input := strings.Repeat("12 -3.5 +7e 4.25 1.x 9a ifelse if abc ", 20)
	want := traceSet(t, set, true, input)

	t.Run("shared by concurrent scanners", func(t *testing.T) {
		var wg sync.WaitGroup
		got := make([][]string, 8)
		errs := make([]error, len(got))
		for i := range got {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				got[i], errs[i] = traceSetResult(set, false, input)
			}(i)
		}
		wg.Wait()
		for i := range got {
			require.NoError(t, errs[i], "scanner %d", i)
			assert.Equal(t, want, got[i], "scanner %d", i)
		}
	})

	t.Run("mid-token value called concurrently", func(t *testing.T) {
		r := core.ZeroOrMore(core.Rune(core.RuneSet("ab")))
		next, _ := r(bytescan.NewSymbol('a', bytescan.FlagNone))
		require.NotNil(t, next)
		var wg sync.WaitGroup
		states := make([]bytescan.State, 8)
		for i := range states {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := 'a' + rune(i%3) // 'c' is not in the class
				_, states[i] = next(bytescan.NewSymbol(c, bytescan.FlagNone))
			}(i)
		}
		wg.Wait()
		for i, st := range states {
			want := bytescan.StateAccept
			if i%3 == 2 {
				want = bytescan.StateReject
			}
			assert.Equal(t, want, st, "call %d", i)
		}
	})

	t.Run("retained ancestor", func(t *testing.T) {
		words := core.Choice(
			core.Literal("abcd"), core.Literal("abce"), core.Literal("abxy"),
			core.Sequence(core.Literal("ab"), core.RuneRun(core.ASCIILetter)),
			core.Sequence(core.RuneRun(core.ASCIILetter), core.Literal("q")),
		)
		for _, in := range []string{"abcdq", "abxyz", "abq", "qqq"} {
			restore := core.SetEngineForced(true)
			want := stepTrace(words, []rune(in), 'q')
			restore()
			assert.Equal(t, want, stepTrace(words, []rune(in), 'q'), "input %q", in)
		}
	})
}

// FuzzTableMatchesEngine is the fuzz target of the differential test: every
// rule set of differentialSets lexes the input, cut in two parts at cut, with
// the table and with the engine forced, and the tokens must be equal.
func FuzzTableMatchesEngine(f *testing.F) {
	if core.EngineForced() {
		f.Skip("the build forces the engine")
	}
	f.Add(`{"a": [1, -0.5e+3, true], "b": "é"}`, uint(5))
	f.Add("\xC0abc\xDB\xDC\x00\xFF\xC0", uint(3))
	f.Add("key=value a b\n", uint(0))
	f.Add("aab ab sk ſK if fi éλ1 ab!", uint(7))
	f.Add("日本語は、2026年に１２.５％。\u3000日本 😀\u200d👍🏽 é", uint(9))
	sets := differentialSets()
	f.Fuzz(func(t *testing.T, in string, cut uint) {
		if len(in) > 512 {
			return
		}
		at := int(cut % uint(len(in)+1))
		for _, ts := range sets {
			if !compareSet(t, ts, in[:at], in[at:]) {
				return
			}
		}
	})
}

// TestTableBudgetFits checks that the table of every rule of the reference
// rule sets (json, slip and stream, in differentialSets) is inside the
// budget, and logs the size of each table: the largest one has 1000 states,
// one for each count of the WORD rule of stream, Repeat(Rune(c), 1, 1000),
// and of the DATA rule of slip, RuneRunMax(c, 1000). The largest oracle row
// of tests/, RuneRunMax(c, 4096) in TestRuneRunMaxMatchesRepeatOracle, fits
// too, and the over-budget rule does not.
func TestTableBudgetFits(t *testing.T) {
	largest := 0
	for _, ts := range differentialSets()[:3] {
		for _, r := range ts.rules {
			states, classes, tabled, inBudget := core.TableSize(r.rule)
			if !tabled {
				continue
			}
			t.Logf("%s %s: %d states, %d symbol classes", ts.name, r.typ, states, classes)
			assert.True(t, inBudget, "%s %s: the table is over the budget", ts.name, r.typ)
			largest = max(largest, states)
		}
	}
	t.Logf("largest table of the reference sets: %d states", largest)
	assert.Equal(t, 1000, largest)

	states, _, _, inBudget := core.TableSize(core.RuneRunMax(core.RuneSet("a"), 4096))
	assert.True(t, inBudget, "RuneRunMax(c, 4096) is over the budget")
	assert.Equal(t, 4096, states)
	_, _, _, inBudget = core.TableSize(core.RuneRunMax(core.RuneSet("a"), 4097))
	assert.False(t, inBudget, "RuneRunMax(c, 4097) fits the budget")

	_, _, tabled, inBudget := core.TableSize(overBudget())
	assert.True(t, tabled)
	assert.False(t, inBudget, "the over-budget rule fits the budget")
}

// BenchmarkTableBuild reports the time and the allocations of the build of
// the table of each tabled rule of the reference rule sets, and of the
// over-budget rule, which stops at the budget.
func BenchmarkTableBuild(b *testing.B) {
	rules := []tableRule{{"over the budget", overBudget()}}
	for _, ts := range differentialSets()[:3] {
		for _, r := range ts.rules {
			rules = append(rules, tableRule{bytescan.TokenType(ts.name + " " + string(r.typ)), r.rule})
		}
	}
	for _, r := range rules {
		b.Run(string(r.typ), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				core.TableSize(r.rule)
			}
		})
	}
}

// TestTableBudgetBoundsClosures: a rule whose closures are large, a long
// Repeat of a part that can match the empty text, reaches every one of its
// 20000 leaves from each of them without reading a symbol. The build counts
// every instruction that a closure visits and every thread that it stores,
// so it stops at the budget early, and the rule keeps the engine with the
// same verdict on a first rune that it rejects.
func TestTableBudgetBoundsClosures(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	part := core.Optional(core.Rune(core.RuneSet("a")))
	r := core.Repeat(core.Repeat(part, 1000, 1000), 20, 20)

	_, over, _ := core.TableBuilds()
	next, state := r(bytescan.NewSymbol('b', bytescan.FlagNone))
	assert.Nil(t, next)
	assert.Equal(t, bytescan.StateReject, state)
	_, over2, _ := core.TableBuilds()
	assert.Equal(t, uint64(1), over2-over, "the build is not over the budget")

	_, _, tabled, inBudget := core.TableSize(r)
	assert.True(t, tabled)
	assert.False(t, inBudget)

	// The engine gives the same verdict. A rune of the class is not
	// stepped here: the engine follows each of the 20000 closures from
	// each leaf, which is its own cost, not the table's.
	restore := core.SetEngineForced(true)
	next, want := r(bytescan.NewSymbol('b', bytescan.FlagNone))
	restore()
	assert.Nil(t, next)
	assert.Equal(t, want, state)
}

// TestTableBudgetBoundsCounts: a counting leaf with a large count,
// RuneRunMax(c, n), has one thread for each count. The build counts every
// thread that it finds and every successor that it stores, so a count far
// above the state bound stops at the budget before the build stores one
// thread for each count, and the rule keeps the engine with the same
// verdicts.
func TestTableBudgetBoundsCounts(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	r := core.RuneRunMax(core.RuneSet("a"), 1<<20)
	_, _, tabled, inBudget := core.TableSize(r)
	assert.True(t, tabled)
	assert.False(t, inBudget)

	for _, in := range []string{"a", "aaaa", "ab", "b"} {
		restore := core.SetEngineForced(true)
		want := stepTrace(r, []rune(in), 'b')
		restore()
		assert.Equal(t, want, stepTrace(r, []rune(in), 'b'), "input %q", in)
	}
}

// TestTableCacheBounded drives the cache of a table past its size: the
// steps of two states on four times as many runes above U+00FF as the cache
// keeps. The cache fills up to its size and never keeps more steps, so the
// memory of the table stays bounded, and every step still gives the verdict
// of the engine.
func TestTableCacheBounded(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	r := core.RuneRun(core.AnyRune)
	start, kept, ok := core.TableCache(r)
	require.True(t, ok)
	run, state := start(bytescan.NewSymbol('a', bytescan.FlagNone))
	require.NotNil(t, run)
	require.Equal(t, bytescan.StateAccept, state)

	n := 2 * core.TableCacheSize
	drive := func(from rune) {
		for c := from; c < from+rune(n); c++ {
			s := bytescan.NewSymbol(c, bytescan.FlagNone)
			for _, rule := range []bytescan.Rule{start, run} {
				next, state := rule(s)
				require.Falsef(t, next == nil || state != bytescan.StateAccept, "rune %U: %v, %v, want a Rule value and accept", c, next != nil, state)
				{
					_, again := next(bytescan.NewSymbol(bytescan.RuneEOF, bytescan.FlagEOF))
					require.Truef(t, again == bytescan.StateReject, "rune %U, then EOF: %v, want reject", c, again)
				}
			}
		}
	}
	drive(0x4E00)
	full := kept()
	assert.LessOrEqual(t, full, core.TableCacheSize, "the cache grew past its size")
	assert.Greater(t, full, core.TableCacheSize*9/10, "the cache is far from full after %d steps", 2*n)
	drive(0x4E00 + rune(n))
	assert.LessOrEqual(t, kept(), core.TableCacheSize, "the cache grew past its size")

	// The rule itself, on its own table, gives the verdicts of the engine
	// on runes beyond the size of its cache.
	in := make([]rune, 0, 3*core.TableCacheSize)
	for c := rune(0x4E00); len(in) < cap(in); c++ {
		in = append(in, c)
	}
	restore := core.SetEngineForced(true)
	want := stepTrace(r, in, 'a')
	restore()
	assert.Equal(t, want, stepTrace(r, in, 'a'))
}

// TestTableCacheConcurrent lexes CJK text with one rule set on concurrent
// scanners, so the Rule values of each table fill and read its cache
// concurrently. The tokens are those of the engine.
func TestTableCacheConcurrent(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	var set tableSet
	for _, ts := range differentialSets() {
		if ts.name == "cjk" {
			set = ts
		}
	}
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteRune(0x4E00 + rune(i*7%5000))
		if i%9 == 8 {
			b.WriteString([]string{"。", "，", " ", "１２.５", "日本語"}[i%5])
		}
	}
	input := b.String()
	want := traceSet(t, set, true, input)

	var wg sync.WaitGroup
	got := make([][]string, 8)
	errs := make([]error, len(got))
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = traceSetResult(set, false, input)
		}(i)
	}
	wg.Wait()
	for i := range got {
		require.NoError(t, errs[i], "scanner %d", i)
		assert.Equal(t, want, got[i], "scanner %d", i)
	}
}

// wideRule is one rule of wideRules, and whether its table fits the budget.
type wideRule struct {
	name string
	rule bytescan.Rule
	fits bool
}

// wideWords returns n distinct words of the letters a to z, of min to max
// letters, from rng.
func wideWords(rng *rand.Rand, n, min, max int) []string {
	seen := map[string]bool{}
	var out []string
	for len(out) < n {
		w := make([]byte, min+rng.Intn(max-min+1))
		for i := range w {
			w[i] = byte('a' + rng.Intn(26))
		}
		if !seen[string(w)] {
			seen[string(w)] = true
			out = append(out, string(w))
		}
	}
	return out
}

// wideLiterals returns a Literal, or a LiteralIgnoreCase when fold is true,
// for each word.
func wideLiterals(words []string, fold bool) []bytescan.Rule {
	var out []bytescan.Rule
	for _, w := range words {
		if fold {
			out = append(out, core.LiteralIgnoreCase(w))
		} else {
			out = append(out, core.Literal(w))
		}
	}
	return out
}

// wideRules returns a generated set of wide pure core rules, the shapes that
// lexers use and whose tables are larger than those of the reference rule
// sets: choices of keywords, the Go keywords beside an identifier, choices of
// literals with case folding, a large AnyLiteral, and the counted shapes of
// addresses, dates, UUIDs, numbers and escapes. The words come from seed 163.
func wideRules() []wideRule {
	rng := rand.New(rand.NewSource(163))
	digit := core.Rune(core.ASCIIDigit)
	hex := core.Rune(core.ASCIIHexDigit)
	letter := core.Rune(core.UnicodeLetter)
	d2 := core.Repeat(digit, 2, 2)
	goKeywords := []string{"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var"}
	ident := core.Sequence(core.Rune(core.ASCIIIdentifierStart), core.ZeroOrMore(core.Rune(core.ASCIIWord)))
	return []wideRule{
		{"go keywords", core.Choice(wideLiterals(goKeywords, false)...), true},
		{"go keywords or identifier", core.Choice(core.Choice(wideLiterals(goKeywords, false)...), ident), true},
		{"50 keywords", core.Choice(wideLiterals(wideWords(rng, 50, 2, 10), false)...), true},
		{"100 keywords", core.Choice(wideLiterals(wideWords(rng, 100, 2, 10), false)...), true},
		{"25 folded keywords", core.Choice(wideLiterals(wideWords(rng, 25, 3, 8), true)...), true},
		{"AnyLiteral of 1000 words", core.Sequence(core.AnyLiteral(wideWords(rng, 1000, 3, 9)...), core.Optional(core.Literal("s"))), false},
		{"IPv4", core.Sequence(core.Repeat(digit, 1, 3), core.Literal("."), core.Repeat(digit, 1, 3), core.Literal("."), core.Repeat(digit, 1, 3), core.Literal("."), core.Repeat(digit, 1, 3)), true},
		{"IPv6", core.Sequence(core.Repeat(hex, 1, 4), core.Repeat(core.Sequence(core.Literal(":"), core.Repeat(hex, 0, 4)), 2, 7)), true},
		{"date and time", core.Sequence(core.Repeat(digit, 4, 4), core.Literal("-"), d2, core.Literal("-"), d2, core.Rune(core.RuneSet("Tt ")), d2, core.Literal(":"), d2, core.Literal(":"), d2,
			core.Optional(core.Sequence(core.Literal("."), core.Repeat(digit, 1, 9))),
			core.Choice(core.Rune(core.RuneSet("Zz")), core.Sequence(core.Rune(core.RuneSet("+-")), d2, core.Literal(":"), d2))), true},
		{"UUID", core.Sequence(core.Repeat(hex, 8, 8), core.Literal("-"), core.Repeat(hex, 4, 4), core.Literal("-"), core.Repeat(hex, 4, 4), core.Literal("-"), core.Repeat(hex, 4, 4), core.Literal("-"), core.Repeat(hex, 12, 12)), true},
		{"email", core.Sequence(core.RuneRun(core.ASCIIWord), core.ZeroOrMore(core.Sequence(core.Literal("."), core.RuneRun(core.ASCIIWord))), core.Literal("@"), core.RuneRun(core.ASCIIWord), core.OneOrMore(core.Sequence(core.Literal("."), core.RuneRun(core.ASCIILetter)))), true},
		{"Unicode identifier", core.Sequence(letter, core.ZeroOrMore(core.Choice(letter, core.Rune(core.UnicodeNumber), core.Literal("_")))), true},
		{"Go number", core.Choice(
			core.Sequence(core.Literal("0"), core.Rune(core.RuneSet("xX")), core.RuneRun(core.ASCIIHexDigit.Or(core.RuneSet("_")))),
			core.Sequence(core.RuneRun(core.ASCIIDigit.Or(core.RuneSet("_"))), core.Optional(core.Sequence(core.Literal("."), core.ZeroOrMore(digit))), core.Optional(core.Sequence(core.Rune(core.RuneSet("eE")), core.Optional(core.Rune(core.RuneSet("+-"))), core.RuneRun(core.ASCIIDigit)))),
		), true},
		{"quoted with escapes", base.QuotedWith('"', core.RuneSet(`"\`).Not(), core.Sequence(core.Literal(`\`), core.Choice(core.Rune(core.RuneSet(`"\abfnrtv`)), core.Sequence(core.Literal("U"), core.Repeat(hex, 8, 8)), core.Sequence(core.Literal("u"), core.Repeat(hex, 4, 4)), core.Sequence(core.Literal("x"), core.Repeat(hex, 2, 2))))), true},
		{"RuneRunMax(c, 255)", core.RuneRunMax(core.ASCIILetter, 255), true},
		{"Repeat of a Sequence, 1 to 64", core.Repeat(core.Sequence(core.Rune(core.ASCIILetter), core.Optional(core.Rune(core.ASCIIDigit))), 1, 64), true},
		{"Repeat(c, 1, 1000)", core.Repeat(core.Rune(core.ASCIIWord), 1, 1000), true},
		{"hex pairs", core.Repeat(core.Sequence(hex, hex, core.Optional(core.Literal(":"))), 1, 32), true},
	}
}

// TestTableBudgetWideRules measures the tables that set the build budget:
// the largest table of the reference rule sets, of the largest oracle row of
// tests/ and of wideRules. It logs the states and the work of each, and
// checks which ones fit. The state bound is set by the oracle row
// RuneRunMax(c, 4096) (TestTableBudgetFits). The work bound is set by the
// choice of 100 keywords, the largest work of wideRules that fits: a choice
// of literals steps each of its leaves on every rune below U+0100 in each
// state of the leaf. The AnyLiteral of 1000 words is over the budget and
// keeps the engine.
func TestTableBudgetWideRules(t *testing.T) {
	type measured struct {
		name string
		rule bytescan.Rule
		fits bool
	}
	var all []measured
	for _, ts := range differentialSets()[:3] {
		for _, r := range ts.rules {
			if _, _, tabled, _ := core.TableSize(r.rule); tabled {
				all = append(all, measured{ts.name + " " + string(r.typ), r.rule, true})
			}
		}
	}
	all = append(all, measured{"oracle RuneRunMax(c, 4096)", core.RuneRunMax(core.RuneSet("a"), 4096), true})
	for _, w := range wideRules() {
		all = append(all, measured{w.name, w.rule, w.fits})
	}

	largestStates, largestWork := 0, 0
	var budget int
	for _, m := range all {
		states, classes, tabled, inBudget := core.TableSize(m.rule)
		require.True(t, tabled, "%s is not a pure core rule", m.name)
		work, b, _ := core.TableWork(m.rule)
		budget = b
		if !assert.Equal(t, m.fits, inBudget, "%s: inside the budget", m.name) || !inBudget {
			t.Logf("%s: over the budget", m.name)
			continue
		}
		t.Logf("%s: %d states, %d symbol classes, work %d", m.name, states, classes, work)
		largestStates = max(largestStates, states)
		largestWork = max(largestWork, work)
	}
	t.Logf("largest: %d states, work %d of a budget of %d", largestStates, largestWork, budget)
	assert.Equal(t, 4096, largestStates)
	assert.Less(t, largestWork, budget)
}

// liveHeap returns the bytes of the live heap after a full collection.
func liveHeap() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

// The bounds of TestTableFixedMemory, per table of RuneRun(UnicodeLetter),
// which has 2 states. Its rows and its states take about 0.7 KiB. The cache
// that its first rune above U+00FF makes, with tableCacheMinSlots slots of 8
// bytes, the index and the Rule values of the states, and one kept step,
// takes about 0.75 KiB more. Each bound leaves a margin of more than 2 to
// these sizes. A cache of tableCacheSize slots, made at once, takes 32 KiB.
const (
	tableBuildBound    = 2 << 10
	tableFirstFarBound = 2 << 10
)

// TestTableFixedMemory measures the memory that the tables of many rules
// hold: once built over ASCII input, and again after each table has met one
// rune above U+00FF. The cache of a table costs nothing before such a rune,
// and little after the first one, so a fixed allocation of the size of the
// whole cache per table fails the test.
func TestTableFixedMemory(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	const n = 64
	rules := make([]bytescan.Rule, n)
	for i := range rules {
		rules[i] = core.RuneRun(core.UnicodeLetter)
		_, _, tabled, inBudget := core.TableSize(rules[i])
		require.True(t, tabled && inBudget)
	}
	before := liveHeap()
	for _, r := range rules {
		_, state := r(bytescan.NewSymbol('a', bytescan.FlagNone))
		require.Equal(t, bytescan.StateAccept, state)
	}
	built := liveHeap()
	for _, r := range rules {
		_, state := r(bytescan.NewSymbol('日', bytescan.FlagNone))
		require.Equal(t, bytescan.StateAccept, state)
	}
	far := liveHeap()
	runtime.KeepAlive(rules)

	perBuild, perFar := (built-before)/n, (far-built)/n
	t.Logf("per table: %d bytes built over ASCII input, %d more after one rune above U+00FF", perBuild, perFar)
	assert.LessOrEqual(t, perBuild, int64(tableBuildBound), "the memory of a table built over ASCII input")
	assert.LessOrEqual(t, perFar, int64(tableFirstFarBound), "the memory of a table after its first rune above U+00FF")
}

// TestTableCacheStates steps many states of one table on the same runes
// above U+00FF, each to a different next state: the cache tells the steps of
// the states apart, before and after it grows, and every step gives the
// verdict of the engine.
func TestTableCacheStates(t *testing.T) {
	if core.EngineForced() {
		t.Skip("the build forces the engine")
	}
	r := core.RuneRunMax(core.UnicodeLetter, 300)
	in := []rune(strings.Repeat("日", 400))
	restore := core.SetEngineForced(true)
	want := stepTrace(r, in, '本')
	restore()
	for i := 0; i < 2; i++ {
		assert.Equal(t, want, stepTrace(r, in, '本'), "pass %d", i)
	}
}
