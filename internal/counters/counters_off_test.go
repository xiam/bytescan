//go:build !bench

package counters_test

import (
	"strings"
	"testing"

	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/counters"
)

// TestNoCountersInTheNormalBuild checks that, in the normal build, no hook is
// set and OfCursor and OfScanner return false (docs/specs/measurement.md, "How
// the harness reads the counters").
func TestNoCountersInTheNormalBuild(t *testing.T) {
	if counters.CursorHook != nil || counters.ScannerHook != nil {
		t.Fatal("a hook is set in the normal build")
	}
	if c, ok := counters.OfCursor(cursor.New(strings.NewReader("a"))); ok || c != (counters.Cursor{}) {
		t.Errorf("OfCursor = %+v, %v; want the zero Cursor and false", c, ok)
	}
	if s, ok := counters.OfScanner(bytescan.New(strings.NewReader("a"))); ok || s != (counters.Scanner{}) {
		t.Errorf("OfScanner = %+v, %v; want the zero Scanner and false", s, ok)
	}
}
