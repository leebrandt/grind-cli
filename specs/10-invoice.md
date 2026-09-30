# Spec 10: Invoice

**Slice 10 of the Go rewrite of the grind CLI.** This slice delivers the
`grind invoice <project>` verb: turn a project's unbilled work sessions into a
markdown invoice, then mark those sessions invoiced so the next invoice covers
only new work.

Everything else (migrate) comes in a later slice — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

1. **Markdown only — no PDF.** v1 emitted `invoice.md` *and* `invoice.pdf`
   (via `pdfkit`). Go has no standard-library PDF writer, so a PDF would add a
   ~40k-line dependency plus font-embedding behavior to learn. The constitution
   says prefer the standard library unless there is a strong reason, so this
   slice emits `invoice.md` only and converts on demand with any tool
   afterwards. A PDF can be added later as its own slice without disturbing
   this design — see "Extension points".
2. **`grind invoice <project>` generates, marks, and commits — in one command.**
   There is no separate "create invoice" and "mark paid" verb. Generating an
   invoice for work the user chose to bill is the whole intent; leaving the
   marking to a second command is a foot-gun (generate, forget to mark,
   double-bill). v1 behaved the same way.
3. **Invoices live in `.main/invoices/<project>/<timestamp>/invoice.md`,** NOT
   v1's `.main/projects/<project>/invoices/<timestamp>/`. In the rewrite
   `.main/projects/` means "merged work product" (slice 9's publish writes
   there), so putting invoices under it would give one directory two unrelated
   meanings. A top-level `invoices/` is unambiguous and keeps every project's
   invoices side by side for review.
4. **Only ended, uninvoiced sessions are billed.** A session is billable when
   `End != nil && Invoiced == false`. Active sessions (`End == nil`) have
   `Rounded == 0` — they contribute nothing yet and are not "unbilled work".
5. **Amounts come from `Session.Rounded`, not `Session.Duration`.** `Rounded`
   is frozen when the session ends (see `roundTime`), so a later change to
   `billing.roundTo` cannot retroactively alter an invoice. `Duration` is the
   raw wall-clock time and is for display only.
6. **The due date honors `paymentTerms` — this fixes v1 bug #4.** v1
   hardcoded `+30 days` regardless of the configured terms, so "Net 7"
   produced a 30-day due date. `DaysFromPaymentTerms` parses the terms
   ("Net 30" → 30, "net 7" → 7, "Due on receipt" → 0, anything else → 30).
7. **All date grouping is LOCAL.** Sessions group by the local `YYYY-MM-DD` of
   their start, and the invoice date and due date are local. This matches the
   existing local-date decision for `dueDate` and `deadline` (both of which
   fix v1's UTC bugs).
8. **The rate is snapshotted into the invoice.** The invoice records the rate
   as it was at generation time. Changing `billing.rate` later does not rewrite
   an issued invoice — the markdown file is the durable record, and this is why
   the file is committed to git.
9. **No unbilled sessions is a success, not an error.** The command prints
   `No unbilled sessions for '<project>'.` and exits 0, matching v1. Running
   it twice in a row is harmless (the second run has nothing to bill), which
   makes the verb safe to put in a script.
10. **Billing works on canceled and published projects.** Canceling a project
    does not erase the work that was done on it, and the client may still owe
    for it. `invoice` therefore ignores project status.
11. **`-n` / `--dry-run` prints the invoice without writing or marking.** The
    verb mutates billing state, so the user must be able to see the number
    before it becomes a record. Nothing is written, nothing is committed, and
    the exit code is 0.
12. **`show --billing` reports the billed/unbilled split.** Invoicing without a
    way to ask "what is left to bill?" would be a half-built feature. This
    adds one read-only flag to `show` — it writes nothing and commits nothing.
13. **`wwd` flags unbilled projects the way v1 did** — yellow project name —
    without adding a column. v1's `hasUnbilled` was a color signal only;
    a new column would widen the dashboard for information that is one
    question away via `show --billing`.
14. **`Invoiced` is a flag, never a deletion.** Same philosophy as
    `Task.Canceled` and the canceled project entry: the session record stays
    so the invoice history in `.projects.json` still shows what was billed and
    when.
15. **No invoice-number sequence.** The timestamp *is* the identifier
    (`20260929T14-30-15`), which is unique per second and needs no counter to
    keep in sync. Sequential numbering (`INV-0001`) is a real requirement for
    some jurisdictions and belongs in its own slice if it is ever needed.

## Workspace layout

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config
│   ├── .projects.json      # ALL project state (single file, versioned)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # daily markdown entries: YYYY-MM-DD.md
│   ├── published/          # final-draft exports (n8n picks these up)
│   ├── invoices/           # <project>/<timestamp>/invoice.md (this slice)
│   └── projects/           # merged work products (publish)
└── <project>/              # project worktrees (one per project, own branch)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array. Never build a shell string. (This slice adds no git
   operations beyond the existing `Commit`, but the rule stands.)
