// Package ideas implements the idea lifecycle: create, list, resolve,
// reject, and prune.
//
// Ideas are timestamped markdown files in <workspace>/.main/ideas/. The
// filename is the creation time in local time (YYYYMMDDHHmmss.md), so
// sorting filenames sorts chronologically. Rejected ideas are renamed with
// a "rejected-" prefix rather than deleted, so they survive until pruned.
package ideas

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// ErrAborted signals that the user cancelled idea creation (e.g. left the
// editor without writing a title). The CLI prints "Aborted." and exits 0.
var ErrAborted = errors.New("idea creation aborted")

// editTemp is the editor entry point used by Create. It is a package-level
// variable so tests can substitute a fake editor without launching a real
// one.
var editTemp = editor.EditTemp

// Idea is one idea file with its display metadata.
type Idea struct {
	Number   int    // 0-based index in the filtered, sorted list
	Filename string // file name inside ideas/, e.g. 20260913143022.md
	Title    string // first heading line, with leading #s stripped
	Rejected bool   // filename starts with "rejected-"
}

// String renders an idea for `list ideas`, e.g. "0. My idea" or
// "1. [REJECTED] Bad idea".
func (i Idea) String() string {
	prefix := ""
	if i.Rejected {
		prefix = "[REJECTED] "
	}
	return fmt.Sprintf("%d. %s%s", i.Number, prefix, i.Title)
}

// ListOptions controls which ideas List returns.
type ListOptions struct {
	All      bool // include rejected ideas
	Rejected bool // only rejected ideas
}

// RejectResult carries the information the CLI needs to report a rejection.
type RejectResult struct {
	Number  int
	Title   string
	OldName string
	NewName string
}

// Service performs idea operations against a workspace using the given git
// implementation. The git layer is injected so tests can substitute a fake
// and verify exactly which paths get staged.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// Create writes a new idea file and commits it. When title is empty the
// user composes the idea in their editor; leaving it without a title
// returns ErrAborted and nothing is written.
func (s *Service) Create(ws *workspace.Workspace, title string) (string, error) {
	var body string
	if title == "" {
		edited, err := editTemp("grind-idea-", "\n# First line is the title; add detail below\n")
		if err != nil {
			return "", err
		}
		title, body = parseEditorContent(edited)
		if title == "" {
			return "", ErrAborted
		}
	}

	content := "# " + title + "\n"
	if body != "" {
		content += "\n" + body + "\n"
	}

	// The filename is the creation time in local time. Two ideas created in
	// the same second would collide, so append a counter until the name is
	// free. The "_1" suffix sorts after the plain timestamp, so
	// chronological order is preserved.
	base := time.Now().Format("20060102150405")
	filename := uniqueFilename(ws.IdeasDir(), base)
	path := filepath.Join(ws.IdeasDir(), filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", grinderr.WrapSystem(err, "write idea file %s", path)
	}

	if err := s.Git.Commit(ws.MainWorktree, "Add idea: "+title, "ideas/"+filename); err != nil {
		return "", err
	}
	return filename, nil
}

