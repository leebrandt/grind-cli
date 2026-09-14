package projects

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// addProject writes a project entry into .projects.json with the given
// sessions, so session tests start from a known state.
func addProject(t *testing.T, ws *workspace.Workspace, name string, sessions ...config.Session) {
	t.Helper()
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := config.ProjectEntry{
		Name:      name,
		Type:      "blog",
		Idea:      "# Test\n",
		Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Sessions:  sessions,
	}
	projects.Projects[name] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
}

// activeSession builds a session that is still running (End == nil).
func activeSession(start time.Time) config.Session {
	return config.Session{Start: start}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  float64
		err   bool
	}{
		{"5", 5, false},
		{"5h", 5, false},
		{"1.5h", 1.5, false},
		{"90m", 1.5, false},
		{"1h30m", 1.5, false},
		{"1H30M", 1.5, false},
		{"30", 30, false},
		{"0", 0, true},
		{"0h", 0, true},
		{"-5", 0, true},
		{"abc", 0, true},
		{"", 0, true},
		{"1h30", 0, true},
		{"1.5", 1.5, false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDuration(tt.input)
			if tt.err {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %v, want error", tt.input, got)
				}
				var user *grinderr.User
				if !errors.As(err, &user) {
					t.Errorf("ParseDuration(%q) error type = %T, want *grinderr.User", tt.input, err)
				}
				if !strings.Contains(err.Error(), "Backfill time must be a positive duration") {
					t.Errorf("ParseDuration(%q) error = %q", tt.input, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestRoundTime(t *testing.T) {
	tests := []struct {
		name     string
		duration int64
		roundTo  string
		want     int64
	}{
		{"quarter exact", 900, "quarter-hour", 900},
		{"quarter ceil", 901, "quarter-hour", 1800},
		{"quarter small", 1, "quarter-hour", 900},
		{"quarter zero", 0, "quarter-hour", 0},
		{"half exact", 1800, "half-hour", 1800},
		{"half ceil", 1801, "half-hour", 3600},
		{"hour exact", 3600, "hour", 3600},
		{"hour ceil", 3601, "hour", 7200},
		{"unknown falls back to quarter", 901, "fortnight", 1800},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := roundTime(tt.duration, tt.roundTo); got != tt.want {
				t.Errorf("roundTime(%d, %q) = %d, want %d", tt.duration, tt.roundTo, got, tt.want)
			}
		})
	}
}

func TestFormatHours(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{5400, "1.50"},
		{7200, "2.00"},
		{28800, "8.00"},
		{0, "0.00"},
	}
	for _, tt := range tests {
		if got := FormatHours(tt.seconds); got != tt.want {
			t.Errorf("FormatHours(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestStartSessionNew(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	session, started, err := svc.StartSession(ws, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Error("started = false, want true for new session")
	}
	if session.Start.IsZero() {
		t.Error("Start is zero")
	}
	if session.End != nil {
		t.Error("End = non-nil, want nil for active session")
	}

	// The session must be persisted in .projects.json.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := projects.Projects["my-blog"]
	if len(entry.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(entry.Sessions))
	}
	if entry.Sessions[0].End != nil {
		t.Error("stored session has End set, want nil")
	}

	// Exactly one commit: .projects.json with the start message.
	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.message != "Start session on my-blog" {
		t.Errorf("commit message = %q", c.message)
	}
	if len(c.paths) != 1 || c.paths[0] != ".projects.json" {
		t.Errorf("commit paths = %v", c.paths)
	}
}

func TestStartSessionContinuesExisting(t *testing.T) {
	ws := newTestWorkspace(t)
	start := time.Date(2026, 9, 13, 14, 30, 0, 0, time.UTC)
	addProject(t, ws, "my-blog", activeSession(start))
	fake := newFakeGit()
	svc := NewService(fake)

	session, started, err := svc.StartSession(ws, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if started {
		t.Error("started = true, want false when continuing")
	}
	if !session.Start.Equal(start) {
		t.Errorf("Start = %v, want %v", session.Start, start)
	}

	// The orphan-bug regression: no second session may be appended.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects.Projects["my-blog"].Sessions) != 1 {
		t.Errorf("sessions = %d, want 1 (no duplicate)", len(projects.Projects["my-blog"].Sessions))
	}

	// Continuing must not commit anything.
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0", len(fake.commits))
	}
}

func TestStartSessionUnknownProject(t *testing.T) {
	ws := newTestWorkspace(t)
	svc := NewService(newFakeGit())

	_, _, err := svc.StartSession(ws, "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestEndSessionEndsNow(t *testing.T) {
	ws := newTestWorkspace(t)
	start := time.Date(2026, 9, 13, 14, 30, 0, 0, time.UTC)
	addProject(t, ws, "my-blog", activeSession(start))
	fake := newFakeGit()
	svc := NewService(fake)

	session, err := svc.EndSession(ws, "my-blog", 0)
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("session = nil, want ended session")
	}
	if session.End == nil {
		t.Fatal("End = nil, want set")
	}
	if !session.End.After(session.Start) {
		t.Errorf("End %v not after Start %v", session.End, session.Start)
	}
	if session.Duration <= 0 {
		t.Errorf("Duration = %d, want > 0", session.Duration)
	}
	if session.Rounded < session.Duration {
		t.Errorf("Rounded = %d < Duration = %d", session.Rounded, session.Duration)
	}

	// The ended session must be persisted.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	stored := projects.Projects["my-blog"].Sessions[0]
	if stored.End == nil {
		t.Error("stored session still active")
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	if fake.commits[0].message != "Save session on my-blog" {
		t.Errorf("commit message = %q", fake.commits[0].message)
	}
}

func TestEndSessionWithBackfillEndsAtStartPlusDuration(t *testing.T) {
	ws := newTestWorkspace(t)
	start := time.Date(2026, 9, 13, 14, 30, 0, 0, time.UTC)
	addProject(t, ws, "my-blog", activeSession(start))
	fake := newFakeGit()
	svc := NewService(fake)

	session, err := svc.EndSession(ws, "my-blog", 1.5)
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := start.Add(90 * time.Minute)
	if !session.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", session.End, wantEnd)
	}
	if session.Duration != 5400 {
		t.Errorf("Duration = %d, want 5400", session.Duration)
	}
	if session.Rounded != 5400 {
		t.Errorf("Rounded = %d, want 5400", session.Rounded)
	}
}

func TestEndSessionBackfillNoActiveSession(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	before := time.Now().UTC()
	session, err := svc.EndSession(ws, "my-blog", 8)
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("session = nil, want backfilled session")
	}
	if session.End == nil {
		t.Fatal("End = nil, want set")
	}
	if session.Duration != 28800 {
		t.Errorf("Duration = %d, want 28800", session.Duration)
	}
	if session.Rounded != 28800 {
		t.Errorf("Rounded = %d, want 28800", session.Rounded)
	}

	// Start must be about 8h before End, and End must be about now.
	if got := session.End.Sub(session.Start); got != 8*time.Hour {
		t.Errorf("End-Start = %v, want 8h", got)
	}
	if session.End.Before(before.Add(-2*time.Second)) || session.End.After(after.Add(2*time.Second)) {
		t.Errorf("End = %v, want within [%v, %v]", session.End, before, after)
	}
}

