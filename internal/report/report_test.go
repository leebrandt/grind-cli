package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// newTestWorkspace writes .projects.json into a temp directory and returns the
// workspace that points at it. The report reads the file through the
// workspace path, so the test writes the file exactly like production would.
func newTestWorkspace(t *testing.T, projects config.ProjectsConfig) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: filepath.Join(dir, ".main"),
	}
	// WriteProjects stages its temp file inside .main, so the directory
	// must exist first.
	if err := os.MkdirAll(ws.MainWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	return ws
}

// at builds a local instant. Tests pin every date this way rather than
// calling time.Now, so the expectations are exact under any TZ.
func at(year int, month time.Month, day, hour, min int) time.Time {
	return time.Date(year, month, day, hour, min, 0, 0, time.Local)
}

// project builds one project entry with the given rate and sessions.
func project(name string, rate float64, sessions ...config.Session) config.ProjectEntry {
	return config.ProjectEntry{
		Name:      name,
		Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: rate},
		CreatedAt: at(2026, time.January, 1, 0, 0),
		Sessions:  sessions,
	}
}

// ended builds a finished session: start + duration seconds, rounded to
// rounded seconds.
func ended(start time.Time, duration, rounded int64, invoiced bool) config.Session {
	end := start.Add(time.Duration(duration) * time.Second)
	return config.Session{
		Start:    start,
		End:      &end,
		Duration: duration,
		Rounded:  rounded,
		Invoiced: invoiced,
	}
}

// running builds a session that is still active (End == nil).
func running(start time.Time) config.Session {
	return config.Session{Start: start}
}

func projectsWith(entries ...config.ProjectEntry) config.ProjectsConfig {
	cfg := config.DefaultProjects()
	for _, e := range entries {
		cfg.Projects[e.Name] = e
	}
	return cfg
}

func TestWindow(t *testing.T) {
	tests := []struct {
		name      string
		now       time.Time
		weeksBack int
		wantStart time.Time
		wantEnd   time.Time
		wantPast  bool
	}{
		{
			"wednesday current week",
			at(2026, time.November, 11, 14, 5), 0,
			at(2026, time.November, 8, 0, 0), at(2026, time.November, 11, 14, 5), false,
		},
		{
			"sunday starts that day",
			at(2026, time.November, 15, 14, 5), 0,
			at(2026, time.November, 15, 0, 0), at(2026, time.November, 15, 14, 5), false,
		},
		{
			"one week back",
			at(2026, time.November, 11, 14, 5), 1,
			at(2026, time.November, 1, 0, 0), at(2026, time.November, 8, 0, 0), true,
		},
		{
			"two weeks back",
			at(2026, time.November, 11, 14, 5), 2,
			at(2026, time.October, 25, 0, 0), at(2026, time.November, 1, 0, 0), true,
		},
		{
			"three weeks back",
			at(2026, time.November, 11, 14, 5), 3,
			at(2026, time.October, 18, 0, 0), at(2026, time.October, 25, 0, 0), true,
		},
		{
			"past week from a sunday",
			at(2026, time.November, 15, 14, 5), 1,
			at(2026, time.November, 8, 0, 0), at(2026, time.November, 15, 0, 0), true,
		},
		{
			"negative is the current week",
			at(2026, time.November, 11, 14, 5), -1,
			at(2026, time.November, 8, 0, 0), at(2026, time.November, 11, 14, 5), false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, past := Window(tt.now, tt.weeksBack)
			if !start.Equal(tt.wantStart) {
				t.Errorf("start = %v, want %v", start, tt.wantStart)
			}
			if !end.Equal(tt.wantEnd) {
				t.Errorf("end = %v, want %v", end, tt.wantEnd)
			}
			if past != tt.wantPast {
				t.Errorf("past = %v, want %v", past, tt.wantPast)
			}
		})
	}
}

// TestWindowSpansDST pins the calendar-arithmetic rule: a week that spans the
// autumn clock change is still seven local dates, so its duration is 169 hours,
// not 168.
func TestWindowSpansDST(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("America/Chicago not available:", err)
	}
	now := time.Date(2026, time.November, 11, 14, 5, 0, 0, chicago)
	start, end, past := Window(now, 1)
	if !past {
		t.Fatal("past = false, want true")
	}
	if got, want := end.Sub(start), 169*time.Hour; got != want {
		t.Errorf("end - start = %v, want %v", got, want)
	}
}

