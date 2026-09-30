// Package journal implements the daily journal: plain markdown files in
// <workspace>/.main/journal/, one per local day, named YYYY-MM-DD.md.
//
// Journal entries are work product, not state: they never touch
// .projects.json and no journal command commits anything. The package is
// deliberately stateless — plain package-level functions, no Service struct —
// because journal operations are just file I/O with no shared state to
// carry. A struct would add ceremony without holding any data.
package journal

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// TodayFilename returns the journal filename for a local date, e.g.
// "2026-09-13.md". now is injectable so tests can fix the reference date.
func TodayFilename(now time.Time) string {
	return now.Format("2006-01-02") + ".md"
}

// OpenToday ensures the journal directory exists and returns the path to
// today's entry. The file itself is created by the editor on save — grind
// does not pre-create it or write a template.
func OpenToday(ws *workspace.Workspace) (string, error) {
	dir := ws.JournalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", grinderr.WrapSystem(err, "create journal directory %s", dir)
	}
	return filepath.Join(dir, TodayFilename(time.Now())), nil
}

// List returns journal entry filenames in chronological order (oldest
// first). A missing journal directory is not an error: it returns an empty
// slice. Non-entry files (like the .gitkeep that init drops in the
// directory) are ignored.
func List(ws *workspace.Workspace) ([]string, error) {
	entries, err := os.ReadDir(ws.JournalDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, grinderr.WrapSystem(err, "read journal directory %s", ws.JournalDir())
	}

	var filenames []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		filenames = append(filenames, entry.Name())
	}
	// Lexicographic order is chronological for YYYY-MM-DD filenames.
	sort.Strings(filenames)
	return filenames, nil
}

// Read returns a journal entry's raw markdown content, unmodified.
func Read(ws *workspace.Workspace, filename string) (string, error) {
	path := filepath.Join(ws.JournalDir(), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", grinderr.WrapSystem(err, "read journal entry %s", path)
	}
	return string(data), nil
}

// FormatLongDate renders a YYYY-MM-DD date as a long English date, e.g.
// "2026-09-13" -> "Sunday, September 13, 2026". Unparseable input is
// returned unchanged (defensive; the filenames always parse).
func FormatLongDate(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Format("Monday, January 2, 2006")
}
