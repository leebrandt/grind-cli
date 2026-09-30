# AGENTS.md

Guidance for AI agents (and future sessions) working in this repository.

## Project overview

**grind** is a CLI tool for managing creative/technical projects from idea to
publication: idea capture, git-worktree-isolated project workspaces, time
tracking, billing, tasks, journaling, and publishing.

This repository is a **Go rewrite** of the TypeScript/Bun CLI at
`~/src/grind` (v1). The v1 codebase is the behavioral reference — when in
doubt about how a feature should behave, read the v1 source. But v1's
architecture is NOT the target; the design decisions below supersede it.

The rewrite is built **in slices**. Each slice is a vertical feature that
works end-to-end. The user reviews and absorbs each slice before the next
one starts.

## The user is learning Go

This project is how the user is learning Go. This has real consequences:

- Code must be **idiomatic, readable, and conventional**. No clever one-liners.
- Comments explain **why**, not **what**. The code should teach.
- Tests are learning material too — they should read like documentation.
- When a slice lands, flag the interesting Go idioms in the summary
  ("here's where `errors.Is` earns its keep", "here's why this is an
  interface"). The user will ask questions about them.
- Prefer the standard library over dependencies unless there's a strong
  reason. Every dependency is something else to learn.

## Design decisions (the constitution)

These were agreed with the user. Do not silently change them. If a decision
turns out wrong, raise it in conversation first.

### Workspace layout

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config
│   ├── .projects.json      # ALL project state (single file, versioned)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # daily markdown entries
│   ├── published/          # final-draft exports (n8n picks these up)
│   └── invoices/           # generated invoices, one dir per invoice
└── <project>/              # project worktrees (one per project, own branch)
```

- The main worktree is named `.main` so it hides from everyday `ls` output
  but stays accessible (`cd .main`) when things need fixing.
- Project branches contain **only the actual work product**. No configs, no
  state. All project state lives in `.projects.json` on the default branch.

### Data model

- `.projects.json` is a single file holding all project state, keyed by
  project name, with a `version` field for future migrations:
  ```json
  { "version": 1, "projects": { "leenix": { "name": "leenix", ... } } }
  ```
- Per-project billing rates are supported (each project entry carries its own
  `billing` block) — this is a hard requirement, not a nice-to-have.
- `.grind.json` holds workspace-level config: billing defaults, project types,
  professional info, payment terms, remote URL, default branch. There is no
  currency key — grind bills in USD (see "Invoicing").
- Config files are JSON with 2-space indentation, written **atomically**
  (temp file + `os.Rename`).

### Git rules

1. **No shell interpolation. Ever.** Every git command runs via `os/exec`
   with an argv array. Never build a shell string. This is the single most
   important rule — v1's shell-string magic was the source of most of its bugs.
2. **The main worktree is clean after every command.** Any command that
   mutates state commits immediately. Stage **only the specific files
   changed** — never `git add -A`. (The one exception: `edit` leaves files
   dirty by design so the user can keep editing, and `grind save` with no
   arguments is the explicit commit-everything verb that sweeps those edits
   up.)
3. The git layer is behind an **interface** so tests can fake it.
4. `Commit` refuses to commit when there are unmerged paths
   (`git ls-files -u` non-empty) — protects the config-on-main design.

### Sessions (time tracking)

- Sessions are per-project. **Double-dipping is supported**: you can have an
  active session on project A and project B simultaneously.
- `grind work <project>` starts a session on that project. If the project
  already has an active session, warn and continue it (don't start a second
  one — that was v1's orphan bug).
- `grind save <project>` ends that project's active session. Project name is
  required.
- `grind save <project> -t 8h` backfills: active session → end at
  `start + 8h`; no session → create `[now-8h, now]`. Both cases work.
- `grind save` with no arguments commits every unsaved change in `.main`
  (idea edits, journal entries, hand-edited configs). This is the missing
  commit verb: `edit idea` leaves files dirty by design, and nothing else
  commits them.

### Push/pull (cross-machine sync)

- `grind save` commits **both** worktrees — the project worktree (the work)
  and the main worktree (the state) — but NEVER pushes. Saving is local:
  fast, offline-friendly, and the work is always committed. v1 only
  committed the main worktree, so the actual work never reached the remote;
  the rewrite fixes that by committing both worktrees.
- `grind push` is scoped. `grind push` pushes only the default branch
  (`.main`'s branch); `grind push <project>` pushes only that project's
  branch; `grind push all` pushes every branch. The default is deliberately
  conservative: state syncs by default, work product syncs on request.
- Push refuses to sync uncommitted work: a dirty worktree in scope fails
  with a user error naming exactly what to run (`grind save` or
  `grind save <project>`). A failed push is a real error (exit 1) — the
  user asked for it.
- `grind pull` fetches, fast-forwards what it can, and creates missing
  project worktrees. The main worktree is updated with
  `git merge --ff-only origin/<default>`; a diverged main is a loud failure,
  not a warning. Project worktrees that cannot fast-forward (dirty or
  diverged) are reported and left alone.
- The remote URL lives in `.grind.json` (`remote.url`), with the git remote
  as fallback. Push/pull resolve the URL (config first, then
  `git remote get-url origin`) and sync the bare repo's `origin` from it —
  the URL travels with the workspace instead of being a per-machine
  artifact. No URL anywhere → user error.

### Time

- **No code calls `time.Now()` directly.** Everything asks a
  `clock.Clock` (`internal/clock`). Production wires `clock.Real{}`; a
  test wires `clock.Fake` and sets the time. `NewRootCmdWithClock` takes
  one clock and hands the same instance to every service, so a command
  run is one instant, not twelve.
- The interface has exactly one method because that is all grind needs.
  The ecosystem versions (`clockwork`, `benbjohnson/clock`) also carry
  `After`, `Sleep` and `Timer` for code that *waits* on time; grind has
  none, so they are absent. **A seam should be as small as the dependency
  really is.**
- Services default to `clock.Real{}` in `NewService`. Nothing builds a
  service any other way, so a nil clock is unreachable — and every test
  that does not care about time keeps working untouched.
- Where a function can simply *take* the instant, it does, and no clock
  is involved: `invoice.Generate(ws, name, dryRun, now)` takes a
  `time.Time`, because plain data needs no abstraction. Reach for the
  clock only where code reads the time for itself.
- A test that needs a date asserts an exact date — `2026-09-30.md`, not
  `TodayFilename(time.Now())`. Asserting "roughly now" is how the status
  tests rotted the first time. Pinning the clock also makes the suite
  timezone-proof, because a fixed local instant renders as the same
  YYYY-MM-DD under any `TZ`.

### Error handling

- Typed errors: user errors exit 1, system errors exit 2, unexpected exit 99.
- `main()` maps errors to exit codes. No `os.Exit` inside commands.
- This mirrors v1's `GrindError` hierarchy, which was one of its strengths.

### Command pattern

`grind <verb> <thing>` — verb first, then the thing you're acting on. Small
verb set, each verb means exactly one thing:

| verb | meaning | examples |
|---|---|---|
| `new` | create | `new idea`, `new project`, `new task` |
| `list` | enumerate | `list ideas`, `list projects`, `list tasks` |
| `show` | detail one | `show my-blog` |
| `edit` | open in editor | `edit idea 3`, `edit my-blog` |
| `work` / `save` | start / stop session, or commit workspace | `work my-blog`, `save my-blog`, `save` |
| `push` / `pull` | sync with remote | `grind push`, `grind push my-blog`, `grind push all`, `grind pull` |
| `reject` / `prune` | idea lifecycle | `reject idea 3`, `prune ideas` |
| `publish` / `cancel` | project lifecycle | `publish my-blog`, `cancel my-blog` |
| `invoice` | bill billable work | `invoice my-blog`, `invoice my-blog -n` |
| `read` | print to stdout | `read journal` |
| `done` | complete a task | `done task 3` |
| `config` | get/set/list config | `grind config`, `grind config billing.defaultRate 200`, `grind config my-blog billing.rate 250` |

Rules:
- **Singular when acting on one, plural when acting on many.** `new idea`,
  `list ideas`, `reject idea 3`, `prune ideas`.
- **When the thing is a project, the project name IS the thing.** `work
  my-blog`, `save my-blog`, `show my-blog` — no word "project" needed.
- **Natural shortcuts** exist for the most-used commands: `grind ideas` =
  `grind list ideas`, `grind projects` = `grind list projects`, `grind tasks`
  = `grind list tasks`.
- **Hidden commands** exist for power users: `grind wwd` (status + tasks
  dashboard) is the user's most-used command and must stay hidden from
  `--help` (Cobra `Hidden: true`). It's the secret handshake.

### Publishing (future slice)

`grind publish <project>` merges the project branch and exports a final-draft
markdown file (frontmatter + body) into `published/`. An n8n workflow watches
that directory and runs the content-marketing pipeline (SubStack, blog,
social). Grind only produces the draft; n8n does everything else.

### Invoicing

- Grind bills in **USD and only USD**. There is no `currency` config key and
  no currency code in the state file; `invoice.Symbol` is the hardcoded `$`.
  v1's `currency` key was deleted rather than left inert, because a setting
  that succeeds and changes nothing is worse than one that is rejected. A v1
  `.grind.json` that still carries `"currency"` is harmless: the unknown JSON
  key is dropped on read and never written back (slice 11 migrates v1).
- `grind invoice <project>` bills the sessions that are **ended and not yet
  invoiced**. Billable time is `Session.Rounded` (the rounded seconds written
  at end time), never `Session.Duration` — the rounding already happened.
- The rule is deliberately status-agnostic: active, canceled and published
  projects all invoice, because the sessions are already real work. Only the
  end and the `invoiced` flag matter.
- Each session is flipped to `invoiced: true`, and the generated markdown lands
  in `.main/invoices/<project>/<timestamp>/invoice.md`. Both the invoice file
  and `.projects.json` are committed in one commit, so a half-invoiced
  workspace is not a state the user can reach.
- `Session.Invoiced` is per session, not a date or amount watermark. Anything
  coarser re-bills work that has already gone out the door.
- Dates are **local**: the invoice id, the invoice date, the due date and the
  per-day breakdown all come from the local clock. A session is grouped into
  the day its `Start` falls on.
- `grind show <project> --billing` prints the billed/unbilled split, and `wwd`
  paints a project yellow while it has unbilled work — the two ways to ask
  "what is still unbillable?" without writing an invoice.
- `Generate` takes `now` as a parameter and the whole package is pure
  (no clock, no I/O) so the day-boundary, ordering and parsing rules are
  testable. The one caller is the CLI, which passes `time.Now()`. A zero `now`
  is *not* silently replaced with the clock — a date that silently comes from
  somewhere else is a date nobody can reason about.

### Migration (future slice)

The v1 workspace migrates cleanly because the git history lives in the bare
repo, which is reused as-is. `grind migrate` will: rename `grind/` →
`.main/` (a `git worktree move`), consolidate `grind/projects/*/.project.json`
into `.projects.json`, and commit. Project worktrees are untouched.

## Slices

Each slice is a vertical feature that works end-to-end, driven by a spec in
`specs/`. Status is tracked here so we can see at a glance how much is done
and what's next.

| # | Slice | Status |
|---|---|---|
| 1 | Foundation + ideas | ✅ done |
| 2 | Projects | ✅ done |
| 3 | Work/save | ✅ done |
| 4 | Tasks | ✅ done |
| 5 | Journal | ✅ done |
| 6 | Status (`wwd`) | ✅ done |
| 7 | Push/pull | ✅ done |
| 8 | Config | ✅ done |
| 9 | Publish/cancel | ✅ done |
| 10 | Invoice | ✅ done |
| 11 | Migrate (v1 workspace conversion) | ⬜ |

## Build & test

```bash
gofmt -l .           # must print nothing — run this first, always
go build ./...        # build
go test ./...         # run tests
go vet ./...          # static checks
go build -o grind ./cmd/grind   # build the binary
```

`gofmt -l .` is the first line for a reason. Nine slices of drift piled up
unformatted before anyone noticed, because nothing ran it — and `go build`
and `go test` both pass on unformatted code, so only the explicit check
catches it. A file counts as formatted only if `gofmt -l` is silent about
it, and silence has to be checked *before* the commit, not after.

Two more checks worth running before calling a slice done, because both
catch classes of bug that a green `go test` hides:

```bash
go test -race ./...   # concurrency; the git and push/pull paths
TZ=Pacific/Niue go test ./...   # day-boundary and timezone assumptions
```

A test that reads `time.Now()` and compares it against a hardcoded date
passes today and fails next month. That is how the status tests rotted
once already. When a test needs "now", derive the expectation from the
clock too (`TodayFilename(time.Now())`), or inject the instant as a
parameter — never mix a literal date into a clock-derived comparison.

## Dev-agent workflow

- Each slice is implemented by **3 independent dev agents**, each in its own
  git worktree off `develop`, all implementing the same spec.
- The orchestrator reads all three, evaluates them, and writes a synthesized
  implementation on a `feature/<slug>` branch — cherry-picking the strongest
  parts. No agent's code is copied wholesale.
- Specs live in `specs/` and are the source of truth for a slice.

## Reference

- v1 TypeScript codebase: `~/src/grind` (behavioral reference)
- v1 code review (known bugs to avoid): `~/src/grind/docs/code-review-08032026.md`