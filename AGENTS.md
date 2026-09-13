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
│   └── published/          # final-draft exports (n8n picks these up)
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
  professional info, currency, payment terms, remote URL, default branch.
- Config files are JSON with 2-space indentation, written **atomically**
  (temp file + `os.Rename`).

### Git rules

1. **No shell interpolation. Ever.** Every git command runs via `os/exec`
   with an argv array. Never build a shell string. This is the single most
   important rule — v1's shell-string magic was the source of most of its bugs.
2. **The main worktree is clean after every command.** Any command that
   mutates state commits immediately. Stage **only the specific files
   changed** — never `git add -A`. (The one exception: `edit` leaves files
   dirty by design so the user can keep editing.)
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

### Push/pull (cross-machine sync)

- `grind save` commits **both** worktrees — the project worktree (the work)
  and the main worktree (the state) — then pushes **both** branches. v1 only
  committed the main worktree, so the actual work never reached the remote.
  That bug is the reason this rule exists.
- `grind push` pushes all branches. `grind pull` fetches, fast-forwards, and
  creates missing project worktrees.

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
| `work` / `save` | start / stop session | `work my-blog`, `save my-blog` |
| `reject` / `prune` | idea lifecycle | `reject idea 3`, `prune ideas` |
| `publish` / `cancel` | project lifecycle | `publish my-blog`, `cancel my-blog` |
| `read` | print to stdout | `read journal` |
| `done` | complete a task | `done task 3` |

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

### Migration (future slice)

The v1 workspace migrates cleanly because the git history lives in the bare
repo, which is reused as-is. `grind migrate` will: rename `grind/` →
`.main/` (a `git worktree move`), consolidate `grind/projects/*/.project.json`
into `.projects.json`, and commit. Project worktrees are untouched.

## Slices

Planned order (each is a spec in `specs/`):

1. **Foundation + ideas** — `init`, `new idea`, `list ideas`, `edit idea`,
   `reject idea`, `prune ideas` ✅ current
2. **Projects** — `new project`, `list projects`, `show`
3. **Work/save** — sessions, commit both worktrees, push
4. **Tasks**
5. **Journal**
6. **Status**
7. **Push/pull**
8. **Config**
9. **Publish/cancel**
10. **Invoice**
11. **Migrate** (v1 workspace conversion)

## Build & test

```bash
go build ./...        # build
go test ./...         # run tests
go vet ./...          # static checks
go build -o grind ./cmd/grind   # build the binary
```

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