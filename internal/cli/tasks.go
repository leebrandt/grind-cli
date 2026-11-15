package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/dates"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/leebrandt/grind/internal/terminal"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newTaskCmd builds `grind new task <project> <description> [-d|--due]`.
func newTaskCmd(projectsSvc *projects.Service, svc *tasks.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task <project> <description>",
		Short: "Add a task to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}

			// Look up the project before parsing -d so a missing project is
			// reported even when the date is also invalid (same pattern as
			// save's -t handling).
			if _, err := projectsSvc.Require(ws, args[0]); err != nil {
				return err
			}
			if strings.TrimSpace(args[1]) == "" {
				return grinderr.NewUser("Task description must not be empty.")
			}

			// The raw -d value is kept for the error message; ParseDate
			// normalizes it to YYYY-MM-DD for storage.
			rawDue, _ := cmd.Flags().GetString("due")
			dueDate := ""
			if rawDue != "" {
				dueDate, err = dates.ParseDate(rawDue, svc.Clock.Now())
				if err != nil {
					return err
				}
			}

			task, err := svc.AddTask(ws, args[0], args[1], dueDate)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Task %d added: %s\n", task.ID, task.Description)
			return nil
		},
	}
	cmd.Flags().StringP("due", "d", "", "due date (today, tomorrow, 3d, 1w, 0720, 2026-07-20, ...)")
	return cmd
}

// newListTasksCmd builds `grind list tasks [project] [-a|--all]`.
func newListTasksCmd(svc *tasks.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tasks [project]",
		Short: "List tasks",
		Args:  cobra.MaximumNArgs(1),
		RunE:  listTasksRunE(svc),
	}
	addListTasksFlags(cmd)
	return cmd
}

// newTasksAliasCmd builds the hidden `grind tasks` shortcut for
// `grind list tasks`, mirroring the `ideas` and `projects` alias tricks.
func newTasksAliasCmd(svc *tasks.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "tasks [project]",
		Short:  "List tasks (alias for 'list tasks')",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE:   listTasksRunE(svc),
	}
	addListTasksFlags(cmd)
	return cmd
}

func addListTasksFlags(cmd *cobra.Command) {
	cmd.Flags().BoolP("all", "a", false, "include completed tasks")
}

// listTasksRunE is the shared body of `list tasks` and the hidden `tasks`
// alias.
func listTasksRunE(svc *tasks.Service) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ws, err := workspace.Require(".")
		if err != nil {
			return err
		}
		// The flag is registered in addListTasksFlags, so GetBool cannot
		// fail here.
		all, _ := cmd.Flags().GetBool("all")
		projectName := ""
		if len(args) > 0 {
			projectName = args[0]
		}

		rows, err := svc.List(ws, projectName, !all)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		// 0 means "no constraint" — a pipe or a file keeps the natural
		// table width.
		width, _, _ := terminal.Size(out)
		return renderTaskList(out, rows, projectName, all, svc.Today(), color.New(out), width)
	}
}

