package invoice

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records the commits the invoice service makes so tests can assert
// exactly what was staged. Every other method is a no-op stub: the invoice
// slice adds no git operations beyond Commit.
type fakeGit struct {
	commits []fakeCommit
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error { return nil }

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) { return true, nil }

func (f *fakeGit) CreateBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) { return false, nil }

func (f *fakeGit) CommitAll(worktreePath, message string) error { return nil }

func (f *fakeGit) RemoteURL(repoPath string) (string, error) { return "", nil }

func (f *fakeGit) PushAll(repoPath string) error { return nil }

func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeGit) DefaultBranch(repoPath string) (string, error) { return "main", nil }

func (f *fakeGit) SetRemoteURL(repoPath, url string) error { return nil }

func (f *fakeGit) PushBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) FetchAll(repoPath string) error { return nil }

func (f *fakeGit) IsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	return false, nil
}

func (f *fakeGit) FastForwardWorktree(worktreePath, branch string) error { return nil }

func (f *fakeGit) FastForwardRef(repoPath, branch string) error { return nil }

func (f *fakeGit) ListRemoteBranches(repoPath string) ([]string, error) { return nil, nil }

func (f *fakeGit) MergeBranch(worktreePath, branch string) error { return nil }

func (f *fakeGit) RemoveWorktree(repoPath, worktreePath string) error { return nil }

func (f *fakeGit) DeleteBranch(repoPath, branch string) error { return nil }

// Ensure the stub satisfies the interface the service depends on.
var _ git.Git = (*fakeGit)(nil)

// newTestWorkspace builds a workspace with projects on disk, written
// exactly as production writes it. cfg is written only when non-nil, so a
// test can exercise the "workspace has no .grind.json" fallback.
func newTestWorkspace(t *testing.T, projects config.ProjectsConfig, cfg *config.GrindConfig) *workspace.Workspace {
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
	if cfg != nil {
		if err := config.Write(ws.GrindConfigPath(), *cfg); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// localDay returns noon on the day offsetDays from today, in local time.
//
// Every date in these tests is derived from the clock, never hardcoded: a
// literal like "2026-09-20" silently drifts into the past (or the future)
// as the calendar moves, which turns a green suite red with no code change
// (this repo shipped that bug in commit 92e63ec). Noon pins the time of day
// so a session can never slide across a local midnight and land on the
// wrong day in the breakdown.
func localDay(offsetDays int) time.Time {
	t := time.Now().AddDate(0, 0, offsetDays)
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.Local)
}

// endedSession builds a finished session: it started at start and is billed
// for rounded seconds.
func endedSession(start time.Time, rounded int64) config.Session {
	end := start.Add(time.Duration(rounded) * time.Second)
	return config.Session{
		Start:    start,
		End:      &end,
		Duration: rounded,
		Rounded:  rounded,
	}
}

// endedAt pins a session's End pointer, for fixtures where the duration
// does not matter.
func endedAt(t time.Time) *time.Time {
	return &t
}

// readProjectsFile re-reads .projects.json from disk, which is how a test
// checks what was actually persisted rather than what was in memory.
func readProjectsFile(t *testing.T, ws *workspace.Workspace) config.ProjectsConfig {
	t.Helper()
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatalf("read .projects.json: %v", err)
	}
	return projects
}

func TestDaysFromPaymentTerms(t *testing.T) {
	// This is the v1 bug #4 regression guard: v1 hardcoded +30 days, so
	// "Net 7" produced a 30-day due date. Unrecognized terms still fall
	// back to 30 — an unknown term must never date the invoice in the past.
	tests := []struct {
		terms string
		want  int
	}{
		{"Net 30", 30},
		{"Net 7", 7},
		{"net 15", 15},
		{"NET 45", 45},
		{"Due on receipt", 0},
		{"due on receipt", 0},
		{"weird", 30},
		{"", 30},
		{"  Net 7  ", 7},
		{"Net  7", 7},
		{"30", 30},
	}
	for _, tt := range tests {
		t.Run(tt.terms, func(t *testing.T) {
			if got := DaysFromPaymentTerms(tt.terms); got != tt.want {
				t.Errorf("DaysFromPaymentTerms(%q) = %d, want %d", tt.terms, got, tt.want)
			}
		})
	}
}

