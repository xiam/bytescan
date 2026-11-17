// Package bytescan reads a stream of data and classifies what it finds, for
// text and binary data alike. Each kind of data is described by a Rule, and
// the scanner returns the stream as tokens: each piece of the input, the
// TokenType of the rule that matched it, and its position. It does not parse:
// it checks no grammar, builds no tree and decodes no value.
//
// For text, the cursor decodes UTF-8 and each Symbol is a rune. For binary
// data, the byte adapter in package bytesource reads each byte as one Symbol,
// U+0000 to U+00FF, so the same rules and the same scanner classify bytes; the
// rules of package rules/octet match byte values.
//
// A Rule is a state-transition function that processes input one Symbol at a
// time. The scanner runs all defined rules in parallel, reading ahead until all
// rules have either rejected the input or terminated. It then selects the
// longest valid match as the next token.
// If multiple rules match the same longest text, the one added first is chosen.
//
// # Layering
//
// The scanner owns lexical policy: rule state transitions, parallel rule
// evaluation, maximal-munch selection, registration-order tie breaking, the
// UNKNOWN fallback, and token construction. It owns no stream mechanics.
// Reading, buffering, positions, and replay come from a positioned cursor
// (github.com/xiam/bytescan/cursor): the cursor decodes UTF-8, or reads bytes
// through bytesource.NewCursor, tracks the logical position, retains the
// input a checkpoint needs, and restores a marked position exactly.
//
// Anything that depends on earlier tokens lives in a layer above Next, as a
// Source: a filter such as Skip, a mode driver (package modes), or a driver
// that reads fields whose length is stored in the data (bytesource.Fields).
//
// A token is assembled speculatively and then committed. The scanner marks a
// checkpoint at the start of a token, reads located runes until the rules
// decide, and then steps the cursor back over the speculative overshoot so it
// sits at the end of the winning match. Token positions come from that
// committed range — the checkpoint's position and the cursor after the step
// back — never from where speculation stopped.
//
// The scanner keeps a fixed pool of checkpoint slots — one per token the context
// window retains, plus the in-flight token. When the cursor can re-arm a
// released checkpoint (the Remark capability of cursor.Reader), a
// committed token's slot is re-armed in place for the next token, so
// steady-state tokenization allocates no checkpoint. A cursor that cannot
// re-arm one (see NewWithCursor) falls back to allocating a checkpoint per
// token. The context window, eviction order, and returned-error rollback are
// unchanged by the reuse.
//
// # Memory and the hard bound
//
// A scanner holds memory in proportion to its longest open token. While a token
// is assembled, the rule stack keeps every symbol read for it (8 bytes per
// rune) and the cursor retains its input. For ASCII input that is about 10
// bytes of live heap per input byte, measured on one open token of 4 MiB; the
// peak while the stack grows is about 3 times that. A token stays open while
// any rule keeps reading, so one token that never settles holds the rest of
// the input.
//
// WithMaxTokenBytes, passed to NewWithOptions or, for a scanner over a shared
// cursor, to NewWithCursorOptions, is the only hard bound. It is fail-stop: a
// token that would exceed it makes Next return an error wrapping
// cursor.ErrRetentionExceeded, and the scanner makes no progress past that
// token. There is no default bound.
//
// A rule whose read-ahead can be rejected (base.Quoted, base.QuotedMultiline,
// base.Raw, base.RawMultiline, base.BlockComment, base.Decimal and base.Float)
// can make the scanner read a long run more than once: the rule reads to the end
// of the run and rejects it, a shorter token commits, and the next token reads
// the rest of the run again.
package bytescan

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/xiam/bytescan/cursor"
)

const (
	// RuneEOF represents the end-of-file marker as a rune.
	RuneEOF = rune(-1)
)

// Cursor is the stream surface the scanner needs from a positioned cursor.
// *cursor.Reader implements it. The interface is exported so a caller
// can supply its own cursor and so the scanner's dependency on the cursor stays
// explicit.
type Cursor interface {
	// ReadLocatedRune reads the next rune and reports the position it started
	// at.
	ReadLocatedRune() (cursor.LocatedRune, error)
	// Cursor returns the logical position of the next read.
	Cursor() cursor.Pos
	// Checkpoint marks the current logical position for replay.
	Checkpoint() *cursor.Checkpoint
	// Seek moves the logical cursor inside the retained input.
	Seek(offset int64, whence int) (int64, error)
}

