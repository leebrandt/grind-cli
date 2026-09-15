package cli

import (
	"fmt"

	"github.com/leebrandt/grind/internal/sync"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newPushCmd builds `grind push [project|all]`, which pushes branches to
// the origin remote.
//
// Pushing is deliberately scoped: `grind push` pushes only the default
// branch (state syncs by default), `grind push <project>` pushes one
// project's branch, and `grind push all` pushes everything. A failed push
// is a real error the user asked for — not a silent warning.
func newPushCmd(svc *sync.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "push [project|all]",
		Short: "Push branches to the remote",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}

			scope := sync.ScopeMain
			project := ""
			if len(args) == 1 {
				if args[0] == "all" {
					// A project literally named "all" is shadowed by the
					// keyword, same as `edit idea` shadowing a project
					// named "idea".
					scope = sync.ScopeAll
				} else {
					scope = sync.ScopeProject
					project = args[0]
				}
			}

			branch, err := svc.Push(ws, scope, project)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if scope == sync.ScopeAll {
				fmt.Fprintln(out, "Pushed all branches to origin.")
				return nil
			}
			// For ScopeMain the branch name comes from git (via the
			// service), not a hardcoded "main", so the message stays
			// correct if init ever supports a different default branch.
			fmt.Fprintf(out, "Pushed %s to origin.\n", branch)
			return nil
		},
	}
}