func TestSymbolIsUSD(t *testing.T) {
	// grind bills in USD only. There is no currency setting, so this is a
	// constant rather than a lookup — pinning it here documents the
	// decision and fails loudly if anyone reintroduces a code.
	if Symbol != "$" {
		t.Errorf("Symbol = %q, want $", Symbol)
	}
}

func TestDayLinesGroupsByLocalDate(t *testing.T) {
	// The input is deliberately OUT of date order. Go randomizes map
	// iteration, so an unsorted grouping would emit a different row order
	// on every run and the committed invoice would churn. Asserting the
	// output is ascending is the regression guard for that.
	lines := dayLines([]config.Session{
		endedSession(localDay(-1), 9000),
		endedSession(localDay(-3), 1800),
		endedSession(localDay(-2), 3600),
		endedSession(localDay(-2).Add(2*time.Hour), 1800),
	})

	want := []DayLine{
		{Date: localDay(-3).Format("2006-01-02"), Seconds: 1800, Hours: 0.5},
		{Date: localDay(-2).Format("2006-01-02"), Seconds: 5400, Hours: 1.5},
		{Date: localDay(-1).Format("2006-01-02"), Seconds: 9000, Hours: 2.5},
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %d, want %d", len(lines), len(want))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %+v, want %+v", i, lines[i], want[i])
		}
	}
}

func TestDayLinesEmpty(t *testing.T) {
	if lines := dayLines(nil); len(lines) != 0 {
		t.Errorf("dayLines(nil) = %+v, want no lines", lines)
	}
}

func TestBlock(t *testing.T) {
	// Empty fields are dropped so a half-filled address does not render as
	// blank lines, and a completely empty block becomes the hint that names
	// the exact command which fills it in.
	got := block([]string{"ACME Consulting", "", "Lee Brandt", ""}, "(not configured)")
	if got != "ACME Consulting\nLee Brandt" {
		t.Errorf("block() = %q", got)
	}
	if got := block([]string{"", ""}, "(not configured)"); got != "(not configured)" {
		t.Errorf("block() with no fields = %q, want the hint", got)
	}
}