2. **The main worktree is clean after every successful command.** Invoice
   commits immediately, staging only the two paths it wrote
   (`.projects.json` and the invoice file) — never `git add -A`, which fixes
   v1's known sweep bug (see `specs/archive/11-git-safety.md`).
3. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
4. **Config files are JSON with 2-space indentation, written atomically** —
   the existing `config.WriteProjects` already does this.
5. **`invoice` never touches a worktree.** No merge, no checkout, no worktree
   write. It reads and writes only `.main` state. So unlike publish/cancel it
   needs no clean-worktree precondition.

## Data model

Add to `Session` in `internal/config/config.go`:

```go
// Invoiced marks a session that has been included in a generated invoice.
// Once true the session is never billed again. The flag (not deletion)
// keeps the record: .projects.json still shows what was worked and that
// it was billed, which is the audit trail behind the invoice files.
Invoiced bool `json:"invoiced,omitempty"`
```

`omitempty` keeps it out of the file for the overwhelmingly common unbilled
session, and `Invoiced == false` on a missing field means "not invoiced", so
workspaces created before this slice need no migration.

Nothing else changes. `MyConfig`, `ClientInfo`, `GrindConfig.Currency`, and
`GrindConfig.PaymentTerms` already exist (added in spec 08) — this slice only
*reads* them. `Client` is the invoice "TO" block and `My` the "FROM" block.

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct (+ InvoicesDir)
internal/config/            # .grind.json + .projects.json (schema + Service)
internal/git/               # git interface + os/exec implementation (unchanged)
internal/grinderr/          # typed errors (User, System) + exit codes
internal/invoice/           # NEW: invoice computation + markdown rendering
internal/status/            # dashboard row computation (+ HasUnbilled)
```

### `internal/invoice` (NEW)

The package is pure with respect to the workspace: it computes an `Invoice`
value from config structs and renders it to a string. It performs no I/O and
imports only `config`, `workspace`, `grinderr`, and the standard library.
That is what makes the billing math testable without a workspace on disk.

```go
// Service generates invoices. It needs git because generating an invoice
// marks sessions invoiced and commits — the same reason projects.Service
// carries a Git.
type Service struct {
    Git git.Git
}

func NewService(g git.Git) *Service

// Invoice is the computed, renderable invoice. Everything the markdown
// needs is already resolved here so rendering is a pure function of this
// value.
type Invoice struct {
    ProjectName  string
    Description  string   // first line of the project idea, capped at 100 chars
    From         string   // "FROM" block from .grind.json my.*
    To           string   // "TO" block from the project's client.*
    Rate         float64
    Currency     string   // code from .grind.json, default "USD"
    Symbol       string   // "$", "€", "£", or "<CODE> "
    PaymentTerms string   // verbatim from .grind.json, default "Net 30"
    InvoiceDate  string   // local YYYY-MM-DD
    DueDate      string   // local YYYY-MM-DD
    ID           string   // local YYYY-MM-DDTHH-MM-SS
    Lines        []DayLine
    TotalSeconds int64
    TotalHours   float64
    Subtotal     float64
}

// DayLine is one row of the time breakdown: all billable sessions that
// started on the same local date, summed.
type DayLine struct {
    Date   string
    Seconds int64
    Hours  float64
    Amount float64
}
```

Exported functions (all pure — this is the teaching surface of the slice):

```go
// DaysFromPaymentTerms maps a payment-terms string to the number of days
// until an invoice is due: "Net 30" → 30, "net 7" → 7, "Due on receipt" → 0,
// anything unrecognized (including "") → 30. The 30-day fallback is v1's
// behavior and the safe default — an unrecognized term must never produce
// a due date in the past.
func DaysFromPaymentTerms(terms string) int

