package leanread

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestImportsOnlyStandardLibrary checks that the non-test files of the package
// import only the standard library. Package cursor imports this package, and
// the cursor must not depend on the rest of the module (docs/specs/cursor.md,
// "Independence").
func TestImportsOnlyStandardLibrary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(imp, "/")
			if strings.Contains(first, ".") || imp == "C" {
				t.Errorf("%s imports %q; the package must import only the standard library", name, imp)
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no non-test Go files found")
	}
}

// TestOfWithoutHook checks that Of reports no read path while Hook is nil.
// This test binary does not link package cursor.
func TestOfWithoutHook(t *testing.T) {
	if Hook != nil {
		t.Skip("package cursor is linked")
	}
	if r, ok := Of(struct{}{}); ok || r != nil {
		t.Errorf("Of without a hook = %v, %v; want nil, false", r, ok)
	}
}
