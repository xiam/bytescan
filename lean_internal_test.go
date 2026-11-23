package bytescan

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode"

	"github.com/xiam/bytescan/cursor"
)

// publicCursor embeds a *cursor.Reader. It is not a *cursor.Reader, so the
// scanner reads it through ReadLocatedRune, the public read path, and the
// methods it promotes (Remark, Start, Context) keep their effect.
type publicCursor struct{ *cursor.Reader }

// leanRecord wraps rule so that every symbol shown to it is appended to log,
// with its rune, its flags and its size. The rules of both scanners are wrapped
// the same way, so the two logs are equal exactly when the rules are shown the
// same symbols.
func leanRecord(log *[]Symbol, rule Rule) Rule {
	return func(s Symbol) (Rule, State) {
		*log = append(*log, s)
		next, st := rule(s)
		if next != nil {
			next = leanRecord(log, next)
		}
		return next, st
	}
}

// leanRunRule accepts one or more runes of class, the longest run.
func leanRunRule(class func(rune) bool) Rule {
	var more Rule
	more = func(s Symbol) (Rule, State) {
		if !s.IsEOF() && class(s.Rune()) {
			return more, StateAccept
		}
		return nil, StateReject
	}
	return more
}

// leanQuoted accepts a double-quoted string on one line. An unterminated one
// reads to the end of the line or of the input and is rejected, so the
// scanner steps back over a long overshoot.
func leanQuoted() Rule {
	var body Rule
	body = func(s Symbol) (Rule, State) {
		switch {
		case s.IsEOF() || s.IsEOL():
			return nil, StateReject
		case s.Rune() == '"':
			return nil, StateAccept
		}
		return body, StateContinue
	}
	return func(s Symbol) (Rule, State) {
		if s.Rune() == '"' {
			return body, StateContinue
		}
		return nil, StateReject
	}
}

// leanHashAtBOL accepts '#' only at the beginning of a line, so the flags
// decide the tokens.
func leanHashAtBOL() Rule {
	return func(s Symbol) (Rule, State) {
		if s.Rune() == '#' && s.IsBOL() {
			return nil, StateAccept
		}
		return nil, StateReject
	}
}

// leanLiteral accepts s exactly.
func leanLiteral(lit string) Rule {
	runes := []rune(lit)
	var at func(i int) Rule
	at = func(i int) Rule {
		return func(s Symbol) (Rule, State) {
			if s.IsEOF() || s.Rune() != runes[i] {
				return nil, StateReject
			}
			if i == len(runes)-1 {
				return nil, StateAccept
			}
			return at(i + 1), StateContinue
		}
	}
	return at(0)
}

