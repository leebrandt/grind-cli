package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/dates"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/tasks"
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
				dueDate, err = dates.ParseDate(rawDue, time.Now())
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
		return renderTaskList(out, rows, projectName, all, color.New(out))
	}
}

// renderTaskList renders the task list view shared by `list tasks` and
// the hidden `wwd` dashboard: the empty-state message when no rows
// match, otherwise the aligned table. projectName == "" selects the
// all-projects view (Project column shown); all includes completed
// tasks and changes the empty state's wording.
func renderTaskList(out io.Writer, rows []tasks.TaskRow, projectName string, all bool, palette color.Palette) error {
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

	// "Today" is the LOCAL date — the v1 UTC bug does not come back.
	today := time.Now().Format("2006-01-02")

	// Column widths come from the widest PLAIN cell (header included), so
	// the padding math never sees ANSI codes. The table is padded by hand
	// rather than rendered through tabwriter for the same reason as the
	// status table: tabwriter counts ANSI codes toward the cell width, so
	// colored or dimmed cells would push their row out of alignment.
	var headers []string
	if projectName == "" {
		headers = []string{"#", "Project", "Task", "Due"}
	} else {
		headers = []string{"#", "Task", "Due"}
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, c := range taskCells(row, projectName, row.DueDate) {
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}

	fmt.Fprintln(out, renderRow(headers, widths))
	for _, row := range rows {
		due := row.DueDate
		if due == "" {
			// An em dash keeps the column aligned when no due date is
			// set, and reads better than an empty cell.
			due = "—"
		} else if !row.Done {
			due = colorDue(palette, due, today)
		}
		cells := taskCells(row, projectName, due)
		if row.Done {
			// Completed rows render dimmed. The whole padded line is
			// wrapped (not each cell) so the dim attribute reads as one
			// block; the padding was applied to plain text, so the codes
			// cannot shift the columns.
			fmt.Fprintln(out, palette.Dim(renderRow(cells, widths)))
		} else {
			fmt.Fprintln(out, renderRow(cells, widths))
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

// newDoneCmd builds the `done` command group. This slice only has
// `done task`; bare `grind done` shows help.
func newDoneCmd(svc *tasks.Service) *cobra.Command {
	done := &cobra.Command{
		Use:   "done",
		Short: "Complete something",
	}

	done.AddCommand(&cobra.Command{
		Use:   "task <id>",
		Short: "Complete a task",
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

	return done
}

// parseTaskID converts the CLI argument to a global task ID.
func parseTaskID(arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, grinderr.NewUser(fmt.Sprintf("Task ID must be a number, got %q", arg))
	}
	return n, nil
}
