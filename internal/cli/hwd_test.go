package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/report"
)

// hwdNow is the instant every hwd test runs at: Wednesday 2026-11-11 14:05
// local, so the current week starts Sunday 2026-11-08.
func hwdNow() *clock.Fake {
	return clock.NewFake(time.Date(2026, 11, 11, 14, 5, 0, 0, time.Local))
}

// hwdAt builds a local instant for a fixture. Pinning every date this way is
// what makes the golden strings exact under any TZ.
func hwdAt(year int, month time.Month, day, hour, min int) time.Time {
	return time.Date(year, month, day, hour, min, 0, 0, time.Local)
}

// hwdSession builds an ended session: start + duration seconds, rounded to
// rounded seconds.
func hwdSession(start time.Time, duration, rounded int64, invoiced bool) config.Session {
	end := start.Add(time.Duration(duration) * time.Second)
	return config.Session{
		Start:    start,
		End:      &end,
		Duration: duration,
		Rounded:  rounded,
		Invoiced: invoiced,
	}
}

// hwdEntry builds a project entry with a rate and no sessions; callers add
// the sessions and any status/timestamps they need.
func hwdEntry(name string, rate float64) config.ProjectEntry {
	return config.ProjectEntry{
		Name:    name,
		Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: rate},
	}
}

// plantProjects writes the entries into .main/.projects.json. The report reads
// only that file, so planting it directly keeps the fixtures readable.
func plantProjects(t *testing.T, entries ...config.ProjectEntry) {
	t.Helper()
	cfg := config.DefaultProjects()
	for _, e := range entries {
		cfg.Projects[e.Name] = e
	}
	if err := config.WriteProjects(filepath.Join(".main", ".projects.json"), cfg); err != nil {
		t.Fatal(err)
	}
}

// canonicalProjects plants the state behind the canonical golden block:
// my-blog (long-term, published) worked 12.33h/13.00h; side-quest (canceled)
// 3.00h/3.00h; leenix 4.00h/4.50h; 8.00h of the week's billable time already
// invoiced; old-thing published before timestamps existed.
func canonicalProjects(t *testing.T) {
	t.Helper()
	published := hwdAt(2026, time.November, 10, 16, 0)
	canceled := hwdAt(2026, time.November, 9, 16, 0)

	myBlog := hwdEntry("my-blog", 150)
	myBlog.Status = "published"
	myBlog.LongTerm = true
	myBlog.PublishedAt = &published
	myBlog.Sessions = []config.Session{
		// 8.00h, invoiced: 8.00h × 150 = $1200.00.
		hwdSession(hwdAt(2026, time.November, 9, 9, 0), 28800, 28800, true),
		// 4.33h worked, 5.00h billable after quarter-hour rounding.
		hwdSession(hwdAt(2026, time.November, 10, 9, 0), 15588, 18000, false),
	}

	sideQuest := hwdEntry("side-quest", 150)
	sideQuest.Status = "canceled"
	sideQuest.CanceledAt = &canceled
	sideQuest.Sessions = []config.Session{
		hwdSession(hwdAt(2026, time.November, 9, 9, 0), 10800, 10800, false),
	}

	leenix := hwdEntry("leenix", 150)
	leenix.Sessions = []config.Session{
		hwdSession(hwdAt(2026, time.November, 9, 9, 0), 14400, 16200, false),
	}

	// Published before this feature shipped: no timestamp, so it lands on the
	// "no date recorded" line rather than the finished block.
	oldThing := hwdEntry("old-thing", 150)
	oldThing.Status = "published"

	plantProjects(t, myBlog, sideQuest, leenix, oldThing)
}

// pastWeekProjects plants work only in the week before the pinned instant.
func pastWeekProjects(t *testing.T) {
	t.Helper()
	leenix := hwdEntry("leenix", 150)
	leenix.Sessions = []config.Session{
		hwdSession(hwdAt(2026, time.November, 3, 9, 0), 21600, 21600, true),
	}
	oldThing := hwdEntry("old-thing", 150)
	oldThing.Status = "published"
	plantProjects(t, leenix, oldThing)
}

