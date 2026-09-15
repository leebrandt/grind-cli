// Package projects implements the project lifecycle: promote an idea into a
// project, list projects, and show one project.
//
// A project is a git worktree on its own branch, created from an EMPTY TREE
// so the worktree contains only the actual work product — never grind's
// state files. All project state lives in .projects.json on the default
// branch.
package projects

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
)

// Service performs project operations against a workspace using the given
// git implementation. The git layer is injected so tests can substitute a
// fake and verify exactly which operations run and in what order.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// Cleanup describes what to do with a project's worktree and branch after
// a lifecycle operation (publish or cancel).
type Cleanup int

const (
	// CleanupNone keeps both the worktree and the branch.
	CleanupNone Cleanup = iota
	// CleanupWorktree removes the worktree but keeps the branch.
	CleanupWorktree
	// CleanupBoth removes the worktree, then the branch. Deleting the
	// branch requires the worktree to be gone first — git refuses to
	// delete a checked-out branch.
	CleanupBoth
)

// EnsureProjectsClean fails with a user error when .projects.json has
// uncommitted changes. Only this file blocks project creation: it is the
// one file Create overwrites, so a hand-edited version would be clobbered
// by the atomic write. A dirty idea file is fine — its content is captured
// into .projects.json before the file is deleted.
func (s *Service) EnsureProjectsClean(ws *workspace.Workspace) error {
	clean, err := s.Git.IsPathClean(ws.MainWorktree, ".projects.json")
	if err != nil {
		return err
	}
	if !clean {
		return grinderr.NewUser("You have uncommitted changes in .projects.json. Commit or discard them first.")
	}
	return nil
}

// Create promotes an idea into a project. ideaFilename is the name of the
// idea file inside ideas/ (from ideas.Service.Resolve). The idea content is
// read here, stored in the project entry, and the file is deleted as part
// of the same transaction.
//
// The git worktree operations run FIRST, before any state file is touched.
// They are the risky ones (branch name validity, directory collisions), so
// a failure never leaves .projects.json or ideas/ half-mutated — the state
// files are the source of truth and must not be corrupted. If a later step
// fails after the worktree was created, the worktree is left in place and
// the error notes how to remove it.
func (s *Service) Create(ws *workspace.Workspace, name, projectType, ideaFilename string) (*config.ProjectEntry, error) {
	// Fail fast when .projects.json is dirty. Create overwrites it, so a
	// hand-edited version would be silently replaced. Everything else in
	// .main is left alone — Commit stages only the specific paths it is
	// given, so unrelated edits can never be swept into the
	// project-creation commits (the v1 bug this design avoids).
	if err := s.EnsureProjectsClean(ws); err != nil {
		return nil, err
	}

	if err := validateName(name); err != nil {
		return nil, err
	}

	// The config supplies both the valid project types and the billing
	// defaults. A missing .grind.json falls back to the defaults.
	cfg, err := readConfig(ws)
	if err != nil {
		return nil, err
	}

	if err := config.ValidateType(cfg, projectType); err != nil {
		return nil, err
	}

	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		projects = config.DefaultProjects()
	}
	if _, exists := projects.Projects[name]; exists {
		return nil, grinderr.NewUser(fmt.Sprintf("Project '%s' already exists.", name))
	}

	// The worktree path must be free, otherwise `git worktree add` would
	// fail after the branch was already created.
	worktreePath := ws.ProjectWorktreePath(name)
	if _, err := os.Stat(worktreePath); err == nil {
		return nil, grinderr.NewUser(fmt.Sprintf("Invalid project name %q: directory already exists", name))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, grinderr.WrapSystem(err, "check project directory %s", worktreePath)
	}

	// Worktree-first: the git operations are the risky ones. Doing them
	// before touching state files means a failure never leaves
	// .projects.json or ideas/ corrupted.
	if err := s.Git.CreateBranch(ws.BareRepo, name); err != nil {
		return nil, err
	}
	if err := s.Git.AddWorktree(ws.BareRepo, worktreePath, name); err != nil {
		return nil, err
	}

	// From here on the worktree exists. Any failure leaves it in place, so
	// wrap the error with a note about removing it.
	ideaPath := filepath.Join(ws.IdeasDir(), ideaFilename)
	ideaContent, err := os.ReadFile(ideaPath)
	if err != nil {
		return nil, worktreeNote(name, grinderr.WrapSystem(err, "read idea file %s", ideaPath))
	}

	// The project branch starts from an EMPTY TREE, so the idea has to be
	// seeded explicitly: write the full idea content to .idea in the
	// worktree and commit it. The .idea file is the project's seed — the
	// first work product on the branch.
	ideaFile := filepath.Join(worktreePath, ".idea")
	if err := os.WriteFile(ideaFile, ideaContent, 0o644); err != nil {
		return nil, worktreeNote(name, grinderr.WrapSystem(err, "write idea file %s", ideaFile))
	}
	title := ideas.ExtractTitle(string(ideaContent))
	if err := s.Git.Commit(worktreePath, "Add idea: "+title, ".idea"); err != nil {
		return nil, worktreeNote(name, err)
	}

	entry := &config.ProjectEntry{
		Name: name,
		Type: projectType,
		// The idea value is the H1 header of the idea file, not the full
		// content — the full content lives in the project's .idea file.
		Idea: title,
		Billing: config.BillingEntry{
			RoundTo: cfg.Billing.RoundTo,
			Rate:    cfg.Billing.DefaultRate,
		},
		// Truncate to seconds so the stored timestamp matches the RFC3339
		// shape in the spec (no fractional seconds).
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	projects.Projects[name] = *entry

	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		return nil, worktreeNote(name, err)
	}
	if err := s.Git.Commit(ws.MainWorktree, "Create project: "+name, ".projects.json"); err != nil {
		return nil, worktreeNote(name, err)
	}

	// Delete the idea file and commit the deletion as a separate commit so
	// the history shows the promotion as two distinct steps.
	if err := os.Remove(ideaPath); err != nil {
		return nil, worktreeNote(name, grinderr.WrapSystem(err, "delete idea file %s", ideaPath))
	}
	if err := s.Git.Commit(ws.MainWorktree,
		fmt.Sprintf("Remove idea %s (now project %s)", ideaFilename, name),
		"ideas/"+ideaFilename); err != nil {
		return nil, worktreeNote(name, err)
	}

	return entry, nil
}

