package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records commits so CLI tests can assert what would have been sent
// to real git.
type fakeGit struct {
	commits      []fakeCommit
	createBranch []string
	dirtyPaths   map[string]bool
	hasChanges   bool
	commitAll    []fakeCommit
	remoteURL    string
	pushAll      int
	pushErr      error
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error {
	// Real git creates the worktree directory on disk; the project
	// creation flow relies on that when it writes the .idea seed file.
	return os.MkdirAll(worktreePath, 0o755)
}

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

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) {
	return f.hasChanges, nil
}

func (f *fakeGit) CommitAll(worktreePath, message string) error {
	f.commitAll = append(f.commitAll, fakeCommit{worktree: worktreePath, message: message})
	return nil
}

func (f *fakeGit) RemoteURL(repoPath string) (string, error) {
	return f.remoteURL, nil
}

func (f *fakeGit) PushAll(repoPath string) error {
	f.pushAll++
	return f.pushErr
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
	if out != "0.90.2\n" {
		t.Errorf("output = %q, want %q", out, "0.90.2\n")
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

	// The project worktree must contain the .idea seed with the FULL idea
	// content, committed on the project branch.
	ideaFile, err := os.ReadFile(filepath.Join("my-blog", ".idea"))
	if err != nil {
		t.Fatalf("read .idea: %v", err)
	}
	if string(ideaFile) != "# My Blog\n" {
		t.Errorf(".idea content = %q, want %q", string(ideaFile), "# My Blog\n")
	}
	seed := commits[len(commits)-3]
	if filepath.Base(seed.worktree) != "my-blog" {
		t.Errorf("seed commit worktree = %q", seed.worktree)
	}
	if seed.message != "Add idea: My Blog" {
		t.Errorf("seed commit message = %q", seed.message)
	}

	// The .projects.json idea value is the H1, not the full content.
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := projects.Projects["my-blog"].Idea; got != "My Blog" {
		t.Errorf("project idea value = %q, want %q", got, "My Blog")
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
	// The idea value is the H1 header, so show prints the title, not the
	// full idea content (which lives in the project's .idea file).
	if !strings.Contains(out, "My Blog") {
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

// captureStderr redirects os.Stderr to a pipe and returns a function that
// restores it and returns everything written. Needed for commands that print
// warnings directly to stderr (save's best-effort push warning).
func captureStderr(t *testing.T) func() string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	return func() string {
		w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
}

// recordingEditor writes a script that appends its arguments to logFile and
// sets $EDITOR to it, so tests can verify which path the editor received.
func recordingEditor(t *testing.T) string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "editor-args.txt")
	script := filepath.Join(t.TempDir(), "editor.sh")
	content := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", script)
	return logFile
}

// createProject promotes the first idea into a project named name.
func createProject(t *testing.T, fake *fakeGit, name string) {
	t.Helper()
	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "project", name, "0", "-t", "blog"); err != nil {
		t.Fatal(err)
	}
}

func TestEditProjectOpensEditorWithoutSession(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	logFile := recordingEditor(t)
	if _, err := execute(t, fake, "edit", "my-blog"); err != nil {
		t.Fatalf("edit my-blog: %v", err)
	}

	// The editor must have been called with the project worktree path.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	wantPath := filepath.Join(cwd, "my-blog")
	if strings.TrimSpace(string(data)) != wantPath {
		t.Errorf("editor arg = %q, want %q", strings.TrimSpace(string(data)), wantPath)
	}

	// No session may be started and nothing may be committed.
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects["my-blog"].Sessions) != 0 {
		t.Error("edit must not start a session")
	}
	for _, c := range fake.commits {
		if strings.Contains(c.message, "Start session") {
			t.Errorf("unexpected commit: %q", c.message)
		}
	}
}

func TestEditUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "edit", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' not found." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestEditIdeaCommandStillWorks(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Idea"); err != nil {
		t.Fatal(err)
	}

	logFile := recordingEditor(t)
	if _, err := execute(t, fake, "edit", "idea", "0"); err != nil {
		t.Fatalf("edit idea 0: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".main/ideas/") {
		t.Errorf("editor arg = %q, want an idea file path", strings.TrimSpace(string(data)))
	}
}

func TestWorkCommandStartAndContinue(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	t.Setenv("EDITOR", "true")

	out, err := execute(t, fake, "work", "my-blog")
	if err != nil {
		t.Fatalf("work my-blog: %v", err)
	}
	if !strings.Contains(out, "Started work session on 'my-blog'") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Time started:") {
		t.Errorf("output = %q", out)
	}

	// .projects.json must have one active session.
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := projects.Projects["my-blog"].Sessions
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].End != nil {
		t.Error("session should be active")
	}

	// The last commit must be the start-session commit.
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Start session on my-blog" {
		t.Errorf("last commit = %q", last.message)
	}

	// Running work again continues the session without a second one.
	out, err = execute(t, fake, "work", "my-blog")
	if err != nil {
		t.Fatalf("work my-blog (second): %v", err)
	}
	if !strings.Contains(out, "Continuing session on 'my-blog'") {
		t.Errorf("output = %q", out)
	}

	projects, err = config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects["my-blog"].Sessions) != 1 {
		t.Errorf("sessions = %d, want still 1", len(projects.Projects["my-blog"].Sessions))
	}
}

