package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newShowCmd builds `grind show <project>`, which prints one project's
// details and its full idea content.
func newShowCmd(svc *projects.Service) *cobra.Command {
	return &cobra.Command{
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

			// The currency prefix on the Rate line comes from .grind.json.
			// A missing file falls back to defaults, which have no currency.
			cfg, err := config.Read(ws.GrindConfigPath())
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return err
				}
				cfg = config.Default()
			}

			out := cmd.OutOrStdout()
			projectType := entry.Type
			if projectType == "" {
				projectType = "—"
			}
			fmt.Fprintf(out, "Name:    %s\n", entry.Name)
			fmt.Fprintf(out, "Type:    %s\n", projectType)
			fmt.Fprintf(out, "Created: %s\n", entry.CreatedAt.Format("2006-01-02"))

			rate := formatRate(entry.Billing.Rate)
			if cfg.Currency != "" {
				rate = cfg.Currency + rate
			}
			fmt.Fprintf(out, "Rate:    %s/hr (%s)\n", rate, entry.Billing.RoundTo)

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
}

// formatRate renders a billing rate without trailing zeros: 150.0 becomes
// "150", 150.5 stays "150.5". FormatFloat with precision -1 avoids the
// scientific notation that %g would produce for large numbers.
func formatRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', -1, 64)
}