// List returns all projects sorted by name, skipping canceled ones. A
// missing .projects.json is an empty workspace, not an error.
//
// Canceled projects disappear from the list to match v1, where cancel
// removed the worktree and the worktree-driven list naturally dropped the
// project. The rewrite's list is .projects.json-driven, so the filter is
// explicit. Published projects stay — their worktree is preserved.
func (s *Service) List(ws *workspace.Workspace) ([]config.ProjectEntry, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	entries := make([]config.ProjectEntry, 0, len(projects.Projects))
	for _, entry := range projects.Projects {
		if entry.Status == "canceled" {
			continue
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Get returns one project, or a user error when it does not exist.
func (s *Service) Get(ws *workspace.Workspace, name string) (*config.ProjectEntry, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, grinderr.NewUser(fmt.Sprintf("Project '%s' not found.", name))
		}
		return nil, err
	}
	entry, ok := projects.Projects[name]
	if !ok {
		return nil, grinderr.NewUser(fmt.Sprintf("Project '%s' not found.", name))
	}
	return &entry, nil
}

// Require returns one project, or a user error when it does not exist. It
// differs from Get only in the error wording: work and save say "does not
// exist" while edit and show say "not found".
func (s *Service) Require(ws *workspace.Workspace, name string) (*config.ProjectEntry, error) {
	_, entry, err := loadProject(ws, name)
	return &entry, err
}

// Publish merges the project branch into the default branch, exports the
// final draft to published/, and marks the project published. It requires
// both worktrees to be clean so the merge is safe and .main stays clean.
// cleanup is applied AFTER the publish commit: the work is in main, so
// removing the worktree and/or branch loses nothing.
func (s *Service) Publish(ws *workspace.Workspace, name string, cleanup Cleanup) error {
	entry, err := s.Require(ws, name)
	if err != nil {
		return err
	}

	worktreePath := ws.ProjectWorktreePath(name)
	if _, err := os.Stat(worktreePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return grinderr.NewUser(fmt.Sprintf("Project worktree '%s' does not exist.", name))
		}
		return grinderr.WrapSystem(err, "check project worktree %s", worktreePath)
	}

	// The merge rewrites .main's history, so both worktrees must be clean
	// first. The error names exactly what to run — the same phrasing push
	// uses, so the user learns one recovery verb.
	mainDirty, err := s.Git.HasChanges(ws.MainWorktree)
	if err != nil {
		return err
	}
	if mainDirty {
		return grinderr.NewUser("Main worktree has uncommitted changes. Run 'grind save' to commit them.")
	}
	projectDirty, err := s.Git.HasChanges(worktreePath)
	if err != nil {
		return err
	}
	if projectDirty {
		return grinderr.NewUser(fmt.Sprintf("Project '%s' has uncommitted changes. Run 'grind save %s' to commit them.", name, name))
	}

	// Merge BEFORE any state change: a failed merge leaves nothing marked
	// published (v1 committed the config first and could strand a project
	// marked published with an unmerged branch).
	if err := s.Git.MergeBranch(ws.MainWorktree, name); err != nil {
		return grinderr.NewUser(fmt.Sprintf(
			"Merge failed for project '%s'. Resolve conflicts in .main manually, then run 'grind save'.", name))
	}

	draftPath, err := s.writeDraft(ws, entry, worktreePath)
	if err != nil {
		return err
	}

	entry.Status = "published"
	if err := writeEntry(ws, name, *entry); err != nil {
		return err
	}
	if err := s.Git.Commit(ws.MainWorktree, "Publish project: "+name, ".projects.json", draftPath); err != nil {
		return err
	}

	return s.applyCleanup(ws, name, cleanup)
}

