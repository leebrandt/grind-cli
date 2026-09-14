// Package tasks implements the task lifecycle: add, list, and complete.
//
// Tasks are STATE, not work product: they live in .projects.json on the
// default branch, never in the project worktree. Task IDs are GLOBAL — a
// single counter in .projects.json hands out monotonic IDs across all
// projects, so `done task 3` needs no project name.
package tasks

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// Service performs task operations against a workspace using the given git
// implementation. The git layer is injected so tests can substitute a fake
// and verify exactly which operations run and in what order.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// AddTask appends a task to the project's task list, assigning the next
// global ID from the nextTaskId counter (100 when the field is missing).
// dueDate is already normalized to YYYY-MM-DD by the CLI. Commits
// .projects.json with "Add task: <description>".
func (s *Service) AddTask(ws *workspace.Workspace, projectName, description, dueDate string) (*config.Task, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", projectName))
		}
		return nil, err
	}
	entry, ok := projects.Projects[projectName]
	if !ok {
		return nil, grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", projectName))
	}

	// The description is trimmed for validation and stored trimmed so a
	// stray leading space never pollutes the task list.
	description = strings.TrimSpace(description)
	if description == "" {
		return nil, grinderr.NewUser("Task description must not be empty.")
	}

	// A workspace created before this slice has no nextTaskId field, which
	// unmarshals to 0. The counter starts at 100 (agreed with the user —
	// the first task is #100, not #1).
	id := projects.NextTaskID
	if id == 0 {
		id = 100
	}
	projects.NextTaskID = id + 1

	task := &config.Task{
		ID:          id,
		Description: description,
		// Truncate to seconds so the stored timestamp matches the RFC3339
		// shape in the spec (no fractional seconds).
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		DueDate:   dueDate,
	}
	entry.Tasks = append(entry.Tasks, *task)
	projects.Projects[projectName] = entry

	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		return nil, err
	}
	if err := s.Git.Commit(ws.MainWorktree, "Add task: "+description, ".projects.json"); err != nil {
		return nil, err
	}
	return task, nil
}

// TaskRow is one row of the task list, tagged with its project for the
// all-projects view.
type TaskRow struct {
	ID          int
	Project     string
	Description string
	DueDate     string // "" when unset
	Done        bool
}

// List returns tasks across all projects (projectName == "") or for one
// project, sorted by due date ascending (no-due last, ID tiebreak).
// openOnly filters to tasks with Done == false.
func (s *Service) List(ws *workspace.Workspace, projectName string, openOnly bool) ([]TaskRow, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A workspace without .projects.json simply has no tasks.
			return nil, nil
		}
		return nil, err
	}

	var rows []TaskRow
	if projectName != "" {
		entry, ok := projects.Projects[projectName]
		if !ok {
			return nil, grinderr.NewUser(fmt.Sprintf("Project '%s' not found.", projectName))
		}
		rows = appendRows(rows, projectName, entry.Tasks, openOnly)
	} else {
		// Projects are iterated in name order so the all-projects view is
		// deterministic even before the due-date sort runs.
		names := make([]string, 0, len(projects.Projects))
		for name := range projects.Projects {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rows = appendRows(rows, name, projects.Projects[name].Tasks, openOnly)
		}
	}

	// Due dates are YYYY-MM-DD strings, so string comparison IS
	// chronological. Tasks without a due date sort last; equal due dates
	// tiebreak by ID ascending for deterministic output.
	sort.Slice(rows, func(i, j int) bool {
		di, dj := rows[i].DueDate, rows[j].DueDate
		if di == "" && dj == "" {
			return rows[i].ID < rows[j].ID
		}
		if di == "" {
			return false
		}
		if dj == "" {
			return true
		}
		if di != dj {
			return di < dj
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

// appendRows converts a project's tasks into rows, optionally filtering to
// open tasks only.
func appendRows(rows []TaskRow, projectName string, tasks []config.Task, openOnly bool) []TaskRow {
	for _, t := range tasks {
		if openOnly && t.Done {
			continue
		}
		rows = append(rows, TaskRow{
			ID:          t.ID,
			Project:     projectName,
			Description: t.Description,
			DueDate:     t.DueDate,
			Done:        t.Done,
		})
	}
	return rows
}

// Complete marks the task with the given global ID done. Returns the task
// and whether it was already done (true = no write, no commit).
func (s *Service) Complete(ws *workspace.Workspace, id int) (*config.Task, bool, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, grinderr.NewUser(fmt.Sprintf("Task #%d not found.", id))
		}
		return nil, false, err
	}

	// Task IDs are global, so every project's task list is searched. The
	// first match wins — IDs are never reused, so there can be only one.
	for name := range projects.Projects {
		entry := projects.Projects[name]
		for i := range entry.Tasks {
			if entry.Tasks[i].ID != id {
				continue
			}
			if entry.Tasks[i].Done {
				// v1 re-completed idempotently, producing a pointless
				// commit; the rewrite skips the write entirely.
				return &entry.Tasks[i], true, nil
			}

			now := time.Now().UTC().Truncate(time.Second)
			entry.Tasks[i].Done = true
			entry.Tasks[i].CompletedAt = &now
			projects.Projects[name] = entry

			if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
				return nil, false, err
			}
			if err := s.Git.Commit(ws.MainWorktree, fmt.Sprintf("Complete task #%d", id), ".projects.json"); err != nil {
				return nil, false, err
			}
			return &entry.Tasks[i], false, nil
		}
	}

	return nil, false, grinderr.NewUser(fmt.Sprintf("Task #%d not found.", id))
}