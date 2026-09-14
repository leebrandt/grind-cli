package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/journal"
)

func TestEditJournalCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	logFile := recordingEditor(t)
	if _, err := execute(t, fake, "edit", "journal"); err != nil {
		t.Fatalf("edit journal: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	// The editor must have been called with today's journal path. The date
	// is computed from the local clock, not hardcoded, so the test does not
	// rot.
	wantSuffix := filepath.Join(".main", "journal", journal.TodayFilename(time.Now()))
	if !strings.HasSuffix(strings.TrimSpace(string(data)), wantSuffix) {
		t.Errorf("editor arg = %q, want suffix %q", strings.TrimSpace(string(data)), wantSuffix)
	}
}

func TestJournalAliasCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	logFile := recordingEditor(t)
	if _, err := execute(t, fake, "journal"); err != nil {
		t.Fatalf("journal: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	wantSuffix := filepath.Join(".main", "journal", journal.TodayFilename(time.Now()))
	if !strings.HasSuffix(strings.TrimSpace(string(data)), wantSuffix) {
		t.Errorf("editor arg = %q, want suffix %q", strings.TrimSpace(string(data)), wantSuffix)
	}
}

func TestJournalAliasHiddenFromHelp(t *testing.T) {
	out, err := execute(t, &fakeGit{}, "--help")
	if err != nil {
		t.Fatal(err)
	}
	// The alias is the user's secret handshake: it must not appear in the
	// top-level help, even though `edit journal` and `read journal` do.
	if strings.Contains(out, "journal") {
		t.Errorf("help output = %q, must not mention the journal alias", out)
	}
}

// writeJournalEntries creates the given entries directly in .main/journal
// so tests can exercise `read journal` without going through the editor.
func writeJournalEntries(t *testing.T, entries map[string]string) {
	t.Helper()
	dir := filepath.Join(".main", "journal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range entries {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestReadJournalCommand uses content WITH trailing newlines — the normal
// editor-saved shape — to prove the rendered output has exactly one blank
// line between blocks, not two.
func TestReadJournalCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	writeJournalEntries(t, map[string]string{
		"2026-09-13.md": "# Day one\n\nFirst entry.\n",
		"2026-09-14.md": "# Day two\n\nSecond entry.\n",
	})

	out, err := execute(t, fake, "read", "journal")
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	want := "─── Sunday, September 13, 2026 ───\n\n# Day one\n\nFirst entry.\n\n" +
		"─── Monday, September 14, 2026 ───\n\n# Day two\n\nSecond entry.\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestReadJournalReverse(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	writeJournalEntries(t, map[string]string{
		"2026-09-13.md": "First entry content.",
		"2026-09-14.md": "Second entry content.",
	})

	out, err := execute(t, fake, "read", "journal", "-r")
	if err != nil {
		t.Fatalf("read journal -r: %v", err)
	}
	want := "─── Monday, September 14, 2026 ───\n\nSecond entry content.\n\n" +
		"─── Sunday, September 13, 2026 ───\n\nFirst entry content.\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestReadJournalLongReverseFlag(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	writeJournalEntries(t, map[string]string{
		"2026-09-13.md": "First entry content.",
		"2026-09-14.md": "Second entry content.",
	})

	out, err := execute(t, fake, "read", "journal", "--reverse")
	if err != nil {
		t.Fatalf("read journal --reverse: %v", err)
	}
	// The second entry must come first.
	if !strings.HasPrefix(out, "─── Monday, September 14, 2026 ───") {
		t.Errorf("output = %q, want newest first", out)
	}
}

func TestReadJournalEmpty(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "read", "journal")
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if out != "" {
		t.Errorf("output = %q, want empty", out)
	}
}

func TestReadJournalMissingDir(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	// A workspace without a journal directory is not an error: it lists
	// nothing and exits 0.
	if err := os.RemoveAll(filepath.Join(".main", "journal")); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "read", "journal")
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if out != "" {
		t.Errorf("output = %q, want empty", out)
	}
}

// notInWorkspace runs fn from a directory that is not a grind workspace.
func notInWorkspace(t *testing.T, fn func() error) {
	t.Helper()
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	if err := fn(); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "Not in a grind workspace.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestEditJournalNotInWorkspace(t *testing.T) {
	notInWorkspace(t, func() error {
		_, err := execute(t, &fakeGit{}, "edit", "journal")
		return err
	})
}

func TestReadJournalNotInWorkspace(t *testing.T) {
	notInWorkspace(t, func() error {
		_, err := execute(t, &fakeGit{}, "read", "journal")
		return err
	})
}