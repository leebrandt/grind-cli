# Spec 13: `hwd` — hours-worked weekly report

**Slice 13 of the Go rewrite of the grind CLI.** This slice delivers a new
hidden command, `grind hwd`, that prints a timesheet for one week: the hours
worked per project, what that work would bill for (and how much of it is
already invoiced), and which projects finished. `grind hwd -w N` prints the
same report for a complete previous week, so a month-end billing run can walk
backwards week by week.

The requirements live in `specs/prd-03/prd-03.md`. This spec is the *how*:
files, signatures, exact strings, ordering, and tests. Where the two disagree,
the PRD wins and this spec is wrong — except where this spec explicitly lists a
deviation in "Deviations from the PRD", each of which was agreed with the user.

`hwd` is read-only, exactly like its sibling `grind wwd`: it never writes,
never commits, and never touches a project worktree. The only new state is two
optional timestamps on `ProjectEntry`, written by the existing `publish` and
`cancel` verbs.

Everything else — `--json`, a month view, a per-day breakdown, rate history,
backfilling old finish dates, paging — is **not** in this slice. See
"Extension points".

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

1. **A pure report package, no service.** `internal/report` is a package of
   pure functions and plain data. It has no `Service`, no `Clock` field and no
   `git.Git` dependency: `Build` takes the instant as a parameter, the way
   `invoice.Generate` takes `now`. "A function that can simply *take* the
   instant does" (AGENTS.md). The CLI reads the clock exactly once and hands
   the instant down.
2. **One clock read per run.** `newHwdCmd(clk clock.Clock)` captures the clock;
   `RunE` calls `clk.Now()` a single time and passes that instant to `Build`.
   Nothing inside `report` reads the wall clock, so a test pins one instant and
   asserts exact dates.
3. **The current week is `[Sunday 00:00 local, now]`, inclusive of `now`.** A
   past week `N ≥ 1` is `[Sunday − N×7d, Sunday − (N−1)×7d)`, **end-exclusive**,
   so a session starting at exactly Sunday midnight belongs to exactly one
   report. Both boundaries use `AddDate`, never multiples of 24 hours, so a
   week that spans a daylight-saving change still begins and ends at local
   midnight.
4. **A session belongs to the week its `Start` falls in**, the same rule
   `grind invoice` uses to group sessions into local days. A session that began
   before the window and ended inside it is not counted.
5. **`-w/--weeks` is a plain string flag parsed in `RunE`.** An `int` flag
   would make pflag reject `-w abc` with pflag's own wording and exit 99;
   parsing the string ourselves is what lets every bad value produce the same
   one user error, exit 1 (decision 6). The flag is the *only* flag on `hwd`.
6. **`-w` is generic and forgiving: any whole number of weeks back, sign
   ignored.** `-w 3` and `-w -3` both mean three weeks back; `-w 0` (the
   default) is byte-identical to no flag. Anything that is not a whole number —
   `2.5`, `abc`, an empty value — fails with one message that says how to pass
   a good one. A negative value is not an error: it is a week count, and the
   sign carries no meaning here.
7. **Repeated `-w` is allowed; the last one wins.** That is pflag's default
   `Set` behaviour and the least surprising thing to a shell user who typed
   `-w 1 -w 2`. (The PRD asked for "repeated" to be an error; see
   "Deviations".)
8. **`-1` is not a shorthand for `-w 1` and never will be.** Cobra rejects it
   as an unknown shorthand before `RunE`, so `grind hwd -1` prints cobra's
   `unknown shorthand flag: '1' in -1` and exits non-zero. No alias, no
   translation.
9. **A positional argument is a user error, exit 1.** `cobra.NoArgs` would exit
   99, which contradicts AC-023, so `hwd` uses a small custom validator that
   returns `grinderr.NewUser`.
10. **Bad `-w` is reported before the workspace is resolved.** `RunE` parses
    the flag first, then calls `workspace.Require`. A usage error is about how
    the command was typed, not about where it was typed.
11. **Numbers are right-aligned; the project name is not.** The PRD's examples
    line the money figures and the hours up on their right edges (the decimal
    points), which is what makes a column of figures scannable. `Project` stays
    left-aligned, and every header stays left-aligned at its column's left
    edge. This is the one place the table needs machinery `wwd` does not have:
    `renderRowAligned`.
12. **The money block's `Billable` line is the table's `TOTAL`, structurally.**
    Both render `Report.Total.BillableSeconds` and `Report.Total.Amount`, so
    they cannot drift; `Unbilled` is `Billable − Invoiced` by subtraction, so
    `Invoiced + Unbilled == Billable` exactly, with no rounding gap.
13. **A zero-rate project is counted, not hidden, and named in plain text.**
    Its hours land in `Worked`/`Billable`; its `Amount` is `$0.00`; its name is
    listed once after the money block. No rate fallback of any kind is applied,
    because the report shows the number an invoice would actually use.
14. **Color is chosen by a named, testable function.** `hwdNameColor(row)`
    returns a small enum, and the command maps it to a palette call. A test
    asserts the choice without needing a terminal, because `color.Palette` is
    disabled for a buffer and its `enabled` field is unexported.
15. **The finished block always prints.** A week where nothing finished says so
    (`Finished this week: none`), so the section is never a mystery absence.
16. **A project is "finished" by the timestamp that matches its *current*
    status.** `published` → `publishedAt`, `canceled` → `canceledAt`. The
    status word and the date on the finished line therefore always describe the
    same event. A finished project with no timestamp is not "finished earlier";
    it is listed once as unrecorded (decision 17).
17. **The `Finished earlier (no date recorded)` line prints on every report.**
    It describes the workspace's unrecorded history, not any one week, and it
    doubles as the worklist for adding those dates by hand.
18. **Timestamps are stored UTC, like `createdAt`.** The PRD's parenthetical
    says "the local offset the clock produced"; every other timestamp in the
    file is `.UTC().Truncate(time.Second)`, and two spellings of the same
    instant in one file is a bug waiting to happen. Rendering is still local,
    and all comparisons are instant comparisons. See "Deviations".
19. **Standard library only.** No new module in `go.mod`. `hwd` shells out to
    nothing.
20. **Docs and version move with the slice.** README's `HIDDEN COMMANDS` table
    gains a `grind hwd` row; AGENTS.md's slice table gains slice 13; `version`
    in `internal/cli/root.go` goes from `0.99.0` to `0.99.1`.

## Workspace layout

Unchanged. No new directories, no new files in the workspace, no new state
beyond the two `ProjectEntry` fields.

## Data model

### `.projects.json` — two additive optional fields

`ProjectEntry` (`internal/config/config.go`) gains:

