package tests

import (
	"errors"
	"flag"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/bytesource"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/rules/base"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/rules/special"
)

// The soak test proves that the engine stays flat over a stream of
// unbounded length: live heap, goroutines and allocations per token. Three
// workloads lex generated input (soak_gen_test.go) through the public API
// only, and the leak variants prove that every check fails when the property
// breaks.
//
// The default run lexes soakDefaultBytes per workload. A long run is
//
//	go test -timeout 0 -run Soak ./tests -soak=30m
//
// which runs each workload for 30 minutes (-soak=1GiB runs each for 1 GiB of
// input) and logs the heap series. The leak variants always run at the
// default size.

var soakFlag = flag.String("soak", "",
	"run each soak workload for a duration (30m) or an input size (1GiB, 512MiB, 64KiB, or bytes); use with -timeout 0")

const (
	// soakDefaultBytes is the input of each workload in the default run, and
	// soakDefaultBytesRace under the race detector, which lexes about ten
	// times slower.
	soakDefaultBytes     = 256 << 10
	soakDefaultBytesRace = 64 << 10

	// soakSamples is the number of samples after the warm-up in the default
	// run, and soakLongSamples in a -soak run. The warm-up is the first
	// quarter of the run.
	soakSamples     = 16
	soakLongSamples = 64

	// soakFitMinSamples is how many samples the heap slope needs before it is
	// checked.
	soakFitMinSamples = 4

	// The heap grows with the stream when a least-squares line of live heap
	// over input, fitted to the samples, has a slope above soakHeapSlopeMax
	// bytes of heap per byte of input and rises more than soakHeapGrowthMin
	// across the samples. The second bound keeps the noise of a short fit
	// from failing: a few KiB of pooled engine state over a few KiB of input
	// is a steep slope but no growth. A flat engine measures a slope of
	// about zero, and the scanner-per-frame leak 2.5 to 5. A slower leak, below
	// soakHeapSlopeMax, passes the ceiling in a long run instead.
	//
	// The slope is asserted only without the race detector. Under -race the
	// pooled engine state of rules/core settles only after about 256 KiB of
	// input (the mode driver rises from 155 to 232 KiB of live heap, then
	// stays flat over 1 MiB), which is past the whole 64 KiB default run, so
	// the slope there is logged.
	soakHeapSlopeMax  = 1.0 / 64
	soakHeapGrowthMin = 64 << 10

	// soakHeapCeiling is the most live heap above the baseline, which is taken
	// before the workload is built. It covers the cursor's buffer and line
	// table, the scanners and the pooled engine state: the workloads hold 73 to
	// 232 KiB, with or without the race detector.
	soakHeapCeiling = 1 << 20

	// soakHeapGuard stops a workload once its heap, live and garbage, passes
	// this much above the baseline, so a leak fails fast instead of
	// exhausting the machine. It is read every soakGuardSteps steps.
	soakHeapGuard  = 256 << 20
	soakGuardSteps = 1024
)

// Allocation budgets, in allocations per token after the warm-up, measured
// with textreader v0.4.0 and go1.25.4, without the race detector,
// in the default run, with about 15% headroom: mixed tiers 22.22, mode driver
// 3.96, byte stream 4.04. They are asserted only without the race detector:
// sync.Pool drops items under -race, so the counts differ there (26.6,
// 5.0 and 4.6) and are only logged.
const (
	soakMixedAllocBudget = 25.6
	soakModeAllocBudget  = 4.55
	soakByteAllocBudget  = 4.65
)

// soakCheck names one check of the soak harness.
type soakCheck int

const (
	soakCheckNone soakCheck = iota
	soakCheckHeapSlope
	soakCheckHeapCeiling
	soakCheckGoroutines
	soakCheckAllocs
	soakCheckWindow
	soakCheckRelease
)

func (c soakCheck) String() string {
	switch c {
	case soakCheckHeapSlope:
		return "heap slope"
	case soakCheckHeapCeiling:
		return "heap ceiling"
	case soakCheckGoroutines:
		return "goroutines"
	case soakCheckAllocs:
		return "allocations per token"
	case soakCheckWindow:
		return "context window"
	case soakCheckRelease:
		return "release"
	}
	return "none"
}

