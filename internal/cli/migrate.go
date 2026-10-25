package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/migrate"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newMigrateCmd builds `grind migrate [-n]`, the one command that turns a v1
// workspace into a v2 one in place.
//
// It is deliberately thin: the plan/apply split lives in internal/migrate, and
// this file only discovers the workspace, prints the plan and reports what
// happened. The one thing it must get right is which discovery call to use —
// workspace.Require refuses a v1 workspace with a "run grind migrate" hint,
// which is exactly the workspace this command is here to convert.
func newMigrateCmd(g git.Git, clk clock.Clock) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Convert a grind v1 workspace to the v2 layout",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Find, not Require: a v1 workspace is the input here.
			ws, err := workspace.Find(".")
			if err != nil {
				return err
			}
			if ws == nil {
				return grinderr.NewUser("Not in a grind workspace.")
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			plan, err := migrate.NewPlanAt(ws.Root, g, clk)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if dryRun {
				fmt.Fprintln(out, "Migrating workspace to the v2 layout (dry run — nothing written)")
			} else {
				fmt.Fprintln(out, "Migrating workspace to the v2 layout...")
			}
			fmt.Fprintln(out)
			printMigrationSummary(out, plan)

			if dryRun {
				return nil
			}

			if err := migrate.Apply(ws, plan, g); err != nil {
				return err
			}

			fmt.Fprintln(out)
			fmt.Fprintf(out, "Committed: %s\n", migrate.CommitMessage)
			if plan.Dirty {
				// v1's config and journal commands never committed, so a
				// workspace in everyday use is routinely dirty. Those edits are
				// the user's, not the migration's — `grind save` is the verb
				// that sweeps them up, and this is the moment to name it.
				fmt.Fprintf(out, "Uncommitted files were already present in %s/ — run 'grind save' to commit them.\n",
					filepath.Base(plan.FromMain()))
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Next: grind wwd")
			return nil
		},
	}
	cmd.Flags().BoolP("dry-run", "n", false, "print the migration plan without changing anything")
	return cmd
}

// labelWidth is the width of the summary's label column, so every value starts
// at the same place: two spaces of indent plus a 16-wide label.
const labelWidth = 16

// printMigrationSummary writes the plan in the shape the user reads: what moves,
// how much of it there is, and what is being lost. Counts of zero are left out
// — "Tasks 0 renumbered" is noise — but warnings never are: they are the only
// record of what the migration could not keep.
func printMigrationSummary(out io.Writer, plan *migrate.Plan) {
	if plan.MoveMain {
		fmt.Fprintf(out, "  %-*s%s\n", labelWidth, "Main worktree", "grind/ → .main/")
	} else {
		fmt.Fprintf(out, "  %-*s%s\n", labelWidth, "Main worktree", ".main/ (already moved)")
	}

	names := projectNames(plan.Projects)
	projectLine := fmt.Sprintf("%d migrated", len(names))
	if len(names) > 0 {
		projectLine += " (" + strings.Join(names, ", ") + ")"
	}
	fmt.Fprintf(out, "  %-*s%s\n", labelWidth, "Projects", projectLine)

	tasks := 0
	lowest, highest := 0, 0
	for _, name := range names {
		for _, task := range plan.Projects.Projects[name].Tasks {
			// The block is contiguous, but computing the low and high water
			// marks rather than assuming "first" and "last" keeps the summary
			// honest whatever order the tasks sit in.
			if tasks == 0 || task.ID < lowest {
				lowest = task.ID
			}
			if tasks == 0 || task.ID > highest {
				highest = task.ID
			}
			tasks++
		}
	}
	if tasks > 0 {
		fmt.Fprintf(out, "  %-*s%d renumbered (%d-%d)\n", labelWidth, "Tasks", tasks, lowest, highest)
	}

	sessions := 0
	for _, name := range names {
		sessions += len(plan.Projects.Projects[name].Sessions)
	}
	if sessions > 0 {
		fmt.Fprintf(out, "  %-*s%d migrated\n", labelWidth, "Sessions", sessions)
	}

	if len(plan.Invoices) > 0 {
		fmt.Fprintf(out, "  %-*s%d moved\n", labelWidth, "Invoices", len(plan.Invoices))
	}

	if len(plan.Warnings) > 0 {
		fmt.Fprintf(out, "  %s\n", "Warnings")
		for _, warning := range plan.Warnings {
			fmt.Fprintf(out, "    %s\n", warning)
		}
	}
}

// projectNames returns the migrated project names in sorted order, which is the
// order they were visited in — the plan already uses it for task renumbering.
func projectNames(projects config.ProjectsConfig) []string {
	names := make([]string, 0, len(projects.Projects))
	for name := range projects.Projects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
