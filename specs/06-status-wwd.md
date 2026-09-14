# Spec 06: Status (`wwd`)

**Slice 6 of the Go rewrite of the grind CLI.** The goal of this slice is
the status dashboard: `grind wwd` prints a per-project status table, a
divider, and the open-task list. It is the user's MOST-USED command and must
stay hidden from `--help` — the secret handshake (Cobra `Hidden: true`).
Everything else (push/pull, config, publish, invoices, migrate) comes in
later slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Workspace layout (unchanged from slice 5)

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config
│   ├── .projects.json      # ALL project state (single file, versioned)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # daily markdown entries: YYYY-MM-DD.md
│   └── published/          # (empty; future slice)
└── <project>/              # project worktrees (one per project, own branch)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array. Never build a shell string.
2. **`wwd` is read-only.** It never writes `.projects.json`, never commits,
   and never touches a worktree. The main worktree stays clean after `wwd`.
3. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
4. **The git layer is behind an interface** so tests can fake it. This slice
   adds ONE method to that interface (`LastCommitDate`).

## Data model

**No changes.** `.projects.json` is untouched and `version` stays 1. `wwd`
reads the existing state: `ProjectEntry.Sessions` (for worked hours, last
session, active flag) and `ProjectEntry.Tasks` (for task count and urgency).

The v1 status table had a **Billed** column; the rewrite's sessions have no
`invoiced` flag yet, so Billed is deliberately dropped from this slice and
returns with the invoice slice (10). Deadline and long-term coloring are
also dropped — the rewrite's project model has no such fields.

## Commands in this slice

### `grind wwd` — hidden dashboard

Prints the status table, a divider, and the open-task list. `Hidden: true`
— it must not appear in `grind --help`.

