# Spec 01: Foundation + Ideas

**Slice 1 of the Go rewrite of the grind CLI.** The goal of this slice is a
working `grind` binary that can initialize a workspace and manage ideas
(create, list, edit, reject, prune). Everything else (projects, sessions,
tasks, journal, push/pull, invoices) comes in later slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Workspace layout (the target for ALL future slices)

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config
│   ├── .projects.json      # all project state (empty skeleton for now)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # (empty; future slice)
│   └── published/          # (empty; future slice)
└── <project>/              # project worktrees (future slices)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array (`exec.Command("git", "-C", path, "add", file)`). Never build a
   shell string. This is the single most important rule.
2. **The main worktree is clean after every command.** Any command that
   mutates state commits immediately. Stage ONLY the specific files changed —
   never `git add -A`.
3. **Atomic JSON writes** (write temp file, then `os.Rename`).
4. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
5. **The git layer is behind an interface** so tests can fake it.

## Commands in this slice

### `grind init`

Creates the workspace skeleton in the current directory:

1. `git init --bare .grind.repo.git`
2. Create the initial commit on the default branch (`main`) in the bare repo
   (use `git hash-object -t tree /dev/null` + `git commit-tree` +
   `git update-ref`, since a bare repo has no working tree).
3. `git worktree add .main -b main` (or `git worktree add .main main` if the
   branch already exists).
4. Write `.main/.grind.json` with defaults:
   ```json
   {
     "billing": { "roundTo": "quarter-hour", "defaultRate": 150 }
   }
   ```
5. Write `.main/.projects.json` with `{ "version": 1, "projects": {} }`.
6. Create empty `ideas/`, `journal/`, `published/` dirs (git doesn't track
   empty dirs — add a `.gitkeep` in each, or create them lazily on first use;
   your choice, but the dirs must exist after init).
7. Commit everything in `.main` with message `Initialize grind workspace`.
8. Print a friendly summary.

If `.grind.repo.git` already exists in the current directory, fail with a
user error ("Already a grind workspace").

### `grind new idea [title]`

1. Require a workspace.
2. If `title` given: file content is `# <title>\n`.
3. If no title: open `$EDITOR` (fallback `$VISUAL`, then `vi`) on a temp file
   whose initial content is `\n# First line is the title; add detail below\n`.
   After the editor closes, strip lines starting with `#`, take the first
   remaining line as the title and the rest as the body. If the result is
   empty, print "Aborted." and exit 0 (no file, no commit).
   File content: `# <title>\n` plus `\n<body>\n` if there is a body.
4. Filename: `<YYYYMMDDHHmmss>.md` using LOCAL time (e.g. `20260913143022.md`).
5. Write the file, then commit ONLY that file in `.main` with message
   `Add idea: <title>`.
6. Print `Created idea: ideas/<filename>`.

### `grind list ideas [-a|--all] [-r|--rejected]`

1. Require a workspace.
2. Read `ideas/`, sort by filename (timestamp filenames sort chronologically).
3. Filtering:
   - `-r`: only files starting with `rejected-`.
   - default: exclude files starting with `rejected-`.
   - `-a`: no filtering.
4. Number each idea 0-based, in sorted order. For each, read the file, extract
   the title from the first line starting with `#` (strip leading `#`s and
   whitespace). Rejected ideas get a `[REJECTED] ` prefix.
   Output format: `<n>. [REJECTED] <title>` — e.g. `0. My idea` or
   `1. [REJECTED] Bad idea`.
5. Empty states:
   - default/all with no ideas: `No ideas yet. Create one with: grind new idea "Your idea"`
   - `-r` with none: `No rejected ideas.`

### `grind ideas` — alias for `grind list ideas`

### `grind edit idea <number>`

1. Require a workspace.
2. Resolve `<number>` (0-based index into the sorted non-rejected list).
   Invalid number → user error `Idea #<n> not found. Run 'grind list ideas' to see available ideas.`
3. Open the file in `$EDITOR` (argv, no shell). Wait for the editor to close.
   Non-zero exit → system error.
4. No commit (editing an idea is a working-tree change; the user commits when
   they save the project). Hmm — but invariant #2 says main worktree is clean
   after every command. Editing an idea leaves it dirty by design. This is the
   ONE exception: `edit` leaves the file dirty so the user can keep editing.
   Document this exception in a comment.

### `grind reject idea <number>`

1. Require a workspace.
2. Resolve `<number>` (0-based, non-rejected list). Same not-found error as
   `edit idea`.
3. If already rejected → user error `Idea #<n> is already rejected.`
4. Rename file to `rejected-<original-filename>`.
5. Print `Rejected idea #<n>: <title>` and `Renamed: <old> → <new>`.
6. Commit ONLY the old and new paths with message `Reject idea: <title>`.