// CurrencySymbol maps a currency code to the symbol used in amounts:
// USD → "$", EUR → "€", GBP → "£". Any other code renders as the code
// followed by a space ("CHF 120.00"), which is how that currency is
// written anyway. An empty code yields "" (the default "USD" is applied
// by the caller).
func CurrencySymbol(code string) string
```

Service method:

```go
// Generate computes the invoice for a project's unbilled sessions. now is
// injectable so tests can pin the invoice date, due date, and ID.
//
// It returns (nil, nil) when the project has no billable sessions — the
// caller reports "nothing to bill" rather than treating it as an error.
// dryRun skips the state change: sessions are not marked invoiced,
// .projects.json is not written, and nothing is committed.
func (s *Service) Generate(ws *workspace.Workspace, name string, dryRun bool) (*Invoice, error)
```

`Generate` does, in order:

1. Load `.grind.json` (falling back to `config.Default()` when the file is
   missing, exactly as `show` does) and `.projects.json`.
2. `loadProject`-equivalent lookup: a missing file or unknown name is the user
   error `Project '<name>' does not exist.` — the same message the rest of
   the codebase uses, so it is worth reusing the wording exactly.
3. Select billable sessions: `s.End != nil && !s.Invoiced`.
4. If none → return `(nil, nil)`.
5. Group by `sess.Start.Local().Format("2006-01-02")`, summing `Rounded`
   seconds. **Sort the groups by date ascending** — a Go map iteration is
   randomized, so an unsorted map would produce a different invoice on every
   run and the committed file would churn. Sorting is not cosmetic here; it
   is what makes the output deterministic and diffable.
6. Apply the default currency (`"USD"`) and default terms (`"Net 30"`) when
   unset, then compute `DueDate` via `DaysFromPaymentTerms`.
7. Build the FROM/TO blocks. An empty block renders the v1 hint naming the
   exact `grind config` command that fills it in.
8. If `dryRun` → return the `Invoice`, stop.
9. Mark every selected session `Invoiced = true`, write `.projects.json`.
10. Write `invoice.md` under `ws.InvoiceDir(name, invoice.ID)`.
11. `s.Git.Commit(ws.MainWorktree, "Invoice "+invoice.ID+" for "+name,
    ".projects.json", "invoices/"+name+"/"+invoice.ID+"/invoice.md")`.

Ordering note (a v1 fix): build all content and compute all totals *before*
writing anything, so a rendering failure cannot leave a half-written invoice
next to marked sessions. Write the file, then the state, then commit both —
one commit, so the invoice and the marking can never disagree.

Unexported helpers:

```go
// renderMarkdown renders the invoice as the committed .md file. Pure
// function of an Invoice value.
func renderMarkdown(inv *Invoice) string

// dayLines groups billable sessions by local date, sorted ascending.
func dayLines(sessions []config.Session) []DayLine

// block renders a FROM/TO address block from its non-empty fields in a
// fixed order, joined by newlines. hint is returned when no field is set.
func block(fields []string, hint string) string
```

### `internal/workspace` (extend)

```go
// InvoicesDir returns the directory holding generated invoices. It is a
// top-level directory rather than projects/<name>/invoices because
// projects/ holds merged work products (publish), not billing records.
func (w *Workspace) InvoicesDir() string