type soakFailure struct {
	check soakCheck
	msg   string
}

// soakWindow is the tokens the active scanner has committed since its last
// Release, newest last; it keeps markWindow+1 of them, so the one before the
// window is known too.
type soakWindow struct {
	lex [soakWindowSize + 1]*bytescan.Token
	n   int
}

// soakWindowSize is the number of committed tokens whose input a scanner
// retains (markWindow in bytescan.go).
const soakWindowSize = 3

func (w *soakWindow) push(l *bytescan.Token) {
	if w.n < len(w.lex) {
		w.lex[w.n] = l
		w.n++
		return
	}
	copy(w.lex[:], w.lex[1:])
	w.lex[len(w.lex)-1] = l
}

func (w *soakWindow) reset() {
	*w = soakWindow{}
}

// soakWorkload is one workload: a driver over one cursor.
type soakWorkload struct {
	// step lexes the next unit of the stream and returns the input bytes and
	// the tokens it consumed.
	step func() (bytes, tokens int, err error)
	// active returns the scanner that read last and its window. A nil scanner
	// means no scanner holds a window.
	active func() (*bytescan.Scanner, *soakWindow)
	// release calls Release on every scanner of the driver.
	release func()
	cursor  *cursor.Reader
	// allocBudget is the budget in allocations per token.
	allocBudget float64
}

// soakLimits is how long a workload runs: an input size, or a duration when
// dur is set.
type soakLimits struct {
	bytes   int64
	dur     time.Duration
	samples int
	long    bool
}

func soakDefaultLimits() soakLimits {
	n := int64(soakDefaultBytes)
	if raceDetector {
		n = soakDefaultBytesRace
	}
	return soakLimits{bytes: n, samples: soakSamples}
}

// soakLimitsFromFlag returns the limits of -soak, or the default.
func soakLimitsFromFlag(t *testing.T) soakLimits {
	t.Helper()
	v := strings.TrimSpace(*soakFlag)
	if v == "" {
		return soakDefaultLimits()
	}
	l := soakLimits{samples: soakLongSamples, long: true}
	for _, u := range []struct {
		suffix string
		shift  uint
	}{{"GiB", 30}, {"MiB", 20}, {"KiB", 10}} {
		if num, ok := strings.CutSuffix(v, u.suffix); ok {
			n, err := strconv.ParseInt(num, 10, 64)
			if err != nil || n <= 0 {
				t.Fatalf("-soak=%s: bad size", v)
			}
			l.bytes = n << u.shift
			return l
		}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
		l.bytes = n
		return l
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		t.Fatalf("-soak=%s: want a duration (30m) or a size (1GiB)", v)
	}
	l.dur = d
	return l
}

// soakSample is one sample, taken after a forced garbage collection.
type soakSample struct {
	bytes      int64
	tokens     int64
	elapsed    time.Duration
	live       int64 // live heap above the baseline
	goroutines int
	mallocs    uint64
	totalAlloc uint64
}

// soakResult is the outcome of one run of a workload.
type soakResult struct {
	samples  []soakSample
	failures []soakFailure
}

func (r *soakResult) failed(c soakCheck) bool {
	for _, f := range r.failures {
		if f.check == c {
			return true
		}
	}
	return false
}

