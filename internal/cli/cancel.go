package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newCancelCmd builds `grind cancel <project> [-y]`, which marks a project
// canceled in .projects.json and cleans up its worktree/branch per the
// user's choice. The entry stays as a record.
func newCancelCmd(svc *projects.Service) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <project>",
		Short: "Cancel a project (keeps the entry as a record)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			name := args[0]

			// The existence checks run BEFORE the prompt so the user never
			// confirms an action that would immediately fail.
			if _, err := svc.Require(ws, name); err != nil {
				return err
			}
			worktreePath := ws.ProjectWorktreePath(name)
			if _, err := os.Stat(worktreePath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return grinderr.NewUser(fmt.Sprintf("Project worktree '%s' does not exist.", name))
				}
				return err
			}

			// Canceling from inside the project worktree would delete the
			// directory the user is standing in. Refuse before the prompt.
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if isInside(cwd, worktreePath) {
				return grinderr.NewUser(fmt.Sprintf(
					"You are inside this project's worktree. Run 'grind cancel %s' from the workspace root.", name))
			}

			yes, _ := cmd.Flags().GetBool("yes")
			cleanup, err := confirmCleanup(cmd.InOrStdin(), cmd.OutOrStdout(), name, yes)
			if err != nil {
				return err
			}

			if err := svc.Cancel(ws, name, cleanup); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Project '%s' cancelled.\n", name)
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "skip the cleanup prompt (delete worktree and branch)")
	return cmd
}

// isInside reports whether path is inside dir (or equal to it), using a
// prefix match on cleaned absolute paths. The separator guard keeps
// /ws/my-blog-extra from matching /ws/my-blog.
func isInside(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}