# Spec 03: Work / Save

**Slice 3 of the Go rewrite of the grind CLI.** The goal of this slice is
time tracking: `work <project>` starts (or continues) a session, and
`save <project>` ends it, commits **both** worktrees (the project work and
the main state), and pushes both branches. Everything else (tasks, journal,
status, push/pull commands, invoices) comes in later slices — do NOT build
it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Workspace layout (unchanged from slice 2)

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config
│   ├── .projects.json      # ALL project state (single file, versioned)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # (empty; future slice)
│   └── published/          # (empty; future slice)
└── <project>/              # project worktrees (one per project, own branch)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array. Never build a shell string.
2. **The main worktree is clean after every command.** Any command that
   mutates state commits immediately. Stage ONLY the specific files changed —
   never `git add -A` (one documented exception, see `CommitAll` below).
3. **Atomic JSON writes** (write temp file, then `os.Rename`).
4. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
5. **The git layer is behind an interface** so tests can fake it.
6. **Project branches contain ONLY the actual work product.** No configs, no
   state. All project state lives in `.projects.json` on the default branch.
   This is what makes `CommitAll` safe (see below).

## Data model

This slice grows each project entry with a `sessions` array:

```json
{
  "version": 1,
  "projects": {
    "my-blog": {
      "name": "my-blog",
      "type": "blog",
      "idea": "# My Blog\n\nSome details",
      "billing": { "roundTo": "quarter-hour", "rate": 150 },
      "createdAt": "2026-09-13T14:30:00Z",
      "sessions": [
        {
          "start": "2026-09-13T14:30:00Z",
          "end": "2026-09-13T16:00:00Z",
          "duration": 5400,
          "rounded": 5400
        }
      ]
    }
  }
}
```

- `start` / `end` are RFC3339 timestamps. `end` is `null` while the session
  is active.
- `duration` and `rounded` are seconds, written when the session ends.
  `rounded` is `duration` rounded UP to the project's `billing.roundTo`
  (quarter-hour / half-hour / hour). Storing them at end time freezes the
  billing math: a later change to `roundTo` must not rewrite history.
- Sessions are per-project. A project can have an active session at the same
  time as another project (double-dipping is supported) — there is no
  global "current session".

## Commands in this slice

### `grind edit <project>`

Opens the editor on the project worktree directory. No session is started —
that is what `work` is for. This is the `edit my-blog` form from the
command table; `edit idea <n>` already exists from slice 1.

Flow:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. Look up the project in `.projects.json` via `projects.Get`. Not found →
   user error `Project '<name>' not found.` (exit 1).
3. `editor.Open(ws.ProjectWorktreePath(name))` — blocking, same editor step
   as `work`. The project worktree is work product, so leaving it dirty is
   fine (that is the point of editing); nothing is committed.

**Cobra wiring:** the `edit` command already has an `idea` subcommand. This
slice adds a `RunE` to the parent `edit` command: cobra runs the parent when
the first argument is not a subcommand, so `edit my-blog` resolves the
project while `edit idea 3` still dispatches to the subcommand. A project
literally named `idea` is shadowed by the subcommand — acceptable edge case.

### `grind work <project>`

Starts a session on the project, or continues the existing one.

Flow:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. Look up the project in `.projects.json`. Not found → user error
   `Project '<name>' does not exist.` (exit 1).
3. If the project already has an active session (`end` is null): print
   ```
   Continuing session on 'my-blog'
   Session started: 2026-09-13T14:30:00Z
   ```
   Do NOT start a second session — v1's orphan bug was starting a new
   session every time, leaking sessions that never ended.
4. Otherwise: append a new session (`start` = now, `end` = null), write
   `.projects.json` atomically, commit ONLY `.projects.json` with message
   `Start session on <name>`. Print:
   ```
   Started work session on 'my-blog'
   Time started: 2026-09-13T14:30:00Z
   ```
5. Open the editor on the project worktree directory
   (`editor.Open(ws.ProjectWorktreePath(name))`), blocking until the editor
   exits — same as v1's `work`, which opens the editor in the project files
   directory. The editor opens in both cases (new session and continuing).
   The session is committed BEFORE the editor launches, so a failed editor
   never loses the session. A non-zero editor exit is a system error
   (exit 2), consistent with `edit` (v1 warned instead; the rewrite keeps
   one error behavior for the editor).

### `grind save <project> [-t|--time <duration>]`

Ends the project's active session, commits both worktrees, and pushes both
branches.

Flow:

