package cli

import (
	"errors"
	"fmt"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newPushCmd builds `grind push`, which pushes every branch (the default
// branch plus each project branch) to the origin remote.
//
// Pushing is deliberately explicit: `save` commits locally and never
// touches the network, so it stays fast and works offline. `push` is the
// one verb that syncs to the remote, so a failed push is a real error the
// user asked for — not a silent warning.
func newPushCmd(g git.Git) *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Push all branches to the remote",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}

			remote, err := g.RemoteURL(ws.BareRepo)
			if err != nil {
				return err
			}
			if remote == "" {
				return grinderr.NewUser("No remote configured. Set remote.url in .grind.json first.")
			}

			if err := g.PushAll(ws.BareRepo); err != nil {
				var pushErr *git.PushError
				if errors.As(err, &pushErr) {
					return grinderr.NewUser(fmt.Sprintf("Could not push to remote: %s", pushErr.Stderr))
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Pushed all branches to origin.\n")
			return nil
		},
	}
}
