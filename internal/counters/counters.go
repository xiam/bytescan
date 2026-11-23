// Package counters holds the snapshot types of the engine counters, and the
// functions that read them from a cursor.Reader and a bytescan.Scanner. It is
// internal, so it adds no name to the API of either package.
//
// The engine keeps the counters only in a build with the tag bench (see
// docs/specs/measurement.md, "The engine counters"). The snapshot types exist
// in both builds. In the normal build, cursor and bytescan do not import this
// package, the hooks below stay nil, and OfCursor and OfScanner return false.
// In the build with the tag, the file counters_on.go of each package sets its
// hook from its init function.
//
// Every counter only grows. A snapshot is a copy: it does not change when the
// engine runs on.
package counters

// Cursor is a snapshot of the counters of one cursor.Reader.
type Cursor struct {
	// Fills counts the reads from the source, and FillBytes the bytes that
	// they returned. For a rune source, the bytes of a rune that did not fit
	// the buffer are counted when a later fill writes them.
	Fills, FillBytes uint64
	// Compactions counts the shifts of the buffer, and CompactBytes the bytes
	// that they moved.
	Compactions, CompactBytes uint64
	// Grows counts the growths of the buffer. MaxBuffer is the largest buffer
	// allocation: the initial buffer, then the size after each growth.
	Grows, MaxBuffer uint64
	// Replays counts the successful resets to a checkpoint, and ReplayBytes
	// the bytes that they rewound.
	Replays, ReplayBytes uint64
	// Locks counts the acquisitions of the reader's lock by an exported
	// method of the reader or of a checkpoint.
	Locks uint64
	// FloorScans counts the checkpoints visited to find the oldest byte that
	// the reader must keep.
	FloorScans uint64
}

// Scanner is a snapshot of the counters of one bytescan.Scanner.
type Scanner struct {
	// Symbols counts the symbols passed to the rules processor, the
	// synthetic end-of-input symbol included.
	Symbols uint64
	// RuleCalls counts the calls of a rule by the VM, pushback calls
	// included.
	RuleCalls uint64
	// Pushbacks counts the rule calls caused by StatePushBack.
	Pushbacks uint64
	// MaxLive is the largest number of rules still active after one step.
	MaxLive uint64
	// Tokens counts the committed tokens. LongTokens counts the committed
	// tokens whose text is longer than the inline text of a token, so that
	// the text gets its own array.
	Tokens, LongTokens uint64
	// Unknown counts the committed tokens of type TokenTypeUnknown.
	Unknown uint64
	// Abandons counts the calls that commit no token, by cause.
	Abandons Abandons
	// StepBacks counts the commits that seek back over bytes that the rules
	// read past the token, and StepBackBytes those bytes.
	StepBacks, StepBackBytes uint64
}

// Abandons counts the calls that commit no token, by cause.
type Abandons struct {
	// EndOfInput counts the calls that return io.EOF.
	EndOfInput uint64
	// MoreInput counts the calls that return ErrMoreInput.
	MoreInput uint64
	// Error counts the calls that return any other error.
	Error uint64
}

// The hooks that the files counters_on.go of cursor and bytescan set from
// their init functions. They stay nil in the normal build.
var (
	// CursorHook returns the counters of a *cursor.Reader.
	CursorHook func(v any) (Cursor, bool)
	// ScannerHook returns the counters of a *bytescan.Scanner.
	ScannerHook func(v any) (Scanner, bool)
)

// OfCursor returns a snapshot of the counters of v, a *cursor.Reader. It
// returns false when v is not a *cursor.Reader, or when the build has no
// counters.
func OfCursor(v any) (Cursor, bool) {
	if CursorHook == nil {
		return Cursor{}, false
	}
	return CursorHook(v)
}

// OfScanner returns a snapshot of the counters of v, a *bytescan.Scanner. It
// returns false when v is not a *bytescan.Scanner, or when the build has no
// counters.
func OfScanner(v any) (Scanner, bool) {
	if ScannerHook == nil {
		return Scanner{}, false
	}
	return ScannerHook(v)
}
