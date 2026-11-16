---
title: "`grind hwd` — hours-worked weekly report"
version: 0.1
date_created: 2026-11-15
last_updated: 2026-11-15
owner: Lee Brandt
status: Final
---

# `grind hwd` — hours-worked weekly report — Product Requirements Document

## 1. Overview

`grind hwd` is a new hidden command that prints a timesheet for the current
week — the week that began at 00:00 local time on the most recent Sunday and
runs up to the moment the command is run. It answers four questions on one
screen: how many hours did I work, on which projects, how much would that work
bill for (and how much of it has already been invoiced), and did any projects
finish. `grind hwd -w N` prints the same report for a complete previous week —
`-w 1` is last week, `-w 2` the week before that — so a month-end billing run
can walk backwards week by week instead of re-deriving each one by hand. It is
a read-only view, like `grind wwd`, its sibling: it never writes, never
commits, and never touches a worktree.

Answering the last question requires one additive change to the project state:
`ProjectEntry` gains `publishedAt` and `canceledAt` timestamps, written by the
existing `grind publish` and `grind cancel` verbs. Nothing else in the data
model changes, and no migration is needed.

## 2. Problem Statement

The state needed to answer "how did this week go?" already exists in
`.projects.json` — every session with its start, end, duration, rounded
duration and invoiced flag — but there is no way to ask a week-shaped question
of it.

Today the user reconstructs the week by hand:

- `grind wwd` shows *all-time* worked hours per project. It cannot scope to a
  week, so "3.5h" on Monday and "38.5h" on Friday look equally useful and
  equally weekless.
- `grind show <project> --billing` answers "what is left to bill?" per
  project, so a five-project week takes five commands and a mental sum.
- `grind invoice <project> -n` prints what an invoice *would* bill — but only
  for unbilled work, and only one project at a time. There is no way to ask
  what the whole week is worth.
- Nothing anywhere records when a project finished. `publish` sets
  `status: "published"` and `cancel` sets `status: "canceled"`, and neither
  records *when*. v1 stored `publications[].publishedAt`; the rewrite dropped
  it deliberately (see `internal/migrate/migrate.go`), so "did any projects
  finish this week?" is currently unanswerable from the state file at all.

The cost is a weekly ritual that is either skipped or done by hand in a
spreadsheet, and a billing gap that is only noticed at invoice time.

The gap is worst at month end. The week-shaped question is asked about weeks
that are already over — "what did the week of the 3rd come to?" — and a report
that can only describe the current week answers the least useful version of it.
By the time the month is closed, the current week's report covers a week that
is half gone and no longer relevant to the invoice being prepared.

## 3. Goals

- One command, run once a week, answers all four questions in a single screen
  of output: ≤ 24 lines for a workspace with up to 10 projects worked that
  week.
- Any previous week is reachable with one flag: `grind hwd -w N` produces the
  same report for the complete week N weeks back, with no other syntax to
  learn, no change in output shape, and no second command.
- The hours shown for the week are reconcilable against what `grind invoice`
  would bill: the money figure is computed from `Session.Rounded` × the
  project rate, never from `Session.Duration`.
- The report distinguishes the hours actually worked from the hours that
  would be billed, so the cost of rounding is visible rather than hidden.
- A project that finishes during the week appears in the report with the date
  it finished, from that point forward, with no manual bookkeeping.
- `grind hwd` leaves the workspace byte-for-byte unchanged: no writes, no
  commits, no worktree access beyond reading `.projects.json`.
- No new third-party dependency is introduced.

## 4. Non-Goals

- This is not a full timesheet application. There is no editing, no adding or
  correcting sessions, no per-day rows, and no per-project × per-day matrix.
- This is not an invoice. `hwd` never generates, marks, or sends an invoice;
  it never flips `Session.Invoiced`.
- This does not change `grind wwd`, its columns, its colors, or its ordering.
- This does not add any flag other than `-w/--weeks`. There is no `--since`,
  `--json`, `--no-pager`, or `--all`, and no shorthand `-1` alias for `-w 1`.
- This does not report future weeks. The week count is a non-negative number
  of weeks back, and a negative value is a user error.
- This does not aggregate or compare weeks: no month view, no "last 4 weeks"
  total, no trend, and no side-by-side of two weeks.
- This does not change how sessions are recorded, rounded, or ended, and does
  not change `roundTo` semantics.
- This does not change the `status` values (`""`, `"published"`,
  `"canceled"`), and does not add a "finished" status.
- This does not expose `publishedAt`/`canceledAt` through `grind config`, and
  does not add them to `grind show`.
- This does not backfill timestamps for projects that finished before this
  ships, and does not derive them from git history.
- This does not bump the `.projects.json` `version` field, and requires no
  migration step.
- This does not show task data, deadlines, or last-commit ages.
- This does not page its output, and does not appear in `grind --help`.

## 5. Users

| User | Need | Context |
|------|------|---------|
| The grind author (primary) | See the week at a glance, and know what it is worth | Runs grind daily inside the workspace root; `grind wwd` is the most-used command; bills clients per project at a per-project rate |
| A freelancer preparing to invoice | Know how much of this week is still unbilled | Sends invoices weekly or monthly; needs the split between invoiced and uninvoiced work without opening each project |
| A piped caller (a script, a status line) | Read the same report without escape codes or width games | Redirects stdout to a file or pipes it; expects stable text and exit 0 |

## 6. Functional Requirements

### 6.1 Command surface