func TestRenderMarkdown(t *testing.T) {
	inv := &Invoice{
		ProjectName:  "my-blog",
		Description:  "Write a blog post about Rust memory management",
		From:         "ACME Consulting\nLee Brandt\nTax ID: GB123456789",
		To:           "Client Corp\nJane Doe\njane@client.example",
		Rate:         150,
		PaymentTerms: "Net 30",
		InvoiceDate:  "2026-09-29",
		DueDate:      "2026-10-29",
		ID:           "20260929T14-30-15",
		Lines: []DayLine{
			{Date: "2026-09-14", Seconds: 12600, Hours: 3.5, Amount: 525},
			{Date: "2026-09-15", Seconds: 13500, Hours: 3.75, Amount: 562.5},
		},
		TotalSeconds: 26100,
		TotalHours:   7.25,
		Subtotal:     1087.5,
	}

	// The file is a record a client receives, so its exact bytes are part
	// of the contract: frontmatter values, block order, table columns,
	// two-decimal formatting, and the trailing newline.
	want := `---
invoiceId: 20260929T14-30-15
date: 2026-09-29
due: 2026-10-29
project: my-blog
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
`

	got := renderMarkdown(inv)
	if got != want {
		t.Errorf("renderMarkdown() mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMarkdownEmptyBlocks(t *testing.T) {
	// A workspace with no professional info and a project with no client
	// still produces a valid invoice: each block becomes a hint naming the
	// exact config command that fills it in.
	inv := &Invoice{
		ProjectName:  "my-blog",
		Description:  "My Blog",
		From:         `(not configured — run grind config my.company "Your Company")`,
		To:           `(not configured — run grind config my-blog client.company "Client Name")`,
		Rate:         150,
		PaymentTerms: "Net 30",
		InvoiceDate:  "2026-09-29",
		DueDate:      "2026-10-29",
		ID:           "20260929T14-30-15",
		Lines: []DayLine{
			{Date: "2026-09-14", Seconds: 1800, Hours: 0.5, Amount: 75},
		},
		TotalSeconds: 1800,
		TotalHours:   0.5,
		Subtotal:     75,
	}

	got := renderMarkdown(inv)
	for _, want := range []string{
		`## FROM` + "\n" + `(not configured — run grind config my.company "Your Company")`,
		`## TO` + "\n" + `(not configured — run grind config my-blog client.company "Client Name")`,
		"| 2026-09-14 | 0.50 | $150.00 | $75.00 |",
		"**Total**: $75.00",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderMarkdown() missing %q:\n%s", want, got)
		}
	}
}

func TestIDHasNoColons(t *testing.T) {
	// The ID doubles as a directory name. Colons are illegal in file names
	// on Windows and awkward everywhere, which is why the layout uses dashes
	// — and why the ID can be a path component at all.
	id := time.Date(2026, 9, 29, 14, 30, 15, 0, time.Local).Format(idLayout)
	if id != "20260929T14-30-15" {
		t.Errorf("id = %q, want 20260929T14-30-15", id)
	}
}

// workspaceCfg returns a .grind.json with professional info, a currency,
// and payment terms — the shape a fully configured workspace has.
func workspaceCfg() *config.GrindConfig {
	cfg := config.Default()
	cfg.My = &config.MyConfig{
		Company: "ACME Consulting",
		Name:    "Lee Brandt",
		TaxID:   "GB123456789",
	}
	cfg.PaymentTerms = "Net 30"
	return &cfg
}

// myBlogEntry is the project under test: three ended sessions across two
// local days, one already-invoiced session, and one active session.
func myBlogEntry() config.ProjectEntry {
	return config.ProjectEntry{
		Name:    "my-blog",
		Idea:    "Write a blog post about Rust memory management",
		Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		Client: &config.ClientInfo{
			Company: "Client Corp",
			Contact: "Jane Doe",
			Email:   "jane@client.example",
		},
		Sessions: []config.Session{
			endedSession(localDay(-2), 10800),
			endedSession(localDay(-2).Add(3*time.Hour), 1800),
			endedSession(localDay(-1), 9000),
			{Start: localDay(-1).Add(2 * time.Hour), End: nil},
			func() config.Session {
				s := endedSession(localDay(-4), 3600)
				s.Invoiced = true
				return s
			}(),
		},
	}
}

func TestGenerateHappyPath(t *testing.T) {
	entry := myBlogEntry()
	projects := config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": entry},
	}
	ws := newTestWorkspace(t, projects, workspaceCfg())
	fake := &fakeGit{}
	svc := NewService(fake)
	now := time.Now()

	inv, err := svc.Generate(ws, "my-blog", false, now)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv == nil {
		t.Fatal("Generate returned no invoice")
	}

	// Only the three ended, uninvoiced sessions are billed: 3h + 0.5h on
	// one day and 2.5h on the next.
	if inv.SessionCount != 3 {
		t.Errorf("SessionCount = %d, want 3", inv.SessionCount)
	}
	if inv.TotalSeconds != 21600 {
		t.Errorf("TotalSeconds = %d, want 21600 (6h)", inv.TotalSeconds)
	}
	if inv.TotalHours != 6 {
		t.Errorf("TotalHours = %v, want 6", inv.TotalHours)
	}
	if inv.Subtotal != 900 {
		t.Errorf("Subtotal = %v, want 900 (6h at 150/hr)", inv.Subtotal)
	}
	if len(inv.Lines) != 2 {
		t.Fatalf("lines = %d, want 2 (grouped by day)", len(inv.Lines))
	}
	if inv.Lines[0].Date != localDay(-2).Format("2006-01-02") {
		t.Errorf("lines[0].Date = %q, want the earlier day", inv.Lines[0].Date)
	}
	if inv.Lines[0].Seconds != 12600 || inv.Lines[0].Amount != 525 {
		t.Errorf("lines[0] = %+v, want 12600s / 525.00", inv.Lines[0])
	}

	// The due date honors paymentTerms (Net 30) in LOCAL time.
	wantDue := now.AddDate(0, 0, 30).Format("2006-01-02")
	if inv.DueDate != wantDue {
		t.Errorf("DueDate = %q, want %q", inv.DueDate, wantDue)
	}
	if inv.InvoiceDate != now.Format("2006-01-02") {
		t.Errorf("InvoiceDate = %q, want today", inv.InvoiceDate)
	}

	// The invoice file lives at invoices/<project>/<id>/invoice.md.
	id := now.Format(idLayout)
	if inv.ID != id {
		t.Errorf("ID = %q, want %q", inv.ID, id)
	}
	path := filepath.Join(ws.InvoiceDir("my-blog", id), "invoice.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invoice file: %v", err)
	}
	if string(content) != renderMarkdown(inv) {
		t.Errorf("invoice file does not match renderMarkdown():\n%s", content)
	}

	// Exactly the billed sessions are marked; the already-invoiced one and
	// the active one keep their state. The flag is never a deletion.
	saved := readProjectsFile(t, ws).Projects["my-blog"]
	if len(saved.Sessions) != len(entry.Sessions) {
		t.Fatalf("sessions = %d, want %d (marking must not delete)", len(saved.Sessions), len(entry.Sessions))
	}
	invoicedCount := 0
	for i, sess := range saved.Sessions {
		if sess.Invoiced {
			invoicedCount++
		}
		if i == len(saved.Sessions)-2 && sess.Invoiced {
			t.Error("the active session was marked invoiced")
		}
	}
	if invoicedCount != 4 {
		t.Errorf("invoiced sessions = %d, want 4 (3 billed + 1 already invoiced)", invoicedCount)
	}

	// One commit, staging only the two paths Generate wrote.
	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	commit := fake.commits[0]
	if commit.worktree != ws.MainWorktree {
		t.Errorf("commit worktree = %q, want the main worktree", commit.worktree)
	}
	if commit.message != "Invoice "+id+" for my-blog" {
		t.Errorf("commit message = %q", commit.message)
	}
	wantPaths := []string{".projects.json", "invoices/my-blog/" + id + "/invoice.md"}
	if len(commit.paths) != len(wantPaths) {
		t.Fatalf("commit paths = %v, want %v", commit.paths, wantPaths)
	}
	for i := range wantPaths {
		if commit.paths[i] != wantPaths[i] {
			t.Errorf("commit paths = %v, want %v", commit.paths, wantPaths)
		}
	}
}

