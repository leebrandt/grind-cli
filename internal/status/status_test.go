package status

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit stubs the git.Git interface for the status package. The
// dashboard only ever asks for LastCommitDate, so that is the only method
// with real behavior: it returns the recorded time for a branch, or the
// zero time for a branch with no entry (mirroring the real "no commits"
// result).
type fakeGit struct {
	lastCommitDates map[string]time.Time
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error { return nil }

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error { return nil }

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) { return true, nil }

func (f *fakeGit) CreateBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) { return false, nil }

func (f *fakeGit) CommitAll(worktreePath, message string) error { return nil }

func (f *fakeGit) RemoteURL(repoPath string) (string, error) { return "", nil }

func (f *fakeGit) PushAll(repoPath string) error { return nil }

func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return f.lastCommitDates[branch], nil
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

// newTestWorkspace builds a workspace directory with the given projects
// config on disk. The status service reads .projects.json through the
// workspace path, so the test writes the file exactly like production
// would.
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

func TestTaskUrgency(t *testing.T) {
	// A task literal keeps the table below short; only the fields
	// TaskUrgency reads need values.
	task := func(due string, done bool) config.Task {
		return config.Task{ID: 100, Description: "x", DueDate: due, Done: done}
	}

	tests := []struct {
		name  string
		tasks []config.Task
		want  string
	}{
		{"no tasks", nil, "none"},
		{"only completed tasks", []config.Task{task("2026-09-01", true)}, "none"},
		{"no due dates", []config.Task{task("", false)}, "none"},
		{"overdue", []config.Task{task("2026-09-13", false)}, "overdue"},
		{"due today", []config.Task{task("2026-09-14", false)}, "today"},
		{"due within 3 days", []config.Task{task("2026-09-16", false)}, "soon"},
		{"due in 4 days", []config.Task{task("2026-09-18", false)}, "none"},
		// Overdue wins outright, even when today/soon tasks are present.
		{
			"overdue beats today and soon",
			[]config.Task{task("2026-09-16", false), task("2026-09-14", false), task("2026-09-10", false)},
			"overdue",
		},
		// Today outranks soon regardless of array order.
		{
			"today beats soon",
			[]config.Task{task("2026-09-16", false), task("2026-09-14", false)},
			"today",
		},
		{
			"today first, then soon",
			[]config.Task{task("2026-09-14", false), task("2026-09-16", false)},
			"today",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// "Today" is pinned so the table's dates are stable.
			got := TaskUrgency(tt.tasks, "2026-09-14")
			if got != tt.want {
				t.Errorf("TaskUrgency() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	now := time.Now()
	// endedAt pins a session's End pointer; sessions below are built with
	// fixed offsets from now so the TimeAgo buckets are stable.
	endedAt := func(d time.Duration) *time.Time {
		end := now.Add(-d)
		return &end
	}

	// alpha: two sessions (30m + 1h rounded) → 1.5h worked, one open and
	// one completed task, a commit 2h ago, no active session.
	// beta: nothing at all → the "never"/"0.0h" row.
	// gamma: same worked time as beta, to exercise the name tiebreak.
	projects := config.ProjectsConfig{
		Version: 1,
		Projects: map[string]config.ProjectEntry{
			"beta":  {Name: "beta"},
			"gamma": {Name: "gamma"},
			"alpha": {
				Name: "alpha",
				Sessions: []config.Session{
					{Start: now.Add(-3 * time.Hour), End: endedAt(3*time.Hour - 30*time.Minute), Rounded: 1800},
					{Start: now.Add(-2 * time.Hour), End: endedAt(time.Hour), Rounded: 3600},
				},
				Tasks: []config.Task{
					{ID: 100, Description: "open task", DueDate: "2026-09-20"},
					{ID: 101, Description: "done task", Done: true},
				},
			},
		},
	}
	ws := newTestWorkspace(t, projects)
	svc := NewService(&fakeGit{lastCommitDates: map[string]time.Time{
		"alpha": now.Add(-2 * time.Hour),
		// beta and gamma have no entry: no commits yet.
	}})

	rows, err := svc.Status(ws)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}

	// Sorting: alpha's 5400s first; beta and gamma tie at 0s, so the
	// name breaks the tie ascending.
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, want := range wantNames {
		if rows[i].Name != want {
			t.Errorf("rows[%d].Name = %q, want %q", i, rows[i].Name, want)
		}
	}

	alpha := rows[0]
	if alpha.WorkedHours != "1.5h" {
		t.Errorf("alpha WorkedHours = %q, want %q", alpha.WorkedHours, "1.5h")
	}
	if alpha.TaskCount != 1 {
		t.Errorf("alpha TaskCount = %d, want 1 (open only)", alpha.TaskCount)
	}
	if alpha.TaskUrgency != "none" {
		t.Errorf("alpha TaskUrgency = %q, want %q", alpha.TaskUrgency, "none")
	}
	// The latest session started 2h ago; the bucket is stable for the
	// (much shorter) life of the test.
	if alpha.LastSession != "2h ago" {
		t.Errorf("alpha LastSession = %q, want %q", alpha.LastSession, "2h ago")
	}
	if alpha.LastCommit != "2h ago" {
		t.Errorf("alpha LastCommit = %q, want %q", alpha.LastCommit, "2h ago")
	}
	if alpha.IsActive {
		t.Error("alpha IsActive = true, want false (all sessions ended)")
	}
	if alpha.TotalSeconds != 5400 {
		t.Errorf("alpha TotalSeconds = %d, want 5400", alpha.TotalSeconds)
	}

	beta := rows[1]
	if beta.WorkedHours != "0.0h" {
		t.Errorf("beta WorkedHours = %q, want %q", beta.WorkedHours, "0.0h")
	}
	if beta.LastSession != "never" {
		t.Errorf("beta LastSession = %q, want %q", beta.LastSession, "never")
	}
	if beta.LastCommit != "never" {
		t.Errorf("beta LastCommit = %q, want %q", beta.LastCommit, "never")
	}
	if beta.TaskUrgency != "none" {
		t.Errorf("beta TaskUrgency = %q, want %q", beta.TaskUrgency, "none")
	}
}

func TestStatusActiveSession(t *testing.T) {
	now := time.Now()
	end := now.Add(-2 * time.Hour)
	projects := config.ProjectsConfig{
		Version: 1,
		Projects: map[string]config.ProjectEntry{
			"my-blog": {
				Name: "my-blog",
				Sessions: []config.Session{
					// An ended session contributes 1h of worked time...
					{Start: now.Add(-3 * time.Hour), End: &end, Rounded: 3600},
					// ...and an active session contributes none (Rounded 0)
					// but marks the project active.
					{Start: now.Add(-30 * time.Second)},
				},
			},
		},
	}
	ws := newTestWorkspace(t, projects)
	svc := NewService(&fakeGit{})

	rows, err := svc.Status(ws)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	row := rows[0]
	if !row.IsActive {
		t.Error("IsActive = false, want true (a session has End == nil)")
	}
	if row.TotalSeconds != 3600 {
		t.Errorf("TotalSeconds = %d, want 3600 (active session contributes nothing)", row.TotalSeconds)
	}
	if row.WorkedHours != "1.0h" {
		t.Errorf("WorkedHours = %q, want %q", row.WorkedHours, "1.0h")
	}
	// The active session started 30 seconds ago — "just now" is stable
	// for the life of the test.
	if row.LastSession != "just now" {
		t.Errorf("LastSession = %q, want %q", row.LastSession, "just now")
	}
}

func TestStatusNoProjects(t *testing.T) {
	ws := newTestWorkspace(t, config.DefaultProjects())
	svc := NewService(&fakeGit{})

	rows, err := svc.Status(ws)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

func TestStatusMissingProjectsFile(t *testing.T) {
	// A workspace without .projects.json is a system error (exit 2),
	// matching how the other packages treat it — only `list tasks` maps
	// it to an empty view.
	dir := t.TempDir()
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: filepath.Join(dir, ".main"),
	}
	svc := NewService(&fakeGit{})

	_, err := svc.Status(ws)
	if err == nil {
		t.Fatal("Status() without .projects.json: expected error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want an error wrapping os.ErrNotExist", err)
	}
}

func TestStatusSkipsCanceled(t *testing.T) {
	projects := config.ProjectsConfig{
		Version: 1,
		Projects: map[string]config.ProjectEntry{
			"active":    {Name: "active"},
			"published": {Name: "published", Status: "published"},
			"canceled":  {Name: "canceled", Status: "canceled"},
		},
	}
	ws := newTestWorkspace(t, projects)
	svc := NewService(&fakeGit{})

	rows, err := svc.Status(ws)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (canceled hidden)", len(rows))
	}
	if rows[0].Name != "active" || rows[1].Name != "published" {
		t.Errorf("rows = %v, want [active published]", []string{rows[0].Name, rows[1].Name})
	}
}

func TestStatusSkipsCanceledTasks(t *testing.T) {
	// A canceled task must not count as open work and must not drive
	// urgency — it died with its project.
	projects := config.ProjectsConfig{
		Version: 1,
		Projects: map[string]config.ProjectEntry{
			"alpha": {
				Name: "alpha",
				Tasks: []config.Task{
					{ID: 100, Description: "open", DueDate: "2026-09-20"},
					{ID: 101, Description: "canceled", DueDate: "2026-09-01", Canceled: true},
					{ID: 102, Description: "done", Done: true},
				},
			},
		},
	}
	ws := newTestWorkspace(t, projects)
	svc := NewService(&fakeGit{})

	rows, err := svc.Status(ws)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].TaskCount != 1 {
		t.Errorf("TaskCount = %d, want 1 (canceled task not open work)", rows[0].TaskCount)
	}
	// The canceled task is overdue (2026-09-01) but must not drive urgency.
	if rows[0].TaskUrgency != "none" {
		t.Errorf("TaskUrgency = %q, want none", rows[0].TaskUrgency)
	}
}
