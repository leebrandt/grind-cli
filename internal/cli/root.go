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
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/spf13/cobra"
)

// version is the grind release version. Bump the patch number at the end of
// each slice so `grind -v` tells you which build you are testing.
const version = "0.90.3"

// NewRootCmd builds the complete grind command tree. The git implementation
// is injected so tests can substitute a fake.
func NewRootCmd(g git.Git) *cobra.Command {
	ideasSvc := ideas.NewService(g)
	projectsSvc := projects.NewService(g)
	tasksSvc := tasks.NewService(g)

	root := &cobra.Command{
		Use:     "grind",
		Short:   "CLI tool for managing creative/technical projects from idea to publication",
		Version: version,
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
	// Print just the version number (no "grind version" prefix) so scripts
	// can parse it and the user can eyeball it.
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(newInitCmd(g))
	root.AddCommand(newNewCmd(ideasSvc, projectsSvc, tasksSvc))
	root.AddCommand(newListCmd(ideasSvc, projectsSvc, tasksSvc))
	root.AddCommand(newIdeasAliasCmd(ideasSvc))
	root.AddCommand(newProjectsAliasCmd(projectsSvc))
	root.AddCommand(newTasksAliasCmd(tasksSvc))
	root.AddCommand(newEditCmd(ideasSvc, projectsSvc))
	root.AddCommand(newRejectCmd(ideasSvc))
	root.AddCommand(newPruneCmd(ideasSvc))
	root.AddCommand(newShowCmd(projectsSvc))
	root.AddCommand(newWorkCmd(projectsSvc))
	root.AddCommand(newSaveCmd(projectsSvc))
	root.AddCommand(newPushCmd(g))
	root.AddCommand(newDoneCmd(tasksSvc))
	root.AddCommand(newJournalAliasCmd())
	root.AddCommand(newReadCmd())

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
