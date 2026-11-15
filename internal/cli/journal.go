package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/journal"
	"github.com/leebrandt/grind/internal/pager"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newEditJournalCmd builds `grind edit journal`. Editing is the ONE
// exception to the "main worktree is clean" invariant: the user is mid-edit
// and the entry stays untracked until the next `grind save`.
func newEditJournalCmd(clk clock.Clock) *cobra.Command {
	return &cobra.Command{
		Use:   "journal",
		Short: "Edit today's journal entry in your editor",
		Args:  cobra.NoArgs,
		RunE:  editJournalRunE(clk),
	}
}

// newJournalAliasCmd builds the hidden `grind journal` shortcut for
// `grind edit journal`. The user asked for this explicitly — v1's `grind
// journal` muscle memory — so it must stay hidden from --help.
func newJournalAliasCmd(clk clock.Clock) *cobra.Command {
	return &cobra.Command{
		Use:    "journal",
		Short:  "Edit today's journal entry (alias for 'edit journal')",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   editJournalRunE(clk),
	}
}

// editJournalRunE is the shared body of `edit journal` and the hidden
// `journal` alias.
func editJournalRunE(clk clock.Clock) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		ws, err := workspace.Require(".")
		if err != nil {
			return err
		}
		path, err := journal.OpenToday(ws, clk)
		if err != nil {
			return err
		}
		return editor.Open(path)
	}
}

// newReadCmd builds the `read` command group. This slice only has
// `read journal`; bare `grind read` shows help (same pattern as `done`).
func newReadCmd() *cobra.Command {
	read := &cobra.Command{
		Use:   "read",
		Short: "Read something",
	}

	journalCmd := &cobra.Command{
		Use:   "journal",
		Short: "Read journal entries in your pager",
		Args:  cobra.NoArgs,
		RunE:  readJournalRunE,
	}
	journalCmd.Flags().BoolP("reverse", "r", false, "newest first")
	journalCmd.Flags().Bool("no-pager", false, "print to stdout instead of the pager")
	read.AddCommand(journalCmd)

	return read
}

// readJournalRunE renders all journal entries oldest first and shows them in
// the pager. An empty journal prints nothing and exits 0 (v1 behavior).
func readJournalRunE(cmd *cobra.Command, args []string) error {
	ws, err := workspace.Require(".")
	if err != nil {
		return err
	}
	filenames, err := journal.List(ws)
	if err != nil {
		return err
	}
	if len(filenames) == 0 {
		return nil
	}

	// Both flags are registered in newReadCmd, so GetBool cannot fail here.
	reverse, _ := cmd.Flags().GetBool("reverse")
	noPager, _ := cmd.Flags().GetBool("no-pager")
	if reverse {
		// The list is already sorted oldest first, so swapping the ends
		// gives newest first.
		for i, j := 0, len(filenames)-1; i < j; i, j = i+1, j-1 {
			filenames[i], filenames[j] = filenames[j], filenames[i]
		}
	}

	var blocks []string
	for _, filename := range filenames {
		content, err := journal.Read(ws, filename)
		if err != nil {
			return err
		}
		date := strings.TrimSuffix(filename, ".md")
		// Trim trailing newlines so the blank-line join between blocks is
		// exactly one line. Editor-saved files almost always end with \n,
		// and a naive join would render two blank lines between entries.
		// The file itself is untouched — only the rendered output is
		// cleaned.
		blocks = append(blocks, fmt.Sprintf("─── %s ───\n\n%s", journal.FormatLongDate(date), strings.TrimRight(content, "\n")))
	}
	// The trailing newline matches v1's console.log.
	text := strings.Join(blocks, "\n\n") + "\n"

	if noPager {
		_, err := io.WriteString(cmd.OutOrStdout(), text)
		return err
	}
	// Page degrades to a plain write when stdout is not a terminal, so
	// `grind read journal > journal.txt` and `... | grep` keep working.
	return pager.Page(cmd.OutOrStdout(), text)
}
