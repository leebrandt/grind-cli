package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/invoice"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/report"
	"github.com/leebrandt/grind/internal/terminal"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newHwdCmd builds the hidden `grind hwd` hours-worked report: one week's
// hours per project, what they would bill for (and how much is already
// invoiced), and which projects finished.
//
// It is read-only, exactly like its sibling `wwd`: it never writes, never
// commits and never reads a worktree. The clock is captured here and read
// once per run, then the instant is handed to report.Build — the report
// package itself has no clock.
func newHwdCmd(clk clock.Clock) *cobra.Command {
	// -w is a STRING flag on purpose: an int flag would let pflag reject
	// `-w abc` with its own wording and exit 99. Parsing the string in RunE
	// is what lets every bad value produce the same user error, exit 1.
	var weeks string

	cmd := &cobra.Command{
		Use:    "hwd",
		Short:  "Hours-worked weekly report (hidden)",
		Hidden: true,
		Args:   noHwdArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse the flag before resolving the workspace: a usage error is
			// about how the command was typed, not about where it was typed.
			weeksBack, err := parseWeeks(weeks)
			if err != nil {
				return err
			}
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			rep, err := report.Build(ws, report.Options{WeeksBack: weeksBack}, clk.Now())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			palette := color.New(out)
			// 0 means "no constraint": a pipe, a file or a test buffer gets
			// the table at its natural width instead of a truncated one.
			width, _, _ := terminal.Size(out)
			renderReport(out, rep, palette, width)
			return nil
		},
	}
	cmd.Flags().StringVarP(&weeks, "weeks", "w", "0", "weeks back to report (0 = this week)")
	return cmd
}

// noHwdArgs rejects every positional argument. It is not cobra.NoArgs because
// NoArgs returns a bare error, which main maps to exit 99; the PRD wants a
// user error and exit 1, and grinderr.NewUser is what produces that.
func noHwdArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return grinderr.NewUser("hwd takes no arguments")
	}
	return nil
}

// parseWeeks turns the -w/--weeks value into a count of weeks back. Any whole
// number is accepted; the sign is ignored, because "weeks back" is already a
// direction and -3 and 3 name the same week. Anything that is not a whole
// number gets one error that shows how to pass a good one — the value is
// echoed so the user can see which argument was wrong.
func parseWeeks(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, grinderr.NewUser(fmt.Sprintf(
			"Invalid value for -w/--weeks: %q. Pass a whole number of weeks to look back — for example: grind hwd -w 1",
			raw))
	}
	if n < 0 {
		n = -n
	}
	return n, nil
}

// renderReport writes the whole report. It is the one function that knows the
// order of the sections; the helpers below each own a single section.
func renderReport(out io.Writer, rep *report.Report, palette color.Palette, width int) {
	fmt.Fprintln(out, report.HeaderLine(rep.Start, rep.End, rep.Past))
	fmt.Fprintln(out)

	// The table and the money block are both omitted when there are no rows:
	// three zero-valued money lines say nothing, and an empty week is not an
	// error.
	if len(rep.Rows) == 0 {
		fmt.Fprintln(out, emptyWorkLine(rep.Past))
	} else {
		renderReportTable(out, rep, palette, width)
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, palette.Dim(divider(width)))
	fmt.Fprintln(out)

	if len(rep.Rows) > 0 {
		renderMoneyBlock(out, rep)
		fmt.Fprintln(out)
		if len(rep.NoRate) > 0 {
			fmt.Fprintln(out, noRateWarning(rep.NoRate))
			fmt.Fprintln(out)
		}
	}

	renderFinished(out, rep)
	if len(rep.Earlier) > 0 {
		fmt.Fprintln(out)
		renderEarlier(out, rep, width)
	}
}

// emptyWorkLine is the one-line empty state, worded for the week being
// reported.
func emptyWorkLine(past bool) string {
	if past {
		return "No work recorded that week."
	}
	return "No work recorded this week."
}

// renderReportTable writes the hours table: one row per project worked in the
// window, then the TOTAL row. Project is left-aligned; the three numeric
// columns are right-aligned so the decimal points line up. Headers stay
// left-aligned at their column's left edge.
func renderReportTable(out io.Writer, rep *report.Report, palette color.Palette, width int) {
	// The Project header is indented by the marker gutter so it lines up
	// with the names beneath it, whether or not a row carries a marker.
	headers := []string{nameGutter + "Project", "Worked", "Billable", "Amount"}

	// Cell text stays plain here: fitColumns truncates it, and color is
	// applied after padding so the codes can never shift a column. The TOTAL
	// row is the last cell slice, so the loop below can color only the
	// project rows.
	cells := make([][]string, len(rep.Rows)+1)
	for i, row := range rep.Rows {
		cells[i] = []string{
			markerCell(row),
			projects.FormatHours(row.WorkedSeconds) + "h",
			projects.FormatHours(row.BillableSeconds) + "h",
			invoice.Symbol + formatAmount(row.Amount),
		}
	}
	cells[len(rep.Rows)] = []string{
		"TOTAL",
		projects.FormatHours(rep.Total.WorkedSeconds) + "h",
		projects.FormatHours(rep.Total.BillableSeconds) + "h",
		invoice.Symbol + formatAmount(rep.Total.Amount),
	}

	// Project is the only shrinkable column; three columns is the minimum
	// that still says something.
	headers, cells, widths := fitColumns(width, headers, cells, []int{0}, 3)
	// fitColumns drops trailing columns on a narrow terminal, and right must
	// lose the same ones. Only the right edge is ever dropped, so a prefix of
	// right is the correct alignment vector.
	right := []bool{false, true, true, true}[:len(widths)]

	fmt.Fprintln(out, renderRow(headers, widths))
	for i, row := range rep.Rows {
		// Color is applied AFTER padding: the visible text is already
		// column-width wide, so the ANSI codes cannot shift it.
		name := padRight(cells[i][0], widths[0])
		switch hwdNameColor(row) {
		case colorGreen:
			name = palette.Green(name)
		case colorYellow:
			name = palette.Yellow(name)
		}
		cells[i][0] = name
		fmt.Fprintln(out, renderRowAligned(cells[i], widths, right))
	}
	fmt.Fprintln(out, renderRowAligned(cells[len(cells)-1], widths, right))
}