// contextualCursor is implemented by cursors that can expose the text they
// retain. It backs Scanner.Context and is optional: everything else works with a
// plain Cursor.
type contextualCursor interface {
	Context(s cursor.Span, before, after int) (string, error)
}

// remarkableCursor is implemented by cursors that can re-arm a released
// checkpoint in place instead of allocating a new one. It backs the
// allocation-free mark reuse: a scanner holding a fixed set of mark slots arms
// each slot once with Checkpoint and then re-arms the same values, so the
// allocation cost is bounded by the number of slots rather than the number of
// tokens. It is optional, following contextualCursor: a cursor without it
// keeps the existing allocating behavior.
type remarkableCursor interface {
	Remark(*cursor.Checkpoint) error
}

// Option configures a scanner created by NewWithOptions or NewWithCursorOptions.
type Option func(*config)

type config struct {
	maxTokenBytes int
}

// WithMaxTokenBytes bounds how many source bytes the scanner may retain while it
// assembles one token. Zero, the default, means no bound.
//
// The bound covers maximal-munch look-ahead: a rule that keeps matching forces
// the scanner to retain everything it has read for the token so far. When a token
// would exceed the bound, Next returns an error wrapping
// cursor.ErrRetentionExceeded, and the scanner makes no partial progress.
//
// The bound is per scanner. NewWithOptions also sizes the retention budget of
// the cursor it builds, so that every token within the bound fits. A scanner
// created by NewWithCursorOptions does not change the cursor: the cursor's
// retention budget (cursor.WithMaxRetained) is set by the caller and is
// shared by every scanner on that cursor.
func WithMaxTokenBytes(n int) Option {
	return func(c *config) {
		if n < 0 {
			n = 0
		}
		c.maxTokenBytes = n
	}
}

// markWindow is how many committed tokens keep their retained input. It is
// what lets Context read a token together with the input that precedes it: a
// single mark would floor the retained region at the token itself, so `before`
// could never return anything.
const markWindow = 3

// markSlots is the total number of reusable checkpoint slots the scanner keeps:
// the markWindow committed tokens plus the one token being assembled. It is a
// fixed bound, so a cursor with the Remark capability re-arms the same values
// in place and the checkpoint allocation cost is bounded by markSlots rather
// than growing with the number of tokens.
const markSlots = markWindow + 1

// Scanner orchestrates the tokenization of an input stream according to a
// set of user-defined rules. It reads through a positioned cursor and runs the
// rule processing engine over the located runes it reads.
type Scanner struct {
	cursor Cursor

	// marks holds the reusable checkpoint slots: the markWindow committed
	// tokens plus the one token being assembled. A cursor with the Remark
	// capability re-arms the same values in place, so the checkpoint allocation
	// cost is bounded by markSlots rather than the number of tokens; a cursor
	// without it allocates a fresh checkpoint per token and the slots hold those
	// values until a later token evicts them. marksNext is the in-flight slot:
	// the token currently being assembled is armed there. A mark is released
	// only when a later token commits and evicts its slot, so a failed Next or
	// an EOF probe leaves the previous token's context readable.
	marks     [markSlots]*cursor.Checkpoint
	marksNext int

	// canRemark reports whether the cursor can re-arm a released checkpoint in
	// place (the Remark capability). It is set once at construction, because
	// the cursor is fixed for the life of the scanner.
	canRemark bool

	// maxTokenBytes is the per-token byte bound passed to WithMaxTokenBytes, or
	// zero for unbounded. It is enforced by the scanner here rather than delegated
	// to the cursor's retention budget, because the cursor must also retain the
	// previous token while a token is assembled.
	maxTokenBytes int

	mu sync.Mutex

	rules    []TokenType
	rulesMap map[TokenType]Rule
	rulesMu  sync.RWMutex

	processor *rulesProcessor
}