// Cancel marks the project canceled in .projects.json, then applies
// cleanup. The entry stays as a record; the confirmation prompt lives in
// the CLI, not here. The record is committed BEFORE the cleanup so a
// cleanup failure leaves a canceled project with a lingering worktree
// (recoverable) rather than a deleted worktree with an active project
// (data loss with no record).
func (s *Service) Cancel(ws *workspace.Workspace, name string, cleanup Cleanup) error {
	entry, err := s.Require(ws, name)
	if err != nil {
		return err
	}

	worktreePath := ws.ProjectWorktreePath(name)
	if _, err := os.Stat(worktreePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return grinderr.NewUser(fmt.Sprintf("Project worktree '%s' does not exist.", name))
		}
		return grinderr.WrapSystem(err, "check project worktree %s", worktreePath)
	}

	entry.Status = "canceled"
	if err := writeEntry(ws, name, *entry); err != nil {
		return err
	}
	if err := s.Git.Commit(ws.MainWorktree, "Cancel project: "+name, ".projects.json"); err != nil {
		return err
	}

	return s.applyCleanup(ws, name, cleanup)
}

// writeDraft builds the final-draft markdown file for a project and writes
// it to published/<name>.md in the main worktree. It returns the path
// relative to .main so the caller can stage exactly that file.
//
// The body is the project's .idea file — the founding document the user
// edits as the work product. If .idea is missing (Create always seeds it,
// but a hand-edited workspace may not have one), the idea title from
// .projects.json is the fallback.
func (s *Service) writeDraft(ws *workspace.Workspace, entry *config.ProjectEntry, worktreePath string) (string, error) {
	body, err := os.ReadFile(filepath.Join(worktreePath, ".idea"))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", grinderr.WrapSystem(err, "read .idea file in %s", worktreePath)
		}
		body = []byte(entry.Idea)
	}

	title := entry.Idea
	if title == "" {
		title = entry.Name
	}

	// The date is LOCAL, not UTC — the same decision as deadlines and due
	// dates, so "published today" matches the user's calendar.
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", title)
	if entry.Type != "" {
		fmt.Fprintf(&b, "type: %s\n", entry.Type)
	}
	fmt.Fprintf(&b, "date: %s\n", time.Now().Format("2006-01-02"))
	cfg, err := readConfig(ws)
	if err != nil {
		return "", err
	}
	if cfg.My != nil && cfg.My.Name != "" {
		fmt.Fprintf(&b, "author: %s\n", cfg.My.Name)
	}
	b.WriteString("status: published\n")
	b.WriteString("---\n\n")
	b.Write(body)
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}

	relPath := filepath.Join("published", entry.Name+".md")
	absPath := filepath.Join(ws.MainWorktree, relPath)
	if err := os.WriteFile(absPath, []byte(b.String()), 0o644); err != nil {
		return "", grinderr.WrapSystem(err, "write draft %s", absPath)
	}
	return relPath, nil
}

