# Spec 02: Projects

**Slice 2 of the Go rewrite of the grind CLI.** The goal of this slice is
promoting ideas into projects: `new project`, `list projects` (+ hidden
`projects` alias), and `show <project>`. Everything else (sessions, tasks,
journal, push/pull, invoices) comes in later slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Workspace layout (unchanged from slice 1)

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
6. **Project branches contain ONLY the actual work product.** No configs, no
   state. All project state lives in `.projects.json` on the default branch.
   This is why the project branch starts from an EMPTY TREE (see below) — if
   it branched off `main`, the worktree would be littered with `.grind.json`,
   `.projects.json`, `ideas/`, etc.

## Data model

`.projects.json` is the single source of truth for all project state. This
slice grows the per-project entry from slice 1's `{ "name": ... }` to:

```json
{
  "version": 1,
  "projects": {
    "my-blog": {
      "name": "my-blog",
      "type": "blog",
      "idea": "# My Blog\n\nSome details",
      "billing": {
        "roundTo": "quarter-hour",
        "rate": 150
      },
      "createdAt": "2026-09-13T14:30:00Z"
    }
  }
}
```

- `type` is omitted when empty (`omitempty`). `-t` is optional at creation.
- `idea` is the FULL markdown content of the idea file that was promoted.
- `billing` is copied from `.grind.json` defaults at creation time. Per-project
  billing rates are a hard requirement (design constitution) — each entry
  carries its own block.
- `createdAt` is an RFC3339 timestamp.

## Commands in this slice

### `grind new project <name> <idea-number> [-t|--type <type>]`

Promotes an idea to a project. The idea number is the 0-based index from
`grind list ideas` (same resolution as `reject idea`).

Flow (order matters — see "why" below):

1. Require a workspace.
2. **Fail fast on a dirty main worktree.** If `git status --porcelain` in
   `.main` is non-empty, fail with user error
   `You have uncommitted changes in .main. Commit or discard them first.`
   (exit 1). This protects against sweeping unrelated edits — e.g. a leftover
   `edit idea` — into the project-creation commits. This was a real v1 bug:
   `reject`/`prune` swept unrelated changes into commits.
3. Resolve the idea number. Non-numeric → user error
   `Idea must be a number, got "<arg>"`. Out of range → user error
   `Idea #<n> not found. Run 'grind list ideas' to see available ideas.`
   (both exit 1, same as slice 1).
4. Validate the project name (see rules below). Any violation → user error
   `Invalid project name "<name>": <reason>` (exit 1).
5. Validate `-t` if given: must be in the effective project types (from
   `.grind.json` `projectTypes`, or the default list
   `blog, webapp, video, song, book, feature, issue`). Violation → user error
   `Invalid type: <type>. Valid types: <list>` (exit 1). If `-t` is omitted,
   the project's type is empty.
6. Read `.grind.json` for billing defaults (`roundTo`, `defaultRate`). If the
   file is missing, use `config.Default()`.
7. Create the project branch at an EMPTY TREE in the bare repo
   (`git.CreateBranch`). Error if the branch already exists.
8. Add the worktree at `<root>/<name>` on branch `<name>`
   (`git.AddWorktree` — branch exists, so plain `worktree add`).
9. Read the idea file content from `ideas/<filename>`.
10. Add the entry to `.projects.json` (name, type, idea content, billing from
    defaults, createdAt = now) and write it atomically.
11. Commit ONLY `.projects.json` with message `Create project: <name>`.
12. Delete the idea file from `ideas/`.
13. Commit ONLY the deleted idea path with message
    `Remove idea <filename> (now project <name>)`.
14. Print:
    ```
    Created project: my-blog
    Branch: my-blog
    Worktree: my-blog/
    Next: cd my-blog
    ```
    then a blank line and the remaining ideas list (same format as
    `list ideas`), prefixed with `Remaining ideas:`.

**Why worktree-first?** The git worktree operations are the risky ones
(branch name validity, directory collisions). Doing them first means a failure
never leaves `.projects.json` or `ideas/` half-mutated — the state files are
the source of truth and must not be corrupted. If a later step fails after the
worktree was created, the worktree is left in place; the error message should
note this so the user can remove it (`git worktree remove <name>`).

**Project name rules** (validate in this order):

