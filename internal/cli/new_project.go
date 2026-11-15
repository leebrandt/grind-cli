package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/invoice"
	"github.com/leebrandt/grind/internal/projects"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// runNewProjectInteractive is the body of `grind new project` with no
// positional arguments. It reads flags, validates everything that can be
// validated before asking, drives the questions, and only then creates.
//
// The ordering is the feature: a dirty .projects.json or an invalid -t fails
// before the idea list is even printed, so no typing is wasted on a creation
// that cannot finish.
func runNewProjectInteractive(cmd *cobra.Command, ideasSvc *ideas.Service, projectsSvc *projects.Service) error {
	ws, err := workspace.Require(".")
	if err != nil {
		return err
	}
	if err := projectsSvc.EnsureProjectsClean(ws); err != nil {
		return err
	}

	cfg, err := projects.ReadConfig(ws)
	if err != nil {
		return err
	}
	// The flag is registered on the command, so GetString cannot fail. Type
	// stays flag-only: the wizard never asks for it.
	projectType, _ := cmd.Flags().GetString("type")
	if err := config.ValidateType(cfg, projectType); err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	// The same list `grind list ideas` shows: non-rejected, filename-sorted,
	// numbered from 0. Resolving the answer through ideas.Service.Resolve
	// below keeps the number meaning identical everywhere.
	list, err := ideasSvc.List(ws, ideas.ListOptions{})
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(out, `No ideas yet. Create one with: grind new idea "Your idea"`)
		return nil
	}

	fmt.Fprintln(out, "Select an idea:")
	for _, idea := range list {
		fmt.Fprintln(out, idea.String())
	}
	fmt.Fprintln(out)

	p := newPrompter(cmd.InOrStdin(), out)

	chosen, ok, err := askIdea(p, ws, ideasSvc)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "Aborted.")
		return nil
	}

	// Suggest a name, but never one this workspace would reject: pressing
	// Enter into an error message is worse than no suggestion at all. Only
	// a user error means "unusable here"; a system error means the
	// workspace cannot be read, and hiding that behind a bare prompt would
	// only postpone the failure.
	suggestion := projects.SuggestName(chosen.Title)
	if suggestion != "" {
		if err := projectsSvc.ValidateNewName(ws, suggestion); err != nil {
			if grinderr.ExitCode(err) != 1 {
				return err
			}
			suggestion = ""
		}
	}

	name, ok, err := askName(p, ws, projectsSvc, suggestion)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "Aborted.")
		return nil
	}

	// The blank line separates the answers from the confirm screen, which
	// reads as one unit.
	fmt.Fprintln(out)
	printConfirmSummary(out, name, projectType, chosen.Title, projects.BillingDefaults(cfg))

	proceed, err := askConfirm(p)
	if err != nil {
		return err
	}
	if !proceed {
		fmt.Fprintln(out, "Aborted.")
		return nil
	}

	// Nothing above this line wrote anything: the branch, the worktree,
	// .projects.json and the idea deletion all happen inside Create.
	entry, err := projectsSvc.Create(ws, name, projectType, chosen.Filename)
	if err != nil {
		return err
	}
	printCreatedProject(out, entry)
	return nil
}

// askIdea asks for an idea number until one resolves. The list is printed by
// the caller; resolution goes through ideas.Service.Resolve so a number means
// the same thing here as in `reject idea` and the two-argument form.
//
// ok is false at end of input. Only user errors re-prompt; a system error is
// returned, because looping on it would be an infinite prompt nobody can
// escape.
func askIdea(p *prompter, ws *workspace.Workspace, svc *ideas.Service) (ideas.Idea, bool, error) {
	for {
		answer, ok, err := p.ask("Idea number: ")
		if err != nil || !ok {
			return ideas.Idea{}, false, err
		}

		n, err := parseIdeaNumber(answer)
		if err != nil {
			if grinderr.ExitCode(err) != 1 {
				return ideas.Idea{}, false, err
			}
			fmt.Fprintln(p.out, err)
			continue
		}

		chosen, err := svc.Resolve(ws, n)
		if err != nil {
			if grinderr.ExitCode(err) != 1 {
				return ideas.Idea{}, false, err
			}
			fmt.Fprintln(p.out, err)
			continue
		}
		return chosen, true, nil
	}
}

// askName asks for a project name until it passes ValidateNewName. A
// non-empty suggestion becomes the answer for an empty line, but never for
// end of input: Ctrl+D must abort, not create a project named after a slug.
// A rejected answer prints the same error the two-argument form would print,
// and the suggestion stays visible for the next attempt.
func askName(p *prompter, ws *workspace.Workspace, svc *projects.Service, suggestion string) (string, bool, error) {
	label := "Project name: "
	if suggestion != "" {
		label = fmt.Sprintf("Project name [%s]: ", suggestion)
	}

	for {
		answer, ok, err := p.ask(label)
		if err != nil || !ok {
			return "", false, err
		}

		name := answer
		if name == "" && suggestion != "" {
			name = suggestion
		}

		if err := svc.ValidateNewName(ws, name); err != nil {
			if grinderr.ExitCode(err) != 1 {
				return "", false, err
			}
			fmt.Fprintln(p.out, err)
			continue
		}
		return name, true, nil
	}
}

// askConfirm asks the create question until the answer is one of "", y, yes,
// n, no. It returns proceed=false for "n", "no", and end of input; the caller
// prints "Aborted." for every false.
//
// Each accepted answer returns from inside the loop on purpose: a bare break
// would exit the switch and re-ask a question the user just answered.
func askConfirm(p *prompter) (proceed bool, err error) {
	for {
		answer, ok, err := p.ask("Create project? [Y/n] ")
		if err != nil || !ok {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		// Any other answer re-asks the question only; the summary above it
		// is not reprinted.
	}
}

// printConfirmSummary writes the screen the user confirms: exactly the values
// Create is about to store. The label column is 10 characters wide — one
// wider than `grind show`'s 9 — because "Worktree:" is 9 characters and must
// still have a space before its value.
//
// billing comes from projects.BillingDefaults, the same function Create uses,
// so the rate shown here cannot drift from the rate that gets written.
func printConfirmSummary(out io.Writer, name, projectType, ideaTitle string, billing config.BillingEntry) {
	displayType := projectType
	if displayType == "" {
		displayType = "—"
	}
	fmt.Fprintf(out, "%-10s%s\n", "Project:", name)
	fmt.Fprintf(out, "%-10s%s\n", "Branch:", name)
	fmt.Fprintf(out, "%-10s%s/\n", "Worktree:", name)
	fmt.Fprintf(out, "%-10s%s\n", "Type:", displayType)
	fmt.Fprintf(out, "%-10s%s\n", "Idea:", ideaTitle)
	// The same symbol and rate formatting as `grind show`, so the confirm
	// screen and the stored entry never disagree.
	fmt.Fprintf(out, "%-10s%s%s/hr (%s)\n",
		"Rate:", invoice.Symbol, formatRate(billing.Rate), billing.RoundTo)
}
