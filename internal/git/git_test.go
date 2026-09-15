package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setGitIdentity makes `git commit` work in tests without depending on the
// developer's global git config.
func setGitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Grind Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "grind-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Grind Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "grind-test@example.com")
}

func TestExecGitLifecycle(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatalf("InitBare() error = %v", err)
	}
	if _, err := os.Stat(bareRepo); err != nil {
		t.Fatalf("bare repo not created: %v", err)
	}

	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatalf("InitialCommit() error = %v", err)
	}

	main := filepath.Join(root, ".main")
	if err := g.AddWorktree(bareRepo, main, "main"); err != nil {
		t.Fatalf("AddWorktree() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(main, ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}

	// Write a file and commit it through the interface.
	ideasDir := filepath.Join(main, "ideas")
	if err := os.MkdirAll(ideasDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ideaFile := filepath.Join(ideasDir, "20260101000000.md")
	if err := os.WriteFile(ideaFile, []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add idea: Test", "ideas/20260101000000.md"); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	// The main worktree must be clean after the commit.
	out, err := output(main, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status error = %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("worktree not clean after commit, status:\n%s", out)
	}

	// The commit must be reachable on the main branch.
	log, err := output(main, "log", "--oneline", "-1")
	if err != nil {
		t.Fatalf("git log error = %v", err)
	}
	if !strings.Contains(log, "Add idea: Test") {
		t.Errorf("latest commit = %q, want message %q", strings.TrimSpace(log), "Add idea: Test")
	}
}

func TestRunErrorIncludesGitStderr(t *testing.T) {
	// Run git in a directory that is not a repository. The failure message
	// must include git's stderr so the user can still debug what went wrong.
	err := run(t.TempDir(), "status")
	if err == nil {
		t.Fatal("run() = nil error, want failure outside a repository")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error = %q, want it to include git's stderr", err)
	}
}

func TestCommitStagesDeletion(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, ".main")
	if err := g.AddWorktree(bareRepo, main, "main"); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(main, "ideas", "rejected-20260101000000.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("# Bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add idea: Bad", "ideas/rejected-20260101000000.md"); err != nil {
		t.Fatal(err)
	}

	// Delete the file, then Commit with the deleted path. `git add <path>`
	// stages the deletion, so the worktree ends up clean.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Prune 1 rejected idea(s)", "ideas/rejected-20260101000000.md"); err != nil {
		t.Fatalf("Commit() after delete error = %v", err)
	}

	out, err := output(main, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("worktree not clean after deletion commit, status:\n%s", out)
	}
}

func TestCommitRefusesUnmergedPaths(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, ".main")
	if err := g.AddWorktree(bareRepo, main, "main"); err != nil {
		t.Fatal(err)
	}

	// Create a file on main and commit it.
	file := filepath.Join(main, "conflict.txt")
	if err := os.WriteFile(file, []byte("main version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add conflict.txt", "conflict.txt"); err != nil {
		t.Fatal(err)
	}

	// Create a second worktree on a new branch and change the same file.
	other := filepath.Join(root, "other")
	if err := g.AddWorktree(bareRepo, other, "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "conflict.txt"), []byte("feature version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(other, "Change conflict.txt", "conflict.txt"); err != nil {
		t.Fatal(err)
	}

	// Change the file on main too, then merge feature → conflict.
	if err := os.WriteFile(file, []byte("main version 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Change conflict.txt on main", "conflict.txt"); err != nil {
		t.Fatal(err)
	}
	if err := run(main, "merge", "feature"); err == nil {
		t.Fatal("merge unexpectedly succeeded, want conflict")
	}

	// Commit must now refuse because unmerged paths exist.
	if err := g.Commit(main, "Should not commit", "conflict.txt"); err == nil {
		t.Fatal("Commit() = nil error, want refusal due to unmerged paths")
	}
}

func TestIsPathClean(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, ".main")
	if err := g.AddWorktree(bareRepo, main, "main"); err != nil {
		t.Fatal(err)
	}

	// A tracked file with no changes is clean.
	file := filepath.Join(main, "state.json")
	if err := os.WriteFile(file, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add state.json", "state.json"); err != nil {
		t.Fatal(err)
	}
	clean, err := g.IsPathClean(main, "state.json")
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("IsPathClean = false, want true for unchanged file")
	}

	// A modified file is dirty for its own path...
	if err := os.WriteFile(file, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = g.IsPathClean(main, "state.json")
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Error("IsPathClean = true, want false for modified file")
	}

	// ...but a second, untouched file stays clean while the first is dirty.
	other := filepath.Join(main, "other.txt")
	if err := os.WriteFile(other, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add other.txt", "other.txt"); err != nil {
		t.Fatal(err)
	}
	clean, err = g.IsPathClean(main, "other.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("IsPathClean = false for untouched file while another is dirty")
	}

	// An untracked file is dirty for its own path.
	if err := os.WriteFile(filepath.Join(main, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = g.IsPathClean(main, "untracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Error("IsPathClean = true, want false for untracked file")
	}
}

func TestCreateBranch(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatal(err)
	}

	if err := g.CreateBranch(bareRepo, "my-blog"); err != nil {
		t.Fatalf("CreateBranch() error = %v", err)
	}

	// The branch must exist and point at a commit with an empty tree.
	tree, err := output(bareRepo, "rev-parse", "my-blog^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	emptyTree, err := output(bareRepo, "hash-object", "-t", "tree", "/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if tree != emptyTree {
		t.Errorf("branch tree = %q, want empty tree %q", tree, emptyTree)
	}

	// Creating the same branch again must fail.
	if err := g.CreateBranch(bareRepo, "my-blog"); err == nil {
		t.Error("CreateBranch() = nil error, want failure for existing branch")
	}
}

func TestAddWorktreeExistingBranch(t *testing.T) {
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatal(err)
	}
	if err := g.CreateBranch(bareRepo, "my-blog"); err != nil {
		t.Fatal(err)
	}

	// AddWorktree with an existing branch must not pass -b, which git would
	// refuse.
	wt := filepath.Join(root, "my-blog")
	if err := g.AddWorktree(bareRepo, wt, "my-blog"); err != nil {
		t.Fatalf("AddWorktree() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
}

// newBareRepoWithMain is a helper that creates a bare repo with an initial
// commit on main and a .main worktree, so tests can exercise worktree-level
// operations.
func newBareRepoWithMain(t *testing.T) (bareRepo, main string) {
	t.Helper()
	setGitIdentity(t)
	root := t.TempDir()
	g := New()

	bareRepo = filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatal(err)
	}
	main = filepath.Join(root, ".main")
	if err := g.AddWorktree(bareRepo, main, "main"); err != nil {
		t.Fatal(err)
	}
	return bareRepo, main
}

func TestHasChanges(t *testing.T) {
	_, main := newBareRepoWithMain(t)
	g := New()

	// A fresh worktree has no changes.
	has, err := g.HasChanges(main)
	if err != nil {
		t.Fatalf("HasChanges() error = %v", err)
	}
	if has {
		t.Error("HasChanges() = true on a clean worktree")
	}

	// An untracked file counts as a change.
	file := filepath.Join(main, "notes.md")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err = g.HasChanges(main)
	if err != nil {
		t.Fatalf("HasChanges() error = %v", err)
	}
	if !has {
		t.Error("HasChanges() = false with an untracked file")
	}
}

func TestCommitAllStagesNewModifiedAndDeleted(t *testing.T) {
	bareRepo, main := newBareRepoWithMain(t)
	g := New()

	// A tracked file to modify and delete later.
	tracked := filepath.Join(main, "tracked.md")
	if err := os.WriteFile(tracked, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add tracked.md", "tracked.md"); err != nil {
		t.Fatal(err)
	}

	// One new file, one modified file, one deleted file.
	if err := os.WriteFile(filepath.Join(main, "new.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tracked); err != nil {
		t.Fatal(err)
	}

	if err := g.CommitAll(main, "Save everything"); err != nil {
		t.Fatalf("CommitAll() error = %v", err)
	}

	// The worktree must be clean: add -A staged the new file, the
	// modification, and the deletion.
	has, err := g.HasChanges(main)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("worktree not clean after CommitAll")
	}

	// The deletion must be committed, not just staged.
	_, err = output(bareRepo, "cat-file", "-e", "HEAD:tracked.md")
	if err == nil {
		t.Error("tracked.md still exists in HEAD after CommitAll")
	}
}

func TestCommitAllRefusesUnmergedPaths(t *testing.T) {
	bareRepo, main := newBareRepoWithMain(t)
	g := New()

	// Create a conflict: two branches modify the same file, then merge with
	// conflicts left in the index.
	file := filepath.Join(main, "conflict.md")
	if err := os.WriteFile(file, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add conflict.md", "conflict.md"); err != nil {
		t.Fatal(err)
	}

	// Branch "other" must share history with main for the merge to be
	// attempted, so create it at HEAD rather than from an empty tree.
	if err := run(bareRepo, "branch", "other"); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(main), "other")
	if err := g.AddWorktree(bareRepo, other, "other"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "conflict.md"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.CommitAll(other, "other side"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "main side", "conflict.md"); err != nil {
		t.Fatal(err)
	}

	// Merge other into main without committing; the conflict stays in the
	// index as unmerged paths.
	if err := run(main, "merge", "other"); err == nil {
		t.Fatal("merge succeeded, want a conflict")
	}

	err := g.CommitAll(main, "should not commit")
	if err == nil {
		t.Fatal("CommitAll() = nil error, want refusal on unmerged paths")
	}
	if !strings.Contains(err.Error(), "unmerged") {
		t.Errorf("error = %q, want mention of unmerged paths", err)
	}
}

func TestRemoteURL(t *testing.T) {
	bareRepo, _ := newBareRepoWithMain(t)
	g := New()

	// No remote configured: empty URL, no error.
	url, err := g.RemoteURL(bareRepo)
	if err != nil {
		t.Fatalf("RemoteURL() error = %v", err)
	}
	if url != "" {
		t.Errorf("RemoteURL() = %q, want empty without a remote", url)
	}

	// With a remote, the URL comes back verbatim.
	if err := run(bareRepo, "remote", "add", "origin", "git@example.com:grind.git"); err != nil {
		t.Fatal(err)
	}
	url, err = g.RemoteURL(bareRepo)
	if err != nil {
		t.Fatalf("RemoteURL() error = %v", err)
	}
	if url != "git@example.com:grind.git" {
		t.Errorf("RemoteURL() = %q", url)
	}
}

func TestPushAll(t *testing.T) {
	bareRepo, main := newBareRepoWithMain(t)
	g := New()

	// A bare repo can be its own remote: clone it, then push back.
	remote := filepath.Join(filepath.Dir(bareRepo), "remote.git")
	if err := run("", "clone", "--bare", bareRepo, remote); err != nil {
		t.Fatal(err)
	}
	if err := run(bareRepo, "remote", "add", "origin", remote); err != nil {
		t.Fatal(err)
	}

	// Make a commit on main so there is something to push.
	if err := os.WriteFile(filepath.Join(main, "pushed.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Push me", "pushed.md"); err != nil {
		t.Fatal(err)
	}

	// A second branch (a project branch) must also reach the remote.
	if err := g.CreateBranch(bareRepo, "my-blog"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddWorktree(bareRepo, filepath.Join(filepath.Dir(bareRepo), "my-blog"), "my-blog"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(bareRepo), "my-blog", "work.md"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.CommitAll(filepath.Join(filepath.Dir(bareRepo), "my-blog"), "Project work"); err != nil {
		t.Fatal(err)
	}

	if err := g.PushAll(bareRepo); err != nil {
		t.Fatalf("PushAll() error = %v", err)
	}

	// The remote must now have both branches.
	for branch, want := range map[string]string{"main": "Push me", "my-blog": "Project work"} {
		log, err := output(remote, "log", "--oneline", "-1", branch)
		if err != nil {
			t.Fatalf("remote log %s: %v", branch, err)
		}
		if !strings.Contains(log, want) {
			t.Errorf("remote %s latest commit = %q, want %q", branch, strings.TrimSpace(log), want)
		}
	}
}

func TestPushAllFailureCarriesStderr(t *testing.T) {
	bareRepo, _ := newBareRepoWithMain(t)
	g := New()

	// Point origin at a path that does not exist, so push fails.
	if err := run(bareRepo, "remote", "add", "origin", "/nonexistent/remote.git"); err != nil {
		t.Fatal(err)
	}

	err := g.PushAll(bareRepo)
	if err == nil {
		t.Fatal("PushAll() = nil error, want failure against a missing remote")
	}

	// The error must be a PushError carrying git's stderr, so push can
	// print a clean message.
	var pushErr *PushError
	if !errors.As(err, &pushErr) {
		t.Fatalf("error = %T, want *PushError", err)
	}
	if pushErr.Stderr == "" {
		t.Error("PushError.Stderr is empty, want git's stderr")
	}
	if !strings.Contains(pushErr.Stderr, "does not appear to be a git repository") {
		t.Errorf("PushError.Stderr = %q", pushErr.Stderr)
	}
}

func TestLastCommitDate(t *testing.T) {
	setGitIdentity(t)
	bareRepo, main := newBareRepoWithMain(t)
	g := New()

	// Pin the author date so the expected value is exact. %aI prints the
	// AUTHOR date, so GIT_AUTHOR_DATE is the one to control.
	t.Setenv("GIT_AUTHOR_DATE", "2026-09-14T10:00:00+00:00")
	if err := os.WriteFile(filepath.Join(main, "note.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(main, "Add note", "note.md"); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	got, err := g.LastCommitDate(bareRepo, "main")
	if err != nil {
		t.Fatalf("LastCommitDate() error = %v", err)
	}
	want := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("LastCommitDate() = %v, want %v", got, want)
	}

	// A branch that does not exist has no commits to date. That is an
	// empty result (the dashboard shows "never"), not an error.
	missing, err := g.LastCommitDate(bareRepo, "no-such-branch")
	if err != nil {
		t.Fatalf("LastCommitDate(missing branch) error = %v", err)
	}
	if !missing.IsZero() {
		t.Errorf("LastCommitDate(missing branch) = %v, want zero time", missing)
	}
}
