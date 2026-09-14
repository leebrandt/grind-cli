package ideas

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records the calls idea operations make so tests can assert what
// would have been sent to real git.
type fakeGit struct {
	commits []fakeCommit
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error { return nil }

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) { return true, nil }

func (f *fakeGit) CreateBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) { return false, nil }

func (f *fakeGit) CommitAll(worktreePath, message string) error { return nil }

func (f *fakeGit) RemoteURL(repoPath string) (string, error) { return "", nil }

func (f *fakeGit) Push(repoPath, branch string) error { return nil }

// newTestWorkspace builds a workspace with an ideas dir, without touching git.
func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, ".main")
	if err := os.MkdirAll(filepath.Join(main, "ideas"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: main,
	}
}

func writeIdea(t *testing.T, ws *workspace.Workspace, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws.IdeasDir(), filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"simple", "# My idea\n", "My idea"},
		{"multiple hashes", "## Deep idea\n", "Deep idea"},
		{"extra spaces", "#   Spaced   \n", "Spaced"},
		{"no heading", "just text\n", ""},
		{"first heading wins", "# First\n# Second\n", "First"},
		{"heading after text", "text\n# Later\n", "Later"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTitle(tt.content); got != tt.want {
				t.Errorf("extractTitle(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

func TestParseEditorContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
		title   string
		body    string
	}{
		{"empty", "", "", ""},
		{"only instruction", "\n# First line is the title; add detail below\n", "", ""},
		{"title only", "\n# First line is the title; add detail below\nMy Idea\n", "My Idea", ""},
		{
			"title and body",
			"\n# First line is the title; add detail below\nMy Idea\n\nSome details here.\nMore details.\n",
			"My Idea",
			"Some details here.\nMore details.",
		},
		{"blank lines ignored", "\n\n\nTitle\n\n\n", "Title", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body := parseEditorContent(tt.content)
			if title != tt.title {
				t.Errorf("title = %q, want %q", title, tt.title)
			}
			if body != tt.body {
				t.Errorf("body = %q, want %q", body, tt.body)
			}
		})
	}
}

