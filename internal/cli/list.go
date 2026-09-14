package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newListCmd builds the `list` command group: `list ideas`, `list projects`,
// and `list tasks`.
func newListCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service, tasksSvc *tasks.Service) *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List things",
	}
	list.AddCommand(newListIdeasCmd(ideasSvc))
	list.AddCommand(newListProjectsCmd(projectsSvc))
	list.AddCommand(newListTasksCmd(tasksSvc))
	return list
}

// newListIdeasCmd builds `grind list ideas`.
func newListIdeasCmd(svc *ideas.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ideas",
		Short: "List ideas",
		Args:  cobra.NoArgs,
		RunE:  listIdeasRunE(svc),
	}
	addListIdeasFlags(cmd)
	return cmd
}

// newIdeasAliasCmd builds the hidden `grind ideas` shortcut for
// `grind list ideas`. It is the user's secret handshake and must stay
// hidden from --help.
func newIdeasAliasCmd(svc *ideas.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "ideas",
		Short:  "List ideas (alias for 'list ideas')",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   listIdeasRunE(svc),
	}
	addListIdeasFlags(cmd)
	return cmd
}

func addListIdeasFlags(cmd *cobra.Command) {
	cmd.Flags().BoolP("all", "a", false, "include rejected ideas")
	cmd.Flags().BoolP("rejected", "r", false, "only rejected ideas")
}

// listIdeasRunE is the shared body of `list ideas` and the hidden `ideas`
// alias.
func listIdeasRunE(svc *ideas.Service) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ws, err := workspace.Require(".")
		if err != nil {
			return err
		}
		// The flags are registered in addListIdeasFlags, so GetBool cannot
		// fail here.
		all, _ := cmd.Flags().GetBool("all")
		rejected, _ := cmd.Flags().GetBool("rejected")

		list, err := svc.List(ws, ideas.ListOptions{All: all, Rejected: rejected})
		if err != nil {
			return err
		}
		if len(list) == 0 {
			if rejected {
				fmt.Fprintln(cmd.OutOrStdout(), "No rejected ideas.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "No ideas yet. Create one with: grind new idea \"Your idea\"")
			}
			return nil
		}
		for _, idea := range list {
			fmt.Fprintln(cmd.OutOrStdout(), idea.String())
		}
		return nil
	}
}

// newListProjectsCmd builds `grind list projects`.
func newListProjectsCmd(svc *projects.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE:  listProjectsRunE(svc),
	}
}

// newProjectsAliasCmd builds the hidden `grind projects` shortcut for
// `grind list projects`, mirroring the `ideas` alias trick.
func newProjectsAliasCmd(svc *projects.Service) *cobra.Command {
	return &cobra.Command{
		Use:    "projects",
		Short:  "List projects (alias for 'list projects')",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   listProjectsRunE(svc),
	}
}

// listProjectsRunE is the shared body of `list projects` and the hidden
// `projects` alias.
func listProjectsRunE(svc *projects.Service) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ws, err := workspace.Require(".")
		if err != nil {
			return err
		}
		list, err := svc.List(ws)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(list) == 0 {
			fmt.Fprintln(out, "No projects yet. Create one with: grind new project \"name\" <idea-number>")
			return nil
		}

		// tabwriter aligns the columns; the padding of 2 spaces matches the
		// spec's example output.
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "Project\tType\tCreated")
		for _, entry := range list {
			projectType := entry.Type
			if projectType == "" {
				// An em dash keeps the column aligned when the type is
				// unset, and reads better than an empty cell.
				projectType = "—"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", entry.Name, projectType, entry.CreatedAt.Format("2006-01-02"))
		}
		return tw.Flush()
	}
}
