package streamtest

import (
	"bytes"
	"io"
	"math/rand"
)

// Records returns the first records of record, seeded with seed, until they
// hold at least n bytes. A test puts them before a generated stream with
// Prefix, as a warm-up: the first tokens grow the live heap once, as the
// scanners and the cursor allocate on first use, and a sampling interval of n
// bytes (WithSampleEvery) takes the baseline after them.
func Records(seed int64, record RecordFunc, n int) []byte {
	rng := rand.New(rand.NewSource(seed))
	var b []byte
	for i := 0; len(b) < n; i++ {
		b = record(rng, i, b)
	}
	return b
}

// Prefix returns a Source that reads each of head in order and then src. Its
// budget is the length of head plus the budget of src. A test uses it to put
// a warm-up from Records, or a hand-made record such as one with the largest
// length a format allows, before a generated stream; the bytes of src then
// start at the summed length of head. A prefix of a source with the default
// budget counts as a source with the default budget.
func Prefix(src Source, head ...[]byte) Source {
	rs := make([]io.Reader, 0, len(head)+1)
	n := int64(0)
	for _, h := range head {
		rs = append(rs, bytes.NewReader(h))
		n += int64(len(h))
	}
	return prefixed{io.MultiReader(append(rs, src)...), n + src.Budget(), usesDefaultBudget(src)}
}

// prefixed is the Source of Prefix.
type prefixed struct {
	io.Reader
	budget  int64
	implied bool // src uses the default budget
}

func (p prefixed) Budget() int64 { return p.budget }

func (p prefixed) usesDefaultBudget() bool { return p.implied }
