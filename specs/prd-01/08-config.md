# Spec 08: Config

**Slice 8 of the Go rewrite of the grind CLI.** This slice delivers the
`grind config` command: get, set, and list workspace config (`.grind.json`)
and project config (`.projects.json`). Both files live in `.main`, so the
command is far simpler than v1's — no `-g` flag, no per-project config
files scattered around the workspace.

Everything else (publish/cancel, invoices, migrate) comes in later slices —
do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

1. **`grind config` covers every config key v1 has.** Workspace keys from
   `.grind.json`, project keys from `.projects.json`. The rewrite's data
   model — two files, both in `.main` — makes the command shape trivial
   compared to v1's `-g` flag and per-project files.
2. **Command shape — flag-free, project-name disambiguation:**
   ```
   grind config                          # list workspace config
   grind config <key> [value]            # get/set workspace config
   grind config <project>                # list project config
   grind config <project> <key> [value]  # get/set project config
   ```
   The first argument is a project if it exists in `.projects.json`;
   otherwise it is a workspace key. A project literally named like a
   workspace key (e.g. `currency`) shadows that key — the same accepted
   edge case as `push all` shadowing a project named `all`.
3. **Every set commits immediately.** Writing `.grind.json` commits with
   `Set config: <key>`; writing `.projects.json` commits with
   `Set config: <project> <key>`. Both stage only the specific file
   (constitution rule 2). The main worktree is clean after every command.
4. **`remote.url` set also syncs the bare repo's origin** via
   `git.SetRemoteURL`, so the URL is live the moment it is set — no need to
   wait for the next push/pull.
5. **`repo` (project) and `code` are stored but not consumed yet.** The
   rewrite's push/pull use the workspace `remote.url`, not per-project
   repos; there is no `grind code` command yet. The keys are part of the
   v1 schema and are settable/gettable now, consumed by later slices.
6. **`defaultBranch` is listed if present but NOT settable.** Renaming the
   default branch is out of scope; init hardcodes `main`.
7. **No URL format validation** for `remote.url`/`repo`. v1 restricted to
   GitHub/GitLab; the rewrite supports any git remote (Codeberg, self-hosted,
   SSH, HTTPS, local paths) — git is the validator.