func TestInWindow(t *testing.T) {
	start := at(2026, time.November, 8, 0, 0)
	end := at(2026, time.November, 15, 0, 0)

	tests := []struct {
		name string
		t    time.Time
		past bool
		want bool
	}{
		{"at start is in", start, false, true},
		{"at start is in (past)", start, true, true},
		{"just before start is out", start.Add(-time.Second), false, false},
		{"at end is in for the current week", end, false, true},
		{"at end is out for a past week", end, true, false},
		{"just before end is in for a past week", end.Add(-time.Second), true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InWindow(tt.t, start, end, tt.past); got != tt.want {
				t.Errorf("InWindow(%v, past=%v) = %v, want %v", tt.t, tt.past, got, tt.want)
			}
		})
	}
}

func TestHeaderLine(t *testing.T) {
	t.Run("current week", func(t *testing.T) {
		got := HeaderLine(at(2026, time.November, 8, 0, 0), at(2026, time.November, 11, 14, 5), false)
		want := "Week: 2026-11-08 (Sun) through now"
		if got != want {
			t.Errorf("HeaderLine = %q, want %q", got, want)
		}
	})

	t.Run("past week", func(t *testing.T) {
		got := HeaderLine(at(2026, time.November, 1, 0, 0), at(2026, time.November, 8, 0, 0), true)
		want := "Week: 2026-11-01 (Sun) through 2026-11-07 (Sat)"
		if got != want {
			t.Errorf("HeaderLine = %q, want %q", got, want)
		}
	})

	t.Run("past week whose end is a sunday", func(t *testing.T) {
		got := HeaderLine(at(2026, time.November, 8, 0, 0), at(2026, time.November, 15, 0, 0), true)
		want := "Week: 2026-11-08 (Sun) through 2026-11-14 (Sat)"
		if got != want {
			t.Errorf("HeaderLine = %q, want %q", got, want)
		}
	})
}

// TestBuildAttributesSessionsByStart is AC-003: a session belongs to the week
// its Start falls in, so a session that began before the window and ended
// inside it is not counted.
func TestBuildAttributesSessionsByStart(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		ended(at(2026, time.November, 7, 23, 0), 3*3600, 3*3600, false),
		ended(at(2026, time.November, 9, 9, 0), 3*3600, 3*3600, false),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	if got, want := rep.Rows[0].WorkedSeconds, int64(3*3600); got != want {
		t.Errorf("WorkedSeconds = %d, want %d", got, want)
	}
}

// TestBuildRounding is AC-004: Worked is the raw Duration, Billable the
// Rounded value stored at end time.
func TestBuildRounding(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		ended(at(2026, time.November, 9, 9, 0), 15600, 16200, false),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	row := rep.Rows[0]
	if row.WorkedSeconds != 15600 {
		t.Errorf("WorkedSeconds = %d, want 15600", row.WorkedSeconds)
	}
	if row.BillableSeconds != 16200 {
		t.Errorf("BillableSeconds = %d, want 16200", row.BillableSeconds)
	}
}

// TestBuildAmount is AC-005: 13.00h billable at rate 150 is $1950.
func TestBuildAmount(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		ended(at(2026, time.November, 9, 9, 0), 13*3600, 13*3600, false),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Rows[0].Amount; got != 1950 {
		t.Errorf("Amount = %v, want 1950", got)
	}
	if rep.Total.Amount != 1950 {
		t.Errorf("Total.Amount = %v, want 1950", rep.Total.Amount)
	}
}

// TestBuildSortsByWorkedThenName is AC-006 and AC-007.
func TestBuildSortsByWorkedThenName(t *testing.T) {
	t.Run("worked descending", func(t *testing.T) {
		ws := newTestWorkspace(t, projectsWith(
			project("small", 150, ended(at(2026, time.November, 9, 9, 0), 4*3600, 4*3600, false)),
			project("big", 150, ended(at(2026, time.November, 9, 9, 0), 12*3600, 12*3600, false)),
		))
		rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
		if err != nil {
			t.Fatal(err)
		}
		if rep.Rows[0].Name != "big" {
			t.Errorf("first row = %q, want big", rep.Rows[0].Name)
		}
	})

	t.Run("name ascending breaks ties", func(t *testing.T) {
		ws := newTestWorkspace(t, projectsWith(
			project("zeta", 150, ended(at(2026, time.November, 9, 9, 0), 4*3600, 4*3600, false)),
			project("alpha", 150, ended(at(2026, time.November, 9, 9, 0), 4*3600, 4*3600, false)),
		))
		rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
		if err != nil {
			t.Fatal(err)
		}
		if rep.Rows[0].Name != "alpha" || rep.Rows[1].Name != "zeta" {
			t.Errorf("row order = %q, %q, want alpha, zeta", rep.Rows[0].Name, rep.Rows[1].Name)
		}
	})
}