- **REQ-001**: `grind hwd` shall be a hidden top-level command
  (`Hidden: true`), absent from `grind --help` and from every help listing,
  exactly like `grind wwd`.
- **REQ-002**: `grind hwd` shall accept no positional arguments. Any positional
  argument shall fail with the argument-count user error (exit 1). Its one
  flag is specified in REQ-040.
- **REQ-003**: `grind hwd` shall be read-only. It shall not write
  `.projects.json`, shall not write any other file, shall not commit, and
  shall not read any project worktree. Reading `.projects.json` and the
  injected clock is the whole of its input.
- **REQ-004**: All output shall go to the command's stdout. Errors shall be
  printed by `main` to stderr, as today.
- **REQ-005**: On a terminal, the project name shall be colored by the palette
  rules in REQ-021; when stdout is not a terminal (a pipe, a file, a test
  buffer) all output shall be plain text with no escape codes.
- **REQ-006**: When stdout is not a terminal the report shall be rendered with
  no width constraint (every column at its natural width, no truncation),
  matching `wwd`'s behaviour.

### 6.2 The reporting window

- **REQ-007**: The reporting window shall begin at 00:00:00 **local** on the
  most recent Sunday, inclusive, and end at the instant the command runs
  ("now"), inclusive. On a Sunday the window therefore begins at that day's
  00:00. This is the window for the default `--weeks 0`; REQ-041 and REQ-042
  define a past week's window.
- **REQ-008**: The window shall be computed from the workspace's local time
  zone and from the clock the command is given, never from `time.Now()` called
  inside the report itself, so a test can pin both.
- **REQ-009**: The header line shall read
  `Week: YYYY-MM-DD (Day) through now`, where the date is the local date of
  the window's first instant and `(Day)` is its three-letter local weekday
  abbreviation (for example `Week: 2026-11-08 (Sun) through now`). A past week
  uses the two-date form in REQ-046. The header shall be followed by one blank
  line.
- **REQ-010**: A session shall belong to the window when its `Start` falls
  within it: at or after the window's first instant, and no later than its
  last. A session that started before the window and ended inside it shall not
  be counted. The current week's last instant is `now`, inclusive; a past
  week's is the following Sunday 00:00, exclusive (REQ-042). This is the same
  rule `grind invoice` uses to group sessions into local days.

### 6.3 The project table

- **REQ-011**: The table shall have exactly four columns, in this order:
  `Project`, `Worked`, `Billable`, `Amount`. The header row shall use those
  exact words, with the `Project` header indented by the two-character marker
  gutter (REQ-020) so it lines up with the names below it.
- **REQ-012**: The table shall contain one row per project that has at least
  one session whose `Start` falls in the window, including a project whose
  only session in the window is still running (that row shows `0.00h`).
  Projects with no session in the window shall not appear.
- **REQ-013**: `Worked` shall be the sum of `Session.Duration` seconds for the
  project's sessions in the window, rendered as hours with exactly two
  decimals followed by `h` (for example `12.33h`). Sessions still running
  (`End == nil`) contribute zero, because their duration is not yet recorded.
- **REQ-014**: `Billable` shall be the sum of `Session.Rounded` seconds for
  the project's sessions in the window, rendered in the same `NN.NNh` form.
  `Rounded` is the value written when a session ends, so a later change to
  `roundTo` cannot alter the report.
- **REQ-015**: `Amount` shall be the project's billable hours multiplied by
  that project's `billing.rate`, rendered as `invoice.Symbol` followed by the
  amount with exactly two decimals (for example `$1950.00`). The format shall
  match `grind show --billing` and the generated invoice file exactly: no
  thousands separator, no currency code, always two decimals.
- **REQ-016**: A project whose `billing.rate` is `0` shall have its hours
  counted normally, contribute `$0.00` to its `Amount`, and contribute nothing
  to the money total. Its name shall be listed in the no-rate warning
  (REQ-025). No rate fallback of any kind shall be applied: the money figure
  is the rate an invoice would actually use.
- **REQ-017**: Rows shall be sorted by `Worked` seconds descending, then by
  project name ascending as a deterministic tie-break. The sort column shall
  be the table's first data column, so the visible order always matches what
  the eye reads first.
- **REQ-018**: A `TOTAL` row shall be printed after the project rows, flush
  with the first column, with the summed `Worked`, `Billable`, and `Amount`.
  The totals shall be computed from the underlying seconds and amounts, never
  by summing the rounded display strings. When there are no project rows there
  shall be no `TOTAL` row (see REQ-030).
- **REQ-019**: Column widths shall be the widest of the header and the cell
  values in that column; cells shall be padded with spaces to the column width
  and joined with two spaces, and color shall be applied after padding so
  escape codes can never shift a column. The table shall be fitted to the
  terminal width using the same rule `wwd` uses: the `Project` column is the
  only shrinkable column, it yields down to a 12-character floor, and only
  when that still does not fit shall columns be dropped from the right edge,
  never dropping below three columns and never dropping the `Project` column.
  On a narrow terminal the `Amount` column is therefore the first to go; the
  money figures remain available in the summary block below the divider.

### 6.4 Row markers and color

- **REQ-020**: Each project name shall be prefixed by a two-character gutter.
  A long-term project (`longTerm: true`) shall show the `★ ` marker `wwd`
  uses; a canceled project (`status: "canceled"`) shall show `✗ `; every other
  project shall show two spaces. A canceled long-term project shall show `✗ `,
  because a canceled project's long-term status is moot. The marker is text,
  not color, so it survives piping.