// New creates a Scanner that reads from rr through a cursor it builds and
// owns. It is the common case: one scanner over one input, with no bound on the
// size of a token. Use NewWithOptions to set a bound.
//
// A source that also implements io.Reader is read as a byte stream by the
// cursor, so no extra UTF-8 decoding layer is introduced for callers that
// already hold one. A source that only implements io.RuneReader is read rune by
// rune, and the cursor stores the UTF-8 encoding of each rune so positions stay
// meaningful.
func New(rr io.RuneReader) *Scanner {
	return NewWithOptions(rr)
}

// NewWithOptions creates a Scanner that reads from rr, as New does, and
// applies opts. Use it to bound the size of a token with WithMaxTokenBytes:
// the cursor it builds gets a retention budget that fits every token within
// the bound.
func NewWithOptions(rr io.RuneReader, opts ...Option) *Scanner {
	cfg := newConfig(opts)

	// The cursor must retain the committed tokens in the context window plus the
	// token being assembled, so its budget covers markWindow+1 tokens. The
	// per-token bound itself is enforced by the scanner.
	retained := 0
	if cfg.maxTokenBytes > 0 {
		retained = (markWindow + 1) * cfg.maxTokenBytes
	}

	var cur Cursor
	if r, ok := rr.(io.Reader); ok {
		cur = cursor.NewReader(r, cursor.WithMaxRetained(retained))
	} else {
		cur = cursor.NewRuneReader(rr, cursor.WithMaxRetained(retained))
	}

	return newWithCursorConfig(cur, cfg)
}

// NewWithCursor creates a Scanner over a positioned cursor the caller
// already owns. Use it to share one cursor between several consumers, such as
// one scanner per mode, or to configure the cursor's retention policy directly.
//
// The scanner has no per-token byte bound; use NewWithCursorOptions to set one.
// The cursor's retention budget (cursor.WithMaxRetained) is shared by
// every scanner on the cursor.
func NewWithCursor(c Cursor) *Scanner {
	return newWithCursorConfig(c, &config{})
}

// NewWithCursorOptions creates a Scanner over a cursor the caller owns, as
// NewWithCursor does, and applies opts.
//
// WithMaxTokenBytes bounds each token of this scanner, with the same semantics
// as in NewWithOptions. The bound is per scanner, so several scanners on one
// cursor can each have their own. The scanner does not change the cursor: the
// cursor's retention budget (cursor.WithMaxRetained) is set by the caller
// and is shared by every scanner on the cursor. A budget smaller than the
// scanner's needs makes Next fail with cursor.ErrRetentionExceeded before
// the per-token bound is reached.
func NewWithCursorOptions(c Cursor, opts ...Option) *Scanner {
	return newWithCursorConfig(c, newConfig(opts))
}

func newWithCursorConfig(c Cursor, cfg *config) *Scanner {
	sc := &Scanner{
		cursor:    c,
		rules:     []TokenType{},
		rulesMap:  map[TokenType]Rule{},
		processor: nil,
	}
	if _, ok := c.(remarkableCursor); ok {
		sc.canRemark = true
	}
	sc.maxTokenBytes = cfg.maxTokenBytes
	return sc
}