// runSoak builds a workload with build, after it has taken the baseline, and
// lexes it within limits. It samples at fixed intervals of input, or of time
// for a duration, and checks every sample. It records the first failure of
// each check, and stops at the first failure of stopOn unless stopOn is
// soakCheckNone.
func runSoak(t *testing.T, build func() *soakWorkload, limits soakLimits, stopOn soakCheck) soakResult {
	t.Helper()

	// Everything the harness keeps is allocated before the baseline, so the
	// live heap above it is the workload's alone.
	res := soakResult{samples: make([]soakSample, 0, limits.samples+1)}
	failSeen := make(map[soakCheck]bool, soakCheckRelease+1)

	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	baseHeap := int64(ms.HeapAlloc)
	baseG := runtime.NumGoroutine()

	w := build()

	var bytes, tokens int64
	steps := 0
	start := time.Now()

	fail := func(c soakCheck, format string, args ...any) {
		if failSeen[c] {
			return
		}
		failSeen[c] = true
		res.failures = append(res.failures, soakFailure{check: c, msg: fmt.Sprintf(format, args...)})
	}
	stop := func() bool {
		return stopOn != soakCheckNone && failSeen[stopOn]
	}

	// boundary reports whether sample i is due. Sample 0 ends the warm-up,
	// the first quarter of the run, and the other samples split the rest
	// evenly.
	boundary := func(i int) bool {
		if limits.dur > 0 {
			warm := limits.dur / 4
			return time.Since(start) >= warm+(limits.dur-warm)*time.Duration(i)/time.Duration(limits.samples)
		}
		warm := limits.bytes / 4
		return bytes >= warm+(limits.bytes-warm)*int64(i)/int64(limits.samples)
	}

	for i := 0; i <= limits.samples; i++ {
		for !boundary(i) {
			b, n, err := w.step()
			if err != nil {
				t.Fatalf("after %d bytes and %d tokens: %v", bytes, tokens, err)
			}
			bytes += int64(b)
			tokens += int64(n)
			steps++
			if steps%soakGuardSteps == 0 {
				runtime.ReadMemStats(&ms)
				if int64(ms.HeapAlloc)-baseHeap > soakHeapGuard {
					t.Fatalf("after %d bytes: heap passed %d MiB above the baseline", bytes, soakHeapGuard>>20)
				}
			}
		}

		soakCheckActiveWindow(w, fail)

		runtime.GC()
		runtime.ReadMemStats(&ms)
		s := soakSample{
			bytes:      bytes,
			tokens:     tokens,
			elapsed:    time.Since(start),
			live:       int64(ms.HeapAlloc) - baseHeap,
			goroutines: runtime.NumGoroutine(),
			mallocs:    ms.Mallocs,
			totalAlloc: ms.TotalAlloc,
		}
		res.samples = append(res.samples, s)

		if s.goroutines != baseG {
			fail(soakCheckGoroutines, "sample %d: %d goroutines, %d before the workload", i, s.goroutines, baseG)
		}
		if s.live > soakHeapCeiling {
			fail(soakCheckHeapCeiling, "sample %d: live heap %d KiB above the baseline, ceiling %d KiB",
				i, s.live>>10, soakHeapCeiling>>10)
		}
		if len(res.samples) >= soakFitMinSamples {
			slope := soakSlope(res.samples)
			growth := slope * float64(s.bytes-res.samples[0].bytes)
			if !raceDetector && slope > soakHeapSlopeMax && growth > soakHeapGrowthMin {
				fail(soakCheckHeapSlope, "sample %d: live heap grows %.4f B per input byte, %.0f KiB across the samples; limits %.4f B per byte and %d KiB",
					i, slope, growth/1024, soakHeapSlopeMax, soakHeapGrowthMin>>10)
			}
		}
		if i > 0 && !raceDetector && w.allocBudget > 0 {
			if a := soakAllocsPerToken(res.samples[0], s); a > w.allocBudget {
				fail(soakCheckAllocs, "sample %d: %.2f allocations per token, budget %.2f", i, a, w.allocBudget)
			}
		}
		if stop() {
			return res
		}
	}

	// Release returns the retained input to the baseline: no checkpoint is
	// left, and the last token is no longer readable.
	lx, win := w.active()
	w.release()
	if got := w.cursor.RetainedBytes(); got != 0 {
		fail(soakCheckRelease, "after Release the cursor retains %d bytes, want 0", got)
	}
	if lx != nil && win.n > 0 {
		if _, err := lx.Context(win.lex[win.n-1], 0, 0); !errors.Is(err, cursor.ErrPositionOutOfBuffer) {
			fail(soakCheckRelease, "after Release, Context of the last token returns %v, want ErrPositionOutOfBuffer", err)
		}
	}
	return res
}

