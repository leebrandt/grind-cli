package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records commits so CLI tests can assert what would have been sent
// to real git.
type fakeGit struct {
	commits      []fakeCommit
	createBranch []string
	dirtyPaths   map[string]bool
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

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) {
	return !f.dirtyPaths[path], nil
}

func (f *fakeGit) CreateBranch(repoPath, branch string) error {
	f.createBranch = append(f.createBranch, branch)
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

func TestVersionFlag(t *testing.T) {
	out, err := execute(t, &fakeGit{}, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if out != "0.90.0\n" {
		t.Errorf("output = %q, want %q", out, "0.90.0\n")
	}
}

func TestNewProjectCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "new", "project", "my-blog", "0", "-t", "blog")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.Contains(out, "Created project: my-blog") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Branch: my-blog") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Worktree: my-blog/") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Next: cd my-blog") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Remaining ideas:") {
		t.Errorf("output = %q", out)
	}

	// CreateBranch must have been called with the project name.
	if len(fake.createBranch) != 1 || fake.createBranch[0] != "my-blog" {
		t.Errorf("CreateBranch calls = %v", fake.createBranch)
	}

	// The last two commits must be the project creation commits.
	commits := fake.commits
	if len(commits) < 3 {
		t.Fatalf("commits = %d, want at least 3 (init + idea + 2 project)", len(commits))
	}
	c1 := commits[len(commits)-2]
	if c1.message != "Create project: my-blog" {
		t.Errorf("commit message = %q", c1.message)
	}
	if len(c1.paths) != 1 || c1.paths[0] != ".projects.json" {
		t.Errorf("commit paths = %v", c1.paths)
	}
	c2 := commits[len(commits)-1]
	if !strings.HasPrefix(c2.message, "Remove idea ") {
		t.Errorf("commit message = %q", c2.message)
	}
	if len(c2.paths) != 1 || !strings.HasPrefix(c2.paths[0], "ideas/") {
		t.Errorf("commit paths = %v", c2.paths)
	}

	// The idea file must be gone from .main/ideas.
	entries, err := os.ReadDir(filepath.Join(".main", "ideas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("ideas dir still has %q after promotion", e.Name())
		}
	}
}

func TestNewProjectNoType(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0"); err != nil {
		t.Fatal(err)
	}

	// The project entry must have an empty type.
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry, exists := projects.Projects["my-blog"]
	if !exists {
		t.Fatal("project not found")
	}
	if entry.Type != "" {
		t.Errorf("Type = %q, want empty", entry.Type)
	}
}

func TestNewProjectLongTypeFlag(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0", "--type", "blog"); err != nil {
		t.Fatal(err)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry, exists := projects.Projects["my-blog"]
	if !exists {
		t.Fatal("project not found")
	}
	if entry.Type != "blog" {
		t.Errorf("Type = %q, want blog", entry.Type)
	}
}

func TestListProjectsEmptyTypeRendersDash(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "list", "projects")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("output = %q, want em-dash for empty type", out)
	}
}

func TestShowEmptyTypeRendersDash(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "show", "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Type:    —") {
		t.Errorf("output = %q, want em-dash for empty type", out)
	}
}

func TestListProjectsCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0", "-t", "blog"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "list", "projects")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Project") || !strings.Contains(out, "Type") || !strings.Contains(out, "Created") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "my-blog") || !strings.Contains(out, "blog") {
		t.Errorf("output = %q", out)
	}
}

func TestListProjectsEmptyState(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "list", "projects")
	if err != nil {
		t.Fatal(err)
	}
	want := "No projects yet. Create one with: grind new project \"name\" <idea-number>\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestProjectsAliasCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "projects")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "my-blog") {
		t.Errorf("output = %q", out)
	}
}

func TestShowCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0", "-t", "blog"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "show", "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Name:    my-blog") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Type:    blog") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Rate:    150/hr (quarter-hour)") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "# My Blog") {
		t.Errorf("output = %q", out)
	}
}

func TestShowCommandWithCurrency(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	// Set a currency in .grind.json.
	cfg := config.Default()
	cfg.Currency = "$"
	if err := config.Write(filepath.Join(".main", ".grind.json"), cfg); err != nil {
		t.Fatal(err)
	}

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "show", "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Rate:    $150/hr (quarter-hour)") {
		t.Errorf("output = %q", out)
	}
}

func TestShowUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "show", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Project 'nope' not found.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewProjectDirtyProjectsFile(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	fake.dirtyPaths = map[string]bool{".projects.json": true}

	_, err := execute(t, fake, "new", "project", "my-blog", "0")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "You have uncommitted changes in .projects.json.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewProjectDirtyIdeaFileStillWorks(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	// A dirty idea file (e.g. from `edit idea`) must not block promotion.
	entries, err := os.ReadDir(filepath.Join(".main", "ideas"))
	if err != nil {
		t.Fatal(err)
	}
	var filename string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			filename = e.Name()
		}
	}
	if filename == "" {
		t.Fatal("no idea file found")
	}
	fake.dirtyPaths = map[string]bool{"ideas/" + filename: true}

	out, err := execute(t, fake, "new", "project", "my-blog", "0", "-t", "blog")
	if err != nil {
		t.Fatalf("new project with dirty idea file: %v", err)
	}
	if !strings.Contains(out, "Created project: my-blog") {
		t.Errorf("output = %q", out)
	}
}

func TestNewProjectBadIdeaNumber(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "new", "project", "my-blog", "abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Idea must be a number") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewProjectIdeaNotFound(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "new", "project", "my-blog", "5")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Idea #5 not found.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewProjectExistingProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", "my-blog", "0"); err != nil {
		t.Fatal(err)
	}
	// Create another idea so there is something to promote.
	if _, err := execute(t, fake, "new", "idea", "Another"); err != nil {
		t.Fatal(err)
	}

	_, err := execute(t, fake, "new", "project", "my-blog", "0")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Project 'my-blog' already exists.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewProjectUnknownType(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	_, err := execute(t, fake, "new", "project", "my-blog", "0", "-t", "nonsense")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid type: nonsense.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewProjectNotInWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	_, err = execute(t, &fakeGit{}, "new", "project", "my-blog", "0")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Not in a grind workspace.") {
		t.Errorf("error = %q", err.Error())
	}
}

// Ensure the git package is linked in tests that reference the interface.
var _ git.Git = (*fakeGit)(nil)
