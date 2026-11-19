package modes

import (
	"fmt"
	"sync"

	"github.com/xiam/bytescan"
)

// Stack is a mode state: a stack of mode names. The top of the stack is the
// mode that reads the next token.
//
// A Stack is a value that never changes. A copy is an assignment, and the copy
// stays the same when the scan goes on. Two stacks are compared with Equal, not
// with ==. The zero Stack holds no mode.
type Stack struct {
	top *frame
}

// frame is one mode name on a stack. Frames are never changed after they are
// made, so stacks share their lower frames.
type frame struct {
	name  string
	below *frame
	n     int // the number of names from this frame to the bottom
}

// Top returns the name of the mode at the top of the stack, or "" for the zero
// Stack.
func (s Stack) Top() string {
	if s.top == nil {
		return ""
	}
	return s.top.name
}

// Len returns the number of mode names on the stack.
func (s Stack) Len() int {
	if s.top == nil {
		return 0
	}
	return s.top.n
}

// Equal reports whether s and t hold the same mode names in the same order.
func (s Stack) Equal(t Stack) bool {
	a, b := s.top, t.top
	if s.Len() != t.Len() {
		return false
	}
	for a != b {
		if a.name != b.name {
			return false
		}
		a, b = a.below, b.below
	}
	return true
}

func (s Stack) push(name string) Stack {
	return Stack{top: &frame{name: name, below: s.top, n: s.Len() + 1}}
}

// pop removes the top mode. It leaves a stack of one mode unchanged.
func (s Stack) pop() Stack {
	if s.Len() <= 1 {
		return s
	}
	return Stack{top: s.top.below}
}

// action is what a rule does to the stack after its token.
type action int

const (
	stay action = iota
	push
	pop
)

type modeRule struct {
	typ  bytescan.TokenType
	rule bytescan.Rule
	act  action
	next string // the mode that push pushes
}

// ModeSet is a set of named modes and the mode that a scan starts in. Each
// mode is a list of rules, and each rule says what happens to the mode stack
// after its token: stay, push a named mode, or pop. There is no switching code
// to write. A rule that is in two modes with two actions is declared in each of
// them.
//
// A ModeSet is not safe for concurrent use. Build it before calling Driver.
type ModeSet struct {
	start string
	modes map[string]*Mode
	order []string // mode names in the order of their first Mode call
}

// Mode is one named mode of a ModeSet: a list of rules, each with its action.
// The rules are registered in the order of the calls, as with
// bytescan.Scanner.AddRule.
type Mode struct {
	name  string
	rules []modeRule
}

// NewModeSet returns an empty mode set whose scans start in the mode start.
func NewModeSet(start string) *ModeSet {
	return &ModeSet{start: start, modes: make(map[string]*Mode)}
}

// Mode returns the mode called name, and adds an empty mode with that name on
// the first call.
func (ms *ModeSet) Mode(name string) *Mode {
	if m, ok := ms.modes[name]; ok {
		return m
	}
	m := &Mode{name: name}
	ms.modes[name] = m
	ms.order = append(ms.order, name)
	return m
}

// Rule adds a rule whose token leaves the mode stack unchanged. It returns m.
func (m *Mode) Rule(typ bytescan.TokenType, r bytescan.Rule) *Mode {
	m.rules = append(m.rules, modeRule{typ: typ, rule: r, act: stay})
	return m
}

// Push adds a rule whose token pushes the mode next onto the mode stack. It
// returns m.
func (m *Mode) Push(typ bytescan.TokenType, r bytescan.Rule, next string) *Mode {
	m.rules = append(m.rules, modeRule{typ: typ, rule: r, act: push, next: next})
	return m
}

// Pop adds a rule whose token pops the top mode off the mode stack. A pop
// while only one mode is on the stack does nothing: the scan stays in that
// mode, and no error is returned. It returns m.
func (m *Mode) Pop(typ bytescan.TokenType, r bytescan.Rule) *Mode {
	m.rules = append(m.rules, modeRule{typ: typ, rule: r, act: pop})
	return m
}