// TestBuildMoneySplit is AC-009: the three money lines add up exactly.
func TestBuildMoneySplit(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		ended(at(2026, time.November, 9, 9, 0), 2*3600, 2*3600, true),
		ended(at(2026, time.November, 10, 9, 0), 2*3600, 2*3600, false),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total.BillableSeconds != 4*3600 {
		t.Errorf("BillableSeconds = %d, want 4h", rep.Total.BillableSeconds)
	}
	if rep.Money.InvoicedSeconds != 2*3600 {
		t.Errorf("InvoicedSeconds = %d, want 2h", rep.Money.InvoicedSeconds)
	}
	if rep.Money.UnbilledSeconds != 2*3600 {
		t.Errorf("UnbilledSeconds = %d, want 2h", rep.Money.UnbilledSeconds)
	}
	if got, want := rep.Money.InvoicedAmount+rep.Money.UnbilledAmount, rep.Total.Amount; got != want {
		t.Errorf("Invoiced + Unbilled = %v, want %v (Total)", got, want)
	}
}

// TestBuildZeroRate is AC-010: a zero-rate project is counted, not hidden.
func TestBuildZeroRate(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("freebie", 0,
		ended(at(2026, time.November, 9, 9, 0), 3*3600, 3*3600, false),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	if rep.Rows[0].Amount != 0 {
		t.Errorf("Amount = %v, want 0", rep.Rows[0].Amount)
	}
	if rep.Total.BillableSeconds != 3*3600 {
		t.Errorf("Total.BillableSeconds = %d, want 3h", rep.Total.BillableSeconds)
	}
	if rep.Total.Amount != 0 {
		t.Errorf("Total.Amount = %v, want 0", rep.Total.Amount)
	}
	if len(rep.NoRate) != 1 || rep.NoRate[0] != "freebie" {
		t.Errorf("NoRate = %v, want [freebie]", rep.NoRate)
	}
}

// TestBuildRunningSessionIsARow is AC-012: a running session gives the project
// a 0.00h row and adds nothing to the totals.
func TestBuildRunningSessionIsARow(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		running(at(2026, time.November, 9, 9, 0)),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	row := rep.Rows[0]
	if row.WorkedSeconds != 0 || row.BillableSeconds != 0 {
		t.Errorf("worked/billable = %d/%d, want 0/0", row.WorkedSeconds, row.BillableSeconds)
	}
	if !row.RunningNow {
		t.Error("RunningNow = false, want true")
	}
	if rep.Total.WorkedSeconds != 0 || rep.Total.BillableSeconds != 0 {
		t.Errorf("totals = %d/%d, want 0/0", rep.Total.WorkedSeconds, rep.Total.BillableSeconds)
	}
}

// TestBuildHasUnbilledIsWorkspaceWide: yellow means the same thing in hwd and
// wwd, so an un-invoiced session in any week sets the flag.
func TestBuildHasUnbilledIsWorkspaceWide(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		// In the window, already invoiced: gives the project a row.
		ended(at(2026, time.November, 9, 9, 0), 3*3600, 3*3600, true),
		// Outside the window, un-invoiced: sets HasUnbilled anyway.
		ended(at(2026, time.October, 1, 9, 0), 3*3600, 3*3600, false),
	)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rep.Rows))
	}
	if !rep.Rows[0].HasUnbilled {
		t.Error("HasUnbilled = false, want true (workspace-wide)")
	}
}

// TestBuildFinishedInWindow is AC-014: a published project whose timestamp is
// in the window appears in Finished with the local date.
func TestBuildFinishedInWindow(t *testing.T) {
	ts := at(2026, time.November, 10, 16, 0)
	entry := project("my-blog", 150, ended(at(2026, time.November, 9, 9, 0), 3*3600, 3*3600, false))
	entry.Status = "published"
	entry.PublishedAt = &ts
	ws := newTestWorkspace(t, projectsWith(entry))

	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Finished) != 1 {
		t.Fatalf("finished = %d, want 1", len(rep.Finished))
	}
	f := rep.Finished[0]
	if f.Status != "published" {
		t.Errorf("Status = %q, want published", f.Status)
	}
	if got := f.Date.Format("2006-01-02"); got != "2026-11-10" {
		t.Errorf("Date = %q, want 2026-11-10", got)
	}
	if !f.HasWork {
		t.Error("HasWork = false, want true")
	}
}

