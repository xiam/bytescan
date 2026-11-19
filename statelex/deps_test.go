package statelex

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCoreDoesNotImportStatelex checks with go list -deps that the root
// package, cursor, modes, rules and bytesource do not depend on statelex, so
// a program that only reads a stream does not build it or link it.
func TestCoreDoesNotImportStatelex(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	const self = "github.com/xiam/bytescan/statelex"
	core := []string{
		"github.com/xiam/bytescan",
		"github.com/xiam/bytescan/cursor",
		"github.com/xiam/bytescan/modes",
		"github.com/xiam/bytescan/rules/...",
		"github.com/xiam/bytescan/bytesource",
	}
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	out, err := exec.Command(goCmd, append([]string{"list", "-deps"}, core...)...).CombinedOutput()
	require.NoError(t, err, "%s", out)
	deps := strings.Fields(string(out))
	require.Contains(t, deps, "github.com/xiam/bytescan/modes")
	for _, dep := range deps {
		require.False(t, strings.HasPrefix(dep, self), "the core depends on %s", dep)
	}
}
