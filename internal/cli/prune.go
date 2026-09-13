package cli

import (
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newPruneCmd builds the `prune` command group. This slice only has
// `prune ideas`.
func newPruneCmd(svc *ideas.Service) *cobra.Command {
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete rejected things",
	}

	cmd := &cobra.Command{
		Use:   "ideas",
		Short: "Delete rejected ideas",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			// The flag is registered below, so GetBool cannot fail here.
			yes, _ := cmd.Flags().GetBool("yes")
			return svc.Prune(ws, yes)
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	prune.AddCommand(cmd)

	return prune
}
