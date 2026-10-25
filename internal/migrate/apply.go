package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// Apply executes the plan and commits the result in one commit.
//
// The ORDER is the safety property, and it is the reason this function reads
// top to bottom rather than by noun:
//
//  1. The new state files are written first, into the v1 main worktree, so they
//     travel with the rename and so that the consolidated state exists on disk
//     before anything is destroyed. Writing first costs nothing — config's
//     writers are atomic — and it means an interruption can only ever leave
//     MORE information behind, never less.
//  2. Then the reversible file work: layout directories, invoice moves. Both
//     live inside the v1 main worktree, so they come along with the move.
//  3. Then the one step that cannot be undone by writing a file: the worktree
//     move. Everything that the migration needs to survive has already been
//     written at this point.
//  4. Then the destructive cleanup — deleting v1's per-project directories,
//     whose content is now in .projects.json, in the entry's idea field, or
//     under invoices/. It goes last because it is the only step that removes
//     data, and by the time it runs that data has a home.
//  5. One commit.
//
// A failure at any point leaves a workspace that the next `grind migrate`
// either finishes (the v1 configs are still readable) or reports as already
// migrated with nothing lost. There is no ordering that makes a multi-step
// rename across two directories atomic; there is an ordering where the worst
// case is a re-run and a dirty worktree.
func Apply(ws *workspace.Workspace, p *Plan, g git.Git) error {
	// A plan carries the paths it is going to rename and delete. Applying one to
	// a different workspace would rename the wrong directory, so fail loudly
	// instead of hoping the caller wired it up right.
	if p.Root != ws.Root {
		return grinderr.NewSystem(fmt.Sprintf("plan is for workspace %s, not %s", p.Root, ws.Root))
	}

	// 1. The state files, written through config's atomic writers so they come
	// out in v2's canonical form: 2-space indent, trailing newline. Written to
	// p.fromMain, not ws.MainWorktree, because before the move the main worktree
	// is still grind/ — and after it (a resumed run) the two are the same path.
	if err := config.Write(filepath.Join(p.fromMain, ".grind.json"), p.Config); err != nil {
		return err
	}
	if err := config.WriteProjects(filepath.Join(p.fromMain, ".projects.json"), p.Projects); err != nil {
		return err
	}

	// 2. The layout directories v2 expects. Git does not track empty
	// directories, so each needs a .gitkeep to survive the commit — the same
	// thing workspace.Init writes for a fresh workspace.
	for _, dir := range p.CreateDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return grinderr.WrapSystem(err, "create %s", dir)
		}
		keep := filepath.Join(dir, ".gitkeep")
		if exists(keep) {
			continue
		}
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return grinderr.WrapSystem(err, "write %s", keep)
		}
	}

	// 2b. Invoices, moved directory by directory so the v1 id (and the PDF
	// beside the markdown) comes along untouched.
	for _, invoice := range p.Invoices {
		if !exists(invoice.From) {
			// Already moved by an interrupted run; the plan would not have
			// listed it, so this only guards a hand-edited plan.
			continue
		}
		if err := os.MkdirAll(filepath.Dir(invoice.To), 0o755); err != nil {
			return grinderr.WrapSystem(err, "create %s", filepath.Dir(invoice.To))
		}
		if err := os.Rename(invoice.From, invoice.To); err != nil {
			return grinderr.WrapSystem(err, "move invoices for %s", invoice.Project)
		}
	}

	// 3. The worktree move. MoveWorktree asserts both paths are absolute, which
	// is what stops git from resolving the destination inside the bare repo.
	if p.MoveMain {
		if err := g.MoveWorktree(ws.BareRepo, p.fromMain, ws.MainWorktree); err != nil {
			return err
		}
	}

	// 4. v1's per-project directories. Their paths are recorded relative to the
	// main worktree, so they are resolved against the LIVE one — grinder after
	// the move, which is where the directories are now. Removing them by their
	// pre-move absolute path would quietly do nothing.
	for _, dir := range p.RemoveDirs {
		if err := os.RemoveAll(filepath.Join(ws.MainWorktree, p.relMain(dir))); err != nil {
			return grinderr.WrapSystem(err, "remove %s", dir)
		}
	}

	// 5. One commit, staging exactly the paths this migration touched. Never
	// `git add -A`: the v1 worktree is routinely dirty, and those edits are the
	// user's, not the migration's.
	return g.Commit(ws.MainWorktree, CommitMessage, p.commitPaths()...)
}

// commitPaths lists what the migration touched, relative to the main worktree.
//
// Relative because that is what git.Commit takes. The paths are the same before
// and after the worktree move — moving the worktree renames the directory, not
// its contents — so one list serves both.
func (p *Plan) commitPaths() []string {
	paths := []string{".grind.json", ".projects.json"}
	for _, dir := range p.CreateDirs {
		paths = append(paths, filepath.Join(p.relMain(dir), ".gitkeep"))
	}
	for _, invoice := range p.Invoices {
		paths = append(paths, p.relMain(invoice.To))
	}
	for _, dir := range p.RemoveDirs {
		paths = append(paths, p.relMain(dir))
	}
	return paths
}

// relMain makes a path relative to the main worktree, whichever side of the
// move it is on.
func (p *Plan) relMain(path string) string {
	rel, err := filepath.Rel(p.fromMain, path)
	if err != nil {
		// Every planned path is inside the main worktree, so this cannot
		// happen; returning the absolute path keeps git's error message honest
		// if it ever does.
		return path
	}
	return rel
}
