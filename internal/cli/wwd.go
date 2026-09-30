package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/status"
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newWwdCmd builds the hidden `grind wwd` dashboard: the per-project
// status table, the divider, and the open-task list.
//
// It is the user's most-used command and stays out of `--help` on
// purpose — the secret handshake (v1 hid it the same way).
func newWwdCmd(statusSvc *status.Service, tasksSvc *tasks.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "wwd",
		Short:  "Status dashboard (hidden)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			palette := color.New(out)

			// Part 1: the status table. No projects is not an error —
			// v1 printed a hint and still showed the rest.
			rows, err := statusSvc.Status(ws)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, `No active projects. Create one with: grind new project "name" <idea-number>`)
			} else {
				if err := renderStatusTable(out, rows, palette); err != nil {
					return err
				}
			}

			// Part 2: the divider, boxed in blank lines.
			fmt.Fprintln(out)
			fmt.Fprintln(out, palette.Dim(divider()))
			fmt.Fprintln(out)

			// Part 3: the open-task list — the same rendering `list
			// tasks` uses (all projects, open only), empty state included.
			taskRows, err := tasksSvc.List(ws, "", true)
			if err != nil {
				return err
			}
			return renderTaskList(out, taskRows, "", false, palette)
		},
	}
	return cmd
}

// divider returns v1's dashboard divider exactly: a leading space, 27
// box-drawing dashes, " The GrindCLI ", and 30 more dashes. A constant
// string would work, but building it from the counts keeps the two
// halves self-documenting.
func divider() string {
	return " " + strings.Repeat("─", 27) + " The GrindCLI " + strings.Repeat("─", 30)
}

// renderStatusTable writes the status table: one row per project, sorted
// by the service (worked time descending). Colors: the project name is
// green while a session is active and yellow while billable work is left
// unbilled; the task count is red when any task is overdue, yellow when one
// is due today.
//
// An active session outranks unbilled work: green is checked first, so a
// project being worked on right now stays green even if it has unbilled
// sessions behind it (v1 checked isActive first too).
//
// The table is padded by hand rather than rendered through tabwriter:
// tabwriter counts ANSI codes toward the cell width, so a colored cell in
// a non-last column would push its row out of alignment. Padding the
// plain text first and coloring the padded cell keeps the visible columns
// straight — the same approach v1's Table class uses.
func renderStatusTable(out io.Writer, rows []status.Row, palette color.Palette) error {
	headers := []string{"Project", "Worked", "Tasks", "Last Session", "Last Commit"}

	// Column widths come from the widest PLAIN cell (header included), so
	// the padding math never sees ANSI codes.
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		cells := []string{row.Name, row.WorkedHours, strconv.Itoa(row.TaskCount), row.LastSession, row.LastCommit}
		for i, c := range cells {
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}

	fmt.Fprintln(out, renderRow(headers, widths))
	for _, row := range rows {
		// Color is applied AFTER padding: the visible text is already
		// column-width wide, so the ANSI codes cannot shift it.
		name := padRight(row.Name, widths[0])
		switch {
		case row.IsActive:
			name = palette.Green(name)
		case row.HasUnbilled:
			name = palette.Yellow(name)
		}
		taskCount := padRight(strconv.Itoa(row.TaskCount), widths[2])
		switch row.TaskUrgency {
		case "overdue":
			taskCount = palette.Red(taskCount)
		case "today":
			taskCount = palette.Yellow(taskCount)
		}
		fmt.Fprintln(out, renderRow([]string{name, row.WorkedHours, taskCount, row.LastSession, row.LastCommit}, widths))
	}
	return nil
}
