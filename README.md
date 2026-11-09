# grind

`grind` is a command-line tool for shepherding creative and technical projects
from a captured idea to something published. It keeps every project in its own
git worktree (so work-in-progress never collides), tracks the time you spend,
turns that time into invoices, and syncs the whole workspace between machines.

## Running it from a fresh clone

You need [Go](https://go.dev/dl/) 1.27 or newer. Nothing else — the binary has
no runtime dependencies.

```bash
git clone https://github.com/leebrandt/grind-cli.git
cd grind-cli
go build -o grind ./cmd/grind
```

Then put it somewhere on your `PATH`:

```bash
mv grind ~/.local/bin/    # or /usr/local/bin, if that's your thing
```

Or skip the install step and run it straight out of the clone with
`./grind <command>`.

### Create a workspace

`grind` keeps state in a hidden workspace directory (`.main/`) backed by a bare
git repo (`.grind.repo.git/`), so any directory can become one:

```bash
mkdir ~/projects && cd ~/projects
grind init
```

`grind init` prints what it set up and moves on — it will not touch an
existing workspace.

### First commands

```bash
grind new idea "A thing worth building"   # capture an idea
grind ideas                               # list ideas (0, 1, 2, ...)
grind new project my-blog 0               # promote idea 0 into a project worktree
grind work my-blog                        # start a time-tracking session
grind save my-blog                        # end it, and commit
grind wwd                                 # status dashboard (the secret handshake)
```

Run `grind --help` for the full verb list, or `grind <command> --help` for one
command's flags.

## Configuration

`grind config` gets, sets, and lists settings. Workspace settings live in
`.main/.grind.json`; project settings live in `.main/.projects.json`. Every
change is committed on its own.

```bash
grind config                              # list all workspace settings
grind config billing.defaultRate 200      # set a workspace value
grind config billing.defaultRate          # get one value
grind config my-blog                      # list one project's settings
grind config my-blog billing.rate 250     # set a project value
grind config my-blog type blog            # set a project value
```

### Workspace keys

Set with `grind config <key> <value>`.

| Key | Values | Default | Purpose |
|---|---|---|---|
| `billing.roundTo` | `quarter-hour`, `half-hour`, `hour` | `quarter-hour` | Rounds each session up to this increment when it is billed. |
| `billing.defaultRate` | positive number (USD/hour) | `150` | Hourly rate for new projects. |
| `projectTypes` | comma-separated list, e.g. `blog,code,song` | `blog,webapp,video,song,book,feature,issue` | The types a project may use. |
| `my.name` | text | unset | Your name on invoices. |
| `my.company` | text | unset | Your company on invoices. |
| `my.address` | text | unset | Your address on invoices. |
| `my.phone` | text | unset | Your phone number on invoices. |
| `my.email` | text | unset | Your email address on invoices. |
| `my.taxId` | text | unset | Your tax ID on invoices. |
| `paymentTerms` | text | `Net 30` | Payment terms printed on invoices. `Net N` sets the due date N days out, and `Due on receipt` sets it to the invoice date. Anything else falls back to 30 days. |
| `remote.url` | git URL | unset | Remote used by `grind push` and `grind pull`. Must not be empty. |

Two keys appear in `grind config` output but cannot be set:

- `defaultBranch` is the workspace's default branch. It is read-only.
- `status` is a project's lifecycle state. The `publish` and `cancel` verbs own it.

### Project keys

Set with `grind config <project> <key> <value>`.

| Key | Values | Default | Purpose |
|---|---|---|---|
| `type` | one of the workspace's `projectTypes` | unset | The kind of project. Must be a configured type. |
| `billing.roundTo` | `quarter-hour`, `half-hour`, `hour` | copied from workspace at creation | Rounding for this project's sessions. |
| `billing.rate` | positive number (USD/hour) | copied from `billing.defaultRate` at creation | Hourly rate for this project. |
| `client.contact` | text | unset | Client contact name on invoices. |
| `client.company` | text | unset | Client company on invoices. |
| `client.address` | text | unset | Client address on invoices. |
| `client.phone` | text | unset | Client phone number on invoices. |
| `client.email` | text | unset | Client email address on invoices. |
| `repo` | text | unset | Repository reference for the project. |
| `code` | text | unset | Project code or reference. |
| `longTerm` | `true`, `false` | `false` | Marks the project as long-term. |
| `deadline` | `YYYY-MM-DD` | unset | Project deadline. |

`status` also appears in a project's listing and is read-only, as described above.

### Value rules

- Project billing values are copied from the workspace when the project is
  created. Changing a workspace default later does not change existing projects.
- Booleans take exactly `true` or `false`.
- Rates must be positive numbers. Zero and negative values are rejected.
- Dates must be strict `YYYY-MM-DD`. Impossible dates such as `2026-02-30` are rejected.
- An unknown key is an error. The error lists the valid keys.

## Man

```text
GRIND(1)                     General Commands Manual                    GRIND(1)
```

### NAME

`grind` — manage creative and technical projects from idea to publication

### SYNOPSIS

```text
grind [--version] [--help]
grind <command> [<subcommand>] [<arguments>] [<options>]
```

### DESCRIPTION

`grind` keeps each project in its own git worktree, tracks the time spent on
it, turns that time into invoices, and syncs the whole workspace between
machines. Commands follow the form `grind <verb> <thing>`: the verb says what
happens, the thing says what it happens to.

Run every command from anywhere inside a workspace. `grind` walks up from the
current directory looking for `.grind.repo.git`. `grind init` creates a
workspace; `grind migrate` is the one command that accepts a v1 workspace,
which every other command refuses with a hint to run `grind migrate`.

Idea numbers are **0-based** indexes into the list printed by
`grind list ideas`. Task IDs are global integers, not per-project numbers.
Dates are local calendar dates. Durations are positive amounts of time.

### COMMANDS

#### init

**grind init**

Creates a workspace in the current directory: the bare repository
`.grind.repo.git/`, the hidden main worktree `.main/`, the config files, and
the `ideas/`, `journal/`, `published/`, `projects/` and `invoices/`
directories. Refuses to touch an existing workspace.

No options beyond `-h, --help`.

#### new

**grind new idea** `[<title>]`

Creates a new idea and commits it. With a title, the idea is written
immediately. Without one, `$EDITOR` opens a scratch file: the first
non-comment line becomes the title and the rest becomes the body. Leaving the
file without a title aborts without writing anything.

No options beyond `-h, --help`.

**grind new project** `<name> <idea-number> [<options>]`

Promotes idea `<idea-number>` into a project. Creates a branch and worktree
named `<name>`, copies the idea's title into the project entry, deletes the
idea file, and commits the state change. Billing values are copied from the
workspace defaults at this moment.

| Option | Description |
|---|---|
| `-t, --type <type>` | Project type. Must be one of the workspace's configured `projectTypes`. |

**grind new task** `<project> <description> [<options>]`

Adds a task to a project and prints its new global ID.

| Option | Description |
|---|---|
| `-d, --due <date>` | Due date. Accepts the forms listed in [ARGUMENT FORMATS](#argument-formats). |

#### list

**grind list ideas** `[<options>]`

Lists ideas in creation order, numbered from 0. Rejected ideas are hidden by
default.

| Option | Description |
|---|---|
| `-a, --all` | Include rejected ideas. |
| `-r, --rejected` | Show only rejected ideas. |

**grind list projects**

Lists projects in name order with type and creation date. Canceled projects
are hidden; published projects remain.

No options beyond `-h, --help`.

**grind list tasks** `[<project>] [<options>]`

Lists open tasks. With a project name, lists only that project's tasks and
omits the Project column. Without one, lists every project's open tasks.
Canceled tasks are always hidden.

| Option | Description |
|---|---|
| `-a, --all` | Include completed tasks. |

#### edit

**grind edit** `<project>`

Opens the project's worktree directory in `$EDITOR`. The worktree is
deliberately left dirty so editing can continue; commit with
`grind save <project>`.

No options beyond `-h, --help`.

**grind edit idea** `<number>`

Opens the idea file for idea `<number>` in `$EDITOR`. The idea is left dirty;
commit it with `grind save`.

No options beyond `-h, --help`.

**grind edit journal**

Opens today's journal entry in `$EDITOR`, creating the journal directory if
needed. The editor creates the file on save. The entry is left dirty; commit
it with `grind save`.

No options beyond `-h, --help`.

**grind edit task** `<id> [<options>]`

Changes one task's due date. This is the one `edit` subcommand that changes a
value instead of opening an editor.

| Option | Description |
|---|---|
| `-d, --due <date>` | New due date. Required. Accepts the forms listed in [ARGUMENT FORMATS](#argument-formats). |

#### reject

**grind reject idea** `<number>`

Rejects an idea by renaming its file with a `rejected-` prefix and committing
the rename. The idea survives until `grind prune ideas` deletes it.

No options beyond `-h, --help`.

#### prune

**grind prune ideas** `[<options>]`

Deletes every rejected idea and commits the deletions. Prompts for
confirmation unless `-y` is given.

| Option | Description |
|---|---|
| `-y, --yes` | Skip the confirmation prompt. |

#### show

**grind show** `<project> [<options>]`

Prints a project's name, type, creation date, rate, and founding idea content.

| Option | Description |
|---|---|
| `-b, --billing` | Also print the billed/unbilled session split: session count, total hours and amount, billed hours and amount, unbilled hours and amount, and the rate. |

#### work

**grind work** `<project>`

Starts a work session on the project, or continues the one already active. The
session is committed before `$EDITOR` opens the project worktree, so a failed
editor cannot lose it. Multiple projects may have active sessions at the same
time.

No options beyond `-h, --help`.

#### save

**grind save** `[<project>] [<options>]`

Without a project, commits every unsaved change in `.main/` — idea edits,
journal entries, and hand-edited config files. With a project, ends that
project's active session (or backfills one) and commits both the project
worktree and `.main/`. With a project name but no active session and no `-t`,
it reports that there was nothing to stop and still commits a dirty project
worktree.

`save` is local only. It never pushes; that is `grind push`'s job.

| Option | Description |
|---|---|
| `-t, --time <duration>` | Backfill instead of using the wall clock. With an active session, the end becomes `start + duration`; with no active session, a session ending now is created. Accepts `5`, `5h`, `1.5h`, `90m`, and `1h30m`. |

#### push

**grind push** `[<project>|all]`

Pushes branches to the remote. With no argument, pushes only the default
branch (the state). With a project name, pushes only that project's branch.
With `all`, pushes every branch. A project literally named `all` is shadowed
by the keyword.

Push refuses to sync a dirty worktree in scope and fails with the exact
`grind save` command to run. The remote URL comes from `remote.url` in
`.grind.json`, falling back to the bare repository's `origin` remote.

No options beyond `-h, --help`.

#### pull

**grind pull**

Fetches from the remote, fast-forwards every local branch that can move
forward, and creates worktrees for remote project branches that do not have
one. The main worktree is updated with a fast-forward only; a diverged main is
a loud failure. Project worktrees that are dirty or diverged are reported and
left alone.

No options beyond `-h, --help`.

#### finish

**grind finish task** `<id>`

Marks a task completed. Finishing an already-completed task reports that fact
and exits successfully.

No options beyond `-h, --help`.

#### cancel

**grind cancel** `<project> [<options>]`

Marks a project canceled, cancels its open tasks, and keeps the project entry
as a record. Canceled projects disappear from `grind list projects`. Refuses
to run from inside the project's own worktree, because cleanup may delete that
directory.

Before changing anything, asks what to do with the worktree and branch. The
canceled record is committed first, then the chosen cleanup runs:

| Answer | Effect |
|---|---|
| `w` | Delete the worktree, keep the branch. |
| `x` | Delete both the worktree and the branch. |
| `n` or Enter | Keep both. |

| Option | Description |
|---|---|
| `-y, --yes` | Skip the prompt and delete both the worktree and the branch. |

**grind cancel task** `<id>`

Cancels one task, hiding it from every task list. The record stays in
`.projects.json`.

No options beyond `-h, --help`.

#### read

**grind read journal** `[<options>]`

Prints every journal entry to stdout, oldest first, each under a long-form
date heading. An empty journal prints nothing and exits successfully.

| Option | Description |
|---|---|
| `-r, --reverse` | Print newest first. |

#### config

**grind config** `[<project>] [<key>] [<value>]`

Gets, sets, or lists workspace and project configuration. Every change is
committed on its own.

| Form | Effect |
|---|---|
| `grind config` | List all workspace settings. |
| `grind config <key>` | Print one workspace value. |
| `grind config <key> <value>` | Set one workspace value. |
| `grind config <project>` | List one project's settings. |
| `grind config <project> <key>` | Print one project value. |
| `grind config <project> <key> <value>` | Set one project value. |

The first argument is treated as a project when it exists in
`.projects.json`; otherwise it is a workspace key. A project named like a
workspace key shadows that key. See [Configuration](#configuration) for the
full key list and value rules.

No options beyond `-h, --help`.

#### publish

**grind publish** `<project> [<options>]`

Merges the project branch into the default branch, placing the work product
under `projects/<name>/`, then marks the project published. Both worktrees
must be clean; the error names the `grind save` command to run.

Before merging, asks what to do with the worktree and branch using the same
`w`/`x`/`n` prompt as `grind cancel`. Cleanup is applied after the publish
commit.

| Option | Description |
|---|---|
| `-y, --yes` | Skip the prompt and delete both the worktree and the branch. |

#### invoice

**grind invoice** `<project> [<options>]`

Bills the project's ended, not-yet-invoiced sessions. Amounts use each
session's frozen rounded time and the project's hourly rate. Writes
`.main/invoices/<project>/<invoice-id>/invoice.md`, marks those sessions
invoiced, and commits the invoice and `.projects.json` together. The command
is status-agnostic: active, canceled, and published projects all invoice.

If there is nothing unbilled, it prints a message and exits successfully.

| Option | Description |
|---|---|
| `-n, --dry-run` | Print what would be invoiced without writing a file or marking sessions. |

#### migrate

**grind migrate** `[<options>]`

Converts a grind v1 workspace to the v2 layout in place: renames `grind/` to
`.main/`, consolidates per-project configs into `.projects.json`, moves
invoices, creates missing layout directories, rewrites `.grind.json`, and
commits the migration. It refuses only on unmerged paths and is safe to re-run
after an interruption.

| Option | Description |
|---|---|
| `-n, --dry-run` | Print the migration plan without changing anything. |

### HIDDEN COMMANDS

These commands work but are omitted from `grind --help`:

| Command | Equivalent |
|---|---|
| `grind ideas` | `grind list ideas` |
| `grind projects` | `grind list projects` |
| `grind tasks` | `grind list tasks` |
| `grind journal` | `grind edit journal` |
| `grind wwd` | The status dashboard: per-project worked time, task counts, last session, last commit, then every open task. Project names are green while a session is active and yellow while unbilled work remains. |
| `grind completion <shell>` | Generates shell completion for `bash`, `zsh`, `fish`, or `powershell`. |

### GLOBAL OPTIONS

| Option | Description |
|---|---|
| `-h, --help` | Print help for `grind` or any command. Available on every command. |
| `-v, --version` | Print just the version number. Available on the root `grind` command. |

`grind help [<command>]` prints the same help text as `--help`.

### ARGUMENT FORMATS

**Idea numbers** are 0-based indexes in the order printed by
`grind list ideas`. `new project`, `edit idea`, and `reject idea` all use that
same numbering.

**Task IDs** are global positive integers. `new task` prints the ID it
assigned; `finish task`, `cancel task`, and `edit task` take it.

**Project names** are also branch names. They may not contain whitespace, may
not be `.main` or `.grind.repo.git`, and must be valid git branch names.

**Dates** are parsed in local time, case-insensitively, with surrounding
whitespace trimmed. Accepted forms:

```text
today
tomorrow
3d         3days
1w         1week
0720       July 20 of the current year (MMDD)
072026     July 20, 2026 (MMDDYY)
20260720   July 20, 2026 (YYYYMMDD)
2026-07-20 July 20, 2026 (ISO)
```

Impossible dates such as `2026-02-30` are rejected.

**Durations** are positive and case-insensitive. Accepted forms are a plain
number of hours (`5`), hours with an `h` suffix (`5h`, `1.5h`), minutes with an
`m` suffix (`90m`), and hours plus minutes (`1h30m`).

### ENVIRONMENT

| Variable | Description |
|---|---|
| `EDITOR` | Editor command used by `new idea`, `edit`, `work`, and `edit journal`. Split on whitespace, so values like `code --wait` work. |
| `VISUAL` | Fallback when `EDITOR` is unset. |
| `TZ` | Local timezone. It decides the journal's date filename and the local days used on invoices; session timestamps themselves are stored in UTC. |

If neither `EDITOR` nor `VISUAL` is set, the editor is `vi`. Colors are emitted
only when stdout is a terminal, so piped output is plain text.

### FILES

| Path | Contents |
|---|---|
| `.grind.repo.git/` | Bare git repository shared by every worktree. |
| `.main/` | Hidden main worktree holding all workspace state. |
| `.main/.grind.json` | Workspace configuration. |
| `.main/.projects.json` | All project state, keyed by project name. |
| `.main/ideas/` | Idea files named `YYYYMMDDHHmmss.md`; rejected ideas gain a `rejected-` prefix. |
| `.main/journal/` | Daily journal entries named `YYYY-MM-DD.md`. |
| `.main/published/` | Final-draft exports. |
| `.main/invoices/<project>/<invoice-id>/invoice.md` | Generated invoices. |
| `<project>/` | One worktree per project, on a branch of the same name. |

### EXIT STATUS

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | User error: bad input, missing project or idea, dirty worktree, user abort. |
| `2` | System error: filesystem, git, or editor failure. |
| `99` | Unexpected error. |

Errors are printed to stderr with a `grind:` prefix.

### SEE ALSO

`grind --help`, `grind <command> --help`, [Configuration](#configuration),
`AGENTS.md`.

## Development

```bash
gofmt -l .            # must print nothing
go build ./...
go test ./...
go vet ./...
```

See `AGENTS.md` for the architecture, the design decisions behind it, and the
per-slice build order.