// renderTaskList renders the task list view shared by `list tasks` and
// the hidden `wwd` dashboard: the empty-state message when no rows
// match, otherwise the aligned table. projectName == "" selects the
// all-projects view (Project column shown); all includes completed
// tasks and changes the empty state's wording.
// today is passed in rather than read from the clock: a renderer that
// takes data is testable, and the caller decides what day it is.
// width is the terminal width in runes, or 0 for no constraint: a narrow
// terminal truncates the task description rather than wrapping the row.
func renderTaskList(out io.Writer, rows []tasks.TaskRow, projectName string, all bool, today string, palette color.Palette, width int) error {
	if len(rows) == 0 {
		switch {
		case projectName != "" && !all:
			fmt.Fprintf(out, "No open tasks. Add one with: grind new task %s \"description\"\n", projectName)
		case all:
			fmt.Fprintln(out, "No tasks yet.")
		default:
			fmt.Fprintln(out, "All caught up! No open tasks.")
		}
		return nil
	}

	// Column widths come from the widest PLAIN cell (header included), so
	// the padding math never sees ANSI codes. The table is padded by hand
	// rather than rendered through tabwriter for the same reason as the
	// status table: tabwriter counts ANSI codes toward the cell width, so
	// colored or dimmed cells would push their row out of alignment.
	var headers []string
	var cells [][]string
	// The user-supplied text is what yields: the project name and the
	// description both shrink, while # and Due are fixed-width. No column
	// is droppable — every one of these carries information the status
	// table's time columns do not.
	shrinkable := []int{1}
	if projectName == "" {
		headers = []string{"#", "Project", "Task", "Due"}
		shrinkable = []int{1, 2}
	} else {
		headers = []string{"#", "Task", "Due"}
	}
	for _, row := range rows {
		cells = append(cells, taskCells(row, projectName, row.DueDate))
	}
	// Due dates are colored later, so the fit sees the plain YYYY-MM-DD
	// form. The em dash that replaces an empty one is one rune, narrower
	// than the "Due" header, so it can never widen the column.
	headers, cells, widths := fitColumns(width, headers, cells, shrinkable, len(headers))

	fmt.Fprintln(out, renderRow(headers, widths))
	for i, row := range rows {
		due := row.DueDate
		if due == "" {
			// An em dash keeps the column aligned when no due date is
			// set, and reads better than an empty cell.
			due = "—"
		} else if !row.Done {
			due = colorDue(palette, due, today)
		}
		// Re-pad from the fitted cells, not from row.DueDate: a truncated
		// description must keep the columns where the header put them.
		cells[i][len(cells[i])-1] = due
		if row.Done {
			// Completed rows render dimmed. The whole padded line is
			// wrapped (not each cell) so the dim attribute reads as one
			// block; the padding was applied to plain text, so the codes
			// cannot shift the columns.
			fmt.Fprintln(out, palette.Dim(renderRow(cells[i], widths)))
		} else {
			fmt.Fprintln(out, renderRow(cells[i], widths))
		}
	}
	return nil
}

// taskCells returns a row's cells in column order. The due cell is passed
// in separately because it may be colored or replaced by an em dash.
func taskCells(row tasks.TaskRow, projectName, due string) []string {
	if projectName == "" {
		return []string{strconv.Itoa(row.ID), row.Project, row.Description, due}
	}
	return []string{strconv.Itoa(row.ID), row.Description, due}
}

// colorDue wraps a due date in the urgency color: red when overdue or due
// today, yellow within 3 days, green otherwise.
func colorDue(p color.Palette, dueDate, today string) string {
	switch dueColor(dueDate, today) {
	case "red":
		return p.Red(dueDate)
	case "yellow":
		return p.Yellow(dueDate)
	case "green":
		return p.Green(dueDate)
	}
	return dueDate
}

// dueColor returns the color name for a due date relative to today:
// "red" when overdue or due today, "yellow" within 3 days, "green"
// otherwise. The empty string means no due date. Both arguments are
// YYYY-MM-DD strings, so the day difference is exact regardless of
// timezone or DST.
func dueColor(dueDate, today string) string {
	due, err := time.Parse("2006-01-02", dueDate)
	if err != nil {
		return ""
	}
	t, err := time.Parse("2006-01-02", today)
	if err != nil {
		return ""
	}
	days := int(due.Sub(t) / (24 * time.Hour))
	switch {
	case days <= 0:
		return "red"
	case days <= 3:
		return "yellow"
	default:
		return "green"
	}
}

// newFinishCmd builds the `finish` command group. This slice only has
// `finish task`; bare `grind finish` shows help.
//
// The verb is `finish` rather than `done` because the command pattern is
// `<verb> <thing>`, and "done" is an adjective — it reads as a state the user
// is asking about, not an action they are taking. `finish task 100` says what
// happens to the task. The stored field stays `done`/`completedAt`, which is
// the state's own vocabulary, not the verb's.
func newFinishCmd(svc *tasks.Service) *cobra.Command {
	finish := &cobra.Command{
		Use:   "finish",
		Short: "Finish something",
	}

	finish.AddCommand(&cobra.Command{
		Use:   "task <id>",
		Short: "Finish a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			id, err := parseTaskID(args[0])
			if err != nil {
				return err
			}

			task, alreadyDone, err := svc.Complete(ws, id)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if alreadyDone {
				fmt.Fprintf(out, "Task %d is already completed.\n", task.ID)
			} else {
				fmt.Fprintf(out, "Task %d completed.\n", task.ID)
			}
			return nil
		},
	})

	return finish
}

// parseTaskID converts the CLI argument to a global task ID.
func parseTaskID(arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, grinderr.NewUser(fmt.Sprintf("Task ID must be a number, got %q", arg))
	}
	return n, nil
}
