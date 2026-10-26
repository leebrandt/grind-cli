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

## Development

```bash
gofmt -l .            # must print nothing
go build ./...
go test ./...
go vet ./...
```

See `AGENTS.md` for the architecture, the design decisions behind it, and the
per-slice build order.
