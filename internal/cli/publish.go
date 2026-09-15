package cli

import (
	"fmt"

	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newPublishCmd builds `grind publish <project> [-y]`, which merges the
// project branch into the default branch, exports the final draft to
// published/, and marks the project published.
func newPublishCmd(svc *projects.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <project>",
		Short: "Merge a project into the default branch and export a final draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			name := args[0]

			yes, _ := cmd.Flags().GetBool("yes")
			cleanup, err := confirmCleanup(cmd.InOrStdin(), cmd.OutOrStdout(), name, yes)
			if err != nil {
				return err
			}

			if err := svc.Publish(ws, name, cleanup); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Published project '%s'. Draft exported to published/%s.md.\n", name, name)
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "skip the cleanup prompt (delete worktree and branch)")
	return cmd
}