// soakCheckActiveWindow checks that the active scanner retains its context
// window and nothing more: the cursor retains exactly the input of the last
// soakWindowSize tokens, Context reaches back to their start and no further,
// and the token before the window is no longer readable.
func soakCheckActiveWindow(w *soakWorkload, fail func(soakCheck, string, ...any)) {
	lx, win := w.active()
	if lx == nil || win.n == 0 {
		if got := w.cursor.RetainedBytes(); got != 0 {
			fail(soakCheckWindow, "no scanner holds a window, but the cursor retains %d bytes", got)
		}
		return
	}
	first := max(win.n-soakWindowSize, 0)
	var want strings.Builder
	retained := 0
	for _, l := range win.lex[first:win.n] {
		want.WriteString(l.Text())
		retained += l.Span().Bytes()
	}
	if got := w.cursor.RetainedBytes(); got != retained {
		fail(soakCheckWindow, "the cursor retains %d bytes, want the %d of the last %d tokens", got, retained, win.n-first)
	}
	last := win.lex[win.n-1]
	got, err := lx.Context(last, 1<<30, 0)
	if err != nil || got != want.String() {
		fail(soakCheckWindow, "Context of the last token reaches %q (%v), want the window %q", got, err, want.String())
	}
	if first > 0 {
		if _, err := lx.Context(win.lex[first-1], 0, 0); !errors.Is(err, cursor.ErrPositionOutOfBuffer) {
			fail(soakCheckWindow, "Context of the token before the window returns %v, want ErrPositionOutOfBuffer", err)
		}
	}
}

