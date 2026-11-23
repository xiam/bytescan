package bytescan

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode"

	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/leanread"
)

// The tests in this file check the symbols that the lean read path carries
// past a committed token (Scanner.keepCarry, Scanner.takeCarry). Each test
// runs one script twice: over a *cursor.Reader, whose scanner carries the
// symbols, and over the same input through publicCursor, whose scanner reads
// rune by rune and steps the cursor back after each token, as before the
// carry. Every token, Lookahead record, context, error and cursor position
// that the script records must be the same on both runs. Each script also
// checks that carried symbols are present at the step it tests, so the test
// covers the carry and not only the path without it.

// carryRun is one run of a script: a scanner over a cursor, and the log the
// script writes.
type carryRun struct {
	t     *testing.T
	lean  bool
	cur   *cursor.Reader
	sc    *Scanner
	log   []string
	carry int
}

func newCarryRun(t *testing.T, lean bool, cur *cursor.Reader) *carryRun {
	r := &carryRun{t: t, lean: lean, cur: cur}
	r.sc = r.scanner()
	return r
}

// scanner returns a new scanner over the cursor of the run, with the rules of
// carryAddRules, on the lean read path or not.
func (r *carryRun) scanner() *Scanner {
	var sc *Scanner
	if r.lean {
		sc = NewWithCursor(r.cur)
	} else {
		sc = NewWithCursor(publicCursor{r.cur})
	}
	if (sc.lean != nil) != r.lean {
		r.t.Fatalf("lean read path = %v, want %v", sc.lean != nil, r.lean)
	}
	carryAddRules(sc)
	return sc
}

// next calls NextLookahead on sc and logs the result, with the context of the
// token and the cursor after it.
func (r *carryRun) next(sc *Scanner) *Token {
	tok, la, err := sc.NextLookahead()
	if err != nil {
		r.log = append(r.log, fmt.Sprintf("error %v at %+v", err, r.cur.Cursor()))
		return nil
	}
	ctx, cerr := sc.Context(tok, 8, 8)
	if cerr != nil {
		ctx = "error: " + cerr.Error()
	}
	r.log = append(r.log, fmt.Sprintf("%s %q %v %d-%d %+v context %q cursor %+v",
		tok.Type(), tok.Text(), tok.Span(), tok.Span().Start().ByteOffset(), tok.Span().End().ByteOffset(), la, ctx, r.cur.Cursor()))
	return tok
}

// carried reports the symbols that sc carries past its last token: kept, and
// not shown yet.
func carried(sc *Scanner) int {
	if !sc.carryOK {
		return 0
	}
	return len(sc.carry) - sc.carryI + sc.leanN - sc.leanI
}

// wantCarry records that sc carries symbols on the lean run. A script calls it
// just before the step it tests.
func (r *carryRun) wantCarry(sc *Scanner) {
	if !r.lean {
		return
	}
	if carried(sc) == 0 {
		r.t.Fatalf("step %d: the scanner carries no symbol", len(r.log))
	}
	r.carry++
}

func carryAddRules(sc *Scanner) {
	sc.MustAddRule("WORD", leanRunRule(unicode.IsLetter))
	sc.MustAddRule("DIGITS", leanRunRule(unicode.IsDigit))
	sc.MustAddRule("SPACE", leanRunRule(func(r rune) bool { return r == ' ' || r == '\t' }))
	sc.MustAddRule("NL", leanLiteral("\n"))
	sc.MustAddRule("STRING", leanQuoted())
	sc.MustAddRule("HASH", leanHashAtBOL())
}

// runCarryScript runs script on both paths over cursors that newCursor builds,
// and compares the logs.
func runCarryScript(t *testing.T, newCursor func() *cursor.Reader, script func(r *carryRun)) {
	t.Helper()
	lean := newCarryRun(t, true, newCursor())
	public := newCarryRun(t, false, newCursor())
	script(lean)
	script(public)
	if lean.carry == 0 {
		t.Fatal("the script checked no carried symbol")
	}
	if len(lean.log) != len(public.log) {
		t.Errorf("lean path: %d steps, public path: %d", len(lean.log), len(public.log))
	}
	for i := 0; i < min(len(lean.log), len(public.log)); i++ {
		if lean.log[i] != public.log[i] {
			t.Fatalf("step %d:\nlean   %s\npublic %s", i, lean.log[i], public.log[i])
		}
	}
	if got, want := lean.cur.Cursor(), public.cur.Cursor(); got != want {
		t.Errorf("final cursor: lean %+v, public %+v", got, want)
	}
}