// hwdCanonical is the exact bytes the canonical fixture renders. It matches
// the spec's block except that the two lower rows are in the order REQ-017
// mandates (Worked descending: leenix 4.00h before side-quest 3.00h). The
// spec's illustrative block lists them in the order the prose describes them,
// which contradicts its own sort rule; the rule wins.
const hwdCanonical = `Week: 2026-11-08 (Sun) through now

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
`

// hwdPast is the exact bytes for the past-week fixture at -w 1.
const hwdPast = `Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)

  Project  Worked  Billable  Amount
  leenix    6.00h     6.00h  $900.00
TOTAL       6.00h     6.00h  $900.00

 ─────────────────────────── The GrindCLI ──────────────────────────────

Billable:   6.00h ($900.00)
Invoiced:   6.00h ($900.00)
Unbilled:   0.00h ($0.00)

Finished that week: none

Finished earlier (no date recorded): old-thing (published)
`

// hwdEmptyCurrent is the empty-week block for the current week.
const hwdEmptyCurrent = `Week: 2026-11-08 (Sun) through now

No work recorded this week.

 ─────────────────────────── The GrindCLI ──────────────────────────────

Finished this week: none
`

func TestHwdCurrentWeek(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	canonicalProjects(t)

	out, err := executeAt(t, fake, hwdNow(), "hwd")
	if err != nil {
		t.Fatalf("hwd: %v", err)
	}
	if out != hwdCanonical {
		t.Errorf("hwd output:\n%s\nwant:\n%s", out, hwdCanonical)
	}
}

func TestHwdPastWeek(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	pastWeekProjects(t)

	out, err := executeAt(t, fake, hwdNow(), "hwd", "-w", "1")
	if err != nil {
		t.Fatalf("hwd -w 1: %v", err)
	}
	if out != hwdPast {
		t.Errorf("hwd -w 1 output:\n%s\nwant:\n%s", out, hwdPast)
	}
}

// TestHwdWeekZeroIsIdentical is AC-036: -w 0 is byte-identical to no flag.
func TestHwdWeekZeroIsIdentical(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	canonicalProjects(t)

	bare, err := executeAt(t, fake, hwdNow(), "hwd")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := executeAt(t, fake, hwdNow(), "hwd", "-w", "0")
	if err != nil {
		t.Fatal(err)
	}
	if bare != zero {
		t.Errorf("hwd and hwd -w 0 differ:\n%q\n%q", bare, zero)
	}
}

// TestHwdWeeksFormsAgree is AC-037: --weeks=N and -w N name the same week.
func TestHwdWeeksFormsAgree(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	canonicalProjects(t)

	long, err := executeAt(t, fake, hwdNow(), "hwd", "--weeks=2")
	if err != nil {
		t.Fatal(err)
	}
	short, err := executeAt(t, fake, hwdNow(), "hwd", "-w", "2")
	if err != nil {
		t.Fatal(err)
	}
	if long != short {
		t.Errorf("--weeks=2 and -w 2 differ:\n%q\n%q", long, short)
	}
}

// TestHwdNegativeWeeksIsWeeksBack: the sign carries no meaning, so -w -1 is
// the same week as -w 1 and exits 0.
func TestHwdNegativeWeeksIsWeeksBack(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	pastWeekProjects(t)

	pos, err := executeAt(t, fake, hwdNow(), "hwd", "-w", "1")
	if err != nil {
		t.Fatal(err)
	}
	neg, err := executeAt(t, fake, hwdNow(), "hwd", "-w", "-1")
	if err != nil {
		t.Fatalf("hwd -w -1: %v", err)
	}
	if pos != neg {
		t.Errorf("-w 1 and -w -1 differ:\n%q\n%q", pos, neg)
	}
}

// TestHwdEmptyWeek is AC-019: a week with no work prints the empty state and
// exits 0.
func TestHwdEmptyWeek(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	plantProjects(t, hwdEntry("my-blog", 150))

	out, err := executeAt(t, fake, hwdNow(), "hwd")
	if err != nil {
		t.Fatalf("hwd: %v", err)
	}
	if out != hwdEmptyCurrent {
		t.Errorf("hwd output:\n%s\nwant:\n%s", out, hwdEmptyCurrent)
	}
}