- **REQ-021**: On a terminal, the project name shall be colored by the same
  rules `wwd` uses, checked in this order: green when the project has a
  session running right now (`End == nil`); otherwise yellow when the project
  has billable work left (any ended session with `Invoiced == false`,
  regardless of week). The yellow rule is deliberately workspace-wide rather
  than week-scoped, so that yellow means the same thing in `hwd` and `wwd`;
  the week-scoped money question is answered by the `Invoiced`/`Unbilled`
  lines instead.
- **REQ-022**: Canceled projects shall be included in the table, the totals,
  and the money block. Their sessions are real work, and the invoicing rule is
  deliberately status-agnostic; the `✗ ` marker is what tells the user the
  project is dead.
- **REQ-023**: The divider between the table and the summary blocks shall be
  `wwd`'s divider verbatim (a leading space, the box-drawing dashes, the
  ` The GrindCLI ` label), dimmed via the palette, boxed in blank lines, and
  shortened to fit the terminal width by the same rule `wwd` uses.

### 6.5 The money block

- **REQ-024**: Below the divider the report shall print a three-line money
  block, with a 10-character label column matching `grind show --billing`, and
  each value rendered as `<hours>h (<symbol><amount>)`:

  ```text
  Billable:  20.50h ($3075.00)
  Invoiced:   8.00h ($1200.00)
  Unbilled:  12.50h ($1875.00)
  ```

  - `Billable` is the week's billable hours and the total money the week's
    work would bill for if every session in the window were invoiced — the
    answer to "how much would I be billing for?".
  - `Invoiced` is the same two figures restricted to sessions in the window
    whose `Invoiced` flag is already true.
  - `Unbilled` is the remainder.
  - `Billable` shall equal the table's `TOTAL` row exactly for both hours and
    money. `Invoiced` + `Unbilled` shall equal `Billable` exactly.
  - For a past week the `Invoiced`/`Unbilled` split reflects the sessions'
    flags as they are now, not as they were then — see REQ-050.
- **REQ-025**: When at least one project row has `billing.rate == 0`, the
  report shall print, after the money block and separated by a blank line, a
  plain-text warning naming those projects in name order:

  ```text
  No billing rate set for: alpha, beta. Their hours are counted; their amount is $0.00.
  ```

  The warning shall be plain text, not colored: the sentence already says what
  is wrong, and a colored warning would compete with the yellow name marker
  for the same meaning. The warning shall only list projects that have a row
  in the table.

### 6.6 The finished block

- **REQ-026**: Below the money block (and the no-rate warning, if any) the
  report shall print a finished block. It shall always be present.
