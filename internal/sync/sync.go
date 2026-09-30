// Package sync implements push and pull: the only two verbs that touch the
// remote. Everything else in grind is local by design — save commits, work
// tracks time, and only push/pull cross the network.
package sync

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// Scope selects what push operates on.
type Scope int

const (
	ScopeMain    Scope = iota // grind push — the default branch
	ScopeProject              // grind push <project>
	ScopeAll                  // grind push all
)

// Service performs push and pull against a workspace using the given git
// implementation. The git layer is injected so tests can substitute a fake
// and verify exactly which operations run and in what order.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// Push pushes the branches selected by scope (project names the branch for
// ScopeProject). It resolves the remote URL, syncs origin, refuses to push
// uncommitted work in scope, and maps a failed push to a user error.
//
// It returns the branch that was pushed so the caller can report it: the
// default branch for ScopeMain, the project name for ScopeProject, and
// "all" for ScopeAll.
func (s *Service) Push(ws *workspace.Workspace, scope Scope, project string) (string, error) {
	if _, err := s.resolveRemote(ws); err != nil {
		return "", err
	}

	switch scope {
	case ScopeMain:
		return s.pushMain(ws)
	case ScopeProject:
		return s.pushProject(ws, project)
	case ScopeAll:
		return s.pushAll(ws)
	}
	return "", nil
}

// pushMain pushes the default branch. The main worktree must be clean: the
// state files in .main are the source of truth, so pushing a dirty .main
// would send half-written config to the remote.
func (s *Service) pushMain(ws *workspace.Workspace) (string, error) {
	branch, err := s.Git.DefaultBranch(ws.BareRepo)
	if err != nil {
		return "", err
	}
	dirty, err := s.Git.HasChanges(ws.MainWorktree)
	if err != nil {
		return "", err
	}
	if dirty {
		return "", grinderr.NewUser("You have uncommitted changes in .main. Run 'grind save' to commit them.")
	}
	if err := s.pushOne(ws, branch); err != nil {
		return "", err
	}
	return branch, nil
}

// pushProject pushes one project's branch. The project must exist in
// .projects.json, and its worktree must be clean when the directory exists
// (a missing worktree directory is skipped — the branch can still be
// pushed).
func (s *Service) pushProject(ws *workspace.Workspace, name string) (string, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		return "", err
	}
	if _, ok := projects.Projects[name]; !ok {
		return "", grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", name))
	}

	worktreePath := ws.ProjectWorktreePath(name)
	if isDir(worktreePath) {
		dirty, err := s.Git.HasChanges(worktreePath)
		if err != nil {
			return "", err
		}
		if dirty {
			return "", grinderr.NewUser(fmt.Sprintf(
				"You have uncommitted changes in %s/. Run 'grind save %s' to commit them.", name, name))
		}
	}
	if err := s.pushOne(ws, name); err != nil {
		return "", err
	}
	return name, nil
}

// pushAll pushes every branch. Every worktree in scope must be clean; a
// single error lists all the dirty ones so the user can fix them in one
// pass.
func (s *Service) pushAll(ws *workspace.Workspace) (string, error) {
	var dirty []string

	mainDirty, err := s.Git.HasChanges(ws.MainWorktree)
	if err != nil {
		return "", err
	}
	if mainDirty {
		dirty = append(dirty, "You have uncommitted changes in .main. Run 'grind save' to commit them.")
	}

	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		return "", err
	}
	// Sort the project names so the error message is deterministic.
	names := make([]string, 0, len(projects.Projects))
	for name := range projects.Projects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		worktreePath := ws.ProjectWorktreePath(name)
		if !isDir(worktreePath) {
			// A project whose worktree directory is missing cannot be
			// dirty; the branch can still be pushed.
			continue
		}
		has, err := s.Git.HasChanges(worktreePath)
		if err != nil {
			return "", err
		}
		if has {
			dirty = append(dirty, fmt.Sprintf(
				"You have uncommitted changes in %s/. Run 'grind save %s' to commit them.", name, name))
		}
	}

	if len(dirty) > 0 {
		return "", grinderr.NewUser(strings.Join(dirty, "\n"))
	}
	if err := s.pushAllBranches(ws); err != nil {
		return "", err
	}
	return "all", nil
}

