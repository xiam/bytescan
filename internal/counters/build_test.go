package counters_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	rootPath   = "github.com/xiam/bytescan"
	cursorPath = "github.com/xiam/bytescan/cursor"
)

// goList runs the go command of the toolchain that runs the test.
func goList(t *testing.T, args ...string) string {
	t.Helper()
	goCmd := filepath.Join(runtime.GOROOT(), "bin", "go")
	out, err := exec.Command(goCmd, append([]string{"list"}, args...)...).CombinedOutput()
	if err != nil {
		require.FailNowf(t, "unexpected test state", "go list %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// goFiles returns the Go files of each package that the build compiles, as go
// list -f '{{.GoFiles}}' reports them.
func goFiles(t *testing.T, tags ...string) map[string][]string {
	t.Helper()
	args := append(tags, "-f", `{{.ImportPath}} {{join .GoFiles " "}}`, rootPath, cursorPath)
	files := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(goList(t, args...)), "\n") {
		fields := strings.Fields(line)
		files[fields[0]] = fields[1:]
	}
	return files
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestNormalBuildHasNoCounters checks the build tag of
// docs/specs/measurement.md, "The build tag": the normal build of bytescan and
// cursor compiles counters_off.go and not counters_on.go, and the build with
// the tag bench compiles the other file of the pair. The test checks both
// builds, whatever the tags of the test binary.
func TestNormalBuildHasNoCounters(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}

	builds := []struct {
		tags      []string
		want, not string
	}{
		{nil, "counters_off.go", "counters_on.go"},
		{[]string{"-tags", "bench"}, "counters_on.go", "counters_off.go"},
	}
	for _, b := range builds {
		files := goFiles(t, b.tags...)
		for _, pkg := range []string{rootPath, cursorPath} {
			if !contains(files[pkg], b.want) {
				assert.Failf(t, "unexpected test state", "go list %v: %s compiles %v, want %s among them", b.tags, pkg, files[pkg], b.want)
			}
			assert.Falsef(t, contains(files[pkg], b.not), "go list %v: %s compiles %s", b.tags, pkg, b.not)
		}
	}
}

// TestCoreDoesNotImportMeasurement checks with go list -deps that, in the
// normal build, the root package, cursor, modes, rules and bytesource depend
// on no package of the measurement code (docs/specs/measurement.md, "How the
// harness reads the counters"). In the build with the tag bench, bytescan and
// cursor import internal/counters.
func TestCoreDoesNotImportMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}

	measurement := []string{
		"github.com/xiam/bytescan/internal/counters",
		"github.com/xiam/bytescan/internal/meter",
		"github.com/xiam/bytescan/internal/bench",
	}
	core := []string{
		rootPath,
		cursorPath,
		"github.com/xiam/bytescan/modes",
		"github.com/xiam/bytescan/rules/...",
		"github.com/xiam/bytescan/bytesource",
	}

	deps := strings.Fields(goList(t, append([]string{"-deps"}, core...)...))
	require.Truef(t, contains(deps, "github.com/xiam/bytescan/modes"), "go list -deps does not list modes: %v", deps)
	for _, dep := range deps {
		for _, m := range measurement {
			assert.Falsef(t, dep == m || strings.HasPrefix(dep, m+"/"), "the normal build of the core depends on %s", dep)
		}
	}

	tagged := strings.Fields(goList(t, "-tags", "bench", "-deps", rootPath, cursorPath))
	if !contains(tagged, measurement[0]) {
		assert.Failf(t, "unexpected test state", "the build with the tag bench does not import %s", measurement[0])
	}
}
