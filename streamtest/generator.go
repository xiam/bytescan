package streamtest

import (
	"io"
	"math/rand"
)

// RecordFunc appends record i of a stream to dst and returns the extended
// slice. Records are numbered from 0. It draws every random choice from rng,
// so a seed always gives the same stream.
type RecordFunc func(rng *rand.Rand, i int, dst []byte) []byte

// Source is a byte stream that Run lexes, with its budget: a Generator, a
// Corrupter, or the result of Prefix. It is the input of a test, not a
// bytescan.Source: Run builds the scanner over it.
type Source interface {
	io.Reader

	// Budget returns the byte budget of the stream.
	Budget() int64
}

// GeneratorOption configures a Generator.
type GeneratorOption func(*Generator)

// WithBudget sets the byte budget of a generator. Zero or less keeps
// DefaultBudget. A budget set here is explicit even when it equals
// DefaultBudget: under the race detector, Run then keeps the DefaultCeiling
// of the budget and the first-interval baseline (see the package
// documentation).
func WithBudget(n int64) GeneratorOption {
	return func(g *Generator) {
		if n > 0 {
			g.budget = n
			g.explicitBudget = true
		}
	}
}

// Generator is an io.Reader that writes whole records until the stream has
// reached its byte budget. It holds one record at a time, never the stream.
//
// The stream ends with the first record that takes it past the budget, so it
// is longer than the budget by at most one record, and it always ends at a
// record boundary.
type Generator struct {
	record  RecordFunc
	rng     *rand.Rand
	budget  int64
	written int64 // bytes of the records generated so far
	records int   // records generated so far

	explicitBudget bool // WithBudget set the budget

	buf []byte // the current record
	off int    // read offset in buf
}

// NewGenerator returns a generator of the records of record, seeded with seed.
// Its budget is DefaultBudget unless an option sets another.
func NewGenerator(seed int64, record RecordFunc, opts ...GeneratorOption) *Generator {
	g := &Generator{
		record: record,
		rng:    rand.New(rand.NewSource(seed)),
		budget: DefaultBudget(),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Budget returns the byte budget of the stream.
func (g *Generator) Budget() int64 {
	return g.budget
}

// usesDefaultBudget reports whether the budget is the default budget of a run
// without -streamtest.long because no WithBudget set one.
func (g *Generator) usesDefaultBudget() bool {
	return !g.explicitBudget && g.budget == defaultBudget
}

// Records returns how many records the generator has produced so far.
func (g *Generator) Records() int {
	return g.records
}

// Bytes returns how many bytes of records the generator has produced so far.
func (g *Generator) Bytes() int64 {
	return g.written
}

// Read implements io.Reader.
func (g *Generator) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if g.off == len(g.buf) {
			if !g.nextRecord() {
				break
			}
		}
		c := copy(p[n:], g.buf[g.off:])
		g.off += c
		n += c
	}
	if n == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return n, nil
}

// nextRecord generates the next record into buf. It returns false when the
// stream has reached its budget.
func (g *Generator) nextRecord() bool {
	if g.written > g.budget {
		return false
	}
	g.buf = g.record(g.rng, g.records, g.buf[:0])
	g.off = 0
	g.records++
	g.written += int64(len(g.buf))
	return true
}

// GarbageFunc appends one run of garbage to dst and returns the extended
// slice. It draws every random choice from rng.
type GarbageFunc func(rng *rand.Rand, dst []byte) []byte

// Garbage returns a GarbageFunc that appends between minLen and maxLen bytes
// (at least 1), each picked at random from alphabet.
func Garbage(alphabet string, minLen, maxLen int) GarbageFunc {
	if alphabet == "" {
		panic("streamtest.Garbage: empty alphabet")
	}
	minLen = max(minLen, 1)
	maxLen = max(maxLen, minLen)
	return func(rng *rand.Rand, dst []byte) []byte {
		n := minLen + rng.Intn(maxLen-minLen+1)
		for i := 0; i < n; i++ {
			dst = append(dst, alphabet[rng.Intn(len(alphabet))])
		}
		return dst
	}
}

// Injection is one garbage run that a Corrupter inserted.
type Injection struct {
	// Offset is the byte offset of the garbage in the corrupted stream.
	Offset int64

	// Len is the length of the garbage in bytes. The next good record starts
	// at Offset+Len.
	Len int

	// Record is the index of the record that follows the garbage.
	Record int
}

// Corrupter wraps a Generator and inserts a garbage run between two records at
// a given rate. It reports each run through NextInjection, so a test can find
// the UNKNOWN tokens it expects and the next good record.
//
// The budget of a Corrupter is the budget of its generator; the garbage comes
// on top of it.
type Corrupter struct {
	gen     *Generator
	rng     *rand.Rand
	rate    float64
	garbage GarbageFunc

	buf     []byte // the current garbage run and record
	off     int    // read offset in buf
	written int64  // bytes of the corrupted stream so far

	pending  []Injection // reported runs that NextInjection has not returned
	injected int
}

// Corrupt returns a Corrupter over gen. Before each record after the first, it
// inserts a run of garbage with probability rate (from 0 to 1). Its own random
// choices come from seed, so the records of gen are those of the clean stream.
// Garbage builds a GarbageFunc from an alphabet.
func Corrupt(gen *Generator, seed int64, rate float64, garbage GarbageFunc) *Corrupter {
	if garbage == nil {
		panic("streamtest.Corrupt: nil GarbageFunc")
	}
	return &Corrupter{
		gen:     gen,
		rng:     rand.New(rand.NewSource(seed)),
		rate:    rate,
		garbage: garbage,
	}
}

// Budget returns the byte budget of the generator.
func (c *Corrupter) Budget() int64 {
	return c.gen.Budget()
}

// usesDefaultBudget reports whether the generator uses the default budget.
func (c *Corrupter) usesDefaultBudget() bool {
	return c.gen.usesDefaultBudget()
}

// Injected returns how many garbage runs the Corrupter has inserted so far.
func (c *Corrupter) Injected() int {
	return c.injected
}

// NextInjection returns the oldest garbage run that it has not returned yet,
// in stream order. It reports false when there is none. A test that checks
// tokens as they arrive calls it to learn where the next run is. Runs that a
// test never asks for are kept, so a long stream should drain them.
func (c *Corrupter) NextInjection() (Injection, bool) {
	if len(c.pending) == 0 {
		return Injection{}, false
	}
	in := c.pending[0]
	c.pending = c.pending[1:]
	if len(c.pending) == 0 {
		c.pending = c.pending[:0:0]
	}
	return in, true
}

// Read implements io.Reader.
func (c *Corrupter) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if c.off == len(c.buf) {
			if !c.next() {
				break
			}
		}
		m := copy(p[n:], c.buf[c.off:])
		c.off += m
		n += m
	}
	if n == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return n, nil
}

// next fills buf with an optional garbage run and the next record of the
// generator. It returns false at the end of the generator's stream.
func (c *Corrupter) next() bool {
	if !c.gen.nextRecord() {
		return false
	}
	c.buf = c.buf[:0]
	c.off = 0
	if c.gen.records > 1 && c.rng.Float64() < c.rate {
		c.buf = c.garbage(c.rng, c.buf)
		if len(c.buf) > 0 {
			c.pending = append(c.pending, Injection{
				Offset: c.written,
				Len:    len(c.buf),
				Record: c.gen.records - 1,
			})
			c.injected++
		}
	}
	c.buf = append(c.buf, c.gen.buf...)
	c.gen.off = len(c.gen.buf)
	c.written += int64(len(c.buf))
	return true
}