func TestWorkCommandOpensEditorOnProjectWorktree(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	logFile := recordingEditor(t)
	if _, err := execute(t, fake, "work", "my-blog"); err != nil {
		t.Fatalf("work my-blog: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	wantPath := filepath.Join(cwd, "my-blog")
	if strings.TrimSpace(string(data)) != wantPath {
		t.Errorf("editor arg = %q, want %q", strings.TrimSpace(string(data)), wantPath)
	}
}

func TestWorkCommandUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "work", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestSaveCommandHappyPath(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	t.Setenv("EDITOR", "true")

	if _, err := execute(t, fake, "work", "my-blog"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "save", "my-blog")
	if err != nil {
		t.Fatalf("save my-blog: %v", err)
	}
	if !strings.Contains(out, "Stopped work session on 'my-blog'") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Duration:") {
		t.Errorf("output = %q", out)
	}

	// The session must be ended in .projects.json.
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := projects.Projects["my-blog"].Sessions[0]
	if session.End == nil {
		t.Error("session still active after save")
	}

	// The last commit must be the save-session commit.
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Save session on my-blog" {
		t.Errorf("last commit = %q", last.message)
	}
}

func TestSaveCommandBackfillActiveSession(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	t.Setenv("EDITOR", "true")

	if _, err := execute(t, fake, "work", "my-blog"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "save", "my-blog", "-t", "8h")
	if err != nil {
		t.Fatalf("save my-blog -t 8h: %v", err)
	}
	if !strings.Contains(out, "Stopped work session on 'my-blog'") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Duration: 8.00 hours (8.00 hours rounded)") {
		t.Errorf("output = %q", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	session := projects.Projects["my-blog"].Sessions[0]
	if session.End == nil {
		t.Fatal("session still active")
	}
	wantEnd := session.Start.Add(8 * time.Hour)
	if !session.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", session.End, wantEnd)
	}
	if session.Duration != 8*3600 {
		t.Errorf("Duration = %d, want %d", session.Duration, 8*3600)
	}
}

func TestSaveCommandBackfillNoSession(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	out, err := execute(t, fake, "save", "my-blog", "-t", "8h")
	if err != nil {
		t.Fatalf("save my-blog -t 8h: %v", err)
	}
	if !strings.Contains(out, "Backfilled 8h on 'my-blog'") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "Duration: 8.00 hours (8.00 hours rounded)") {
		t.Errorf("output = %q", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := projects.Projects["my-blog"].Sessions
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].End == nil {
		t.Fatal("backfilled session should be ended")
	}
	if sessions[0].Duration != 8*3600 {
		t.Errorf("Duration = %d, want %d", sessions[0].Duration, 8*3600)
	}
}

func TestSaveCommandInvalidDuration(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	_, err := execute(t, fake, "save", "my-blog", "-t", "banana")
	if err == nil {
		t.Fatal("expected error")
	}
	want := "Backfill time must be a positive duration (e.g. 5, 5h, 1h30m, 90m). Got 'banana'."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestSaveCommandUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "save", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestSaveCommandNoActiveSession(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	out, err := execute(t, fake, "save", "my-blog")
	if err != nil {
		t.Fatalf("save my-blog: %v", err)
	}
	if !strings.Contains(out, "No active session on 'my-blog'.") {
		t.Errorf("output = %q", out)
	}

	// No save-session commit should have happened.
	for _, c := range fake.commits {
		if strings.Contains(c.message, "Save session") {
			t.Errorf("unexpected commit: %q", c.message)
		}
	}
}

func TestSaveCommandCommitsDirtyWorktree(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	fake.hasChanges = true

	if _, err := execute(t, fake, "save", "my-blog"); err != nil {
		t.Fatalf("save my-blog: %v", err)
	}

	if len(fake.commitAll) != 1 {
		t.Fatalf("CommitAll calls = %d, want 1", len(fake.commitAll))
	}
	if fake.commitAll[0].message != "Save on my-blog" {
		t.Errorf("CommitAll message = %q", fake.commitAll[0].message)
	}
}

func TestSaveCommandNeverPushes(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	fake.remoteURL = "git@example.com:repo.git"

	if _, err := execute(t, fake, "save", "my-blog"); err != nil {
		t.Fatalf("save my-blog: %v", err)
	}

	// Save is local-only: committing is its job, pushing is `grind push`'s.
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}

func TestPushCommandNoRemote(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "push")
	if err == nil {
		t.Fatal("push without a remote: expected error")
	}
	if !strings.Contains(err.Error(), "No remote configured") {
		t.Errorf("error = %q, want no-remote message", err.Error())
	}
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}

func TestPushCommandHappyPath(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	fake.remoteURL = "git@example.com:repo.git"

	out, err := execute(t, fake, "push")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if fake.pushAll != 1 {
		t.Errorf("PushAll calls = %d, want 1", fake.pushAll)
	}
	if !strings.Contains(out, "Pushed all branches to origin.") {
		t.Errorf("output = %q", out)
	}
}

func TestPushCommandFailureIsUserError(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	fake.remoteURL = "git@example.com:repo.git"
	fake.pushErr = &git.PushError{
		Stderr: "fatal: unable to access",
		Err:    errors.New("push failed"),
	}

	_, err := execute(t, fake, "push")
	if err == nil {
		t.Fatal("push with failing remote: expected error")
	}
	// A failed push is what the user asked for, so it is a real user error
	// (exit 1) carrying git's stderr — not a silent warning.
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error = %T, want *grinderr.User", err)
	}
	if !strings.Contains(err.Error(), "fatal: unable to access") {
		t.Errorf("error = %q, want git's stderr", err.Error())
	}
}

// Ensure the git package is linked in tests that reference the interface.
var _ git.Git = (*fakeGit)(nil)