func newConfig(opts []Option) *config {
	cfg := &config{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

// AddRule registers a new tokenizing rule with the scanner.
// This method is not safe for concurrent use with Next(). All rules should be
// added before tokenization begins.
//
// It returns an error matching ErrDuplicateRule when lexType already has a
// rule, ErrEmptyRuleType when lexType is empty, and ErrNilRule when lexRule is
// nil. A rejected call registers nothing.
func (sc *Scanner) AddRule(lexType TokenType, lexRule Rule) error {
	sc.rulesMu.Lock()
	defer sc.rulesMu.Unlock()

	if _, ok := sc.rulesMap[lexType]; ok {
		return fmt.Errorf("%w: rule %q already exists", ErrDuplicateRule, lexType)
	}
	if lexType == "" {
		return ErrEmptyRuleType
	}
	if lexRule == nil {
		return ErrNilRule
	}

	sc.rulesMap[lexType] = lexRule
	sc.rules = append(sc.rules, lexType)
	sc.processor = nil // Invalidate processor so it's rebuilt with the new rule.
	return nil
}

// MustAddRule is like AddRule but panics if the rule cannot be added.
func (sc *Scanner) MustAddRule(lexType TokenType, lexRule Rule) {
	if err := sc.AddRule(lexType, lexRule); err != nil {
		panic(fmt.Sprintf("MustAddRule: %v", err))
	}
}

// Next reads from the input and returns the next recognized Token.
//
// Next returns a token and a nil error, or a nil token and an error. Input
// that no rule matches is not an error: it becomes a single-rune token of
// type TokenTypeUnknown. A byte that is not valid UTF-8 is not an error
// either: it is read as the rune U+FFFD, and the token's span keeps the
// position of the original byte.
//
// At the end of input Next returns io.EOF itself, not a wrapped error, so
// err == io.EOF holds; every later call returns io.EOF again. A rule that is
// still waiting for input at the end of input is retired, not reported: only
// the matches it recorded earlier count.
//
// Any other error leaves the input where it was: a failed call consumes
// nothing, and a retry starts the same token over. Such an error matches
// ErrNoRules when no rule is registered, ErrTokenTooLarge together with
// cursor.ErrRetentionExceeded when a token would exceed WithMaxTokenBytes,
// or ErrInternal when an engine invariant breaks. An error from the reader or
// the cursor, such as a cursor's own cursor.ErrRetentionExceeded, is
// wrapped so that errors.Is matches it.
//
// This method is safe for concurrent use by multiple goroutines.
func (sc *Scanner) Next() (*Token, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	return sc.nextToken()
}

// Context returns the source text around tok, extended by before bytes on the
// left and after bytes on the right and clamped to the retained input.
//
// Context is a diagnostic aid. The scanner retains the input of the tokens in
// its context window, so `before` can reach back across the tokens that
// precede tok; anything beyond the window is clamped, not reported as an error.
// A token is released once enough later tokens have committed to push it out
// of the window, and a call that commits nothing — a failed Next, or the EOF
// probe — releases nothing. Once tok is no longer retained, Context returns
// cursor.ErrPositionOutOfBuffer, unchanged.
//
// On a byte-mode cursor, such as one from bytesource.NewCursor, the result
// holds the raw input bytes, not the runes of Token.Text.
//
// A nil tok returns ErrNilToken, and a cursor that does not expose retained
// text returns ErrContextUnsupported. Any other error from the cursor is
// returned unchanged.
func (sc *Scanner) Context(tok *Token, before, after int) (string, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if tok == nil {
		return "", ErrNilToken
	}

	contextual, ok := sc.cursor.(contextualCursor)
	if !ok {
		return "", ErrContextUnsupported
	}

	return contextual.Context(tok.span, before, after)
}

// Release drops the retained input this scanner holds for its context window,
// including the checkpoint of the most recently committed token. It does not
// close the underlying reader, and the scanner remains usable.
//
// Call it before handing a shared cursor to another consumer: while the scanner
// holds checkpoints, a cursor configured with a finite retention budget keeps
// that input reserved, and the other consumer sees
// cursor.ErrRetentionExceeded sooner than it expects. After Release,
// Context returns cursor.ErrPositionOutOfBuffer until the next token
// commits. Release cannot fail, and Next works as before after it.
//
// Release keeps the released checkpoint values in their slots, so a cursor
// with the Remark capability re-arms them in place on the next Next: a mode
// driver that calls Release on every switch allocates no checkpoint for it.
func (sc *Scanner) Release() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	for _, c := range sc.marks {
		if c != nil {
			_ = c.Release()
		}
	}

	sc.marksNext = 0
}

func (sc *Scanner) getProcessor() (*rulesProcessor, error) {
	// Fast path: Check for existing processor with a read lock.
	sc.rulesMu.RLock()
	p := sc.processor
	sc.rulesMu.RUnlock()

	if p != nil {
		p.Reset()
		return p, nil
	}

	// Slow path: Acquire a write lock to create the processor.
	sc.rulesMu.Lock()
	defer sc.rulesMu.Unlock()

	// Double-check in case another goroutine created it while we were waiting for the lock.
	if sc.processor != nil {
		sc.processor.Reset()
		return sc.processor, nil
	}

	if len(sc.rules) == 0 {
		return nil, ErrNoRules
	}
	sc.processor = newRulesProcessor(sc.rules, sc.rulesMap)
	return sc.processor, nil
}

// readSymbol reads the next symbol from the cursor.
//
// It reports isEOF for the synthetic end-of-input symbol, which carries RuneEOF
// and the FlagEOF flag and is not part of the input. The positional flags come
// from the located rune and the cursor position, so a symbol read again during
// replay gets the same flags.
func (sc *Scanner) readSymbol() (sym Symbol, isEOF bool, err error) {
	lr, err := sc.cursor.ReadLocatedRune()
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return Symbol{}, false, err
		}

		at := sc.cursor.Cursor()

		return NewSymbol(RuneEOF, symbolFlags(at, RuneEOF, true)), true, nil
	}

	return Symbol{r: lr.Rune(), flags: uint8(symbolFlags(lr.Pos(), lr.Rune(), false)), size: uint8(lr.Size())}, false, nil
}

