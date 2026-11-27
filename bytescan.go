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
// sits at the end of the winning match. Over a *cursor.Reader the scanner
// reads the runes of a token from the cursor's buffer in batches and leaves
// the cursor at the start of the token; once the rules decide, it moves the
// cursor over the winning match only, so there is no step back. It keeps the
// symbols of the overshoot and shows them to the rules of the next token
// before it reads the cursor again, when the cursor is still at the end of the
// token and nothing reset the cursor or marked a checkpoint on it. Between two calls of Next the cursor sits at the end of the
// committed token on both paths. Token positions come from that committed
// range — the checkpoint's position and the cursor at the end of the match —
// never from where speculation stopped.
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
// is assembled, the rule stack keeps every symbol read for it (one packed entry
// per rune, half the size of a Symbol) and the cursor retains its input. So the
// live heap grows linearly with the bytes of the open token, and the peak while
// the stack grows is higher than the live heap. A token stays open while
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
	"github.com/xiam/bytescan/internal/leanread"
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

// startCursor is implemented by cursors that can start in the middle of a text
// (cursor.Reader.ResetStart). It backs the BOF and BOL flags of the symbol at
// offset 0: the cursor reports positions from the start of its source, and
// Start reports whether that source begins at offset 0 of the text and whether
// the byte before it is a newline. It is optional, following contextualCursor:
// a cursor without it gives the symbol at offset 0 both flags, as before.
type startCursor interface {
	Start() cursor.Start
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

	// starter is the cursor when it implements startCursor, and nil otherwise.
	// It is set once at construction, as canRemark is.
	starter startCursor

	// maxTokenBytes is the per-token byte bound passed to WithMaxTokenBytes, or
	// zero for unbounded. It is enforced by the scanner here rather than delegated
	// to the cursor's retention budget, because the cursor must also retain the
	// previous token while a token is assembled.
	maxTokenBytes int

	// lean is the lean read path of the cursor (package internal/leanread),
	// or nil when the cursor has none. It is set once at construction, as
	// canRemark is. With it, readSymbol takes the runes of a token from
	// leanWin, which one Peek fills, and the cursor stays at the start of the
	// token until the rules decide it (see readSymbolLean).
	lean leanread.Reader

	// advancer is the commit operation of the lean read path, or nil when
	// the read path has none. With it, the commit of a token moves the cursor
	// to the end of the token with a position computed from the token's
	// runes (see leanExtent), instead of a Seek that decodes the token's
	// bytes again. It is set once at construction, with lean.
	advancer leanread.Advancer[cursor.Pos]

	// boundary holds the operations of the lean read path at the boundary
	// between two tokens, or nil when the read path has none. With it,
	// remarkSlot re-arms a mark and gets its position under one lock of the
	// cursor, and the commit of a token moves the cursor and releases the
	// oldest committed mark under one lock (see leanCommit). It is set once
	// at construction, with lean.
	boundary leanread.Boundary[cursor.Pos, *cursor.Checkpoint]

	// leanWin holds the runes of the last Peek, and leanN is their number.
	// leanI is the next rune to read from it. leanAt is the position the
	// last Peek started at. leanSkip is the number of bytes of the symbols
	// read for the token so far: the next Peek starts there. leanPartial
	// reports that a Peek of the token decoded an incomplete UTF-8 sequence
	// (leanread.At.Partial), so leanAdvance moves the cursor one symbol at a
	// time. nextToken sets them to zero at the start of each token.
	leanWin     [leanBatch]leanread.Rune
	leanN       int
	leanI       int
	leanAt      leanread.At
	leanSkip    int
	leanPartial bool

	// carry holds symbols that the rules were shown past the end of the last
	// committed token, and carryI is the next one to show. readSymbolLean
	// shows carry[carryI:] first, then the rest of leanWin, and only then
	// peeks again, so the next token does not decode those runes again and
	// the commit does not step the cursor back over them. carryAt is the
	// byte offset of the cursor at that commit, and carryEpoch is the epoch
	// of the cursor then (leanread.Reader.Epoch). carryOK reports that a
	// commit kept the carry and nothing dropped it since. nextToken uses the
	// carry only when the cursor is at carryAt and nothing reset it or marked
	// it since, except the mark of the token itself; otherwise it drops the
	// carry and reads from the cursor (see takeCarry).
	carry      []Symbol
	carryI     int
	carryAt    int
	carryEpoch uint64
	carryOK    bool

	mu sync.Mutex

	rules    []TokenType
	rulesMap map[TokenType]Rule
	rulesMu  sync.RWMutex

	// ctr holds the engine counters in the build with the tag bench, and
	// nothing in the normal build (counters_on.go, counters_off.go). It is not
	// the last field, so its size of zero in the normal build adds no padding.
	// It comes after the fields of the hot path, so the counters of the build
	// with the tag do not move them.
	ctr scanCounters

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
	if s, ok := c.(startCursor); ok {
		sc.starter = s
	}
	if l, ok := leanread.Of(c); ok {
		sc.lean = l
		sc.advancer, _ = l.(leanread.Advancer[cursor.Pos])
		sc.boundary, _ = l.(leanread.Boundary[cursor.Pos, *cursor.Checkpoint])
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
// A source that has no input now, but has not ended, can return
// cursor.ErrMoreInput instead of blocking. Next then returns ErrMoreInput
// itself, not a wrapped error, so err == ErrMoreInput holds. Next returns it
// only when the rules have not decided the next token: the call consumes
// nothing, and the next call lexes the same token again from its start, with
// the input that arrived in between. The rules are shown the end of input only
// when the source returns io.EOF. WithMaxTokenBytes still fails a token that
// grows past the bound before it is decided.
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

	tok, _, err := sc.nextToken()
	return tok, err
}

// Lookahead records how far the rules read when they decided a token. The
// reach of the token is its end byte offset plus After.
type Lookahead struct {
	// After is the number of bytes that the rules were shown after the end of
	// the token. It counts the symbols shown to the rules, not the bytes that
	// the cursor buffered. It is 0 when the rules decided the token without
	// reading past its end.
	After int
	// ReadEnd reports that the rules were shown the end of input: the source
	// returned io.EOF while the token was decided. A source that returns
	// cursor.ErrMoreInput does not show the rules the end of input, and a
	// token that the rules had not decided then is not returned. So a token
	// that the rules decided from the available input has ReadEnd false.
	ReadEnd bool
}

// NextLookahead returns the next token, as Next does, together with its
// Lookahead record. It returns the same tokens and the same errors as Next,
// and it consumes the same input. When the error is not nil, the token is nil
// and the record is the zero Lookahead.
//
// The record is returned with its token, so it stays correct when several
// goroutines call the scanner.
//
// This method is safe for concurrent use by multiple goroutines.
func (sc *Scanner) NextLookahead() (*Token, Lookahead, error) {
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

	// A mode driver calls Release when it switches to another scanner. The
	// symbols carried past the last token are dropped, and the next token
	// of this scanner reads them from the cursor again.
	sc.dropCarry()
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
	sc.processor.ctr = sc.ctr.rules()
	return sc.processor, nil
}

// readSymbol reads the next symbol from the cursor.
//
// It reports isEOF for the synthetic end-of-input symbol, which carries RuneEOF
// and the FlagEOF flag and is not part of the input. The positional flags come
// from the located rune and the cursor position, so a symbol read again during
// replay gets the same flags. At offset 0 they also come from the start of the
// cursor's source (see startCursor).
//
// A cursor with the lean read path is read by readSymbolLean instead.
func (sc *Scanner) readSymbol() (sym Symbol, isEOF bool, err error) {
	if sc.lean != nil {
		return sc.readSymbolLean()
	}

	lr, err := sc.cursor.ReadLocatedRune()
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return Symbol{}, false, err
		}

		at := sc.cursor.Cursor()

		flags := symbolFlags(at, RuneEOF, true)
		if sc.starter != nil && at.IsStart() {
			flags = flags&^(FlagBOF|FlagBOL) | startFlags(sc.starter)
		}

		return NewSymbol(RuneEOF, flags), true, nil
	}

	at := lr.Pos()
	flags := symbolFlags(at, lr.Rune(), false)
	if sc.starter != nil && at.IsStart() {
		flags = flags&^(FlagBOF|FlagBOL) | startFlags(sc.starter)
	}

	return Symbol{r: lr.Rune(), flags: uint8(flags), size: uint8(lr.Size())}, false, nil
}

