package cli

import (
	"fmt"
	"os"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newInitCmd builds `grind init`, which creates the workspace skeleton in
// the current directory.
func newInitCmd(g git.Git) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a grind workspace in the current directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return grinderr.WrapSystem(err, "get current directory")
			}
			if err := workspace.Init(g, cwd); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Initialized grind workspace in %s\n", cwd)
			fmt.Fprintln(out, "  Bare repo: .grind.repo.git")
			fmt.Fprintln(out, "  Main worktree: .main")
			fmt.Fprintln(out, "  Next: grind new idea \"Your idea\"")
			return nil
		},
	}
}