Output shape (v1's structure exactly):

```
Project    Worked  Tasks  Last Session  Last Commit
my-blog    3.5h    2      2h ago        1d ago
leenix     0.0h    0      never         never

 ─────────────────────────── The GrindCLI ──────────────────────────────

#    Project  Task          Due
100  my-blog  Write intro   2026-09-20
```

#### Part 1: status table

One row per project, in this order:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. Read `.projects.json`. No projects → print
   `No active projects. Create one with: grind new project "name" <idea-number>`
   and skip the table (the divider and task list still print — v1 behavior).
3. For each project compute:
   - **Worked**: sum of `Rounded` seconds across all sessions, rendered as
     `X.Xh` (one decimal, v1's `toFixed(1)`). Active sessions (End == nil)
     have Rounded 0 and contribute nothing — matches the current model.
   - **Tasks**: count of open tasks (`Done == false`).
   - **TaskUrgency**: highest urgency across the project's open tasks via
     the pure helper below (`overdue` > `today` > `soon` > `none`).
   - **Last Session**: `dates.TimeAgo` of the session with the latest
     `Start`, or `never` when there are no sessions.
   - **Last Commit**: `dates.TimeAgo` of `git.LastCommitDate(bareRepo,
     projectName)`, or `never` when the branch has no commits.
   - **IsActive**: true when any session has `End == nil`.
4. Sort: total worked seconds descending, then name ascending (deterministic).
5. Render an aligned table (stdlib `text/tabwriter`, matching the `list
   tasks` style — left-aligned columns):
   ```
   Project    Worked  Tasks  Last Session  Last Commit
   ```
   - Project name: GREEN when `IsActive`, plain otherwise.
   - Tasks column: RED when urgency is `overdue`, YELLOW when `today`,
     plain otherwise.
   - Colors via `internal/color` (TTY detection — plain when piped).

#### Part 2: divider

A blank line, then the dimmed divider, then a blank line:

```
 ─────────────────────────── The GrindCLI ──────────────────────────────
```

Exactly v1's line: a leading space, 27 `─` (U+2500), ` The GrindCLI `, 30
`─`. Dimmed via the color palette.

#### Part 3: task list

The same rendering as `grind list tasks` with no project argument (all
projects, open only) — including its empty state
`All caught up! No open tasks.` and the due-date coloring from slice 4.
Extract the shared table rendering from `listTasksRunE` into a helper so
`list tasks` and `wwd` use the same code path (no duplication).

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json (unchanged)
internal/git/               # git interface + os/exec implementation (+1 method)
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (unchanged)
internal/ideas/             # idea operations (unchanged)
internal/projects/          # project lifecycle + sessions (unchanged)
internal/tasks/             # task operations (unchanged)
internal/dates/             # due-date parsing + NEW TimeAgo
internal/color/             # ANSI colors with TTY detection (unchanged)
internal/journal/           # journal file operations (unchanged)
internal/status/            # NEW: dashboard row computation (read-only)
```

### `internal/dates` (extend)

```go
// TimeAgo renders t relative to now in v1's format: "just now" under a
// minute, then "5m ago", "3h ago", "2d ago", "4mo ago", "1y ago" (floored
// units). now is injectable so tests can fix the reference time.
func TimeAgo(t, now time.Time) string
```

- Matches v1's `timeAgo` exactly: <60s → `just now`; <60m → `Xm ago`;
  <24h → `Xh ago`; <30d → `Xd ago`; <12mo → `Xmo ago`; else `Xy ago`.
- Future timestamps (negative delta) → `just now` (v1 behavior).

### `internal/git` (extend)

```go
// LastCommitDate returns the time of the branch's most recent commit in
// the bare repo, or the zero time when the branch has no commits.
LastCommitDate(repoPath, branch string) (time.Time, error)
```

- Implementation: `git log <branch> -1 --format=%aI` in repoPath (argv
  array, no shell string). Empty output → zero time, nil error. Parse the
  strict ISO 8601 timestamp with `time.Parse(time.RFC3339, ...)`.
- The fake git in tests gains a matching method.

### `internal/status` (NEW)

```go
// Service computes the wwd dashboard rows. It is read-only: it never
// writes .projects.json or commits.
type Service struct {
    Git git.Git
}

func NewService(g git.Git) *Service

// Row is one project's line in the status table.
type Row struct {
    Name         string
    WorkedHours  string // e.g. "3.5h"
    TaskCount    int
    TaskUrgency  string // "overdue" | "today" | "soon" | "none"
    LastSession  string // TimeAgo of the latest session start, or "never"
    LastCommit   string // TimeAgo of the branch's last commit, or "never"
    IsActive     bool
    TotalSeconds int64  // for sorting
}

// Status returns the dashboard rows, sorted by total worked seconds
// descending, then name ascending.
func (s *Service) Status(ws *workspace.Workspace) ([]Row, error)

// TaskUrgency returns the highest urgency across a project's open tasks:
// "overdue" if any is past due, "today" if any is due today, "soon" if
// any is due within 3 days, else "none". today is the local YYYY-MM-DD.
func TaskUrgency(tasks []config.Task, today string) string
```

- `TaskUrgency` is a pure function with its own table tests. It mirrors
  v1's `getTaskUrgency`: overdue wins outright; otherwise the highest of
  today/soon/none.
- `Status` reads `.projects.json` via `config.ReadProjects` (missing file →
  system error, matching the other packages). It calls
  `s.Git.LastCommitDate(ws.BareRepo, entry.Name)` per project.
- The status package does NOT depend on `internal/tasks` — it reads
  `config.Task` entries directly.

### `internal/cli` (extend)

- `wwd` — hidden top-level command. Runs the status service, renders the
  table with `tabwriter` + `color.New(cmd.OutOrStdout())`, prints the
  divider, then the shared task-list rendering.
- Refactor: extract the task table rendering from `listTasksRunE` into a
  shared helper (e.g. `renderTasksTable(out io.Writer, rows []tasks.TaskRow,
  palette color.Palette)`) used by both `list tasks` and `wwd`.
- `NewRootCmd` wires `status.NewService(g)` and bumps the version constant
  to `0.90.4` (the code comment says to bump per slice).

## Testing

- `internal/dates`: `TimeAgo` table tests with a fixed `now` — `just now`
  (30s), `5m ago`, `59m ago`, `3h ago`, `2d ago`, `4mo ago`, `1y ago`,
  and a future timestamp → `just now`.
- `internal/status` with a fake git:
  - `TaskUrgency` table: no tasks → `none`; overdue wins over today/soon;
    today beats soon; soon within 3 days; no-due tasks → `none`.
  - `Status` computes WorkedHours from Rounded sums (e.g. two sessions of
    30m + 1h → `1.5h`), TaskCount (open only), LastSession (`never` with
    no sessions; `TimeAgo` of the latest start), LastCommit (from the fake
    git; `never` for zero time), IsActive (End == nil).
  - Sorting: more worked time first; equal time → name ascending.
  - No projects → empty rows, no error.
- `internal/cli` with a fake git:
  - `wwd` happy path: a project with sessions and tasks renders the table,
    divider, and task list; plain text in buffers (colors off).
  - `wwd` with no projects prints both empty-state messages.
  - `wwd` is hidden from `--help`.
  - `list tasks` still works after the rendering refactor (existing tests
    must pass unchanged).
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind wwd` prints the status table, the dimmed divider, and the open-task
  list; colors appear on a terminal and stay plain when piped.
- `grind wwd` does not appear in `grind --help`.
- A workspace with no projects prints
  `No active projects. Create one with: grind new project "name" <idea-number>`
  followed by the divider and `All caught up! No open tasks.`
- `git -C .main status --porcelain` is empty after `grind wwd` (read-only).
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.