package cli

import (
	"path/filepath"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newEditCmd builds the `edit` command group. The parent command edits a
// project worktree directory; the `idea` subcommand edits an idea file.
// Cobra runs the parent when the first argument is not a subcommand, so
// `edit my-blog` resolves the project while `edit idea 3` still dispatches
// to the subcommand. A project literally named "idea" is shadowed by the
// subcommand — an acceptable edge case.
func newEditCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service, clk clock.Clock) *cobra.Command {
	edit := &cobra.Command{
		Use:   "edit",
		Short: "Edit something",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			entry, err := projectsSvc.Get(ws, args[0])
			if err != nil {
				return err
			}

			// The project worktree is work product, so leaving it dirty is
			// fine (that is the point of editing); nothing is committed.
			return editor.Open(ws.ProjectWorktreePath(entry.Name))
		},
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
			idea, err := ideasSvc.Resolve(ws, n)
			if err != nil {
				return err
			}

			// Editing is the ONE exception to the "main worktree is clean"
			// invariant: the user is mid-edit and will commit when they save
			// the project. We deliberately leave the file dirty here.
			return editor.Open(filepath.Join(ws.IdeasDir(), idea.Filename))
		},
	})
	edit.AddCommand(newEditJournalCmd(clk))

	return edit
}
