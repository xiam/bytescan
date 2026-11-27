package modes

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/core"
)

// The rules of these tests are rules of rules/core. None of them keeps a
// variable from one token to the next, and there is no switching code: the
// actions are on the rules.

var (
	letters  = core.RuneRun(core.RuneRange('a', 'z').Or(core.RuneRange('A', 'Z')))
	spaces   = core.RuneRun(core.RuneSet(" "))
	newline  = core.Literal("\n")
	quote    = core.Literal(`"`)
	notQuote = core.RuneRun(core.RuneSet(`"`).Not())
)

// ruleSetB returns the modes of rule set B in docs/specs/resumable-lexing.md.
func ruleSetB() *ModeSet {
	ms := NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", letters).Rule("WS", spaces).Rule("NL", newline).
		Push("QUOTE", quote, "str")
	ms.Mode("str").
		Rule("TEXT", notQuote).
		Pop("QUOTE", quote)
	return ms
}

// nested returns modes that nest two levels: a parenthesis in code, and a
// string in code or in a parenthesis.
func nested() *ModeSet {
	ms := NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", letters).
		Push("OPEN", core.Literal("("), "paren").
		Push("QUOTE", quote, "str")
	ms.Mode("paren").
		Rule("WORD", letters).
		Pop("CLOSE", core.Literal(")")).
		Push("QUOTE", quote, "str")
	ms.Mode("str").
		Rule("TEXT", notQuote).
		Pop("QUOTE", quote)
	return ms
}

// step is one token and the mode state at its start.
type step struct {
	typ    bytescan.TokenType
	text   string
	offset int
	state  Stack
}

// scan drains d and returns each token with the mode state at its start, and
// the mode state at the end.
func scan(t *testing.T, d *StackDriver) ([]step, Stack) {
	t.Helper()
	var out []step
	for {
		st := d.Stack()
		l, err := d.Next()
		if err == io.EOF {
			return out, st
		}
		require.NoError(t, err)
		out = append(out, step{l.Type(), l.Text(), l.Offset(), st})
	}
}

func driverOver(t *testing.T, ms *ModeSet, text string, start Stack) *StackDriver {
	t.Helper()
	d, err := ms.Driver(cursor.New(strings.NewReader(text)), start)
	require.NoError(t, err)
	return d
}

// want is a token and its mode state, written as in rule set B: the names
// from the bottom to the top.
type want struct {
	typ   bytescan.TokenType
	text  string
	state []string
}

func checkSteps(t *testing.T, got []step, wants []want) {
	t.Helper()
	require.Len(t, got, len(wants))
	for i, w := range wants {
		assert.Equal(t, w.typ, got[i].typ, "token %d", i)
		assert.Equal(t, w.text, got[i].text, "token %d", i)
		assert.True(t, got[i].state.Equal(stackOf(w.state...)),
			"token %d %s %q: the state has the top %q and %d names, want %v",
			i, w.typ, w.text, got[i].state.Top(), got[i].state.Len(), w.state)
	}
}

var (
	code    = []string{"code"}
	codeStr = []string{"code", "str"}
)

// TestRuleSetB checks the mode state at the start of each token of the
// "Before" texts of rule set B, and of the text after edit B1.
func TestRuleSetB(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		wants []want
		end   []string
	}{
		{"B1 before", "say \"hi\" now \"x\" end\n", []want{
			{"WORD", "say", code}, {"WS", " ", code}, {"QUOTE", `"`, code},
			{"TEXT", "hi", codeStr}, {"QUOTE", `"`, codeStr}, {"WS", " ", code},
			{"WORD", "now", code}, {"WS", " ", code}, {"QUOTE", `"`, code},
			{"TEXT", "x", codeStr}, {"QUOTE", `"`, codeStr}, {"WS", " ", code},
			{"WORD", "end", code}, {"NL", "\n", code},
		}, code},
		{"B1 after", "say hi\" now \"x\" end\n", []want{
			{"WORD", "say", code}, {"WS", " ", code}, {"WORD", "hi", code},
			{"QUOTE", `"`, code}, {"TEXT", " now ", codeStr}, {"QUOTE", `"`, codeStr},
			{"WORD", "x", code}, {"QUOTE", `"`, code}, {"TEXT", " end\n", codeStr},
		}, codeStr},
		{"B2 before", "say hi now\n", []want{
			{"WORD", "say", code}, {"WS", " ", code}, {"WORD", "hi", code},
			{"WS", " ", code}, {"WORD", "now", code}, {"NL", "\n", code},
		}, code},
		{"B3 before", "say \"hi\" now\n", []want{
			{"WORD", "say", code}, {"WS", " ", code}, {"QUOTE", `"`, code},
			{"TEXT", "hi", codeStr}, {"QUOTE", `"`, codeStr}, {"WS", " ", code},
			{"WORD", "now", code}, {"NL", "\n", code},
		}, code},
		{"B4 before", "say \"hi now\" end\n", []want{
			{"WORD", "say", code}, {"WS", " ", code}, {"QUOTE", `"`, code},
			{"TEXT", "hi now", codeStr}, {"QUOTE", `"`, codeStr}, {"WS", " ", code},
			{"WORD", "end", code}, {"NL", "\n", code},
		}, code},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, end := scan(t, driverOver(t, ruleSetB(), tc.text, Stack{}))
			checkSteps(t, got, tc.wants)
			assert.True(t, end.Equal(stackOf(tc.end...)), "the state at the end")
		})
	}
}