const carryInput = "alpha  beta\t12 \"x y\" \"open string é世🙂\n#h 34gamma\n"

func newCarryCursor() *cursor.Reader {
	return cursor.NewReader(strings.NewReader(carryInput))
}

// TestCarryModeSwitchBetweenTokens switches between two scanners on one
// cursor, as a mode driver does, after every second token. With Release on
// the outgoing scanner, the carry is dropped. Without it, the outgoing
// scanner keeps its carry, and the cursor has moved when it comes back, so
// the carry is dropped then. In both cases the incoming scanner reads from
// the end of the last committed token.
func TestCarryModeSwitchBetweenTokens(t *testing.T) {
	for _, release := range []bool{true, false} {
		t.Run(fmt.Sprintf("release=%v", release), func(t *testing.T) {
			runCarryScript(t, newCarryCursor, func(r *carryRun) {
				scanners := []*Scanner{r.sc, r.scanner()}
				active := 0
				for i := 0; i < 40; i++ {
					if i%2 == 0 && i > 0 {
						out := scanners[active]
						if carried(out) > 0 {
							r.wantCarry(out)
						}
						if release {
							out.Release()
							if carried(out) != 0 {
								r.t.Fatalf("the released scanner carries %d symbols", carried(out))
							}
						}
						active = 1 - active
					}
					if r.next(scanners[active]) == nil {
						break
					}
				}
			})
		})
	}
}

// TestCarryCheckpointBetweenTokens takes a checkpoint of the cursor between
// two tokens and reads runes through the cursor. Mostly it resets the
// checkpoint, so the cursor is back at the end of the token; every fourth time
// it commits the checkpoint, so the cursor has moved. A checkpoint is a new
// mark of the cursor, so in both cases the scanner drops the carry and reads
// from the cursor (see TestCarryOlderCheckpointBetweenTokens for why).
func TestCarryCheckpointBetweenTokens(t *testing.T) {
	runCarryScript(t, newCarryCursor, func(r *carryRun) {
		for i := 0; i < 40; i++ {
			if carried(r.sc) > 0 {
				r.wantCarry(r.sc)
			}
			cp := r.cur.Checkpoint()
			for k := 0; k < 3; k++ {
				lr, err := r.cur.ReadLocatedRune()
				r.log = append(r.log, fmt.Sprintf("read %q %v", lr.Rune(), err))
			}
			if i%4 == 3 {
				if err := cp.Commit(); err != nil {
					r.t.Fatal(err)
				}
			} else {
				if err := cp.Reset(); err != nil {
					r.t.Fatal(err)
				}
				if err := cp.Release(); err != nil {
					r.t.Fatal(err)
				}
			}
			if r.next(r.sc) == nil {
				break
			}
		}
	})
}

// TestCarryContextBetweenTokens calls Context for each of the last three
// tokens between two tokens, while the scanner carries symbols. Context does
// not move the cursor, so the carry is used by the next token.
func TestCarryContextBetweenTokens(t *testing.T) {
	runCarryScript(t, newCarryCursor, func(r *carryRun) {
		var last []*Token
		for i := 0; i < 40; i++ {
			if carried(r.sc) > 0 {
				r.wantCarry(r.sc)
			}
			for _, tok := range last {
				for _, n := range []int{0, 3, 100} {
					ctx, err := r.sc.Context(tok, n, n)
					r.log = append(r.log, fmt.Sprintf("context %q %d: %q %v", tok.Text(), n, ctx, err))
				}
			}
			tok := r.next(r.sc)
			if tok == nil {
				break
			}
			last = append(last, tok)
			if len(last) > 3 {
				last = last[1:]
			}
		}
	})
}

