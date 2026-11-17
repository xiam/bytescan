package streamtest_test

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/rules/core"
	"github.com/xiam/bytescan/streamtest"
)

// record writes record i of a stream of lines such as "k3=417\n".
func record(rng *rand.Rand, i int, dst []byte) []byte {
	return fmt.Appendf(dst, "k%d=%d\n", i, rng.Intn(1000))
}

// A Generator writes records until its budget, and a Corrupter inserts
// garbage between them and reports where. In a test, streamtest.Run lexes
// such a stream and checks the live heap; here the stream is lexed by hand to
// show what the scanner sees.
func Example() {
	gen := streamtest.NewGenerator(1, record, streamtest.WithBudget(4<<10))
	src := streamtest.Corrupt(gen, 2, 0.1, streamtest.Garbage("#%&", 1, 8))

	lx := bytescan.NewWithOptions(bufio.NewReader(src), bytescan.WithMaxTokenBytes(streamtest.TextGuard))
	lx.MustAddRule("KEY", core.Sequence(core.Literal("k"), core.RuneRunMax(core.ASCIIDigit, 20)))
	lx.MustAddRule("EQUALS", core.Literal("="))
	lx.MustAddRule("NUMBER", core.RuneRunMax(core.ASCIIDigit, 20))
	lx.MustAddRule("NEWLINE", core.Literal("\n"))

	counts := map[bytescan.TokenType]int{}
	for {
		l, err := lx.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println(err)
			return
		}
		counts[l.Type()]++
	}
	fmt.Println("records:", gen.Records(), "NEWLINE:", counts["NEWLINE"])
	fmt.Println("garbage runs:", src.Injected(), "UNKNOWN:", counts[bytescan.TokenTypeUnknown])
	// Output:
	// records: 475 NEWLINE: 475
	// garbage runs: 54 UNKNOWN: 245
}