// InvoiceDir returns the directory for one project's invoice with the
// given ID: invoices/<project>/<id>/. One directory per invoice keeps
// re-generating a month of billing from overwriting last month's file.
func (w *Workspace) InvoiceDir(project, id string) string
```

`Init` also gains `"invoices"` in the loop that creates `.gitkeep` files, so a
fresh workspace has the directory in its initial commit (git does not track
empty directories).

### `internal/config` (extend)

Only `Session.Invoiced` (above). No new keys, no service changes.

## Commands in this slice

### `grind invoice <project> [-n]`

Flow:

1. CLI: `workspace.Require(".")` → user error `Not in a grind workspace.`
2. `invoiceSvc.Generate(ws, args[0], dryRun)`.
3. CLI renders the result (see below).

Flag: `BoolP("dry-run", "n", false, "print the invoice without marking sessions invoiced")`.

Output (success):

```
$ grind invoice my-blog
Invoiced 3 session(s) totalling 7.25h across 2 day(s).
Subtotal: $1087.50
Invoice:  .main/invoices/my-blog/20260929T14-30-15/invoice.md
Due:      2026-10-29 (Net 30)
```

Output (nothing to bill, exit 0):

```
$ grind invoice my-blog
No unbilled sessions for 'my-blog'.
```

Output (`--dry-run`, exit 0, nothing written or committed):

```
$ grind invoice my-blog --dry-run
Would invoice 3 session(s) totalling 7.25h across 2 day(s).
Subtotal: $1087.50
Due:      2026-10-29 (Net 30)
(Dry run — nothing was written. Re-run without --dry-run to issue it.)
```

### The invoice file

`.main/invoices/<project>/<id>/invoice.md`:

```markdown
---
invoiceId: 20260929T14-30-15
date: 2026-09-29
due: 2026-10-29
project: my-blog
currency: USD
rate: 150
subtotal: 1087.50
---

# INVOICE

**Invoice Date**: 2026-09-29
**Invoice ID**: 20260929T14-30-15

---

## FROM
ACME Consulting
Lee Brandt
Tax ID: GB123456789

## TO
Client Corp
Jane Doe
jane@client.example

---

## PROJECT
**Name**: my-blog
**Description**: Write a blog post about Rust memory management

---

## TIME BREAKDOWN

| Date | Hours | Rate | Amount |
|------|-------|------|--------|
| 2026-09-14 | 3.50 | $150.00 | $525.00 |
| 2026-09-15 | 3.75 | $150.00 | $562.50 |

**Subtotal**: $1087.50
**Total**: $1087.50

---