// uniqueFilename returns base.md, or base_1.md, base_2.md, ... when the
// earlier names are already taken.
func uniqueFilename(dir, base string) string {
	candidate := base + ".md"
	if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
		return candidate
	}
	for i := 1; ; i++ {
		candidate = fmt.Sprintf("%s_%d.md", base, i)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

// List returns the ideas in ideas/, sorted by filename, filtered by opts,
// and numbered 0-based in that sorted order.
func (s *Service) List(ws *workspace.Workspace, opts ListOptions) ([]Idea, error) {
	entries, err := os.ReadDir(ws.IdeasDir())
	if err != nil {
		if os.IsNotExist(err) {
			// A workspace without an ideas dir simply has no ideas.
			return nil, nil
		}
		return nil, grinderr.WrapSystem(err, "read ideas directory %s", ws.IdeasDir())
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
	sort.Strings(filenames)

	var result []Idea
	for _, filename := range filenames {
		rejected := strings.HasPrefix(filename, "rejected-")
		if opts.Rejected && !rejected {
			continue
		}
		if !opts.All && !opts.Rejected && rejected {
			continue
		}

		title, err := titleFromFile(filepath.Join(ws.IdeasDir(), filename))
		if err != nil {
			return nil, err
		}
		result = append(result, Idea{
			Number:   len(result),
			Filename: filename,
			Title:    title,
			Rejected: rejected,
		})
	}
	return result, nil
}

// Resolve returns the non-rejected idea at the given 0-based index.
func (s *Service) Resolve(ws *workspace.Workspace, number int) (Idea, error) {
	ideas, err := s.List(ws, ListOptions{})
	if err != nil {
		return Idea{}, err
	}
	if number < 0 || number >= len(ideas) {
		return Idea{}, grinderr.NewUser(fmt.Sprintf(
			"Idea #%d not found. Run 'grind list ideas' to see available ideas.", number))
	}
	return ideas[number], nil
}

// Reject renames the idea at the given index to rejected-<filename> and
// commits both the old and new paths so the rename is recorded as a single
// change.
func (s *Service) Reject(ws *workspace.Workspace, number int) (RejectResult, error) {
	idea, err := s.Resolve(ws, number)
	if err != nil {
		return RejectResult{}, err
	}
	if idea.Rejected {
		return RejectResult{}, grinderr.NewUser(fmt.Sprintf("Idea #%d is already rejected.", number))
	}

	newName := "rejected-" + idea.Filename
	oldPath := filepath.Join(ws.IdeasDir(), idea.Filename)
	newPath := filepath.Join(ws.IdeasDir(), newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return RejectResult{}, grinderr.WrapSystem(err, "rename idea file %s", oldPath)
	}

	if err := s.Git.Commit(ws.MainWorktree, "Reject idea: "+idea.Title,
		"ideas/"+idea.Filename, "ideas/"+newName); err != nil {
		return RejectResult{}, err
	}

	return RejectResult{
		Number:  number,
		Title:   idea.Title,
		OldName: idea.Filename,
		NewName: newName,
	}, nil
}

// Prune deletes all rejected ideas. Unless yes is true it prompts on stdin
// for confirmation. It prints progress as it goes and commits the deletions.
func (s *Service) Prune(ws *workspace.Workspace, yes bool) error {
	rejected, err := s.List(ws, ListOptions{Rejected: true})
	if err != nil {
		return err
	}
	if len(rejected) == 0 {
		fmt.Println("No rejected ideas to prune.")
		return nil
	}

	fmt.Printf("Found %d rejected idea(s) to prune:\n", len(rejected))
	for _, idea := range rejected {
		fmt.Println("  " + idea.Filename)
	}

	if !yes {
		fmt.Printf("Delete %d rejected idea(s)? [y/N] ", len(rejected))
		reader := bufio.NewReader(os.Stdin)
		answer, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return grinderr.WrapSystem(err, "read confirmation")
		}
		answer = strings.TrimSpace(answer)
		if answer != "y" && answer != "Y" {
			return nil
		}
	}

	paths := make([]string, 0, len(rejected))
	for _, idea := range rejected {
		path := filepath.Join(ws.IdeasDir(), idea.Filename)
		if err := os.Remove(path); err != nil {
			return grinderr.WrapSystem(err, "delete idea file %s", path)
		}
		fmt.Printf("  - Deleted: %s\n", idea.Filename)
		paths = append(paths, "ideas/"+idea.Filename)
	}

	if err := s.Git.Commit(ws.MainWorktree,
		fmt.Sprintf("Prune %d rejected idea(s)", len(rejected)), paths...); err != nil {
		return err
	}
	fmt.Printf("Pruned %d rejected idea(s) and committed to main branch\n", len(rejected))
	return nil
}

// titleFromFile reads an idea file and extracts its title.
func titleFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", grinderr.WrapSystem(err, "read idea file %s", path)
	}
	return extractTitle(string(data)), nil
}

// extractTitle returns the first heading line of an idea file with the
// leading # characters and surrounding whitespace stripped. It returns ""
// when the file has no heading.
func extractTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
	}
	return ""
}

// parseEditorContent turns the editor's output into a title and body.
//
// Lines starting with # are instructions, not content, so they are dropped.
// The first remaining non-empty line is the title; everything after it is
// the body. An empty title means the user aborted.
func parseEditorContent(content string) (title, body string) {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return "", ""
	}
	return lines[0], strings.Join(lines[1:], "\n")
}