// symbolFlags derives a symbol's positional flags from the position it was read
// at. BOF is the start of the stream, BOL is rune column zero, and EOL is a
// newline; EOF is supplied by the caller.
func symbolFlags(at cursor.Pos, r rune, eof bool) uint {
	flags := uint(FlagNone)

	if at.IsStart() {
		flags |= FlagBOF
	}
	if at.Column() == 0 {
		flags |= FlagBOL
	}
	if r == '\n' {
		flags |= FlagEOL
	}
	if eof {
		flags |= FlagEOF
	}

	return flags
}

// remarkSlot arms the reusable checkpoint in slot idx at the cursor's current
// position and returns it. It is the allocation-free half of the mark lifecycle:
// a cursor with the Remark capability re-arms the slot's released value in place
// (Remark never allocates), so after the first pass over the ring the checkpoint
// cost is bounded by markSlots. A nil slot — the first time a slot is armed —
// and a cursor without the capability allocate a fresh Checkpoint and store it
// in the slot, preserving the existing allocating behavior.
//
// The caller must pass the in-flight slot (marksNext): a slot is re-armed only
// when it is released, which is exactly the state of the in-flight slot when the
// previous token that used it has committed. A Remark that is somehow refused
// (an active value, which the invariant above rules out) falls back to a fresh
// Checkpoint and replaces the slot's stale value, so the token still arms a
// valid mark.
func (sc *Scanner) remarkSlot(idx int) *cursor.Checkpoint {
	if sc.canRemark {
		if c := sc.marks[idx]; c != nil {
			if err := sc.cursor.(remarkableCursor).Remark(c); err == nil {
				return c
			}
			// Remark refused: fall through to a fresh checkpoint and replace
			// the slot's value below.
		}
	}

	c := sc.cursor.Checkpoint()
	sc.marks[idx] = c
	return c
}