```go
	// PublishedAt is when the project last went out (Status "published").
	// It records WHEN the project entered that state; Status is still the
	// single source of truth for WHICH state. Nil for a project that was
	// never published, or published before this field existed.
	PublishedAt *time.Time `json:"publishedAt,omitempty"`

	// CanceledAt is when the project last died (Status "canceled"), with the
	// same "last event wins" meaning.
	CanceledAt *time.Time `json:"canceledAt,omitempty"`
```

- Pointers with `omitempty`, exactly like `Task.CompletedAt`: an unset
  timestamp is absent from the JSON, so a file written by any earlier slice
  loads unchanged and is written back unchanged.
- `version` stays `1`. No migration step exists or is needed.
- They are set by the lifecycle verbs, never by `grind config` and never
  rendered by `grind show` (PRD REQ-035, §4).
- Both are written as instants (see decision 18); all window checks compare
  instants, and the rendered `YYYY-MM-DD` is the local date.

### `internal/projects` — stamp the timestamp on every publish and cancel

`Publish` (in `internal/projects/projects.go`) sets the field on the same entry
it already mutates, using the same clock convention as `CreatedAt`:

```go
	entry.Status = "published"
	now := s.Clock.Now().UTC().Truncate(time.Second)
	// Every publish re-stamps, even a re-publish: the timestamp means "when
	// this project last went out", not "when it first did".
	entry.PublishedAt = &now
	if err := writeEntry(ws, name, *entry); err != nil {
		return err
	}
```

`Cancel` does the same with `entry.CanceledAt`. Nothing else in either function
changes: the merge-before-state-change ordering, the `IsPathClean` guard in
`Publish`, and the commit-before-cleanup ordering in `Cancel` all stay exactly
as they are.

Because `Publish` now always writes a new timestamp, a re-publish always
changes `.projects.json`, so the `IsPathClean` guard's "skip the commit" branch
is no longer reached for a re-publish — but the guard stays, because it is
still the right test for a *no-op* write and removing it would make the commit
unconditional. The existing test's comment is updated to say so.

## Commands in this slice

### `grind hwd`

```
Use:    "hwd"
Short:  "Hours-worked weekly report (hidden)"
Hidden: true
Args:   noHwdArgs
```

Its only flag:

```go
cmd.Flags().StringVarP(&weeks, "weeks", "w", "0", "weeks back to report (0 = this week)")
```

`RunE`, in order:

1. `weeksBack, err := parseWeeks(weeks)` — a bad value is a user error
   (exit 1) returned **before** anything else (decision 10).
2. `ws, err := workspace.Require(".")` — the existing user errors:
   `Not in a grind workspace.` (exit 1) and, for a v1 workspace,
   `This workspace was created by grind v1. Run 'grind migrate' to convert it.`
   (exit 1).
3. `rep, err := report.Build(ws, report.Options{WeeksBack: weeksBack}, clk.Now())`
   — reads `.projects.json`; a missing/unreadable file is the existing system
   error (`read projects file: …`, exit 2), identical to `wwd`.
4. `out := cmd.OutOrStdout()`, `palette := color.New(out)`,
   `width, _, _ := terminal.Size(out)` — 0 means "no constraint", which is what
   a pipe, a file or a test buffer gets.
5. `renderReport(out, rep, palette, width)`.

All output goes to stdout; errors are printed by `main` to stderr, as today.
`grind hwd --help` still works (cobra prints help for a hidden command) and
shows the single `-w, --weeks` flag.

#### Argument validation

```go
// noHwdArgs rejects every positional argument. It is not cobra.NoArgs because
// NoArgs returns a bare error, which main maps to exit 99; the PRD wants a
// user error and exit 1, and grinderr.NewUser is what produces that.
func noHwdArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return grinderr.NewUser("hwd takes no arguments")
	}
	return nil
}
```

#### `-w` parsing

```go
// parseWeeks turns the -w/--weeks value into a count of weeks back. Any whole
// number is accepted; the sign is ignored, because "weeks back" is already a
// direction and -3 and 3 name the same week. Anything that is not a whole
// number gets one error that shows how to pass a good one — the value is
// echoed so the user can see which argument was wrong.
func parseWeeks(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, grinderr.NewUser(fmt.Sprintf(
			"Invalid value for -w/--weeks: %q. Pass a whole number of weeks to look back — for example: grind hwd -w 1",
			raw))
	}
	if n < 0 {
		n = -n
	}
	return n, nil
}
```

`strconv.Atoi` accepts an optional `+`/`-` and digits only, so `2.5`, `abc`,
`1h`, `""` and `" 1"` all take the error branch. `Atoi` on a value larger than
an `int` also errors, which is the right answer: a week count that overflows
`int` is not a week count.

`parseWeeks` is not exported and lives beside the command. It is a pure
function of a string, so its table test needs no workspace.

#### Exit contract for flag errors

| Input | Where it fails | Message | Exit |
|---|---|---|---|
| `hwd extra` | `noHwdArgs`, before `RunE` | `hwd takes no arguments` | 1 |
| `hwd -w abc`, `-w 2.5`, `-w ''` | `parseWeeks` in `RunE` | `Invalid value for -w/--weeks: "abc". Pass a whole number of weeks to look back — for example: grind hwd -w 1` | 1 |
| `hwd -w -1` | nothing — legal | (reports the week 1 week back) | 0 |
| `hwd -1` | pflag, before `RunE` | `unknown shorthand flag: '1' in -1` | 99 |
| `hwd -w` (no value) | pflag, before `RunE` | `flag needs an argument: 'w' in -w` | 99 |

The last two are cobra/pflag's own errors. `main` maps an untyped error to
exit 99, and `SilenceUsage: true` (already set on the root command) suppresses
the usage block. This is a deliberate deviation from AC-041's "exit 1 plus the
usage block" — see "Deviations".

## The reporting window

`internal/report` computes the window from `now` and `weeksBack`:

```go
// Window returns the half-open-or-closed reporting window. For the current
// week (weeksBack <= 0) it is [Sunday 00:00 local, now], with now included.
// For a past week it is [start, start+7d), with the end EXCLUSIVE so a session
// starting at exactly Sunday midnight belongs to the newer week and is never
// counted twice.
//
// past reports which shape it is; it is what the header and the two "this
// week"/"that week" strings switch on.
func Window(now time.Time, weeksBack int) (start, end time.Time, past bool) {
	// Midnight today in the workspace's local zone, then back up to the most
	// recent Sunday. time.Weekday() has Sunday == 0, so subtracting the
	// weekday lands on Sunday without a lookup table.
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start = day.AddDate(0, 0, -int(day.Weekday()))
	if weeksBack <= 0 {
		return start, now, false
	}
	// AddDate, not 24-hour multiples: a week that spans a daylight-saving
	// change still begins and ends at local midnight.
	start = start.AddDate(0, 0, -7*weeksBack)
	return start, start.AddDate(0, 0, 7), true
}
```

