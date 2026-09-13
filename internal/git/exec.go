package git

import (
	"bytes"
	"errors"
	"os"
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
	treeHash, err := output(repoPath, "hash-object", "-t", "tree", "/dev/null")
	if err != nil {
		return err
	}

	// commit-tree needs an author/committer identity. The freshly created
	// bare repo has no user config, so provide one via -c flags.
	commitHash, err := output(repoPath,
		"-c", "user.name=grind",
		"-c", "user.email=grind@localhost",
		"commit-tree", treeHash, "-m", "Initial commit")
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

// run executes git in dir ("" for the current directory), inheriting
// stdout/stderr so git progress and errors reach the terminal.
func run(dir string, args ...string) error {
	cmd := exec.Command("git", withDir(dir, args)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return grinderr.WrapSystem(err, "git %s", strings.Join(args, " "))
	}
	return nil
}

// output executes git in dir and returns its trimmed stdout. Stderr is
// inherited so git errors stay visible.
func output(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", withDir(dir, args)...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", grinderr.WrapSystem(err, "git %s", strings.Join(args, " "))
	}
	return strings.TrimSpace(buf.String()), nil
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
