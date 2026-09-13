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

// Git is the set of git operations this slice needs.
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
}

// New returns the production implementation backed by os/exec.
func New() Git {
	return &execGit{}
}
