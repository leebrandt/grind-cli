package projects

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	commitAll       []fakeCommit
	createBranch    []string
	addWorktree     [][]string
	removeWorktree  []string
	deleteBranch    []string
	dirtyPaths      map[string]bool
	dirtyWorktrees  map[string]bool
	createBranchErr error
	commitErr       error
	mergeErr        error
	hasChanges      bool
	remoteURL       string
	pushAll         int
	pushErr         error
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func newFakeGit() *fakeGit {
	return &fakeGit{}
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error {
	f.calls = append(f.calls, "AddWorktree:"+branch)
	f.addWorktree = append(f.addWorktree, []string{repoPath, worktreePath, branch})
	// Real git creates the worktree directory on disk; Create relies on
	// that when it writes the .idea seed file.
	return os.MkdirAll(worktreePath, 0o755)
}

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) {
	return !f.dirtyPaths[path], nil
}

func (f *fakeGit) CreateBranch(repoPath, branch string) error {
	f.calls = append(f.calls, "CreateBranch:"+branch)
	f.createBranch = append(f.createBranch, branch)
	return f.createBranchErr
}

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) {
	// Per-worktree dirty control lets publish/cancel tests mark exactly one
	// worktree dirty. When unset, the plain hasChanges flag is the answer.
	if f.dirtyWorktrees != nil {
		return f.dirtyWorktrees[worktreePath], nil
	}
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

// LastCommitDate returns the zero time: project and session operations
// never need commit dates, so the stub keeps the fake a complete git.Git.
func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeGit) DefaultBranch(repoPath string) (string, error) { return "main", nil }

func (f *fakeGit) SetRemoteURL(repoPath, url string) error { return nil }

func (f *fakeGit) PushBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) FetchAll(repoPath string) error { return nil }

func (f *fakeGit) IsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	return false, nil
}

func (f *fakeGit) FastForwardWorktree(worktreePath, branch string) error { return nil }

func (f *fakeGit) FastForwardRef(repoPath, branch string) error { return nil }

func (f *fakeGit) ListRemoteBranches(repoPath string) ([]string, error) { return nil, nil }

func (f *fakeGit) MergeBranch(worktreePath, branch string) error {
	f.calls = append(f.calls, "MergeBranch:"+branch)
	return f.mergeErr
}

func (f *fakeGit) RemoveWorktree(repoPath, worktreePath string) error {
	f.removeWorktree = append(f.removeWorktree, worktreePath)
	return nil
}