// pushOne runs `git push origin <branch>` and maps a failed push to a user
// error carrying git's stderr — the user asked for the push, so a failure
// is something they need to act on.
func (s *Service) pushOne(ws *workspace.Workspace, branch string) error {
	err := s.Git.PushBranch(ws.BareRepo, branch)
	if err == nil {
		return nil
	}
	var pushErr *git.PushError
	if errors.As(err, &pushErr) {
		return grinderr.NewUser(fmt.Sprintf("Could not push to remote: %s", pushErr.Stderr))
	}
	return err
}

// pushAllBranches runs `git push origin --all` and maps a failed push to a
// user error, same as pushOne.
func (s *Service) pushAllBranches(ws *workspace.Workspace) error {
	err := s.Git.PushAll(ws.BareRepo)
	if err == nil {
		return nil
	}
	var pushErr *git.PushError
	if errors.As(err, &pushErr) {
		return grinderr.NewUser(fmt.Sprintf("Could not push to remote: %s", pushErr.Stderr))
	}
	return err
}

// PullResult summarizes what pull did. Empty slices are omitted from the
// printed summary.
type PullResult struct {
	Updated  []string // fast-forwarded branches
	Diverged []string // need a manual merge
	Dirty    []string // worktree had uncommitted changes
	Created  []string // new worktrees
	Skipped  []string // worktree creation failed
}

// Pull fetches from origin, fast-forwards every local branch that can be,
// and creates worktrees for remote project branches that have none.
func (s *Service) Pull(ws *workspace.Workspace) (*PullResult, error) {
	if _, err := s.resolveRemote(ws); err != nil {
		return nil, err
	}

	// A fast-forward merge would refuse to run against a dirty .main
	// anyway; failing here gives a clean message before any network call.
	mainDirty, err := s.Git.HasChanges(ws.MainWorktree)
	if err != nil {
		return nil, err
	}
	if mainDirty {
		return nil, grinderr.NewUser("You have uncommitted changes in .main. Run 'grind save' before pulling.")
	}

	if err := s.Git.FetchAll(ws.BareRepo); err != nil {
		return nil, err
	}

	defaultBranch, err := s.Git.DefaultBranch(ws.BareRepo)
	if err != nil {
		return nil, err
	}

	// The remote branch names tell us which branches exist upstream. A
	// local branch with no remote counterpart is skipped — there is nothing
	// to fast-forward to.
	remoteBranches, err := s.Git.ListRemoteBranches(ws.BareRepo)
	if err != nil {
		return nil, err
	}
	remoteSet := make(map[string]bool, len(remoteBranches))
	for _, name := range remoteBranches {
		remoteSet[name] = true
	}

	// Local branches are the default branch plus every project branch from
	// the LOCAL .projects.json. Projects that arrive in this pull's
	// fast-forward of the default branch are not in the list yet — their
	// worktrees are created below, which also creates their branches. A
	// missing .projects.json means no project branches, matching the
	// projects package's tolerance for old workspaces.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if errors.Is(err, os.ErrNotExist) {
		projects = config.DefaultProjects()
	}
	localBranches := []string{defaultBranch}
	for name := range projects.Projects {
		localBranches = append(localBranches, name)
	}
	sort.Strings(localBranches[1:]) // keep the default branch first

	result := &PullResult{}
	for _, branch := range localBranches {
		if !remoteSet[branch] {
			continue
		}
		if err := s.fastForward(ws, branch, defaultBranch, result); err != nil {
			return nil, err
		}
	}

	// Create worktrees for remote branches that have none. The default
	// branch already has .main; every other remote branch is a project
	// branch that should get a worktree at <root>/<branch>.
	for _, branch := range remoteBranches {
		if branch == defaultBranch {
			continue
		}
		worktreePath := ws.ProjectWorktreePath(branch)
		if isDir(worktreePath) {
			continue
		}
		if err := s.Git.AddWorktree(ws.BareRepo, worktreePath, branch); err != nil {
			// A stale directory or an invalid branch name is not worth
			// aborting the whole pull over; report it and move on.
			result.Skipped = append(result.Skipped, branch)
			continue
		}
		result.Created = append(result.Created, branch)
	}

	return result, nil
}

