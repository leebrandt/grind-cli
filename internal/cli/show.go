package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/invoice"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newShowCmd builds `grind show <project>`, which prints one project's
// details and its full idea content. With --billing it also prints the
// billed/unbilled split — invoicing is only half a feature without a way to
// ask "what is left to bill?". The flag is read-only: it writes nothing and
// commits nothing.
func newShowCmd(svc *projects.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <project>",
		Short: "Show project details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			entry, err := svc.Get(ws, args[0])
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			projectType := entry.Type
			if projectType == "" {
				projectType = "—"
			}
			fmt.Fprintf(out, "Name:    %s\n", entry.Name)
			fmt.Fprintf(out, "Type:    %s\n", projectType)
			fmt.Fprintf(out, "Created: %s\n", entry.CreatedAt.Format("2006-01-02"))

			// The rate carries the currency symbol rather than a code, so
			// this line, the --billing block below it, and the invoice file
			// all write money the same way.
			fmt.Fprintf(out, "Rate:    %s%s/hr (%s)\n",
				invoice.Symbol, formatRate(entry.Billing.Rate), entry.Billing.RoundTo)

			billing, _ := cmd.Flags().GetBool("billing")
			if billing {
				printBillingSummary(out, *entry)
			}

			// The idea content is printed verbatim after a blank line — it
			// is the project's founding document.
			fmt.Fprintln(out)
			fmt.Fprint(out, entry.Idea)
			if !strings.HasSuffix(entry.Idea, "\n") {
				fmt.Fprintln(out)
			}
			return nil
		},
	}
	cmd.Flags().BoolP("billing", "b", false, "show the billed/unbilled session totals")
	return cmd
}

// printBillingSummary writes the --billing block: the session count, the
// worked totals, and how much of that has been invoiced.
//
// The label column is 10 wide, so the values line up under each other. The
// rate is repeated here with two decimals because this block is about money,
// where "$150.00/hr" reads as the number an invoice will use.
func printBillingSummary(out io.Writer, entry config.ProjectEntry) {
	summary := invoice.Summarize(entry)

	fmt.Fprintln(out)
	fmt.Fprintf(out, "%-10s %d\n", "Sessions:", summary.SessionCount)
	fmt.Fprintf(out, "%-10s %sh (%s%s)\n", "Total:",
		projects.FormatHours(summary.TotalSeconds), invoice.Symbol, formatAmount(summary.TotalAmount))
	fmt.Fprintf(out, "%-10s %sh (%s%s)\n", "Billed:",
		projects.FormatHours(summary.BilledSeconds), invoice.Symbol, formatAmount(summary.BilledAmount))
	fmt.Fprintf(out, "%-10s %sh (%s%s)\n", "Unbilled:",
		projects.FormatHours(summary.UnbilledSeconds), invoice.Symbol, formatAmount(summary.UnbilledAmount))
	fmt.Fprintf(out, "%-10s %s%s/hr (%s)\n",
		"Rate:", invoice.Symbol, formatAmount(entry.Billing.Rate), entry.Billing.RoundTo)
}

// formatRate renders a billing rate without trailing zeros: 150.0 becomes
// "150", 150.5 stays "150.5". FormatFloat with precision -1 avoids the
// scientific notation that %g would produce for large numbers.
func formatRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', -1, 64)
}