```go
// InWindow reports whether t falls in the window. The current week includes
// its last instant (now); a past week excludes it (the following Sunday
// midnight), which is what keeps the two reports disjoint.
func InWindow(t, start, end time.Time, past bool) bool {
	if t.Before(start) {
		return false
	}
	if past {
		return t.Before(end)
	}
	return !t.After(end)
}
```

Worked examples (all local):

| `now` | `weeksBack` | `start` | `end` | `past` |
|---|---|---|---|---|
| Wed 2026-11-11 14:05 | 0 | 2026-11-08 00:00 | `now` | false |
| Sun 2026-11-15 14:05 | 0 | 2026-11-15 00:00 | `now` | false |
| Wed 2026-11-11 14:05 | 1 | 2026-11-01 00:00 | 2026-11-08 00:00 | true |
| Wed 2026-11-11 14:05 | 2 | 2026-10-25 00:00 | 2026-11-01 00:00 | true |
| Wed 2026-11-11 14:05 | 3 | 2026-10-18 00:00 | 2026-10-25 00:00 | true |
| Sun 2026-11-15 14:05 | 1 | 2026-11-08 00:00 | 2026-11-15 00:00 | true |

`weeksBack <= 0` is treated as the current week. `parseWeeks` already
normalises a negative to positive, so the `<= 0` guard is belt-and-braces for a
caller that builds `Options` directly; a test pins it.

### Header line

```go
// HeaderLine is the report's first line. The current week ends "through now"
// because it is still running; a past week names both end dates, and the last
// named day is the last day actually in the week.
func HeaderLine(start, end time.Time, past bool) string {
	if !past {
		return "Week: " + start.Format("2006-01-02 (Mon)") + " through now"
	}
	last := end.AddDate(0, 0, -1)
	return "Week: " + start.Format("2006-01-02 (Mon)") + " through " + last.Format("2006-01-02 (Mon)")
}
```

- Current: `Week: 2026-11-08 (Sun) through now`.
- Past: `Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)`.

`AddDate(0, 0, -1)` on the exclusive end gives the last day *in* the week,
which is correct across a DST change because it is calendar arithmetic.

## The report

```go
// Package report computes the `grind hwd` weekly report.
//
// It is a pure package: a report is a function of .projects.json and one
// instant. There is no Service and no Clock, because the instant is passed in
// — the same shape as invoice.Generate. That keeps the week-boundary,
// ordering and money rules testable without arranging a filesystem or a clock.
//
// The package is read-only by construction: it has no git dependency, so
// there is no code path here that could write or commit.
package report
```

### Types

```go
// Options selects which week to report. WeeksBack 0 is the current week;
// N >= 1 is the complete week N weeks back.
type Options struct {
	WeeksBack int
}

// Row is one project's line in the hours table.
type Row struct {
	Name            string
	Status          string // "" | "published" | "canceled"
	LongTerm        bool
	WorkedSeconds   int64   // Σ Session.Duration in the window
	BillableSeconds int64   // Σ Session.Rounded in the window
	Amount          float64 // billableSeconds / 3600 × rate
	RunningNow      bool    // any session with End == nil (any week)
	HasUnbilled     bool    // any ended, un-invoiced session (any week)
}

// Totals is the table's TOTAL row, in raw units so nothing is summed from
// display strings.
type Totals struct {
	WorkedSeconds   int64
	BillableSeconds int64
	Amount          float64
}

// Money is the invoiced/un-invoiced split of the week. Billable is not stored
// here: it is Report.Total, so the money block's Billable line and the table's
// TOTAL row are the same numbers by construction. Unbilled is Billable minus
// Invoiced, computed once, so the three lines always add up.
type Money struct {
	InvoicedSeconds int64
	InvoicedAmount  float64
	UnbilledSeconds int64
	UnbilledAmount  float64
}

// Finished is one project that finished inside the window.
type Finished struct {
	Name          string
	Status        string    // "published" | "canceled"
	Date          time.Time // the timestamp, in the window's local zone
	WorkedSeconds int64     // the project's hours for the week
	HasWork       bool      // whether the project has a row in the table
}

// FinishedEarlier is a project that finished but has no recorded timestamp.
type FinishedEarlier struct {
	Name   string
	Status string
}

// Report is everything one run of the command renders.
type Report struct {
	Start time.Time
	End   time.Time
	Past  bool

	Rows     []Row
	Total    Totals
	Money    Money
	NoRate   []string // names of zero-rate projects with a row, sorted
	Finished []Finished
	Earlier  []FinishedEarlier
}
```

### `Build`

```go
// Build reads .projects.json and computes the report for opts at now. now is
// the single instant every window decision uses; nothing here reads a clock.
func Build(ws *workspace.Workspace, opts Options, now time.Time) (*Report, error)
```

Two passes over `projects.Projects`, then a sort of each slice:

**Pass 1 — rows, totals, money, zero-rate names.** For each project, walk its
sessions once, accumulating into a small local struct:

- `runningNow`: set by any session with `End == nil`, **before** the window
  check — it is a workspace-wide "is this project active right now" signal
  (REQ-021).
- `hasUnbilled`: set by any ended session with `!Invoiced`, also before the
  window check — yellow means the same thing in `hwd` and `wwd`.
- `has`: set when `InWindow(session.Start, start, end, past)`. A project has a
  row **iff** `has`; that is what lets a project whose only in-window session is
  still running appear with `0.00h` (REQ-012).
- `worked += session.Duration`, `billable += session.Rounded` and, when
  `session.Invoiced`, `invoiced += session.Rounded` — all only for in-window
  sessions.

Then, for each project with `has`:

```go
row := Row{
	Name:            name,
	Status:          entry.Status,
	LongTerm:        entry.LongTerm,
	WorkedSeconds:   w.worked,
	BillableSeconds: w.billable,
	Amount:          float64(w.billable) / 3600 * entry.Billing.Rate,
	RunningNow:      w.runningNow,
	HasUnbilled:     w.hasUnbilled,
}
```

and add `w.worked`, `w.billable`, `row.Amount`, `w.invoiced` and
`float64(w.invoiced)/3600*rate` into `Total` and `Money.Invoiced*`. When
`entry.Billing.Rate == 0`, append `name` to `NoRate` (the row is still added).

After the loop:

```go
rep.Money.UnbilledSeconds = rep.Total.BillableSeconds - rep.Money.InvoicedSeconds
rep.Money.UnbilledAmount  = rep.Total.Amount - rep.Money.InvoicedAmount
```

Then:

