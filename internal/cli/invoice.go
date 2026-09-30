package cli

import (
	"fmt"
	"path/filepath"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/invoice"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newInvoiceCmd builds `grind invoice <project> [-n]`, which turns a
// project's unbilled sessions into a markdown invoice and marks those
// sessions invoiced in the same command.
//
// Generating and marking are one verb on purpose: splitting them invites
// the foot-gun of generating an invoice, forgetting to mark the sessions,
// and billing the same work twice (v1 behaved this way).
//
// The verb only reads and writes .main, so unlike publish/cancel it needs
// no clean-worktree precondition.
func newInvoiceCmd(svc *invoice.Service, clk clock.Clock) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invoice <project>",
		Short: "Bill unbilled sessions as a markdown invoice",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			name := args[0]
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			// The clock is read here and passed in, so every date on the
			// invoice comes from one instant and tests can pin it.
			inv, err := svc.Generate(ws, name, dryRun, clk.Now())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if inv == nil {
				// Nothing to bill is a success, not an error: running
				// invoice twice in a row is harmless.
				fmt.Fprintf(out, "No unbilled sessions for '%s'.\n", name)
				return nil
			}

			money := invoice.Symbol + formatAmount(inv.Subtotal)
			// The label column is 9 wide, so the values line up whatever
			// the totals are.
			if dryRun {
				fmt.Fprintf(out, "Would invoice %s\n", sessionSummary(inv))
				fmt.Fprintf(out, "%-9s %s\n", "Subtotal:", money)
				fmt.Fprintf(out, "%-9s %s (%s)\n", "Due:", inv.DueDate, inv.PaymentTerms)
				fmt.Fprintln(out, "(Dry run — nothing was written. Re-run without --dry-run to issue it.)")
				return nil
			}

			fmt.Fprintf(out, "Invoiced %s\n", sessionSummary(inv))
			fmt.Fprintf(out, "%-9s %s\n", "Subtotal:", money)
			// The path is shown relative to the workspace root, which is
			// where the user is standing and where the file lives.
			path, err := filepath.Rel(ws.Root, filepath.Join(ws.InvoiceDir(name, inv.ID), "invoice.md"))
			if err != nil {
				path = "invoices/" + name + "/" + inv.ID + "/invoice.md"
			}
			fmt.Fprintf(out, "%-9s %s\n", "Invoice:", path)
			fmt.Fprintf(out, "%-9s %s (%s)\n", "Due:", inv.DueDate, inv.PaymentTerms)
			return nil
		},
	}
	cmd.Flags().BoolP("dry-run", "n", false, "print the invoice without marking sessions invoiced")
	return cmd
}

// sessionSummary renders the headline counts: how many sessions, how many
// hours in total, spread across how many days. The day count is the number
// of breakdown rows — one line per local day that had billable work.
func sessionSummary(inv *invoice.Invoice) string {
	return fmt.Sprintf("%d session(s) totalling %.2fh across %d day(s).",
		inv.SessionCount, inv.TotalHours, len(inv.Lines))
}

// formatAmount renders a monetary amount with exactly two decimals, matching
// how the invoice file writes them.
func formatAmount(v float64) string {
	return fmt.Sprintf("%.2f", v)
}
