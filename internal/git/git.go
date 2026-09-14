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
