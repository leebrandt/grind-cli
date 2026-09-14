# Spec 05: Journal

**Slice 5 of the Go rewrite of the grind CLI.** The goal of this slice is
the daily journal: `edit journal` opens today's entry in the editor, and
`read journal` prints the entries to stdout. Journal entries are plain
markdown files in `.main/journal/` — work product, not state. They never
touch `.projects.json` and no journal command commits anything. Everything
else (status/wwd, pull, publish, invoices) comes in later slices — do NOT
build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Workspace layout (unchanged from slice 4)

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
   argv array. Never build a shell string. (No journal command needs git at
   all — this is a reminder, not a new requirement.)
2. **Editing leaves files dirty by design.** `edit journal` is the same
   exception as `edit idea`: the user is mid-edit, nothing is committed, and
   the entry shows up as untracked in `.main` until the next `grind save`.
3. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
4. **Journal entries are work product, not state.** They are markdown files
   in `.main/journal/`, one per local day, named `YYYY-MM-DD.md`. No journal
   command reads or writes `.projects.json`.

## Data model

**No changes.** `.projects.json` is untouched and `version` stays 1. The
journal is a directory of files:

```
.main/journal/
├── 2026-09-13.md
└── 2026-09-14.md
```

- Filenames are LOCAL dates (`YYYY-MM-DD.md`), so a lexicographic sort is
  chronological. The local-date rule matches the tasks slice: v1's UTC
  "today" bug does not come back.
- Entries are free-form markdown. The editor creates the file when the user
  saves; grind does not pre-create it or write a template.

## Commands in this slice

### `grind edit journal`

Opens today's entry in `$EDITOR` (via the existing `internal/editor`).

Flow:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. Compute today's filename from the LOCAL date (e.g. `2026-09-13.md`).
3. Create the journal directory if it does not exist (`os.MkdirAll`).
4. Open `<journal-dir>/<today>.md` in the editor. The editor creates the
   file when the user saves — grind does not create it first.
5. Print nothing. Leave the file dirty (untracked in `.main`) — same
   exception as `edit idea`.

### `grind journal` — hidden alias

Same trick as the `ideas`/`projects`/`tasks` aliases: `Hidden: true`, same
body as `edit journal`. The user asked for this explicitly — v1's `grind
journal` muscle memory. It must stay hidden from `--help`.

### `grind read journal [-r|--reverse]`

Prints all journal entries to stdout, oldest first.

Flow:

1. Require a workspace.
2. List entry filenames (sorted; a missing journal directory is NOT an
   error — it lists nothing).
3. `-r` reverses the order (newest first).
4. Empty list → print nothing, exit 0 (v1 behavior).
5. Otherwise print each entry as a block, oldest first, joined by blank
   lines:

   ```
   ─── Sunday, September 13, 2026 ───

   <raw markdown content>
   ```

   The header date is the filename without `.md`, rendered long-form via
   `journal.FormatLongDate` (see below). The em-dash header and blank-line
   separation match v1 exactly.

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json (unchanged)
internal/git/               # git interface + os/exec implementation (unchanged)
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (unchanged)
internal/ideas/             # idea operations (unchanged)
internal/projects/          # project lifecycle + sessions (unchanged)
internal/tasks/             # task operations (unchanged)
internal/dates/             # due-date parsing (unchanged)
internal/color/             # ANSI colors with TTY detection (unchanged)
internal/journal/           # NEW: journal file operations (no git)
```

### `internal/journal` (NEW)

Plain package-level functions — no `Service` struct. Journal is stateless
file I/O with no dependencies beyond `os`, `path/filepath`, `time`, and
`workspace`; a struct would add ceremony without carrying any state. That is
the idiomatic shape for this package, and worth a comment.

```go
// TodayFilename returns the journal filename for a local date, e.g.
// "2026-09-13.md". now is injectable so tests can fix the reference date.
func TodayFilename(now time.Time) string

// OpenToday ensures the journal directory exists and returns the path to
// today's entry. The file itself is created by the editor on save.
func OpenToday(ws *workspace.Workspace) (string, error)

// List returns journal entry filenames in chronological order (oldest
// first). A missing journal directory is not an error: it returns an empty
// slice.
func List(ws *workspace.Workspace) ([]string, error)

// Read returns a journal entry's raw markdown content, unmodified.
func Read(ws *workspace.Workspace, filename string) (string, error)

// FormatLongDate renders a YYYY-MM-DD date as a long English date, e.g.
// "2026-09-13" -> "Sunday, September 13, 2026". Unparseable input is
// returned unchanged.
func FormatLongDate(date string) string
```

- `TodayFilename` uses the local timezone: `now.Format("2006-01-02") + ".md"`.
- `FormatLongDate` parses with `time.Parse("2006-01-02", date)` and formats
  with the layout `"Monday, January 2, 2006"`. On parse failure, return the
  input unchanged (defensive; the filenames always parse).
- `List` sorts with `sort.Strings` — lexicographic order is chronological
  for `YYYY-MM-DD` filenames.

### `internal/cli` (extend)

- `edit journal` — subcommand of the existing `edit` group (`cobra.NoArgs`).
  Calls `journal.OpenToday(ws)` then `editor.Open(path)`. Prints nothing.
- `grind journal` — hidden alias with the same body.
- `read journal [-r|--reverse]` — new top-level `read` command group with a
  `journal` subcommand, mirroring the `done` group pattern (bare `grind
  read` shows help). Calls `journal.List` and `journal.Read`, joins the
  blocks with `\n\n`, prints with `fmt.Println` (trailing newline, matching
  v1's `console.log`).
- `NewRootCmd` wires the new commands and bumps the version constant to
  `0.90.3` (the code comment says to bump per slice).

## Testing

- `internal/journal`:
  - `TodayFilename` with a fixed `now` → `"2026-09-13.md"`.
  - `OpenToday` creates the journal directory and returns the path; calling
    it twice is safe (idempotent).
  - `List` returns sorted filenames; a missing journal directory returns an
    empty slice, not an error.
  - `Read` returns the raw content of an entry.
  - `FormatLongDate` table test: `"2026-09-13"` → `"Sunday, September 13,
    2026"`, plus an unparseable input returned unchanged.
- `internal/cli` with a fake git (the existing `runInWorkspace`/`execute`
  helpers):
  - `edit journal` calls the editor with the journal path — reuse the
    existing fake-editor script pattern (`t.Setenv("EDITOR", script)` that
    logs its argument to a file) and assert the arg ends with
    `journal/2026-09-13.md` (compute the expected date from `time.Now()`
    local, not a hardcoded string).
  - The hidden `journal` alias does the same.
  - `read journal` with two entries prints them oldest first with the
    `─── <Long Date> ───` headers; `-r` reverses.
  - `read journal` with no entries prints nothing and exits 0; a missing
    journal directory behaves the same.
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind edit journal` opens today's entry in `$EDITOR`; after the editor
  saves, `journal/YYYY-MM-DD.md` exists in `.main` and shows as untracked in
  `git -C .main status --porcelain` (dirty by design, like `edit idea`).
- `grind journal` does the same and does not appear in `grind --help`.
- `grind read journal` prints entries oldest first with the em-dash headers;
  `grind read journal -r` prints newest first.
- An empty journal: `grind read journal` prints nothing and exits 0.
- No shell-string git commands anywhere in the codebase.
- All tests pass, `go vet` clean.