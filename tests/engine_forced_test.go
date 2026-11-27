package tests

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOraclesWithEngineForced runs the oracle tests of this package, and the
// reference check of the harness (TestReferencesLex in internal/bench),
// again in the build with the tag coreengine. The normal build runs every
// pure core rule on its table (docs/specs/table-compiler.md, form A); the
// tagged build runs every core rule on the engine. So each oracle tests
// both paths.
func TestOraclesWithEngineForced(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	if coreEngineTag {
		t.Skip("this build forces the engine already")
	}
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goCmd, "test", "-count=1", "-tags", "coreengine",
		"-run", "Oracle|^TestReferencesLex$", ".", "../internal/bench")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go test -tags coreengine: %v\n%s", err, out)
	t.Logf("go test -tags coreengine:\n%s", out)
}
