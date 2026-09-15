package sync

import (
	"errors"
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

// fakeGit records the calls sync operations make so tests can assert what
// would have been sent to real git.
type fakeGit struct {
	defaultBranch  string
	remoteURL      string
	setRemoteURLs  []string
	pushBranch     []string
	pushAll        int
	pushErr        error
	fetchAll       int
	hasChanges     map[string]bool
	isAncestor     map[string]bool
	ffWorktree     []string
	ffWorktreeErr  error
	ffRef          []string
	remoteBranches []string
	addWorktree    [][]string
	addWorktreeErr error
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error {
	if f.addWorktreeErr != nil {
		return f.addWorktreeErr
	}
	f.addWorktree = append(f.addWorktree, []string{repoPath, worktreePath, branch})
	return os.MkdirAll(worktreePath, 0o755)
}

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error { return nil }

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) { return true, nil }

func (f *fakeGit) CreateBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) {
	return f.hasChanges[worktreePath], nil
}

func (f *fakeGit) CommitAll(worktreePath, message string) error { return nil }

func (f *fakeGit) RemoteURL(repoPath string) (string, error) {
	return f.remoteURL, nil
}

func (f *fakeGit) PushAll(repoPath string) error {
	f.pushAll++
	return f.pushErr
}

func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeGit) DefaultBranch(repoPath string) (string, error) {
	if f.defaultBranch != "" {
		return f.defaultBranch, nil
	}
	return "main", nil
}

func (f *fakeGit) SetRemoteURL(repoPath, url string) error {
	f.setRemoteURLs = append(f.setRemoteURLs, url)
	return nil
}

func (f *fakeGit) PushBranch(repoPath, branch string) error {
	f.pushBranch = append(f.pushBranch, branch)
	return f.pushErr
}

func (f *fakeGit) FetchAll(repoPath string) error {
	f.fetchAll++
	return nil
}

func (f *fakeGit) IsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	return f.isAncestor[ancestor+"|"+descendant], nil
}

func (f *fakeGit) FastForwardWorktree(worktreePath, branch string) error {
	f.ffWorktree = append(f.ffWorktree, branch)
	return f.ffWorktreeErr
}

func (f *fakeGit) FastForwardRef(repoPath, branch string) error {
	f.ffRef = append(f.ffRef, branch)
	return nil
}

func (f *fakeGit) ListRemoteBranches(repoPath string) ([]string, error) {
	return f.remoteBranches, nil
}

func (f *fakeGit) MergeBranch(worktreePath, branch string) error { return nil }

func (f *fakeGit) RemoveWorktree(repoPath, worktreePath string) error { return nil }

func (f *fakeGit) DeleteBranch(repoPath, branch string) error { return nil }

// Ensure the fake satisfies the interface the service depends on.
var _ git.Git = (*fakeGit)(nil)

// newTestWorkspace builds a workspace with config files and an optional
// remote URL in .grind.json.
func newTestWorkspace(t *testing.T, remoteURL string, projects config.ProjectsConfig) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, ".main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: main,
	}
	cfg := config.Default()
	if remoteURL != "" {
		cfg.Remote = &config.RemoteConfig{URL: remoteURL}
	}
	if err := config.Write(ws.GrindConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	return ws
}

// projectConfig returns a ProjectsConfig with one project of the given name.
func projectConfig(name string) config.ProjectsConfig {
	cfg := config.DefaultProjects()
	cfg.Projects[name] = config.ProjectEntry{Name: name}
	return cfg
}

func TestPushNoRemote(t *testing.T) {
	ws := newTestWorkspace(t, "", config.DefaultProjects())
	fake := &fakeGit{}
	svc := NewService(fake)

	_, err := svc.Push(ws, ScopeMain, "")
	if err == nil {
		t.Fatal("Push() = nil error, want no-remote user error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error = %T, want *grinderr.User", err)
	}
	if !strings.Contains(err.Error(), "No remote configured") {
		t.Errorf("error = %q, want no-remote message", err.Error())
	}
	if len(fake.setRemoteURLs) != 0 {
		t.Errorf("SetRemoteURL calls = %v, want 0", fake.setRemoteURLs)
	}
}

func TestPushURLFromConfigSyncsOrigin(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{}
	svc := NewService(fake)

	if _, err := svc.Push(ws, ScopeMain, ""); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if len(fake.setRemoteURLs) != 1 || fake.setRemoteURLs[0] != "git@example.com:repo.git" {
		t.Errorf("SetRemoteURL calls = %v, want [git@example.com:repo.git]", fake.setRemoteURLs)
	}
}

func TestPushMainUsesDefaultBranch(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{defaultBranch: "develop"}
	svc := NewService(fake)

	branch, err := svc.Push(ws, ScopeMain, "")
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if branch != "develop" {
		t.Errorf("Push() branch = %q, want %q", branch, "develop")
	}
	if len(fake.pushBranch) != 1 || fake.pushBranch[0] != "develop" {
		t.Errorf("PushBranch calls = %v, want [develop]", fake.pushBranch)
	}
}

