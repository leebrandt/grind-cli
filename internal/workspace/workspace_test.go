package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leebrandt/grind/internal/grinderr"
)

func TestFindFromRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".grind.repo.git"), 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ws == nil {
		t.Fatal("Find returned nil workspace")
	}
	if ws.Root != dir {
		t.Errorf("Root = %q, want %q", ws.Root, dir)
	}
	if ws.BareRepo != filepath.Join(dir, ".grind.repo.git") {
		t.Errorf("BareRepo = %q", ws.BareRepo)
	}
	if ws.MainWorktree != filepath.Join(dir, ".main") {
		t.Errorf("MainWorktree = %q", ws.MainWorktree)
	}
}

func TestFindFromNestedDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".grind.repo.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate being inside a project worktree, which is a sibling of .main.
	nested := filepath.Join(dir, "my-project", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Find(nested)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ws == nil {
		t.Fatal("Find returned nil workspace")
	}
	if ws.Root != dir {
		t.Errorf("Root = %q, want %q", ws.Root, dir)
	}
}

func TestFindFromMainWorktree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".grind.repo.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, ".main", "ideas")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}

	ws, err := Find(main)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ws == nil {
		t.Fatal("Find returned nil workspace")
	}
	if ws.Root != dir {
		t.Errorf("Root = %q, want %q", ws.Root, dir)
	}
}

func TestFindNotFound(t *testing.T) {
	ws, err := Find(t.TempDir())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if ws != nil {
		t.Errorf("Find returned %+v, want nil", ws)
	}
}

func TestRequireNotFound(t *testing.T) {
	_, err := Require(t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Not in a grind workspace." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestPathHelpers(t *testing.T) {
	ws := &Workspace{
		Root:         "/ws",
		BareRepo:     "/ws/.grind.repo.git",
		MainWorktree: "/ws/.main",
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"IdeasDir", ws.IdeasDir(), "/ws/.main/ideas"},
		{"JournalDir", ws.JournalDir(), "/ws/.main/journal"},
		{"PublishedDir", ws.PublishedDir(), "/ws/.main/published"},
		{"GrindConfigPath", ws.GrindConfigPath(), "/ws/.main/.grind.json"},
		{"ProjectsConfigPath", ws.ProjectsConfigPath(), "/ws/.main/.projects.json"},
		{"ProjectWorktreePath", ws.ProjectWorktreePath("my-blog"), "/ws/my-blog"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// fakeGit records the calls Init makes so tests can assert the sequence
// without running real git commands.
type fakeGit struct {
	initBareCalled    bool
	initialCommitArgs []string
	addWorktreeArgs   [][]string
	commits           []fakeCommit
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) InitBare(path string) error {
	f.initBareCalled = true
	return nil
}

func (f *fakeGit) InitialCommit(repoPath, branch string) error {
	f.initialCommitArgs = []string{repoPath, branch}
	return nil
}

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error {
	f.addWorktreeArgs = append(f.addWorktreeArgs, []string{repoPath, worktreePath, branch})
	return nil
}

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

func TestInit(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeGit{}

	if err := Init(fake, dir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if !fake.initBareCalled {
		t.Error("InitBare was not called")
	}
	if len(fake.initialCommitArgs) != 2 || fake.initialCommitArgs[1] != "main" {
		t.Errorf("InitialCommit args = %v, want branch main", fake.initialCommitArgs)
	}
	if len(fake.addWorktreeArgs) != 1 {
		t.Fatalf("AddWorktree calls = %d, want 1", len(fake.addWorktreeArgs))
	}
	if fake.addWorktreeArgs[0][1] != filepath.Join(dir, ".main") {
		t.Errorf("AddWorktree path = %q", fake.addWorktreeArgs[0][1])
	}

	// The main worktree must contain the config files and the three dirs.
	main := filepath.Join(dir, ".main")
	for _, path := range []string{
		filepath.Join(main, ".grind.json"),
		filepath.Join(main, ".projects.json"),
		filepath.Join(main, "ideas", ".gitkeep"),
		filepath.Join(main, "journal", ".gitkeep"),
		filepath.Join(main, "published", ".gitkeep"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Initialize grind workspace" {
		t.Errorf("commit message = %q", c.message)
	}
	if len(c.paths) != 5 {
		t.Errorf("commit paths = %v, want 5 specific files", c.paths)
	}
}

func TestInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".grind.repo.git"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Init(&fakeGit{}, dir)
	if err == nil {
		t.Fatal("expected error for existing workspace")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Already a grind workspace" {
		t.Errorf("message = %q", err.Error())
	}
}
