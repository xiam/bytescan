//go:build !bench

package counters_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan"
	"github.com/xiam/bytescan/cursor"
	"github.com/xiam/bytescan/internal/counters"
)

// TestNoCountersInTheNormalBuild checks that, in the normal build, no hook is
// set and OfCursor and OfScanner return false (docs/specs/measurement.md, "How
// the harness reads the counters").
func TestNoCountersInTheNormalBuild(t *testing.T) {
	require.False(t, counters.CursorHook != nil || counters.ScannerHook != nil, "a hook is set in the normal build")
	{
		c, ok := counters.OfCursor(cursor.New(strings.NewReader("a")))
		assert.Falsef(t, ok || c != (counters.Cursor{}), "OfCursor = %+v, %v; want the zero Cursor and false", c, ok)
	}
	{
		s, ok := counters.OfScanner(bytescan.New(strings.NewReader("a")))
		assert.Falsef(t, ok || s != (counters.Scanner{}), "OfScanner = %+v, %v; want the zero Scanner and false", s, ok)
	}
}
