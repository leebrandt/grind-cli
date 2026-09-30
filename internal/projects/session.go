package projects

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// Duration parsing. The accepted forms are a plain number of hours ("5"),
// hours with an h suffix ("5h", "1.5h"), minutes with an m suffix ("90m"),
// and a combined hours+minutes form ("1h30m"). All are case-insensitive.
var (
	durationPlainRe    = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
	durationHoursRe    = regexp.MustCompile(`^(\d+(?:\.\d+)?)h$`)
	durationMinutesRe  = regexp.MustCompile(`^(\d+(?:\.\d+)?)m$`)
	durationCombinedRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)h(\d+(?:\.\d+)?)m$`)
)

// ParseDuration parses a backfill duration into hours. The accepted forms
// are "5", "5h", "1.5h", "90m", and "1h30m" (case-insensitive). Anything
// else, including zero and negative values, is a user error.
func ParseDuration(input string) (float64, error) {
	s := strings.ToLower(strings.TrimSpace(input))

	var hours float64
	switch {
	case durationCombinedRe.MatchString(s):
		m := durationCombinedRe.FindStringSubmatch(s)
		h, _ := strconv.ParseFloat(m[1], 64)
		min, _ := strconv.ParseFloat(m[2], 64)
		hours = h + min/60
	case durationHoursRe.MatchString(s):
		m := durationHoursRe.FindStringSubmatch(s)
		hours, _ = strconv.ParseFloat(m[1], 64)
	case durationMinutesRe.MatchString(s):
		m := durationMinutesRe.FindStringSubmatch(s)
		min, _ := strconv.ParseFloat(m[1], 64)
		hours = min / 60
	case durationPlainRe.MatchString(s):
		hours, _ = strconv.ParseFloat(s, 64)
	default:
		return 0, durationError(input)
	}

	if hours <= 0 {
		return 0, durationError(input)
	}
	return hours, nil
}

// durationError builds the user error for an unparseable or non-positive
// backfill duration. The original input is echoed back so the user can see
// exactly what was rejected.
func durationError(input string) error {
	return grinderr.NewUser(fmt.Sprintf(
		"Backfill time must be a positive duration (e.g. 5, 5h, 1h30m, 90m). Got '%s'.", input))
}

// roundTime rounds duration seconds UP to the project's billing bucket.
// Storing the rounded value at end time freezes the billing math: a later
// change to roundTo must not rewrite history.
func roundTime(duration int64, roundTo string) int64 {
	var bucket int64
	switch roundTo {
	case "half-hour":
		bucket = 1800
	case "hour":
		bucket = 3600
	default:
		// "quarter-hour" is the workspace default; anything unknown falls
		// back to it rather than silently skipping the rounding.
		bucket = 900
	}
	if duration <= 0 {
		return 0
	}
	return ((duration + bucket - 1) / bucket) * bucket
}

// FormatHours renders seconds as hours with two decimals, matching v1's
// toFixed(2) output ("1.50", "2.00").
func FormatHours(seconds int64) string {
	return fmt.Sprintf("%.2f", float64(seconds)/3600)
}

// loadProject reads .projects.json and returns the config plus the named
// project entry. A missing file or unknown name is the same user error:
// "Project '<name>' does not exist."
func loadProject(ws *workspace.Workspace, name string) (config.ProjectsConfig, config.ProjectEntry, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.ProjectsConfig{}, config.ProjectEntry{}, grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", name))
		}
		return config.ProjectsConfig{}, config.ProjectEntry{}, err
	}
	entry, ok := projects.Projects[name]
	if !ok {
		return config.ProjectsConfig{}, config.ProjectEntry{}, grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", name))
	}
	return projects, entry, nil
}

// StartSession starts a session on the project if none is active. Returns
// the session and whether one was newly started (false = continuing).
//
// The session is committed BEFORE the editor launches (the CLI opens the
// editor after this returns), so a failed editor never loses the session.
func (s *Service) StartSession(ws *workspace.Workspace, name string) (*config.Session, bool, error) {
	projects, entry, err := loadProject(ws, name)
	if err != nil {
		return nil, false, err
	}

	// An active session (End == nil) is continued, never duplicated — v1's
	// orphan bug was starting a fresh session every time, leaking sessions
	// that never ended.
	for i := range entry.Sessions {
		if entry.Sessions[i].End == nil {
			return &entry.Sessions[i], false, nil
		}
	}

	now := s.Clock.Now().UTC().Truncate(time.Second)
	session := config.Session{Start: now}
	entry.Sessions = append(entry.Sessions, session)
	projects.Projects[name] = entry

	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		return nil, false, err
	}
	if err := s.Git.Commit(ws.MainWorktree, "Start session on "+name, ".projects.json"); err != nil {
		return nil, false, err
	}
	return &session, true, nil
}

// EndSession ends the project's active session. backfill is a duration in
// hours (0 = end now). With no active session and backfill > 0, a session
// [now-backfill, now] is created. Returns the ended session, or nil when
// there was nothing to end and no backfill.
func (s *Service) EndSession(ws *workspace.Workspace, name string, backfill float64) (*config.Session, error) {
	projects, entry, err := loadProject(ws, name)
	if err != nil {
		return nil, err
	}

	now := s.Clock.Now().UTC().Truncate(time.Second)

	// Find the active session, if any.
	activeIdx := -1
	for i := range entry.Sessions {
		if entry.Sessions[i].End == nil {
			activeIdx = i
			break
		}
	}

	var session *config.Session
	switch {
	case activeIdx >= 0:
		// End the active session. With -t the end is start + duration (even
		// if that lands in the future — the user asked for a specific
		// length); without -t it ends now.
		s := entry.Sessions[activeIdx]
		end := now
		if backfill > 0 {
			end = s.Start.Add(time.Duration(backfill * float64(time.Hour)))
		}
		duration := int64(end.Sub(s.Start) / time.Second)
		s.End = &end
		s.Duration = duration
		s.Rounded = roundTime(duration, entry.Billing.RoundTo)
		entry.Sessions[activeIdx] = s
		session = &s
	case backfill > 0:
		// No active session but a backfill was requested: create a session
		// [now - duration, now] (AGENTS.md design decision — v1 only warned
		// here; the rewrite creates the session).
		start := now.Add(-time.Duration(backfill * float64(time.Hour)))
		duration := int64(now.Sub(start) / time.Second)
		s := config.Session{
			Start:    start,
			End:      &now,
			Duration: duration,
			Rounded:  roundTime(duration, entry.Billing.RoundTo),
		}
		entry.Sessions = append(entry.Sessions, s)
		session = &s
	default:
		// Nothing to end and no backfill: no mutation, no commit.
		return nil, nil
	}

	projects.Projects[name] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		return nil, err
	}
	if err := s.Git.Commit(ws.MainWorktree, "Save session on "+name, ".projects.json"); err != nil {
		return nil, err
	}
	return session, nil
}

// Save commits the project worktree (if dirty). session is the session
// EndSession just ended (nil when there was none); it decides the worktree
// commit message. Called by the CLI after EndSession.
//
// Save is deliberately LOCAL: it commits the work but never pushes. Remote
// sync is `grind push`'s job, so saving stays fast and works offline.
func (s *Service) Save(ws *workspace.Workspace, name string, session *config.Session) error {
	// The project worktree holds the actual work product. If it is dirty,
	// commit everything — the documented CommitAll exception, because a
	// project branch contains only work by construction.
	worktreePath := ws.ProjectWorktreePath(name)
	hasChanges, err := s.Git.HasChanges(worktreePath)
	if err != nil {
		return err
	}
	if hasChanges {
		msg := "Save on " + name
		if session != nil {
			msg = fmt.Sprintf("Work session on %s (%sh)", name, FormatHours(session.Rounded))
		}
		if err := s.Git.CommitAll(worktreePath, msg); err != nil {
			return err
		}
	}
	return nil
}