func leanAddRules(sc *Scanner, log *[]Symbol) {
	sc.MustAddRule("WORD", leanRecord(log, leanRunRule(unicode.IsLetter)))
	sc.MustAddRule("SPACE", leanRecord(log, leanRunRule(func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' })))
	sc.MustAddRule("NL", leanRecord(log, leanLiteral("\n")))
	sc.MustAddRule("STRING", leanRecord(log, leanQuoted()))
	sc.MustAddRule("HASH", leanRecord(log, leanHashAtBOL()))
	sc.MustAddRule("ARROW", leanRecord(log, leanLiteral("-->")))
	sc.MustAddRule("DASH", leanRecord(log, leanLiteral("-")))
}

// leanStep is what one call of NextLookahead returned, with the context of
// the token.
type leanStep struct {
	typ     TokenType
	text    string
	span    cursor.Span
	la      Lookahead
	context string
	err     string
}

// leanScan lexes with sc until the end of input, a repeated error or a limit,
// and returns every result. When seekBack is set, it steps the shared cursor
// back over every third token after it is returned, so the scanner lexes that
// token again from the replayed input.
func leanScan(sc *Scanner, cur *cursor.Reader, seekBack bool) []leanStep {
	var steps []leanStep
	errs := 0
	for i := 0; i < 5000; i++ {
		tok, la, err := sc.NextLookahead()
		if err != nil {
			steps = append(steps, leanStep{err: err.Error()})
			if err == io.EOF {
				return steps
			}
			if errors.Is(err, ErrMoreInput) {
				continue
			}
			if errors.Is(err, io.ErrNoProgress) {
				// Not sticky: a retry lexes the token again.
				continue
			}
			if errs++; errs > 2 {
				return steps
			}
			continue
		}
		ctx, cerr := sc.Context(tok, 4, 4)
		if cerr != nil {
			ctx = "error: " + cerr.Error()
		}
		steps = append(steps, leanStep{typ: tok.Type(), text: tok.Text(), span: tok.Span(), la: la, context: ctx})
		if seekBack && i%3 == 2 {
			if _, err := cur.Seek(int64(-tok.Span().Bytes()), io.SeekCurrent); err != nil {
				steps = append(steps, leanStep{err: "seek: " + err.Error()})
			}
		}
	}
	return steps
}

// moreInputSource returns ErrMoreInput once after every chunk of n bytes.
type moreInputSource struct {
	data    []byte
	n       int
	pending bool
}

func (m *moreInputSource) Read(p []byte) (int, error) {
	if m.pending {
		m.pending = false
		return 0, cursor.ErrMoreInput
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	k := min(m.n, len(p), len(m.data))
	copy(p, m.data[:k])
	m.data = m.data[k:]
	m.pending = true
	return k, nil
}

// zeroReadSource returns its data one byte at a time, with an empty read that
// has a nil error after every byte. The cursor reports such a read as
// io.ErrNoProgress, which is not sticky, so a retry lexes the token again.
type zeroReadSource struct {
	data []byte
	zero bool
}

func (z *zeroReadSource) Read(p []byte) (int, error) {
	if z.zero {
		z.zero = false
		return 0, nil
	}
	if len(z.data) == 0 {
		return 0, io.EOF
	}
	p[0] = z.data[0]
	z.data = z.data[1:]
	z.zero = true
	return 1, nil
}

// failSource returns its data and then a source error.
type failSource struct{ data []byte }

func (f *failSource) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, errors.New("lean test: source failed")
	}
	k := copy(p, f.data)
	f.data = f.data[k:]
	return k, nil
}

// TestLeanReadPathMatchesPublic lexes each input twice: through the lean read
// path of a *cursor.Reader, and through ReadLocatedRune on a cursor over the
// same source that hides the lean path. Every token must have the same type,
// text, span (byte offset, rune offset, line and column of its start and of
// its end), Lookahead record and context, every error must be the same, and
// the rules must be shown the same symbols with the same flags.
func TestLeanReadPathMatchesPublic(t *testing.T) {
	inputs := []string{
		"",
		"abc def\n",
		"#a #b\n#c\n\n#\r\n#d",
		"ab",
		`x "a b" "unterminated string that is longer than one batch of runes` + "\n" + `"é世🙂" y`,
		"héllo wörld\n世界\n🙂 --> - -- --->x",
		"\xff\xe2\x28\xa1 \xf0\x9f\x98 a\xc3",
		strings.Repeat("word ", 30) + "\n" + strings.Repeat("é", 40),
		`"` + strings.Repeat("long", 40),
		strings.Repeat("\n", 20) + "#z",
	}

	type source struct {
		name     string
		cursor   func(s string) *cursor.Reader
		opts     []Option
		seekBack bool
	}
	sources := []source{
		{name: "text", cursor: func(s string) *cursor.Reader { return cursor.NewReader(strings.NewReader(s)) }},
		{name: "seek-back", cursor: func(s string) *cursor.Reader { return cursor.NewReader(strings.NewReader(s)) }, seekBack: true},
		{name: "cap4-onebyte", cursor: func(s string) *cursor.Reader {
			return cursor.NewReader(iotest.OneByteReader(strings.NewReader(s)), cursor.WithCapacity(4))
		}},
		{name: "rune", cursor: func(s string) *cursor.Reader {
			return cursor.NewRuneReader(strings.NewReader(s), cursor.WithCapacity(4))
		}},
		{name: "byte", cursor: func(s string) *cursor.Reader {
			return cursor.NewByteReader(strings.NewReader(s), cursor.WithCapacity(4))
		}},
		{name: "retained", cursor: func(s string) *cursor.Reader {
			return cursor.NewReader(strings.NewReader(s), cursor.WithMaxRetained(40))
		}},
		{name: "max-token", cursor: func(s string) *cursor.Reader { return cursor.NewReader(strings.NewReader(s)) }, opts: []Option{WithMaxTokenBytes(7)}},
		{name: "more-input", cursor: func(s string) *cursor.Reader {
			return cursor.NewReader(&moreInputSource{data: []byte(s), n: 3})
		}},
		{name: "zero-read", cursor: func(s string) *cursor.Reader {
			return cursor.NewReader(&zeroReadSource{data: []byte(s)})
		}},
		{name: "zero-read-byte", cursor: func(s string) *cursor.Reader {
			return cursor.NewByteReader(&zeroReadSource{data: []byte(s)})
		}},
		{name: "failing", cursor: func(s string) *cursor.Reader {
			return cursor.NewReader(&failSource{data: []byte(s)}, cursor.WithCapacity(8))
		}},
		{name: "in-text", cursor: func(s string) *cursor.Reader {
			r := cursor.NewReader(strings.NewReader(""))
			if err := r.ResetStart(strings.NewReader(s), cursor.Start{InText: true}); err != nil {
				panic(err)
			}
			return r
		}},
		{name: "after-newline", cursor: func(s string) *cursor.Reader {
			r := cursor.NewReader(strings.NewReader(""))
			if err := r.ResetStart(strings.NewReader(s), cursor.Start{InText: true, LineStart: true}); err != nil {
				panic(err)
			}
			return r
		}},
	}

	for _, src := range sources {
		for i, in := range inputs {
			t.Run(fmt.Sprintf("%s/%d", src.name, i), func(t *testing.T) {
				leanCur := src.cursor(in)
				publicCur := src.cursor(in)

				lean := NewWithCursorOptions(leanCur, src.opts...)
				public := NewWithCursorOptions(publicCursor{publicCur}, src.opts...)
				if lean.lean == nil {
					t.Fatal("the scanner over a *cursor.Reader has no lean read path")
				}
				if public.lean != nil {
					t.Fatal("the scanner over a wrapped cursor has the lean read path")
				}

				var leanLog, publicLog []Symbol
				leanAddRules(lean, &leanLog)
				leanAddRules(public, &publicLog)

				got := leanScan(lean, leanCur, src.seekBack)
				want := leanScan(public, publicCur, src.seekBack)

				if len(got) != len(want) {
					t.Errorf("lean path: %d results, public path: %d", len(got), len(want))
				}
				for k := 0; k < min(len(got), len(want)); k++ {
					if got[k] != want[k] {
						t.Fatalf("result %d:\nlean   %+v\npublic %+v", k, got[k], want[k])
					}
				}
				if len(leanLog) != len(publicLog) {
					t.Fatalf("the rules were shown %d symbols on the lean path, %d on the public path", len(leanLog), len(publicLog))
				}
				for k := range leanLog {
					if leanLog[k] != publicLog[k] {
						t.Fatalf("symbol %d: lean %+v, public %+v", k, leanLog[k], publicLog[k])
					}
				}
				if got, want := leanCur.Cursor(), publicCur.Cursor(); got != want {
					t.Errorf("final cursor: lean %+v, public %+v", got, want)
				}
			})
		}
	}
}

// scriptedSource returns one scripted read per call: the bytes of the step
// and a nil error, and io.EOF after the last step. An empty step is an empty
// read with a nil error.
type scriptedSource struct{ steps []string }

func (s *scriptedSource) Read(p []byte) (int, error) {
	if len(s.steps) == 0 {
		return 0, io.EOF
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return copy(p, step), nil
}

// TestLeanReadPathNoProgressAfterPeek reads "a", an empty read with a nil
// error, "b" and the end of input, with a rule that accepts every symbol
// until the end. The cursor reports the empty read as io.ErrNoProgress after
// one symbol of the token was read. On both paths Next returns that error,
// consumes nothing, and the retry returns the one token "ab".
func TestLeanReadPathNoProgressAfterPeek(t *testing.T) {
	newCursors := map[string]func(io.Reader) *cursor.Reader{
		"text": func(r io.Reader) *cursor.Reader { return cursor.NewReader(r) },
		"byte": func(r io.Reader) *cursor.Reader { return cursor.NewByteReader(r) },
	}
	for name, newCursor := range newCursors {
		for _, lean := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/lean=%v", name, lean), func(t *testing.T) {
				cur := newCursor(&scriptedSource{steps: []string{"a", "", "b"}})
				var sc *Scanner
				if lean {
					sc = NewWithCursor(cur)
				} else {
					sc = NewWithCursor(publicCursor{cur})
				}
				if (sc.lean != nil) != lean {
					t.Fatalf("lean read path = %v, want %v", sc.lean != nil, lean)
				}
				sc.MustAddRule("ANY", leanRunRule(func(rune) bool { return true }))

				if _, err := sc.Next(); !errors.Is(err, io.ErrNoProgress) {
					t.Fatalf("first Next: %v, want io.ErrNoProgress", err)
				}
				if got := cur.Cursor(); !got.IsStart() {
					t.Fatalf("after the failed Next the cursor is at %+v, want the start", got)
				}
				tok, la, err := sc.NextLookahead()
				if err != nil {
					t.Fatalf("retry: %v", err)
				}
				if tok.Text() != "ab" || tok.Span().Start().ByteOffset() != 0 || tok.Span().End().ByteOffset() != 2 {
					t.Fatalf("retry token %q %v, want \"ab\" over bytes 0 to 2", tok.Text(), tok.Span())
				}
				if !la.ReadEnd || la.After != 0 {
					t.Fatalf("retry lookahead %+v, want ReadEnd and After 0", la)
				}
				if _, err := sc.Next(); err != io.EOF {
					t.Fatalf("after the token: %v, want io.EOF", err)
				}
			})
		}
	}
}
