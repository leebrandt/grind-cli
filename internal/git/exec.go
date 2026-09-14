package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/leebrandt/grind/internal/grinderr"
)

// execGit is the production Git implementation backed by the git binary.
type execGit struct{}

// InitBare runs `git init --bare <path>`.
func (g *execGit) InitBare(path string) error {
	return run("", "init", "--bare", path)
}

// InitialCommit creates the first commit on branch in a bare repo.
//
// A bare repo has no working tree, so the commit is assembled from an
// empty tree object: hash the empty tree, commit it, then point the branch
// ref at the resulting commit.
func (g *execGit) InitialCommit(repoPath, branch string) error {
	commitHash, err := emptyTreeCommit(repoPath, "Initial commit")
	if err != nil {
		return err
	}

	if err := run(repoPath, "update-ref", "refs/heads/"+branch, commitHash); err != nil {
		return err
	}

	// Point HEAD at the branch so later `git worktree add -b <other>` calls
	// have a valid starting reference. `git init --bare` may leave HEAD on a
	// branch name that was never created, which makes worktree add fail with
	// "invalid reference: HEAD".
	return run(repoPath, "symbolic-ref", "HEAD", "refs/heads/"+branch)
}

// emptyTreeCommit creates a commit with an empty tree in repoPath and
// returns its hash. A bare repo has no working tree, so the commit is
// assembled from an empty tree object rather than from staged files.
func emptyTreeCommit(repoPath, message string) (string, error) {
	treeHash, err := output(repoPath, "hash-object", "-t", "tree", "/dev/null")
	if err != nil {
		return "", err
	}

	// commit-tree needs an author/committer identity. The freshly created
	// bare repo has no user config, so provide one via -c flags.
	return output(repoPath,
		"-c", "user.name=grind",
		"-c", "user.email=grind@localhost",
		"commit-tree", treeHash, "-m", message)
}

// CreateBranch creates a branch pointing at an empty-tree commit. Project
// branches start from an empty tree so the worktree contains only the
// actual work product — no configs, no state files.
func (g *execGit) CreateBranch(repoPath, branch string) error {
	exists, err := branchExists(repoPath, branch)
	if err != nil {
		return err
	}
	if exists {
		// A leftover branch is an environment inconsistency, not a user
		// mistake, so it is a system error.
		return grinderr.NewSystem(fmt.Sprintf("branch %s already exists", branch))
	}

	commitHash, err := emptyTreeCommit(repoPath, "Initialize project branch")
	if err != nil {
		return err
	}
	return run(repoPath, "update-ref", "refs/heads/"+branch, commitHash)
}

// IsPathClean reports whether the given path in worktreePath has no
// changes. `git status --porcelain -- <path>` lists only changes to that
// path, so a dirty file elsewhere in the worktree does not count.
func (g *execGit) IsPathClean(worktreePath, path string) (bool, error) {
	out, err := output(worktreePath, "status", "--porcelain", "--", path)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// AddWorktree adds a worktree at worktreePath checked out to branch. If the
// branch already exists (e.g. re-running init after a partial failure) we
// must not pass -b, because git would refuse.
func (g *execGit) AddWorktree(repoPath, worktreePath, branch string) error {
	exists, err := branchExists(repoPath, branch)
	if err != nil {
		return err
	}
	if exists {
		return run(repoPath, "worktree", "add", worktreePath, branch)
	}
	return run(repoPath, "worktree", "add", worktreePath, "-b", branch)
}

// Commit stages exactly the given paths and commits them. It refuses to
// commit when `git ls-files -u` reports unmerged paths, because a merge
// conflict in the main worktree would corrupt the config-on-main design.
func (g *execGit) Commit(worktreePath, message string, paths ...string) error {
	if len(paths) == 0 {
		// `git add` with no pathspec stages everything, which would break
		// the "stage only specific files" invariant.
		return grinderr.NewSystem("refusing to commit: no paths given")
	}

	unmerged, err := output(worktreePath, "ls-files", "-u")
	if err != nil {
		return err
	}
	if strings.TrimSpace(unmerged) != "" {
		return grinderr.NewSystem("refusing to commit: unmerged paths exist")
	}

	// The `--` separator protects against paths that start with a dash.
	// `git add <path>` also stages deletions of tracked files, which is
	// what prune relies on.
	args := append([]string{"add", "--"}, paths...)
	if err := run(worktreePath, args...); err != nil {
		return err
	}
	return run(worktreePath, "commit", "-m", message)
}

// branchExists reports whether refs/heads/<branch> exists in repoPath.
func branchExists(repoPath, branch string) (bool, error) {
	_, err := output(repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		// show-ref exits 1 when the ref does not exist.
		return false, nil
	}
	return false, err
}

// run executes git in dir ("" for the current directory). Output is
// captured and discarded on success so git's chatter never leaks into
// grind's output; on failure the captured stderr is included in the error
// so the user can still debug.
func run(dir string, args ...string) error {
	_, err := output(dir, args...)
	return err
}

// output executes git in dir and returns its trimmed stdout. Like run, it
// captures stderr and includes it in the error on failure.
func output(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", withDir(dir, args)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", grinderr.WrapSystem(err, "git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// withDir prefixes args with -C dir when dir is non-empty, so the command
// runs in that repository or worktree.
func withDir(dir string, args []string) []string {
	if dir == "" {
		return args
	}
	cmdArgs := make([]string, 0, len(args)+2)
	cmdArgs = append(cmdArgs, "-C", dir)
	return append(cmdArgs, args...)
}
