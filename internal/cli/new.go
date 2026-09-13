package cli

import (
	"errors"
	"fmt"

	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newNewCmd builds the `new` command group. This slice only has `new idea`.
func newNewCmd(svc *ideas.Service) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Create something new",
	}

	newCmd.AddCommand(&cobra.Command{
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
	})

	return newCmd
}
