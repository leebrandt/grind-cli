package cli

import (
	"fmt"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newSaveCmd builds `grind save [project] [-t|--time <duration>]`.
//
// With no arguments it commits the main worktree — the missing commit verb
// for .main, where `edit idea` and `edit journal` leave files dirty by
// design. With a project name it keeps the slice-3 meaning: end the
// project's active session (or backfill one) and commit both worktrees.
// Saving is local-only — pushing is `grind push`'s job.
func newSaveCmd(svc *projects.Service, g git.Git) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save [project]",
		Short: "Commit workspace changes, or end a work session and save",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}

			if len(args) == 0 {
				return saveMain(cmd, g, ws)
			}

			name := args[0]

			// Look up the project before parsing -t so a missing project is
			// reported even when the duration is also invalid.
			entry, err := svc.Require(ws, name)
			if err != nil {
				return err
			}

			// The raw -t value is kept for the backfill message; the parsed
			// hours drive the session math.
			rawTime, _ := cmd.Flags().GetString("time")
			backfill := 0.0
			if rawTime != "" {
				backfill, err = projects.ParseDuration(rawTime)
				if err != nil {
					return err
				}
			}

			// The stopped/backfilled message depends on whether a session
			// was active before EndSession ran, so check before mutating.
			hadActive := false
			for i := range entry.Sessions {
				if entry.Sessions[i].End == nil {
					hadActive = true
					break
				}
			}

			session, err := svc.EndSession(ws, name, backfill)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch {
			case session == nil:
				fmt.Fprintf(out, "No active session on '%s'.\n", name)
			case !hadActive && backfill > 0:
				// The raw -t input goes into the message, e.g. "8h".
				fmt.Fprintf(out, "Backfilled %s on '%s'\n", rawTime, name)
				fmt.Fprintf(out, "Duration: %s hours (%s hours rounded)\n",
					projects.FormatHours(session.Duration), projects.FormatHours(session.Rounded))
			default:
				fmt.Fprintf(out, "Stopped work session on '%s'\n", name)
				fmt.Fprintf(out, "Duration: %s hours (%s hours rounded)\n",
					projects.FormatHours(session.Duration), projects.FormatHours(session.Rounded))
			}

			return svc.Save(ws, name, session)
		},
	}
	cmd.Flags().StringP("time", "t", "", "backfill duration (e.g. 5, 5h, 1h30m, 90m)")
	return cmd
}

// saveMain commits every unsaved change in .main. This is the explicit
// save-everything verb, so CommitAll's `git add -A` is the documented
// exception to the "stage only specific files" rule — the user asked to
// commit everything.
func saveMain(cmd *cobra.Command, g git.Git, ws *workspace.Workspace) error {
	hasChanges, err := g.HasChanges(ws.MainWorktree)
	if err != nil {
		return err
	}
	if !hasChanges {
		// A no-op is not an error: the workspace is already clean.
		fmt.Fprintln(cmd.OutOrStdout(), "Nothing to save.")
		return nil
	}
	if err := g.CommitAll(ws.MainWorktree, "Save workspace"); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Saved workspace changes.")
	return nil
}