// TestStackDriverNesting pushes two levels and pops both.
func TestStackDriverNesting(t *testing.T) {
	got, end := scan(t, driverOver(t, nested(), `a(b"c"d)e`, Stack{}))
	checkSteps(t, got, []want{
		{"WORD", "a", []string{"code"}},
		{"OPEN", "(", []string{"code"}},
		{"WORD", "b", []string{"code", "paren"}},
		{"QUOTE", `"`, []string{"code", "paren"}},
		{"TEXT", "c", []string{"code", "paren", "str"}},
		{"QUOTE", `"`, []string{"code", "paren", "str"}},
		{"WORD", "d", []string{"code", "paren"}},
		{"CLOSE", ")", []string{"code", "paren"}},
		{"WORD", "e", []string{"code"}},
	})
	assert.True(t, end.Equal(stackOf("code")))
}

// TestStackCopyAndCompare checks that a saved state does not change when the
// scan goes on, and that states compare by their names.
func TestStackCopyAndCompare(t *testing.T) {
	d := driverOver(t, nested(), `(a"b")(c)"d"`, Stack{})
	var saved []Stack
	for i := 0; i < 3; i++ { // ( a "
		saved = append(saved, d.Stack())
		_, err := d.Next()
		require.NoError(t, err)
	}
	inString := d.Stack() // a copy is an assignment
	got, _ := scan(t, d)

	// The copies did not change while the scan went on.
	assert.True(t, inString.Equal(stackOf("code", "paren", "str")))
	assert.Equal(t, "str", inString.Top())
	assert.Equal(t, 3, inString.Len())
	assert.True(t, saved[0].Equal(stackOf("code")))
	assert.True(t, saved[1].Equal(stackOf("code", "paren")))
	assert.True(t, saved[2].Equal(stackOf("code", "paren")))

	// The second parenthesis pushes a new frame. Its state holds the same
	// names as the state in the first one, so the two are equal.
	require.Equal(t, "c", got[4].text)
	assert.NotSame(t, saved[1].top, got[4].state.top)
	assert.True(t, got[4].state.Equal(saved[1]))

	// The string in code is [code str], and not the string in a
	// parenthesis.
	require.Equal(t, "d", got[7].text)
	assert.True(t, got[7].state.Equal(stackOf("code", "str")))
	assert.False(t, got[7].state.Equal(inString))
	assert.False(t, got[7].state.Equal(saved[1]))
	assert.False(t, got[7].state.Equal(saved[0]))
}

// TestStackDriverGivenStart starts a scan at each token boundary with the
// state that the scan from the start of the text had there. It gives the
// tokens and the states of the scan from the start, from that boundary on.
func TestStackDriverGivenStart(t *testing.T) {
	for _, tc := range []struct {
		ms   func() *ModeSet
		text string
	}{
		{ruleSetB, "say \"hi\" now \"x\" end\n"},
		{ruleSetB, "say hi\" now \"x\" end\n"},
		{nested, `a(b"c"d)e"(f)"(g)`},
	} {
		full, fullEnd := scan(t, driverOver(t, tc.ms(), tc.text, Stack{}))
		for i, at := range full {
			p := at.offset
			got, end := scan(t, driverOver(t, tc.ms(), tc.text[p:], at.state))
			require.Len(t, got, len(full)-i, "%q from %d", tc.text, p)
			for j, g := range got {
				w := full[i+j]
				assert.Equal(t, w.typ, g.typ, "%q from %d, token %d", tc.text, p, j)
				assert.Equal(t, w.text, g.text, "%q from %d, token %d", tc.text, p, j)
				assert.Equal(t, w.offset-p, g.offset, "%q from %d, token %d", tc.text, p, j)
				assert.True(t, w.state.Equal(g.state), "%q from %d, token %d: state", tc.text, p, j)
			}
			assert.True(t, fullEnd.Equal(end), "%q from %d: the state at the end", tc.text, p)
		}
	}
}

