// Package modes runs several scanners in turn over one shared cursor: a mode
// driver.
//
// A lexical mode is a rule set that applies only in some context, such as the
// inside of a string, a comment or a long bracket. The engine holds no state
// from one token to the next, so a mode cannot live in a rule. It lives in a
// layer above Next: one scanner per mode, all built over the same cursor with
// bytescan.NewWithCursor or bytescan.NewWithCursorOptions, and a driver that
// picks which scanner reads the next token.
//
// A mode driver follows three rules. Without them the output can still look
// right, but the cursor retains input without bound, or tokens come out wrong
// without an error:
//
//  1. Call Release on the outgoing scanner at every switch. A scanner keeps
//     checkpoints on the cursor for its last tokens; while it is idle, those
//     checkpoints pin all the input that the other scanners read after them.
//  2. Build one scanner per mode, per terminator or per level, and reuse it.
//     Never build a scanner per token: that is much slower, and a dropped scanner
//     that was not released keeps its checkpoints forever.
//  3. Never call Next on two scanners that share one cursor at the same time.
//     The engine locks Next per scanner, not per cursor.
//
// Driver applies rules 1 and 3: Use releases the outgoing scanner, and Next and
// Use hold one mutex. Cache applies rule 2 for drivers that build a scanner per
// key, such as a delimiter, a terminator or a bracket level.
//
// A Driver decides only which scanner is active. The state that picks the next
// mode, such as the position in a line or the level of a bracket, belongs to
// the code that uses the driver: a SwitchFunc reads it and returns the next
// scanner.
//
// A ModeSet holds the mode state itself, as a Stack of mode names. Each rule of
// a mode says what happens to the stack after its token: stay, push a named
// mode, or pop. Its StackDriver applies these actions and follows rules 1 and
// 3, and it builds one scanner per mode, which follows rule 2. The state at the
// start of each token is a plain value: it can be saved with an assignment,
// compared with Equal, and given to ModeSet.Driver to start a scan at that
// token.
package modes

import (
	"sync"

	"github.com/xiam/bytescan"
)

// SwitchFunc picks the scanner for the next token, after the active scanner
// returned the token l. It returns nil, or active, to stay in the current
// mode.
//
// The driver calls it from Next, with its mutex held, once for each token the
// active scanner returns, and never after an error. It must not call Next, Use or
// Active on the same driver.
type SwitchFunc func(active *bytescan.Scanner, l *bytescan.Token) *bytescan.Scanner

// Option configures a Driver.
type Option func(*Driver)

// WithSwitch sets the function that Next calls after each token to pick the
// next scanner. Without it the driver switches only when Use is called.
func WithSwitch(fn SwitchFunc) Option {
	return func(d *Driver) {
		d.next = fn
	}
}

// Driver reads tokens from one active scanner at a time, out of several scanners
// that share one cursor. It satisfies bytescan.Source: Next returns a token
// and a nil error, or a nil token and an error, with io.EOF itself at the end
// of the stream, exactly as the active scanner returns them.
//
// A Driver is safe for concurrent use. Next and Use hold one mutex, so no two
// scanners on the cursor run at the same time, and a switch never happens in the
// middle of a token. A switch between scanners that already lexed allocates
// nothing.
type Driver struct {
	mu     sync.Mutex
	cur    bytescan.Cursor
	active *bytescan.Scanner
	next   SwitchFunc
}

var _ bytescan.Source = (*Driver)(nil)

// New returns a driver over cur with initial as the active scanner. Every scanner
// that the driver uses must be built over cur. New panics if initial is nil.
func New(cur bytescan.Cursor, initial *bytescan.Scanner, opts ...Option) *Driver {
	if initial == nil {
		panic("modes: New with a nil scanner")
	}
	d := &Driver{cur: cur, active: initial}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Cursor returns the cursor that the scanners of the driver share.
func (d *Driver) Cursor() bytescan.Cursor {
	return d.cur
}

// Active returns the scanner that reads the next token.
func (d *Driver) Active() *bytescan.Scanner {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

// Use makes lx the active scanner. When lx is another scanner than the active one,
// Use first calls Release on the active scanner, so that it pins no input while
// it is idle. When lx is already active, Use does nothing. Use panics if lx is
// nil.
//
// Use waits for a Next call in progress to return, so it switches between two
// tokens. A driver that picks the mode before a token, rather than after
// one, calls Use and then Next.
func (d *Driver) Use(lx *bytescan.Scanner) {
	if lx == nil {
		panic("modes: Use with a nil scanner")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.use(lx)
}

func (d *Driver) use(lx *bytescan.Scanner) {
	if lx == d.active {
		return
	}
	d.active.Release()
	d.active = lx
}

// Next returns the next token from the active scanner. After the token it calls
// the SwitchFunc, if any, and switches to the scanner it returns, as Use does.
// An error from the active scanner, including io.EOF, is returned unchanged and
// does not switch the mode.
func (d *Driver) Next() (*bytescan.Token, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	l, err := d.active.Next()
	if err != nil {
		return nil, err
	}
	if d.next != nil {
		if lx := d.next(d.active, l); lx != nil {
			d.use(lx)
		}
	}
	return l, nil
}

// Cache builds one scanner per key on first use and returns the same scanner for
// that key afterwards. It is for drivers that need a scanner per delimiter,
// terminator or level, where the set of keys is not known in advance.
//
// A Cache never drops a scanner, so the number of keys bounds the number of
// scanners. A Cache is safe for concurrent use.
type Cache[K comparable] struct {
	mu       sync.Mutex
	cur      bytescan.Cursor
	build    func(cur bytescan.Cursor, key K) *bytescan.Scanner
	scanners map[K]*bytescan.Scanner
}

// NewCache returns an empty cache that builds the scanner for a key with
// build(cur, key). build must return a new scanner over cur.
func NewCache[K comparable](cur bytescan.Cursor, build func(cur bytescan.Cursor, key K) *bytescan.Scanner) *Cache[K] {
	return &Cache[K]{
		cur:      cur,
		build:    build,
		scanners: make(map[K]*bytescan.Scanner),
	}
}

// Get returns the scanner for key, and builds it on the first call for that
// key. Get panics if build returns nil.
func (c *Cache[K]) Get(key K) *bytescan.Scanner {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lx, ok := c.scanners[key]; ok {
		return lx
	}
	lx := c.build(c.cur, key)
	if lx == nil {
		panic("modes: Cache build returned a nil scanner")
	}
	c.scanners[key] = lx
	return lx
}

// Len returns the number of scanners the cache has built.
func (c *Cache[K]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.scanners)
}