```go
sort.Slice(rep.Rows, func(i, j int) bool {
	if rep.Rows[i].WorkedSeconds != rep.Rows[j].WorkedSeconds {
		return rep.Rows[i].WorkedSeconds > rep.Rows[j].WorkedSeconds
	}
	return rep.Rows[i].Name < rep.Rows[j].Name
})
sort.Strings(rep.NoRate)
```

Worked time descending, name ascending as the deterministic tie-break
(REQ-017). The sort key is the table's first data column, so the visible order
matches what the eye reads first.

**Pass 2 — finished and earlier.** Build `workedByName` from `rep.Rows`, then
for each project:

```go
var ts *time.Time
switch entry.Status {
case "published":
	ts = entry.PublishedAt
case "canceled":
	ts = entry.CanceledAt
default:
	continue // never finished; not "earlier" either
}
if ts == nil {
	rep.Earlier = append(rep.Earlier, FinishedEarlier{Name: name, Status: entry.Status})
	continue
}
if !InWindow(*ts, start, end, past) {
	continue // finished, but not this week
}
worked, has := workedByName[name]
rep.Finished = append(rep.Finished, Finished{
	Name:          name,
	Status:        entry.Status,
	Date:          ts.In(now.Location()),
	WorkedSeconds: worked,
	HasWork:       has,
})
```

`ts.In(now.Location())` puts the stored UTC instant in the workspace's local
zone, so the rendered date is the local date it finished. Both slices are
sorted by name ascending.

A project that finished this week *and* was worked this week appears in both
`Rows` and `Finished` (REQ-027).

## Rendering

`internal/cli/hwd.go` turns a `*report.Report` into bytes. The renderer is the
only place that knows about palettes, terminal width and the exact strings.

### Sequence

```go
// renderReport writes the whole report. It is the one function that knows the
// order of the sections; the helpers below each own a single section.
func renderReport(out io.Writer, rep *report.Report, palette color.Palette, width int) {
	fmt.Fprintln(out, report.HeaderLine(rep.Start, rep.End, rep.Past))
	fmt.Fprintln(out)

	if len(rep.Rows) == 0 {
		fmt.Fprintln(out, emptyWorkLine(rep.Past))
	} else {
		renderReportTable(out, rep, palette, width)
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, palette.Dim(divider(width)))
	fmt.Fprintln(out)

	if len(rep.Rows) > 0 {
		renderMoneyBlock(out, rep)
		fmt.Fprintln(out)
		if len(rep.NoRate) > 0 {
			fmt.Fprintln(out, noRateWarning(rep.NoRate))
			fmt.Fprintln(out)
		}
	}

	renderFinished(out, rep)
	if len(rep.Earlier) > 0 {
		fmt.Fprintln(out)
		renderEarlier(out, rep, width)
	}
}
```

Which produces, in order: header, blank, [table *or* the empty-work line],
blank, divider, blank, [money block, blank], [no-rate warning, blank],
finished block, [blank, unknown-date line].

`emptyWorkLine(past)` returns `No work recorded this week.` or
`No work recorded that week.`

The table and the money block are both omitted when there are no rows: three
zero-valued money lines say nothing, and an empty week is not an error.

### The table

```go
headers := []string{nameGutter + "Project", "Worked", "Billable", "Amount"}
```

`nameGutter` is `wwd`'s two-space gutter, reused so the header lines up with
the names below it whether or not a row carries a marker.

Each row's cells are plain text — `markerCell(row)`, then
`projects.FormatHours(secs) + "h"` for `Worked` and `Billable`, then
`invoice.Symbol + formatAmount(row.Amount)` for `Amount`. A `TOTAL` row is
appended with `"TOTAL"` and the same three formatted `rep.Total` values.

```go
// markerCell is the plain text of a row's Project cell: the marker that says
// what kind of project it is, followed by the name. Canceled outranks
// long-term, because a canceled project's long-term status is moot.
func markerCell(row report.Row) string {
	switch {
	case row.Status == "canceled":
		return canceledMarker + row.Name
	case row.LongTerm:
		return longTermMarker + row.Name
	default:
		return nameGutter + row.Name
	}
}
```

```go
const canceledMarker = "✗ "
```

(`longTermMarker = "★ "` and `nameGutter = "  "` already exist in `wwd.go`.)

Fitting reuses `wwd`'s rule through the existing `fitColumns`, with `Project`
the only shrinkable column and a floor of three columns:

```go
headers, cells, widths := fitColumns(width, headers, cells, []int{0}, 3)
right := []bool{false, true, true, true}[:len(widths)]
```

The `[:len(widths)]` matters: `fitColumns` drops trailing columns on a narrow
terminal, and `right` must lose the same ones. Only ever the right edge is
dropped, so a prefix of `right` is the correct alignment vector.

The header row is rendered left-aligned with the existing `renderRow`; the data
rows are rendered with the new `renderRowAligned`, right-aligning the three
numeric columns. Color is applied to the already-padded name, exactly as
`wwd` does, so the escape codes can never shift a column:

```go
fmt.Fprintln(out, renderRow(headers, widths))
for i, row := range rep.Rows {
	name := padRight(cells[i][0], widths[0])
	switch hwdNameColor(row) {
	case colorGreen:
		name = palette.Green(name)
	case colorYellow:
		name = palette.Yellow(name)
	}
	cells[i][0] = name
	fmt.Fprintln(out, renderRowAligned(cells[i], widths, right))
}
fmt.Fprintln(out, renderRowAligned(cells[len(cells)-1], widths, right))
```

```go
// nameColor is the palette choice for a project name. It is a named type so
// the green-wins-over-yellow rule is a value a test can assert, rather than a
// pair of ifs buried in a renderer that needs a terminal to exercise.
type nameColor int

const (
	colorNone nameColor = iota
	colorGreen
	colorYellow
)

// hwdNameColor picks a row's name color by the same rules wwd uses: an active
// session outranks unbilled work, so green is checked first.
func hwdNameColor(row report.Row) nameColor {
	switch {
	case row.RunningNow:
		return colorGreen
	case row.HasUnbilled:
		return colorYellow
	default:
		return colorNone
	}
}
```

`renderRowAligned` and `padLeft` are added to `internal/cli/table.go` (see
"Package structure"). `renderRow` becomes a one-line wrapper over
`renderRowAligned`, so its callers do not change.

### The money block

```go
fmt.Fprintf(out, "%-10s %6s (%s%s)\n", "Billable:", projects.FormatHours(rep.Total.BillableSeconds)+"h", invoice.Symbol, formatAmount(rep.Total.Amount))
fmt.Fprintf(out, "%-10s %6s (%s%s)\n", "Invoiced:", projects.FormatHours(rep.Money.InvoicedSeconds)+"h", invoice.Symbol, formatAmount(rep.Money.InvoicedAmount))
fmt.Fprintf(out, "%-10s %6s (%s%s)\n", "Unbilled:", projects.FormatHours(rep.Money.UnbilledSeconds)+"h", invoice.Symbol, formatAmount(rep.Money.UnbilledAmount))
```

