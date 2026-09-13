package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
