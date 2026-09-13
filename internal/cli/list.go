package cli

import (
	"fmt"

	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newListCmd builds the `list` command group. This slice only has
// `list ideas`.
func newListCmd(svc *ideas.Service) *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List things",
	}
	list.AddCommand(newListIdeasCmd(svc))
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
