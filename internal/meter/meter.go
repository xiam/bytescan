// Package meter counts what enters and what leaves a scanner. It is internal
// to this module: it is a measurement tool, not API. The measurement contract
// is docs/specs/measurement.md.
//
// A Reader wraps the io.Reader that a scanner reads. It counts the bytes that
// its Read calls return, and the Read calls. A Source wraps a bytescan.Source.
// It counts the tokens that its Next calls return, and the bytes of those
// tokens. The bytes of a token are the bytes of its span (Token.Span), so a
// token without a span, or with an empty span, counts as a token of 0 bytes.
//
// Both wrappers write into one Counts that the caller gives them. The caller
// copies that Counts at a boundary it chooses, for example after the last Next
// returned io.EOF. The copy is a snapshot: a later call does not change it.
//
// The wrappers hold no clock. The caller measures one elapsed interval, from
// before the first Next to after the last one returns, and Rates divides the
// counts by that interval. So the interval holds the tokens that the scanner
// emits after its last Read, from the input that it buffered.
//
// The cost of the wrappers is one more call through an interface for each Read
// call and for each token, and one update of the counts for each: two
// additions to Bytes and Reads for a Read call, two to Tokens and TokenBytes
// for a token. They do no work for each byte.
//
// A Reader and a Source are not safe for concurrent use, and neither is the
// Counts that they share.
package meter

import (
	"errors"
	"io"
	"time"

	"github.com/xiam/bytescan"
)

// Counts holds what a Reader and a Source counted.
type Counts struct {
	// Bytes is the sum of the byte counts that Read returned.
	Bytes uint64
	// Reads is the number of Read calls, the calls that returned an error
	// included.
	Reads uint64
	// Tokens is the number of tokens that Next returned.
	Tokens uint64
	// TokenBytes is the sum of the bytes of the spans of those tokens.
	TokenBytes uint64
}

// Reader is an io.Reader that reads another io.Reader and counts the bytes
// and the Read calls in a Counts.
type Reader struct {
	r io.Reader
	c *Counts
}

// NewReader returns a Reader that reads r and counts in c. It panics when r or
// c is nil.
func NewReader(r io.Reader, c *Counts) *Reader {
	if r == nil || c == nil {
		panic("meter.NewReader: nil reader or nil counts")
	}
	return &Reader{r: r, c: c}
}

// Read calls Read on the wrapped reader and returns its results unchanged:
// the byte count, io.EOF and every other error. It adds the call to Reads,
// and a positive byte count to Bytes.
func (m *Reader) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	m.c.Reads++
	if n > 0 {
		m.c.Bytes += uint64(n)
	}
	return n, err
}

// Source is a bytescan.Source that reads another bytescan.Source and counts
// the tokens and their bytes in a Counts.
type Source struct {
	src bytescan.Source
	c   *Counts
}

var _ bytescan.Source = (*Source)(nil)

// NewSource returns a Source that reads src and counts in c. It panics when
// src or c is nil.
func NewSource(src bytescan.Source, c *Counts) *Source {
	if src == nil || c == nil {
		panic("meter.NewSource: nil source or nil counts")
	}
	return &Source{src: src, c: c}
}

// Next calls Next on the wrapped source and returns its results unchanged:
// the token, io.EOF, bytescan.ErrMoreInput and every other error. It counts a
// token that comes with a nil error. A nil token with a nil error breaks the
// Source contract; it is passed through and not counted.
func (m *Source) Next() (*bytescan.Token, error) {
	tok, err := m.src.Next()
	if err == nil && tok != nil {
		m.c.Tokens++
		if n := tok.Span().Bytes(); n > 0 {
			m.c.TokenBytes += uint64(n)
		}
	}
	return tok, err
}

// Throughput is a rate over an elapsed interval.
type Throughput struct {
	// BytesPerSecond is Counts.Bytes divided by the interval.
	BytesPerSecond float64
	// TokensPerSecond is Counts.Tokens divided by the interval.
	TokensPerSecond float64
}

// errNoInterval is the error of Rates for an interval that is not positive.
var errNoInterval = errors.New("meter: the elapsed interval is not positive")

// Rates divides the counts of c by elapsed, an interval that the caller
// measured. An interval of zero or less gives no rate and an error.
func Rates(c Counts, elapsed time.Duration) (Throughput, error) {
	if elapsed <= 0 {
		return Throughput{}, errNoInterval
	}
	s := elapsed.Seconds()
	return Throughput{
		BytesPerSecond:  float64(c.Bytes) / s,
		TokensPerSecond: float64(c.Tokens) / s,
	}, nil
}