func (sc *Scanner) nextToken() (*Token, error) {
	processor, err := sc.getProcessor()
	if err != nil {
		return nil, err
	}

	// Mark the start of the token in the in-flight slot: the cursor retains every
	// byte from here until the token is committed, which is what makes the
	// rewind below exact and what WithMaxTokenBytes bounds.
	mark := sc.remarkSlot(sc.marksNext)

	tokenBytes := 0

	// abandon ends the token without committing it: the cursor is rewound to the
	// token start so a failed Next consumes nothing and a retry starts over. The
	// previous token's checkpoint is deliberately left active, so its context
	// survives a failed call and an EOF probe.
	abandon := func(err error) (*Token, error) {
		_ = mark.Reset()
		_ = mark.Release()
		return nil, err
	}

	for {
		sym, isEOF, err := sc.readSymbol()
		if err != nil {
			return abandon(fmt.Errorf("bytescan: ReadRune: %w", err))
		}
		if !isEOF {
			tokenBytes += sym.Size()

			if sc.maxTokenBytes > 0 && tokenBytes > sc.maxTokenBytes {
				return abandon(fmt.Errorf("%w: token exceeds %d bytes: %w", ErrTokenTooLarge, sc.maxTokenBytes, cursor.ErrRetentionExceeded))
			}
		}

		typ, textLen := processor.Process(sym)

		// textLen < 0 means a rule is still active and needs more input.
		if textLen < 0 {
			if isEOF {
				// Defensive, unreachable: processSymbol retires every rule
				// after the EOF symbol, so Process always decides on it.
				// The check stays so that a broken invariant fails the token
				// instead of reading past the end of input forever.
				return abandon(fmt.Errorf("%w: processor undecided after the EOF symbol", ErrInternal))
			}
			continue
		}

		// The processor's stack is the only copy of the token's symbols: every
		// symbol read for the token, then the synthetic EOF symbol once it is
		// read, which is always the last. read counts the stack without the EOF
		// symbol, so it is the number of real symbols read.
		stack := processor.Symbols()
		read := len(stack)
		if isEOF {
			read--
		}

		if textLen == 0 {
			if read > 0 {
				typ = TokenTypeUnknown
				textLen = 1 // Force consumption of one unknown symbol.
			} else if isEOF {
				return abandon(io.EOF)
			} else {
				// Defensive, unreachable: before the EOF symbol the
				// processor decides only after it reads a real symbol, so
				// read > 0 above.
				return abandon(fmt.Errorf("%w: rule returned zero-length token", ErrInternal))
			}
		}

		if isEOF && read == 0 {
			// The synthetic EOF symbol is fed to the rules, so a rule may
			// report a match for it. Nothing was consumed, so the stream is
			// simply done.
			return abandon(io.EOF)
		}

		if textLen > read {
			// Defensive, unreachable: processSymbol discards an accept that
			// counts the EOF symbol, and before the EOF symbol the stack
			// holds only real symbols, so no recorded match is longer than
			// the symbols read. The check stays so that a broken invariant
			// fails the token instead of slicing past the symbols read.
			return abandon(fmt.Errorf("%w: match of %d symbols exceeds the %d symbols read", ErrInternal, textLen, read))
		}

		// The winner is a prefix of the real symbols on the stack, so it never
		// includes the EOF symbol. Its runes are copied out below, before the
		// next token's Reset reuses the stack.
		winner := stack[:textLen]

		// A short token's text is stored inline, in the same allocation as
		// its Token; a longer one gets its own array (see tokenBox).
		var box *tokenBox
		var runes []rune
		if len(winner) <= inlineTokenRunes {
			box = new(tokenBox)
			runes = box.text[:len(winner):len(winner)]
		} else {
			runes = make([]rune, len(winner))
		}
		spanBytes := 0
		for i, s := range winner {
			runes[i] = s.Rune()
			spanBytes += s.Size()
		}

		start := mark.Pos()

		// Commit the cursor over exactly the matched bytes. The token is the
		// prefix of what was read, so committing is stepping back over the
		// speculative overshoot rather than replaying the token.
		end := sc.cursor.Cursor()
		if overshoot := (end.ByteOffset() - start.ByteOffset()) - spanBytes; overshoot > 0 {
			if _, err := sc.cursor.Seek(int64(-overshoot), io.SeekCurrent); err != nil {
				return abandon(fmt.Errorf("bytescan: commit cursor: %w", err))
			}
			end = sc.cursor.Cursor()
		}

		span := cursor.NewSpan(start, end)

		// The token is committed: its in-flight slot now holds a committed mark
		// (remarkSlot stored or re-armed it there), and the oldest committed
		// mark — the slot immediately ahead in the ring — is displaced. It is
		// released, but its value stays in the slot to be re-armed in place on
		// the next pass, so the reuse path allocates no checkpoint. A mark is
		// never released by a call that did not commit a token.
		if old := sc.marks[(sc.marksNext+1)%markSlots]; old != nil {
			_ = old.Release()
		}
		sc.marksNext = (sc.marksNext + 1) % markSlots

		if box != nil {
			box.token = Token{typ: typ, text: runes, offset: span.Start().RuneOffset(), span: span}
			return &box.token, nil
		}
		return NewTokenWithSpan(typ, runes, span), nil
	}
}