func TestGenerateBillsRoundedNotDuration(t *testing.T) {
	// Rounded is frozen when the session ends, so a later change to
	// billing.roundTo cannot retroactively alter an issued invoice. The
	// amount comes from Rounded; Duration is raw wall-clock time and is for
	// display only.
	start := localDay(-1)
	end := start.Add(time.Hour)
	entry := config.ProjectEntry{
		Name:    "my-blog",
		Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: 100},
		Sessions: []config.Session{
			{Start: start, End: &end, Duration: 3600, Rounded: 900},
		},
	}
	projects := config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": entry},
	}
	ws := newTestWorkspace(t, projects, workspaceCfg())
	svc := NewService(&fakeGit{})

	inv, err := svc.Generate(ws, "my-blog", false, time.Now())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv.TotalSeconds != 900 {
		t.Errorf("TotalSeconds = %d, want 900 (the rounded value)", inv.TotalSeconds)
	}
	if inv.Subtotal != 25 {
		t.Errorf("Subtotal = %v, want 25 (0.25h at 100/hr)", inv.Subtotal)
	}
}

func TestGenerateNoBillableSessions(t *testing.T) {
	// An active session and an already-invoiced one leave nothing to bill.
	// That is a success, not an error: (nil, nil) tells the caller to
	// report "nothing to bill" and exit 0.
	end := localDay(-1).Add(time.Hour)
	entry := config.ProjectEntry{
		Name:    "my-blog",
		Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		Sessions: []config.Session{
			{Start: localDay(-1), End: nil},
			{Start: localDay(-2), End: &end, Duration: 3600, Rounded: 3600, Invoiced: true},
		},
	}
	projects := config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": entry},
	}
	ws := newTestWorkspace(t, projects, workspaceCfg())
	before, err := os.ReadFile(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{}
	svc := NewService(fake)

	inv, err := svc.Generate(ws, "my-blog", false, time.Now())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv != nil {
		t.Errorf("invoice = %+v, want nil", inv)
	}
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0", len(fake.commits))
	}
	after, err := os.ReadFile(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error(".projects.json was rewritten even though there was nothing to bill")
	}
}

