package cli

import (
	"errors"
	"fmt"

	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newNewCmd builds the `new` command group: `new idea` and `new project`.
func newNewCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Create something new",
	}

	newCmd.AddCommand(newIdeaCmd(ideasSvc))
	newCmd.AddCommand(newProjectCmd(ideasSvc, projectsSvc))

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

// newProjectCmd builds `grind new project <name> <idea-number> [-t|--type]`.
func newProjectCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project <name> <idea-number>",
		Short: "Promote an idea to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}

			// The dirty-main check comes before idea resolution so a dirty
			// workspace is reported even when the idea number is also bad
			// (spec flow step 2 before step 3). Create checks again
			// defensively for direct callers of the service.
			if err := projectsSvc.EnsureClean(ws); err != nil {
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

			// The flag is registered below, so GetString cannot fail here.
			projectType, _ := cmd.Flags().GetString("type")
			entry, err := projectsSvc.Create(ws, args[0], projectType, idea.Filename)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created project: %s\n", entry.Name)
			fmt.Fprintf(out, "Branch: %s\n", entry.Name)
			fmt.Fprintf(out, "Worktree: %s/\n", entry.Name)
			fmt.Fprintf(out, "Next: cd %s\n", entry.Name)
			fmt.Fprintln(out)

			// List the remaining ideas in the same format as `list ideas`.
			remaining, err := ideasSvc.List(ws, ideas.ListOptions{})
			if err != nil {
				return err
			}
			fmt.Fprintln(out, "Remaining ideas:")
			if len(remaining) == 0 {
				fmt.Fprintln(out, "No ideas yet. Create one with: grind new idea \"Your idea\"")
			} else {
				for _, idea := range remaining {
					fmt.Fprintln(out, idea.String())
				}
			}
			return nil
		},
	}
	cmd.Flags().StringP("type", "t", "", "project type")
	return cmd
}
