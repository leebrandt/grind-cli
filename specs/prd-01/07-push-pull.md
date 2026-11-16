# Spec 07: Push / Pull

**Slice 7 of the Go rewrite of the grind CLI.** This slice delivers the
cross-machine sync: `grind push` and `grind pull`. It also adds the missing
commit verb for the main worktree: `grind save` with no arguments.

Everything else (config, publish/cancel, invoices, migrate) comes in later
slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

These change the constitution in AGENTS.md — update it as part of this slice.

1. **`grind save` with no arguments commits the main worktree.** It commits
   any unsaved changes in `.main` (idea edits, journal entries, hand-edited
   configs). This is the missing commit verb: `edit idea` leaves files dirty
   by design, and nothing else commits them. `grind save <project>` keeps its
   current meaning (end session, commit `.projects.json`, commit the project
   worktree if dirty).
2. **`grind push` is scoped.** `grind push` pushes only the default branch
   (`.main`'s branch). `grind push <project>` pushes only that project's
   branch. `grind push all` pushes every branch. The default is deliberately
   conservative: state syncs by default, work product syncs on request.
3. **Push refuses to sync uncommitted work.** If the scope's worktree(s) are
   dirty, push fails with a user error naming exactly what to run
   (`grind save` or `grind save <project>`). A failed push is a real error
   (exit 1) — the user asked for it.
4. **Pull fast-forwards only.** The main worktree is updated with
   `git merge --ff-only origin/<default>`; a diverged main is a loud failure,
   not a warning. Project worktrees that cannot fast-forward (dirty or
   diverged) are reported and left alone.
5. **The remote URL lives in `.grind.json` (`remote.url`),** with the git
   remote as fallback. Push/pull resolve the URL (config first, then
   `git remote get-url origin`) and sync the bare repo's `origin` from it —
   the URL travels with the workspace instead of being a per-machine
   artifact. No URL anywhere → user error.

## Workspace layout (unchanged)

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config (remote.url lives here)
│   ├── .projects.json      # ALL project state (single file, versioned)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # daily markdown entries: YYYY-MM-DD.md
│   └── published/          # (empty; future slice)
└── <project>/              # project worktrees (one per project, own branch)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array. Never build a shell string.
2. **The main worktree is clean after every successful command.** `grind
   save` (no args) is the commit verb for `.main`; push/pull refuse to run
   against a dirty `.main` rather than leaving it dirty.
3. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
4. **The git layer is behind an interface** so tests can fake it. This slice
   adds several methods to that interface (below).

## Data model

**No changes to `.projects.json`.** `version` stays 1. The only config read
is `.grind.json`'s `remote.url` (already in the schema).

## Commands in this slice

### `grind save` — commit the main worktree

With no arguments, commits all unsaved changes in `.main`:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. `git.HasChanges(ws.MainWorktree)`. No changes → print `Nothing to save.`
   and exit 0 (a no-op is not an error).
3. `git.CommitAll(ws.MainWorktree, "Save workspace")` — the documented
   "never `git add -A`" exception extended to the explicit save-everything
   verb, same rationale as project worktrees: the user asked to commit
   everything. CommitAll already refuses on unmerged paths.
4. Print `Saved workspace changes.`

`grind save <project>` is UNCHANGED from slice 3: end the active session
(committing `.projects.json`), then commit the project worktree if dirty.
The existing tests must pass unchanged.

### `grind push` — push branches to the remote

Three forms, one verb:

| form | pushes | dirty check |
|---|---|---|
| `grind push` | default branch (`.main`'s branch) | `.main` clean |
| `grind push <project>` | that project's branch | that worktree clean |
| `grind push all` | every branch | `.main` + every project worktree |

Common flow:

1. Require a workspace.
2. Resolve the remote URL (see below). No URL → user error
   `No remote configured. Set remote.url in .grind.json first.`
3. Sync `origin` on the bare repo from the resolved URL.
4. Dirty check for the scope. Any dirty worktree → user error naming the fix:
   - `You have uncommitted changes in .main. Run 'grind save' to commit them.`
   - `You have uncommitted changes in <project>/. Run 'grind save <project>' to commit them.`
   - `push all` with several dirty worktrees lists them all in one error.
   - A project entry whose worktree directory is missing is skipped in the
     dirty check (the branch can still be pushed).
5. Push. `push` and `push <project>` use `git push origin <branch>`;
   `push all` uses `git push origin --all`. A failed push is a user error
   (exit 1) carrying git's stderr: `Could not push to remote: <stderr>`.
6. Print the result:
   - `Pushed main to origin.`
   - `Pushed my-blog to origin.`
   - `Pushed all branches to origin.`

`push <project>` validates the project first (`Project '<name>' does not
exist.`, matching the other verbs). A project literally named `all` is
shadowed by the keyword — `push all` always pushes everything (the same
accepted edge case as `edit idea` shadowing a project named `idea`).

### `grind pull` — fetch, fast-forward, create missing worktrees

1. Require a workspace.
2. Resolve the remote URL and sync `origin` (same as push).
3. **Fail fast on a dirty `.main`**: user error
   `You have uncommitted changes in .main. Run 'grind save' before pulling.`
   (A fast-forward merge would refuse anyway; this gives a clean message
   before any network call.)
4. `git fetch origin` (updates `refs/remotes/origin/*`).
5. For each local branch (default branch + project branches):
   - No remote tracking branch → skip.
   - Local == remote → skip (already up to date).
   - Local is an ancestor of remote (fast-forward possible):
     - Branch checked out in a worktree → `git merge --ff-only
       origin/<branch>` IN the worktree, so the working files refresh too.
       If it fails because the worktree is dirty, report it as **dirty**
       (message: `run 'grind save <project>'`). If it fails for any other
       reason, report it as **diverged**.
     - Branch not checked out → point `refs/heads/<branch>` at
       `refs/remotes/origin/<branch>` in the bare repo.
     - Count as **updated**.
   - Otherwise → report as **diverged** (manual merge needed).
6. Create missing worktrees: for each remote branch that is not the default
   branch and has no worktree at `<root>/<branch>`, `git worktree add`. A
   failure (e.g. a stale directory) counts as **skipped**. Canceled/published
   filtering is NOT in this slice (that is slice 9).
7. Print the summary, omitting empty sections:
   ```
   Fast-forwarded 2 branch(es): main, my-blog
   Created 1 worktree(s): leenix
   1 branch(es) diverged from remote (manual merge needed): other
   1 branch(es) not updated (uncommitted changes): dirty-project
   Skipped 1 project(s): stale-dir
   ```

### Remote URL resolution (shared by push and pull)

1. Read `.grind.json` (`config.Read`). `remote.url` non-empty → use it.
2. Else `git.RemoteURL(bareRepo)` (the existing origin). Non-empty → use it.
3. Else → user error `No remote configured. Set remote.url in .grind.json
   first.`
4. Sync `origin`: if the bare repo has no origin, `git remote add origin
   <url>`; if it has a different URL, `git remote set-url origin <url>`.
   (A missing `.grind.json` falls back to the git remote, matching the
   projects package's tolerance for old workspaces.)

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json (unchanged)
internal/git/               # git interface + os/exec implementation (+methods)
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (unchanged)
internal/ideas/             # idea operations (unchanged)
internal/projects/          # project lifecycle + sessions (unchanged)
internal/tasks/             # task operations (unchanged)
internal/dates/             # due-date parsing (unchanged)
internal/color/             # ANSI colors with TTY detection (unchanged)
internal/journal/           # journal file operations (unchanged)
internal/status/            # dashboard row computation (unchanged)
internal/sync/              # NEW: push/pull orchestration (remote URL, scopes)
```

### `internal/git` (extend)

```go
// DefaultBranch returns the name of the repo's current branch
// (`git symbolic-ref --short HEAD`). The rewrite hardcodes "main" at init,
// but resolving it here avoids v1's bug of hardcoding "main" in pull logic.
DefaultBranch(repoPath string) (string, error)

// SetRemoteURL sets the origin remote URL, adding origin when it does not
// exist and updating it otherwise. push and pull sync origin from
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
```

The existing `PushAll` stays (used by `push all`). The fake git in every
test package gains the new methods.

### `internal/sync` (NEW)

```go
// Package sync implements push and pull: the only two verbs that touch the
// remote. Everything else in grind is local by design.
package sync

// Scope selects what push operates on.
type Scope int

const (
    ScopeMain Scope = iota // grind push — the default branch
    ScopeProject           // grind push <project>
    ScopeAll               // grind push all
)

// Service performs push and pull against a workspace using the given git
// implementation.
type Service struct {
    Git git.Git
}

func NewService(g git.Git) *Service

// Push pushes the branches selected by scope (project names the branch for
// ScopeProject). It resolves the remote URL, syncs origin, refuses to push
// uncommitted work in scope, and maps a failed push to a user error.
func (s *Service) Push(ws *workspace.Workspace, scope Scope, project string) error

// PullResult summarizes what pull did. Empty slices are omitted from the
// printed summary.
type PullResult struct {
    Updated  []string // fast-forwarded branches
    Diverged []string // need a manual merge
    Dirty    []string // worktree had uncommitted changes
    Created  []string // new worktrees
    Skipped  []string // worktree creation failed
}

// Pull fetches from origin, fast-forwards every local branch that can be,
// and creates worktrees for remote project branches that have none.
func (s *Service) Pull(ws *workspace.Workspace) (*PullResult, error)
```

- The remote URL resolution (config → git remote → error, then sync origin)
  is a private helper shared by Push and Pull.
- `Push` reads `.projects.json` via `config.ReadProjects` for the dirty
  checks and project validation (missing file → system error, matching the
  other packages).
- `Pull` uses `s.Git.DefaultBranch` to tell the default branch apart from
  project branches — never a hardcoded `"main"`.

### `internal/cli` (extend)

- `save.go`: `grind save` becomes optional-args. No args → the save-main
  flow above (thin: Require + HasChanges + CommitAll + print). One arg →
  the existing project flow unchanged.
- `push.go`: rewritten to resolve the scope (no arg / project / `all`) and
  call `sync.Service.Push`, printing the result line.
- `pull.go` (NEW): thin command calling `sync.Service.Pull` and printing the
  summary.
- `root.go`: wire `sync.NewService(g)`, add `newPullCmd`, bump the version
  constant to `0.90.5`.
- `AGENTS.md`: update the constitution — the save/push semantics above, the
  command-pattern table rows for `save`/`push`/`pull`, and mark slice 7 done
  in the slice table.

## Testing

- `internal/git` (real git, like the existing tests):
  - `DefaultBranch` returns `main` for a fresh bare repo.
  - `SetRemoteURL` adds origin when missing and updates it when present.
  - `PushBranch` pushes one branch to a local bare remote; a failed push
    returns a `*PushError` carrying stderr.
  - `FetchAll` makes `refs/remotes/origin/*` appear.
  - `IsAncestor` true/false/equal cases.
  - `FastForwardWorktree` fast-forwards a worktree and fails on a dirty one.
  - `FastForwardRef` moves a non-checked-out branch ref.
  - `ListRemoteBranches` lists remote branches excluding HEAD.
- `internal/sync` with a fake git:
  - Push: no remote → user error; URL from `.grind.json` triggers
    `SetRemoteURL`; `push` calls `PushBranch` with the default branch;
    `push <project>` calls it with the project name; unknown project → user
    error; dirty `.main`/worktree → user error naming the save command;
    `push all` calls `PushAll` and lists every dirty worktree; a `PushError`
    becomes a user error.
  - Pull: happy path (fetch, ff, create worktrees); dirty `.main` → error
    before fetch; diverged branch reported and left alone; dirty project
    worktree reported as dirty; remote branch with no local worktree gets
    one created; no remote → user error.
- `internal/cli` with the fake git:
  - `save` (no args): dirty `.main` → `CommitAll` with `Save workspace`,
    prints `Saved workspace changes.`; clean → `Nothing to save.` and no
    commit.
  - `save <project>`: existing tests pass unchanged.
  - `push`: no-arg pushes the default branch; `push <project>`; `push all`;
    dirty errors; no-remote error; failure error. The existing push tests
    are updated for the new semantics (no-arg push now pushes ONE branch).
  - `pull`: happy path prints the summary; dirty `.main` errors.
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind save` commits `.main`; `grind save <project>` still works.
- `grind push` pushes the default branch, `grind push <project>` pushes one
  branch, `grind push all` pushes everything; dirty worktrees in scope fail
  with an actionable message; a failed push exits 1.
- `grind pull` fetches, fast-forwards what it can, creates missing project
  worktrees, and reports diverged/dirty branches without touching them.
- The remote URL resolves from `.grind.json` `remote.url` with the git
  remote as fallback, and `origin` is synced from it.
- AGENTS.md reflects the new save/push/pull semantics and marks slice 7 done.
- `git -C .main status --porcelain` is empty after every successful command
  in this slice.
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.