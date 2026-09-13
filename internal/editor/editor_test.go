package editor

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// writeFakeEditor creates an executable script that writes a fixed line to
// the file path passed as its first argument. Returns the script path.
func writeFakeEditor(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-editor.sh")
	// The script writes the content to the file named by $1. The content is
	// embedded in single quotes; test content must not contain single quotes.
	body := "#!/bin/sh\nprintf '%s\\n' '" + content + "' > \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	return script
}

func TestOpenWritesFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake editor script is POSIX-only")
	}
	editor := writeFakeEditor(t, "edited content")
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := Open(path); err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != "edited content\n" {
		t.Errorf("file content = %q, want %q", string(got), "edited content\n")
	}
}

func TestEditTemp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake editor script is POSIX-only")
	}
	editor := writeFakeEditor(t, "My idea")
	t.Setenv("EDITOR", editor)
	t.Setenv("VISUAL", "")

	got, err := EditTemp("grind-test-", "initial")
	if err != nil {
		t.Fatalf("EditTemp: %v", err)
	}
	if got != "My idea\n" {
		t.Errorf("EditTemp returned %q, want %q", got, "My idea\n")
	}
}

func TestResolve(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "subl")
	if got := resolve(); !reflect.DeepEqual(got, []string{"subl"}) {
		t.Errorf("resolve with VISUAL = %v, want [subl]", got)
	}

	t.Setenv("VISUAL", "")
	if got := resolve(); !reflect.DeepEqual(got, []string{"vi"}) {
		t.Errorf("resolve with no env = %v, want [vi]", got)
	}
}

func TestResolveSplitsFlags(t *testing.T) {
	t.Setenv("EDITOR", "code --wait")
	t.Setenv("VISUAL", "")
	if got := resolve(); !reflect.DeepEqual(got, []string{"code", "--wait"}) {
		t.Errorf("resolve = %v, want [code --wait]", got)
	}
}
