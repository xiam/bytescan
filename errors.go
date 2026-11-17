package bytescan

import "errors"

// Errors that the scanner itself decides. Each error the package returns for
// one of these failures matches its sentinel with errors.Is; the message may
// add detail after the sentinel's text.
//
// Errors that the scanner does not decide keep their identity and get no
// sentinel of their own: Next returns plain io.EOF (compare with ==) at the
// end of input; an error from the reader or the cursor is wrapped with %w, so
// errors.Is matches the original error; and an error from the cursor's
// Context is returned unchanged.
var (
	// ErrDuplicateRule is returned by AddRule for a TokenType that already
	// has a rule.
	ErrDuplicateRule = errors.New("bytescan: duplicate rule type")

	// ErrEmptyRuleType is returned by AddRule for the empty TokenType.
	ErrEmptyRuleType = errors.New("bytescan: rule type cannot be empty")

	// ErrNilRule is returned by AddRule for a nil Rule.
	ErrNilRule = errors.New("bytescan: rule cannot be nil")

	// ErrNoRules is returned by Next when no rule is registered.
	ErrNoRules = errors.New("bytescan: no rules defined")

	// ErrNilToken is returned by Context for a nil token.
	ErrNilToken = errors.New("bytescan: nil token")

	// ErrContextUnsupported is returned by Context when the cursor does not
	// expose the text it retains.
	ErrContextUnsupported = errors.New("bytescan: cursor does not expose retained text")

	// ErrTokenTooLarge is returned by Next when a token would exceed the
	// bound set with WithMaxTokenBytes. The same error also matches
	// textreader.ErrRetentionExceeded, which a cursor's own retention budget
	// returns as well; only the scanner's per-token bound matches
	// ErrTokenTooLarge.
	ErrTokenTooLarge = errors.New("bytescan: token too large")

	// ErrInternal reports a broken engine invariant. Next checks its
	// invariants defensively; this failure is not expected.
	ErrInternal = errors.New("bytescan: internal error")
)