## Workspace layout (unchanged)

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config (this slice's target)
│   ├── .projects.json      # ALL project state (this slice's target)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # daily markdown entries: YYYY-MM-DD.md
│   └── published/          # (empty; future slice)
└── <project>/              # project worktrees (one per project, own branch)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array. Never build a shell string.
2. **The main worktree is clean after every successful command.** Every
   `grind config ... set` commits immediately, staging only the specific
   file it changed.
3. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
4. **Config files are JSON with 2-space indentation, written atomically**
   (temp file + `os.Rename`) — the existing `config.Write`/`WriteProjects`
   already do this.

## Data model

### `.grind.json` — workspace config

Extend `MyConfig` to the full v1 professional-info set:

```go
type MyConfig struct {
    Name    string `json:"name,omitempty"`
    Company string `json:"company,omitempty"`
    Address string `json:"address,omitempty"`
    Phone   string `json:"phone,omitempty"`
    Email   string `json:"email,omitempty"`
    TaxID   string `json:"taxId,omitempty"`
}
```

Settable workspace keys (all already in the `GrindConfig` schema):

| key | type | validation |
|---|---|---|
| `billing.roundTo` | string | `quarter-hour` \| `half-hour` \| `hour` |
| `billing.defaultRate` | number | positive |
| `projectTypes` | []string | comma-separated input, non-empty |
| `my.name` | string | — |
| `my.company` | string | — |
| `my.address` | string | — |
| `my.phone` | string | — |
| `my.email` | string | — |
| `my.taxId` | string | — |
| `currency` | string | — |
| `paymentTerms` | string | — |
| `remote.url` | string | non-empty; also syncs origin |

`defaultBranch` is listed if present in the file, but is NOT settable.

### `.projects.json` — project config

Add the v1 project keys to `ProjectEntry`:

```go
// ClientInfo is the invoice "TO" block. It lives in the config package
// (not internal/invoice) because the config command writes it and the
// invoice slice will read it.
type ClientInfo struct {
    Contact string `json:"contact,omitempty"`
    Company string `json:"company,omitempty"`
    Address string `json:"address,omitempty"`
    Phone   string `json:"phone,omitempty"`
    Email   string `json:"email,omitempty"`
}

type ProjectEntry struct {
    // ...existing fields (name, type, idea, billing, createdAt, sessions, tasks)...
    Client   *ClientInfo `json:"client,omitempty"`
    Repo     string      `json:"repo,omitempty"`
    Code     string      `json:"code,omitempty"`
    LongTerm bool        `json:"longTerm,omitempty"`
    Deadline string      `json:"deadline,omitempty"`
}
```

Settable project keys:

| key | type | validation |
|---|---|---|
| `type` | string | in the effective project types |
| `billing.roundTo` | string | `quarter-hour` \| `half-hour` \| `hour` |
| `billing.rate` | number | positive |
| `client.contact` | string | — |
| `client.company` | string | — |
| `client.address` | string | — |
| `client.phone` | string | — |
| `client.email` | string | — |
| `repo` | string | — |
| `code` | string | — |
| `longTerm` | bool | `true` \| `false` |
| `deadline` | string | strict `YYYY-MM-DD`, real calendar date |

`LongTerm` is a plain bool: unset and `false` are the same effective value,
and `json:"longTerm,omitempty"` omits `false` from the file. The project
list always shows the effective value.

### Shared type validation

Move `defaultTypes`, `validTypes`, and `validateType` from
`internal/projects` into `internal/config`, exported as `ValidTypes(cfg
GrindConfig) []string` and `ValidateType(cfg GrindConfig, projectType
string) error`. The config command needs the same validation, and the
helpers must live in config because projects imports config — a
config→projects import would be a cycle. `internal/projects` calls
`config.ValidateType` instead of its own copy.

## Commands in this slice

### `grind config` — get, set, list config

Grammar (max 3 args):

| args | scope | action |
|---|---|---|
| (none) | workspace | list all keys |
| `<key>` | workspace | get one value |
| `<key> <value>` | workspace | set one value |
| `<project>` | project | list all keys |
| `<project> <key>` | project | get one value |
| `<project> <key> <value>` | project | set one value |

Flow:

1. Require a workspace (user error `Not in a grind workspace.`, exit 1).
2. Build `config.Paths` from the workspace.
3. If args are present and the first is a project (`.projects.json`
   lookup), project scope; otherwise workspace scope.
4. Workspace scope with 3 args → user error (too many arguments).
5. Get: missing key → user error `Key not found: <key>`.
6. Set: validate the key against the settable list and the value against
   the key's rule; write the file atomically; commit staging only the
   specific file; print `key = value`.
7. List: print `key = value` lines, sorted by key, arrays comma-joined.

Output examples:

```
$ grind config
billing.defaultRate = 150
billing.roundTo = quarter-hour
my.email = lee@example.com
my.name = Lee
remote.url = git@github.com:lee/grind.git

$ grind config my-blog
billing.rate = 250
billing.roundTo = quarter-hour
type = blog
```

Error messages (user errors, exit 1):

- `Invalid key for workspace config: <key>. Valid keys: <list>`
- `Invalid key for project config: <key>. Valid keys: <list>`
- `Key not found: <key>`
- `Invalid roundTo value: <v>. Valid options: quarter-hour, half-hour, hour`
- `Invalid rate: <v>. Must be a positive number.`
- `Invalid projectTypes: <v>. Must be a comma-separated list of types.`
- `Invalid type: <v>. Valid types: <list>` (from the shared validator)
- `Invalid longTerm value: <v>. Must be true or false.`
- `Invalid deadline: <v>. Expected format: YYYY-MM-DD.`
- `Project '<name>' does not exist.`

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json (schema + NEW Service)
internal/git/               # git interface + os/exec implementation (unchanged)
internal/grinderr/          # typed errors (User, System) + exit codes
internal/editor/            # $EDITOR integration (unchanged)
internal/ideas/             # idea operations (unchanged)
internal/projects/          # project lifecycle + sessions (validators move out)
internal/tasks/             # task operations (unchanged)
internal/dates/             # due-date parsing (unchanged)
internal/color/             # ANSI colors with TTY detection (unchanged)
internal/journal/           # journal file operations (unchanged)
internal/status/            # dashboard row computation (unchanged)
internal/sync/              # push/pull orchestration (unchanged)
```

No new `git.Git` interface methods are needed — the Service uses the
existing `Commit` and `SetRemoteURL`.

### `internal/config` (extend)

```go
// Paths bundles the file paths a config operation touches. The CLI builds
// it from *workspace.Workspace; config cannot take the workspace itself
// because workspace imports config — a config→workspace import would be a
// cycle.
type Paths struct {
    ConfigPath   string // .main/.grind.json
    ProjectsPath string // .main/.projects.json
    MainWorktree string // .main
    BareRepo     string // .grind.repo.git
}

// Entry is one flattened key=value config pair.
type Entry struct {
    Key   string
    Value string
}

// Service reads and writes the workspace config files. The git layer is
// injected so tests can substitute a fake.
type Service struct {
    Git git.Git
}

func NewService(g git.Git) *Service

// HasProject reports whether project exists in .projects.json. The CLI
// uses it to decide whether the first argument names a project.
func (s *Service) HasProject(p Paths, name string) (bool, error)

func (s *Service) List(p Paths) ([]Entry, error)                    // workspace
func (s *Service) Get(p Paths, key string) (string, error)
func (s *Service) Set(p Paths, key, value string) error

func (s *Service) ProjectList(p Paths, project string) ([]Entry, error)
func (s *Service) ProjectGet(p Paths, project, key string) (string, error)
func (s *Service) ProjectSet(p Paths, project, key, value string) error
```

- A missing `.grind.json` is treated as `config.Default()` (matching
  projects.go's `readConfig`); `Set` writes the defaults plus the change.
- A missing `.projects.json` is treated as empty; project lookups fail with
  `Project '<name>' does not exist.`
- `Set` validates the key against the settable list and the value against
  the key's rule, then writes atomically and commits.
- `remote.url` set also calls `s.Git.SetRemoteURL(p.BareRepo, value)`.
- Flattening and setting are explicit — a switch over the known keys, not
  reflection. The key set is fixed and small; reflection would hide bugs.

### `internal/cli` (extend)

- `config.go` (NEW): thin command — resolve workspace, build `Paths`,
  decide scope via `HasProject`, dispatch to the service, print.
- `root.go`: wire `config.NewService(g)`, add `newConfigCmd`, bump the
  version constant to `0.90.6`.
- `AGENTS.md`: add the `config` row to the command-pattern table, mark
  slice 8 done in the slice table.

### `internal/projects` (refactor)

- Remove `defaultTypes`, `validTypes`, `validateType`; call
  `config.ValidateType` instead. Behavior unchanged.

## Testing

- `internal/config` with a fake git (records `Commit` calls and
  `SetRemoteURL`):
  - Schema round-trips: `MyConfig` with all six fields; `ProjectEntry`
    with client/repo/code/longTerm/deadline.
  - List: fresh workspace shows the billing defaults; output sorted; arrays
    comma-joined; missing `.grind.json` → defaults.
  - Get: existing key, missing key (`Key not found`), nested key.
  - Set: one test per key family — roundTo validation, rate validation,
    projectTypes parsing, `my.*`, `currency`, `paymentTerms`,
    `remote.url` (asserts `SetRemoteURL` was called); commit message and
    staged file asserted; missing `.grind.json` → defaults plus the change.
  - Project: `HasProject`; list; get; set for each key family (`type`
    validated against effective types, billing, `client.*`, `repo`, `code`,
    `longTerm`, `deadline`); unknown project → user error.
  - Invalid values → user errors with the exact messages above.
- `internal/cli` with the fake git:
  - `config` (no args) lists; `<key>` gets; `<key> <value>` sets;
    `<project> ...` project scope; unknown key error; invalid value error;
    not-in-workspace error.
- `internal/projects`: existing tests updated for the moved validators
  (the `validTypes`/`validateType` tests move to `internal/config`).
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind config` lists workspace config; `grind config <key> [value]`
  gets/sets every v1 workspace key; `grind config <project> <key> [value]`
  gets/sets every v1 project key.
- Every set commits immediately and leaves `.main` clean.
- `remote.url` set syncs the bare repo's origin.
- All v1 config keys are settable: workspace (`billing.roundTo`,
  `billing.defaultRate`, `projectTypes`, `my.*`, `currency`,
  `paymentTerms`, `remote.url`) and project (`type`, `billing.roundTo`,
  `billing.rate`, `client.*`, `repo`, `code`, `longTerm`, `deadline`).
- AGENTS.md reflects the config command and marks slice 8 done.
- `git -C .main status --porcelain` is empty after every successful command
  in this slice.
- All tests pass, `go vet` clean.