// TestStackDriverPopLastMode checks that a pop while only the bottom mode is
// on the stack does nothing: the stack stays the same, and Next returns no
// error.
func TestStackDriverPopLastMode(t *testing.T) {
	ms := NewModeSet("code")
	ms.Mode("code").
		Rule("WORD", letters).
		Push("OPEN", core.Literal("("), "paren").
		Pop("CLOSE", core.Literal(")"))
	ms.Mode("paren").
		Rule("WORD", letters).
		Pop("CLOSE", core.Literal(")"))

	got, end := scan(t, driverOver(t, ms, "a)b))(c))d", Stack{}))
	checkSteps(t, got, []want{
		{"WORD", "a", []string{"code"}},
		{"CLOSE", ")", []string{"code"}},
		{"WORD", "b", []string{"code"}},
		{"CLOSE", ")", []string{"code"}},
		{"CLOSE", ")", []string{"code"}},
		{"OPEN", "(", []string{"code"}},
		{"WORD", "c", []string{"code", "paren"}},
		{"CLOSE", ")", []string{"code", "paren"}},
		{"CLOSE", ")", []string{"code"}},
		{"WORD", "d", []string{"code"}},
	})
	assert.True(t, end.Equal(stackOf("code")))

	// A given start of one mode other than the start mode stays in it too.
	got, end = scan(t, driverOver(t, ms, "x)y", stackOf("paren")))
	checkSteps(t, got, []want{
		{"WORD", "x", []string{"paren"}},
		{"CLOSE", ")", []string{"paren"}},
		{"WORD", "y", []string{"paren"}},
	})
	assert.True(t, end.Equal(stackOf("paren")))
}

// TestStackDriverEOF checks that io.EOF comes back itself, repeats, and does
// not change the stack, and that an error from the scanner is returned
// unchanged.
func TestStackDriverEOF(t *testing.T) {
	d := driverOver(t, ruleSetB(), `say "hi`, Stack{})
	_, end := scan(t, d)
	assert.True(t, end.Equal(stackOf("code", "str")))
	for i := 0; i < 2; i++ {
		l, err := d.Next()
		assert.Nil(t, l)
		assert.True(t, err == io.EOF, "err = %v, want io.EOF itself", err)
		assert.True(t, d.Stack().Equal(end))
	}

	// No rule of code matches "!", so the scanner returns an UNKNOWN token.
	// A token that no rule of the mode made leaves the stack unchanged.
	d = driverOver(t, ruleSetB(), `"!`, Stack{})
	got, _ := scan(t, d)
	checkSteps(t, got, []want{
		{"QUOTE", `"`, []string{"code"}},
		{"TEXT", "!", []string{"code", "str"}},
	})
	d = driverOver(t, ruleSetB(), "a!b", Stack{})
	got, _ = scan(t, d)
	checkSteps(t, got, []want{
		{"WORD", "a", []string{"code"}},
		{bytescan.TokenTypeUnknown, "!", []string{"code"}},
		{"WORD", "b", []string{"code"}},
	})

	// An error from the reader comes back from Next and leaves the stack
	// unchanged.
	errBoom := errors.New("boom")
	cur := cursor.New(io.MultiReader(strings.NewReader(`a "b`), iotest.ErrReader(errBoom)))
	d, err := ruleSetB().Driver(cur, Stack{})
	require.NoError(t, err)
	var last Stack
	for {
		last = d.Stack()
		if _, err = d.Next(); err != nil {
			break
		}
	}
	assert.ErrorIs(t, err, errBoom)
	assert.True(t, d.Stack().Equal(last))
}

// TestStackDriverReleases checks that a change of the top mode releases the
// scanner of the outgoing mode, and that a token that stays does not.
func TestStackDriverReleases(t *testing.T) {
	cur := cursor.New(strings.NewReader(`ab "cd" ef`))
	d, err := ruleSetB().Driver(cur, Stack{})
	require.NoError(t, err)
	for i, switches := range []bool{false, false, true, false, true, false, false} {
		_, err := d.Next()
		require.NoError(t, err)
		if switches {
			assert.Zero(t, cur.RetainedBytes(), "token %d: the switch after it must release the outgoing scanner", i)
		} else {
			assert.Greater(t, cur.RetainedBytes(), 0, "token %d: a token that stays keeps its context", i)
		}
	}
}