1. Require a workspace.
2. Look up the project in `.projects.json`. Not found → user error
   `Project '<name>' does not exist.` (exit 1).
3. Parse `-t` if given. Accepted forms (case-insensitive): `5`, `5h`,
   `1.5h`, `90m`, `1h30m` — a positive duration in hours. Anything else →
   user error
   `Backfill time must be a positive duration (e.g. 5, 5h, 1h30m, 90m). Got '<input>'.`
   (exit 1).
4. If there is an active session:
   - no `-t`: `end` = now.
   - with `-t`: `end` = `start` + duration (even if that lands in the
     future — the user asked for a specific length).
   - compute `duration` and `rounded`, print:
     ```
     Stopped work session on 'my-blog'
     Duration: 1.50 hours (2.00 hours rounded)
     ```
     (hours formatted with two decimals, like v1).
5. If there is NO active session:
   - with `-t`: create a backfilled session `[now - duration, now]`
     (AGENTS.md design decision — v1 only warned here; the rewrite creates
     the session). Compute `duration` and `rounded`, print:
     ```
     Backfilled 8h on 'my-blog'
     Duration: 8.00 hours (8.00 hours rounded)
     ```
   - without `-t`: print `No active session on 'my-blog'.` — no mutation,
     no commits. (The project worktree commit below still runs if dirty.)
6. If a session was ended or backfilled: write `.projects.json` atomically
   and commit ONLY `.projects.json` with message `Save session on <name>`.
7. If the project worktree has uncommitted changes: commit ALL of them with
   `CommitAll` (see git section). Message is `Work session on <name> (<rounded>h)`
   when a session ended or was backfilled, otherwise `Save on <name>`.
8. Push both branches — the project branch and the default branch (from
   `.grind.json` `defaultBranch`, or `main`) — if a remote is configured.
   No remote → skip silently. Push failure → print
   `Warning: could not push to remote: <stderr>` and exit 0 (the work is
   saved locally; the remote is best-effort, matching v1).

**Why commit both worktrees?** v1 only committed the main worktree on save,
so the actual work product never reached the remote — the bug that this rule
exists to prevent. The project worktree holds the work; `.main` holds the
state. Both must be committed and pushed or the remote is useless.

**Why `CommitAll` for the project worktree?** The constitution says "never
`git add -A`". That rule protects the MAIN worktree, where config and state
files live alongside nothing else. A project worktree contains ONLY work
product by construction (its branch starts at an empty tree), so staging
everything there IS "stage the specific files changed". This is a deliberate,
documented exception — approved by the user (see conversation).

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json typed load/save; atomic writes
internal/git/               # git interface + os/exec implementation
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (reused by work for directories)
internal/ideas/             # idea operations (unchanged from slice 1)
internal/projects/          # project lifecycle + sessions (extended)
```

### `internal/config` (extend)

```go
type Session struct {
    Start    time.Time  `json:"start"`
    End      *time.Time `json:"end"`       // nil while the session is active
    Duration int64      `json:"duration"`  // seconds; written when the session ends
    Rounded  int64      `json:"rounded"`   // seconds; duration rounded up per billing.roundTo
}

type ProjectEntry struct {
    Name      string       `json:"name"`
    Type      string       `json:"type,omitempty"`
    Idea      string       `json:"idea"`
    Billing   BillingEntry `json:"billing"`
    CreatedAt time.Time    `json:"createdAt"`
    Sessions  []Session    `json:"sessions,omitempty"`
}
```

### `internal/git` (extend)

```go
type Git interface {
    InitBare(path string) error
    InitialCommit(repoPath, branch string) error
    AddWorktree(repoPath, worktreePath, branch string) error
    Commit(worktreePath, message string, paths ...string) error
    IsPathClean(worktreePath, path string) (bool, error)
    CreateBranch(repoPath, branch string) error
    HasChanges(worktreePath string) (bool, error)   // NEW
    CommitAll(worktreePath, message string) error   // NEW
    RemoteURL(repoPath string) (string, error)      // NEW
    Push(repoPath, branch string) error             // NEW
}
```

- `HasChanges`: `git status --porcelain` output non-empty → true. Untracked
  files count.
- `CommitAll`: `git add -A` then `git commit -m <message>`, run in the
  worktree. Comment MUST explain the documented exception above (project
  worktrees are all work product; the no-`-A` rule protects `.main`). Like
  `Commit`, it refuses to run when the index has unmerged paths.
- `RemoteURL`: `git remote get-url origin` in the bare repo; returns `""`
  when no remote is configured (git exits non-zero — treat as empty, not an
  error).
- `Push`: `git push origin <branch>` run in the bare repo.

### `internal/projects` (extend)

```go
// StartSession starts a session on the project if none is active. Returns
// the session and whether one was newly started (false = continuing).
func (s *Service) StartSession(ws *workspace.Workspace, name string) (*config.Session, bool, error)