// leanBatch is the most runes one Peek of the lean read path decodes. A Peek
// decodes only the runes that the cursor's buffer already holds, so a larger
// batch never reads more input; it can only decode runes after the end of a
// short token that the token does not use.
const leanBatch = 16

// readSymbolLean reads the next symbol of the token through the lean read path
// (package internal/leanread). It returns the same symbols, with the same
// flags, as readSymbol returns through ReadLocatedRune, but it does not move the
// cursor: the cursor stays at the start of the token, and nextToken moves it
// over the symbols read once the rules decide the token.
//
// The flags come from the position of the rune instead of from a full Pos. The
// first rune of the token gets BOF and BOL from the position at the cursor, as
// Peek reports it. A later rune starts a line exactly when the rune before it
// is a newline, and it is never at the start of the stream.
func (sc *Scanner) readSymbolLean() (sym Symbol, isEOF bool, err error) {
	if sc.carryI < len(sc.carry) {
		// A symbol that the rules were shown past the end of the last token.
		// Its flags came from the same position, so they are the flags a
		// read from the cursor gives.
		s := sc.carry[sc.carryI]
		sc.carryI++
		sc.leanSkip += s.Size()
		return s, false, nil
	}

	if sc.leanI == sc.leanN {
		n, at, err := sc.lean.Peek(sc.leanSkip, sc.leanWin[:])
		sc.leanAt = at
		sc.leanN, sc.leanI = n, 0
		if at.Partial {
			sc.leanPartial = true
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return Symbol{}, false, err
			}
			return NewSymbol(RuneEOF, sc.leanFlags(at, RuneEOF)|FlagEOF), true, nil
		}
	}

	lr := sc.leanWin[sc.leanI]

	var flags uint
	if sc.leanI == 0 {
		flags = sc.leanFlags(sc.leanAt, lr.Rune)
	} else {
		flags = FlagNone
		if sc.leanWin[sc.leanI-1].Rune == '\n' {
			flags |= FlagBOL
		}
		if lr.Rune == '\n' {
			flags |= FlagEOL
		}
	}

	sc.leanI++
	sc.leanSkip += lr.Size

	return Symbol{r: lr.Rune, flags: uint8(flags), size: uint8(lr.Size)}, false, nil
}