func TestIdeaString(t *testing.T) {
	tests := []struct {
		name string
		idea Idea
		want string
	}{
		{"normal", Idea{Number: 0, Title: "My idea"}, "0. My idea"},
		{"rejected", Idea{Number: 1, Title: "Bad idea", Rejected: true}, "1. [REJECTED] Bad idea"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.idea.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListFiltering(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# A\n")
	writeIdea(t, ws, "20260101000001.md", "# B\n")
	writeIdea(t, ws, "rejected-20260101000002.md", "# C\n")
	svc := NewService(&fakeGit{})

	t.Run("default excludes rejected", func(t *testing.T) {
		list, err := svc.List(ws, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 {
			t.Fatalf("len = %d, want 2", len(list))
		}
		if list[0].Filename != "20260101000000.md" || list[1].Filename != "20260101000001.md" {
			t.Errorf("filenames = %v", []string{list[0].Filename, list[1].Filename})
		}
		if list[0].Number != 0 || list[1].Number != 1 {
			t.Errorf("numbers = %d, %d", list[0].Number, list[1].Number)
		}
	})

	t.Run("rejected only", func(t *testing.T) {
		list, err := svc.List(ws, ListOptions{Rejected: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 {
			t.Fatalf("len = %d, want 1", len(list))
		}
		if !list[0].Rejected {
			t.Error("expected rejected idea")
		}
		if list[0].Number != 0 {
			t.Errorf("number = %d, want 0", list[0].Number)
		}
	})

	t.Run("all includes rejected", func(t *testing.T) {
		list, err := svc.List(ws, ListOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 3 {
			t.Fatalf("len = %d, want 3", len(list))
		}
		if !list[2].Rejected {
			t.Error("expected third idea to be rejected")
		}
	})
}

func TestListSortsByFilename(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000002.md", "# Later\n")
	writeIdea(t, ws, "20260101000000.md", "# Earlier\n")
	writeIdea(t, ws, "20260101000001.md", "# Middle\n")
	svc := NewService(&fakeGit{})

	list, err := svc.List(ws, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, idea := range list {
		got = append(got, idea.Filename)
	}
	want := []string{"20260101000000.md", "20260101000001.md", "20260101000002.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestListMissingDir(t *testing.T) {
	ws := &workspace.Workspace{
		Root:         t.TempDir(),
		BareRepo:     filepath.Join(t.TempDir(), ".grind.repo.git"),
		MainWorktree: filepath.Join(t.TempDir(), ".main"),
	}
	svc := NewService(&fakeGit{})
	list, err := svc.List(ws, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0", len(list))
	}
}

func TestResolve(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# A\n")
	writeIdea(t, ws, "20260101000001.md", "# B\n")
	writeIdea(t, ws, "rejected-20260101000002.md", "# C\n")
	svc := NewService(&fakeGit{})

	idea, err := svc.Resolve(ws, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idea.Filename != "20260101000001.md" || idea.Title != "B" {
		t.Errorf("Resolve(1) = %+v", idea)
	}

	t.Run("out of range", func(t *testing.T) {
		_, err := svc.Resolve(ws, 2) // only 2 non-rejected ideas
		if err == nil {
			t.Fatal("expected error")
		}
		var user *grinderr.User
		if !errors.As(err, &user) {
			t.Fatalf("expected *grinderr.User, got %T", err)
		}
		want := "Idea #2 not found. Run 'grind list ideas' to see available ideas."
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("negative", func(t *testing.T) {
		if _, err := svc.Resolve(ws, -1); err == nil {
			t.Fatal("expected error for negative number")
		}
	})
}

func TestCreateWithTitleCommitsOnlyIdeaFile(t *testing.T) {
	ws := newTestWorkspace(t)
	fake := &fakeGit{}
	svc := NewService(fake)

	filename, err := svc.Create(ws, "My idea")
	if err != nil {
		t.Fatal(err)
	}
	if filename == "" {
		t.Fatal("expected a filename")
	}

	data, err := os.ReadFile(filepath.Join(ws.IdeasDir(), filename))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# My idea\n" {
		t.Errorf("content = %q, want %q", string(data), "# My idea\n")
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Add idea: My idea" {
		t.Errorf("message = %q", c.message)
	}
	if len(c.paths) != 1 || c.paths[0] != "ideas/"+filename {
		t.Errorf("paths = %v, want [ideas/%s]", c.paths, filename)
	}
}

func TestCreateWithEditorFlow(t *testing.T) {
	ws := newTestWorkspace(t)
	fake := &fakeGit{}
	svc := NewService(fake)

	oldEditTemp := editTemp
	editTemp = func(prefix, initial string) (string, error) {
		return "\n# First line is the title; add detail below\nMy Editor Idea\n\nBody text.\n", nil
	}
	defer func() { editTemp = oldEditTemp }()

	filename, err := svc.Create(ws, "")
	if err != nil {
		t.Fatal(err)
	}
	if filename == "" {
		t.Fatal("expected a filename")
	}

	data, err := os.ReadFile(filepath.Join(ws.IdeasDir(), filename))
	if err != nil {
		t.Fatal(err)
	}
	want := "# My Editor Idea\n\nBody text.\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}

	// The commit message must use the title extracted from the editor.
	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	if fake.commits[0].message != "Add idea: My Editor Idea" {
		t.Errorf("commit message = %q", fake.commits[0].message)
	}
}

func TestCreateWithEditorAbort(t *testing.T) {
	ws := newTestWorkspace(t)
	fake := &fakeGit{}
	svc := NewService(fake)

	oldEditTemp := editTemp
	editTemp = func(prefix, initial string) (string, error) {
		return "\n# First line is the title; add detail below\n", nil
	}
	defer func() { editTemp = oldEditTemp }()

	_, err := svc.Create(ws, "")
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Create() error = %v, want ErrAborted", err)
	}
	if len(fake.commits) != 0 {
		t.Error("no commit should happen on abort")
	}
}

func TestUniqueFilename(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# First\n")

	if got := uniqueFilename(ws.IdeasDir(), "20260101000000"); got != "20260101000000_1.md" {
		t.Errorf("uniqueFilename with existing base = %q, want %q", got, "20260101000000_1.md")
	}
	if got := uniqueFilename(ws.IdeasDir(), "20260102000000"); got != "20260102000000.md" {
		t.Errorf("uniqueFilename with free base = %q, want %q", got, "20260102000000.md")
	}
}

func TestRejectStagesOldAndNewPaths(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My idea\n")
	fake := &fakeGit{}
	svc := NewService(fake)

	result, err := svc.Reject(ws, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.OldName != "20260101000000.md" || result.NewName != "rejected-20260101000000.md" {
		t.Errorf("result = %+v", result)
	}

	// The file must be renamed.
	if _, err := os.Stat(filepath.Join(ws.IdeasDir(), "rejected-20260101000000.md")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.IdeasDir(), "20260101000000.md")); !os.IsNotExist(err) {
		t.Error("original file still exists")
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Reject idea: My idea" {
		t.Errorf("message = %q", c.message)
	}
	want := []string{"ideas/20260101000000.md", "ideas/rejected-20260101000000.md"}
	if !reflect.DeepEqual(c.paths, want) {
		t.Errorf("paths = %v, want %v", c.paths, want)
	}
}

func TestRejectAlreadyRejected(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "rejected-20260101000000.md", "# Bad\n")
	fake := &fakeGit{}
	svc := NewService(fake)

	// Resolve only looks at the non-rejected list, so a rejected-only
	// workspace reports "not found" rather than "already rejected". The
	// "already rejected" branch in Reject is defensive dead code inherited
	// from v1, kept for parity.
	_, err := svc.Reject(ws, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Idea #0 not found. Run 'grind list ideas' to see available ideas." {
		t.Errorf("message = %q", err.Error())
	}
	if len(fake.commits) != 0 {
		t.Error("no commit should happen for already-rejected idea")
	}
}

func TestPruneStagesDeletedPaths(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "rejected-20260101000000.md", "# Bad\n")
	writeIdea(t, ws, "rejected-20260101000001.md", "# Worse\n")
	fake := &fakeGit{}
	svc := NewService(fake)

	if err := svc.Prune(ws, true); err != nil {
		t.Fatal(err)
	}

	for _, fn := range []string{"rejected-20260101000000.md", "rejected-20260101000001.md"} {
		if _, err := os.Stat(filepath.Join(ws.IdeasDir(), fn)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", fn)
		}
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Prune 2 rejected idea(s)" {
		t.Errorf("message = %q", c.message)
	}
	want := []string{"ideas/rejected-20260101000000.md", "ideas/rejected-20260101000001.md"}
	if !reflect.DeepEqual(c.paths, want) {
		t.Errorf("paths = %v, want %v", c.paths, want)
	}
}

func TestPruneNoRejected(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# Fine\n")
	fake := &fakeGit{}
	svc := NewService(fake)

	if err := svc.Prune(ws, true); err != nil {
		t.Fatal(err)
	}
	if len(fake.commits) != 0 {
		t.Error("no commit should happen when there is nothing to prune")
	}
}

func TestPruneAbortsOnNo(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "rejected-20260101000000.md", "# Bad\n")
	fake := &fakeGit{}
	svc := NewService(fake)

	// Feed "n" on stdin via a temp file so the prompt has something to read.
	stdin, err := os.CreateTemp("", "grind-stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(stdin.Name())
	if _, err := stdin.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = oldStdin }()

	if err := svc.Prune(ws, false); err != nil {
		t.Fatal(err)
	}

	// The file must survive and no commit may happen.
	if _, err := os.Stat(filepath.Join(ws.IdeasDir(), "rejected-20260101000000.md")); err != nil {
		t.Errorf("file was deleted despite abort: %v", err)
	}
	if len(fake.commits) != 0 {
		t.Error("no commit should happen on abort")
	}
}

func TestPruneProceedsOnYes(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "rejected-20260101000000.md", "# Bad\n")
	fake := &fakeGit{}
	svc := NewService(fake)

	stdin, err := os.CreateTemp("", "grind-stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(stdin.Name())
	if _, err := stdin.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = oldStdin }()

	if err := svc.Prune(ws, false); err != nil {
		t.Fatal(err)
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
}
