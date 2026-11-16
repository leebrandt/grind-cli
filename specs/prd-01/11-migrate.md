# Spec 11: Migrate

**Slice 11 of the Go rewrite of the grind CLI — the last one.** This slice
delivers `grind migrate`: convert a v1 workspace into the v2 layout in place,
keeping the git history, the ideas, the journal, the sessions, the tasks and
the invoices.

This is the slice that lets the user stop running v1. Until it lands, every
workspace they own is a v1 workspace, and v2 can only be tried against
throwaway ones.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

1. **In place, one command, one commit.** `grind migrate` converts the
   workspace it is run in. It does not copy, and it does not produce a
   parallel v2 workspace — a user with one workspace should not have to
   reason about which one is live. The bare repo is reused untouched: the
   history is the history, and rewriting it would invalidate every clone.
2. **`git worktree move` with ABSOLUTE paths — this is a trap, and it is
   already confirmed empirically.** `git -C <bare-repo> worktree move grind
   .main` resolves the *destination* relative to the repository directory,
   not the shell's working directory, and lands the worktree at
   `.grind.repo.git/.main` — inside the bare repo, where it silently breaks
   every path in the workspace (verified against a real v1 workspace while
   writing this spec). Both arguments must be absolute. A regression test
   covers exactly this.
3. **Everything is planned before anything is written.** Reading v1's
   configs, deriving `createdAt` from git history, renumbering tasks and
   collecting warnings all happen in a pure plan step. Only the plan's
   application touches the disk. That makes `--dry-run` free, makes the
   mapping table testable without a filesystem, and means the overwhelming
   majority of failures abort before the first byte changes.
4. **The migration is resumable, because one step genuinely is not
   atomic.** `git worktree move` cannot be rolled back by writing a file.
   The spec therefore defines the v1 main worktree to be "`grind/` if it
   exists, otherwise `.main/`" — after the move, v1's `projects/*/.project.json`
   files are sitting at `.main/projects/*/.project.json`, so a re-run finds
   them and finishes the job. An interrupted migration is a second
   invocation, not a manual repair.
5. **`.project.json` files are deleted, not carried over.** All their
   content lives in `.projects.json` afterwards. The sibling `the-idea.md`
   is deleted too: its text is already in the `idea` field (v1 stored the
   whole trimmed idea file there), and keeping a second copy under
   `.main/projects/<name>/` would put it in the directory slice 9 reserved
   for *merged work product*, where a later `publish` would `git rm -r` it.
6. **`idea` is reduced to its title, with `ideas.ExtractTitle`.** v1 stored
   `"# Second idea about video"` — the whole idea file, markdown heading
   included. v2's own `new project` stores `"Second idea about video"`,
   extracted from the `# ` line. Migrated projects must be indistinguishable
   from native ones, so the same extraction runs here. If the v1 idea had a
   body below the title, only the title survives — that is exactly what a
   native `new project` does today, so it is consistent, but it is a
   *loss*, and the plan reports it as a warning naming the project.
7. **Task IDs are renumbered into a single global block starting at 100.**
   v1 allocated task IDs per project from 1. v2's `done task <id>` searches
   every project for the ID (`internal/tasks/tasks.go`), so v1 IDs collide
   the moment a workspace has two projects, and `done task 1` becomes
   ambiguous. Renumbering is mandatory; the order is deterministic —
   projects sorted by name, tasks in stored order — so the same workspace
   always migrates to the same IDs. The block starts at 100 because v2's
   convention is that the first task is #100, and `nextTaskId` lands one
   past the highest issued ID.
8. **`currency` is dropped, and a non-USD value is a loud warning, not a
   silent drop.** The constitution says grind bills in USD and only USD, and
   that a v1 workspace carrying `"currency"` is harmless because the unknown
   key is dropped on read. Dropping it silently is right for *reading*; for
   *migrating* it is not, because a workspace that claimed AUD is about to
   start printing `$`. The amounts are not converted, so the warning says so
   plainly.
9. **`publications` is dropped with a warning per publication.** v1's
   `ProjectConfig` has `publications?: { url, publishedAt }[]`; v2's
   `ProjectEntry` has no field for it and no code reads it. Dropping is the
   honest option for this slice, but the URLs are printed in the plan so
   they can be recovered by hand. Adding the field is listed under
   "Extension points".