// TestBuildFinishedNotInWindow is AC-017: a project published in a previous
// week is in neither Finished nor Earlier.
func TestBuildFinishedNotInWindow(t *testing.T) {
	ts := at(2026, time.November, 2, 16, 0) // the week before
	entry := project("my-blog", 150)
	entry.Status = "published"
	entry.PublishedAt = &ts
	ws := newTestWorkspace(t, projectsWith(entry))

	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Finished) != 0 {
		t.Errorf("Finished = %v, want empty", rep.Finished)
	}
	if len(rep.Earlier) != 0 {
		t.Errorf("Earlier = %v, want empty", rep.Earlier)
	}
}

// TestBuildFinishedEarlier is AC-018: a finished project with no timestamp is
// listed once as unrecorded.
func TestBuildFinishedEarlier(t *testing.T) {
	entry := project("old-thing", 150)
	entry.Status = "published"
	ws := newTestWorkspace(t, projectsWith(entry))

	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Finished) != 0 {
		t.Errorf("Finished = %v, want empty", rep.Finished)
	}
	if len(rep.Earlier) != 1 || rep.Earlier[0].Name != "old-thing" || rep.Earlier[0].Status != "published" {
		t.Errorf("Earlier = %v, want [{old-thing published}]", rep.Earlier)
	}
}

// TestBuildFinishedUsesStatusTimestamp: the date always describes the project's
// CURRENT status, so a canceled project reports CanceledAt, not PublishedAt.
func TestBuildFinishedUsesStatusTimestamp(t *testing.T) {
	published := at(2026, time.November, 9, 10, 0)
	canceled := at(2026, time.November, 10, 10, 0)
	entry := project("side-quest", 150)
	entry.Status = "canceled"
	entry.PublishedAt = &published
	entry.CanceledAt = &canceled
	ws := newTestWorkspace(t, projectsWith(entry))

	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Finished) != 1 {
		t.Fatalf("finished = %d, want 1", len(rep.Finished))
	}
	if got := rep.Finished[0].Date.Format("2006-01-02"); got != "2026-11-10" {
		t.Errorf("Date = %q, want 2026-11-10 (CanceledAt)", got)
	}
}

// TestBuildFinishedNoWork is AC-016: a project published in the window with no
// work in the window has HasWork false.
func TestBuildFinishedNoWork(t *testing.T) {
	ts := at(2026, time.November, 10, 16, 0)
	entry := project("my-blog", 150)
	entry.Status = "published"
	entry.PublishedAt = &ts
	ws := newTestWorkspace(t, projectsWith(entry))

	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Finished) != 1 {
		t.Fatalf("finished = %d, want 1", len(rep.Finished))
	}
	if rep.Finished[0].HasWork {
		t.Error("HasWork = true, want false")
	}
}

// TestBuildPastWeekInvoicedNow is AC-043: for a past week the invoiced split
// reflects today's flags, so a session invoiced after the week ended lands in
// Invoiced, not Unbilled.
func TestBuildPastWeekInvoicedNow(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150,
		ended(at(2026, time.November, 3, 9, 0), 2*3600, 2*3600, true),
	)))
	rep, err := Build(ws, Options{WeeksBack: 1}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Money.InvoicedSeconds != 2*3600 {
		t.Errorf("InvoicedSeconds = %d, want 2h", rep.Money.InvoicedSeconds)
	}
	if rep.Money.UnbilledSeconds != 0 {
		t.Errorf("UnbilledSeconds = %d, want 0", rep.Money.UnbilledSeconds)
	}
}

// TestBuildEmptyWeek is AC-038: no sessions anywhere is an empty report, not
// an error.
func TestBuildEmptyWeek(t *testing.T) {
	ws := newTestWorkspace(t, projectsWith(project("my-blog", 150)))
	rep, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rep.Rows))
	}
	if rep.Total != (Totals{}) {
		t.Errorf("Total = %+v, want zero", rep.Total)
	}
	if len(rep.Finished) != 0 || len(rep.Earlier) != 0 {
		t.Errorf("finished/earlier = %d/%d, want 0/0", len(rep.Finished), len(rep.Earlier))
	}
}

// TestBuildMissingProjectsFile is AC-026: a missing file is the existing
// system error, exit 2.
func TestBuildMissingProjectsFile(t *testing.T) {
	dir := t.TempDir()
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: filepath.Join(dir, ".main"),
	}
	if err := os.MkdirAll(ws.MainWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Build(ws, Options{}, at(2026, time.November, 11, 14, 5))
	if err == nil {
		t.Fatal("expected error")
	}
	if got := grinderr.ExitCode(err); got != 2 {
		t.Errorf("ExitCode = %d, want 2", got)
	}
}
