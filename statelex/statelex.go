package statelex

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/modedef"
	"github.com/xiam/bytescan/modes"
)

// Errors of Apply, SetText, Position and Offset. A call that returns one of
// them changes nothing. The returned error matches its sentinel with
// errors.Is.
var (
	// ErrRange is returned by Apply for an edit with Start < 0, End < Start
	// or End > Len(), and by Position and Offset for a place outside the
	// text.
	ErrRange = errors.New("statelex: out of range")

	// ErrSplitRune is returned by Apply for an edit whose Start or End falls
	// inside a valid UTF-8 character of the held text, and by Position for
	// an offset inside a valid UTF-8 character.
	ErrSplitRune = errors.New("statelex: offset splits a UTF-8 character")
)

// Edit says that the old bytes [Start, End) of the held text became Text. An
// insertion has Start == End. A deletion has an empty Text. A replacement has
// both. Text may hold any bytes, invalid UTF-8 included.
type Edit struct {
	Start, End int
	Text       []byte
}

// Insert returns the edit that inserts text at the byte offset at.
func Insert(at int, text []byte) Edit {
	return Edit{Start: at, End: at, Text: text}
}

// Delete returns the edit that deletes the bytes [start, end).
func Delete(start, end int) Edit {
	return Edit{Start: start, End: end}
}

// Token is a token of the held text: its type and its byte range [Start, End)
// in the whole text. It carries no mode state. A token has no identity that
// survives an edit: it is its type and its range.
type Token struct {
	Type       bytescan.TokenType
	Start, End int
}

// Change is what one edit changed in the held tokens:
//
//   - the old bytes [Start, OldEnd) became the new bytes [Start, NewEnd);
//   - Removed lists the old tokens in [Start, OldEnd), with offsets in the old
//     text, and Inserted lists the new tokens in [Start, NewEnd), with offsets
//     in the new text;
//   - every old token from OldEnd on moves by Shift() bytes.
//
// The tokens that did not change at both ends of the re-lexed run are left
// out: a token at either end is left out when the old token and the new token
// have the same type, the same range (moved by Shift() at the end) and the
// same bytes. An empty change has no removed and no inserted token, and its
// Shift() is 0.
//
// A consumer that drops its tokens in [Start, OldEnd), adds Inserted and moves
// every later token by Shift() holds the tokens of the new text.
type Change struct {
	Start, OldEnd, NewEnd int
	Removed, Inserted     []Token
}

// Shift returns NewEnd - OldEnd: how far every old token from OldEnd on moves.
func (c Change) Shift() int {
	return c.NewEnd - c.OldEnd
}

// Lexer holds a text, its tokens and, for each token, the mode state at its
// start and its lookahead record. Apply changes the text and returns what
// changed in the tokens. The held tokens are always the tokens of a scan of
// the whole held text from its start, with the end of the held text as the end
// of input.
//
// A Lexer is not safe for concurrent use.
type Lexer struct {
	text rope
	toks tokTree
	rnd  prng
	work workCounters

	cur      *cursor.Reader
	modes    map[string]*lexMode
	startSt  modes.Stack // the stack that holds only the start mode
	endSt    modes.Stack // the mode state after the last token
	err      error       // the internal error, once there is one
	lastRun  relexRun    // the run of the last edit, for the tests
	testHook func() error
}

// lexMode is the scanner of one mode and the actions of its rules.
type lexMode struct {
	sc      *bytescan.Scanner
	actions map[bytescan.TokenType]modedef.Rule
}

// workCounters count the work of one edit. The tests read them.
type workCounters struct {
	textBytes  int // bytes of the held text that were copied
	ropeNodes  int // nodes of the rope that were visited or changed
	tokens     int // held tokens that were visited, moved or rewritten
	find       int // steps that found the first affected token
	countBytes int // bytes that were read to count newlines and runes
}

// relexRun is the run of tokens that the last edit lexed again.
type relexRun struct {
	restart int  // the restart point, in both texts
	end     int  // the new offset where the run ended
	syncOld int  // the old offset of the sync point, or the old length
	synced  bool // the run ended at a sync point
}

// New returns a lexer that holds an empty text, for the rules of ms.
//
// New returns the error of ms.Driver when the definitions cannot be lexed. The
// lexer builds one scanner per mode, over one cursor, and drives them itself:
// the mode at the top of the mode state reads the next token, and the action
// of the token's rule changes the state. A change to ms after the call does
// not change the lexer.
func New(ms *modes.ModeSet) (*Lexer, error) {
	if ms == nil {
		return nil, errors.New("statelex: nil mode set")
	}
	if _, err := ms.Driver(cursor.New(strings.NewReader("")), modes.Stack{}); err != nil {
		return nil, err
	}
	set := modedef.ReadSet(ms)

	lx := &Lexer{
		rnd:   prng{s: 0x9e3779b97f4a7c15},
		cur:   cursor.New(strings.NewReader("")),
		modes: make(map[string]*lexMode, len(set.Modes)),
	}
	lx.text = rope{rnd: &lx.rnd, work: &lx.work}
	lx.toks = tokTree{rnd: &lx.rnd, work: &lx.work}
	for _, m := range set.Modes {
		lm := &lexMode{
			sc:      bytescan.NewWithCursor(lx.cur),
			actions: make(map[bytescan.TokenType]modedef.Rule, len(m.Rules)),
		}
		for _, r := range m.Rules {
			if err := lm.sc.AddRule(r.Type, r.Rule); err != nil {
				return nil, fmt.Errorf("modes: mode %q: %w", m.Name, err)
			}
			lm.actions[r.Type] = r
		}
		lx.modes[m.Name] = lm
	}
	lx.startSt = modedef.PushMode(modes.Stack{}, set.Start).(modes.Stack)
	lx.endSt = lx.startSt
	return lx, nil
}

