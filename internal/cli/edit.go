package cli

import (
	"path/filepath"

	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newEditCmd builds the `edit` command group. This slice only has
// `edit idea`.
func newEditCmd(svc *ideas.Service) *cobra.Command {
	edit := &cobra.Command{
		Use:   "edit",
		Short: "Edit something",
	}

	edit.AddCommand(&cobra.Command{
		Use:   "idea <number>",
		Short: "Edit an idea in your editor",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			n, err := parseIdeaNumber(args[0])
			if err != nil {
				return err
			}
			idea, err := svc.Resolve(ws, n)
			if err != nil {
				return err
			}

			// Editing is the ONE exception to the "main worktree is clean"
			// invariant: the user is mid-edit and will commit when they save
			// the project. We deliberately leave the file dirty here.
			return editor.Open(filepath.Join(ws.IdeasDir(), idea.Filename))
		},
	})

	return edit
}