10. **Invoices move to `.main/invoices/<project>/<id>/`, keeping v1's
    directory names.** v1 wrote `grind/projects/<project>/invoices/<stamp>/`
    with `invoice.md` and `invoice.pdf`. v2 writes
    `.main/invoices/<project>/<stamp>/invoice.md`. The move follows the
    layout change from slice 10 and nothing else: the stamp is left exactly
    as v1 named it (`2026-10-25T21-12-46`) rather than being renamed to v2's
    `20060102T15-04-05`, because v2 never parses an existing invoice ID and
    rewriting the folder name would rewrite history for cosmetics. The PDFs
    come along untouched.
11. **Project worktrees are untouched.** The constitution promises this, and
    the move does not threaten them: their `.git` files point at
    `.grind.repo.git/worktrees/<name>`, not at the main worktree, so
    renaming `grind/` → `.main/` leaves every project worktree working.
    There is a real consequence worth documenting (see "Known limitations"):
    a v1 project branch also contains `.grind.json`, `ideas/` and stale
    `projects/*` copies, and v2's `publish` merges the *whole branch tree*
    under `projects/<name>/`.
12. **A dirty v1 main worktree does not block the migration and is not
    swept up.** v1's `config` and `journal` commands write files without
    committing them, so a workspace that was in everyday use is routinely
    dirty. Migrate commits only the paths it changed, then tells the user to
    run `grind save` (v2's commit-everything verb) to pick up the rest. It
    refuses only when the index has unmerged paths, which would corrupt the
    config-on-main design.
13. **Every other command explains a v1 workspace instead of failing
    mysteriously.** Today `grind wwd` in a v1 workspace says
    `grind: read projects file: open …/.main/.projects.json: no such file or
    directory`. That message cost the user twenty minutes of debugging this
    week, and it will cost the next person the same. When `.grind.repo.git`
    is found but `.main/` is missing while `grind/` exists, `workspace.Require`
    returns a user error pointing at `grind migrate`.
14. **One flag: `-n`/`--dry-run`.** It prints the plan and writes nothing.
    No `--force`, no `--keep-going`: a half-migrated workspace that resumed
    wrongly is worse than a migration that stopped.

Not in this slice, even though v1 shipped them: `grind clone`, `grind
cleanup`, and v1's own `migrate` (which consolidated project configs from
worktrees onto main — a different, older refactor). See "Extension points".

## What a v1 workspace actually looks like

Captured from a real v1 workspace (grind 0.9.4) while writing this spec, so
the migration is written against observed files rather than memory:

```
<root>/
├── .grind.repo.git/                 bare repo — same name, same role as v2
│   └── worktrees/{grind,alpha,beta}/
├── grind/                           main worktree (v2 renames this to .main/)
│   ├── .git                         "gitdir: …/.grind.repo.git/worktrees/grind"
│   ├── .grind.json                  workspace config
│   ├── ideas/20261025161013.md      "# Second idea about video"
│   ├── journal/2026-10-25.md
│   └── projects/
│       ├── alpha/.project.json      per-project config (the v1 data model)
│       ├── alpha/the-idea.md
│       └── alpha/invoices/2026-10-25T21-12-46/{invoice.md,invoice.pdf}
├── alpha/                           project worktree, branch "alpha"
│   ├── .git                         "gitdir: …/worktrees/alpha"
│   ├── .grind.json                  a stale copy, committed on the branch
│   ├── ideas/                       a stale copy
│   └── projects/{alpha,beta}/       the work product, plus stale config copies
└── beta/                            project worktree, branch "beta"
```

Facts that shape the code:

- `.grind.json` in the fixture: `billing` plus optional `my`, `currency`,
  `paymentTerms`, `projectTypes`, `defaultBranch`, `remote.url`. No trailing
  newline; v2 rewrites it with one.
- `.project.json` carries `name`, `type`, `idea`, `time[]`, `tasks[]`,
  `billing{roundTo,rate}`, and optionally `client`, `repo`, `code`,
  `longTerm`, `deadline`, `publications`, `status`. There is **no
  `createdAt`**.
- v1 sessions are `{start, end, duration, rounded, invoiced?}` with ISO-8601
  UTC timestamps and `end: null` for an active session.
- v1 tasks are `{id, description, done, createdAt, completedAt?, dueDate?}`
  with IDs allocated per project starting at 1.
- `grind work`/`save`/`new`/`invoice` commit; `config` and `journal` do not.
- Older v1 workspaces may store the config in the project worktree at
  `<worktree>/projects/<name>/.project.json` instead of on main — that is
  what v1's own `migrate` command existed to fix, and those workspaces are
  still out there.

## Workspace layout: before and after

