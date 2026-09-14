package journal

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/workspace"
)

// newTestWorkspace builds a workspace whose journal directory already
// exists, without touching git.
func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, ".main")
	if err := os.MkdirAll(filepath.Join(main, "journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: main,
	}
}

// writeEntry writes a journal entry directly, bypassing the editor.
func writeEntry(t *testing.T, ws *workspace.Workspace, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws.JournalDir(), filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTodayFilename(t *testing.T) {
	now := time.Date(2026, 9, 13, 15, 4, 5, 0, time.Local)
	if got := TodayFilename(now); got != "2026-09-13.md" {
		t.Errorf("TodayFilename(%v) = %q, want %q", now, got, "2026-09-13.md")
	}
}

func TestOpenToday(t *testing.T) {
	ws := newTestWorkspace(t)

	path, err := OpenToday(ws)
	if err != nil {
		t.Fatalf("OpenToday: %v", err)
	}
	want := filepath.Join(ws.JournalDir(), TodayFilename(time.Now()))
	if path != want {
		t.Errorf("OpenToday path = %q, want %q", path, want)
	}

	// The file itself must NOT be created — the editor does that on save.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("OpenToday must not create the entry file")
	}

	// Calling it twice is safe (MkdirAll is idempotent).
	path2, err := OpenToday(ws)
	if err != nil {
		t.Fatalf("OpenToday (second call): %v", err)
	}
	if path2 != path {
		t.Errorf("second OpenToday = %q, want %q", path2, path)
	}
}

func TestList(t *testing.T) {
	ws := newTestWorkspace(t)
	// Write entries out of order, plus the .gitkeep that init drops in the
	// directory and a stray non-entry file. List must sort the entries
	// chronologically and ignore everything else.
	writeEntry(t, ws, "2026-09-14.md", "Second\n")
	writeEntry(t, ws, "2026-09-13.md", "First\n")
	writeEntry(t, ws, "notes.txt", "not an entry\n")
	if err := os.WriteFile(filepath.Join(ws.JournalDir(), ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := List(ws)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"2026-09-13.md", "2026-09-14.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestListMissingDir(t *testing.T) {
	// A workspace without a journal directory is not an error: it lists
	// nothing.
	ws := &workspace.Workspace{MainWorktree: t.TempDir()}

	got, err := List(ws)
	if err != nil {
		t.Fatalf("List with missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty slice", got)
	}
}

func TestRead(t *testing.T) {
	ws := newTestWorkspace(t)
	content := "# Day one\n\nSome notes.\n"
	writeEntry(t, ws, "2026-09-13.md", content)

	got, err := Read(ws, "2026-09-13.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != content {
		t.Errorf("Read = %q, want %q", got, content)
	}
}

func TestFormatLongDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2026-09-13", "Sunday, September 13, 2026"},
		{"2026-01-01", "Thursday, January 1, 2026"},
		{"not-a-date", "not-a-date"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := FormatLongDate(tt.input); got != tt.want {
				t.Errorf("FormatLongDate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}