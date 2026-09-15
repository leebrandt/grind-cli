// Package status computes the rows of the `wwd` dashboard.
//
// The dashboard is a VIEW over existing state: the service reads
// .projects.json and the bare repo and never writes anything, never
// commits, and never touches a worktree. Keeping that read-only guarantee
// here (rather than in the command) means the guarantee is testable —
// a test that hands the service a fake git can assert no commit calls.
//
// The package deliberately does NOT depend on internal/tasks: it reads
// config.Task entries directly, because the dashboard only needs the
// fields, not the task lifecycle.
package status

import (
	"fmt"
	"sort"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/dates"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/workspace"
)

// Service computes the wwd dashboard rows. It is read-only: it never
// writes .projects.json or commits.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// Row is one project's line in the status table. The rendered strings
// (WorkedHours, LastSession, LastCommit) are computed here so the command
// layer stays a thin renderer; TotalSeconds rides along for sorting.
type Row struct {
	Name         string
	WorkedHours  string // e.g. "3.5h"
	TaskCount    int
	TaskUrgency  string // "overdue" | "today" | "soon" | "none"
	LastSession  string // TimeAgo of the latest session start, or "never"
	LastCommit   string // TimeAgo of the branch's last commit, or "never"
	IsActive     bool
	TotalSeconds int64 // for sorting
}

// Status returns the dashboard rows, sorted by total worked seconds
// descending, then name ascending (so the order is deterministic even
// between projects with identical worked time).
func (s *Service) Status(ws *workspace.Workspace) ([]Row, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		return nil, err
	}

	now := time.Now()
	rows := make([]Row, 0, len(projects.Projects))
	for name, entry := range projects.Projects {
		// Canceled projects disappear from the dashboard, matching v1 where
		// cancel removed the worktree and the worktree-driven view naturally
		// dropped the project. Published projects stay — their worktree is
		// preserved.
		if entry.Status == "canceled" {
			continue
		}
		row, err := s.rowFor(ws, name, entry, now)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TotalSeconds != rows[j].TotalSeconds {
			return rows[i].TotalSeconds > rows[j].TotalSeconds
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// rowFor computes one project's row. now is passed in (rather than read
// from the clock per field) so every TimeAgo in the row is measured
// against the same instant.
func (s *Service) rowFor(ws *workspace.Workspace, name string, entry config.ProjectEntry, now time.Time) (Row, error) {
	var total int64
	var latestStart time.Time
	hasSession := false
	isActive := false
	for _, sess := range entry.Sessions {
		// Active sessions have Rounded 0 (it is written at end time), so
		// they contribute nothing to the worked total — matching the model.
		total += sess.Rounded
		if sess.End == nil {
			isActive = true
		}
		// "Latest" is the newest Start, not the last array element: the
		// sessions array is append-ordered, which is chronological in
		// practice, but a backfilled session can start earlier than a
		// later one. Max-ing over Start is the honest computation.
		if !hasSession || sess.Start.After(latestStart) {
			latestStart = sess.Start
			hasSession = true
		}
	}

	openTasks := 0
	for _, t := range entry.Tasks {
		if !t.Done {
			openTasks++
		}
	}

	lastSession := "never"
	if hasSession {
		lastSession = dates.TimeAgo(latestStart, now)
	}

	// The zero time means "branch has no commits" — the dashboard shows
	// "never" rather than 1970's "56y ago".
	lastCommit, err := s.Git.LastCommitDate(ws.BareRepo, name)
	if err != nil {
		return Row{}, err
	}
	lastCommitDisplay := "never"
	if !lastCommit.IsZero() {
		lastCommitDisplay = dates.TimeAgo(lastCommit, now)
	}

	return Row{
		Name:         name,
		WorkedHours:  fmt.Sprintf("%.1fh", float64(total)/3600),
		TaskCount:    openTasks,
		TaskUrgency:  TaskUrgency(entry.Tasks, now.Format("2006-01-02")),
		LastSession:  lastSession,
		LastCommit:   lastCommitDisplay,
		IsActive:     isActive,
		TotalSeconds: total,
	}, nil
}

// TaskUrgency returns the highest urgency across a project's open tasks:
// "overdue" if any is past due, "today" if any is due today, "soon" if
// any is due within 3 days, else "none". today is the local YYYY-MM-DD.
//
// It mirrors v1's getTaskUrgency: overdue wins outright (an early return
// — no later task can soften it); otherwise the highest of today/soon/
// none survives the loop.
func TaskUrgency(tasks []config.Task, today string) string {
	highest := "none"
	for _, t := range tasks {
		// Completed tasks and tasks without a due date carry no urgency.
		if t.Done || t.DueDate == "" {
			continue
		}
		switch {
		case t.DueDate < today:
			// Due dates are YYYY-MM-DD, so string comparison IS
			// chronological — no parsing needed to spot "past due".
			return "overdue"
		case t.DueDate == today:
			highest = "today"
		case dueWithinDays(t.DueDate, today, 3) && highest != "today":
			// "soon" only applies while nothing more urgent was seen;
			// a "today" seen earlier must not be downgraded.
			highest = "soon"
		}
	}
	return highest
}

// dueWithinDays reports whether dueDate is at most days days after today.
// Both are YYYY-MM-DD strings; parsing them as dates (rather than doing
// calendar math on strings) keeps the day difference exact regardless of
// timezone or DST.
func dueWithinDays(dueDate, today string, days int) bool {
	due, err := time.Parse("2006-01-02", dueDate)
	if err != nil {
		return false
	}
	t, err := time.Parse("2006-01-02", today)
	if err != nil {
		return false
	}
	return int(due.Sub(t)/(24*time.Hour)) <= days
}
