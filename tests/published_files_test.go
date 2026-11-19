package tests

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// internalHost is the host name of the internal tracker. It is built from
// parts so that this file, which is published, does not hold it.
var internalHost = strings.Join([]string{"lab", "xiam", "dev"}, ".")

// TestPublishedFilesLinkNoInternalHost checks that no published file names the
// internal host. A published file is a tracked file that `git archive` exports:
// every file that `git ls-files` lists and that the `export-ignore` attribute
// does not remove. The test reads the files from the working tree, so it also
// sees a change that is not committed yet.
//
// The test skips when git is not installed or when it does not run inside a
// git checkout, for example from the module cache. Any other error of git
// fails the test, and so does an empty list of files.
func TestPublishedFilesLinkNoInternalHost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed, so the published files cannot be listed: %v", err)
	}
	var stderr bytes.Buffer
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("not inside a git checkout, so the published files cannot be listed: %v: %s",
			err, strings.TrimSpace(stderr.String()))
	}
	root := strings.TrimSpace(string(out))

	tracked := gitNulList(t, root, nil, "ls-files", "-z")
	if len(tracked) == 0 {
		t.Fatal("git ls-files listed no file")
	}

	// `git check-attr -z` prints three fields for each path: the path, the
	// attribute and its value.
	attrs := gitNulList(t, root, []byte(strings.Join(tracked, "\x00")+"\x00"),
		"check-attr", "-z", "--stdin", "export-ignore")
	if len(attrs) != 3*len(tracked) {
		t.Fatalf("git check-attr returned %d fields for %d files", len(attrs), len(tracked))
	}
	ignored := make(map[string]bool)
	for i := 0; i < len(attrs); i += 3 {
		if attrs[i+1] != "export-ignore" {
			t.Fatalf("git check-attr returned attribute %q for %q", attrs[i+1], attrs[i])
		}
		if attrs[i+2] == "set" {
			ignored[attrs[i]] = true
		}
	}

	needle := []byte(internalHost)
	scanned := 0
	for _, name := range tracked {
		if ignored[name] {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // deleted in the working tree
		}
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		var data []byte
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				t.Fatalf("readlink %s: %v", name, err)
			}
			data = []byte(target)
		case info.IsDir():
			continue // a submodule; git archive does not export its content
		default:
			if data, err = os.ReadFile(path); err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
		}
		scanned++
		if n := bytes.Count(data, needle); n > 0 {
			t.Errorf("%s is published and names the internal host %d times", name, n)
		}
	}
	if scanned == 0 {
		t.Fatal("no published file was scanned")
	}
	if ignored["go.mod"] {
		t.Fatal("go.mod is marked export-ignore; the export rule is not the one this test expects")
	}
	t.Logf("scanned %d published files; %d tracked files are export-ignore", scanned, len(ignored))
}

// gitNulList runs git in dir with stdin and returns its NUL-separated output.
func gitNulList(t *testing.T, dir string, stdin []byte, args ...string) []string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	out = bytes.TrimSuffix(out, []byte{0})
	if len(out) == 0 {
		return nil
	}
	return strings.Split(string(out), "\x00")
}