// Len returns the number of bytes of the held text.
func (lx *Lexer) Len() int {
	return lx.text.len()
}

// Apply applies one edit to the held text, lexes again the tokens that the
// edit can change, and returns the change.
//
// Apply checks the edit before any work. It returns an error that matches
// ErrRange when Start < 0, End < Start or End > Len(), and an error that
// matches ErrSplitRune when Start or End falls inside a valid UTF-8 character
// of the held text. An offset next to a byte that is not part of a valid
// character is a character boundary. A refused edit changes nothing.
//
// An error that matches bytescan.ErrInternal reports a broken invariant of
// the engine during the re-lex. After it, the held text and the held tokens
// may not agree, and every later Apply and SetText returns the same error.
func (lx *Lexer) Apply(e Edit) (Change, error) {
	if lx.err != nil {
		return Change{}, lx.err
	}
	n := lx.text.len()
	if e.Start < 0 || e.End < e.Start || e.End > n {
		return Change{}, fmt.Errorf("%w: [%d,%d) in a text of %d bytes", ErrRange, e.Start, e.End, n)
	}
	for _, off := range [2]int{e.Start, e.End} {
		if lx.splitsChar(off) {
			return Change{}, fmt.Errorf("%w: offset %d", ErrSplitRune, off)
		}
	}
	return lx.apply(e.Start, e.End, e.Text)
}

// SetText replaces the held text with text, as one edit. It drops the common
// start and the common end of the held text and text, widens the middle to
// character boundaries of the held text, and applies one edit for the middle.
// So it never returns ErrSplitRune. It reads the whole text, so its cost grows
// with the size of the text. The re-lex after it is the one of the equal
// Apply.
func (lx *Lexer) SetText(text []byte) (Change, error) {
	if lx.err != nil {
		return Change{}, lx.err
	}
	oldLen := lx.text.len()
	pre := lx.commonStart(text)
	suf := lx.commonEnd(text, min(oldLen, len(text))-pre)
	start, end := pre, oldLen-suf
	if k, _, inside := lx.charAround(start); inside {
		start = k
	}
	if k, size, inside := lx.charAround(end); inside {
		end = k + size
	}
	return lx.apply(start, end, text[start:len(text)-(oldLen-end)])
}

// commonStart returns the length of the common start of the held text and
// text. It compares the chunks of the rope with text in place, one chunk at a
// time, and stops at the first chunk that differs.
func (lx *Lexer) commonStart(text []byte) int {
	it := lx.text.iter(0)
	pos := 0
	for pos < len(text) {
		c := it.next()
		if c == nil {
			break
		}
		c = c[:min(len(c), len(text)-pos)]
		t := text[pos : pos+len(c)]
		if !bytes.Equal(c, t) {
			i := 0
			for c[i] == t[i] {
				i++
			}
			return pos + i
		}
		pos += len(c)
	}
	return pos
}

// commonEnd returns the length of the common end of the held text and text,
// at most m. The last m bytes of both are compared from their start, one
// chunk of the rope at a time, and the common end follows the last byte that
// differs.
func (lx *Lexer) commonEnd(text []byte, m int) int {
	if m <= 0 {
		return 0
	}
	it := lx.text.iter(lx.text.len() - m)
	text = text[len(text)-m:]
	last := -1 // the last byte that differs, in the last m bytes
	pos := 0
	for pos < m {
		c := it.next()
		if c == nil {
			break
		}
		c = c[:min(len(c), m-pos)]
		t := text[pos : pos+len(c)]
		if !bytes.Equal(c, t) {
			i := len(c) - 1
			for c[i] == t[i] {
				i--
			}
			last = pos + i
		}
		pos += len(c)
	}
	return m - 1 - last
}

// charAround is charAt for the held text: it reads only the bytes next to
// off, as splitsChar does.
func (lx *Lexer) charAround(off int) (start, size int, inside bool) {
	lo := max(0, off-(utf8.UTFMax-1))
	hi := min(lx.text.len(), off+utf8.UTFMax-1)
	if lo >= off || off >= hi {
		return 0, 0, false
	}
	k, size, inside := charAt(lx.text.read(lo, hi), off-lo)
	return lo + k, size, inside
}

// splitsChar reports whether off falls inside a valid UTF-8 character of the
// held text.
func (lx *Lexer) splitsChar(off int) bool {
	lo := max(0, off-(utf8.UTFMax-1))
	hi := min(lx.text.len(), off+utf8.UTFMax-1)
	if lo >= off || off >= hi {
		return false
	}
	_, _, inside := charAt(lx.text.read(lo, hi), off-lo)
	return inside
}

// charAt reports the valid UTF-8 character of b that holds the offset off
// inside it, not at its start: its start and its size. The decoder reads a
// byte that does not start a valid character as one symbol of one byte, so a
// valid multi-byte character always starts at its first byte, and the search
// looks only at the three bytes before off.
func charAt(b []byte, off int) (start, size int, inside bool) {
	for k := max(0, off-(utf8.UTFMax-1)); k < off; k++ {
		if b[k] < utf8.RuneSelf || !utf8.RuneStart(b[k]) {
			continue
		}
		r, n := utf8.DecodeRune(b[k:])
		if (r != utf8.RuneError || n > 1) && k+n > off {
			return k, n, true
		}
	}
	return 0, 0, false
}

// fail records an internal error. Every later call that can fail returns it.
func (lx *Lexer) fail(format string, args ...any) error {
	lx.err = fmt.Errorf("%w: statelex: "+format, append([]any{bytescan.ErrInternal}, args...)...)
	return lx.err
}
