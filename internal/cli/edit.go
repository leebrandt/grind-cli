package cli

import (
	"fmt"
	"path/filepath"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/dates"
	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/tasks"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newEditCmd builds the `edit` command group. The parent command edits a
// project worktree directory; the `idea` subcommand edits an idea file.
// Cobra runs the parent when the first argument is not a subcommand, so
// `edit my-blog` resolves the project while `edit idea 3` still dispatches
// to the subcommand. A project literally named "idea" is shadowed by the
// subcommand — an acceptable edge case.
func newEditCmd(ideasSvc *ideas.Service, projectsSvc *projects.Service, tasksSvc *tasks.Service, clk clock.Clock) *cobra.Command {
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
	edit.AddCommand(newEditTaskCmd(tasksSvc, clk))

	return edit
}

// newEditTaskCmd builds `grind edit task <id> -d <date>`. Unlike the other
// edit subcommands it changes a value rather than opening an editor — the
// one place `edit` does not mean "open in editor". Its project-name
// shadowing matches `edit idea`: a project named "task" is unreachable
// through `edit`.
func newEditTaskCmd(svc *tasks.Service, clk clock.Clock) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task <id>",
		Short: "Change a task's due date",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			id, err := parseTaskID(args[0])
			if err != nil {
				return err
			}
			// The date is the whole point of this command, so it is required.
			// Checked by hand because cobra's required-flag message does not
			// show the fix.
			rawDue, _ := cmd.Flags().GetString("due")
			if rawDue == "" {
				return grinderr.NewUser("Give a due date with -d, e.g. grind edit task " + args[0] + " -d 2026-09-20")
			}
			dueDate, err := dates.ParseDate(rawDue, clk.Now())
			if err != nil {
				return err
			}

			task, err := svc.SetDue(ws, id, dueDate)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Task %d due %s\n", task.ID, task.DueDate)
			return nil
		},
	}
	cmd.Flags().StringP("due", "d", "", "due date (today, tomorrow, 3d, 1w, 0720, 2026-07-20, ...)")
	return cmd
}
