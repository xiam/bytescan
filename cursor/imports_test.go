package cursor_test

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The cursor is import-independent: a program can use it without the rest of
// bytescan. These are the only non-standard imports its files may name.
const (
	cursorPath   = "github.com/xiam/bytescan/cursor"
	positionPath = "github.com/xiam/bytescan/cursor/position"
	testifyPath  = "github.com/stretchr/testify"
	countersPath = "github.com/xiam/bytescan/internal/counters"
	leanreadPath = "github.com/xiam/bytescan/internal/leanread"
)

// TestImportIndependence parses every Go file of cursor and cursor/position
// and fails on an import outside the allowed set. Non-test files may import
// only the standard library and cursor/position. Test files may also import
// cursor itself and testify. A non-test file that only the build with the tag
// bench compiles may also import internal/counters
// (docs/specs/measurement.md, "How the harness reads the counters"). The files
// of cursor, and not of cursor/position, may also import internal/leanread, the
// lean read path of the scanner, which imports only the standard library. It is
// the only internal package of the normal build: the owner decided this on
// 2026-11-21 ("Internal hook package"; docs/specs/cursor.md, "Independence").
func TestImportIndependence(t *testing.T) {
	for _, dir := range []string{".", "position"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}

		var checked int
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") {
				continue
			}
			path := filepath.Join(dir, name)
			isTest := strings.HasSuffix(name, "_test.go")

			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly|parser.ParseComments)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, spec := range file.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("%s: bad import %s: %v", path, spec.Path.Value, err)
				}
				if imp == countersPath && !isTest && benchOnly(t, path, file.Comments) {
					continue
				}
				if imp == leanreadPath && dir == "." {
					continue
				}
				if !allowedImport(imp, isTest) {
					t.Errorf("%s imports %q; the cursor must import only the standard library, %s and %s%s",
						path, imp, positionPath, leanreadPath, testNote(isTest))
				}
			}
			if !isTest {
				checked++
			}
		}

		if checked == 0 {
			t.Fatalf("no non-test Go files found in %s", dir)
		}
	}
}

// benchOnly reports whether the //go:build line of a file, among its comments
// before the package clause, compiles the file only in a build with the tag
// bench. Only the exact constraint "//go:build bench" qualifies (see
// benchOnlyExpr).
func benchOnly(t *testing.T, path string, comments []*ast.CommentGroup) bool {
	t.Helper()
	for _, group := range comments {
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				t.Fatalf("%s: bad build constraint %q: %v", path, c.Text, err)
			}
			return benchOnlyExpr(expr)
		}
	}
	return false
}

// benchOnlyExpr reports whether a build constraint is exactly the tag bench.
// A wider expression such as "bench || linux" can compile without the tag, so
// it does not qualify.
func benchOnlyExpr(expr constraint.Expr) bool {
	tag, ok := expr.(*constraint.TagExpr)
	return ok && tag.Tag == "bench"
}

// TestBenchOnlyExpr checks that only the exact constraint "bench" lets a file
// import internal/counters.
func TestBenchOnlyExpr(t *testing.T) {
	cases := map[string]bool{
		"//go:build bench":              true,
		"//go:build !bench":             false,
		"//go:build bench || linux":     false,
		"//go:build bench || windows":   false,
		"//go:build bench || customtag": false,
		"//go:build bench && linux":     false,
		"//go:build linux":              false,
	}
	for line, want := range cases {
		expr, err := constraint.Parse(line)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		if got := benchOnlyExpr(expr); got != want {
			t.Errorf("benchOnlyExpr(%q) = %v, want %v", line, got, want)
		}
	}
}

func allowedImport(imp string, isTest bool) bool {
	if isStandard(imp) || imp == positionPath {
		return true
	}
	if !isTest {
		return false
	}
	return imp == cursorPath || imp == testifyPath || strings.HasPrefix(imp, testifyPath+"/")
}

// isStandard reports whether imp names a standard library package: its first
// path element has no dot. cgo's "C" is not part of the standard library.
func isStandard(imp string) bool {
	if imp == "C" {
		return false
	}
	first, _, _ := strings.Cut(imp, "/")
	return !strings.Contains(first, ".")
}

func testNote(isTest bool) string {
	if !isTest {
		return ""
	}
	return " (test files also " + cursorPath + " and " + testifyPath + ")"
}
