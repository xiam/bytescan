package streamtest

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/xiam/bytescan"
)

// minSampleEvery is the smallest default sampling interval, in bytes.
const minSampleEvery = 4 << 10

// ErrCeiling is wrapped by the error of a run whose live heap grew past the
// ceiling.
var ErrCeiling = errors.New("streamtest: live heap grew past the ceiling")

// Option configures Run.
type Option func(*config)

type config struct {
	ceiling       int64
	sampleEvery   int64
	baselineAfter int64 // token progress, in bytes, before the baseline sample
	onToken       func(*bytescan.Token)
	onSample      func(pos, read int64) // called at each sample; for the self-tests
}

// WithCeiling sets the heap ceiling in bytes: the most that a sample may
// exceed the baseline sample by. Zero or less keeps the default:
// DefaultCeiling of the source's budget, or 48 KiB under the race detector
// for a stream with the implicit default budget.
func WithCeiling(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.ceiling = n
		}
	}
}

// WithSampleEvery sets the sampling interval: Run samples the live heap each
// time the scanner has lexed another n bytes of the stream. Zero or less keeps
// the default, budget/16 but at least 4 KiB. An interval at least as long as
// the stream leaves only the end-of-stream sample, and Run fails. Where the
// baseline waits for the first cursor buffer (see the package documentation),
// it is the first sample of this interval after that point.
func WithSampleEvery(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.sampleEvery = n
		}
	}
}

// WithTokenFunc calls fn with every token, in order, as Run reads it. A
// corruption test uses it to check tokens against the reported injections
// without keeping the stream.
func WithTokenFunc(fn func(*bytescan.Token)) Option {
	return func(c *config) {
		c.onToken = fn
	}
}

// Result is what Run measured.
type Result struct {
	Bytes   int64                        // bytes the scanner read from the source
	Tokens  int64                        // tokens, all types
	Counts  map[bytescan.TokenType]int64 // tokens by type
	Elapsed time.Duration                // wall time from the first Next to io.EOF, without the heap samples

	Baseline  uint64 // live heap at the first sample, in bytes
	MaxGrowth int64  // the largest sample minus the baseline, in bytes
	Samples   int    // samples taken, the baseline included
	Ceiling   int64  // the ceiling the run enforced, in bytes
}

// MiBPerSecond returns the throughput of the run in MiB per second.
func (r Result) MiBPerSecond() float64 {
	if r.Elapsed <= 0 {
		return 0
	}
	return float64(r.Bytes) / (1 << 20) / r.Elapsed.Seconds()
}

// Run lexes src to the end with the bytescan.Source that newScanner builds over
// it: a *bytescan.Scanner, or a filter or driver above Next. It fails t if
// the scanner returns an error other than io.EOF, if a heap sample exceeds the
// baseline by more than the ceiling, if the scanner returns io.EOF before it has
// read all of src, or if it took fewer than two heap samples. It logs bytes,
// tokens, elapsed time, throughput and the largest heap growth, and returns
// what it measured.
//
// Under the race detector, a stream with the implicit default budget (a
// Generator built without WithBudget, a Corrupter over one, or a Prefix of
// either, in a run without -streamtest.long) gets a ceiling of 48 KiB unless WithCeiling sets one, and its
// baseline is the first sample after the tokens have passed the first 64 KiB
// cursor buffer. Every other stream gets DefaultCeiling of its budget and
// the first sample as its baseline.
func Run[L bytescan.Source](t testing.TB, newScanner func(io.Reader) L, src Source, opts ...Option) Result {
	t.Helper()
	res, err := run(newScanner, src, opts...)
	if err != nil {
		t.Fatalf("streamtest: %v", err)
	}
	t.Logf("%s: %d bytes, %d tokens in %v, %.2f MiB/s; heap growth at most %.1f KiB in %d samples (ceiling %.1f KiB, race detector: %v)",
		t.Name(), res.Bytes, res.Tokens, res.Elapsed.Round(time.Millisecond), res.MiBPerSecond(),
		float64(res.MaxGrowth)/(1<<10), res.Samples, float64(res.Ceiling)/(1<<10), raceDetector)
	return res
}