// Driver returns a driver over cur that starts in the mode state start. The
// zero Stack starts in the start mode of the set, as the stack that holds only
// that mode.
//
// Driver builds one scanner per mode over cur with bytescan.NewWithCursor. A
// change to the set after the call does not change the driver. Driver returns
// an error when the start mode, a mode on start or a mode that a Push names is
// not in the set, when a Push or a Pop has the type
// bytescan.TokenTypeUnknown, and when a mode has a rule that
// bytescan.Scanner.AddRule rejects, such as two rules with one type. A scanner
// also returns an UNKNOWN token for input that no rule matches, so the driver
// could not tell such a token from a match of the rule.
func (ms *ModeSet) Driver(cur bytescan.Cursor, start Stack) (*StackDriver, error) {
	if start.Len() == 0 {
		if _, ok := ms.modes[ms.start]; !ok {
			return nil, fmt.Errorf("modes: the start mode %q is not in the set", ms.start)
		}
		start = Stack{}.push(ms.start)
	}
	for f := start.top; f != nil; f = f.below {
		if _, ok := ms.modes[f.name]; !ok {
			return nil, fmt.Errorf("modes: the start stack holds the mode %q, which is not in the set", f.name)
		}
	}

	d := &StackDriver{stack: start, modes: make(map[string]*driverMode, len(ms.modes))}
	for _, name := range ms.order {
		m := ms.modes[name]
		dm := &driverMode{
			sc:      bytescan.NewWithCursor(cur),
			actions: make(map[bytescan.TokenType]modeRule, len(m.rules)),
		}
		for _, r := range m.rules {
			if r.act != stay && r.typ == bytescan.TokenTypeUnknown {
				return nil, fmt.Errorf("modes: the rule %q of the mode %q pushes or pops; a scanner gives that type to input that no rule matches", r.typ, name)
			}
			if r.act == push {
				if _, ok := ms.modes[r.next]; !ok {
					return nil, fmt.Errorf("modes: the rule %q of the mode %q pushes the mode %q, which is not in the set", r.typ, name, r.next)
				}
			}
			if err := dm.sc.AddRule(r.typ, r.rule); err != nil {
				return nil, fmt.Errorf("modes: mode %q: %w", name, err)
			}
			dm.actions[r.typ] = r
		}
		d.modes[name] = dm
	}
	return d, nil
}

// StackDriver reads tokens over one cursor in the mode at the top of its mode
// stack, and applies the action of each token's rule to the stack. It
// satisfies bytescan.Source: Next returns a token and a nil error, or a nil
// token and an error, with io.EOF itself at the end of the stream, exactly as
// the scanner of the top mode returns them.
//
// It follows the rules of a mode driver: when the top mode changes, it calls
// Release on the scanner of the outgoing mode, and Next and Stack hold one
// mutex. A StackDriver is safe for concurrent use.
type StackDriver struct {
	mu    sync.Mutex
	stack Stack
	modes map[string]*driverMode
}

type driverMode struct {
	sc      *bytescan.Scanner
	actions map[bytescan.TokenType]modeRule
}

var _ bytescan.Source = (*StackDriver)(nil)

// Stack returns the mode state at the start of the next token.
func (d *StackDriver) Stack() Stack {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stack
}

// Next returns the next token from the scanner of the top mode, then applies
// the action of the token's rule to the mode stack. An error, including
// io.EOF, is returned unchanged and does not change the stack.
func (d *StackDriver) Next() (*bytescan.Token, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	cur := d.modes[d.stack.Top()]
	l, err := cur.sc.Next()
	if err != nil {
		return nil, err
	}
	if r, ok := cur.actions[l.Type()]; ok {
		switch r.act {
		case push:
			d.stack = d.stack.push(r.next)
		case pop:
			d.stack = d.stack.pop()
		}
	}
	if d.modes[d.stack.Top()] != cur {
		cur.sc.Release()
	}
	return l, nil
}
