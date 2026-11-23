//go:build bench

package cursor

import "github.com/xiam/bytescan/internal/counters"

// readerCounters holds the engine counters of a Reader in the build with the
// tag bench (docs/specs/measurement.md, "The engine counters"). The reader's
// lock guards it: every method is called with t.mu held, or from the
// constructor before the reader is shared.
type readerCounters struct {
	c counters.Cursor
}

func init() {
	counters.CursorHook = readerSnapshot
}

// readerSnapshot returns a copy of the counters of v, a *Reader.
func readerSnapshot(v any) (counters.Cursor, bool) {
	t, ok := v.(*Reader)
	if !ok || t == nil {
		return counters.Cursor{}, false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.ctr.c, true
}

// fill counts n bytes written to the buffer by one fill, and one read from
// the source when read is true. A fill that encodes a rune held back by an
// earlier fill reads nothing from the source.
func (rc *readerCounters) fill(read bool, n int) {
	if read {
		rc.c.Fills++
	}
	rc.c.FillBytes += uint64(n)
}

// compaction counts one shift of the buffer that moved the given bytes.
func (rc *readerCounters) compaction(moved int) {
	rc.c.Compactions++
	rc.c.CompactBytes += uint64(moved)
}

// initBuffer records the size of the initial buffer.
func (rc *readerCounters) initBuffer(size int) {
	rc.c.MaxBuffer = max(rc.c.MaxBuffer, uint64(size))
}

// grow counts one growth of the buffer to the given size.
func (rc *readerCounters) grow(size int) {
	rc.c.Grows++
	rc.c.MaxBuffer = max(rc.c.MaxBuffer, uint64(size))
}

// replay counts one reset to a checkpoint that rewound the given bytes.
func (rc *readerCounters) replay(rewound int) {
	rc.c.Replays++
	if rewound > 0 {
		rc.c.ReplayBytes += uint64(rewound)
	}
}

// lock counts one acquisition of the reader's lock by an exported method.
func (rc *readerCounters) lock() {
	rc.c.Locks++
}

// floorScan counts the checkpoints that one search for the retention floor
// visits.
func (rc *readerCounters) floorScan(visited int) {
	rc.c.FloorScans += uint64(visited)
}
