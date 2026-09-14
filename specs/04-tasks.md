# Spec 04: Tasks

**Slice 4 of the Go rewrite of the grind CLI.** The goal of this slice is
tasks: `new task` creates one, `list tasks` enumerates them, and
`done task <id>` completes one. Task IDs are GLOBAL — a single counter in
`.projects.json` hands out monotonic IDs across all projects, so
`done task 3` needs no project name. Everything else (journal, status/wwd,
pull, invoices) comes in later slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Workspace layout (unchanged from slice 3)

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
   never `git add -A`.
3. **Atomic JSON writes** (write temp file, then `os.Rename`).
4. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
5. **The git layer is behind an interface** so tests can fake it.
6. **Tasks are state, not work product.** They live in `.projects.json` on
   the default branch, never in the project worktree. Task commands commit
   ONLY `.projects.json`; the project worktree is never touched.

## Data model

This slice grows `.projects.json` with a top-level `nextTaskId` counter and
a `tasks` array on each project entry:

```json
{
  "version": 1,
  "nextTaskId": 101,
  "projects": {
    "my-blog": {
      "name": "my-blog",
      "type": "blog",
      "idea": "# My Blog\n\nSome details",
      "billing": { "roundTo": "quarter-hour", "rate": 150 },
      "createdAt": "2026-09-13T14:30:00Z",
      "sessions": [],
      "tasks": [
        {
          "id": 100,
          "description": "Write intro",
          "done": false,
          "createdAt": "2026-09-13T14:30:00Z",
          "completedAt": "2026-09-14T10:00:00Z",
          "dueDate": "2026-09-20"
        }
      ]
    }
  }
}
```

