package cli

import (
	"fmt"

	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newSaveCmd builds `grind save <project> [-t|--time <duration>]`, which
// ends the project's active session (or backfills one), commits both
// worktrees, and pushes both branches.
func newSaveCmd(svc *projects.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "save <project>",
		Short: "End a work session and save",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
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