- **REQ-027**: A project shall count as finishing inside the window when its
  `publishedAt` or `canceledAt` falls within the window (REQ-010's rule, with
  a past week's end exclusive). Each such project shall be printed on its own
  indented line, in name order, as: the project name, its status word
  (`published` or `canceled`), the local date it finished (`YYYY-MM-DD`), and
  the project's `Worked` hours for the week in the same `NN.NNh` form as the
  table — or `—` when the project had no work in the window. The block's label
  follows REQ-048. The name column is padded to the longest name in the block,
  the status column to nine characters, and the date column to ten:

  ```text
  Finished this week:
    my-blog     published  2026-11-12  6.50h
    side-quest  canceled   2026-11-13  3.00h
  ```

  A project that finished this week *and* was worked this week appears in both
  the table and this block.
- **REQ-028**: When no project finished inside the window the block shall read
  exactly `Finished this week: none`.
- **REQ-029**: Projects that finished before this feature existed have no
  timestamp and shall never be reported as finishing inside any window. They
  shall be listed once, on a single line after the finished block, in name
  order, with their status in parentheses:

  ```text
  Finished earlier (no date recorded): old-thing (published), older-thing (canceled)
  ```

  This line shall be omitted entirely when there are no such projects. When a
  terminal width is known and the line would exceed it, the line shall be
  truncated with `…` rather than wrapped.

  The line doubles as the worklist for recording those dates by hand: adding
  a `publishedAt` or `canceledAt` to a project's entry removes it from the
  line. It is printed on every report, past weeks included (REQ-051).

### 6.7 Empty states and errors

- **REQ-030**: When no session falls inside the window, the report shall print
  the header, then `No work recorded this week.` (or `No work recorded that
  week.` for a past week — REQ-048), then the divider, then the finished block
  (and the unknown-date line if any). The table and the money block shall be
  omitted: three zero-valued money lines say nothing, and the empty state is
  not an error. Exit code shall be 0.
- **REQ-031**: `grind hwd` outside a workspace shall fail with the existing
  user error `Not in a grind workspace.` and exit 1, before printing anything.
- **REQ-032**: In a workspace created by grind v1, `grind hwd` shall fail with
  the existing user error `This workspace was created by grind v1. Run 'grind
  migrate' to convert it.` and exit 1.
- **REQ-033**: A missing or unreadable `.projects.json` shall surface as the
  existing system error (`read projects file: …`) and exit 2, identical to
  `grind wwd`.
- **REQ-034**: A workspace with no projects at all shall print the empty-state
  output of REQ-030 — including its `none` finished line in the wording
  REQ-048 requires for the week being reported — and exit 0.

### 6.8 Recording when a project finishes

- **REQ-035**: `ProjectEntry` shall gain two optional timestamps:
  `publishedAt` and `canceledAt`, both omitted from the JSON when unset. No
  other field shall change, and `version` shall stay `1`. A `.projects.json`
  written by any earlier slice shall continue to load unchanged, with both
  timestamps unset.
- **REQ-036**: `grind publish` shall set `publishedAt` to the current instant
  from the service's clock, on every publish — including a publish of an
  already-published project, so the timestamp always means "when this project
  last went out".
- **REQ-037**: `grind cancel` shall set `canceledAt` to the current instant
  from the service's clock, on every cancel, with the same "last event wins"
  meaning.
- **REQ-038**: Both timestamps shall be written in the same format as the
  existing `createdAt` and session timestamps (RFC 3339 with the local offset
  the clock produced), and shall be compared as instants, never as formatted
  strings.
- **REQ-039**: Because a publish now always changes `.projects.json`, the
  "nothing changed, skip the commit" path in `Publish` no longer applies to a
  re-publish: the publish commit shall be made. The existing test
  `TestPublishRePublishNoChange` shall be updated to assert the new behaviour
  and to document why it changed; the merge-before-state-change ordering, and
  the `IsPathClean` guard for every other case, shall be left intact.

### 6.9 Previous weeks (`-w`, `--weeks`)

- **REQ-040**: `grind hwd` shall define exactly one flag: `-w, --weeks`, a
  non-negative integer defaulting to `0`, meaning how many complete weeks back
  to report. It shall use cobra's ordinary flag parsing, so `grind hwd -w 1`,
  `grind hwd --weeks 1`, and `grind hwd --weeks=1` are all accepted, and
  `grind hwd --help` prints the command's usage. No other flag shall exist.
- **REQ-041**: `--weeks 0` (the default) shall report the current, partial
  week: from local Sunday 00:00 through `now`, exactly as specified in
  REQ-007 through REQ-009. Passing `-w 0` shall be byte-identical to passing
  no flag at all.
- **REQ-042**: `--weeks N` with `N >= 1` shall report the complete week that
  began `N` weeks before the current week's Sunday. With `weekStart` as
  defined in REQ-007, the window is
  `[weekStart - N×7 days, weekStart - (N-1)×7 days)` in local time: the
  earlier Sunday 00:00 inclusive, the following Sunday 00:00 exclusive. The
  end is exclusive so that a session starting at exactly Sunday midnight
  belongs to the newer week and is never counted in two reports.
- **REQ-043**: Both boundaries shall be computed with calendar arithmetic in
  the local time zone (`AddDate`), never by adding multiples of 24 hours, so a
  week that spans a daylight-saving change still begins and ends at local
  midnight.
- **REQ-044**: A negative, non-numeric, or repeated `-w` value shall fail with
  the user error
  `weeks must be a whole number of weeks back, 0 or more. Got '<value>'.` and
  exit 1, with nothing printed before it.
- **REQ-045**: `grind hwd -1` shall not be supported. It shall fail with
  cobra's own flag-parse error (`unknown shorthand flag: '1' in -1`) plus the
  usage block, exit 1. No alias, error translation, or special case shall be
  added for it.
- **REQ-046**: For a past week the header line shall read
  `Week: YYYY-MM-DD (Day) through YYYY-MM-DD (Day)`, naming the first and last
  local dates of the window — for example
  `Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)`. `through` is inclusive,
  so the header names the last day actually in the week. The current week
  keeps `through now` (REQ-009). These two are the only header forms.
- **REQ-047**: Everything below the header — the table, the `TOTAL` row, the
  divider, the money block, the no-rate warning, and the finished block —
  shall behave identically for a past week. The window and the header line are
  the only differences.
- **REQ-048**: Two user-visible strings say "this week": the empty-state line
  and the finished block's label. For a past week they shall read
  `No work recorded that week.` and `Finished that week:` — or
  `Finished that week: none` when nothing finished. The current week keeps
  `No work recorded this week.`, `Finished this week:`, and
  `Finished this week: none` (REQ-030). These are the only variants.
- **REQ-049**: A week with no work — including one that begins before the
  workspace existed, and a week count far beyond the workspace's history —
  shall print the empty state and exit 0. There is no upper bound on the week
  count, and no warning for a week that predates the workspace.
- **REQ-050**: For a past week, the `Invoiced` and `Unbilled` figures reflect
  the sessions' invoiced flags **as they are now**, not as they were during
  that week, because the model stores no invoicing date. This is deliberate
  and useful — it answers "is there anything from that week still unbilled?"
  — and the report shall not attempt to reconstruct historical invoicing
  state.
- **REQ-051**: The `Finished earlier (no date recorded)` line (REQ-029) shall
  be printed on every report, past weeks included, because it describes the
  workspace's unrecorded history rather than any one week.

## 7. Non-Functional Requirements

- **NFR-001**: `grind hwd` shall use the Go standard library and the existing
  internal packages only. It shall add no module to `go.mod`.
- **NFR-002**: The command shall complete in well under 100 ms on a workspace
  with 50 projects and 5,000 sessions. It shall not shell out to git, and
  shall not spawn any external process.
- **NFR-003**: The report shall be deterministic: the same state file and the
  same instant shall always produce byte-identical output.
- **NFR-004**: Every instant the report depends on shall come from the
  injected clock, so tests pin one instant and assert exact dates and exact
  output strings. No test shall compare against a literal date derived from
  the real clock.
- **NFR-005**: The week boundary shall be computed in the local time zone and
  shall be correct across daylight-saving transitions, for the current week
  and for every `--weeks N` week: a window that begins at local midnight
  begins at local midnight whether or not the clock moved that day, and a week
  that spans a transition still covers exactly seven local dates. The test
  suite shall pass with `TZ=Pacific/Niue` and with a UTC-offset-positive zone.
- **NFR-006**: Output shall be plain text: no tabs, no carriage returns, no
  trailing whitespace on any line, and no escape sequences when stdout is not
  a terminal.
- **NFR-007**: The report shall not depend on any project worktree existing.
  A project whose worktree was removed by publish/cancel cleanup shall still
  appear, because all of its data lives in `.projects.json`.
- **NFR-008**: The read-only guarantee shall be enforced and tested at the
  service boundary, not only in the command, so a test can assert that no
  commit and no write was requested.

## 8. Data Contracts & Interface Shapes

### 8.1 `.projects.json` — additive changes to `ProjectEntry`

Two fields are added. Both are optional and omitted when unset, so existing
files load unchanged and no migration is required.

```jsonc
{
  "version": 1,
  "nextTaskId": 100,
  "projects": {
    "my-blog": {
      "name": "my-blog",
      "type": "blog",
      "idea": "My Blog",
      "billing": { "roundTo": "quarter-hour", "rate": 150 },
      "createdAt": "2026-11-01T09:12:00-06:00",
      "status": "published",

      // NEW — set by `grind publish`, every time, from the service clock.
      // RFC 3339 with the local offset, same shape as createdAt.
      "publishedAt": "2026-11-12T16:40:11-06:00",

      // NEW — set by `grind cancel`, every time, from the service clock.
      "canceledAt": "2026-11-13T11:05:02-06:00",

      "sessions": [
        {
          "start": "2026-11-11T09:00:00-06:00",
          "end": "2026-11-11T12:20:00-06:00",
          "duration": 12000,
          "rounded": 12600,
          "invoiced": false
        }
      ]
    }
  }
}
```

| Field | Type | Set by | Meaning | Absent means |
|---|---|---|---|---|
| `publishedAt` | RFC 3339 timestamp, optional | `grind publish` | When the project last went out | Never published, or published before this feature shipped |
| `canceledAt` | RFC 3339 timestamp, optional | `grind cancel` | When the project last died | Never canceled, or canceled before this feature shipped |

Notes on the contract:

- `version` stays `1`. These are additive optional fields; a reader that does
  not know them ignores them, and a writer that never sets them omits them.
- The timestamps are stored as instants with a local offset, exactly like
  `createdAt` and `Session.Start`. All comparisons ("is this in the window?")
  are instant comparisons; all renderings ("2026-11-12") are local.
- `status` is unchanged and remains the single source of truth for *what*
  state a project is in. The timestamps say only *when* it entered that state,
  and they are never used to derive `status`.

### 8.2 Computed values (the report's internal contract)

The report is a pure function of `.projects.json` and one instant. These are
the values it derives; every rendered figure comes from one of them.

| Value | Definition |
|---|---|
| `weeksBack` | the `--weeks` value: `0` = the current week, `N ≥ 1` = the complete week N weeks back |
| `weekStart` | 00:00:00 local on the most recent Sunday on or before `now`, less `weeksBack × 7` days (calendar arithmetic, not 24-hour multiples) |
| `weekEnd` | `now` when `weeksBack == 0`; otherwise 00:00:00 local on the Sunday that closes the window, **exclusive** |
| `inWindow(t)` | `t >= weekStart && t <= weekEnd` for the current week; `t >= weekStart && t < weekEnd` for a past week |
| `workedSeconds[project]` | Σ `Session.Duration` where `inWindow(Start)` |
| `billableSeconds[project]` | Σ `Session.Rounded` where `inWindow(Start)` |
| `amount[project]` | `billableSeconds / 3600 × entry.Billing.Rate` |
| `invoicedSeconds[project]` | Σ `Session.Rounded` where `inWindow(Start) && Invoiced` |
| `invoicedAmount[project]` | `invoicedSeconds / 3600 × entry.Billing.Rate` |
| `runningNow[project]` | any session with `End == nil` |
| `hasUnbilled[project]` | any session with `End != nil && !Invoiced` (any week) |
| `finishedInWindow[project]` | `inWindow(PublishedAt)` or `inWindow(CanceledAt)` |

### 8.3 Rendered output contract

Canonical example — Wednesday 2026-11-11, week starting Sunday 2026-11-08.
Three projects worked; `side-quest` was canceled on the 13th; `my-blog` was
published on the 12th; `old-thing` was published before this feature existed.
Column widths are derived by REQ-019 (widest of header and cells, two-space
join), so the spacing below is the shape, and REQ-019 is the rule.

```text
Week: 2026-11-08 (Sun) through now

  Project       Worked  Billable  Amount
★ my-blog       12.33h  13.00h    $1950.00
✗ side-quest     3.00h   3.00h     $450.00
  leenix         4.00h   4.50h     $675.00
TOTAL           19.33h  20.50h    $3075.00

 ─────────────────────────── The GrindCLI ──────────────────────────────

Billable:  20.50h ($3075.00)
Invoiced:   8.00h ($1200.00)
Unbilled:  12.50h ($1875.00)

Finished this week:
  my-blog     published  2026-11-12  6.50h
  side-quest  canceled   2026-11-13  3.00h

Finished earlier (no date recorded): old-thing (published)
```

Past-week example — `grind hwd -w 1` on a workspace with work only in the
previous week (illustrating shape, not the same numbers as above). The window
is the complete week before the current one, so the header names both end
dates, the end is exclusive, and the two "this week" strings become "that
week":

```text
Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)

  Project      Worked  Billable  Amount
  leenix        6.00h   6.00h     $900.00
TOTAL           6.00h   6.00h     $900.00

 ─────────────────────────── The GrindCLI ──────────────────────────────

Billable:   6.00h ($900.00)
Invoiced:   6.00h ($900.00)
Unbilled:   0.00h ($0.00)

Finished that week: none

Finished earlier (no date recorded): old-thing (published)
```

Formatting rules, stated once:

| Element | Format |
|---|---|
| Header, current week | `Week: YYYY-MM-DD (Day) through now` |
| Header, past week | `Week: YYYY-MM-DD (Day) through YYYY-MM-DD (Day)` — first and last local dates of the window |
| Empty state, current week | `No work recorded this week.` |
| Empty state, past week | `No work recorded that week.` |
| Finished block label | `Finished this week:` / `Finished that week:`, and `… : none` when nothing finished |
| Hours | `%.2f` + `h` — `12.33h`, `0.00h` — matching `projects.FormatHours` |
| Money | `$` + `%.2f` — `$1950.00`, `$0.00` — matching `show --billing` and the invoice file |
| Dates | local `YYYY-MM-DD` |
| Empty value | `—` (em dash), matching `show` and the `new project` summary |
| Marker gutter | `★ ` long-term, `✗ ` canceled, `  ` otherwise |
| Column separator | two spaces; color applied after padding |

Empty-week example, current week (no session in the window, nothing finished):

```text
Week: 2026-11-08 (Sun) through now

No work recorded this week.

 ─────────────────────────── The GrindCLI ──────────────────────────────

Finished this week: none
```

The same for a past week — `grind hwd -w 3` — differs only in the header and
the two "that week" strings:

```text
Week: 2026-10-18 (Sun) through 2026-10-24 (Sat)

No work recorded that week.

 ─────────────────────────── The GrindCLI ──────────────────────────────

Finished that week: none
```

### 8.4 Exit contract

| Condition | Output | Exit |
|---|---|---|
| Normal run, work in the window | Header, table, totals, divider, money block, finished block | 0 |
| Normal run, no work in the window | Header, `No work recorded this week.` (or `… that week.`), divider, finished block | 0 |
| Workspace with no projects | Same as no work in the window | 0 |
| Past week predating the workspace | Same as no work in the window, with `that week` wording | 0 |
| Positional argument given | Argument-count user error | 1 |
| `-w` negative, non-numeric, or repeated | `weeks must be a whole number of weeks back, 0 or more. Got '<value>'.` | 1 |
| `-1` (unsupported shorthand) | Cobra's `unknown shorthand flag: '1' in -1`, plus the usage block | 1 |
| Not in a workspace | `Not in a grind workspace.` | 1 |
| v1 workspace | `This workspace was created by grind v1. Run 'grind migrate' to convert it.` | 1 |
| `.projects.json` missing or unreadable | Existing system error (`read projects file: …`) | 2 |
| Unparseable `.projects.json` | Existing system error (`parse projects file: …`) | 2 |

## 9. Acceptance Criteria

- **AC-001**: Given a pinned clock at Wednesday 2026-11-11 14:05 local, When
  `grind hwd` runs, Then the first line is exactly
  `Week: 2026-11-08 (Sun) through now`.
- **AC-002**: Given a pinned clock at Sunday 2026-11-15 14:05 local, When
  `grind hwd` runs, Then the first line is exactly
  `Week: 2026-11-15 (Sun) through now`, and only sessions started on or after
  that day's midnight are counted.
- **AC-003**: Given a project with a session from 2026-11-07 23:00 to
  2026-11-08 02:00 (local) and another from 2026-11-09 09:00 to 2026-11-09
  12:00, When `grind hwd` runs on 2026-11-11, Then only the second session is
  counted and the project's `Worked` is `3.00h`.
- **AC-004**: Given a session of 4h20m (`Duration` 15600, `Rounded` 16200 at
  quarter-hour rounding), When `grind hwd` runs in the week that session
  started, Then its row shows `4.33h` worked and `4.50h` billable.
- **AC-005**: Given a project with rate 150 and 13.00h billable this week,
  When `grind hwd` runs, Then the row's `Amount` is `$1950.00` and the
  `Billable` money line is `$1950.00`.
- **AC-006**: Given two projects worked this week with 4.00h and 12.00h worked
  respectively, When `grind hwd` runs, Then the 12.00h project's row appears
  above the 4.00h project's row.
- **AC-007**: Given two projects with identical worked seconds, When
  `grind hwd` runs, Then the two rows appear in ascending name order.
- **AC-008**: Given a week with work, When `grind hwd` runs, Then the `TOTAL`
  row's `Worked`, `Billable`, and `Amount` equal the sums of the columns above
  it, and the `Billable` line's hours and money equal the `TOTAL` row's
  `Billable` and `Amount` exactly.
- **AC-009**: Given a project with 4.00h billable, of which 2.00h is already
  invoiced, When `grind hwd` runs, Then `Billable` is `4.00h`, `Invoiced` is
  `2.00h`, and `Unbilled` is `2.00h`, and `Invoiced` + `Unbilled` equals
  `Billable` in both hours and money.
- **AC-010**: Given a project with `billing.rate` 0 and 3.00h worked this
  week, When `grind hwd` runs, Then the row shows `3.00h` worked, `$0.00`
  amount, contributes nothing to the money total, and the line
  `No billing rate set for: <name>. Their hours are counted; their amount is $0.00.`
  is printed after the money block.
- **AC-011**: Given a long-term project worked this week, When `grind hwd`
  runs, Then its name is prefixed with `★ `; given a canceled project worked
  this week, Then its name is prefixed with `✗ `; given an ordinary project,
  Then its name is prefixed with two spaces.
- **AC-012**: Given a project with a session that has `End == nil` and started
  this week, When `grind hwd` runs, Then the project has a row showing
  `0.00h` worked and `0.00h` billable, its name is green on a terminal, and
  its hours are absent from every total.
- **AC-013**: Given a project that is published and has unbilled sessions,
  When `grind hwd` runs on a terminal, Then its name is yellow; given a
  project with an active session and unbilled sessions, Then its name is green
  (green wins).
- **AC-014**: Given `grind publish my-blog` runs at a pinned instant and then
  `grind hwd` runs within the same week, Then the finished block contains a
  `my-blog published <date> <hours>` line, and `.projects.json` contains
  `publishedAt` equal to that instant.
- **AC-015**: Given `grind cancel side-quest` runs at a pinned instant and
  then `grind hwd` runs within the same week, Then the finished block contains
  a `side-quest canceled <date> <hours>` line, and the project still appears in
  the hours table with its `✗ ` marker and its hours included in the totals.
- **AC-016**: Given a project published this week with no work this week, When
  `grind hwd` runs, Then its finished line shows `—` in place of hours.
- **AC-017**: Given a project published in a previous week, When `grind hwd`
  runs, Then it does not appear in the finished block.
- **AC-018**: Given a `.projects.json` in which one project has
  `status: "published"` and no `publishedAt`, When `grind hwd` runs, Then the
  line `Finished earlier (no date recorded): <name> (published)` is printed
  after the finished block, and the project is not listed as finishing this
  week.
- **AC-019**: Given a workspace with projects but no session in the window,
  When `grind hwd` runs, Then the output is the header, a blank line,
  `No work recorded this week.`, a blank line, the divider, a blank line, and
  `Finished this week: none` — with no table and no money block — and the exit
  code is 0.
- **AC-020**: Given a workspace with no projects, When `grind hwd` runs, Then
  the output is the empty-week output and the exit code is 0.
- **AC-021**: Given a workspace with projects and sessions, When
  `grind hwd` runs, Then `git -C .main status --porcelain` is empty
  afterwards and no commit was recorded by the fake git.
- **AC-022**: Given `grind hwd` is run, Then the string `hwd` does not appear
  in the output of `grind --help`.
- **AC-023**: Given `grind hwd extra` is run, Then a user error is printed and
  the exit code is 1.
- **AC-024**: Given a directory that is not a grind workspace, When
  `grind hwd` runs, Then `Not in a grind workspace.` is printed and the exit
  code is 1.
- **AC-025**: Given a v1 workspace (bare repo plus `grind/`, no `.main/`),
  When `grind hwd` runs, Then the migrate hint is printed and the exit code
  is 1.
- **AC-026**: Given a workspace whose `.projects.json` has been deleted, When
  `grind hwd` runs, Then the existing `read projects file` system error is
  printed and the exit code is 2.
- **AC-027**: Given a project published with worktree cleanup (worktree
  removed, branch deleted), When `grind hwd` runs in the week it was
  published, Then its row and its finished line are both present.
- **AC-028**: Given a terminal 40 columns wide, When `grind hwd` renders the
  table, Then the `Amount` column is dropped, the `Project` column is
  truncated rather than wrapped, and no line exceeds 40 columns.
- **AC-029**: Given stdout is a buffer or a pipe, When `grind hwd` runs, Then
  the output contains no escape sequences, no line exceeds the natural table
  width, and no column is truncated.
- **AC-030**: Given a project published twice at two pinned instants in
  different weeks, When `grind hwd` runs in the week of the second publish,
  Then the finished line shows the second instant's date and
  `publishedAt` equals the second instant.
- **AC-031**: Given `TZ=Pacific/Niue` (UTC-11), When the suite runs with a
  clock pinned at Sunday 2026-11-15 00:30 local, Then the window starts at
  `2026-11-15` local and the header reads `Week: 2026-11-15 (Sun) through now`.
- **AC-032**: Given a `.projects.json` written before this feature (no
  `publishedAt`, no `canceledAt`), When it is read and written back by any
  command, Then the file still parses, `version` is still `1`, and neither new
  key is written for projects that never published or canceled.
- **AC-033**: Given the existing test `TestPublishRePublishNoChange` is
  updated, When `grind publish` runs on an already-published project, Then
  `.projects.json` is written with a new `publishedAt` and the publish commit
  is recorded.
- **AC-034**: Given a pinned clock at Wednesday 2026-11-11 14:05 local, When
  `grind hwd -w 1` runs, Then the first line is exactly
  `Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)`, and only sessions whose
  `Start` falls in that range are counted.
- **AC-035**: Given a session that starts at exactly Sunday 2026-11-08 00:00
  local and a clock pinned at 2026-11-11, When `grind hwd` and `grind hwd -w 1`
  both run, Then the session is counted in the current-week report and not in
  the `-w 1` report — it appears in exactly one of the two.
- **AC-036**: Given any workspace and pinned clock, When `grind hwd -w 0` runs,
  Then its output is byte-identical to `grind hwd` with no flag.
- **AC-037**: Given `grind hwd --weeks=2` and `grind hwd -w 2`, Then both
  produce byte-identical output.
- **AC-038**: Given a workspace whose earliest session is three weeks old, When
  `grind hwd -w 52` runs, Then the output is the empty state with
  `No work recorded that week.` and `Finished that week: none`, and the exit
  code is 0.
- **AC-039**: Given `grind hwd -w -1` runs, Then
  `weeks must be a whole number of weeks back, 0 or more. Got '-1'.` is
  printed, the exit code is 1, and no report is printed.
- **AC-040**: Given `grind hwd -w abc` runs, Then the same user error is
  printed with `Got 'abc'.` and the exit code is 1.
- **AC-041**: Given `grind hwd -1` runs, Then cobra's
  `unknown shorthand flag: '1' in -1` error and the usage block are printed and
  the exit code is 1.
- **AC-042**: Given a project published during week `-1` and nothing published
  during week `-2`, When `grind hwd -w 1` and `grind hwd -w 2` run, Then the
  project's finished line appears in the first report and not in the second.
- **AC-043**: Given a session worked during week `-1` that was invoiced after
  that week ended, When `grind hwd -w 1` runs, Then its hours appear under
  `Invoiced` and not under `Unbilled`.
- **AC-044**: Given a project with `status: "published"` and no `publishedAt`,
  When `grind hwd -w 1` runs, Then the
  `Finished earlier (no date recorded)` line is still printed.
- **AC-045**: Given a clock pinned at Wednesday 2026-11-11 in a zone that
  observes US daylight saving (which ended Sunday 2026-11-01), When
  `grind hwd -w 2` runs, Then the header reads
  `Week: 2026-10-25 (Sun) through 2026-10-31 (Sat)`, a session starting
  2026-10-25 00:30 local is counted, and one starting 2026-10-24 23:30 local
  is not.
- **AC-046**: Given a past week with work, When `grind hwd -w 1` runs, Then the
  table, `TOTAL` row, divider, money block, and finished block are all present
  and formatted exactly as in a current-week report.

## 10. Open Questions

None. Every decision raised during the design interview is resolved in
Section 6: the window is the most recent Sunday 00:00 to now; sessions are
attributed by start; the money figure is the aspirational value of the week's
work with an invoiced/uninvoiced split beneath it; finished projects are
tracked with two new timestamps stamped on every publish and cancel; hours are
shown both raw and billable; canceled projects are included and marked;
zero-rate projects count their hours and warn by name; rows sort by worked
time descending; markers, divider and palette are reused from `wwd`; the
finished block always prints; an empty week is a friendly line at exit 0; and
previous weeks are reached with a single `-w/--weeks` flag — a complete,
end-exclusive week whose header names both end dates, with `-1` deliberately
not accepted as a shorthand, and with `Invoiced`/`Unbilled` reporting the
flags as they stand now rather than as they stood then.

## 11. Out of Scope / Future Considerations

- Future: `--json` output for scripting. Considered and deferred so the flag
  surface stays at `-w/--weeks` alone.
- Future: aggregating weeks — a month view, a `--since` date range, or a
  trend across weeks. The lookback is deliberately a *count of weeks* rather
  than a date range, so there is exactly one way to ask for a past week.
- Future: a per-day breakdown (day rows, or a project × day matrix) for a
  genuinely timesheet-shaped report. Deferred because nine columns cannot
  survive a narrow terminal.
- Future: rate history. `Amount` uses the project's *current* rate, so
  changing a rate retroactively changes what past weeks are reported as worth.
  The invoice file is unaffected — it snapshots the rate at generation time —
  but `hwd` cannot, because no historical rate is stored. A `rateHistory`
  list on the project entry would fix this.
- Future: recording *when* a session was invoiced, which would let a past
  week report its invoicing state as it was then rather than as it is now
  (REQ-050). The invoice directory names are timestamps, so the data is
  recoverable for v2 invoices but not for migrated v1 ones.
- Future: backfilling `publishedAt` for projects that finished before this
  feature shipped, from the merge commit publish created. Deliberately not
  done: `hwd` must stay read-only, a guess is worse than an honest "no date
  recorded", and the unknown-date line exists precisely so those dates can be
  added by hand.
- Future: exposing `publishedAt`/`canceledAt` through `grind config` (the
  `status` key is already listed read-only there) and through `grind show`.
- Future: aligning `wwd` with this report — a shared week scope, or a Billed
  column returning now that `Invoiced` exists. Slice 6 dropped the v1 Billed
  column because the flag did not exist yet; it does now.
- Future: a pager for long reports, if `hwd` ever grows the sections this PRD
  deliberately leaves out.
- Known consequence, documented rather than fixed: a session contributes to
  the week containing its `Start`, and only once it has ended. A session left
  running across a week boundary therefore contributes nothing to either week
  until it ends, at which point its whole rounded duration lands in the week it
  started. This is the invoice's day-grouping rule applied to weeks, and it is
  deliberate: no other view of the data would agree with a different rule.
- Known consequence, documented rather than fixed: for a past week, the
  `Invoiced`/`Unbilled` split reflects today's flags, not that week's
  (REQ-050). This is the useful reading — it answers "what is still unbilled
  from that week?" — but it means a `-w 1` report run today and the same
  report re-run after an invoice will differ.