// leanAdvance moves the cursor, which the lean read path left at the start of
// the token, over the leanSkip bytes of the symbols read for the token, so it
// sits where ReadLocatedRune would have left it. The stack of rp holds the
// symbols the rules were shown, in order; a symbol read but not shown yet is
// the rest of leanSkip.
//
// One Seek over the whole range counts the runes of the range as one decode of
// it. That is what ReadLocatedRune counted, unless a Peek decoded an incomplete
// sequence that later bytes completed (leanPartial). Then the cursor moves one
// symbol at a time, so each symbol is counted as it was read.
func (sc *Scanner) leanAdvance(rp *rulesProcessor) error {
	if sc.leanSkip == 0 {
		return nil
	}
	if !sc.leanPartial {
		_, err := sc.cursor.Seek(int64(sc.leanSkip), io.SeekCurrent)
		return err
	}
	done := 0
	for i := 0; i < rp.Len(); i++ {
		size := rp.at(i).Size()
		if size == 0 || done+size > sc.leanSkip {
			continue
		}
		if _, err := sc.cursor.Seek(int64(size), io.SeekCurrent); err != nil {
			return err
		}
		done += size
	}
	if rest := sc.leanSkip - done; rest > 0 {
		_, err := sc.cursor.Seek(int64(rest), io.SeekCurrent)
		return err
	}
	return nil
}

// leanCommit moves the cursor, which the lean read path left at the start of
// the token, over the n bytes of the token, whose text is text, and returns
// the position after the move. No Peek of the token decoded an incomplete
// sequence. With the commit operation of the read path (advancer), the
// position comes from the runes of the text; otherwise it is a Seek.
//
// old is the mark that the commit releases (see commit). With the boundary
// operations of the read path, the move releases it under the same lock of the
// cursor, and leanCommit reports released. Otherwise commit releases it.
func (sc *Scanner) leanCommit(text []rune, n int, old *cursor.Checkpoint) (end cursor.Pos, released bool, err error) {
	if sc.boundary != nil {
		end, err = sc.boundary.AdvanceRelease(n, leanExtent(text), old)
		return end, true, err
	}
	if sc.advancer != nil {
		end, err = sc.advancer.Advance(n, leanExtent(text))
		return end, false, err
	}
	if _, err := sc.cursor.Seek(int64(n), io.SeekCurrent); err != nil {
		return cursor.Pos{}, false, err
	}
	return sc.cursor.Cursor(), false, nil
}

