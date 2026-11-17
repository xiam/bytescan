package cursor_test

import (
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
)

// TestImportIndependence parses every Go file of cursor and cursor/position
// and fails on an import outside the allowed set. Non-test files may import
// only the standard library and cursor/position. Test files may also import
// cursor itself and testify.
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

			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, spec := range file.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("%s: bad import %s: %v", path, spec.Path.Value, err)
				}
				if !allowedImport(imp, isTest) {
					t.Errorf("%s imports %q; the cursor must import only the standard library and %s%s",
						path, imp, positionPath, testNote(isTest))
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