func TestEndSessionNoActiveNoBackfill(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	session, err := svc.EndSession(ws, "my-blog", 0)
	if err != nil {
		t.Fatal(err)
	}
	if session != nil {
		t.Errorf("session = %+v, want nil", session)
	}
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0", len(fake.commits))
	}
}

func TestEndSessionUnknownProject(t *testing.T) {
	ws := newTestWorkspace(t)
	svc := NewService(newFakeGit())

	_, err := svc.EndSession(ws, "nope", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("expected *grinderr.User, got %T", err)
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestRequire(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	svc := NewService(newFakeGit())

	entry, err := svc.Require(ws, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "my-blog" {
		t.Errorf("Name = %q, want my-blog", entry.Name)
	}

	// The wording differs from Get: work and save say "does not exist".
	_, err = svc.Require(ws, "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestSaveCommitsDirtyWorktree(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := newFakeGit()
	fake.hasChanges = true
	svc := NewService(fake)

	if err := svc.Save(ws, "my-blog", nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.commitAll) != 1 {
		t.Fatalf("CommitAll calls = %d, want 1", len(fake.commitAll))
	}
	if fake.commitAll[0].message != "Save on my-blog" {
		t.Errorf("CommitAll message = %q", fake.commitAll[0].message)
	}
	// Save is local-only: it must never push.
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}

func TestSaveSkipsCleanWorktree(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := newFakeGit()
	svc := NewService(fake)

	if err := svc.Save(ws, "my-blog", nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.commitAll) != 0 {
		t.Errorf("CommitAll calls = %d, want 0", len(fake.commitAll))
	}
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}

func TestSaveUsesConfiguredDefaultBranch(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	cfg := config.Default()
	cfg.DefaultBranch = "trunk"
	if err := config.Write(ws.GrindConfigPath(), cfg); err != nil {
		t.Fatal(err)
	}
	fake := newFakeGit()
	fake.remoteURL = "git@example.com:repo.git"
	svc := NewService(fake)

	if err := svc.Save(ws, "my-blog", nil); err != nil {
		t.Fatal(err)
	}
	// Save never pushes, regardless of the configured default branch.
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}

func TestSaveNeverPushes(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := newFakeGit()
	// Even with a remote configured and a push error armed, Save must not
	// touch the remote — pushing is `grind push`'s job.
	fake.remoteURL = "git@example.com:repo.git"
	fake.pushErr = grinderr.NewSystem("git exploded")
	svc := NewService(fake)

	if err := svc.Save(ws, "my-blog", nil); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if fake.pushAll != 0 {
		t.Errorf("PushAll calls = %d, want 0", fake.pushAll)
	}
}
