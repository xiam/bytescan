package streamtest_test

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xiam/bytescan/streamtest"
)

// long is a -long flag of a test that imports streamtest, as a user's own
// long-test switch would be. The package registers -streamtest.long, so the
// two flags do not collide and this test binary starts.
var long = flag.Bool("long", false, "a -long flag of the importing test")

// TestFlag checks that -long belongs to the importing test, and that
// -streamtest.long, and only it, selects LongBudget.
func TestFlag(t *testing.T) {
	f := flag.Lookup("streamtest.long")
	require.False(t, f == nil, "the -streamtest.long flag is not registered")
	if *long || streamtest.Long() {
		t.Skip("run without -long and -streamtest.long")
	}
	{
		err := flag.Set("long", "true")
		require.NoError(t, err)
	}
	t.Cleanup(func() { flag.Set("long", "false") })
	require.False(t, streamtest.Long() || streamtest.DefaultBudget() == streamtest.LongBudget, "the importing test's -long flag selected the long budget")
	{
		err := flag.Set("streamtest.long", "true")
		require.NoError(t, err)
	}
	t.Cleanup(func() { flag.Set("streamtest.long", "false") })
	if !streamtest.Long() || streamtest.DefaultBudget() != streamtest.LongBudget {
		require.FailNowf(t, "unexpected test state", "with -streamtest.long, DefaultBudget is %d, want %d", streamtest.DefaultBudget(), streamtest.LongBudget)
	}
}
