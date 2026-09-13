package projects

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records the calls project operations make so tests can assert
// what would have been sent to real git.
type fakeGit struct {
	calls           []string
	commits         []fakeCommit
	createBranch    []string
	addWorktree     [][]string
	isClean         bool
	createBranchErr error
	commitErr       error
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func newFakeGit() *fakeGit {
	return &fakeGit{isClean: true}
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error {
	f.calls = append(f.calls, "AddWorktree:"+branch)
	f.addWorktree = append(f.addWorktree, []string{repoPath, worktreePath, branch})
	return nil
}

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}

func (f *fakeGit) IsClean(worktreePath string) (bool, error) { return f.isClean, nil }

func (f *fakeGit) CreateBranch(repoPath, branch string) error {
	f.calls = append(f.calls, "CreateBranch:"+branch)
	f.createBranch = append(f.createBranch, branch)
	return f.createBranchErr
}

// newTestWorkspace builds a workspace with config files and an ideas dir,
// without touching git.
func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, ".main")
	if err := os.MkdirAll(filepath.Join(main, "ideas"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: main,
	}
	if err := config.Write(ws.GrindConfigPath(), config.Default()); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), config.DefaultProjects()); err != nil {
		t.Fatal(err)
	}
	return ws
}

func writeIdea(t *testing.T, ws *workspace.Workspace, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws.IdeasDir(), filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name string
		want string // substring of the expected error, "" for valid
	}{
		{"my-blog", ""},
		{"blog", ""},
		{"a", ""},
		{"my_blog", ""},
		{"my.blog", ""},
		{"", "name is empty"},
		{"my blog", "whitespace"},
		{"my\tblog", "whitespace"},
		{".main", "reserved"},
		{".grind.repo.git", "reserved"},
		{"HEAD", "HEAD is reserved"},
		{"foo.lock", "must not end in .lock"},
		{"-foo", "must not start with -"},
		{"/foo", "must not start or end with /"},
		{"foo/", "must not start or end with /"},
		{"foo..bar", "must not contain .."},
		{"foo@{bar", "must not contain @{"},
		{"foo~bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo^bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo:bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo?bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo*bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo[bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo\\bar", "must not contain ~ ^ : ? * [ \\"},
		{"foo\x01bar", "control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateName(tt.name)
			if tt.want == "" {
				if err != nil {
					t.Errorf("validateName(%q) = %v, want nil", tt.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateName(%q) = nil, want error containing %q", tt.name, tt.want)
			}
			var user *grinderr.User
			if !errors.As(err, &user) {
				t.Errorf("validateName(%q) error type = %T, want *grinderr.User", tt.name, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("validateName(%q) error = %q, want it to contain %q", tt.name, err.Error(), tt.want)
			}
		})
	}
}

func TestValidTypes(t *testing.T) {
	cfg := config.Default()
	if got := validTypes(cfg); !reflect.DeepEqual(got, defaultTypes) {
		t.Errorf("validTypes(default) = %v, want %v", got, defaultTypes)
	}

	cfg.ProjectTypes = []string{"blog", "code"}
	if got := validTypes(cfg); !reflect.DeepEqual(got, []string{"blog", "code"}) {
		t.Errorf("validTypes(configured) = %v, want [blog code]", got)
	}
}

func TestCreateHappyPath(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n\nSome details\n")
	fake := newFakeGit()
	svc := NewService(fake)

	entry, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "my-blog" {
		t.Errorf("Name = %q", entry.Name)
	}
	if entry.Type != "blog" {
		t.Errorf("Type = %q", entry.Type)
	}
	if entry.Idea != "# My Blog\n\nSome details\n" {
		t.Errorf("Idea = %q", entry.Idea)
	}
	if entry.Billing.RoundTo != "quarter-hour" {
		t.Errorf("RoundTo = %q", entry.Billing.RoundTo)
	}
	if entry.Billing.Rate != 150 {
		t.Errorf("Rate = %v", entry.Billing.Rate)
	}
	if entry.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}

	// CreateBranch must run before AddWorktree.
	wantCalls := []string{"CreateBranch:my-blog", "AddWorktree:my-blog"}
	if !reflect.DeepEqual(fake.calls, wantCalls) {
		t.Errorf("git calls = %v, want %v", fake.calls, wantCalls)
	}
	if len(fake.addWorktree) != 1 {
		t.Fatalf("AddWorktree calls = %d, want 1", len(fake.addWorktree))
	}
	if fake.addWorktree[0][1] != ws.ProjectWorktreePath("my-blog") {
		t.Errorf("AddWorktree path = %q", fake.addWorktree[0][1])
	}
	if fake.addWorktree[0][2] != "my-blog" {
		t.Errorf("AddWorktree branch = %q", fake.addWorktree[0][2])
	}

	// Two commits: .projects.json, then the idea deletion.
	if len(fake.commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(fake.commits))
	}
	c1 := fake.commits[0]
	if c1.message != "Create project: my-blog" {
		t.Errorf("first commit message = %q", c1.message)
	}
	if len(c1.paths) != 1 || c1.paths[0] != ".projects.json" {
		t.Errorf("first commit paths = %v", c1.paths)
	}
	c2 := fake.commits[1]
	if c2.message != "Remove idea 20260101000000.md (now project my-blog)" {
		t.Errorf("second commit message = %q", c2.message)
	}
	if len(c2.paths) != 1 || c2.paths[0] != "ideas/20260101000000.md" {
		t.Errorf("second commit paths = %v", c2.paths)
	}

	// The idea file must be deleted.
	if _, err := os.Stat(filepath.Join(ws.IdeasDir(), "20260101000000.md")); !os.IsNotExist(err) {
		t.Error("idea file still exists")
	}

	// The project must be in .projects.json.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projects.Projects["my-blog"]; !ok {
		t.Error("project not in .projects.json")
	}
}