// TestStackDriverConcurrentNext runs Next and Stack from several goroutines.
// Under -race it fails if two scanners on the cursor run at the same time.
func TestStackDriverConcurrentNext(t *testing.T) {
	input := strings.Repeat(`ab "cd" `, 300)
	d := driverOver(t, ruleSetB(), input, Stack{})

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		total int
	)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_ = d.Stack().Len()
				l, err := d.Next()
				if err == io.EOF {
					return
				}
				if err != nil {
					assert.NoError(t, err)
					return
				}
				mu.Lock()
				total += len(l.Text())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, len(input), total)
	assert.True(t, d.Stack().Equal(stackOf("code")))
}

func TestModeSetDriverErrors(t *testing.T) {
	cur := cursor.New(strings.NewReader(""))

	_, err := NewModeSet("code").Driver(cur, Stack{})
	assert.ErrorContains(t, err, `the start mode "code" is not in the set`)

	ms := NewModeSet("code")
	ms.Mode("code").Push("QUOTE", quote, "str")
	_, err = ms.Driver(cur, Stack{})
	assert.ErrorContains(t, err, `pushes the mode "str", which is not in the set`)

	_, err = ruleSetB().Driver(cur, stackOf("code", "paren"))
	assert.ErrorContains(t, err, `the start stack holds the mode "paren"`)

	ms = NewModeSet("code")
	ms.Mode("code").Rule("QUOTE", quote).Pop("QUOTE", quote)
	_, err = ms.Driver(cur, Stack{})
	assert.ErrorIs(t, err, bytescan.ErrDuplicateRule)

	ms = NewModeSet("code")
	ms.Mode("code").Rule("WORD", nil)
	_, err = ms.Driver(cur, Stack{})
	assert.ErrorIs(t, err, bytescan.ErrNilRule)

	// A Push or a Pop with the type UNKNOWN is rejected: the scanner gives
	// that type to input that no rule matches, so an unmatched "!" would run
	// the action of the rule.
	ms = NewModeSet("code")
	ms.Mode("code").Push(bytescan.TokenTypeUnknown, quote, "str")
	ms.Mode("str").Rule("TEXT", notQuote)
	_, err = ms.Driver(cur, Stack{})
	assert.ErrorContains(t, err, `the rule "UNKNOWN" of the mode "code" pushes or pops`)

	ms = NewModeSet("code")
	ms.Mode("code").Push("QUOTE", quote, "str")
	ms.Mode("str").Rule("TEXT", notQuote).Pop(bytescan.TokenTypeUnknown, quote)
	_, err = ms.Driver(cur, Stack{})
	assert.ErrorContains(t, err, `the rule "UNKNOWN" of the mode "str" pushes or pops`)

	// A Rule with the type UNKNOWN stays, so an unmatched token and a match
	// of the rule both leave the stack unchanged.
	ms = NewModeSet("code")
	ms.Mode("code").Rule(bytescan.TokenTypeUnknown, quote).Push("OPEN", core.Literal("("), "paren")
	ms.Mode("paren").Rule("WORD", letters).Pop("CLOSE", core.Literal(")"))
	got, _ := scan(t, driverOver(t, ms, `!"(a)!`, Stack{}))
	checkSteps(t, got, []want{
		{bytescan.TokenTypeUnknown, "!", []string{"code"}},
		{bytescan.TokenTypeUnknown, `"`, []string{"code"}},
		{"OPEN", "(", []string{"code"}},
		{"WORD", "a", []string{"code", "paren"}},
		{"CLOSE", ")", []string{"code", "paren"}},
		{bytescan.TokenTypeUnknown, "!", []string{"code"}},
	})

	// A given start need not hold the start mode.
	ms = NewModeSet("code")
	ms.Mode("other").Rule("WORD", letters)
	_, err = ms.Driver(cur, stackOf("other"))
	assert.NoError(t, err)
}

// TestModeSetMode checks that Mode returns the same mode for one name, and
// that a change to the set after Driver does not change the driver.
func TestModeSetMode(t *testing.T) {
	ms := NewModeSet("code")
	assert.Same(t, ms.Mode("code"), ms.Mode("code"))
	ms.Mode("code").Rule("WORD", letters)

	d := driverOver(t, ms, "ab cd", Stack{})
	ms.Mode("code").Rule("WS", spaces)

	l, err := d.Next()
	require.NoError(t, err)
	assert.Equal(t, "ab", l.Text())
	l, err = d.Next()
	require.NoError(t, err)
	assert.Equal(t, bytescan.TokenTypeUnknown, l.Type(), "the WS rule added after Driver must not be in the driver")

	got, _ := scan(t, driverOver(t, ms, "ab cd", Stack{}))
	assert.Len(t, got, 3)
}