func TestPushProjectUsesProjectName(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	fake := &fakeGit{}
	svc := NewService(fake)

	branch, err := svc.Push(ws, ScopeProject, "my-blog")
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if branch != "my-blog" {
		t.Errorf("Push() branch = %q, want %q", branch, "my-blog")
	}
	if len(fake.pushBranch) != 1 || fake.pushBranch[0] != "my-blog" {
		t.Errorf("PushBranch calls = %v, want [my-blog]", fake.pushBranch)
	}
}

func TestPushUnknownProject(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{}
	svc := NewService(fake)

	_, err := svc.Push(ws, ScopeProject, "nope")
	if err == nil {
		t.Fatal("Push() = nil error, want unknown-project user error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
	if len(fake.pushBranch) != 0 {
		t.Errorf("PushBranch calls = %v, want 0", fake.pushBranch)
	}
}

func TestPushDirtyMain(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{hasChanges: map[string]bool{ws.MainWorktree: true}}
	svc := NewService(fake)

	_, err := svc.Push(ws, ScopeMain, "")
	if err == nil {
		t.Fatal("Push() = nil error, want dirty-main user error")
	}
	if !strings.Contains(err.Error(), "You have uncommitted changes in .main. Run 'grind save' to commit them.") {
		t.Errorf("error = %q", err.Error())
	}
	if len(fake.pushBranch) != 0 {
		t.Errorf("PushBranch calls = %v, want 0", fake.pushBranch)
	}
}

func TestPushDirtyProject(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	worktree := ws.ProjectWorktreePath("my-blog")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{hasChanges: map[string]bool{worktree: true}}
	svc := NewService(fake)

	_, err := svc.Push(ws, ScopeProject, "my-blog")
	if err == nil {
		t.Fatal("Push() = nil error, want dirty-project user error")
	}
	if !strings.Contains(err.Error(), "You have uncommitted changes in my-blog/. Run 'grind save my-blog' to commit them.") {
		t.Errorf("error = %q", err.Error())
	}
	if len(fake.pushBranch) != 0 {
		t.Errorf("PushBranch calls = %v, want 0", fake.pushBranch)
	}
}

func TestPushAllListsEveryDirtyWorktree(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("a"))
	// Add a second project.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	projects.Projects["b"] = config.ProjectEntry{Name: "b"}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	worktreeA := ws.ProjectWorktreePath("a")
	worktreeB := ws.ProjectWorktreePath("b")
	if err := os.MkdirAll(worktreeA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeB, 0o755); err != nil {
		t.Fatal(err)
	}

	fake := &fakeGit{hasChanges: map[string]bool{
		ws.MainWorktree: true,
		worktreeA:       true,
		worktreeB:       true,
	}}
	svc := NewService(fake)

	_, err = svc.Push(ws, ScopeAll, "")
	if err == nil {
		t.Fatal("Push() = nil error, want dirty worktrees user error")
	}
	msg := err.Error()
	for _, want := range []string{
		"You have uncommitted changes in .main. Run 'grind save' to commit them.",
		"You have uncommitted changes in a/. Run 'grind save a' to commit them.",
		"You have uncommitted changes in b/. Run 'grind save b' to commit them.",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q:\n%s", want, msg)
		}
	}
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}

func TestPushAllCallsPushAll(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	fake := &fakeGit{}
	svc := NewService(fake)

	if _, err := svc.Push(ws, ScopeAll, ""); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if fake.pushAll != 1 {
		t.Errorf("PushAll calls = %d, want 1", fake.pushAll)
	}
	if len(fake.pushBranch) != 0 {
		t.Errorf("PushBranch calls = %v, want 0", fake.pushBranch)
	}
}

func TestPushErrorBecomesUserError(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{pushErr: &git.PushError{
		Stderr: "fatal: unable to access",
		Err:    errors.New("push failed"),
	}}
	svc := NewService(fake)

	_, err := svc.Push(ws, ScopeMain, "")
	if err == nil {
		t.Fatal("Push() = nil error, want user error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error = %T, want *grinderr.User", err)
	}
	if !strings.Contains(err.Error(), "fatal: unable to access") {
		t.Errorf("error = %q, want git's stderr", err.Error())
	}
}

func TestPullHappyPath(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	worktree := ws.ProjectWorktreePath("my-blog")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{
		remoteBranches: []string{"main", "my-blog"},
		isAncestor: map[string]bool{
			"main|origin/main":       true,
			"origin/main|main":       false,
			"my-blog|origin/my-blog": true,
			"origin/my-blog|my-blog": false,
		},
	}
	svc := NewService(fake)

	result, err := svc.Pull(ws)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if fake.fetchAll != 1 {
		t.Errorf("FetchAll calls = %d, want 1", fake.fetchAll)
	}
	if len(result.Updated) != 2 {
		t.Errorf("Updated = %v, want [main my-blog]", result.Updated)
	}
	if len(fake.ffWorktree) != 2 {
		t.Errorf("FastForwardWorktree calls = %v, want 2", fake.ffWorktree)
	}
}