func TestGenerateDryRun(t *testing.T) {
	projects := config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": myBlogEntry()},
	}
	ws := newTestWorkspace(t, projects, workspaceCfg())
	before, err := os.ReadFile(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{}
	svc := NewService(fake)

	inv, err := svc.Generate(ws, "my-blog", true, time.Now())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv == nil {
		t.Fatal("dry run returned no invoice")
	}
	// The numbers are real so the user can check them; the side effects
	// are not: no file, no state change, no commit.
	if inv.Subtotal != 900 {
		t.Errorf("Subtotal = %v, want 900", inv.Subtotal)
	}
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0 (dry run must not commit)", len(fake.commits))
	}
	if _, err := os.Stat(ws.InvoiceDir("my-blog", inv.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invoice directory was created during a dry run: %v", err)
	}
	after, err := os.ReadFile(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error(".projects.json was rewritten during a dry run")
	}
}

func TestGenerateIgnoresProjectStatus(t *testing.T) {
	// Canceling a project does not erase the work that was done on it, and
	// the client may still owe for it — so billing works on canceled and
	// published projects alike.
	for _, status := range []string{"canceled", "published"} {
		t.Run(status, func(t *testing.T) {
			entry := myBlogEntry()
			entry.Status = status
			projects := config.ProjectsConfig{
				Version:  1,
				Projects: map[string]config.ProjectEntry{"my-blog": entry},
			}
			ws := newTestWorkspace(t, projects, workspaceCfg())
			svc := NewService(&fakeGit{})

			inv, err := svc.Generate(ws, "my-blog", false, time.Now())
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if inv == nil {
				t.Fatal("no invoice generated for a " + status + " project")
			}
			if inv.Subtotal != 900 {
				t.Errorf("Subtotal = %v, want 900", inv.Subtotal)
			}
		})
	}
}

func TestGenerateDefaultPaymentTerms(t *testing.T) {
	// A workspace with no .grind.json at all (or one with no payment terms)
	// still bills: "Net 30" is the default, and the due date follows from it.
	projects := config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": myBlogEntry()},
	}
	ws := newTestWorkspace(t, projects, nil)
	svc := NewService(&fakeGit{})
	now := time.Now()

	inv, err := svc.Generate(ws, "my-blog", true, now)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv.PaymentTerms != "Net 30" {
		t.Errorf("PaymentTerms = %q, want %q", inv.PaymentTerms, "Net 30")
	}
	wantDue := now.AddDate(0, 0, 30).Format("2006-01-02")
	if inv.DueDate != wantDue {
		t.Errorf("DueDate = %q, want %q", inv.DueDate, wantDue)
	}
	// An unconfigured FROM block becomes the hint, not an empty section.
	if !strings.Contains(inv.From, "not configured") {
		t.Errorf("From = %q, want the not-configured hint", inv.From)
	}
}

func TestGenerateUnknownProject(t *testing.T) {
	ws := newTestWorkspace(t, config.DefaultProjects(), workspaceCfg())
	svc := NewService(&fakeGit{})

	_, err := svc.Generate(ws, "nope", false, time.Now())
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
	// The wording is shared with work/save so the user learns one message.
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Errorf("error type = %T, want *grinderr.User (exit 1)", err)
	}
}

