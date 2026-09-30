package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/leebrandt/grind/internal/projects"
)

// confirmCleanup asks what to do with the project's worktree and branch
// after a lifecycle operation. It returns projects.CleanupBoth when yes
// is set (the prompt is skipped); otherwise it reads a w/x/n answer
// from in, re-prompting until the answer is valid. Enter or EOF defaults
// to projects.CleanupNone — nothing is deleted unless explicitly asked.
func confirmCleanup(in io.Reader, out io.Writer, project string, yes bool) (projects.Cleanup, error) {
	if yes {
		return projects.CleanupBoth, nil
	}

	scanner := bufio.NewScanner(in)
	for {
		fmt.Fprintf(out, "Delete worktree and/or branch for '%s'? [w/x/n] ", project)
		if !scanner.Scan() {
			// EOF (or a read error) defaults to n — nothing is deleted
			// unless the user explicitly asked.
			return projects.CleanupNone, nil
		}
		switch strings.TrimSpace(scanner.Text()) {
		case "w":
			return projects.CleanupWorktree, nil
		case "x":
			return projects.CleanupBoth, nil
		case "n", "":
			return projects.CleanupNone, nil
		}
		// Invalid input: re-ask. The loop continues without an error.
	}
}
