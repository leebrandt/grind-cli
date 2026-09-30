package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/leebrandt/grind/internal/sync"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newPullCmd builds `grind pull`, which fetches from the remote,
// fast-forwards every local branch that can be, and creates worktrees for
// remote project branches that have none. Pull is the only verb besides
// push that touches the network.
func newPullCmd(svc *sync.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Fetch and fast-forward from the remote",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			result, err := svc.Pull(ws)
			if err != nil {
				return err
			}
			printPullSummary(cmd.OutOrStdout(), result)
			return nil
		},
	}
}

// printPullSummary renders the pull result, omitting empty sections so a
// clean pull prints just the branches that moved.
func printPullSummary(w io.Writer, result *sync.PullResult) {
	if len(result.Updated) > 0 {
		fmt.Fprintf(w, "Fast-forwarded %d branch(es): %s\n",
			len(result.Updated), strings.Join(result.Updated, ", "))
	}
	if len(result.Created) > 0 {
		fmt.Fprintf(w, "Created %d worktree(s): %s\n",
			len(result.Created), strings.Join(result.Created, ", "))
	}
	if len(result.Diverged) > 0 {
		fmt.Fprintf(w, "%d branch(es) diverged from remote (manual merge needed): %s\n",
			len(result.Diverged), strings.Join(result.Diverged, ", "))
	}
	if len(result.Dirty) > 0 {
		fmt.Fprintf(w, "%d branch(es) not updated (uncommitted changes): %s\n",
			len(result.Dirty), strings.Join(result.Dirty, ", "))
	}
	if len(result.Skipped) > 0 {
		fmt.Fprintf(w, "Skipped %d project(s): %s\n",
			len(result.Skipped), strings.Join(result.Skipped, ", "))
	}
}