// TestHwdNoProjects is AC-020: a workspace with no projects prints the same
// empty-week block.
func TestHwdNoProjects(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := executeAt(t, fake, hwdNow(), "hwd")
	if err != nil {
		t.Fatalf("hwd: %v", err)
	}
	if out != hwdEmptyCurrent {
		t.Errorf("hwd output:\n%s\nwant:\n%s", out, hwdEmptyCurrent)
	}
}

// TestHwdNoRateWarning is AC-010: a zero-rate project's hours are counted and
// its name appears in the warning, after the money block.
func TestHwdNoRateWarning(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	freebie := hwdEntry("freebie", 0)
	freebie.Sessions = []config.Session{
		hwdSession(hwdAt(2026, time.November, 9, 9, 0), 10800, 10800, false),
	}
	plantProjects(t, freebie)

	out, err := executeAt(t, fake, hwdNow(), "hwd")
	if err != nil {
		t.Fatalf("hwd: %v", err)
	}
	want := "No billing rate set for: freebie. Their hours are counted; their amount is $0.00."
	if !strings.Contains(out, want) {
		t.Errorf("output = %q, missing %q", out, want)
	}
	if strings.Index(out, want) < strings.Index(out, "Unbilled:") {
		t.Errorf("the no-rate warning must follow the money block:\n%s", out)
	}
}

