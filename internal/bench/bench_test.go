package bench

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/internal/counters"
)

// laneFlag selects the lane of BenchmarkCase. The default is control in the
// normal build and instrumented in the build with the tag bench.
var laneFlag = flag.String("lane", "", "the lane of BenchmarkCase: control or wrapped in the normal build, instrumented with -tags bench")

// lane is the lane of this test binary, set by TestMain.
var lane Lane

// tagged reports whether the build has the engine counters.
func tagged() bool {
	_, ok := counters.OfScanner(bytescan.NewWithCursor(textCursor(nil)))
	return ok
}

// TestMain sets the lane and prints it as a configuration line ("lane:
// control"), so that benchstat -col lane over the output of several binaries
// splits the samples by lane.
func TestMain(m *testing.M) {
	flag.Parse()
	lane = Control
	if tagged() {
		lane = Instrumented
	}
	if *laneFlag != "" {
		l, err := parseLane(*laneFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		lane = l
	}
	fmt.Printf("lane: %s\n", lane)
	os.Exit(m.Run())
}

// inputs caches the input of each case in this process.
var inputs = map[string][]byte{}

func input(tb testing.TB, c Case) []byte {
	tb.Helper()
	in, ok := inputs[c.name]
	if !ok {
		var err error
		if in, err = c.input(); err != nil {
			tb.Fatal(err)
		}
		inputs[c.name] = in
	}
	return in
}

// BenchmarkCase runs every case of namedCases in the lane of -lane. Its timer
// runs only over the interval of the harness: from before the first Next to
// after the last Next returns. The setup of the scanner is outside it. In
// the wrapped and the instrumented lanes it logs the counts of the run as a
// line "result {...}" (JSON of result) that the runner reads.
//
// The runner internal/bench/cmd/benchreport runs it with -test.benchtime 1x,
// one case and one lane per process.
func BenchmarkCase(b *testing.B) {
	for _, c := range namedCases() {
		c := c
		b.Run(c.name, func(b *testing.B) {
			in := input(b, c)
			b.ReportAllocs()
			b.StopTimer()
			b.ResetTimer()
			var first result
			for i := 0; i < b.N; i++ {
				res, err := lex(c, lane, in, b.StartTimer, b.StopTimer)
				if err != nil {
					b.Fatal(err)
				}
				if i == 0 {
					first = res
				} else if res != first {
					b.Fatalf("run %d counted %+v, run 0 counted %+v", i, res, first)
				}
			}
			if lane != Control {
				out, err := json.Marshal(first)
				if err != nil {
					b.Fatal(err)
				}
				b.Logf("result %s", out)
			}
		})
	}
}

// BenchmarkReference runs the outside references on every text case:
// BenchmarkReference/<case>/<tool>, with the tools textscanner and regexp.
// Its timer runs only from before the first token to after the last one, as
// that of BenchmarkCase. It logs the counts of the run as a line
// "reference {...}" (JSON of refResult) that the runner reads. The lane of
// the binary does not change it.
func BenchmarkReference(b *testing.B) {
	for _, c := range cases() {
		c := c
		refs := references(c)
		if refs == nil {
			continue
		}
		b.Run(c.name, func(b *testing.B) {
			for _, t := range refs {
				t := t
				b.Run(string(t), func(b *testing.B) {
					in := input(b, c)
					b.ReportAllocs()
					b.StopTimer()
					b.ResetTimer()
					var first refResult
					for i := 0; i < b.N; i++ {
						res, err := refLex(c, t, in, b.StartTimer, b.StopTimer)
						if err != nil {
							b.Fatal(err)
						}
						if i == 0 {
							first = res
						} else if res != first {
							b.Fatalf("run %d counted %+v, run 0 counted %+v", i, res, first)
						}
					}
					out, err := json.Marshal(first)
					if err != nil {
						b.Fatal(err)
					}
					b.Logf("reference %s", out)
				})
			}
		})
	}
}