// EndSession ends the project's active session. backfill is a duration in
// hours (0 = end now). With no active session and backfill > 0, a session
// [now-backfill, now] is created. Returns the ended session, or nil when
// there was nothing to end and no backfill.
func (s *Service) EndSession(ws *workspace.Workspace, name string, backfill float64) (*config.Session, error)

// Save commits the project worktree (if dirty) and pushes both branches.
// session is the session EndSession just ended (nil when there was none);
// it decides the worktree commit message. Called by the CLI after
// EndSession.
func (s *Service) Save(ws *workspace.Workspace, name string, session *config.Session) error
```

- `ParseDuration(input string) (float64, error)` is EXPORTED (the CLI needs
  it to validate `-t` and produce the user error). `roundTime` stays
  package-private. Both live in `session.go` with table-driven tests. The
  invoice slice may extract them later — do NOT build a shared package now.
- `StartSession` commits `.projects.json` with `Start session on <name>`.
- `EndSession` commits `.projects.json` with `Save session on <name>`.
- `Save` uses `HasChanges` + `CommitAll` on the project worktree, then
  `RemoteURL` + `Push` on both branches (project branch and default branch).
  The worktree commit message is `Work session on <name> (<rounded>h)` when
  `session` is non-nil, otherwise `Save on <name>`.

### `internal/cli` (extend)

- `edit <project>` — the existing `edit` command gains a `RunE` on the
  parent (the `idea` subcommand is unchanged). Resolves the project via
  `projects.Get`, then `editor.Open` on the project worktree directory.
- `work <project>` — new top-level command. Calls `StartSession`, prints the
  continue/start block, then `editor.Open` on the project worktree directory.
- `save <project> [-t|--time <duration>]` — new top-level command. Parses
  `-t` via `projects.ParseDuration` (keeping the raw string for the
  backfill message), calls `EndSession`, prints the stopped/backfilled
  block, then calls `Save`.
- `NewRootCmd` wires the extended `projects.NewService(g)`.

## Testing

- Unit tests for pure logic: `parseDuration` (table-driven: `5`, `5h`,
  `1.5h`, `90m`, `1h30m`, invalid inputs, zero, negative), `roundTime`
  (quarter-hour / half-hour / hour, exact and ceil cases).
- Session logic with a fake git:
  - `StartSession` on a project with no active session appends one and
    commits `.projects.json` with `Start session on <name>`.
  - `StartSession` on a project with an active session does NOT append a
    second session (orphan bug regression test) and does NOT commit.
  - `EndSession` ends at now; with `-t` ends at `start + duration`.
  - `EndSession` with no active session and `-t` creates `[now-t, now]`.
  - `EndSession` with no active session and no `-t` returns nil, no commit.
  - `Save` commits the project worktree only when dirty; pushes both
    branches when a remote exists; skips silently when none.
- git package tests: `HasChanges` (clean vs dirty vs untracked), `CommitAll`
  (stages new, modified, AND deleted files — the reason `git add .` alone is
  not enough), `RemoteURL` (set vs unset), `Push` (pushes the named branch).
- cli tests: `edit <project>` opens the editor on the project worktree
  without starting a session, `work` happy path (start + continue), `save`
  happy path, backfill both cases, invalid duration, unknown project, no
  active session. Editor tests set `$EDITOR` to a no-op (`true`) so the
  editor does not block; a recording script can verify the project worktree
  directory is passed to the editor.
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind edit <project>` opens the editor on the project worktree directory
  without starting a session or committing anything.
- `grind work <project>` starts a session (and commits `.projects.json`);
  running it again continues the session without creating a second one.
- `grind save <project>` ends the session, commits `.projects.json` on main
  AND the work product in the project worktree, and pushes both branches.
- `grind save <project> -t 8h` backfills in BOTH cases: active session ends
  at `start + 8h`; no session creates `[now-8h, now]`.
- `git -C .main status --porcelain` is empty after every command.
- With a remote configured (a second bare repo works for testing), `save`
  pushes both branches; without one, it skips silently.
- Error cases above all exit 1 with the specified messages.
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.