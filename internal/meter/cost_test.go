package meter_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/internal/meter"
)

// costInputs caches the input of each case, so that the two lanes of a case,
// and the runs of one benchmark, generate it once.
var costInputs = map[string][]byte{}

func costInput(c costCase) []byte {
	in, ok := costInputs[c.name]
	if !ok {
		in = c.input()
		costInputs[c.name] = in
	}
	return in
}

// sinkToken keeps the compiler from dropping the tokens.
var sinkToken *bytescan.Token

// BenchmarkWrapperCost measures the cost of the wrappers. Each case runs in
// two lanes that differ only in the wrappers: control reads the input and
// lexes it with no wrapper; wrapped reads it through a Reader and lexes it
// through a Source. The input is in memory before the timer starts, and the
// rate is in bytes of input per second.
//
// To compare the lanes, run the benchmark several times in a row, so that the
// runs of the two lanes alternate, and compare them with benchstat:
//
//	for i in $(seq 10); do go test -run '^$' -bench WrapperCost -count 1 ./internal/meter; done > cost.txt
//	benchstat -col /lane cost.txt
func BenchmarkWrapperCost(b *testing.B) {
	for _, c := range costCases {
		c := c
		b.Run("case="+c.name+"/lane=control", func(b *testing.B) {
			in := costInput(c)
			b.ReportAllocs()
			b.SetBytes(int64(len(in)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sc := bytescan.NewWithCursor(c.newCursor(bytes.NewReader(in)))
				c.addRules(sc)
				drainCost(b, sc)
			}
		})
		b.Run("case="+c.name+"/lane=wrapped", func(b *testing.B) {
			in := costInput(c)
			b.ReportAllocs()
			b.SetBytes(int64(len(in)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var counts meter.Counts
				sc := bytescan.NewWithCursor(c.newCursor(meter.NewReader(bytes.NewReader(in), &counts)))
				c.addRules(sc)
				drainCost(b, meter.NewSource(sc, &counts))
				if counts.Bytes != uint64(len(in)) || counts.TokenBytes != uint64(len(in)) {
					b.Fatalf("counted %d bytes and %d token bytes of %d", counts.Bytes, counts.TokenBytes, len(in))
				}
			}
		})
	}
}

// drainCost reads src to io.EOF.
func drainCost(b *testing.B, src bytescan.Source) {
	for {
		tok, err := src.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			b.Fatal(err)
		}
		sinkToken = tok
	}
}