**Payment Terms**: Net 30
**Due Date**: 2026-10-29
```

- The YAML frontmatter makes the invoice *machine-readable* — it is the same
  convention `published/<name>.md` uses (spec 09), so anything watching the
  workspace can parse invoices without scraping the table. The human-readable
  body follows v1's layout, which clients recognize.
- `FROM` field order: company, name, address, phone, email, `Tax ID: <id>`.
- `TO` field order: company, contact, address, phone, email.
- Empty FROM/TO blocks render v1's hint:
  `(not configured — run grind config my.company "Your Company")` and
  `(not configured — run grind config my-blog client.company "Client Name")`.
- `Description` is the first line of `entry.Idea`, capped at 100 characters.
- Hours and amounts render with exactly two decimals (`%.2f`).
- The file ends with a newline.

### `grind show <project> --billing`

Adds `-b, --billing` to `show`. Read-only: no writes, no commit, works on
canceled projects. v1 had this behind the same flag.

```
$ grind show my-blog --billing
Sessions:  12
Total:     20.00h (3000.00)
Billed:    12.75h (1912.50)
Unbilled:  7.25h (1087.50)
Rate:      150.00/hr (quarter-hour)
```

Without `--billing`, `show` behaves exactly as it does today (unchanged).

### `wwd` flags unbilled projects

`status.Row` gains:

```go
// HasUnbilled is true when the project has billable sessions left
// (any ended session with Invoiced == false). It is a color signal only —
// the dashboard gains no column for it.
HasUnbilled bool
```

`renderStatusTable` colors the project name yellow when `HasUnbilled`, keeping
the existing precedence: green (active session) > red (overdue deadline, not
in this rewrite) > yellow (unbilled) > plain. In the current rewrite the
order is green-when-active, then yellow-when-unbilled, then plain. An active
session with unbilled work stays green, matching v1 (where `isActive` was
checked first).

## Testing

- `internal/invoice` — the bulk of the slice's tests, all pure:
  - `DaysFromPaymentTerms`: `"Net 30"` → 30, `"Net 7"` → 7, `"net 15"` → 15,
    `"Due on receipt"` → 0, `"due on receipt"` → 0, `"weird"` → 30,
    `""` → 30, `"  Net 7  "` (trimmed) → 7. This is the v1 regression guard
    for bug #4.
  - `CurrencySymbol`: `"USD"` → `"$"`, `"EUR"` → `"€"`, `"GBP"` → `"£"`,
    `"CHF"` → `"CHF "`, `""` → `""`.
  - `dayLines`: groups by local date, sums seconds, **sorts ascending**
    (build the input out of date order and assert the output order — this is
    the regression guard against Go's randomized map iteration).
  - `renderMarkdown`: exact output for a fixed `Invoice` — frontmatter
    values, FROM/TO blocks in field order, empty-block hints, the table
    rows, two-decimal formatting, trailing newline.
  - With a fake git and a temp workspace (`.projects.json` written as
    production would write it):
    - Happy path: only ended-and-uninvoiced sessions are billed; the
      invoice file exists at `invoices/<name>/<id>/invoice.md`; exactly
      those sessions have `Invoiced == true` in `.projects.json`; the
      commit message is `Invoice <id> for <name>` staging exactly
      `[".projects.json", "invoices/<name>/<id>/invoice.md"]`.
    - Active session (`End == nil`) excluded; already-invoiced session
      excluded.
    - No billable sessions → `(nil, nil)`, and **no commit** and no
      `.projects.json` write.
    - `--dry-run` → invoice returned, but no commit, no file, and no
      session marked.
    - Project status ignored: works on `canceled` and `published`.
    - Unconfigured currency/terms fall back to USD / "Net 30"; the due
      date matches (`now + 30 days`, local).
    - Nonexistent project → exact user error.
- `internal/status`: `HasUnbilled` is true when an ended uninvoiced session
  exists, false when all ended sessions are invoiced or none exist.
- `internal/cli` with the fake git:
  - `invoice <project>` → invoice generated, file committed, output
    mentions the invoice path.
  - `invoice <project> --dry-run` → nothing written.
  - `invoice <nonexistent>` → exact user error.
  - `invoice` outside a workspace → `Not in a grind workspace.`
  - `show <project> --billing` → exact totals line; `show <project>`
    unchanged.
- `internal/workspace`: `InvoicesDir`/`InvoiceDir` return the documented
  paths; `Init` creates `invoices/.gitkeep`.
- `go build ./...`, `go vet ./...`, and `go test ./...` must all pass.

**Every test that needs a date derives it from `time.Now()`** (or injects an
explicit `now`). Hardcoded due dates are a proven failure mode in this repo —
see the fix in commit `92e63ec`, where hardcoded `2026-09-20` fixtures turned
"overdue" and broke a green suite with no code change. `Generate` takes `now`
as a parameter precisely so no test has to guess.

## Extension points

Deliberately left out, each cheap to add later without touching the design
above:

- **PDF output.** A second writer beside `renderMarkdown` that takes the same
  `Invoice` and emits bytes. The value already carries every field a PDF needs.
- **Sequential invoice numbers.** An `Invoices` counter on `ProjectEntry`,
  analogous to `NextTaskID`.
- **Multiple rate tiers or expenses.** The `DayLine` grouping is the natural
  place to add a per-day rate or a non-session line item.
- **Tax / VAT.** One field in the frontmatter plus one line in the body.
- **Emailing the invoice.** n8n already watches `published/`; a similar watch
  on `invoices/` is the same pattern.

## Definition of done

- `grind invoice <project>` writes
  `.main/invoices/<project>/<id>/invoice.md`, marks exactly the billed
  sessions `Invoiced`, commits both in one commit, and leaves `.main` clean.
- The due date honors `paymentTerms` (v1 bug #4 fixed), and all dates are
  local (v1's UTC bugs avoided).
- Time-breakdown rows are sorted by date ascending, so two runs over the same
  sessions produce byte-identical files.
- `--dry-run` prints the invoice and writes nothing.
- `grind invoice` with nothing to bill exits 0 with a clear message.
- `grind show <project> --billing` prints the total/billed/unbilled split.
- `wwd` colors projects with unbilled work yellow.
- Billing works on canceled and published projects.
- All error messages match the spec exactly; user errors exit 1.
- No new module is added to `go.mod`.
- AGENTS.md marks slice 10 done and gains the `invoice` row in the
  command-pattern table.
- `git -C .main status --porcelain` is empty after every successful command
  in this slice.
- All tests pass, `go vet` clean.
