package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

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

// HasChanges reports whether the worktree has any changes, including
// untracked files. `git status --porcelain` lists every change in a
// machine-readable form; a non-empty output means the worktree is dirty.
func (g *execGit) HasChanges(worktreePath string) (bool, error) {
	out, err := output(worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// CommitAll stages every change in the worktree and commits it. This is the
// ONE documented exception to the "never git add -A" rule: the rule protects
// the MAIN worktree, where config and state files live. A project worktree
// contains ONLY work product by construction (its branch starts at an empty
// tree), so staging everything there IS "stage the specific files changed".
// Like Commit, it refuses to run when the index has unmerged paths.
func (g *execGit) CommitAll(worktreePath, message string) error {
	unmerged, err := output(worktreePath, "ls-files", "-u")
	if err != nil {
		return err
	}
	if strings.TrimSpace(unmerged) != "" {
		return grinderr.NewSystem("refusing to commit: unmerged paths exist")
	}

	if err := run(worktreePath, "add", "-A"); err != nil {
		return err
	}
	return run(worktreePath, "commit", "-m", message)
}

// RemoteURL returns the origin remote URL, or "" when no remote is
// configured. `git remote get-url origin` exits non-zero without a remote;
// that is an empty result, not an error — save skips pushing silently.
func (g *execGit) RemoteURL(repoPath string) (string, error) {
	out, err := output(repoPath, "remote", "get-url", "origin")
	if err != nil {
		return "", nil
	}
	return out, nil
}

// PushAll runs `git push origin --all` in the bare repo. A failed push is
// not fatal — the work is committed locally — so the error carries git's
// stderr for a clean message instead of a hard failure.
func (g *execGit) PushAll(repoPath string) error {
	_, stderr, err := outputFull(repoPath, "push", "origin", "--all")
	if err == nil {
		return nil
	}
	return &PushError{Stderr: stderr, Err: err}
}

// DefaultBranch returns the name of the repo's current branch.
// `git symbolic-ref --short HEAD` prints just the branch name, e.g. "main".
func (g *execGit) DefaultBranch(repoPath string) (string, error) {
	return output(repoPath, "symbolic-ref", "--short", "HEAD")
}

// SetRemoteURL sets the origin remote URL, adding origin when it does not
// exist and updating it when it points elsewhere. push and pull call this
// after resolving the URL so the bare repo's origin always matches the
// workspace's configured remote.
func (g *execGit) SetRemoteURL(repoPath, url string) error {
	current, err := g.RemoteURL(repoPath)
	if err != nil {
		return err
	}
	if current == url {
		return nil
	}
	if current == "" {
		return run(repoPath, "remote", "add", "origin", url)
	}
	return run(repoPath, "remote", "set-url", "origin", url)
}

// PushBranch runs `git push -u origin <branch>` in the bare repo. The -u
// sets the upstream tracking ref on the first push. Git only does that
// automatically for the current branch of a checked-out worktree; pushing
// from a bare repo leaves branch.<name>.remote unset, so `git status` in
// .main would show no tracking info. -u is idempotent — on later pushes it
// just re-asserts the same upstream. Like PushAll, a failed push is not
// fatal — the work is committed locally — so the error carries git's stderr
// for a clean message.
func (g *execGit) PushBranch(repoPath, branch string) error {
	_, stderr, err := outputFull(repoPath, "push", "-u", "origin", branch)
	if err == nil {
		return nil
	}
	return &PushError{Stderr: stderr, Err: err}
}

// FetchAll runs `git fetch origin` in the bare repo, updating the
// refs/remotes/origin/* tracking branches that pull reads to decide what
// can be fast-forwarded.
func (g *execGit) FetchAll(repoPath string) error {
	return run(repoPath, "fetch", "origin")
}

// IsAncestor reports whether ancestor is an ancestor of descendant (or
// equal). `git merge-base --is-ancestor` exits 0 for yes and 1 for no; the
// exit-1 case is a result, not an error.
func (g *execGit) IsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	_, err := output(repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// FastForwardWorktree runs `git merge --ff-only origin/<branch>` in the
// worktree, updating both the branch ref and the working files. It fails
// when the worktree has uncommitted changes or the branch diverged; pull
// classifies the failure by checking HasChanges afterwards.
func (g *execGit) FastForwardWorktree(worktreePath, branch string) error {
	return run(worktreePath, "merge", "--ff-only", "origin/"+branch)
}

// FastForwardRef points refs/heads/<branch> at refs/remotes/origin/<branch>
// in the bare repo. pull uses it for branches that are not checked out in
// any worktree, where there are no working files to refresh.
func (g *execGit) FastForwardRef(repoPath, branch string) error {
	return run(repoPath, "update-ref", "refs/heads/"+branch, "refs/remotes/origin/"+branch)
}

// ListRemoteBranches returns the remote branch names under
// refs/remotes/origin/*, excluding the symbolic HEAD. `for-each-ref` prints
// one ref per line with the origin/ prefix, which is stripped to give plain
// branch names.
func (g *execGit) ListRemoteBranches(repoPath string) ([]string, error) {
	out, err := output(repoPath, "for-each-ref", "refs/remotes/origin", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimPrefix(line, "origin/")
		if name == "HEAD" {
			continue
		}
		branches = append(branches, name)
	}
	return branches, nil
}

// MergeBranch runs `git merge --no-ff --allow-unrelated-histories <branch>`
// in the worktree. publish uses it to bring a project branch into the
// default branch. --no-ff guarantees a visible merge commit in history even
// when the merge could fast-forward, so the publication is always a
// distinct, reviewable step.
//
// --allow-unrelated-histories is required because project branches start
// from an empty tree (CreateBranch), not from main's history: the
// constitution keeps project branches free of config and state, so they
// cannot branch off main. The merge brings the work product (the .idea
// file, etc.) into main's tree; there is no conflict because the project
// branch and main touch disjoint paths.
func (g *execGit) MergeBranch(worktreePath, branch string) error {
	return run(worktreePath, "merge", "--no-ff", "--allow-unrelated-histories", branch)
}

// RemoveWorktree runs `git worktree remove --force <path>` in the bare
// repo. publish/cancel use it after the user chose to clean up; --force is
// safe because the cleanup prompt already warned about losing uncommitted
// work.
func (g *execGit) RemoveWorktree(repoPath, worktreePath string) error {
	return run(repoPath, "worktree", "remove", "--force", worktreePath)
}

// DeleteBranch runs `git branch -D <branch>` in the bare repo, deleting the
// branch without checking whether it is merged. It is only called AFTER the
// worktree is removed — git refuses to delete a branch that is checked out
// in a worktree.
func (g *execGit) DeleteBranch(repoPath, branch string) error {
	return run(repoPath, "branch", "-D", branch)
}

// LastCommitDate returns the time of the branch's most recent commit.
// `git log <branch> -1 --format=%aI` prints the author date in strict ISO
// 8601, which time.RFC3339 parses directly.
//
// A branch that does not exist has no commits to date. That is an empty
// result — the dashboard shows "never" — but only after ruling out real
// git breakage, so the unknown-branch case is verified with show-ref
// before swallowing the failure.
func (g *execGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	out, err := output(repoPath, "log", branch, "-1", "--format=%aI")
	if err != nil {
		exists, existsErr := branchExists(repoPath, branch)
		if existsErr == nil && !exists {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	if out == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, out)
	if err != nil {
		return time.Time{}, grinderr.WrapSystem(err, "parse commit date %q", out)
	}
	return t, nil
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
	stdout, _, err := outputFull(dir, args...)
	return stdout, err
}

// outputFull executes git in dir and returns trimmed stdout and stderr.
// Push uses the stderr to build a warning when the remote is unreachable.
func outputFull(dir string, args ...string) (string, string, error) {
	cmd := exec.Command("git", withDir(dir, args)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", strings.TrimSpace(stderr.String()), grinderr.WrapSystem(err, "git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), nil
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