// carryMoreInput returns the bytes of each chunk and then ErrMoreInput, and
// io.EOF after the last chunk.
type carryMoreInput struct {
	chunks  []string
	pending bool
}

func (m *carryMoreInput) Read(p []byte) (int, error) {
	if m.pending {
		m.pending = false
		return 0, cursor.ErrMoreInput
	}
	if len(m.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, m.chunks[0])
	m.chunks[0] = m.chunks[0][n:]
	if m.chunks[0] == "" {
		m.chunks = m.chunks[1:]
		m.pending = true
	}
	return n, nil
}

// TestCarryMoreInputAtTokenBoundary returns ErrMoreInput just after the
// symbols that end a token. The token commits and carries them. The next call
// shows them to the rules, which need more input, so it returns ErrMoreInput,
// consumes nothing and drops the carry. The retry lexes the token again from
// its start.
func TestCarryMoreInputAtTokenBoundary(t *testing.T) {
	chunks := []string{"abc  ", "  def 12", "3\t\tx", "\"q é\" ", "z"}
	newCursor := func() *cursor.Reader {
		return cursor.NewReader(&carryMoreInput{chunks: append([]string(nil), chunks...)})
	}
	runCarryScript(t, newCursor, func(r *carryRun) {
		moreInput := 0
		for i := 0; i < 60; i++ {
			had := carried(r.sc)
			tok, la, err := r.sc.NextLookahead()
			if errors.Is(err, ErrMoreInput) {
				if had > 0 {
					// The call that returned ErrMoreInput started with
					// carried symbols.
					r.carry++
				}
				moreInput++
				if r.lean && carried(r.sc) != 0 {
					r.t.Fatalf("after ErrMoreInput the scanner carries %d symbols", carried(r.sc))
				}
				r.log = append(r.log, fmt.Sprintf("more input at %+v", r.cur.Cursor()))
				continue
			}
			if err != nil {
				r.log = append(r.log, fmt.Sprintf("error %v at %+v", err, r.cur.Cursor()))
				break
			}
			r.log = append(r.log, fmt.Sprintf("%s %q %v %+v cursor %+v", tok.Type(), tok.Text(), tok.Span(), la, r.cur.Cursor()))
		}
		if moreInput == 0 {
			r.t.Fatal("no call returned ErrMoreInput")
		}
	})
}

// TestCarryResetStartBetweenTokens resets the cursor onto a new source
// between two tokens, and a second scanner reads the new source up to the
// byte offset where the first scanner's last token ended. The first scanner
// then lexes from that offset of the new source: its carry was decoded from
// the old source, so it is dropped although the offset is the same.
func TestCarryResetStartBetweenTokens(t *testing.T) {
	runCarryScript(t, func() *cursor.Reader {
		return cursor.NewReader(strings.NewReader("abc  def"))
	}, func(r *carryRun) {
		r.next(r.sc) // "abc", read past to a space
		r.wantCarry(r.sc)
		if err := r.cur.ResetStart(strings.NewReader("12#xyz qq"), cursor.Start{}); err != nil {
			r.t.Fatal(err)
		}
		other := r.scanner()
		r.next(other) // "12", which ends at offset 2
		r.next(other) // "#", which ends at offset 3, where "abc" ended
		other.Release()
		for i := 0; i < 5; i++ {
			if r.next(r.sc) == nil {
				break
			}
		}
	})
}

// carryUntilZ accepts first and then any run of symbols that ends with 'z'.
// It reads to the end of input when no 'z' follows, and rejects there.
func carryUntilZ(first rune) Rule {
	var more Rule
	more = func(s Symbol) (Rule, State) {
		switch {
		case s.IsEOF():
			return nil, StateReject
		case s.Rune() == 'z':
			return nil, StateAccept
		}
		return more, StateContinue
	}
	return func(s Symbol) (Rule, State) {
		if s.Rune() == first {
			return more, StateContinue
		}
		return nil, StateReject
	}
}

