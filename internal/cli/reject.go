package cli

import (
	"fmt"

	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newRejectCmd builds the `reject` command group. This slice only has
// `reject idea`.
func newRejectCmd(svc *ideas.Service) *cobra.Command {
	reject := &cobra.Command{
		Use:   "reject",
		Short: "Reject something",
	}

	reject.AddCommand(&cobra.Command{
		Use:   "idea <number>",
		Short: "Reject an idea",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			n, err := parseIdeaNumber(args[0])
			if err != nil {
				return err
			}

			result, err := svc.Reject(ws, n)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Rejected idea #%d: %s\n", result.Number, result.Title)
			fmt.Fprintf(out, "Renamed: %s → %s\n", result.OldName, result.NewName)
			return nil
		},
	})

	return reject
}