```
BEFORE (v1)                              AFTER (v2)
<root>/                                  <root>/
├── .grind.repo.git/                     ├── .grind.repo.git/   (unchanged)
├── grind/                               ├── .main/
│   ├── .grind.json                      │   ├── .grind.json      currency dropped
│   ├── ideas/*.md                       │   ├── .projects.json   all project state
│   ├── journal/*.md                     │   ├── ideas/*.md       moved for free
│   ├── projects/<name>/                 │   ├── journal/*.md     moved for free
│   │   ├── .project.json                │   ├── published/       created, .gitkeep
│   │   ├── the-idea.md                  │   ├── invoices/<name>/<v1 id>/
│   │   └── invoices/<stamp>/            │   └── projects/        created, .gitkeep
├── <project>/   (untouched)             └── <project>/          (untouched)
```

Ideas and journal move for free inside the worktree move. Everything else is
explicit work.

## Data model mapping

`grind/projects/<name>/.project.json` (v1) → `.projects.json` entry (v2):

| v1 field | v2 field | rule |
|---|---|---|
| `name` | `name` | copied; must equal the directory name (mismatch is a system error) |
| `type` | `type` | copied, `omitempty` when absent |
| `idea` | `idea` | `ideas.ExtractTitle(idea)`; body → warning |
| `time[]` | `sessions[]` | copied element-wise; `end: null` → omitted; negative `duration`/`rounded` rejected |
| `billing.roundTo` | `billing.roundTo` | copied; empty → workspace `billing.roundTo` |
| `billing.rate` | `billing.rate` | copied; missing/zero → workspace `billing.defaultRate` + warning |
| `tasks[]` | `tasks[]` | copied; IDs renumbered globally from 100; `canceled` absent → false |
| `client` | `client` | copied verbatim |
| `repo` | `repo` | copied |
| `code` | `code` | copied |
| `longTerm` | `longTerm` | copied |
| `deadline` | `deadline` | copied; malformed date → warning, dropped |
| `status` | `status` | copied; absent → `""` (active) |
| `publications[]` | — | dropped + one warning line per URL |
| — | `createdAt` | derived, see below |

`createdAt` derivation, in order:

1. the author date of the commit that first added
   `projects/<name>/.project.json` on the default branch
   (`git log --diff-filter=A --format=%aI -- projects/<name>/.project.json`);
2. otherwise the earliest session `start` for that project;
3. otherwise the config file's modification time;
4. otherwise the migration instant, with a warning naming the project.

Workspace level, `grind/.grind.json` → `.main/.grind.json`:

| v1 | v2 | rule |
|---|---|---|
| `billing` | `billing` | copied |
| `my` | `my` | copied (all six fields) |
| `paymentTerms` | `paymentTerms` | copied |
| `projectTypes` | `projectTypes` | copied |
| `defaultBranch` | `defaultBranch` | copied |
| `remote.url` | `remote.url` | copied |
| `currency` | — | dropped; warning when the value is not `USD` |

The file is rewritten through `config.Write`, so it comes out in v2's
canonical form (2-space indent, trailing newline).

## Package structure

### `internal/migrate` (NEW)

Two exported entry points, mirroring the plan/apply split:

```go
// Plan is everything migrate is going to do, computed without touching the
// filesystem. It is what --dry-run prints and what Apply executes.
type Plan struct {
    Root         string        // workspace root
    fromMain     string        // <root>/grind when it exists, else <root>/.main
    MoveMain     bool          // true when the worktree still needs moving
    Config       config.GrindConfig
    Projects     config.ProjectsConfig
    Invoices     []InvoiceMove // {Project, From, To}
    RemoveDirs   []string      // v1 project dirs to delete after consolidation
    CreateDirs   []string      // layout dirs v2 expects that v1 never made
    Warnings     []string      // human-readable, already formatted
}

// Plan reads a v1 workspace and produces the migration plan.
// It is pure with respect to the workspace: no writes, no renames.
func Plan(root string, g git.Git) (*Plan, error)

// Apply executes the plan and commits, in one commit.
func Apply(ws *workspace.Workspace, p *Plan, g git.Git) error
```

- `Plan` validates before it reads: `.grind.repo.git` exists, a v1 main
  worktree is locatable, `.projects.json` does not already exist.
- `Apply`, in order: create the new `projects/`/`invoices/`/`published/`
  dirs and any missing `.gitkeep`; move invoice directories; delete the v1
  project directories; `git worktree move <abs> <abs>` when `MoveMain`;
  write `.grind.json` and `.projects.json`; then one commit staging exactly
  the paths it touched.
- The commit message is `Migrate workspace from grind v1`.
- `Apply` never calls `git add -A`. Deletions are staged by naming the
  deleted paths, which is how `Commit` already works.