// applyCleanup removes the project's worktree and/or branch per the user's
// choice. It is shared by Publish and Cancel so both verbs ask the same
// question and behave identically.
func (s *Service) applyCleanup(ws *workspace.Workspace, name string, cleanup Cleanup) error {
	switch cleanup {
	case CleanupNone:
		return nil
	case CleanupWorktree:
		return s.Git.RemoveWorktree(ws.BareRepo, ws.ProjectWorktreePath(name))
	case CleanupBoth:
		// The branch can only be deleted after the worktree is gone — git
		// refuses to delete a branch that is checked out in a worktree.
		if err := s.Git.RemoveWorktree(ws.BareRepo, ws.ProjectWorktreePath(name)); err != nil {
			return err
		}
		return s.Git.DeleteBranch(ws.BareRepo, name)
	default:
		return grinderr.NewSystem(fmt.Sprintf("unknown cleanup choice %d", cleanup))
	}
}

// writeEntry updates one project in .projects.json and writes the file
// atomically.
func writeEntry(ws *workspace.Workspace, name string, entry config.ProjectEntry) error {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		return err
	}
	projects.Projects[name] = entry
	return config.WriteProjects(ws.ProjectsConfigPath(), projects)
}

// validateName checks the project name rules from the spec. The name IS the
// git branch name, so most rules are git's own check-ref-format rules.
func validateName(name string) error {
	if name == "" {
		return grinderr.NewUser(`Invalid project name "": name is empty`)
	}
	if strings.ContainsAny(name, " \t") {
		return grinderr.NewUser(fmt.Sprintf("Invalid project name %q: must not contain whitespace", name))
	}
	switch name {
	case ".main", ".grind.repo.git":
		return grinderr.NewUser(fmt.Sprintf("Invalid project name %q: %q is reserved", name, name))
	}
	if reason := invalidBranchName(name); reason != "" {
		return grinderr.NewUser(fmt.Sprintf("Invalid project name %q: %s", name, reason))
	}
	return nil
}

// invalidBranchName returns a reason why name is not a valid git branch
// name, or "" when it is valid. The rules mirror git check-ref-format
// --branch for the subset of names grind cares about.
func invalidBranchName(name string) string {
	if name == "HEAD" {
		return "HEAD is reserved"
	}
	if strings.HasSuffix(name, ".lock") {
		return "must not end in .lock"
	}
	if strings.HasPrefix(name, "-") {
		return "must not start with -"
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return "must not start or end with /"
	}
	if strings.Contains(name, "..") {
		return "must not contain .."
	}
	if strings.Contains(name, "@{") {
		return "must not contain @{"
	}
	if strings.ContainsAny(name, "~^:?*[\\") {
		return "must not contain ~ ^ : ? * [ \\"
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "must not contain control characters"
		}
	}
	return ""
}

// readConfig loads .grind.json, falling back to defaults when the file is
// missing (a workspace created by an older grind version may not have one).
func readConfig(ws *workspace.Workspace) (config.GrindConfig, error) {
	cfg, err := config.Read(ws.GrindConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.Default(), nil
		}
		return config.GrindConfig{}, err
	}
	return cfg, nil
}

// worktreeNote wraps a system error with a note that the project worktree
// was created and left in place, so the user knows to remove it. The
// original error stays in the chain, so errors.Is/errors.As still work on
// it and the exit code is unchanged.
func worktreeNote(name string, err error) error {
	return grinderr.WrapSystem(err,
		"project worktree %s was created; remove it with 'git worktree remove %s'", name, name)
}