// fastForward updates one local branch to match its remote tracking branch
// when a fast-forward is possible. Branches that cannot be fast-forwarded
// are reported (diverged or dirty) and left alone.
func (s *Service) fastForward(ws *workspace.Workspace, branch, defaultBranch string, result *PullResult) error {
	remoteRef := "origin/" + branch

	// IsAncestor is true when the branches are equal too, so check both
	// directions to tell the three cases apart:
	//   both true      → up to date, nothing to do
	//   local ahead    → remote is an ancestor of local, nothing to pull
	//   local behind   → fast-forward possible
	//   neither        → diverged, needs a manual merge
	localIsAncestor, err := s.Git.IsAncestor(ws.BareRepo, branch, remoteRef)
	if err != nil {
		return err
	}
	remoteIsAncestor, err := s.Git.IsAncestor(ws.BareRepo, remoteRef, branch)
	if err != nil {
		return err
	}
	if localIsAncestor && remoteIsAncestor {
		return nil // already up to date
	}
	if remoteIsAncestor {
		// Local is ahead of the remote: there is nothing new to pull.
		// Not an error — the user just has unpushed work.
		return nil
	}
	if !localIsAncestor {
		result.Diverged = append(result.Diverged, branch)
		return nil
	}

	// A fast-forward is possible. Branches checked out in a worktree are
	// merged in the worktree so the working files refresh; branches without
	// a worktree just move their ref in the bare repo.
	worktreePath := ""
	switch {
	case branch == defaultBranch:
		worktreePath = ws.MainWorktree
	case isDir(ws.ProjectWorktreePath(branch)):
		worktreePath = ws.ProjectWorktreePath(branch)
	}

	if worktreePath == "" {
		if err := s.Git.FastForwardRef(ws.BareRepo, branch); err != nil {
			return err
		}
		result.Updated = append(result.Updated, branch)
		return nil
	}

	if err := s.Git.FastForwardWorktree(worktreePath, branch); err != nil {
		// A failed merge is either a dirty worktree (git refuses to
		// overwrite uncommitted changes) or a diverged branch. Check the
		// worktree to tell them apart.
		dirty, dirtyErr := s.Git.HasChanges(worktreePath)
		if dirtyErr != nil {
			return dirtyErr
		}
		if dirty {
			result.Dirty = append(result.Dirty, branch)
		} else {
			result.Diverged = append(result.Diverged, branch)
		}
		return nil
	}
	result.Updated = append(result.Updated, branch)
	return nil
}

// resolveRemote finds the remote URL and syncs the bare repo's origin from
// it. The URL comes from .grind.json's remote.url first, with the git
// remote as fallback so old workspaces keep working. No URL anywhere is a
// user error.
func (s *Service) resolveRemote(ws *workspace.Workspace) (string, error) {
	// A missing .grind.json is tolerated (old workspaces may not have one);
	// any other read failure is real.
	cfg, err := config.Read(ws.GrindConfigPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil && cfg.Remote != nil && cfg.Remote.URL != "" {
		if err := s.Git.SetRemoteURL(ws.BareRepo, cfg.Remote.URL); err != nil {
			return "", err
		}
		return cfg.Remote.URL, nil
	}

	url, err := s.Git.RemoteURL(ws.BareRepo)
	if err != nil {
		return "", err
	}
	if url != "" {
		// The URL already lives on the git remote; syncing origin is a
		// no-op but keeps the flow uniform.
		if err := s.Git.SetRemoteURL(ws.BareRepo, url); err != nil {
			return "", err
		}
		return url, nil
	}

	return "", grinderr.NewUser("No remote configured. Set remote.url in .grind.json first.")
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