### `internal/workspace` (extend)

- `V1MainWorktreePath(root string) string` — `<root>/grind`.
- `Require` gains the v1 branch described in decision 13: bare repo found,
  `.main` missing, `grind/` present → user error
  `This workspace was created by grind v1. Run 'grind migrate' to convert it.`
- `Workspace` gains nothing else; `.main` stays the only main worktree v2
  knows about.

### `internal/git` (extend)

- `MoveWorktree(repoPath, from, to string) error` — `git worktree move`
  with both arguments absolute. The implementation asserts absolute paths
  before running, so the `.grind.repo.git/.main` trap cannot be reintroduced
  by a caller.
- `CommitAddDate(repoPath, branch, path string) (time.Time, error)` — the
  `--diff-filter=A` lookup from the `createdAt` rules, returning the zero
  time when the path has no adding commit. Implemented with
  `refs/heads/<branch>` rather than a bare branch name, for the reason the
  `LastCommitDate` comment already explains.

Both need fake implementations in the test double, which lives beside the
other fakes in `internal/cli/cli_test.go` / a shared test helper.

### `internal/cli/migrate.go` (NEW)

Thin: `workspace.Find` → `migrate.Plan` → print (and stop if `--dry-run`) →
`migrate.Apply` → print the summary.

## Command

### `grind migrate [-n|--dry-run]`

```
$ grind migrate --dry-run
Migrating workspace to the v2 layout (dry run — nothing written)

  Main worktree   grind/ → .main/
  Projects        2 migrated (alpha, beta)
  Tasks           3 renumbered (100-102)
  Sessions        1 migrated
  Invoices        1 moved
  Warnings
    beta: dropped 1 publication: https://example.com/rain
    alpha: billing.rate was missing, used workspace default 150

$ grind migrate
Migrating workspace to the v2 layout...

  Main worktree   grind/ → .main/
  Projects        2 migrated (alpha, beta)
  Tasks           3 renumbered (100-102)
  Sessions        1 migrated
  Invoices        1 moved

Committed: Migrate workspace from grind v1
Uncommitted files were already present in grind/ — run 'grind save' to commit them.

Next: grind wwd
```

- The trailing `grind save` line appears only when the v1 main worktree was
  dirty before the migration.
- Exit 0 on success, including the empty workspace (no projects).
- `--dry-run` exits 0 and writes nothing — not even `.main`.

## Error messages (exact)

| condition | message | exit |
|---|---|---|
| not in a workspace | `Not in a grind workspace.` | 1 |
| already v2 | `This workspace is already a v2 workspace.` | 1 |
| `grind/` missing and `.main/` missing | `Not in a grind workspace.` | 1 |
| unmerged paths in the index | whatever `Commit` already returns | 1 |
| v1 config unparseable | `parse projects/<name>/.project.json: <err>` | 2 |
| `name` disagrees with directory | `projects/<dir>/.project.json says name "<n>"; the directory says "<dir>".` | 2 |

## Known limitations (document, do not fix here)

- **A migrated project publishes with v1's shape nested inside it.** v1
  project branches contain `.grind.json`, `ideas/` and stale `projects/*`
  copies; v2's `MergeBranch` re-roots the whole branch tree under
  `projects/<name>/`, so publishing produces
  `projects/<name>/projects/<name>/…`. The work product is preserved, at an
  uglier path. Fixing it means either rewriting the branch (destructive) or
  teaching publish to re-root a v1 branch — both belong in their own slice.
- **`worktrees/grind` keeps its name.** `git worktree move` moves the
  directory but not the metadata directory, so `.main/.git` reads
  `gitdir: …/.grind.repo.git/worktrees/grind`. Every path v2 computes comes
  from `.main`, never from that file, and `git worktree list` reports the new
  path correctly, so this is cosmetic — but it is why a migrated workspace
  and a freshly initialised one differ in one invisible place.
- **Invoice IDs are v1-shaped.** New invoices use v2's `20060102T15-04-05`;
  migrated ones keep `2026-10-25T21-12-46`.

## Testing

Unit tests (`internal/migrate/migrate_test.go`), table-driven, no real git:

- the field mapping table, one case per row, including: `idea` with a body
  (title kept, warning), `idea` with no heading (empty title, warning),
  missing `billing.rate` (workspace default + warning), absent `billing`
  entirely, `end: null` session, negative `duration` (rejected),
  `publications` dropped, malformed `deadline`, `status` absent.
- task renumbering across two projects whose v1 IDs both start at 1 —
  asserts 100-102 in name-then-order sequence and `nextTaskId == 103`.
