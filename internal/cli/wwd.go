package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

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
// green while a session is active; the task count is red when any task is
// overdue, yellow when one is due today.
func renderStatusTable(out io.Writer, rows []status.Row, palette color.Palette) error {
	// StripEscape removes the palette's tabwriter escape bytes (see
	// internal/color) so colored cells stay aligned.
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', tabwriter.StripEscape)
	fmt.Fprintln(tw, "Project\tWorked\tTasks\tLast Session\tLast Commit")
	for _, row := range rows {
		name := row.Name
		if row.IsActive {
			name = palette.Green(name)
		}
		taskCount := strconv.Itoa(row.TaskCount)
		switch row.TaskUrgency {
		case "overdue":
			taskCount = palette.Red(taskCount)
		case "today":
			taskCount = palette.Yellow(taskCount)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			name, row.WorkedHours, taskCount, row.LastSession, row.LastCommit)
	}
	return tw.Flush()
}