- `nextTaskId` is the next ID to hand out. It starts at **100** (agreed with
  the user — the first task is #100, not #1) and only ever increments: IDs
  are monotonic, never reused, and unique across ALL projects. A missing
  field (a workspace created before this slice) is treated as 100. The
  change is additive, so `version` stays 1.
- `id` is the global task ID from the counter.
- `description` is free-form text.
- `done` is false while the task is open; `completedAt` (RFC3339, UTC,
  truncated to seconds) is set when it flips to true.
- `dueDate` is an optional **local** `YYYY-MM-DD` date. Local, not UTC —
  this deliberately fixes v1's known bug where "due today" was computed
  against UTC and mislabeled tasks in negative-offset timezones.

## Commands in this slice

### `grind new task <project> <description> [-d|--due <date>]`

Creates a task on the project.

Flow:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. Look up the project in `.projects.json`. Not found → user error
   `Project '<name>' does not exist.` (exit 1).
3. Validate the description: non-empty after trimming → user error
   `Task description must not be empty.` (exit 1). (v1 accepted empty
   descriptions; the rewrite rejects them.)
4. Parse `-d` if given via `dates.ParseDate` (see below). Invalid → user
   error `Unparseable date: "<input>"` (exit 1).
5. Assign `id` = `nextTaskId` (100 when the field is missing), then bump the
   counter by 1.
6. Append the task to the project's `tasks` array, write `.projects.json`
   atomically, and commit ONLY `.projects.json` with message
   `Add task: <description>`.
7. Print:
   ```
   Task 100 added: Write intro
   ```

The project worktree is NOT touched — tasks are state, and state lives on
the default branch.

### `grind list tasks [project] [-a|--all]`

Lists open tasks (all projects, or one project). `-a` includes completed
tasks.

Flow:

1. Require a workspace.
2. With a project argument: look it up. Not found → user error
   `Project '<name>' not found.` (exit 1). List only that project's tasks.
   Without: list every project's tasks, each row tagged with its project.
3. Filter: open tasks by default; `-a` includes completed.
4. Sort by due date ascending (soonest first); tasks without a due date go
   last; equal due dates tiebreak by ID ascending (deterministic output).
5. Print an aligned table (stdlib `text/tabwriter`):
   ```
   #     Project   Task          Due
   100   my-blog   Write intro   2026-09-20
   ```
   The all-projects view has the `Project` column; the single-project view
   omits it. A missing due date renders as `—`.
6. Color the `Due` column (via `internal/color`, see below):
   - RED: overdue (due date before today) or due today
   - YELLOW: due within 3 days
   - GREEN: due in 4+ days
   - plain: no due date
   Completed rows (with `-a`) render dimmed. "Today" is the LOCAL date —
   the v1 UTC bug does not come back.
7. Empty states:
   - all projects, no `-a`: `All caught up! No open tasks.`
   - all projects, `-a`: `No tasks yet.`
   - single project, no `-a`:
     `No open tasks. Add one with: grind new task <project> "description"`
   - single project, `-a`: `No tasks yet.`

### `grind tasks [project] [-a|--all]` — hidden alias

Same trick as the `ideas`/`projects` aliases: `Hidden: true`, same flags and
body as `list tasks`.

### `grind done task <id>`

Completes the task with the given GLOBAL id. No project name needed — the id
is unique across all projects.

Flow:

1. Require a workspace.
2. Parse the id. Non-numeric → user error
   `Task ID must be a number, got "<arg>"` (exit 1).
3. Search every project's tasks for the id. Not found → user error
   `Task #<id> not found.` (exit 1).
4. If the task is already done: print `Task <id> is already completed.` and
   stop — no write, no commit. (v1 re-completed idempotently, producing a
   pointless commit; the rewrite skips it.)
5. Otherwise: set `done` = true and `completedAt` = now (UTC, truncated to
   seconds), write `.projects.json` atomically, and commit ONLY
   `.projects.json` with message `Complete task #<id>`.
6. Print:
   ```
   Task 100 completed.
   ```

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json typed load/save; atomic writes
internal/git/               # git interface + os/exec implementation
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (unchanged)
internal/ideas/             # idea operations (unchanged)
internal/projects/          # project lifecycle + sessions (unchanged)
internal/tasks/             # NEW: task operations (add, list, complete)
internal/dates/             # NEW: due-date parsing (pure)
internal/color/             # NEW: ANSI colors with TTY detection
```

### `internal/config` (extend)

```go
type ProjectsConfig struct {
    Version    int                     `json:"version"`
    NextTaskID int                     `json:"nextTaskId"`  // NEW
    Projects   map[string]ProjectEntry `json:"projects"`
}

type Task struct {
    ID          int        `json:"id"`
    Description string     `json:"description"`
    Done        bool       `json:"done"`
    CreatedAt   time.Time  `json:"createdAt"`
    CompletedAt *time.Time `json:"completedAt,omitempty"` // set when done
    DueDate     string     `json:"dueDate,omitempty"`     // local YYYY-MM-DD
}

type ProjectEntry struct {
    Name      string       `json:"name"`
    Type      string       `json:"type,omitempty"`
    Idea      string       `json:"idea"`
    Billing   BillingEntry `json:"billing"`
    CreatedAt time.Time    `json:"createdAt"`
    Sessions  []Session    `json:"sessions,omitempty"`
    Tasks     []Task       `json:"tasks,omitempty"`  // NEW
}
```

- `DefaultProjects()` sets `NextTaskID: 100`.
- The `Task` type lives in `config` for the same reason `ProjectEntry` does:
  `tasks` imports `workspace`, which imports `config` — a tasks-owned type
  would create an import cycle. Note this in a comment.

### `internal/dates` (NEW)

```go
// ParseDate parses a due-date input into a local YYYY-MM-DD string. now is
// injectable so tests can fix the reference date.
func ParseDate(input string, now time.Time) (string, error)
```

Accepted forms (case-insensitive, leading/trailing whitespace trimmed):

| input | meaning |
|---|---|
| `today` | today (local) |
| `tomorrow` | tomorrow (local) |
| `3d` / `3days` | 3 days from now |
| `1w` / `1week` | 1 week from now |
| `0720` | July 20 of the current year (MMDD) |
| `072026` | July 20, 2026 (MMDDYY) |
| `20260720` | July 20, 2026 (YYYYMMDD) |
| `2026-07-20` | July 20, 2026 (ISO) |

Relative forms are computed from `now` in LOCAL time (the result is a local
date, not UTC). Absolute forms are validated against the real calendar
(month 1–12, day valid for the month and year, leap years included). Any
failure — unparseable format or impossible date — is a user error
`Unparseable date: "<input>"` (exit 1).

This is a pure function with no I/O and no dependencies beyond `time`; the
journal slice will reuse it.

### `internal/color` (NEW)

```go
// Palette wraps strings in ANSI color codes. When the writer is not a
// terminal, the methods return the string unchanged so piped output stays
// clean and tests see plain text.
type Palette struct {
    enabled bool
}

// New returns a Palette that colors output written to w. Colors are only
// emitted when w is a terminal: an *os.File whose mode has ModeCharDevice
// set. Buffers and pipes get plain text.
func New(w io.Writer) Palette

func (p Palette) Red(s string) string
func (p Palette) Yellow(s string) string
func (p Palette) Green(s string) string
func (p Palette) Dim(s string) string
```

- ANSI codes: red `\x1b[31m`, yellow `\x1b[33m`, green `\x1b[32m`, dim
  `\x1b[2m`, reset `\x1b[0m`.
- The status/wwd slice will reuse this package — do NOT build anything
  beyond the four methods now.

### `internal/tasks` (NEW)

```go
type Service struct {
    Git git.Git
}

func NewService(g git.Git) *Service

// AddTask appends a task to the project's task list, assigning the next
// global ID from the nextTaskId counter (100 when the field is missing).
// dueDate is already normalized to YYYY-MM-DD by the CLI. Commits
// .projects.json with "Add task: <description>".
func (s *Service) AddTask(ws *workspace.Workspace, projectName, description, dueDate string) (*config.Task, error)

// TaskRow is one row of the task list, tagged with its project for the
// all-projects view.
type TaskRow struct {
    ID          int
    Project     string
    Description string
    DueDate     string // "" when unset
    Done        bool
}

// List returns tasks across all projects (projectName == "") or for one
// project, sorted by due date ascending (no-due last, ID tiebreak).
// openOnly filters to tasks with Done == false.
func (s *Service) List(ws *workspace.Workspace, projectName string, openOnly bool) ([]TaskRow, error)

// Complete marks the task with the given global ID done. Returns the task
// and whether it was already done (true = no write, no commit).
func (s *Service) Complete(ws *workspace.Workspace, id int) (*config.Task, bool, error)
```

- `AddTask` validates the project exists (`Project '<name>' does not exist.`)
  and the description is non-empty (`Task description must not be empty.`).
- `Complete` searches every project's tasks for the ID; not found → user
  error `Task #<id> not found.`
- All three commit ONLY `.projects.json` (via the existing `git.Commit`) —
  no new git interface methods, and the project worktree is never touched.

### `internal/cli` (extend)

- `new task <project> <description> [-d|--due <date>]` — subcommand of
  `new`. Parses `-d` via `dates.ParseDate` (keeping the raw input for the
  error message), calls `tasks.AddTask`, prints
  `Task <id> added: <description>`.
- `list tasks [project] [-a|--all]` — subcommand of `list`; `tasks` hidden
  alias with the same flags and body. Renders the table with `tabwriter`
  and a `color.New(cmd.OutOrStdout())` palette. The due-color mapping
  (overdue/today → red, ≤3 days → yellow, else green) is a small pure
  helper in this package with its own table-driven tests.
- `done task <id>` — new top-level `done` command group with a `task`
  subcommand, mirroring `reject idea` (bare `grind done` shows help). Calls
  `tasks.Complete`, prints `Task <id> completed.` or
  `Task <id> is already completed.`
- `NewRootCmd` wires `tasks.NewService(g)` and bumps the version constant to
  `0.90.2` (the code comment says to bump per slice).

## Testing

- `internal/dates`: table-driven `ParseDate` with a fixed `now` (e.g.
  2026-07-15T12:00:00 local) — today, tomorrow, `3d`/`3days`, `1w`/`1week`,
  `2w`, `0720`, `1225`, `0101`, `072026`, `122525`, `20260720`,
  `2026-07-20`, case-insensitivity (`Tomorrow`, `3DAYS`), whitespace
  (`" tomorrow "`), and errors (`banana`, `1340`, `0230`, `2026-02-30`,
  empty string).
- `internal/tasks` with a fake git:
  - `AddTask` assigns 100 first, then 101 (counter increments); a missing
    counter starts at 100; commits `.projects.json` with
    `Add task: <description>`.
  - `AddTask` errors: unknown project, empty description.
  - `List` all vs single project, `openOnly` filtering, sorting (due asc,
    no-due last, ID tiebreak).
  - `Complete` sets done + completedAt and commits `Complete task #<id>`;
    unknown ID errors; already-done returns without committing.
- `internal/color`: `New` on a buffer → plain text (no escape codes);
  white-box tests construct `Palette{enabled: true}` and verify each method
  wraps and resets.
- `internal/cli`: `new task` happy path + errors (unknown project, empty
  description, bad date), `list tasks` (all/single, `-a`, empty states,
  plain text in buffers), `done task` happy path + already-done + errors
  (non-numeric, not found), hidden `tasks` alias. All with a fake git.
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind new task my-blog "Write intro" -d tomorrow` works end-to-end
  against real git: the task gets ID 100, `.projects.json` is committed,
  and `git -C .main status --porcelain` is empty.
- `grind list tasks`, `grind list tasks my-blog`, and the hidden
  `grind tasks` alias all work; colors appear on a terminal and stay plain
  when piped.
- `grind done task 100` completes the task and commits; `grind done task 999`
  exits 1 with `Task #999 not found.`; `grind done task abc` exits 1 with
  `Task ID must be a number, got "abc"`.
- `grind new task` on an unknown project exits 1; an empty description exits
  1; a bad `-d` exits 1 with the input echoed.
- `git -C .main status --porcelain` is empty after every command.
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.