- non-empty
- no whitespace (spaces or tabs)
- not reserved: `.main`, `.grind.repo.git`
- valid git branch name (the branch IS the name): no `~ ^ : ? * [ \`, no `..`,
  no `@{`, no leading or trailing `/`, no leading `-`, no control characters,
  not `HEAD`, not ending in `.lock`
- not already present in `.projects.json` → user error
  `Project '<name>' already exists.`
- no existing directory at `<root>/<name>`

### `grind list projects`

1. Require a workspace.
2. Read `.projects.json` (the authoritative source — do NOT enumerate git
   worktrees). If the file is missing, treat as empty.
3. Sort projects by name.
4. Print an aligned table (use stdlib `text/tabwriter`):
   ```
   Project   Type  Created
   my-blog   blog  2026-09-13
   ```
   `Created` is the date part of `createdAt` (YYYY-MM-DD). Empty type renders
   as `—`. Later slices (work/save) add Hours/Sessions/Last Worked columns —
   do NOT add them now.
5. Empty state: `No projects yet. Create one with: grind new project "name" <idea-number>`

### `grind projects` — hidden alias for `grind list projects`

Same trick as the `ideas` alias: `Hidden: true`.

### `grind show <project>`

1. Require a workspace.
2. Look up the project in `.projects.json`. Not found → user error
   `Project '<name>' not found.` (exit 1).
3. Print:
   ```
   Name:    my-blog
   Type:    blog
   Created: 2026-09-13
   Rate:    150/hr (quarter-hour)

   # My Blog
   ...full idea content...
   ```
   Empty type renders as `—`. The `Rate` line prefixes the configured
   currency from `.grind.json` if set (e.g. `$150/hr`), otherwise no prefix.
   The idea content is printed verbatim after a blank line.

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json typed load/save; atomic writes
internal/git/               # git interface + os/exec implementation
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (unchanged from slice 1)
internal/ideas/             # idea operations (unchanged from slice 1)
internal/projects/          # NEW: project lifecycle (create, list, get)
```

### `internal/config` (extend)

- Grow `ProjectEntry` to the full slice-2 shape:
  ```go
  type ProjectEntry struct {
      Name      string       `json:"name"`
      Type      string       `json:"type,omitempty"`
      Idea      string       `json:"idea"`
      Billing   BillingEntry `json:"billing"`
      CreatedAt time.Time    `json:"createdAt"`
  }
  type BillingEntry struct {
      RoundTo string  `json:"roundTo"`
      Rate    float64 `json:"rate"`
  }
  ```
- Add `ReadProjects(path string) (ProjectsConfig, error)` (mirror of
  `Read`). Missing file → system error (callers decide whether to treat as
  empty).
- The domain type lives in `config` (not `projects`) because `projects`
  imports `workspace`, which imports `config` — a `projects`-owned type would
  create an import cycle. Note this in a comment.

### `internal/git` (extend)

```go
type Git interface {
    InitBare(path string) error
    InitialCommit(repoPath, branch string) error
    AddWorktree(repoPath, worktreePath, branch string) error
    Commit(worktreePath, message string, paths ...string) error
    IsClean(worktreePath string) (bool, error)   // NEW
    CreateBranch(repoPath, branch string) error  // NEW
}
```

- `IsClean`: `git status --porcelain` output empty → true. Untracked files
  count as dirty.
- `CreateBranch`: creates a branch pointing at an empty-tree commit (same
  `hash-object`/`commit-tree`/`update-ref` dance as `InitialCommit` — extract
  a shared helper). Commit message `Initialize project branch`. Error if the
  branch already exists (`git show-ref --verify refs/heads/<branch>`).
- `AddWorktree` semantics change: if the branch already exists, use
  `git worktree add <path> <branch>` (no `-b`); otherwise
  `git worktree add -b <branch> <path>`. This matches v1's `gitAddWorktree`
  and is needed because `CreateBranch` runs first.

### `internal/workspace` (extend)

- Add `ProjectWorktreePath(name string) string` → `<root>/<name>`.

### `internal/projects` (NEW)

```go
type Service struct {
    Git git.Git
}

func NewService(g git.Git) *Service

// Create promotes an idea into a project. ideaFilename is the name of the
// idea file inside ideas/ (from ideas.Service.Resolve). The idea content is
// read here, stored in the project entry, and the file is deleted as part of
// the same transaction.
func (s *Service) Create(ws *workspace.Workspace, name, projectType, ideaFilename string) (*config.ProjectEntry, error)

// List returns all projects sorted by name.
func (s *Service) List(ws *workspace.Workspace) ([]config.ProjectEntry, error)

// Get returns one project, or a user error when it does not exist.
func (s *Service) Get(ws *workspace.Workspace, name string) (*config.ProjectEntry, error)
```

- Name validation lives here (the rules above), as a small table-driven
  helper `validateName(name string) error`.
- Type validation lives here too: `validTypes(cfg config.GrindConfig) []string`
  (config `projectTypes` or the default list) and a check that the given type
  is in it.
- `Create` reads `.grind.json` for billing defaults and project types; a
  missing `.grind.json` falls back to `config.Default()`.
- `Create` deletes the idea file and commits both mutations (`.projects.json`
  then the idea deletion) as two commits with the messages above.

### `internal/cli` (extend)

- `new project <name> <idea-number> [-t|--type]` — subcommand of `new`.
  Resolves the idea via `ideas.Service.Resolve`, calls
  `projects.Service.Create`, prints the success block, then lists remaining
  ideas via `ideas.Service.List`.
- `list projects` — subcommand of `list`; `projects` hidden alias.
- `show <project>` — new top-level command.
- `NewRootCmd` wires both services: `ideas.NewService(g)` and
  `projects.NewService(g)`.

## Testing

- Unit tests for pure logic: name validation (table-driven — good names, bad
  names, reserved names, branch-name edge cases), type validation, list
  sorting, `show` rendering.
- The git layer is an interface — write a fake and test command flows:
  - `Create` calls `CreateBranch` then `AddWorktree` (in that order), commits
    `.projects.json` with `Create project: <name>`, then commits the deleted
    idea path with `Remove idea <file> (now project <name>)`.
  - `Create` fails fast when the main worktree is dirty (fake `IsClean` false).
  - `Create` refuses invalid names / existing projects / unknown types.
- git package tests: `IsClean` (clean vs dirty vs untracked), `CreateBranch`
  (branch exists at empty tree; errors when branch already exists),
  `AddWorktree` with an existing branch (no `-b`).
- cli tests: `new project` happy path with fake git, `list projects` output,
  `show` output, error cases (not in workspace, dirty main, bad idea number,
  existing project, unknown type, unknown project in `show`).
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind new project <name> <n> [-t type]` works end-to-end against real git:
  branch created at an empty tree, worktree at `<root>/<name>`, `.projects.json`
  entry written, idea file deleted, two commits on main.
- `git -C .main status --porcelain` is empty after every command.
- `ls <root>/<name>` is empty right after creation (only work product, no
  state files).
- `grind list projects` and the hidden `grind projects` alias work.
- `grind show <name>` works; unknown project errors with exit 1.
- Error cases above all exit 1 with the specified messages.
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.