// Package git wraps the git commands grind needs behind an interface.
//
// The interface exists so tests can substitute a fake implementation and
// verify command flows without touching a real repository.
//
// The production implementation has two hard rules from the design
// constitution:
//   - Every command runs via os/exec with an argv array. No shell strings.
//   - Commit stages only the paths it is given, never `git add -A`, and
//     refuses to run when the index has unmerged paths (which would corrupt
//     the config-on-main design).
package git

import (
	"time"
)

// Git is the set of git operations grind needs.
type Git interface {
	// InitBare creates a bare repository at path.
	InitBare(path string) error
	// InitialCommit creates the first commit on branch in the bare repo.
	// A bare repo has no working tree, so the commit is built from an
	// empty tree object rather than from staged files.
	InitialCommit(repoPath, branch string) error
	// AddWorktree adds a worktree at worktreePath checked out to branch,
	// creating the branch first if it does not exist yet.
	AddWorktree(repoPath, worktreePath, branch string) error
	// Commit stages exactly the given paths (relative to worktreePath) and
	// commits them with message. It refuses to commit when no paths are
	// given or when unmerged paths exist.
	Commit(worktreePath, message string, paths ...string) error
	// IsPathClean reports whether the given path in worktreePath has no
	// changes. Unlike a full-worktree check, it ignores everything else, so
	// a dirty idea file does not block project creation.
	IsPathClean(worktreePath, path string) (bool, error)
	// CreateBranch creates a branch pointing at an empty-tree commit in the
	// bare repo. Project branches start from an empty tree so the worktree
	// contains only the actual work product, never grind's state files. It
	// errors when the branch already exists.
	CreateBranch(repoPath, branch string) error
	// HasChanges reports whether the worktree has any changes, including
	// untracked files. `save` uses it to decide whether the project worktree
	// needs a commit.
	HasChanges(worktreePath string) (bool, error)
	// CommitAll stages every change in the worktree and commits it. This is
	// the ONE documented exception to the "never git add -A" rule: the rule
	// protects the MAIN worktree, where config and state files live. A
	// project worktree contains only work product by construction, so
	// staging everything there IS "stage the specific files changed".
	CommitAll(worktreePath, message string) error
	// RemoteURL returns the origin remote URL, or "" when no remote is
	// configured. `push` uses it to give a clean error when there is
	// nothing to push to.
	RemoteURL(repoPath string) (string, error)
	// PushAll runs `git push origin --all` in the bare repo, pushing every
	// branch (the default branch plus each project branch) so the remote
	// mirrors the local bare repo. A failed push is not fatal — the work is
	// committed locally — so the error carries git's stderr for a clean
	// message instead of a hard failure.
	PushAll(repoPath string) error
	// LastCommitDate returns the time of the branch's most recent commit
	// in the bare repo, or the zero time when the branch has no commits.
	// The wwd dashboard uses it for the "Last Commit" column.
	LastCommitDate(repoPath, branch string) (time.Time, error)
	// DefaultBranch returns the name of the repo's current branch
	// (`git symbolic-ref --short HEAD`). The rewrite hardcodes "main" at
	// init, but resolving it here avoids v1's bug of hardcoding "main" in
	// pull logic.
	DefaultBranch(repoPath string) (string, error)
	// SetRemoteURL sets the origin remote URL, adding origin when it does
	// not exist and updating it otherwise. push and pull sync origin from
	// .grind.json's remote.url so the URL travels with the workspace.
	SetRemoteURL(repoPath, url string) error
	// PushBranch runs `git push origin <branch>` in the bare repo. Like
	// PushAll, a failed push returns a *PushError carrying git's stderr.
	PushBranch(repoPath, branch string) error
	// FetchAll runs `git fetch origin` in the bare repo, updating the
	// refs/remotes/origin/* tracking branches.
	FetchAll(repoPath string) error
	// IsAncestor reports whether ancestor is an ancestor of descendant
	// (or equal). pull uses it to decide whether a branch can fast-forward.
	IsAncestor(repoPath, ancestor, descendant string) (bool, error)
	// FastForwardWorktree runs `git merge --ff-only origin/<branch>` in the
	// worktree, updating both the branch ref and the working files. It fails
	// when the worktree has uncommitted changes or the branch diverged.
	FastForwardWorktree(worktreePath, branch string) error
	// FastForwardRef points refs/heads/<branch> at refs/remotes/origin/<branch>
	// in the bare repo, for branches not checked out in any worktree. Callers
	// verify IsAncestor first.
	FastForwardRef(repoPath, branch string) error
	// ListRemoteBranches returns the remote branch names
	// (refs/remotes/origin/*, excluding HEAD).
	ListRemoteBranches(repoPath string) ([]string, error)
}

// PushError is returned by PushAll when git push fails. It carries git's
// stderr so callers can print a clean message without treating the failure
// as fatal: the work is committed locally, the remote is best-effort.
type PushError struct {
	Stderr string
	Err    error
}

// Error implements the error interface.
func (e *PushError) Error() string { return e.Err.Error() }

// Unwrap lets errors.As see through to the underlying git error.
func (e *PushError) Unwrap() error { return e.Err }

// New returns the production implementation backed by os/exec.
func New() Git {
	return &execGit{}
}