// leanExtent describes the text of a token for leanread.Advancer.Advance and
// leanread.Boundary.AdvanceRelease: its runes, its newlines, and the runes
// after its last newline. Each symbol of the token is one rune of the text, in
// text mode and in byte mode, and a newline is the rune 0x0A in both, so the
// counts are the counts the cursor makes over the token's bytes.
func leanExtent(text []rune) leanread.Extent {
	ext := leanread.Extent{Runes: len(text)}
	for i, r := range text {
		if r == '\n' {
			ext.Lines++
			ext.Column = len(text) - i - 1
		}
	}
	return ext
}

// dropCarry drops the symbols carried past the last token, and the runes of
// leanWin that are not shown yet. The next token reads from the cursor.
func (sc *Scanner) dropCarry() {
	sc.carry = sc.carry[:0]
	sc.carryI = 0
	sc.leanN, sc.leanI = 0, 0
	sc.carryOK = false
}

// takeCarry decides, at the start of a token, whether the token uses the
// symbols carried past the last token. It runs just after remarkSlot armed the
// mark of the token, which is one mark of the cursor's epoch. start is the
// byte offset of the cursor, where that mark is.
//
// The carry is used only when the cursor is where the last commit left it and
// its epoch moved by that one mark only. The bytes at that offset are then
// the bytes the carried symbols were decoded from, and no new mark moved the
// retention floor back, so every carried symbol is within the retention limit
// that Peek checked when it decoded it. Otherwise, for example after a caller
// moved a shared cursor, took a checkpoint of it or reset it onto a new
// source, the carry is dropped. It returns the index of the first carried
// symbol.
func (sc *Scanner) takeCarry(start int) int {
	if !sc.carryOK || start != sc.carryAt || sc.lean.Epoch() != sc.carryEpoch+1 {
		sc.dropCarry()
		return 0
	}
	// The token uses the carry now. A token that commits keeps a new one
	// (keepCarry). A token that is abandoned leaves carryOK false, so the
	// next token drops what is left and reads from the cursor.
	sc.carryOK = false
	return sc.carryI
}

// keepCarry keeps, at the commit of a token, the symbols that the rules were
// shown past its end, for the next token. The stack of rp holds the symbols
// shown for the token. Its first read symbols are real symbols, and its first
// textLen symbols are the token. Its first used symbols came from the carry,
// from the carry index from on. end is the byte offset of the cursor after the
// commit.
//
// A commit copies at most the symbols that the rules were shown past the end
// of the token. Carried symbols that the rules were not shown stay where they
// are, so a long carry is not copied for each token.
func (sc *Scanner) keepCarry(rp *rulesProcessor, read, textLen, from, used, end int) {
	if textLen <= used {
		head := from + textLen
		if used < read {
			// The token read past the carry, so every carried symbol was
			// shown, and the symbols from used on follow carry[head:].
			n := copy(sc.carry, sc.carry[head:])
			sc.carry = sc.carry[:n]
			for i := used; i < read; i++ {
				sc.carry = append(sc.carry, rp.at(i))
			}
			head = 0
		}
		sc.carryI = head
	} else {
		sc.carry = sc.carry[:0]
		for i := textLen; i < read; i++ {
			sc.carry = append(sc.carry, rp.at(i))
		}
		sc.carryI = 0
	}
	sc.carryAt = end
	sc.carryEpoch = sc.lean.Epoch()
	sc.carryOK = true
}

// leanFlags returns the BOF, BOL and EOL flags of the rune r at the position
// at, as symbolFlags and readSymbol give them for the same position.
func (sc *Scanner) leanFlags(at leanread.At, r rune) uint {
	flags := uint(FlagNone)
	if at.Start {
		flags |= FlagBOF
	}
	if at.LineStart {
		flags |= FlagBOL
	}
	if r == '\n' {
		flags |= FlagEOL
	}
	if sc.starter != nil && at.Start {
		flags = flags&^(FlagBOF|FlagBOL) | startFlags(sc.starter)
	}
	return flags
}

