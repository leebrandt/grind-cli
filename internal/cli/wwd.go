package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/status"
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/leebrandt/grind/internal/terminal"
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
			// 0 means "no constraint": a pipe or file gets the table at
			// its natural width instead of a truncated one.
			width, _, _ := terminal.Size(out)

			// Part 1: the status table. No projects is not an error —
			// v1 printed a hint and still showed the rest.
			rows, err := statusSvc.Status(ws)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, `No active projects. Create one with: grind new project "name" <idea-number>`)
			} else {
				if err := renderStatusTable(out, rows, palette, width); err != nil {
					return err
				}
			}

			// Part 2: the divider, boxed in blank lines.
			fmt.Fprintln(out)
			fmt.Fprintln(out, palette.Dim(divider(width)))
			fmt.Fprintln(out)

			// Part 3: the open-task list — the same rendering `list
			// tasks` uses (all projects, open only), empty state included.
			taskRows, err := tasksSvc.List(ws, "", true)
			if err != nil {
				return err
			}
			return renderTaskList(out, taskRows, "", false, tasksSvc.Today(), palette, width)
		},
	}
	return cmd
}

// longTermMarker flags long-term projects in the Project column. It is a
// prefix, not a color, so it survives piping and stays visible on the
// green/yellow name colors that already carry status.
const longTermMarker = "★ "

// nameGutter is the blank space that stands in for the marker on rows
// without one. Reserving it on every row keeps the names aligned: without
// it, a starred row would shift its name two columns right of the rest.
const nameGutter = "  "

// nameCell is the plain text of a row's Project cell: the marker (or the
// gutter) followed by the name.
func nameCell(row status.Row) string {
	if row.LongTerm {
		return longTermMarker + row.Name
	}
	return nameGutter + row.Name
}

// divider returns v1's dashboard divider exactly: a leading space, 27
// box-drawing dashes, " The GrindCLI ", and 30 more dashes. A constant
// string would work, but building it from the counts keeps the two
// halves self-documenting.
//
// When width is given and the full divider would not fit, the dashes are
// eaten from the right first and then from the left, so the label stays
// roughly centered instead of hugging one edge. width <= 0 means "no
// constraint", which is what a pipe or a file gets.
func divider(width int) string {
	const label = " The GrindCLI "
	left, right := 27, 30
	if width > 0 {
		for len(label)+left+right+1 > width && right > 1 {
			right--
		}
		for len(label)+left+right+1 > width && left > 1 {
			left--
		}
	}
	return " " + strings.Repeat("─", left) + label + strings.Repeat("─", right)
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
// width is the terminal width in runes, or 0 for no constraint. When the
// table is too wide it loses its least useful columns (Last Commit, then
// Last Session) before the Project column is truncated, so a narrow terminal
// stays readable rather than wrapping every row.
//
// The table is padded by hand rather than rendered through tabwriter:
// tabwriter counts ANSI codes toward the cell width, so a colored cell in
// a non-last column would push its row out of alignment. Padding the
// plain text first and coloring the padded cell keeps the visible columns
// straight — the same approach v1's Table class uses.
func renderStatusTable(out io.Writer, rows []status.Row, palette color.Palette, width int) error {
	// The Project header is indented by the marker gutter so it lines up
	// with the names beneath it, whether or not a row carries a marker.
	headers := []string{nameGutter + "Project", "Worked", "Tasks", "Last Session", "Last Commit"}

	// Cell text stays plain here: fitColumns truncates it, and color is
	// applied after padding so the codes can never shift a column.
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = []string{nameCell(row), row.WorkedHours, strconv.Itoa(row.TaskCount), row.LastSession, row.LastCommit}
	}

	// Project is the text column; Worked, Tasks and one time column are
	// the minimum that still tells the user something.
	headers, cells, widths := fitColumns(width, headers, cells, []int{0}, 3)

	fmt.Fprintln(out, renderRow(headers, widths))
	for i, row := range rows {
		// Color is applied AFTER padding: the visible text is already
		// column-width wide, so the ANSI codes cannot shift it.
		name := padRight(cells[i][0], widths[0])
		switch {
		case row.IsActive:
			name = palette.Green(name)
		case row.HasUnbilled:
			name = palette.Yellow(name)
		}
		taskCount := padRight(cells[i][2], widths[2])
		switch row.TaskUrgency {
		case "overdue":
			taskCount = palette.Red(taskCount)
		case "today":
			taskCount = palette.Yellow(taskCount)
		}
		cells[i][0] = name
		cells[i][2] = taskCount
		fmt.Fprintln(out, renderRow(cells[i], widths))
	}
	return nil
}
