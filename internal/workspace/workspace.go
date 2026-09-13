// Package workspace handles discovery of the grind workspace, workspace
// initialization, and all path construction within it.
//
// The workspace layout is:
//
//	<root>/
//	├── .grind.repo.git/   bare git repo (shared history)
//	├── .main/             main worktree (hidden from everyday use)
//	└── <project>/         project worktrees (future slices)
//
// All path construction lives here so no other package hardcodes ".main"
// or ".grind.repo.git".
package workspace

import (
	"os"
	"path/filepath"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
)

// Workspace describes a discovered grind workspace. All paths are absolute.
type Workspace struct {
	Root         string // directory containing the bare repo
	BareRepo     string // path to .grind.repo.git
	MainWorktree string // path to .main
}

// Find walks up from startDir looking for a .grind.repo.git directory.
// Project worktrees are siblings of the bare repo, so from inside a project
// the repo is found in a parent directory. Returns nil when no workspace
// exists.
func Find(startDir string) (*Workspace, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, grinderr.WrapSystem(err, "resolve start directory %s", startDir)
	}
	for {
		bareRepo := filepath.Join(dir, ".grind.repo.git")
		if isDir(bareRepo) {
			return &Workspace{
				Root:         dir,
				BareRepo:     bareRepo,
				MainWorktree: filepath.Join(dir, ".main"),
			}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding a workspace.
			return nil, nil
		}
		dir = parent
	}
}

// Require is like Find but turns "not found" into a user error so commands
// can fail with a helpful message.
func Require(startDir string) (*Workspace, error) {
	ws, err := Find(startDir)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, grinderr.NewUser("Not in a grind workspace.")
	}
	return ws, nil
}

// Init creates a new workspace skeleton in dir.
//
// The order matters: the bare repo must have an initial commit before
// `git worktree add` can create a branch from it, and the config files must
// exist before the final commit stages them.
func Init(g git.Git, dir string) error {
	bareRepo := filepath.Join(dir, ".grind.repo.git")
	if isDir(bareRepo) {
		return grinderr.NewUser("Already a grind workspace")
	}

	if err := g.InitBare(bareRepo); err != nil {
		return err
	}
	if err := g.InitialCommit(bareRepo, "main"); err != nil {
		return err
	}

	mainWorktree := filepath.Join(dir, ".main")
	if err := g.AddWorktree(bareRepo, mainWorktree, "main"); err != nil {
		return err
	}

	// Real `git worktree add` creates the directory, but a fake git in tests
	// does not. Making sure it exists keeps Init independent of the git
	// implementation.
	if err := os.MkdirAll(mainWorktree, 0o755); err != nil {
		return grinderr.WrapSystem(err, "create main worktree %s", mainWorktree)
	}

	ws := &Workspace{
		Root:         dir,
		BareRepo:     bareRepo,
		MainWorktree: mainWorktree,
	}

	if err := config.Write(ws.GrindConfigPath(), config.Default()); err != nil {
		return err
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), config.DefaultProjects()); err != nil {
		return err
	}

	// Git does not track empty directories, so each gets a .gitkeep to make
	// sure the layout exists after init.
	for _, sub := range []string{"ideas", "journal", "published"} {
		subDir := filepath.Join(mainWorktree, sub)
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			return grinderr.WrapSystem(err, "create %s directory", sub)
		}
		keep := filepath.Join(subDir, ".gitkeep")
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return grinderr.WrapSystem(err, "write %s/.gitkeep", sub)
		}
	}

	paths := []string{
		".grind.json",
		".projects.json",
		"ideas/.gitkeep",
		"journal/.gitkeep",
		"published/.gitkeep",
	}
	return g.Commit(mainWorktree, "Initialize grind workspace", paths...)
}

// IdeasDir returns the directory holding idea markdown files.
func (w *Workspace) IdeasDir() string {
	return filepath.Join(w.MainWorktree, "ideas")
}

// JournalDir returns the directory holding daily journal entries.
func (w *Workspace) JournalDir() string {
	return filepath.Join(w.MainWorktree, "journal")
}

// PublishedDir returns the directory holding final-draft exports.
func (w *Workspace) PublishedDir() string {
	return filepath.Join(w.MainWorktree, "published")
}

// GrindConfigPath returns the path to .grind.json.
func (w *Workspace) GrindConfigPath() string {
	return filepath.Join(w.MainWorktree, ".grind.json")
}

// ProjectsConfigPath returns the path to .projects.json.
func (w *Workspace) ProjectsConfigPath() string {
	return filepath.Join(w.MainWorktree, ".projects.json")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
