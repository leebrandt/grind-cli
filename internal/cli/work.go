package cli

import (
	"fmt"
	"time"

	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newWorkCmd builds `grind work <project>`, which starts (or continues) a
// work session and opens the editor on the project worktree directory.
func newWorkCmd(svc *projects.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "work <project>",
		Short: "Start or continue a work session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}

			session, started, err := svc.StartSession(ws, args[0])
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if started {
				fmt.Fprintf(out, "Started work session on '%s'\n", args[0])
				fmt.Fprintf(out, "Time started: %s\n", session.Start.Format(time.RFC3339))
			} else {
				fmt.Fprintf(out, "Continuing session on '%s'\n", args[0])
				fmt.Fprintf(out, "Session started: %s\n", session.Start.Format(time.RFC3339))
			}

			// The session is committed BEFORE the editor launches, so a
			// failed editor never loses the session. A non-zero editor exit
			// is a system error, consistent with `edit`.
			return editor.Open(ws.ProjectWorktreePath(args[0]))
		},
	}
}