func TestCreateWithoutType(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	svc := NewService(fake)

	entry, err := svc.Create(ws, "my-blog", "", "20260101000000.md")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Type != "" {
		t.Errorf("Type = %q, want empty", entry.Type)
	}
}

func TestCreateFailsFastOnDirtyMain(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	fake.isClean = false
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	want := "You have uncommitted changes in .main. Commit or discard them first."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if len(fake.calls) != 0 {
		t.Errorf("git calls = %v, want none", fake.calls)
	}
}

func TestCreateRefusesInvalidName(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	svc := NewService(fake)

	_, err := svc.Create(ws, "bad name", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if !strings.Contains(err.Error(), "Invalid project name") {
		t.Errorf("message = %q", err.Error())
	}
	if len(fake.calls) != 0 {
		t.Errorf("git calls = %v, want none", fake.calls)
	}
}

func TestCreateRefusesExistingProject(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	projects := config.DefaultProjects()
	projects.Projects["my-blog"] = config.ProjectEntry{Name: "my-blog"}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	fake := newFakeGit()
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project 'my-blog' already exists." {
		t.Errorf("message = %q", err.Error())
	}
	if len(fake.calls) != 0 {
		t.Errorf("git calls = %v, want none", fake.calls)
	}
}

func TestCreateRefusesExistingDirectory(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	if err := os.MkdirAll(ws.ProjectWorktreePath("my-blog"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := newFakeGit()
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if !strings.Contains(err.Error(), "directory already exists") {
		t.Errorf("message = %q", err.Error())
	}
	if len(fake.calls) != 0 {
		t.Errorf("git calls = %v, want none", fake.calls)
	}
}

func TestCreateRefusesUnknownType(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "not-a-type", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	want := "Invalid type: not-a-type. Valid types: blog, webapp, video, song, book, feature, issue"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if len(fake.calls) != 0 {
		t.Errorf("git calls = %v, want none", fake.calls)
	}
}

func TestCreateUsesConfiguredTypes(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	cfg := config.Default()
	cfg.ProjectTypes = []string{"blog", "code"}
	if err := config.Write(ws.GrindConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	fake := newFakeGit()
	svc := NewService(fake)

	// "code" is in the configured list, so it must be accepted.
	if _, err := svc.Create(ws, "my-blog", "code", "20260101000000.md"); err != nil {
		t.Fatalf("Create with configured type: %v", err)
	}
}

func TestCreateBranchAlreadyExists(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	fake.createBranchErr = grinderr.NewSystem("branch my-blog already exists")
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	// AddWorktree must not be called when CreateBranch fails.
	if len(fake.addWorktree) != 0 {
		t.Error("AddWorktree should not be called when CreateBranch fails")
	}
}

func TestCreateCommitFailureNotesWorktree(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	fake.commitErr = grinderr.NewSystem("commit failed")
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	// The error must tell the user the worktree was left behind.
	if !strings.Contains(err.Error(), "project worktree my-blog was created") {
		t.Errorf("message = %q, want worktree cleanup note", err.Error())
	}
	if !strings.Contains(err.Error(), "git worktree remove my-blog") {
		t.Errorf("message = %q, want removal hint", err.Error())
	}
	// The error must still be a system error so the exit code is 2.
	var sys *grinderr.System
	if !errors.As(err, &sys) {
		t.Errorf("error type = %T, want *grinderr.System", err)
	}
}

func TestCreateMissingIdeaFileNotesWorktree(t *testing.T) {
	ws := newTestWorkspace(t)
	// No idea file is written; Create must fail after the worktree exists.
	fake := newFakeGit()
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "project worktree my-blog was created") {
		t.Errorf("message = %q, want worktree cleanup note", err.Error())
	}
	// The branch and worktree must have been created before the failure.
	if len(fake.createBranch) != 1 || len(fake.addWorktree) != 1 {
		t.Errorf("createBranch = %v, addWorktree = %v, want both called once",
			fake.createBranch, fake.addWorktree)
	}
	// No commits should have happened.
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0", len(fake.commits))
	}
}

func TestListSortsByName(t *testing.T) {
	ws := newTestWorkspace(t)
	projects := config.DefaultProjects()
	projects.Projects["zeta"] = config.ProjectEntry{Name: "zeta"}
	projects.Projects["alpha"] = config.ProjectEntry{Name: "alpha"}
	projects.Projects["mid"] = config.ProjectEntry{Name: "mid"}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	svc := NewService(newFakeGit())

	list, err := svc.List(ws)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range list {
		got = append(got, p.Name)
	}
	want := []string{"alpha", "mid", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestListMissingFile(t *testing.T) {
	ws := &workspace.Workspace{
		Root:         t.TempDir(),
		BareRepo:     filepath.Join(t.TempDir(), ".grind.repo.git"),
		MainWorktree: filepath.Join(t.TempDir(), ".main"),
	}
	svc := NewService(newFakeGit())

	list, err := svc.List(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("len = %d, want 0", len(list))
	}
}

func TestGet(t *testing.T) {
	ws := newTestWorkspace(t)
	projects := config.DefaultProjects()
	projects.Projects["my-blog"] = config.ProjectEntry{Name: "my-blog", Type: "blog"}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	svc := NewService(newFakeGit())

	entry, err := svc.Get(ws, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "my-blog" || entry.Type != "blog" {
		t.Errorf("entry = %+v", entry)
	}

	t.Run("not found", func(t *testing.T) {
		_, err := svc.Get(ws, "nope")
		if err == nil {
			t.Fatal("expected error")
		}
		var user *grinderr.User
		if !errors.As(err, &user) {
			t.Fatalf("expected *grinderr.User, got %T", err)
		}
		if err.Error() != "Project 'nope' not found." {
			t.Errorf("message = %q", err.Error())
		}
	})
}

func TestGetMissingFile(t *testing.T) {
	ws := &workspace.Workspace{
		Root:         t.TempDir(),
		BareRepo:     filepath.Join(t.TempDir(), ".grind.repo.git"),
		MainWorktree: filepath.Join(t.TempDir(), ".main"),
	}
	svc := NewService(newFakeGit())

	_, err := svc.Get(ws, "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project 'nope' not found." {
		t.Errorf("message = %q", err.Error())
	}
}

// Ensure the git package is linked in tests that reference the interface.
var _ git.Git = (*fakeGit)(nil)