// soakSlope is the slope of a least-squares line of live heap over input
// bytes.
func soakSlope(samples []soakSample) float64 {
	var sx, sy, sxx, sxy float64
	n := float64(len(samples))
	for _, s := range samples {
		x, y := float64(s.bytes), float64(s.live)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	d := n*sxx - sx*sx
	if d == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / d
}

func soakAllocsPerToken(from, to soakSample) float64 {
	if to.tokens == from.tokens {
		return 0
	}
	return float64(to.mallocs-from.mallocs) / float64(to.tokens-from.tokens)
}

func soakBytesPerToken(from, to soakSample) float64 {
	if to.tokens == from.tokens {
		return 0
	}
	return float64(to.totalAlloc-from.totalAlloc) / float64(to.tokens-from.tokens)
}

// logSoak logs the throughput, the allocations per token and the heap series
// of a run.
func logSoak(t *testing.T, name string, res soakResult, verbose bool) {
	t.Helper()
	if len(res.samples) == 0 {
		return
	}
	first, last := res.samples[0], res.samples[len(res.samples)-1]
	mib := float64(last.bytes) / (1 << 20)
	secs := last.elapsed.Seconds()
	series := make([]string, 0, len(res.samples))
	for _, s := range res.samples {
		series = append(series, strconv.FormatInt(s.live>>10, 10))
	}
	t.Logf("%s: %.2f MiB, %d tokens in %.2f s (%.2f MiB/s); %.2f allocations and %.0f B per token after the warm-up; "+
		"heap slope %.5f B per input byte; live heap above the baseline (KiB): %s; goroutines %d; race detector: %v",
		name, mib, last.tokens, secs, mib/secs,
		soakAllocsPerToken(first, last), soakBytesPerToken(first, last),
		soakSlope(res.samples), strings.Join(series, " "), last.goroutines, raceDetector)
	if verbose {
		for i, s := range res.samples {
			t.Logf("%s sample %d: %.1f s, %d bytes, %d tokens, live %d KiB, slope %.5f, goroutines %d, allocations per token %.3f",
				name, i, s.elapsed.Seconds(), s.bytes, s.tokens, s.live>>10, soakSlope(res.samples[:i+1]),
				s.goroutines, soakAllocsPerToken(first, s))
		}
	}
}

// runSoakWorkload runs a workload within the limits of -soak and fails the
// test on every failed check.
func runSoakWorkload(t *testing.T, name string, build func() *soakWorkload) {
	limits := soakLimitsFromFlag(t)
	res := runSoak(t, build, limits, soakCheckNone)
	logSoak(t, name, res, limits.long)
	for _, f := range res.failures {
		t.Errorf("%s: %s: %s", name, f.check, f.msg)
	}
}

// TestSoakMixedTiers lexes one stream with a rule set from all three tiers:
// identifiers, numbers, strings and comments (rules/base, code), words and
// punctuation (rules/base, text), and URLs, email addresses and emoji
// (rules/special).
func TestSoakMixedTiers(t *testing.T) {
	runSoakWorkload(t, "mixed tiers", newSoakMixedTiers)
}

func newSoakMixedTiers() *soakWorkload {
	cur := cursor.NewReader(newSoakFragmentSource(59, soakMixedFragments, soakMixedSeps))
	lx := bytescan.NewWithCursor(cur)
	lx.MustAddRule("URL", special.URL())
	lx.MustAddRule("EMAIL", special.Email())
	lx.MustAddRule("EMOJI", special.Emoji())
	lx.MustAddRule("LINE_COMMENT", base.LineComment("//"))
	lx.MustAddRule("BLOCK_COMMENT", base.BlockComment("/*", "*/"))
	lx.MustAddRule("STRING", base.Quoted('"', '\\'))
	lx.MustAddRule("FLOAT", base.Float())
	lx.MustAddRule("INT", base.Integer())
	lx.MustAddRule("IDENT", base.ASCIIIdentifier())
	lx.MustAddRule("WORD", base.Word())
	lx.MustAddRule("WS", base.Whitespace())
	lx.MustAddRule("NL", base.Newline())
	lx.MustAddRule("PUNCT", base.Punctuation())
	lx.MustAddRule("SYMBOL", base.Symbol())

	win := &soakWindow{}
	return &soakWorkload{
		step: func() (int, int, error) {
			l, err := lx.Next()
			if err != nil {
				return 0, 0, err
			}
			if l.Type() == bytescan.TokenTypeUnknown {
				return 0, 0, fmt.Errorf("UNKNOWN token %q at %s", l.Text(), l.Span())
			}
			win.push(l)
			return l.Span().Bytes(), 1, nil
		},
		active:      func() (*bytescan.Scanner, *soakWindow) { return lx, win },
		release:     lx.Release,
		cursor:      cur,
		allocBudget: soakMixedAllocBudget,
	}
}

// TestSoakModeDriver lexes one stream with three cached scanners over one
// cursor, the mode-driver pattern: a code scanner, a string scanner and a comment
// scanner. The driver switches on every quote and comment, and calls Release on
// the scanner it leaves.
func TestSoakModeDriver(t *testing.T) {
	runSoakWorkload(t, "mode driver", newSoakModeDriver)
}

const (
	soakModeCode = iota
	soakModeString
	soakModeComment
)

func newSoakModeDriver() *soakWorkload {
	cur := cursor.NewReader(newSoakFragmentSource(60, soakModeFragments, soakModeSeps))

	code := bytescan.NewWithCursor(cur)
	code.MustAddRule("IDENT", base.ASCIIIdentifier())
	code.MustAddRule("INT", base.Integer())
	code.MustAddRule("OP", core.Rune(core.RuneSet("=+(),;{}")))
	code.MustAddRule("WS", base.Whitespace())
	code.MustAddRule("NL", base.Newline())
	code.MustAddRule("QUOTE", core.Literal(`"`))
	code.MustAddRule("HASH", core.Literal("#"))

	str := bytescan.NewWithCursor(cur)
	str.MustAddRule("CHARS", core.RuneRun(core.RuneSet("\"\\\n").Not()))
	str.MustAddRule("ESCAPE", core.Sequence(core.Literal(`\`), core.Rune(core.RuneSet("\n").Not())))
	str.MustAddRule("QUOTE", core.Literal(`"`))

	comment := bytescan.NewWithCursor(cur)
	comment.MustAddRule("TEXT", core.RuneRun(core.RuneSet("\n").Not()))
	comment.MustAddRule("NL", base.Newline())

	scanners := [...]*bytescan.Scanner{soakModeCode: code, soakModeString: str, soakModeComment: comment}
	mode := soakModeCode
	win := &soakWindow{}

	return &soakWorkload{
		step: func() (int, int, error) {
			lx := scanners[mode]
			l, err := lx.Next()
			if err != nil {
				return 0, 0, err
			}
			if l.Type() == bytescan.TokenTypeUnknown {
				return 0, 0, fmt.Errorf("mode %d: UNKNOWN token %q at %s", mode, l.Text(), l.Span())
			}
			win.push(l)
			next := mode
			switch {
			case mode == soakModeCode && l.Type() == "QUOTE":
				next = soakModeString
			case mode == soakModeCode && l.Type() == "HASH":
				next = soakModeComment
			case mode == soakModeString && l.Type() == "QUOTE":
				next = soakModeCode
			case mode == soakModeComment && l.Type() == "NL":
				next = soakModeCode
			}
			if next != mode {
				lx.Release()
				win.reset()
				mode = next
			}
			return l.Span().Bytes(), 1, nil
		},
		active: func() (*bytescan.Scanner, *soakWindow) { return scanners[mode], win },
		release: func() {
			for _, lx := range scanners {
				lx.Release()
			}
		},
		cursor:      cur,
		allocBudget: soakModeAllocBudget,
	}
}

// TestSoakByteStream lexes a byte stream through the byte-to-rune adapter of
// the binary examples: a scanner reads the header and the trailer of each
// frame, and the driver reads the payload, framed by the length in the
// header, straight from the cursor.
func TestSoakByteStream(t *testing.T) {
	runSoakWorkload(t, "byte stream", func() *soakWorkload { return newSoakByteStream(t, soakLeakNone) })
}

// soakLeak is a deliberate leak injected into the byte-stream workload.
type soakLeak int

const (
	soakLeakNone soakLeak = iota
	// soakLeakScannerPerFrame builds a new scanner for every frame and never
	// calls Release, so the cursor retains the whole stream.
	soakLeakScannerPerFrame
	// soakLeakPinnedBlock holds a block twice the heap ceiling from the first
	// frame on.
	soakLeakPinnedBlock
	// soakLeakGoroutinePerFrame starts a goroutine for every frame that runs
	// until the test ends.
	soakLeakGoroutinePerFrame
	// soakLeakTextPerToken calls Text on every token, an allocation per
	// token.
	soakLeakTextPerToken
	// soakLeakNoReleaseBeforeRead reads the payload without calling Release
	// first, so the scanner's window pins the payload too.
	soakLeakNoReleaseBeforeRead
	// soakLeakNoFinalRelease leaves the scanner unreleased at the end of the
	// run.
	soakLeakNoFinalRelease
)

// Sinks that keep the leaks of the leak variants alive.
var (
	soakPinned   []byte
	soakTextSink string
)

// newSoakByteStream builds the byte-stream workload with one injected leak.
// The goroutines of soakLeakGoroutinePerFrame end when t ends.
func newSoakByteStream(t *testing.T, leak soakLeak) *soakWorkload {
	cur := bytesource.NewCursor(newSoakFrameSource(61))
	header := core.Sequence(
		core.Literal(bytesource.Text("\x7e\xa5")),
		core.Rune(bytesource.AnyByte), core.Rune(bytesource.AnyByte), core.Rune(bytesource.AnyByte))
	newScanner := func() *bytescan.Scanner {
		lx := bytescan.NewWithCursor(cur)
		lx.MustAddRule("HEADER", header)
		lx.MustAddRule("TRAILER", core.Literal(soakFrameTrailer))
		return lx
	}
	lx := newScanner()
	win := &soakWindow{}

	var done chan struct{}
	var wg sync.WaitGroup
	if leak == soakLeakGoroutinePerFrame {
		done = make(chan struct{})
		t.Cleanup(func() {
			close(done)
			wg.Wait()
		})
	}

	next := func(want bytescan.TokenType) (*bytescan.Token, error) {
		l, err := lx.Next()
		if err != nil {
			return nil, err
		}
		if l.Type() != want {
			return nil, fmt.Errorf("got %s %q at %s, want %s", l.Type(), l.Text(), l.Span(), want)
		}
		win.push(l)
		if leak == soakLeakTextPerToken {
			soakTextSink = l.Text()
		}
		return l, nil
	}

	return &soakWorkload{
		step: func() (int, int, error) {
			switch leak {
			case soakLeakScannerPerFrame:
				// The previous scanner is dropped without Release: its
				// checkpoints stay active in the cursor.
				lx = newScanner()
				win.reset()
			case soakLeakPinnedBlock:
				if soakPinned == nil {
					soakPinned = make([]byte, 2*soakHeapCeiling)
				}
			case soakLeakGoroutinePerFrame:
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-done
				}()
			}

			h, err := next("HEADER")
			if err != nil {
				return 0, 0, err
			}
			// The driver decodes the payload length from the header text,
			// as a framed-format driver does; its allocations count in the
			// budget.
			text := []rune(h.Text())
			n := int(text[3])<<8 | int(text[4])
			bytes, tokens := h.Len(), 1

			if leak != soakLeakScannerPerFrame && leak != soakLeakNoReleaseBeforeRead {
				lx.Release()
				win.reset()
			}
			if n > 0 {
				p, err := bytesource.Read(cur, "PAYLOAD", n)
				if err != nil {
					return 0, 0, err
				}
				if p == nil || p.Len() != n {
					return 0, 0, fmt.Errorf("short payload at %s, want %d bytes", h.Span(), n)
				}
				if leak == soakLeakTextPerToken {
					soakTextSink = p.Text()
				}
				bytes += n
				tokens++
			}

			tr, err := next("TRAILER")
			if err != nil {
				return 0, 0, err
			}
			return bytes + tr.Len(), tokens + 1, nil
		},
		active: func() (*bytescan.Scanner, *soakWindow) { return lx, win },
		release: func() {
			if leak != soakLeakNoFinalRelease {
				lx.Release()
			}
		},
		cursor:      cur,
		allocBudget: soakByteAllocBudget,
	}
}

// TestSoakLeakVariants proves that every check of the soak harness fails when
// its property breaks: each variant injects one leak into the byte-stream
// workload, the cheapest one, at the default size, and stops at the first
// failure of the check it targets.
func TestSoakLeakVariants(t *testing.T) {
	cases := []struct {
		name  string
		leak  soakLeak
		check soakCheck
	}{
		{"scanner per frame without Release grows the heap", soakLeakScannerPerFrame, soakCheckHeapSlope},
		{"pinned block passes the heap ceiling", soakLeakPinnedBlock, soakCheckHeapCeiling},
		{"goroutine per frame changes the goroutine count", soakLeakGoroutinePerFrame, soakCheckGoroutines},
		{"Text per token passes the allocation budget", soakLeakTextPerToken, soakCheckAllocs},
		{"payload read without Release is pinned by the window", soakLeakNoReleaseBeforeRead, soakCheckWindow},
		{"scanner left unreleased keeps its input", soakLeakNoFinalRelease, soakCheckRelease},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if raceDetector && (tc.check == soakCheckAllocs || tc.check == soakCheckHeapSlope) {
				t.Skipf("the %s check is not asserted under the race detector", tc.check)
			}
			t.Cleanup(func() {
				soakPinned, soakTextSink = nil, ""
			})
			res := runSoak(t, func() *soakWorkload { return newSoakByteStream(t, tc.leak) }, soakDefaultLimits(), tc.check)
			logSoak(t, tc.name, res, false)
			if !res.failed(tc.check) {
				t.Fatalf("the %s check did not fail", tc.check)
			}
			for _, f := range res.failures {
				t.Logf("expected failure: %s: %s", f.check, f.msg)
			}
		})
	}
}