// countingReader counts the bytes read through it and notes io.EOF.
type countingReader struct {
	r   io.Reader
	n   int64
	eof bool
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if err == io.EOF {
		c.eof = true
	}
	return n, err
}

// run is Run without the testing.TB, so the self-tests can check its errors.
func run[L bytescan.Source](newScanner func(io.Reader) L, src Source, opts ...Option) (Result, error) {
	budget := src.Budget()
	cfg := config{
		ceiling:       defaultCeiling(src),
		sampleEvery:   max(budget/16, minSampleEvery),
		baselineAfter: defaultBaselineAfter(src),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	res := Result{
		Counts:  map[bytescan.TokenType]int64{},
		Ceiling: cfg.ceiling,
	}
	cr := &countingReader{r: src}
	lx := newScanner(cr)

	var (
		ms       runtime.MemStats
		sampling time.Duration // time spent in samples, left out of Elapsed
	)
	sample := func(pos int64) error {
		t0 := time.Now()
		defer func() { sampling += time.Since(t0) }()
		runtime.GC()
		runtime.ReadMemStats(&ms)
		if cfg.onSample != nil {
			cfg.onSample(pos, cr.n)
		}
		res.Samples++
		if res.Samples == 1 {
			res.Baseline = ms.HeapAlloc
			return nil
		}
		growth := int64(ms.HeapAlloc) - int64(res.Baseline)
		res.MaxGrowth = max(res.MaxGrowth, growth)
		if growth > cfg.ceiling {
			return fmt.Errorf("%w: %.1f KiB above the baseline at sample %d, %d bytes read (ceiling %.1f KiB)",
				ErrCeiling, float64(growth)/(1<<10), res.Samples, cr.n, float64(cfg.ceiling)/(1<<10))
		}
		return nil
	}

	next := cfg.sampleEvery
	var pos int64 // the offset of the last token: the progress of the scanner
	start := time.Now()
	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			res.Bytes, res.Elapsed = cr.n, time.Since(start)-sampling
			return res, fmt.Errorf("Next after %d bytes and %d tokens: %w", cr.n, res.Tokens, err)
		}
		res.Tokens++
		res.Counts[l.Type()]++
		if cfg.onToken != nil {
			cfg.onToken(l)
		}
		// The cursor reads ahead of the scanner, so progress is the position of
		// the token, not the bytes read from the source. A token built
		// without a span reports byte offset 0, so take its rune offset too.
		if pos = int64(max(l.ByteOffset(), l.Offset())); pos >= next {
			for next <= pos {
				next += cfg.sampleEvery
			}
			if pos < cfg.baselineAfter {
				continue // before the baseline point: no sample
			}
			if err := sample(pos); err != nil {
				res.Bytes, res.Elapsed = cr.n, time.Since(start)-sampling
				return res, err
			}
		}
	}
	res.Elapsed = time.Since(start) - sampling
	res.Bytes = cr.n
	if !cr.eof {
		return res, fmt.Errorf("the scanner returned io.EOF after %d bytes, before the end of the stream", cr.n)
	}

	// A last sample at the end of the stream, while the scanner and its cursor
	// are still reachable: a stream no longer than one reader buffer shows its
	// growth only here.
	err := sample(pos)
	runtime.KeepAlive(lx)
	if err == nil && res.Samples < 2 {
		if cfg.baselineAfter > 0 {
			err = fmt.Errorf("took %d heap sample over %d bytes, so no growth was checked: the baseline is the first sample after %d bytes of tokens (one cursor buffer), and the stream gave no sample after it (it ended at %d bytes of tokens; sampling interval %d bytes)",
				res.Samples, res.Bytes, cfg.baselineAfter, pos, cfg.sampleEvery)
		} else {
			err = fmt.Errorf("took %d heap sample over %d bytes, so no growth was checked: the sampling interval (%d bytes) is longer than the stream",
				res.Samples, res.Bytes, cfg.sampleEvery)
		}
	}
	return res, err
}