func TestPullDirtyMainFailsBeforeFetch(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{hasChanges: map[string]bool{ws.MainWorktree: true}}
	svc := NewService(fake)

	_, err := svc.Pull(ws)
	if err == nil {
		t.Fatal("Pull() = nil error, want dirty-main user error")
	}
	if !strings.Contains(err.Error(), "You have uncommitted changes in .main. Run 'grind save' before pulling.") {
		t.Errorf("error = %q", err.Error())
	}
	if fake.fetchAll != 0 {
		t.Errorf("FetchAll calls = %d, want 0 (fail before fetch)", fake.fetchAll)
	}
}

func TestPullDivergedBranchReported(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	worktree := ws.ProjectWorktreePath("my-blog")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{
		remoteBranches: []string{"main", "my-blog"},
		isAncestor: map[string]bool{
			"main|origin/main":       true,
			"origin/main|main":       true, // main is up to date
			"my-blog|origin/my-blog": false,
		},
	}
	svc := NewService(fake)

	result, err := svc.Pull(ws)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Diverged) != 1 || result.Diverged[0] != "my-blog" {
		t.Errorf("Diverged = %v, want [my-blog]", result.Diverged)
	}
	if len(fake.ffWorktree) != 0 {
		t.Errorf("FastForwardWorktree calls = %v, want 0", fake.ffWorktree)
	}
}

func TestPullLocalAheadSkipped(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	worktree := ws.ProjectWorktreePath("my-blog")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	// my-blog is ahead of the remote (remote is an ancestor of local):
	// there is nothing to pull, and it is NOT a divergence.
	fake := &fakeGit{
		remoteBranches: []string{"main", "my-blog"},
		isAncestor: map[string]bool{
			"main|origin/main":       true,
			"origin/main|main":       true, // main is up to date
			"my-blog|origin/my-blog": false,
			"origin/my-blog|my-blog": true, // remote is an ancestor of local
		},
	}
	svc := NewService(fake)

	result, err := svc.Pull(ws)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Diverged) != 0 {
		t.Errorf("Diverged = %v, want none (local is ahead, not diverged)", result.Diverged)
	}
	if len(result.Updated) != 0 {
		t.Errorf("Updated = %v, want none", result.Updated)
	}
	if len(fake.ffWorktree) != 0 {
		t.Errorf("FastForwardWorktree calls = %v, want 0", fake.ffWorktree)
	}
}

func TestPullDirtyProjectWorktreeReported(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", projectConfig("my-blog"))
	worktree := ws.ProjectWorktreePath("my-blog")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{
		remoteBranches: []string{"main", "my-blog"},
		isAncestor: map[string]bool{
			"main|origin/main":       true,
			"origin/main|main":       true, // main is up to date
			"my-blog|origin/my-blog": true,
			"origin/my-blog|my-blog": false,
		},
		ffWorktreeErr: errors.New("merge failed"),
		hasChanges:    map[string]bool{worktree: true},
	}
	svc := NewService(fake)

	result, err := svc.Pull(ws)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Dirty) != 1 || result.Dirty[0] != "my-blog" {
		t.Errorf("Dirty = %v, want [my-blog]", result.Dirty)
	}
	if len(result.Updated) != 0 {
		t.Errorf("Updated = %v, want none", result.Updated)
	}
}

func TestPullCreatesMissingWorktree(t *testing.T) {
	ws := newTestWorkspace(t, "git@example.com:repo.git", config.DefaultProjects())
	fake := &fakeGit{
		remoteBranches: []string{"main", "leenix"},
		isAncestor: map[string]bool{
			"main|origin/main":       true,
			"origin/main|main":       true, // main is up to date
		},
	}
	svc := NewService(fake)

	result, err := svc.Pull(ws)
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Created) != 1 || result.Created[0] != "leenix" {
		t.Errorf("Created = %v, want [leenix]", result.Created)
	}
	if len(fake.addWorktree) != 1 {
		t.Fatalf("AddWorktree calls = %v, want 1", fake.addWorktree)
	}
	if fake.addWorktree[0][2] != "leenix" {
		t.Errorf("AddWorktree branch = %q, want leenix", fake.addWorktree[0][2])
	}
}

func TestPullNoRemote(t *testing.T) {
	ws := newTestWorkspace(t, "", config.DefaultProjects())
	fake := &fakeGit{}
	svc := NewService(fake)

	_, err := svc.Pull(ws)
	if err == nil {
		t.Fatal("Pull() = nil error, want no-remote user error")
	}
	if !strings.Contains(err.Error(), "No remote configured") {
		t.Errorf("error = %q, want no-remote message", err.Error())
	}
	if fake.fetchAll != 0 {
		t.Errorf("FetchAll calls = %d, want 0", fake.fetchAll)
	}
}