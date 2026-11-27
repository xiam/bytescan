//go:build bench

package bytescan

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/counters"
)

// TestLeanReadPathCounters lexes the inputs of the lean read path tests on
// both paths, in the build with the tag bench, and compares the engine
// counters. The scanner counters are the same except StepBacks and
// StepBackBytes: the lean path carries the symbols read past a token to the
// next token instead of stepping back over them, so on these inputs it steps
// back never. The cursor counters are the same except Locks, Compactions,
// CompactBytes and FloorScans: the lean path takes the reader's lock once for
// each Peek and once for the Seek over the token, not once for each rune, and
// it does not peek the carried symbols again. Each Peek can compact the buffer
// and finds the retention floor, so the lean path compacts and scans the
// checkpoints at most as often. With a buffer of 8 bytes a Peek rarely holds
// more than one multi-byte rune, so the lean path can take more locks there;
// Locks is checked at the default capacity.
func TestLeanReadPathCounters(t *testing.T) {
	inputs := []string{
		"abc def\n",
		`x "a b" "unterminated string that is longer than one batch of runes` + "\n" + `"é世🙂" y`,
		"héllo wörld\n世界\n🙂 --> - -- --->x",
		strings.Repeat("word ", 300) + "\n" + strings.Repeat("é", 400),
	}
	for i, in := range inputs {
		for _, capacity := range []int{8, 64 * 1024} {
			t.Run(fmt.Sprintf("%d/cap%d", i, capacity), func(t *testing.T) {
				a := cursor.NewReader(strings.NewReader(in), cursor.WithCapacity(capacity))
				b := cursor.NewReader(strings.NewReader(in), cursor.WithCapacity(capacity))
				lean := NewWithCursor(a)
				public := NewWithCursor(publicCursor{b})
				var l1, l2 []Symbol
				leanAddRules(lean, &l1)
				leanAddRules(public, &l2)
				leanScan(lean, a, false)
				leanScan(public, b, false)

				ca, _ := counters.OfCursor(a)
				cb, _ := counters.OfCursor(b)
				assert.Falsef(t, capacity > 8 && ca.Locks >= cb.Locks, "Locks: lean %d, public %d; want fewer on the lean path", ca.Locks, cb.Locks)
				assert.Falsef(t, ca.Compactions > cb.Compactions || ca.CompactBytes > cb.CompactBytes || ca.FloorScans > cb.FloorScans, "Compactions, CompactBytes, FloorScans: lean %d, %d, %d, public %d, %d, %d; want at most as many on the lean path", ca.Compactions, ca.CompactBytes, ca.FloorScans, cb.Compactions, cb.CompactBytes, cb.FloorScans)
				ca.Locks, cb.Locks = 0, 0
				ca.Compactions, cb.Compactions = 0, 0
				ca.CompactBytes, cb.CompactBytes = 0, 0
				ca.FloorScans, cb.FloorScans = 0, 0
				assert.Truef(t, ca == cb, "cursor counters:\nlean   %+v\npublic %+v", ca, cb)
				sa, _ := counters.OfScanner(lean)
				sb, _ := counters.OfScanner(public)
				assert.Falsef(t, sa.StepBacks != 0 || sa.StepBackBytes != 0, "StepBacks, StepBackBytes: lean %d, %d; want 0, 0", sa.StepBacks, sa.StepBackBytes)
				sa.StepBacks, sb.StepBacks = 0, 0
				sa.StepBackBytes, sb.StepBackBytes = 0, 0
				assert.Truef(t, sa == sb, "scanner counters:\nlean   %+v\npublic %+v", sa, sb)
			})
		}
	}
}
