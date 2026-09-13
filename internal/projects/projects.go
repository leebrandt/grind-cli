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
	"github.com/leebrandt/grind/internal/workspace"
)

// defaultTypes is the fallback list of project types when .grind.json does
// not configure any.
var defaultTypes = []string{"blog", "webapp", "video", "song", "book", "feature", "issue"}

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

// EnsureClean fails with a user error when the main worktree has
// uncommitted changes. The CLI calls it before resolving the idea number so
// the dirty check happens first, matching the spec's command flow.
func (s *Service) EnsureClean(ws *workspace.Workspace) error {
	clean, err := s.Git.IsClean(ws.MainWorktree)
	if err != nil {
		return err
	}
	if !clean {
		return grinderr.NewUser("You have uncommitted changes in .main. Commit or discard them first.")
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
	// Fail fast on a dirty main worktree. A leftover `edit idea` or any
	// other uncommitted change would otherwise be swept into the
	// project-creation commits — a real v1 bug.
	if err := s.EnsureClean(ws); err != nil {
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

	if err := validateType(cfg, projectType); err != nil {
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

	entry := &config.ProjectEntry{
		Name: name,
		Type: projectType,
		Idea: string(ideaContent),
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

// List returns all projects sorted by name. A missing .projects.json is an
// empty workspace, not an error.
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

// validTypes returns the effective project types: the configured list from
// .grind.json, or the default list when none is configured.
func validTypes(cfg config.GrindConfig) []string {
	if len(cfg.ProjectTypes) > 0 {
		return cfg.ProjectTypes
	}
	return defaultTypes
}

// validateType checks that projectType is in the effective types list. An
// empty type is always allowed — the type is optional at creation.
func validateType(cfg config.GrindConfig, projectType string) error {
	if projectType == "" {
		return nil
	}
	types := validTypes(cfg)
	for _, t := range types {
		if t == projectType {
			return nil
		}
	}
	return grinderr.NewUser(fmt.Sprintf("Invalid type: %s. Valid types: %s",
		projectType, strings.Join(types, ", ")))
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