// symbolFlags derives a symbol's positional flags from the position it was read
// at. BOF is the start of the stream, BOL is rune column zero, and EOL is a
// newline; EOF is supplied by the caller. readSymbol replaces BOF and BOL at
// offset 0 when the cursor knows where its source starts (see startFlags).
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

// startFlags returns the BOF and BOL flags of the symbol at offset 0 of a
// cursor that knows where its source starts: BOF when the source is not in the
// middle of a text (!InText), and BOL when the byte before the source is a
// newline (LineStart). A cursor that was never reset reports InText false and
// LineStart true, so the flags are the ones symbolFlags gives. Every other
// symbol gets its flags from its position. It is kept apart from symbolFlags,
// which stays small enough to be inlined for every symbol.
func startFlags(starter startCursor) uint {
	s := starter.Start()
	flags := uint(FlagNone)
	if !s.InText {
		flags |= FlagBOF
	}
	if s.LineStart {
		flags |= FlagBOL
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
//
// It also returns the marked position, which Pos on the checkpoint returns.
// With the boundary operations of the lean read path, the re-arm and the
// position take one lock of the cursor (leanread.Boundary.Mark).
func (sc *Scanner) remarkSlot(idx int) (*cursor.Checkpoint, cursor.Pos) {
	if c := sc.marks[idx]; c != nil {
		if sc.boundary != nil {
			if pos, err := sc.boundary.Mark(c); err == nil {
				return c, pos
			}
		} else if sc.canRemark {
			if err := sc.cursor.(remarkableCursor).Remark(c); err == nil {
				return c, c.Pos()
			}
		}
		// Remark refused: fall through to a fresh checkpoint and replace
		// the slot's value below.
	}

	c := sc.cursor.Checkpoint()
	sc.marks[idx] = c
	return c, c.Pos()
}

// nextToken lexes the next token and returns it with its Lookahead record.
// Next drops the record; NextLookahead returns it.
func (sc *Scanner) nextToken() (*Token, Lookahead, error) {
	processor, err := sc.getProcessor()
	if err != nil {
		return nil, Lookahead{}, err
	}

	// Mark the start of the token in the in-flight slot: the cursor retains every
	// byte from here until the token is committed, which is what makes the
	// rewind below exact and what WithMaxTokenBytes bounds. start is the
	// position of the mark: the position of the token's first byte.
	mark, start := sc.remarkSlot(sc.marksNext)

	tokenBytes := 0
	sc.leanSkip, sc.leanPartial = 0, false

	// On the lean read path the token first uses the symbols carried past the last token, when the
	// cursor is still where that commit left it (see takeCarry). carryFrom
	// is the index of the first of them in sc.carry.
	carryFrom := 0
	if sc.lean != nil {
		carryFrom = sc.takeCarry(start.ByteOffset())
	}

	// abandon ends the token without committing it: the cursor is rewound to the
	// token start so a failed Next consumes nothing and a retry starts over. The
	// previous token's checkpoint is deliberately left active, so its context
	// survives a failed call and an EOF probe.
	abandon := func(err error) (*Token, Lookahead, error) {
		sc.ctr.abandon(err)
		if sc.lean != nil {
			// The lean read path left the cursor at the start of the token.
			// It moves over the symbols read, as ReadLocatedRune would have,
			// so the reset below rewinds the same bytes. The reset restores
			// the start of the token whether the move succeeds or not.
			_ = sc.leanAdvance(processor)
		}
		_ = mark.Reset()
		_ = mark.Release()
		return nil, Lookahead{}, err
	}

	for {
		sym, isEOF, err := sc.readSymbol()
		if err != nil {
			if errors.Is(err, cursor.ErrMoreInput) {
				// The rules have not decided the token, and the source has
				// no input now. The unfinished token is not returned: the
				// cursor steps back to its start, and the next call lexes it
				// again from there. The signal is returned unwrapped.
				return abandon(ErrMoreInput)
			}
			return abandon(fmt.Errorf("bytescan: ReadRune: %w", err))
		}
		if !isEOF {
			tokenBytes += sym.Size()

			if sc.maxTokenBytes > 0 && tokenBytes > sc.maxTokenBytes {
				return abandon(fmt.Errorf("%w: token exceeds %d bytes: %w", ErrTokenTooLarge, sc.maxTokenBytes, cursor.ErrRetentionExceeded))
			}
		}

		sc.ctr.symbol()
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
		read := processor.Len()
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

		runes, box, spanBytes := processor.tokenText(textLen)

		if sc.lean != nil && !sc.leanPartial {
			// The lean read path left the cursor at the start of the token.
			// It moves over the token only, so it sits at the end of the
			// token, and there is no step back. The symbols that the rules
			// were shown past the end are carried to the next token (see
			// keepCarry). The bytes of the overshoot are the bytes of the
			// symbols read minus the bytes of the token: the After of the
			// record.
			end, released, err := sc.leanCommit(runes, spanBytes, sc.marks[sc.oldestSlot()])
			if err != nil {
				return abandon(fmt.Errorf("bytescan: commit cursor: %w", err))
			}
			la := Lookahead{ReadEnd: isEOF, After: sc.leanSkip - spanBytes}
			sc.keepCarry(processor, read, textLen, carryFrom, sc.carryI-carryFrom, end.ByteOffset())
			return sc.commit(typ, runes, box, cursor.NewSpan(start, end), la, released)
		}

		if sc.lean != nil {
			// A Peek of the token decoded an incomplete UTF-8 sequence
			// (leanPartial), which later bytes can complete. Nothing is
			// carried. The cursor moves over every symbol the rules were
			// shown, one at a time, so it sits where ReadLocatedRune would
			// have left it, and the step back below is the same.
			sc.dropCarry()
			if err := sc.leanAdvance(processor); err != nil {
				return abandon(fmt.Errorf("bytescan: commit cursor: %w", err))
			}
		}

		// Commit the cursor over exactly the matched bytes. The token is the
		// prefix of what was read, so committing is stepping back over the
		// speculative overshoot rather than replaying the token.
		//
		// Before the step back, the cursor sits just after the last symbol
		// that the rules were shown: that position is the token's reach. The
		// overshoot is the reach minus the token's end, which is the After of
		// the record. It counts symbols shown to the rules, not bytes that the
		// cursor buffered. The rules were shown the end of input exactly when
		// the last symbol was the synthetic EOF symbol.
		end := sc.cursor.Cursor()
		la := Lookahead{ReadEnd: isEOF}
		if overshoot := (end.ByteOffset() - start.ByteOffset()) - spanBytes; overshoot > 0 {
			if _, err := sc.cursor.Seek(int64(-overshoot), io.SeekCurrent); err != nil {
				return abandon(fmt.Errorf("bytescan: commit cursor: %w", err))
			}
			end = sc.cursor.Cursor()
			la.After = overshoot
			sc.ctr.stepBack(overshoot)
		}

		return sc.commit(typ, runes, box, cursor.NewSpan(start, end), la, false)
	}
}

// oldestSlot is the slot of the oldest committed mark: the slot just ahead
// of the in-flight slot in the ring. The commit of a token releases it.
func (sc *Scanner) oldestSlot() int {
	return (sc.marksNext + 1) % markSlots
}

// commit ends a token whose cursor sits at the end of span: it moves the ring
// of marks on and builds the token. box holds the token when its text is
// inline, and runes is its text. released reports that the move of the cursor
// over the token already released the oldest committed mark (see
// leanCommit).
func (sc *Scanner) commit(typ TokenType, runes []rune, box *tokenBox, span cursor.Span, la Lookahead, released bool) (*Token, Lookahead, error) {
	// The token is committed: its in-flight slot now holds a committed mark
	// (remarkSlot stored or re-armed it there), and the oldest committed
	// mark — the slot immediately ahead in the ring — is displaced. It is
	// released here, or already by the move of the cursor (released), but
	// its value stays in the slot to be re-armed in place on the next pass,
	// so the reuse path allocates no checkpoint. A mark is never released by
	// a call that did not commit a token.
	next := sc.oldestSlot()
	if old := sc.marks[next]; old != nil && !released {
		_ = old.Release()
	}
	sc.marksNext = next

	sc.ctr.token(typ, len(runes) > inlineTokenRunes)

	if box != nil {
		box.token = Token{typ: typ, text: runes, offset: span.Start().RuneOffset(), span: span}
		return &box.token, la, nil
	}
	return NewTokenWithSpan(typ, runes, span), la, nil
}
