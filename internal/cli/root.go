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
	"github.com/leebrandt/grind/internal/projects"
	"github.com/spf13/cobra"
)

// NewRootCmd builds the complete grind command tree. The git implementation
// is injected so tests can substitute a fake.
func NewRootCmd(g git.Git) *cobra.Command {
	ideasSvc := ideas.NewService(g)
	projectsSvc := projects.NewService(g)

	root := &cobra.Command{
		Use:   "grind",
		Short: "CLI tool for managing creative/technical projects from idea to publication",
		// main() prints errors and maps them to exit codes, so cobra should
		// stay quiet and let us control the output.
		SilenceUsage:  true,
		SilenceErrors: true,
		// Cobra adds a `completion` command by default. Hide it from help —
		// it still works, it's just out of sight (same trick as the `ideas`
		// alias).
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
	}

	root.AddCommand(newInitCmd(g))
	root.AddCommand(newNewCmd(ideasSvc, projectsSvc))
	root.AddCommand(newListCmd(ideasSvc, projectsSvc))
	root.AddCommand(newIdeasAliasCmd(ideasSvc))
	root.AddCommand(newProjectsAliasCmd(projectsSvc))
	root.AddCommand(newEditCmd(ideasSvc))
	root.AddCommand(newRejectCmd(ideasSvc))
	root.AddCommand(newPruneCmd(ideasSvc))
	root.AddCommand(newShowCmd(projectsSvc))

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