- The label column is 10 characters (`%-10s`), matching `grind show --billing`;
  each label is 9 characters, so there is always at least one space before the
  value.
- The hours sit in a 6-wide field (`%6s`), right-aligned so the `h` lines up.
  This is derived from the PRD's examples byte-for-byte:
  `Billable:  20.50h ($3075.00)`, `Invoiced:   8.00h ($1200.00)`,
  `Unbilled:  12.50h ($1875.00)`, and the past-week `Billable:   6.00h`.
- `%6s` is a *minimum* width: a five-digit hour count widens the field rather
  than truncating it, which is the honest failure.
- `invoice.Symbol` is the hardcoded `$`; `formatAmount` is `%.2f`. No thousands
  separator, no currency code, always two decimals — identical to
  `show --billing` and the invoice file.
- `Billable` is `rep.Total`, so it equals the table's `TOTAL` row for both
  hours and money, exactly.

### The no-rate warning

```go
// noRateWarning names the zero-rate projects whose hours are in the table but
// whose money is not. It is plain text on purpose: the sentence already says
// what is wrong, and a colored warning would compete with the yellow name
// marker for the same meaning.
func noRateWarning(names []string) string {
	return fmt.Sprintf("No billing rate set for: %s. Their hours are counted; their amount is $0.00.",
		strings.Join(names, ", "))
}
```

Example: `No billing rate set for: alpha, beta. Their hours are counted; their amount is $0.00.`

### The finished block

```go
// renderFinished writes the finished block, which always prints. Each entry is
// the name padded to the block's longest name, the status padded to nine
// characters, the local date padded to ten, then the week's hours — or an em
// dash when the project had no work in the window.
func renderFinished(out io.Writer, rep *report.Report) {
	label := "Finished this week:"
	if rep.Past {
		label = "Finished that week:"
	}
	if len(rep.Finished) == 0 {
		fmt.Fprintln(out, label+" none")
		return
	}
	fmt.Fprintln(out, label)

	maxName := 0
	for _, f := range rep.Finished {
		if n := utf8.RuneCountInString(f.Name); n > maxName {
			maxName = n
		}
	}
	for _, f := range rep.Finished {
		hours := "—"
		if f.HasWork {
			hours = projects.FormatHours(f.WorkedSeconds) + "h"
		}
		fmt.Fprintf(out, "  %s  %s  %s  %s\n",
			padRight(f.Name, maxName),
			padRight(f.Status, 9),
			padRight(f.Date.Format("2006-01-02"), 10),
			hours)
	}
}
```

The hours cell is the last cell and is not padded — nothing follows it, and a
trailing space would violate NFR-006.

### The unknown-date line

```go
// renderEarlier writes the one-line list of projects that finished but have no
// recorded date. It prints on every report, past weeks included, because it
// describes the workspace's unrecorded history rather than any one week. When
// the terminal width is known and the line would exceed it, it is truncated
// with an ellipsis rather than wrapped.
func renderEarlier(out io.Writer, rep *report.Report, width int) {
	var b strings.Builder
	b.WriteString("Finished earlier (no date recorded): ")
	for i, e := range rep.Earlier {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%s)", e.Name, e.Status)
	}
	line := b.String()
	if width > 0 {
		line = truncate(line, width)
	}
	fmt.Fprintln(out, line)
}
```

Example: `Finished earlier (no date recorded): old-thing (published)`

## Exact output

### Canonical example — current week

State: clock pinned at Wednesday 2026-11-11 14:05 local; `my-blog` (long-term,
rate 150, published 2026-11-10) worked 12.33h / 13.00h billable; `side-quest`
(rate 150, canceled 2026-11-09) worked 3.00h / 3.00h; `leenix` (rate 150) worked
4.00h / 4.50h; 8.00h of the week's billable time is already invoiced;
`old-thing` was published before this feature shipped.

```text
Week: 2026-11-08 (Sun) through now

  Project     Worked  Billable  Amount
★ my-blog     12.33h    13.00h  $1950.00
  leenix       4.00h     4.50h   $675.00
✗ side-quest   3.00h     3.00h   $450.00
TOTAL         19.33h    20.50h  $3075.00

 ─────────────────────────── The GrindCLI ──────────────────────────────

Billable:  20.50h ($3075.00)
Invoiced:   8.00h ($1200.00)
Unbilled:  12.50h ($1875.00)

Finished this week:
  my-blog     published  2026-11-10  12.33h
  side-quest  canceled   2026-11-09  3.00h

Finished earlier (no date recorded): old-thing (published)
```

Column widths are `12, 6, 8, 8` (widest of header and cells per column, two
spaces between columns). Note that `Billable`'s header (8) is wider than any of
its cells (6), which is why those cells carry two leading spaces when
right-aligned. The divider is `wwd`'s verbatim string.

The PRD's §8.3 sample shows the same numbers with slightly different spacing;
the PRD itself says "the spacing below is the shape, and REQ-019 is the rule".
The block above is what REQ-019 plus right-aligned numerics produces.

Two notes on the row order, found while implementing:

