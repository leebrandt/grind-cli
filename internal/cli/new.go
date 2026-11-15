package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newNewCmd builds the `new` command group: `new idea`, `new project`, and
// `new task`.
func newNewCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service, tasksSvc *tasks.Service) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Create something new",
	}

	newCmd.AddCommand(newIdeaCmd(ideasSvc))
	newCmd.AddCommand(newProjectCmd(ideasSvc, projectsSvc))
	newCmd.AddCommand(newTaskCmd(projectsSvc, tasksSvc))

	return newCmd
}

// newIdeaCmd builds `grind new idea [title]`.
func newIdeaCmd(svc *ideas.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "idea [title]",
		Short: "Create a new idea",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			title := ""
			if len(args) > 0 {
				title = args[0]
			}

			filename, err := svc.Create(ws, title)
			if errors.Is(err, ideas.ErrAborted) {
				// The user left the editor without a title; that is a
				// successful no-op, not an error.
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created idea: ideas/%s\n", filename)
			return nil
		},
	}
}

// newProjectCmd builds `grind new project`. With no positional arguments it
// runs the interactive wizard; with a name and an idea number it is the
// non-interactive original. Both share the same success block.
func newProjectCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project [<name> <idea-number>]",
		Short: "Promote an idea to a project",
		Args:  validateProjectArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runNewProjectInteractive(cmd, ideasSvc, projectsSvc)
			}
			return runNewProjectArgs(cmd, args, ideasSvc, projectsSvc)
		},
	}
	cmd.Flags().StringP("type", "t", "", "project type")
	return cmd
}

// validateProjectArgs accepts exactly zero arguments (interactive) or two
// (name and idea number). One argument is the mistake this feature exists to
// fix, and three is always a typo, so both get one message that says how to
// proceed instead of cobra's "accepts at most 2 arg(s)".
func validateProjectArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || len(args) == 2 {
		return nil
	}
	return grinderr.NewUser("new project requires either no arguments (interactive) or <name> <idea-number>")
}

// runNewProjectArgs is the non-interactive `grind new project <name>
// <idea-number>` path. It asks no questions.
func runNewProjectArgs(cmd *cobra.Command, args []string, ideasSvc *ideas.Service, projectsSvc *projects.Service) error {
	ws, err := workspace.Require(".")
	if err != nil {
		return err
	}

	// The .projects.json check comes before idea resolution so a
	// dirty state file is reported even when the idea number is
	// also bad (spec flow step 2 before step 3). Create checks
	// again defensively for direct callers of the service.
	if err := projectsSvc.EnsureProjectsClean(ws); err != nil {
		return err
	}

	n, err := parseIdeaNumber(args[1])
	if err != nil {
		return err
	}
	idea, err := ideasSvc.Resolve(ws, n)
	if err != nil {
		return err
	}

	// The flag is registered on the command, so GetString cannot fail here.
	projectType, _ := cmd.Flags().GetString("type")
	entry, err := projectsSvc.Create(ws, args[0], projectType, idea.Filename)
	if err != nil {
		return err
	}

	printCreatedProject(cmd.OutOrStdout(), entry)
	return nil
}

// printCreatedProject writes the block both invocation forms share. The
// Next hint names `work`, the verb that starts tracking time on the project
// that was just created.
func printCreatedProject(out io.Writer, entry *config.ProjectEntry) {
	fmt.Fprintf(out, "Created project: %s\n", entry.Name)
	fmt.Fprintf(out, "Branch: %s\n", entry.Name)
	fmt.Fprintf(out, "Worktree: %s/\n", entry.Name)
	fmt.Fprintf(out, "Next: work %s\n", entry.Name)
}
