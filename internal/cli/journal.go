package cli

import (
	"fmt"
	"strings"

	"github.com/leebrandt/grind/internal/editor"
	"github.com/leebrandt/grind/internal/journal"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newEditJournalCmd builds `grind edit journal`. Editing is the ONE
// exception to the "main worktree is clean" invariant: the user is mid-edit
// and the entry stays untracked until the next `grind save`.
func newEditJournalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "journal",
		Short: "Edit today's journal entry in your editor",
		Args:  cobra.NoArgs,
		RunE:  editJournalRunE,
	}
}

// newJournalAliasCmd builds the hidden `grind journal` shortcut for
// `grind edit journal`. The user asked for this explicitly — v1's `grind
// journal` muscle memory — so it must stay hidden from --help.
func newJournalAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "journal",
		Short:  "Edit today's journal entry (alias for 'edit journal')",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   editJournalRunE,
	}
}

// editJournalRunE is the shared body of `edit journal` and the hidden
// `journal` alias.
func editJournalRunE(cmd *cobra.Command, args []string) error {
	ws, err := workspace.Require(".")
	if err != nil {
		return err
	}
	path, err := journal.OpenToday(ws)
	if err != nil {
		return err
	}
	return editor.Open(path)
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
		Short: "Read journal entries",
		Args:  cobra.NoArgs,
		RunE:  readJournalRunE,
	}
	journalCmd.Flags().BoolP("reverse", "r", false, "newest first")
	read.AddCommand(journalCmd)

	return read
}

// readJournalRunE prints all journal entries to stdout, oldest first. An
// empty journal prints nothing and exits 0 (v1 behavior).
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

	// The flag is registered in newReadCmd, so GetBool cannot fail here.
	reverse, _ := cmd.Flags().GetBool("reverse")
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
	// Fprintln adds the trailing newline, matching v1's console.log.
	fmt.Fprintln(cmd.OutOrStdout(), strings.Join(blocks, "\n\n"))
	return nil
}
