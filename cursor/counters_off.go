//go:build !bench

package cursor

// readerCounters is the empty substitute for the engine counters of a Reader
// (docs/specs/measurement.md, "The build tag"). It has size zero and every
// method has an empty body, so the compiler inlines each call to nothing. The
// build with the tag bench uses counters_on.go instead.
type readerCounters struct{}

func (*readerCounters) fill(read bool, n int) {}
func (*readerCounters) compaction(moved int)  {}
func (*readerCounters) initBuffer(size int)   {}
func (*readerCounters) grow(size int)         {}
func (*readerCounters) replay(rewound int)    {}
func (*readerCounters) lock()                 {}
func (*readerCounters) floorScan(visited int) {}