### `grind prune ideas [-y|--yes]`

1. Require a workspace.
2. Collect all `rejected-*` files. If none: `No rejected ideas to prune.` and exit.
3. Print `Found <n> rejected idea(s) to prune:` and each filename.
4. Unless `-y`, prompt `Delete <n> rejected idea(s)? [y/N]` on stdin. Anything
   other than `y`/`Y` aborts (exit 0, no changes).
5. Delete each file, printing `  - Deleted: <file>`.
6. Commit ONLY the deleted paths with message `Prune <n> rejected idea(s)`.
7. Print `Pruned <n> rejected idea(s) and committed to main branch`.

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery (walk up for .grind.repo.git), paths, Workspace struct
internal/config/            # .grind.json typed load/save/validate; atomic writes
internal/git/               # git interface + os/exec implementation
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (argv, no shell)
internal/ideas/             # idea operations (create, list, resolve, reject, prune)
```

### `internal/workspace`

- `Find(startDir string) (*Workspace, error)` — walk up from startDir looking
  for `.grind.repo.git`; also check the parent of each dir (worktrees are
  siblings of the bare repo). Returns `nil` if not found.
- `Require(startDir string) (*Workspace, error)` — like Find but returns a
  user error `Not in a grind workspace.` if not found.
- `Workspace` struct: `{ Root, BareRepo, MainWorktree string }` plus path
  helpers (`IdeasDir()`, `GrindConfigPath()`, `ProjectsConfigPath()`, etc.).
  All path construction lives here — never hardcode `grind` or `.main`
  elsewhere.

### `internal/git`

Define an interface with the operations this slice needs:

```go
type Git interface {
    InitBare(path string) error
    InitialCommit(repoPath, branch string) error
    AddWorktree(repoPath, worktreePath, branch string) error
    Commit(worktreePath, message string, paths ...string) error
    // ... anything else you need
}
```

Implement it with `os/exec`. The implementation should be a struct that can be
swapped for a fake in tests. `Commit` must stage only the given paths (never
`add -A`), and refuse to commit if there are unmerged paths
(`git ls-files -u` non-empty) — this protects the config-on-main design.

### `internal/grinderr`

- `User` error type (exit 1) — bad input, missing data, user aborts.
- `System` error type (exit 2) — I/O failures, git failures, editor failures.
- `ExitCode(err error) int` — maps any error to an exit code (99 for anything
  not a grinderr).

### `internal/config`

- `GrindConfig` struct matching the v1 schema:
  ```go
  type GrindConfig struct {
      Billing      BillingConfig `json:"billing"`
      DefaultBranch string       `json:"defaultBranch,omitempty"`
      ProjectTypes []string      `json:"projectTypes,omitempty"`
      // my.*, currency, paymentTerms, remote.url — include the fields, they're
      // part of the schema even if unused this slice
  }
  ```
- `Read(path)`, `Write(path, cfg)` with atomic write (temp + rename).
- `Default()` returns the default config.

### `internal/editor`

- `Open(path string) error` — resolve `$EDITOR` → `$VISUAL` → `vi`, run with
  argv, inherit stdio, wait. Non-zero exit → system error.
- `EditTemp(prefix string, initial string) (string, error)` — write temp file,
  open editor, read result, clean up temp file. Used by `new idea` when no
  title is given.

### `internal/ideas`

- `Create(ws, title string) (filename string, err error)` — with editor flow
  when title is empty.
- `List(ws, opts) ([]Idea, error)` — `Idea{ Number, Filename, Title, Rejected }`.
- `Resolve(ws, number int) (Idea, error)` — 0-based, non-rejected.
- `Reject(ws, number int) error`
- `Prune(ws, yes bool) error`

### `internal/cli`

Cobra wiring. Root command `grind` with description
`CLI tool for managing creative/technical projects from idea to publication`.
Commands: `init`, `new idea`, `list ideas` (+ hidden alias `ideas`), `edit idea`,
`reject idea`, `prune ideas`. Commands are thin — they call into the packages
above and print results.

## Testing

- Unit tests for pure logic: idea title extraction, filtering, number
  resolution, config defaults, error exit codes.
- The git layer is an interface — write a fake and test command flows
  (new idea commits the right file, reject stages old+new paths, prune stages
  deleted paths, etc.).
- Table-driven tests where it fits.
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind init` creates a working workspace; `grind new idea`, `list ideas`,
  `ideas`, `edit idea`, `reject idea`, `prune ideas` all work end-to-end.
- After every command except `edit idea`, `git -C .main status --porcelain` is
  empty.
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.