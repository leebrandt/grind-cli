package cli

import (
	"fmt"
	"io"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
	"github.com/spf13/cobra"
)

// newConfigCmd builds `grind config [project] [key] [value]`, the get/set/
// list command for workspace and project config.
//
// The first argument is a project when it exists in .projects.json;
// otherwise it is a workspace key. A project literally named like a
// workspace key (e.g. "currency") shadows that key — the same accepted
// edge case as `push all` shadowing a project named "all".
func newConfigCmd(svc *config.Service) *cobra.Command {
	return &cobra.Command{
		Use:   "config [project] [key] [value]",
		Short: "Get, set, or list workspace and project config",
		Args:  cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspace.Require(".")
			if err != nil {
				return err
			}
			paths := config.Paths{
				ConfigPath:   ws.GrindConfigPath(),
				ProjectsPath: ws.ProjectsConfigPath(),
				MainWorktree: ws.MainWorktree,
				BareRepo:     ws.BareRepo,
			}

			// Decide the scope: a first argument that names a project
			// switches to project config; anything else is a workspace key.
			project := ""
			if len(args) > 0 {
				isProject, err := svc.HasProject(paths, args[0])
				if err != nil {
					return err
				}
				if isProject {
					project = args[0]
					args = args[1:]
				}
			}

			out := cmd.OutOrStdout()
			if project != "" {
				return runProjectConfig(svc, paths, project, args, out)
			}
			return runWorkspaceConfig(svc, paths, args, out)
		},
	}
}

// runWorkspaceConfig dispatches the workspace-scope forms: list (no args),
// get (one arg), set (two args). Three args in workspace scope is a user
// error — the first arg was not a project, so there is nothing to do with
// the extra value.
func runWorkspaceConfig(svc *config.Service, paths config.Paths, args []string, out io.Writer) error {
	switch len(args) {
	case 0:
		entries, err := svc.List(paths)
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Fprintf(out, "%s = %s\n", e.Key, e.Value)
		}
		return nil
	case 1:
		value, err := svc.Get(paths, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(out, value)
		return nil
	case 2:
		if err := svc.Set(paths, args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s = %s\n", args[0], args[1])
		return nil
	default:
		return grinderr.NewUser("Too many arguments for workspace config.")
	}
}

// runProjectConfig dispatches the project-scope forms: list (no args), get
// (one arg), set (two args).
func runProjectConfig(svc *config.Service, paths config.Paths, project string, args []string, out io.Writer) error {
	switch len(args) {
	case 0:
		entries, err := svc.ProjectList(paths, project)
		if err != nil {
			return err
		}
		for _, e := range entries {
			fmt.Fprintf(out, "%s = %s\n", e.Key, e.Value)
		}
		return nil
	case 1:
		value, err := svc.ProjectGet(paths, project, args[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(out, value)
		return nil
	case 2:
		if err := svc.ProjectSet(paths, project, args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s = %s\n", args[0], args[1])
		return nil
	default:
		return grinderr.NewUser("Too many arguments for project config.")
	}
}
