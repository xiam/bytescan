package streamtest_test

import (
	"flag"
	"testing"

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
	if f == nil {
		t.Fatal("the -streamtest.long flag is not registered")
	}
	if *long || streamtest.Long() {
		t.Skip("run without -long and -streamtest.long")
	}
	if err := flag.Set("long", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flag.Set("long", "false") })
	if streamtest.Long() || streamtest.DefaultBudget() == streamtest.LongBudget {
		t.Fatal("the importing test's -long flag selected the long budget")
	}
	if err := flag.Set("streamtest.long", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flag.Set("streamtest.long", "false") })
	if !streamtest.Long() || streamtest.DefaultBudget() != streamtest.LongBudget {
		t.Fatalf("with -streamtest.long, DefaultBudget is %d, want %d", streamtest.DefaultBudget(), streamtest.LongBudget)
	}
}
