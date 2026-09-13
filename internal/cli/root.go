// Package cli wires the cobra command tree. Commands are deliberately thin:
// they resolve the workspace, call into the domain packages, and print the
// results. All real logic lives in internal/ideas, internal/workspace, etc.
package cli

import (
	"fmt"
	"strconv"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the complete grind command tree. The git implementation
// is injected so tests can substitute a fake.
func NewRootCmd(g git.Git) *cobra.Command {
	svc := ideas.NewService(g)

	root := &cobra.Command{
		Use:   "grind",
		Short: "CLI tool for managing creative/technical projects from idea to publication",
		// main() prints errors and maps them to exit codes, so cobra should
		// stay quiet and let us control the output.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newInitCmd(g))
	root.AddCommand(newNewCmd(svc))
	root.AddCommand(newListCmd(svc))
	root.AddCommand(newIdeasAliasCmd(svc))
	root.AddCommand(newEditCmd(svc))
	root.AddCommand(newRejectCmd(svc))
	root.AddCommand(newPruneCmd(svc))

	return root
}

// parseIdeaNumber converts the CLI argument to a 0-based idea index.
func parseIdeaNumber(arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, grinderr.NewUser(fmt.Sprintf("Idea must be a number, got %q", arg))
	}
	return n, nil
}