- `createdAt` fallback chain, with a fake `CommitAddDate` returning the zero
  time for each step.
- `currency: "AUD"` produces a warning; `currency: "USD"` does not.
- `Plan` on an already-migrated workspace is a user error.
- `Plan` writes nothing: assert the temp dir's file list and mtimes are
  unchanged after planning.

Integration tests with real git (`internal/cli/migrate_test.go`), building a
v1 workspace with git commands rather than shelling out to v1:

- full migration of a two-project workspace: `.main` exists, `grind/` gone,
  `git -C .main status --porcelain` empty, `.projects.json` parses through
  `config.ReadProjects` with both entries, tasks 100-102, invoice under
  `.main/invoices/alpha/2026-10-25T21-12-46/invoice.md`, PDF alongside it.
- **regression: the move uses absolute paths.** After migration,
  `.main` exists at `<root>/.main` and `.grind.repo.git/.main` does not.
- project worktrees still work after the move: `git -C alpha status` exits 0
  and `git -C alpha rev-parse --abbrev-ref HEAD` is `alpha`.
- v2 commands work on the result: `grind projects`, `grind wwd`, `grind
  show alpha`, `grind invoice alpha -n` (finds the migrated unbilled session).
- `grind migrate` twice: second run is a user error and changes nothing.
- resume: perform the move by hand, then run `grind migrate` and assert it
  completes (the "v1 main worktree is `grind/` or `.main/`" rule).
- dirty v1 main worktree (`journal/` untracked, `.grind.json` modified):
  migration succeeds, its own commit contains only migration paths, the
  dirty files are still dirty, and the message tells the user to run
  `grind save`.
- a v1 workspace with zero projects migrates and leaves an empty
  `.projects.json` with `nextTaskId` 100.
- legacy config location: config only at
  `alpha/projects/alpha/.project.json` migrates.
- the v1 guard: `grind wwd` in an unmigrated workspace prints the
  `grind migrate` hint, not the `.projects.json` error.

Also add `TZ=Pacific/Niue go test ./...` to the pre-commit checklist run for
this slice: `createdAt` comes from git author dates and the invoice IDs carry
local timestamps, so a timezone assumption would be caught there.

## Definition of done

- `grind migrate` converts a real v1 workspace in place: `grind/` becomes
  `.main/`, `.projects.json` holds every project, and the commit message is
  `Migrate workspace from grind v1`.
- The bare repo, the branches and every project worktree are untouched;
  `git -C <project> status` still works afterwards.
- Ideas and journal files are present under `.main/` with their contents
  unchanged.
- Sessions, tasks (renumbered from 100), clients, rates and statuses all
  survive the trip; `grind show <project>` prints the same rate and client
  the v1 config held.
- Invoices land in `.main/invoices/<project>/<v1 id>/` with the PDF beside
  the markdown.
- `currency` is gone from `.main/.grind.json`; a non-USD value produced a
  warning, not a silent drop.
- `--dry-run` writes nothing and exits 0.
- Re-running is a clean user error; an interrupted migration resumes.
- Every other command in a v1 workspace says `Run 'grind migrate' to convert
  it` instead of an `os.ErrNotExist` about `.main`.
- The `grind migrate` step never passes a relative path to
  `git worktree move`, and a test proves it.
- AGENTS.md marks slice 11 done, adds `migrate` to the command-pattern table,
  and replaces the "Migration (future slice)" section with a description of
  what shipped.
- `gofmt -l .` is silent, `go vet ./...` is clean, `go build ./...` and
  `go test ./...` pass, and `go test -race ./...` passes.
- No new module is added to `go.mod`.

## Extension points

Deliberately left out, each cheap to add without touching the design above:

- **Normalise a v1 project branch on publish.** `MergeBranch` could detect
  v1's worktree shape (a `.grind.json` at the branch root) and re-root only
  its `projects/<name>/` subtree, which would fix the known limitation above
  without rewriting history.
- **`grind clone <url>`.** v1 had it: clone a workspace, restore the main
  worktree, recreate project worktrees. Nothing in this slice depends on it,
  and `git clone` plus `grind pull` covers most of the need today.
- **`grind cleanup`.** v1's stale-branch pruner. Needs a decision on what a
  stale branch is once `.projects.json` is the source of truth.
- **`publications` in the v2 schema.** One `[]Publication` field on
  `ProjectEntry` and this slice stops dropping data.
- **Rename the `worktrees/grind` metadata directory** via
  `git worktree repair`, to make `.main/.git` read `.main`.
- **Invoice ID normalisation** to v2's `20060102T15-04-05` for migrated
  directories.