- The rows are sorted by `Worked` descending (REQ-017): `my-blog` 12.33h,
  `leenix` 4.00h, `side-quest` 3.00h. The PRD's §8.2 example lists
  `side-quest` before `leenix`, which contradicts its own REQ-017; the rule
  wins, so the block above shows `leenix` first. (Earlier drafts of this spec
  repeated the PRD's order; that was a spec bug.)
- The finished block is sorted by *name* ascending, so `my-blog` precedes
  `side-quest` there.

### Past week

`grind hwd -w 1` at the same clock, with work only in the previous week:

```text
Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)

  Project  Worked  Billable  Amount
  leenix    6.00h     6.00h  $900.00
TOTAL       6.00h     6.00h  $900.00

 ─────────────────────────── The GrindCLI ──────────────────────────────

Billable:   6.00h ($900.00)
Invoiced:   6.00h ($900.00)
Unbilled:   0.00h ($0.00)

Finished that week: none

Finished earlier (no date recorded): old-thing (published)
```

Only the header and the two "this week" strings change; everything below the
header behaves identically (REQ-047).

### Empty week

No session in the window, nothing finished:

```text
Week: 2026-11-08 (Sun) through now

No work recorded this week.

 ─────────────────────────── The GrindCLI ──────────────────────────────

Finished this week: none
```

A past week (`grind hwd -w 3`) differs only in the header and the wording:

```text
Week: 2026-10-18 (Sun) through 2026-10-24 (Sat)

No work recorded that week.

 ─────────────────────────── The GrindCLI ──────────────────────────────

Finished that week: none
```

A workspace with no projects at all produces the same empty-week output.

### Formatting rules, stated once

| Element | Format |
|---|---|
| Header, current week | `Week: YYYY-MM-DD (Day) through now` |
| Header, past week | `Week: YYYY-MM-DD (Day) through YYYY-MM-DD (Day)` |
| Empty-work line, current week | `No work recorded this week.` |
| Empty-work line, past week | `No work recorded that week.` |
| Finished block label | `Finished this week:` / `Finished that week:`, and `…: none` when empty |
| Hours | `%.2f` + `h` via `projects.FormatHours` — `12.33h`, `0.00h` |
| Money | `invoice.Symbol` + `%.2f` via `formatAmount` — `$1950.00`, `$0.00` |
| Dates | local `YYYY-MM-DD` |
| Empty value | `—` (em dash) |
| Marker gutter | `★ ` long-term, `✗ ` canceled, `  ` otherwise |
| Column separator | two spaces; color applied after padding |
| Table alignment | `Project` left; `Worked`/`Billable`/`Amount` right; headers left |

## Package structure

```
internal/report/report.go       # NEW: Window, InWindow, HeaderLine, Build, the types
internal/config/config.go       # + PublishedAt, + CanceledAt on ProjectEntry
internal/projects/projects.go   # Publish/Cancel stamp the timestamp
internal/cli/hwd.go             # NEW: the command, parseWeeks, the renderer
internal/cli/table.go           # + padLeft, + renderRowAligned
internal/cli/root.go            # wire newHwdCmd(clk); version 0.99.1
```

### `internal/report/report.go` (new)

Imports `time`, `sort`, `internal/config` and `internal/workspace`. No `clock`,
no `git`. The whole package is the file; a second file for rendering would
split one concern in two.

### `internal/cli/table.go` (extend)

```go
// padLeft returns s padded with leading spaces to width runes. It is the
// mirror of padRight, for the numeric columns whose figures line up on their
// right edge.
func padLeft(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// renderRowAligned pads cells to their column widths and joins them with two
// spaces, aligning each cell left or right according to right. A nil right
// means "every cell left", which is what renderRow wants.
//
// The last cell is padded only when it is right-aligned: left-aligned trailing
// spaces would only pollute the line, but a right-aligned figure needs its
// leading spaces to sit under the column above it.
func renderRowAligned(cells []string, widths []int, right []bool) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		switch {
		case right != nil && right[i]:
			c = padLeft(c, widths[i])
		case i < len(cells)-1:
			c = padRight(c, widths[i])
		}
		b.WriteString(c)
	}
	return b.String()
}

// renderRow is renderRowAligned with no right-aligned columns.
func renderRow(cells []string, widths []int) string {
	return renderRowAligned(cells, widths, nil)
}
```

`renderRow`'s signature is unchanged, so every existing caller (`wwd`,
`list tasks`, `list projects`, …) is untouched.

### `internal/cli/root.go` (extend)

```go
	root.AddCommand(newHwdCmd(clk))
```

alongside the other `AddCommand` calls, and:

```go
const version = "0.99.1"
```

`hwd` takes the clock directly (it has no service), so it is wired from the
same `clk` the rest of the tree shares.

## Testing

### `internal/report` (unit — no workspace, no clock)

`Build` takes `now` as a parameter, so these tests construct a workspace on
disk with `config.WriteProjects` and pass a literal instant. A small helper
mirrors `internal/status`'s `newTestWorkspace`:

```go
// newTestWorkspace writes .projects.json into a temp directory and returns the
// workspace that points at it. The report reads the file through the
// workspace path, so the test writes the file exactly like production would.
func newTestWorkspace(t *testing.T, projects config.ProjectsConfig) *workspace.Workspace
```

- `TestWindow` — the table in "The reporting window" above, plus a
  `weeksBack == -1` case that equals `weeksBack == 0`. Assert `start`, `end`
  and `past` for each row.
- `TestWindowSpansDST` — with `America/Chicago`, `now` = Wed 2026-11-11 14:05
  local and `weeksBack` 1, assert `end.Sub(start) == 169*time.Hour` (7 days
  plus the hour the clock gave back on 2026-11-01). Skip with
  `t.Skip` when `time.LoadLocation("America/Chicago")` fails.
- `TestInWindow` — a table of boundaries: at `start` (in), just before `start`
  (out), at `end` for the current week (in), at `end` for a past week (out),
  just before `end` for a past week (in).
- `TestHeaderLine` — both forms, including a past week whose end is a Sunday.
- `TestBuildAttributesSessionsByStart` (AC-003) — a session
  2026-11-07 23:00 → 2026-11-08 02:00 and one 2026-11-09 09:00 → 12:00; only
  the second is counted, `WorkedSeconds == 3*3600`.
- `TestBuildRounding` (AC-004) — `Duration 15600`, `Rounded 16200` → the row's
  `WorkedSeconds` is 15600 and `BillableSeconds` 16200.
- `TestBuildAmount` (AC-005) — 13.00h billable at rate 150 → `Amount == 1950`.
- `TestBuildSortsByWorkedThenName` (AC-006, AC-007) — a 12.00h and a 4.00h
  project come out 12 first; two equal-worked projects come out name-ascending.
- `TestBuildMoneySplit` (AC-009) — 4.00h billable of which 2.00h invoiced →
  `Total.BillableSeconds == 4h`, `Money.InvoicedSeconds == 2h`,
  `Money.UnbilledSeconds == 2h`, and `InvoicedAmount + UnbilledAmount ==
  Total.Amount` exactly.
- `TestBuildZeroRate` (AC-010) — rate 0, 3.00h worked → the row is present,
  `Amount == 0`, the project's hours are in `Total`, `Total.Amount` is
  unaffected, and `NoRate == []string{name}`.
- `TestBuildRunningSessionIsARow` (AC-012) — a session with `End == nil` and
  `Start` in the window gives the project a row with `WorkedSeconds == 0`,
  `BillableSeconds == 0` and `RunningNow == true`, and adds nothing to `Total`.
- `TestBuildHasUnbilledIsWorkspaceWide` — a project with an un-invoiced session
  *outside* the window still has `HasUnbilled == true`.
- `TestBuildFinishedInWindow` (AC-014) — a published project with `PublishedAt`
  in the window appears in `Finished` with `Status == "published"` and the
  local date.
- `TestBuildFinishedNotInWindow` (AC-017) — a `PublishedAt` from a previous
  week is in neither `Finished` nor `Earlier`.