// markerCell is the plain text of a row's Project cell: the marker that says
// what kind of project it is, followed by the name. Canceled outranks
// long-term, because a canceled project's long-term status is moot.
func markerCell(row report.Row) string {
	switch {
	case row.Status == "canceled":
		return canceledMarker + row.Name
	case row.LongTerm:
		return longTermMarker + row.Name
	default:
		return nameGutter + row.Name
	}
}

// canceledMarker flags a canceled project in the Project column, mirroring
// wwd's long-term marker: text, not color, so it survives piping.
const canceledMarker = "✗ "

// nameColor is the palette choice for a project name. It is a named type so
// the green-wins-over-yellow rule is a value a test can assert, rather than a
// pair of ifs buried in a renderer that needs a terminal to exercise.
type nameColor int

const (
	colorNone nameColor = iota
	colorGreen
	colorYellow
)

// hwdNameColor picks a row's name color by the same rules wwd uses: an active
// session outranks unbilled work, so green is checked first.
func hwdNameColor(row report.Row) nameColor {
	switch {
	case row.RunningNow:
		return colorGreen
	case row.HasUnbilled:
		return colorYellow
	default:
		return colorNone
	}
}

// renderMoneyBlock writes the three-line money block. Billable is the table's
// TOTAL by construction — both render Report.Total — and Unbilled is Billable
// minus Invoiced, so the three lines always add up.
func renderMoneyBlock(out io.Writer, rep *report.Report) {
	fmt.Fprintf(out, "%-10s %6s (%s%s)\n",
		"Billable:", projects.FormatHours(rep.Total.BillableSeconds)+"h", invoice.Symbol, formatAmount(rep.Total.Amount))
	fmt.Fprintf(out, "%-10s %6s (%s%s)\n",
		"Invoiced:", projects.FormatHours(rep.Money.InvoicedSeconds)+"h", invoice.Symbol, formatAmount(rep.Money.InvoicedAmount))
	fmt.Fprintf(out, "%-10s %6s (%s%s)\n",
		"Unbilled:", projects.FormatHours(rep.Money.UnbilledSeconds)+"h", invoice.Symbol, formatAmount(rep.Money.UnbilledAmount))
}

// noRateWarning names the zero-rate projects whose hours are in the table but
// whose money is not. It is plain text on purpose: the sentence already says
// what is wrong, and a colored warning would compete with the yellow name
// marker for the same meaning.
func noRateWarning(names []string) string {
	return fmt.Sprintf("No billing rate set for: %s. Their hours are counted; their amount is $0.00.",
		strings.Join(names, ", "))
}

// renderFinished writes the finished block, which always prints. Each entry is
// the name padded to the block's longest name, the status padded to nine
// characters, the local date padded to ten, then the week's hours — or an em
// dash when the project had no work in the window.
func renderFinished(out io.Writer, rep *report.Report) {
	label := "Finished this week:"
	if rep.Past {
		label = "Finished that week:"
	}
	if len(rep.Finished) == 0 {
		fmt.Fprintln(out, label+" none")
		return
	}
	fmt.Fprintln(out, label)

	maxName := 0
	for _, f := range rep.Finished {
		if n := utf8.RuneCountInString(f.Name); n > maxName {
			maxName = n
		}
	}
	for _, f := range rep.Finished {
		hours := "—"
		if f.HasWork {
			hours = projects.FormatHours(f.WorkedSeconds) + "h"
		}
		// The hours cell is the last cell and is not padded: nothing follows
		// it, and a trailing space would violate NFR-006.
		fmt.Fprintf(out, "  %s  %s  %s  %s\n",
			padRight(f.Name, maxName),
			padRight(f.Status, 9),
			padRight(f.Date.Format("2006-01-02"), 10),
			hours)
	}
}

// renderEarlier writes the one-line list of projects that finished but have no
// recorded date. It prints on every report, past weeks included, because it
// describes the workspace's unrecorded history rather than any one week. When
// the terminal width is known and the line would exceed it, it is truncated
// with an ellipsis rather than wrapped.
func renderEarlier(out io.Writer, rep *report.Report, width int) {
	var b strings.Builder
	b.WriteString("Finished earlier (no date recorded): ")
	for i, e := range rep.Earlier {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%s)", e.Name, e.Status)
	}
	line := b.String()
	if width > 0 {
		line = truncate(line, width)
	}
	fmt.Fprintln(out, line)
}