// createProjectAt promotes an idea into a project at the pinned instant, so
// the entry's timestamps are facts rather than whatever the wall clock says.
func createProjectAt(t *testing.T, fake *fakeGit, clk clock.Clock, name string) {
	t.Helper()
	if _, err := executeAt(t, fake, clk, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAt(t, fake, clk, "new", "project", name, "0", "-t", "blog"); err != nil {
		t.Fatal(err)
	}
}

// TestHwdFinishedBlock is AC-014 and AC-015: the real publish and cancel verbs
// stamp the timestamps the finished block renders, and a canceled project
// still has a table row with the marker.
func TestHwdFinishedBlock(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	clk := hwdNow()

	createProjectAt(t, fake, clk, "my-blog")
	createProjectAt(t, fake, clk, "side-quest")
	if _, err := executeAt(t, fake, clk, "save", "my-blog", "-t", "3h"); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAt(t, fake, clk, "save", "side-quest", "-t", "2h"); err != nil {
		t.Fatal(err)
	}
	if _, err := executeAt(t, fake, clk, "publish", "my-blog", "-y"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := executeAt(t, fake, clk, "cancel", "side-quest", "-y"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	out, err := executeAt(t, fake, clk, "hwd")
	if err != nil {
		t.Fatalf("hwd: %v", err)
	}
	for _, want := range []string{
		"Finished this week:",
		"my-blog     published  2026-11-11  3.00h",
		"side-quest  canceled   2026-11-11  2.00h",
		"✗ side-quest",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

// TestHwdUnknownDateLine is AC-018 and AC-044: a finished project with no
// timestamp is listed once, on every report.
func TestHwdUnknownDateLine(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	oldThing := hwdEntry("old-thing", 150)
	oldThing.Status = "published"
	plantProjects(t, oldThing)

	want := "Finished earlier (no date recorded): old-thing (published)"
	for _, args := range [][]string{{"hwd"}, {"hwd", "-w", "1"}} {
		out, err := executeAt(t, fake, hwdNow(), args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%v output = %q, missing %q", args, out, want)
		}
	}
}

// TestHwdHiddenFromHelp is AC-022: hwd is the second secret handshake.
func TestHwdHiddenFromHelp(t *testing.T) {
	out, err := execute(t, &fakeGit{}, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if strings.Contains(out, "hwd") {
		t.Errorf("--help output leaks the hidden command: %q", out)
	}
}

// TestHwdRejectsArguments is AC-023: a positional argument is a user error
// (exit 1), not cobra's bare error (exit 99).
func TestHwdRejectsArguments(t *testing.T) {
	_, err := execute(t, &fakeGit{}, "hwd", "extra")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := grinderr.ExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
	if err.Error() != "hwd takes no arguments" {
		t.Errorf("error = %q, want %q", err.Error(), "hwd takes no arguments")
	}
}

// TestHwdNotInWorkspace is AC-024.
func TestHwdNotInWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	_, err = execute(t, &fakeGit{}, "hwd")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Not in a grind workspace.") {
		t.Errorf("error = %q", err.Error())
	}
	if got := grinderr.ExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

// TestHwdExplainsAV1Workspace is AC-025: the same migrate hint wwd prints.
func TestHwdExplainsAV1Workspace(t *testing.T) {
	root := buildV1Workspace(t)

	_, err := runGrind(t, root, "hwd")
	if err == nil {
		t.Fatal("hwd in a v1 workspace = nil error, want the migrate hint")
	}
	want := "This workspace was created by grind v1. Run 'grind migrate' to convert it."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if got := grinderr.ExitCode(err); got != 1 {
		t.Errorf("exit code = %d, want 1", got)
	}
}

// TestHwdMissingProjectsFile is AC-026: a missing file is the existing system
// error, exit 2.
func TestHwdMissingProjectsFile(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	if err := os.Remove(filepath.Join(".main", ".projects.json")); err != nil {
		t.Fatal(err)
	}

	_, err := executeAt(t, fake, hwdNow(), "hwd")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := grinderr.ExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
}

// TestHwdIsReadOnly is AC-021: hwd commits nothing and leaves .projects.json
// byte-identical.
func TestHwdIsReadOnly(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	canonicalProjects(t)

	path := filepath.Join(".main", ".projects.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	commitsBefore := len(fake.commits)

	if _, err := executeAt(t, fake, hwdNow(), "hwd"); err != nil {
		t.Fatalf("hwd: %v", err)
	}

	if len(fake.commits) != commitsBefore {
		t.Errorf("commits = %d, want %d (hwd must not commit)", len(fake.commits), commitsBefore)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf(".projects.json changed:\n%s\n%s", before, after)
	}
}

// TestHwdPlainWhenCaptured is AC-029: a buffer is not a terminal, so the
// output is plain and untruncated.
func TestHwdPlainWhenCaptured(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	canonicalProjects(t)

	out, err := executeAt(t, fake, hwdNow(), "hwd")
	if err != nil {
		t.Fatalf("hwd: %v", err)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("captured output has escape codes: %q", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("captured output is truncated: %q", out)
	}
	// The divider is the report's widest natural line; nothing may exceed it.
	max := rowRunes(divider(0))
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if got := rowRunes(line); got > max {
			t.Errorf("line is %d runes, want at most the natural %d: %q", got, max, line)
		}
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

// TestHwdNarrowTerminalDropsAmount is AC-028: on a narrow terminal the table
// drops the trailing Amount column, then truncates the project name. The
// fixture is wider than the golden one so the minimum width exceeds 40.
func TestHwdNarrowTerminalDropsAmount(t *testing.T) {
	rep := &report.Report{
		Start: hwdAt(2026, time.November, 8, 0, 0),
		End:   hwdAt(2026, time.November, 11, 14, 5),
		Rows: []report.Row{{
			Name:            "a-very-long-project-name",
			LongTerm:        true,
			WorkedSeconds:   444420, // 123.45h
			BillableSeconds: 844416, // 234.56h
			Amount:          12345.67,
		}},
		Total: report.Totals{
			WorkedSeconds:   444420,
			BillableSeconds: 844416,
			Amount:          12345.67,
		},
	}

	var buf bytes.Buffer
	renderReport(&buf, rep, color.Palette{}, 40)
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")

	// Line 0 is the header, line 1 the blank, line 2 the table header and
	// line 3 the single data row.
	if strings.Contains(lines[2], "Amount") {
		t.Errorf("table header = %q, want Amount dropped", lines[2])
	}
	if !strings.Contains(lines[3], "…") {
		t.Errorf("row = %q, want the long name truncated", lines[3])
	}
	for _, line := range lines {
		if got := rowRunes(line); got > 40 {
			t.Errorf("line is %d runes, want at most 40: %q", got, line)
		}
	}
}

// TestParseWeeks is the flag parser's table test. It is a pure function of a
// string, so it needs no workspace.
func TestParseWeeks(t *testing.T) {
	ok := []struct {
		raw  string
		want int
	}{
		{"0", 0},
		{"1", 1},
		{"3", 3},
		{"-1", 1},
		{"-3", 3},
		{"+2", 2},
	}
	for _, tt := range ok {
		got, err := parseWeeks(tt.raw)
		if err != nil {
			t.Errorf("parseWeeks(%q) = error %v, want nil", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseWeeks(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}

	for _, raw := range []string{"2.5", "abc", "", " 1", "1h"} {
		_, err := parseWeeks(raw)
		if err == nil {
			t.Errorf("parseWeeks(%q) = nil error, want a user error", raw)
			continue
		}
		want := `Invalid value for -w/--weeks: "` + raw + `". Pass a whole number of weeks to look back — for example: grind hwd -w 1`
		if err.Error() != want {
			t.Errorf("parseWeeks(%q) error = %q, want %q", raw, err.Error(), want)
		}
		if got := grinderr.ExitCode(err); got != 1 {
			t.Errorf("parseWeeks(%q) exit code = %d, want 1", raw, got)
		}
	}
}

// TestHwdBadWeeksValue is AC-039/AC-040 in spirit: every bad -w value is the
// same user error, printed before any report.
func TestHwdBadWeeksValue(t *testing.T) {
	for _, raw := range []string{"abc", "2.5", ""} {
		t.Run(raw, func(t *testing.T) {
			out, err := execute(t, &fakeGit{}, "hwd", "-w", raw)
			if err == nil {
				t.Fatal("expected error")
			}
			if got := grinderr.ExitCode(err); got != 1 {
				t.Errorf("exit code = %d, want 1", got)
			}
			want := `Invalid value for -w/--weeks: "` + raw + `". Pass a whole number of weeks to look back — for example: grind hwd -w 1`
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
			if strings.Contains(out, "Week:") {
				t.Errorf("a bad -w must print no report: %q", out)
			}
		})
	}
}

// TestHwdUnsupportedShorthand is AC-041: -1 is pflag's unknown shorthand, and
// the deviation from "exit 1" is pinned here so it stays deliberate. Cobra is
// silent (SilenceErrors), so the wording lives on the returned error.
func TestHwdUnsupportedShorthand(t *testing.T) {
	_, err := execute(t, &fakeGit{}, "hwd", "-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown shorthand flag: '1' in -1") {
		t.Errorf("error = %q, want pflag's shorthand error", err.Error())
	}
	if got := grinderr.ExitCode(err); got == 1 {
		t.Errorf("exit code = 1, want cobra's 99 (documented deviation from AC-041)")
	}
}

// TestHwdNameColor is AC-013: an active session outranks unbilled work.
func TestHwdNameColor(t *testing.T) {
	tests := []struct {
		name string
		row  report.Row
		want nameColor
	}{
		{"green wins over yellow", report.Row{RunningNow: true, HasUnbilled: true}, colorGreen},
		{"unbilled is yellow", report.Row{HasUnbilled: true}, colorYellow},
		{"neither is plain", report.Row{}, colorNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hwdNameColor(tt.row); got != tt.want {
				t.Errorf("hwdNameColor(%+v) = %v, want %v", tt.row, got, tt.want)
			}
		})
	}
}

// TestHwdWeeksFlagHelp is REQ-040: the help documents the single flag.
func TestHwdWeeksFlagHelp(t *testing.T) {
	out, err := execute(t, &fakeGit{}, "hwd", "--help")
	if err != nil {
		t.Fatalf("hwd --help: %v", err)
	}
	for _, want := range []string{"--weeks", "-w"} {
		if !strings.Contains(out, want) {
			t.Errorf("help = %q, missing %q", out, want)
		}
	}
}