- `TestBuildFinishedEarlier` (AC-018) — `status: "published"`, no
  `publishedAt` → `Earlier == [{name, "published"}]`, and `Finished` is empty.
- `TestBuildFinishedUsesStatusTimestamp` — a project with `status: "canceled"`
  and both timestamps set reports the `CanceledAt` date, not `PublishedAt`.
- `TestBuildFinishedNoWork` (AC-016) — a project published in the window with
  no work in the window has `HasWork == false`.
- `TestBuildPastWeekInvoicedNow` (AC-043) — a session in week `-1` that is
  invoiced *now* lands in `Money.InvoicedSeconds`, not `Unbilled`.
- `TestBuildEmptyWeek` (AC-038) — no sessions anywhere → no rows, zero totals,
  empty `Finished` and `Earlier`, no error.
- `TestBuildMissingProjectsFile` (AC-026) — no `.projects.json` →
  `grinderr.ExitCode(err) == 2`.

### `internal/cli` (the fake git)

Use the existing `runInWorkspace` / `executeAt` helpers. Add one pinned instant:

```go
// hwdNow is the instant every hwd test runs at: Wednesday 2026-11-11 14:05
// local, so the current week starts Sunday 2026-11-08.
func hwdNow() *clock.Fake {
	return clock.NewFake(time.Date(2026, 11, 11, 14, 5, 0, 0, time.Local))
}
```

Sessions are planted by writing `.projects.json` directly (the report reads
only that file), which keeps the fixtures readable; `createProject` +
`config`/`save` are used where the test is about the command, not the report.

- `TestHwdCurrentWeek` — the golden test. Plant the canonical example's state,
  run `hwd` at `hwdNow()`, and assert the exact bytes of the block in "Exact
  output" (compare the full string, not substrings).
- `TestHwdPastWeek` (AC-034, AC-046) — the past-week block, exact bytes.
- `TestHwdWeekZeroIsIdentical` (AC-036) — `hwd` and `hwd -w 0` produce the same
  string.
- `TestHwdWeeksFormsAgree` (AC-037) — `hwd --weeks=2` and `hwd -w 2` produce
  the same string.
- `TestHwdNegativeWeeksIsWeeksBack` — `hwd -w -1` produces the same string as
  `hwd -w 1`, and exits 0.
- `TestHwdEmptyWeek` (AC-019) — exact bytes of the empty-week block, exit 0.
- `TestHwdNoProjects` (AC-020) — a fresh workspace prints the empty-week block.
- `TestHwdNoRateWarning` (AC-010) — a zero-rate project's name appears in the
  `No billing rate set for: …` line, after the money block.
- `TestHwdFinishedBlock` (AC-014, AC-015) — publish then cancel at pinned
  instants via the real verbs, then `hwd`: both lines appear, and the canceled
  project still has a table row with the `✗ ` marker.
- `TestHwdUnknownDateLine` (AC-018, AC-044) — a project with `status:
  "published"` and no timestamp produces the `Finished earlier (no date
  recorded): …` line, in both the current week and `-w 1`.
- `TestHwdHiddenFromHelp` (AC-022) — `--help` output does not contain `hwd`.
- `TestHwdRejectsArguments` (AC-023) — `hwd extra` returns a user error
  (`grinderr.ExitCode(err) == 1`) and prints `hwd takes no arguments`.
- `TestHwdNotInWorkspace` (AC-024) — `Not in a grind workspace.`, exit 1.
- `TestHwdExplainsAV1Workspace` (AC-025) — build a v1 workspace with the
  `buildV1Workspace` helper from `migrate_test.go`; the migrate hint prints and
  the exit code is 1.
- `TestHwdMissingProjectsFile` (AC-026) — delete `.projects.json`; the
  `read projects file` system error is returned and the exit code is 2.
- `TestHwdIsReadOnly` (AC-021) — record `len(fake.commits)` before, run `hwd`,
  assert it is unchanged; also assert `.projects.json` bytes are identical
  before and after.
- `TestHwdPlainWhenCaptured` (AC-029) — the output from `executeAt` (a buffer)
  contains no `\x1b`, and no line is longer than the natural table width.
- `TestHwdNarrowTerminalDropsAmount` (AC-028) — build a `*report.Report` by
  hand and call `renderReport(&buf, rep, color.Palette{}, 40)` directly (the
  `wwd_responsive_test.go` pattern): `Amount` is absent from the header, the
  long project name is truncated with `…`, and every line is at most 40 runes.
  The fixture must be wide enough that the table's *minimum* width exceeds 40:
  one row whose `Project` cell is `★ a-very-long-project-name`, `Worked`
  `123.45h`, `Billable` `234.56h`, `Amount` `$12345.67` gives natural widths
  `[26, 7, 8, 9]` and a minimum of 42 (only `Project` shrinks, to
  `minFlexibleWidth` 12), so `Amount` is dropped and `Project` is then
  truncated to 21 runes. Note the *canonical* table at exactly 40 columns does
  **not** drop anything — its natural widths `[12, 6, 8, 8]` already have a
  minimum of exactly 40 — which is why the narrow-terminal test needs a wider
  fixture than the golden example.
- `TestParseWeeks` — a table: `"0"`→0, `"1"`→1, `"3"`→3, `"-1"`→1, `"-3"`→3,
  `"+2"`→2; and errors for `"2.5"`, `"abc"`, `""`, `" 1"`, `"1h"`, with the
  exact message asserted.
- `TestHwdBadWeeksValue` (AC-039, AC-040 in spirit) — `hwd -w abc` returns a
  user error (`ExitCode == 1`) whose message is the generic one with `"abc"`
  quoted, and prints no report; `hwd -w 2.5` and `hwd -w ''` do the same.
- `TestHwdUnsupportedShorthand` (AC-041) — `hwd -1` returns an error and the
  output contains `unknown shorthand flag: '1' in -1`. Assert
  `grinderr.ExitCode(err) != 1` (it is 99) so the deviation is documented in
  code.
- `TestHwdNameColor` (AC-013) — a table over `report.Row`s:
  `{RunningNow: true, HasUnbilled: true}` → `colorGreen` (green wins);
  `{HasUnbilled: true}` → `colorYellow`; `{}` → `colorNone`.
- `TestHwdWeeksFlagHelp` (REQ-040) — `hwd --help` output contains `--weeks`
  and `-w`.

### `internal/cli/table_test.go` (extend)

- `TestRenderRowAligned` — left/right/nil cases, including that the last cell
  is unpadded when left-aligned and left-padded when right-aligned, and that a
  nil `right` slice reproduces `renderRow` byte-for-byte.

### `internal/projects` (change existing tests)

- `TestPublishHappyPath` — set `svc.Clock = clock.NewFake(testNow)` and assert
  `projects.Projects["my-blog"].PublishedAt` equals
  `testNow.UTC().Truncate(time.Second)`. Everything else stays.