func (f *fakeGit) DeleteBranch(repoPath, branch string) error {
	f.deleteBranch = append(f.deleteBranch, branch)
	return nil
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
	if err := os.MkdirAll(filepath.Join(main, "published"), 0o755); err != nil {
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
	// The idea value is the H1 header, not the full idea content.
	if entry.Idea != "My Blog" {
		t.Errorf("Idea = %q, want %q", entry.Idea, "My Blog")
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

	// Three commits: the .idea seed on the project worktree, .projects.json,
	// then the idea deletion.
	if len(fake.commits) != 3 {
		t.Fatalf("commits = %d, want 3", len(fake.commits))
	}
	c0 := fake.commits[0]
	if c0.worktree != ws.ProjectWorktreePath("my-blog") {
		t.Errorf("first commit worktree = %q", c0.worktree)
	}
	if c0.message != "Add idea: My Blog" {
		t.Errorf("first commit message = %q", c0.message)
	}
	if len(c0.paths) != 1 || c0.paths[0] != ".idea" {
		t.Errorf("first commit paths = %v", c0.paths)
	}
	c1 := fake.commits[1]
	if c1.message != "Create project: my-blog" {
		t.Errorf("second commit message = %q", c1.message)
	}
	if len(c1.paths) != 1 || c1.paths[0] != ".projects.json" {
		t.Errorf("second commit paths = %v", c1.paths)
	}
	c2 := fake.commits[2]
	if c2.message != "Remove idea 20260101000000.md (now project my-blog)" {
		t.Errorf("third commit message = %q", c2.message)
	}
	if len(c2.paths) != 1 || c2.paths[0] != "ideas/20260101000000.md" {
		t.Errorf("third commit paths = %v", c2.paths)
	}

	// The .idea file in the worktree holds the FULL idea content.
	ideaFile, err := os.ReadFile(filepath.Join(ws.ProjectWorktreePath("my-blog"), ".idea"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ideaFile) != "# My Blog\n\nSome details\n" {
		t.Errorf(".idea content = %q", string(ideaFile))
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

func TestCreateFailsFastOnDirtyProjectsFile(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	fake.dirtyPaths = map[string]bool{".projects.json": true}
	svc := NewService(fake)

	_, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	want := "You have uncommitted changes in .projects.json. Commit or discard them first."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if len(fake.calls) != 0 {
		t.Errorf("git calls = %v, want none", fake.calls)
	}
}

func TestCreateIgnoresDirtyIdeaFile(t *testing.T) {
	ws := newTestWorkspace(t)
	writeIdea(t, ws, "20260101000000.md", "# My Blog\n")
	fake := newFakeGit()
	// A dirty idea file (e.g. from `edit idea`) must not block promotion:
	// its content is captured into .projects.json before the file is
	// deleted.
	fake.dirtyPaths = map[string]bool{"ideas/20260101000000.md": true}
	svc := NewService(fake)

	entry, err := svc.Create(ws, "my-blog", "blog", "20260101000000.md")
	if err != nil {
		t.Fatalf("Create with dirty idea file: %v", err)
	}
	if entry.Name != "my-blog" {
		t.Errorf("Name = %q", entry.Name)
	}
	if len(fake.commits) != 3 {
		t.Errorf("commits = %d, want 3", len(fake.commits))
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

// addProjectEntry writes a project entry with the given status into
// .projects.json, so publish/cancel tests start from a known state.
func addProjectEntry(t *testing.T, ws *workspace.Workspace, name, status string) {
	t.Helper()
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := config.ProjectEntry{
		Name:      name,
		Type:      "blog",
		Idea:      "My Blog",
		Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Status:    status,
	}
	projects.Projects[name] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
}

// addProjectWorktree creates the project worktree directory with an .idea
// file, mimicking what `grind new project` leaves behind.
func addProjectWorktree(t *testing.T, ws *workspace.Workspace, name string) {
	t.Helper()
	dir := ws.ProjectWorktreePath(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".idea"), []byte("# My Blog\n\nSome details\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishHappyPath(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Publish(ws, "my-blog", CleanupBoth); err != nil {
		t.Fatal(err)
	}

	// The merge must run before the state change.
	if len(fake.calls) != 1 || fake.calls[0] != "MergeBranch:my-blog" {
		t.Errorf("git calls = %v, want [MergeBranch:my-blog]", fake.calls)
	}

	// The draft must be written with the full frontmatter and the .idea
	// body, ending in a newline.
	draft, err := os.ReadFile(filepath.Join(ws.MainWorktree, "published", "my-blog.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ntitle: My Blog\ntype: blog\ndate: " + time.Now().Format("2006-01-02") + "\nstatus: published\n---\n\n# My Blog\n\nSome details\n"
	if string(draft) != want {
		t.Errorf("draft = %q, want %q", string(draft), want)
	}

	// The entry must be marked published.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := projects.Projects["my-blog"].Status; got != "published" {
		t.Errorf("Status = %q, want published", got)
	}

	// One commit: the publish commit staging .projects.json and the draft.
	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Publish project: my-blog" {
		t.Errorf("commit message = %q", c.message)
	}
	if len(c.paths) != 2 || c.paths[0] != ".projects.json" || c.paths[1] != "published/my-blog.md" {
		t.Errorf("commit paths = %v", c.paths)
	}

	// CleanupBoth removes the worktree, then the branch.
	if len(fake.removeWorktree) != 1 || fake.removeWorktree[0] != ws.ProjectWorktreePath("my-blog") {
		t.Errorf("RemoveWorktree calls = %v", fake.removeWorktree)
	}
	if len(fake.deleteBranch) != 1 || fake.deleteBranch[0] != "my-blog" {
		t.Errorf("DeleteBranch calls = %v", fake.deleteBranch)
	}
}

func TestPublishDraftOmitsOptionalFrontmatter(t *testing.T) {
	ws := newTestWorkspace(t)
	// No type, no author in config, and no .idea file: the draft falls back
	// to entry.Idea for both title and body.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	projects.Projects["my-blog"] = config.ProjectEntry{
		Name: "my-blog",
		Idea: "My Blog",
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws.ProjectWorktreePath("my-blog"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Publish(ws, "my-blog", CleanupNone); err != nil {
		t.Fatal(err)
	}

	draft, err := os.ReadFile(filepath.Join(ws.MainWorktree, "published", "my-blog.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ntitle: My Blog\ndate: " + time.Now().Format("2006-01-02") + "\nstatus: published\n---\n\nMy Blog\n"
	if string(draft) != want {
		t.Errorf("draft = %q, want %q", string(draft), want)
	}
}

func TestPublishDraftIncludesAuthor(t *testing.T) {
	ws := newTestWorkspace(t)
	cfg := config.Default()
	cfg.My = &config.MyConfig{Name: "Lee"}
	if err := config.Write(ws.GrindConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Publish(ws, "my-blog", CleanupNone); err != nil {
		t.Fatal(err)
	}

	draft, err := os.ReadFile(filepath.Join(ws.MainWorktree, "published", "my-blog.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(draft), "author: Lee\n") {
		t.Errorf("draft missing author:\n%s", draft)
	}
}

func TestPublishNonexistentProject(t *testing.T) {
	ws := newTestWorkspace(t)
	svc := NewService(newFakeGit())

	err := svc.Publish(ws, "nope", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestPublishMissingWorktree(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	// No worktree directory.
	svc := NewService(newFakeGit())

	err := svc.Publish(ws, "my-blog", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project worktree 'my-blog' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestPublishDirtyMain(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	fake.dirtyWorktrees = map[string]bool{ws.MainWorktree: true}
	svc := NewService(fake)

	err := svc.Publish(ws, "my-blog", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	want := "Main worktree has uncommitted changes. Run 'grind save' to commit them."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	// No merge, no commit.
	if len(fake.calls) != 0 || len(fake.commits) != 0 {
		t.Errorf("calls = %v, commits = %d, want none", fake.calls, len(fake.commits))
	}
}

func TestPublishDirtyProject(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	fake.dirtyWorktrees = map[string]bool{ws.ProjectWorktreePath("my-blog"): true}
	svc := NewService(fake)

	err := svc.Publish(ws, "my-blog", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	want := "Project 'my-blog' has uncommitted changes. Run 'grind save my-blog' to commit them."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestPublishMergeFailure(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	fake.mergeErr = grinderr.NewSystem("merge conflict")
	svc := NewService(fake)

	err := svc.Publish(ws, "my-blog", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var sys *grinderr.System
	if !errors.As(err, &sys) {
		t.Fatalf("expected *grinderr.System, got %T", err)
	}
	want := "merge project 'my-blog' into main: merge conflict"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	// No commit: the merge failed before any state change.
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0", len(fake.commits))
	}
}

func TestCancelHappyPath(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Cancel(ws, "my-blog", CleanupWorktree); err != nil {
		t.Fatal(err)
	}

	// The entry must be marked canceled.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := projects.Projects["my-blog"].Status; got != "canceled" {
		t.Errorf("Status = %q, want canceled", got)
	}

	// One commit staging only .projects.json.
	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Cancel project: my-blog" {
		t.Errorf("commit message = %q", c.message)
	}
	if len(c.paths) != 1 || c.paths[0] != ".projects.json" {
		t.Errorf("commit paths = %v", c.paths)
	}

	// CleanupWorktree removes the worktree but keeps the branch.
	if len(fake.removeWorktree) != 1 || fake.removeWorktree[0] != ws.ProjectWorktreePath("my-blog") {
		t.Errorf("RemoveWorktree calls = %v", fake.removeWorktree)
	}
	if len(fake.deleteBranch) != 0 {
		t.Errorf("DeleteBranch calls = %v, want none", fake.deleteBranch)
	}
}

func TestCancelMarksOpenTasksCanceled(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")

	// Seed one open task and one done task. The done task must survive
	// cancel untouched — it was completed before the project ended.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := projects.Projects["my-blog"]
	entry.Tasks = []config.Task{
		{ID: 100, Description: "write intro", Done: false},
		{ID: 101, Description: "ship v1", Done: true},
	}
	projects.Projects["my-blog"] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}

	svc := NewService(newFakeGit())
	if err := svc.Cancel(ws, "my-blog", CleanupNone); err != nil {
		t.Fatal(err)
	}

	projects, err = config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	tasks := projects.Projects["my-blog"].Tasks
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2 (cancel marks, never deletes)", len(tasks))
	}
	if !tasks[0].Canceled {
		t.Errorf("open task Canceled = false, want true")
	}
	if tasks[1].Canceled {
		t.Errorf("done task Canceled = true, want false")
	}
}

func TestCancelNonexistentProject(t *testing.T) {
	ws := newTestWorkspace(t)
	svc := NewService(newFakeGit())

	err := svc.Cancel(ws, "nope", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestCancelMissingWorktree(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	svc := NewService(newFakeGit())

	err := svc.Cancel(ws, "my-blog", CleanupNone)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project worktree 'my-blog' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestPublishCleanupWorktreeOnly(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Publish(ws, "my-blog", CleanupWorktree); err != nil {
		t.Fatal(err)
	}
	if len(fake.removeWorktree) != 1 || fake.removeWorktree[0] != ws.ProjectWorktreePath("my-blog") {
		t.Errorf("RemoveWorktree calls = %v", fake.removeWorktree)
	}
	if len(fake.deleteBranch) != 0 {
		t.Errorf("DeleteBranch calls = %v, want none", fake.deleteBranch)
	}
}

func TestPublishCleanupNone(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Publish(ws, "my-blog", CleanupNone); err != nil {
		t.Fatal(err)
	}
	if len(fake.removeWorktree) != 0 || len(fake.deleteBranch) != 0 {
		t.Errorf("cleanup calls = remove:%v delete:%v, want none", fake.removeWorktree, fake.deleteBranch)
	}
}

func TestCancelCleanupNone(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Cancel(ws, "my-blog", CleanupNone); err != nil {
		t.Fatal(err)
	}
	if len(fake.removeWorktree) != 0 || len(fake.deleteBranch) != 0 {
		t.Errorf("cleanup calls = remove:%v delete:%v, want none", fake.removeWorktree, fake.deleteBranch)
	}
}

func TestCancelCleanupBoth(t *testing.T) {
	ws := newTestWorkspace(t)
	addProjectEntry(t, ws, "my-blog", "")
	addProjectWorktree(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Cancel(ws, "my-blog", CleanupBoth); err != nil {
		t.Fatal(err)
	}
	// The worktree must be removed before the branch is deleted.
	if len(fake.removeWorktree) != 1 || fake.removeWorktree[0] != ws.ProjectWorktreePath("my-blog") {
		t.Errorf("RemoveWorktree calls = %v", fake.removeWorktree)
	}
	if len(fake.deleteBranch) != 1 || fake.deleteBranch[0] != "my-blog" {
		t.Errorf("DeleteBranch calls = %v", fake.deleteBranch)
	}
}

func TestListSkipsCanceled(t *testing.T) {
	ws := newTestWorkspace(t)
	projects := config.DefaultProjects()
	projects.Projects["active"] = config.ProjectEntry{Name: "active"}
	projects.Projects["published"] = config.ProjectEntry{Name: "published", Status: "published"}
	projects.Projects["canceled"] = config.ProjectEntry{Name: "canceled", Status: "canceled"}
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
	want := []string{"active", "published"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("list = %v, want %v", got, want)
	}
}