// TestCarryOlderCheckpointBetweenTokens takes a checkpoint at an earlier
// buffered offset between two tokens and moves the cursor back to the end of
// the token. The cursor is at the same offset, but the oldest retained byte
// moved back, so less input fits the retention limit. The carry is dropped,
// and the next token fails with ErrRetentionExceeded as on the public path.
// The first variant carries an overshoot of 16 symbols; the second carries
// only the runes of the lean window that the rules were not shown.
func TestCarryOlderCheckpointBetweenTokens(t *testing.T) {
	variants := map[string]func(sc *Scanner){
		"overshoot": func(sc *Scanner) {
			sc.MustAddRule("A", leanLiteral("a"))
			sc.MustAddRule("B", leanLiteral("b"))
			sc.MustAddRule("AZ", carryUntilZ('a'))
			sc.MustAddRule("BZ", carryUntilZ('b'))
		},
		"window": func(sc *Scanner) {
			sc.MustAddRule("A", leanLiteral("a"))
			sc.MustAddRule("B", leanLiteral("b"))
			sc.MustAddRule("BX", leanLiteral("bxxxxxxxxxz"))
		},
	}
	for name, addRules := range variants {
		t.Run(name, func(t *testing.T) {
			newCursor := func() *cursor.Reader {
				return cursor.NewReader(strings.NewReader(".........."+"ab"+strings.Repeat("x", 15)), cursor.WithMaxRetained(20))
			}
			runCarryScript(t, newCursor, func(r *carryRun) {
				var sc *Scanner
				if r.lean {
					sc = NewWithCursor(r.cur)
				} else {
					sc = NewWithCursor(publicCursor{r.cur})
				}
				addRules(sc)
				if _, err := r.cur.Seek(10, io.SeekStart); err != nil {
					r.t.Fatal(err)
				}
				r.next(sc) // "a"
				r.wantCarry(sc)
				if _, err := r.cur.Seek(0, io.SeekStart); err != nil {
					r.t.Fatal(err)
				}
				cp := r.cur.Checkpoint()
				if _, err := r.cur.Seek(11, io.SeekStart); err != nil {
					r.t.Fatal(err)
				}
				r.next(sc)
				_ = cp.Release()
			})
		})
	}
}

// countingLean counts the calls of Peek of the lean read path it wraps.
type countingLean struct {
	leanread.Reader
	peeks int
}

func (c *countingLean) Peek(skip int, dst []leanread.Rune) (int, leanread.At, error) {
	c.peeks++
	return c.Reader.Peek(skip, dst)
}

// carryPeeks lexes in with the rules of carryAddRules and returns the tokens
// and the number of Peeks. With drop set, it drops the carry before each call
// of Next.
func carryPeeks(t *testing.T, in string, drop bool) (string, int) {
	t.Helper()
	sc := NewWithCursor(cursor.NewReader(strings.NewReader(in)))
	carryAddRules(sc)
	counter := &countingLean{Reader: sc.lean}
	sc.lean = counter
	var texts []string
	for {
		if drop {
			sc.dropCarry()
		}
		tok, err := sc.Next()
		if err == io.EOF {
			return strings.Join(texts, "|"), counter.peeks
		}
		if err != nil {
			t.Fatal(err)
		}
		texts = append(texts, tok.Text())
	}
}

// TestCarryIsUsed lexes a line of short words, which the lean window holds,
// and counts the Peeks. The tokens after the first use the carry and the rest
// of the lean window, so the scanner peeks once for the runes before the last
// utf8.UTFMax bytes, once for each of those, and once for the end of input.
// When the carry is dropped before every token, each token peeks again.
func TestCarryIsUsed(t *testing.T) {
	in := "ab cd ef\n"
	got, peeks := carryPeeks(t, in, false)
	want, dropped := carryPeeks(t, in, true)
	if got != want {
		t.Fatalf("tokens %q, without the carry %q", got, want)
	}
	if peeks != 5 {
		t.Errorf("%d Peeks with the carry, want 5", peeks)
	}
	if dropped <= peeks {
		t.Errorf("%d Peeks without the carry, %d with it; want more without it", dropped, peeks)
	}
}