- `TestCancelHappyPath` — the same for `CanceledAt`.
- `TestPublishRePublishNoChange` → **renamed
  `TestPublishRePublishStampsAndCommits`**. It sets
  `fake.dirtyPaths[".projects.json"] = true` (so `IsPathClean` reports the file
  dirty, exactly as a real timestamp write would), sets
  `svc.Clock = clock.NewFake(testNow)`, and asserts one commit was recorded and
  `PublishedAt == testNow.UTC().Truncate(time.Second)`. Its comment explains
  the rename: a publish now always changes `.projects.json`, so the commit is
  the point of the test, not its absence.
- `projects_test.go` gains the `internal/clock` import (it currently has none;
  `session_test.go` does).

### `internal/config` (new test)

- `TestProjectsRoundTripWithoutTimestamps` (AC-032) — write a `.projects.json`
  with no `publishedAt`/`canceledAt` for a never-published project, read and
  write it back with `WriteProjects`, and assert the bytes are unchanged,
  `version == 1`, and neither key appears.

### Existing tests that must change

- `TestVersionFlag` (`cli_test.go`) — `0.99.0` → `0.99.1`.
- `TestPublishRePublishNoChange` — see above.
- No other existing test asserts anything this slice changes.

### Docs

- `README.md`: a `grind hwd` row in the `HIDDEN COMMANDS` table, beside
  `grind wwd`, describing the weekly hours report, the `-w/--weeks` flag, and
  that it is read-only.
- `AGENTS.md`: the slice table gains
  `13 | Hours-worked report (`hwd`) | ✅ done` (or the in-progress marker the
  workflow uses), and the command-pattern section notes `hwd` as a second
  hidden command.

## Deviations from the PRD

Each of these was raised with the user and agreed; the PRD wins on everything
else.

| PRD ref | The PRD says | This spec does | Why |
|---|---|---|---|
| REQ-038 | Timestamps are RFC 3339 "with the local offset the clock produced" | Stored `.UTC().Truncate(time.Second)`, like `createdAt` and `Session.Start`; rendered in the local zone | One spelling of an instant per file. Comparisons are instant comparisons either way, so nothing observable changes except the offset in the JSON. |
| REQ-044, AC-039 | A negative `-w` is a user error | `-w -3` means three weeks back | "Weeks back" already carries the direction; a sign with no meaning is a trap, and the user asked for the generic rule. |
| REQ-044, AC-039/AC-040 | Error string `weeks must be a whole number of weeks back, 0 or more. Got '<value>'.` | `Invalid value for -w/--weeks: "<value>". Pass a whole number of weeks to look back — for example: grind hwd -w 1` | The user asked for one generic message with a usage hint. `%q` matches the codebase's other value-echoing errors. |
| REQ-044 | A repeated `-w` is an error | Repeats are allowed; the last wins | pflag's default, and the least surprising thing for a shell user. |
| REQ-045, AC-041 | `-1` prints cobra's error **plus the usage block**, exit 1 | cobra's `unknown shorthand flag: '1' in -1`, **no** usage block, exit 99 | The user chose "leave cobra's default". `SilenceUsage: true` is already set root-wide; changing it for one hidden command would change it for every command. |
| REQ-019, §8.3 | "cells shall be padded with spaces to the column width" | Numeric columns are right-aligned | The PRD's examples right-align the figures so the decimal points line up. §8.3's spacing is hand-aligned; the PRD says "the spacing below is the shape, and REQ-019 is the rule". |
| REQ-002, AC-023 | "argument-count user error (exit 1)" | Custom `noHwdArgs` returning `grinderr.NewUser` | `cobra.NoArgs` returns a bare error, which `main` maps to exit 99 — that would contradict AC-023. |

## Extension points

Deliberately out of this slice, each additive:

- **`--json`.** `report.Report` is already plain data with no rendering in it;
  a JSON encoder is one function in `internal/cli/hwd.go` and a flag.
- **A month view or `--since`.** `Window` is the only place the window is
  computed; a date-range variant would sit beside it, and `Build` would take
  the window instead of `Options`.
- **Per-day rows.** `Report` would gain a `[]Day` and `Build` would group by
  `Start.Local()` day, exactly as `invoice` already does.
- **Rate history.** `Row.Amount` uses the project's current rate. A
  `rateHistory` list on the entry would let `Build` pick the rate in force at
  the session's start.
- **Backfilling old finish dates.** The unknown-date line is the worklist;
  a command could stamp `publishedAt` from the merge commit that `publish`
  created. Not here: `hwd` must stay read-only, and a guess is worse than an
  honest "no date recorded".
- **Aligning `wwd`.** `wwd` could gain a week scope, or a `Billed` column now
  that `Invoiced` exists. This slice changes neither `wwd` nor its tests.
- **A pager.** `hwd` prints at most a couple of screens; if it ever grows,
  `internal/pager` is already there.

## Definition of done

- `grind hwd` prints the canonical block above, byte for byte, at a pinned
  clock, and `grind hwd -w 1` prints the past-week block.
- `grind hwd -w 0` is byte-identical to `grind hwd`; `--weeks=N` and `-w N`
  agree; `-w -N` equals `-w N`.
- A week with no work prints the empty state (header, empty-work line, divider,
  finished block) and exits 0, as does a workspace with no projects and a week
  that predates the workspace.
- `grind hwd` writes nothing, commits nothing and reads no worktree:
  `git -C .main status --porcelain` is empty afterwards and the fake git
  records no commit; `.projects.json` is byte-identical before and after.
- `grind publish` and `grind cancel` stamp `publishedAt`/`canceledAt` from the
  service clock on every run, and the finished block shows the date and hours
  of the project's *current* status.
- A finished project with no timestamp is listed once on the
  `Finished earlier (no date recorded): …` line, on every report.
- A zero-rate project is counted in the table and named in the no-rate warning.
- `hwd` does not appear in `grind --help`; `hwd extra` exits 1 with
  `hwd takes no arguments`; a bad `-w` exits 1 with the generic message and no
  report; `hwd` outside a workspace and in a v1 workspace exits 1 with the
  existing messages; a missing `.projects.json` exits 2.
- Output is plain text when stdout is not a terminal: no escape sequences, no
  tabs, no carriage returns, no trailing whitespace, no truncation.
- No new module in `go.mod`; `hwd` spawns no external process.
- `gofmt -l .` prints nothing; `go build ./...`, `go vet ./...`,
  `go test ./...`, `go test -race ./...`, and `TZ=Pacific/Niue go test ./...`
  all pass.
- README and AGENTS.md are updated; `version` is `0.99.1`.