func TestGenerateUnknownProjectNoProjectsFile(t *testing.T) {
	// A workspace with no .projects.json has no projects; the answer is the
	// same user error, not a system error.
	dir := t.TempDir()
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: filepath.Join(dir, ".main"),
	}
	if err := os.MkdirAll(ws.MainWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&fakeGit{})

	_, err := svc.Generate(ws, "nope", false, time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestSummarize(t *testing.T) {
	// `show --billing` splits the same work the invoice bills: 12.75h
	// already invoiced, 7.25h still to bill, 20h in total.
	entry := config.ProjectEntry{
		Name:    "my-blog",
		Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		Sessions: []config.Session{
			{Start: localDay(-3), End: endedAt(localDay(-3)), Rounded: 12600, Invoiced: true},
			{Start: localDay(-2), End: endedAt(localDay(-2)), Rounded: 33300, Invoiced: true},
			{Start: localDay(-1), End: endedAt(localDay(-1)), Rounded: 26100},
			// An active session has Rounded 0, so it contributes nothing.
			{Start: localDay(-1), End: nil},
		},
	}

	got := Summarize(entry)
	if got.SessionCount != 4 {
		t.Errorf("SessionCount = %d, want 4", got.SessionCount)
	}
	if got.TotalSeconds != 72000 {
		t.Errorf("TotalSeconds = %d, want 72000 (20h)", got.TotalSeconds)
	}
	if got.BilledSeconds != 45900 {
		t.Errorf("BilledSeconds = %d, want 45900 (12.75h)", got.BilledSeconds)
	}
	if got.UnbilledSeconds != 26100 {
		t.Errorf("UnbilledSeconds = %d, want 26100 (7.25h)", got.UnbilledSeconds)
	}
	if got.TotalAmount != 3000 {
		t.Errorf("TotalAmount = %v, want 3000", got.TotalAmount)
	}
	if got.BilledAmount != 1912.5 {
		t.Errorf("BilledAmount = %v, want 1912.50", got.BilledAmount)
	}
	if got.UnbilledAmount != 1087.5 {
		t.Errorf("UnbilledAmount = %v, want 1087.50", got.UnbilledAmount)
	}
}

func TestGenerateDatesComeFromNow(t *testing.T) {
	ws := newTestWorkspace(t, config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": myBlogEntry()},
	}, workspaceCfg())

	// Every date on the invoice is derived from the `now` argument, never
	// from a call to time.Now() inside this package. Pinning the argument
	// pins all three dates, which is what makes the local-day and
	// payment-terms rules testable at all.
	now := time.Date(2026, 3, 1, 9, 30, 0, 0, time.Local)
	inv, err := NewService(&fakeGit{}).Generate(ws, "my-blog", true, now)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv.InvoiceDate != "2026-03-01" {
		t.Errorf("InvoiceDate = %q, want 2026-03-01", inv.InvoiceDate)
	}
	if inv.DueDate != "2026-03-31" {
		t.Errorf("DueDate = %q, want 2026-03-31 (Net 30)", inv.DueDate)
	}
	if inv.ID != "20260301T09-30-00" {
		t.Errorf("ID = %q, want 20260301T09-30-00", inv.ID)
	}
}

func TestSummarizeNoSessions(t *testing.T) {
	got := Summarize(config.ProjectEntry{Name: "my-blog"})
	if got.SessionCount != 0 || got.TotalSeconds != 0 || got.TotalAmount != 0 {
		t.Errorf("Summarize() of an empty project = %+v, want zeros", got)
	}
}

// The tests above use a fake git, which proves WHICH paths Generate asks to
// stage but not that git accepts them. This one runs against a real
// repository so the design constitution's promise — "the main worktree is
// clean after every command" — is checked against git itself rather than
// against a stub that agrees with us.

// setGitIdentity makes `git commit` work in tests without depending on the
// developer's global git config.
func setGitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Grind Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "grind-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Grind Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "grind-test@example.com")
}

// gitOutput runs a git command in dir and returns its output. It uses
// os/exec with an argv array, exactly like the production git layer: no
// shell, so nothing here can be rewritten by a filename.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func TestGenerateWithRealGit(t *testing.T) {
	setGitIdentity(t)
	g := git.New()
	root := t.TempDir()
	bareRepo := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bareRepo); err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		t.Fatalf("InitialCommit: %v", err)
	}
	main := filepath.Join(root, ".main")
	if err := g.AddWorktree(bareRepo, main, "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	ws := &workspace.Workspace{Root: root, BareRepo: bareRepo, MainWorktree: main}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{"my-blog": myBlogEntry()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.Write(ws.GrindConfigPath(), *workspaceCfg()); err != nil {
		t.Fatal(err)
	}
	// workspace.Init commits the config files, so the workspace starts
	// clean — otherwise the "clean afterwards" assertion below would only
	// be measuring this setup's leftovers.
	if err := g.Commit(main, "Initialize grind workspace", ".grind.json", ".projects.json"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	inv, err := NewService(g).Generate(ws, "my-blog", false, time.Now())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// The commit must contain exactly the two paths Generate wrote, and
	// nothing else: no `git add -A` sweep of an unrelated edit.
	committed := gitOutput(t, main, "show", "--name-only", "--format=", "HEAD")
	gotPaths := strings.Fields(committed)
	wantPaths := []string{".projects.json", "invoices/my-blog/" + inv.ID + "/invoice.md"}
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("commit contains %v, want %v", gotPaths, wantPaths)
	}
	for i := range wantPaths {
		if gotPaths[i] != wantPaths[i] {
			t.Errorf("commit contains %v, want %v", gotPaths, wantPaths)
		}
	}

	// The headline invariant: .main is clean afterwards.
	if status := gitOutput(t, main, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf(".main is not clean after Generate:\n%s", status)
	}
}
