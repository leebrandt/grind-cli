package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records commits so CLI tests can assert what would have been sent
// to real git.
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

// runInWorkspace creates a workspace in a temp dir, chdirs into it, and
// returns a cleanup function. The fake git is returned so tests can pass it
// to NewRootCmd and assert on the recorded commits.
func runInWorkspace(t *testing.T) (*fakeGit, func()) {
	t.Helper()
	dir := t.TempDir()
	fake := &fakeGit{}
	if err := workspace.Init(fake, dir); err != nil {
		t.Fatalf("workspace.Init: %v", err)
	}
	// The fake git does not create the bare repo directory, but workspace
	// discovery needs it to exist on disk.
	if err := os.MkdirAll(filepath.Join(dir, ".grind.repo.git"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		os.Chdir(oldDir)
	}
	return fake, cleanup
}

// execute runs the root command with the given git fake and args, returning
// stdout.
func execute(t *testing.T, fake *fakeGit, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd(fake)
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// captureStdout redirects os.Stdout to a temp file and returns a function
// that restores it and returns everything written. Needed for commands that
// print from the ideas package (prune) rather than through cobra.
func captureStdout(t *testing.T) func() string {
	t.Helper()
	old := os.Stdout
	tmp, err := os.CreateTemp("", "grind-stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = tmp
	return func() string {
		tmp.Close()
		os.Stdout = old
		data, err := os.ReadFile(tmp.Name())
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(tmp.Name())
		return string(data)
	}
}

func TestNewIdeaCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "new", "idea", "Test idea")
	if err != nil {
		t.Fatalf("new idea: %v", err)
	}
	if !strings.Contains(out, "Created idea: ideas/") {
		t.Errorf("output = %q", out)
	}

	// The idea file must exist in .main/ideas (ignoring .gitkeep).
	entries, err := os.ReadDir(filepath.Join(".main", "ideas"))
	if err != nil {
		t.Fatal(err)
	}
	var mdFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdFiles = append(mdFiles, e.Name())
		}
	}
	if len(mdFiles) != 1 {
		t.Fatalf("ideas dir has %d .md files, want 1", len(mdFiles))
	}
	filename := mdFiles[0]

	if len(fake.commits) != 2 {
		t.Fatalf("commits = %d, want 2 (init + add idea)", len(fake.commits))
	}
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Add idea: Test idea" {
		t.Errorf("commit message = %q", last.message)
	}
	if len(last.paths) != 1 || last.paths[0] != "ideas/"+filename {
		t.Errorf("commit paths = %v", last.paths)
	}
}

func TestListIdeasCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "First idea"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "idea", "Second idea"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "list", "ideas")
	if err != nil {
		t.Fatal(err)
	}
	want := "0. First idea\n1. Second idea\n"
	if out != want {
		t.Errorf("list output = %q, want %q", out, want)
	}
}

func TestIdeasAliasCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "Aliased idea"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "ideas")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0. Aliased idea") {
		t.Errorf("alias output = %q", out)
	}
}

func TestListIdeasEmptyState(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "list", "ideas")
	if err != nil {
		t.Fatal(err)
	}
	want := "No ideas yet. Create one with: grind new idea \"Your idea\"\n"
	if out != want {
		t.Errorf("empty output = %q, want %q", out, want)
	}
}

func TestRejectIdeaCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "Doomed idea"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "reject", "idea", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Rejected idea #0: Doomed idea") {
		t.Errorf("reject output = %q", out)
	}
	if !strings.Contains(out, "Renamed:") {
		t.Errorf("reject output missing rename line: %q", out)
	}

	// The last commit must be the reject commit with old+new paths.
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Reject idea: Doomed idea" {
		t.Errorf("last commit message = %q", last.message)
	}
	if len(last.paths) != 2 {
		t.Errorf("last commit paths = %v, want 2", last.paths)
	}
}

func TestPruneIdeasCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "Doomed idea"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "reject", "idea", "0"); err != nil {
		t.Fatal(err)
	}

	// ideas.Prune prints to os.Stdout, so capture it separately.
	readStdout := captureStdout(t)
	_, err := execute(t, fake, "prune", "ideas", "-y")
	if err != nil {
		t.Fatal(err)
	}
	out := readStdout()
	if !strings.Contains(out, "Pruned 1 rejected idea(s) and committed to main branch") {
		t.Errorf("prune output = %q", out)
	}

	// The rejected file must be gone (only .gitkeep may remain).
	entries, err := os.ReadDir(filepath.Join(".main", "ideas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("ideas dir still has %q after prune", e.Name())
		}
	}

	last := fake.commits[len(fake.commits)-1]
	if last.message != "Prune 1 rejected idea(s)" {
		t.Errorf("last commit message = %q", last.message)
	}
}

func TestInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".grind.repo.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	_, err = execute(t, &fakeGit{}, "init")
	if err == nil {
		t.Fatal("expected error for existing workspace")
	}
	if !strings.Contains(err.Error(), "Already a grind workspace") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestParseIdeaNumber(t *testing.T) {
	n, err := parseIdeaNumber("3")
	if err != nil || n != 3 {
		t.Errorf("parseIdeaNumber(3) = %d, %v", n, err)
	}
	if _, err := parseIdeaNumber("abc"); err == nil {
		t.Error("expected error for non-numeric input")
	}
}

// Ensure the git package is linked in tests that reference the interface.
var _ git.Git = (*fakeGit)(nil)
