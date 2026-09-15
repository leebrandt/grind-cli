# Spec 09: Publish / Cancel

**Slice 9 of the Go rewrite of the grind CLI.** This slice delivers the
project lifecycle verbs: `grind publish <project>` (merge the work into the
default branch and export a final draft for the content pipeline) and
`grind cancel <project>` (abandon a project: remove its worktree and branch,
keeping the entry as a record).

Everything else (invoices, migrate) comes in later slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (proposed — review before dispatching)

1. **`grind publish <project>` — merge + export + mark.** Merges the
   project branch into the default branch (`.main` is always on it), exports
   a final-draft markdown file (frontmatter + body) into `published/`, and
   marks the project `published` in `.projects.json`. No flags — v1's `-d`
   /`-D` (delete worktree/branch) and `-u <url>` (record publication URL)
   are dropped: the worktree is always preserved for future work, and the
   constitution says n8n owns the publication record.
2. **`grind cancel <project> [-y]` — confirm, then destroy.** Prompts for
   confirmation (or `-y` skips it), removes the project worktree and branch,
   and marks the project `canceled`. The entry STAYS in `.projects.json` as
   a record (v1 behavior). Cancel is local-only: no remote branch deletion —
   push/pull are the explicit sync verbs, and cancel must not hit the
   network.
3. **`Status` field on `ProjectEntry`.** `Status string json:"status,omitempty"`.
   Unset means `active` (the common case stays out of the file). Values:
   `active` (unset), `published`, `canceled`.
4. **Canceled projects disappear from `list projects` and `wwd`.** v1's list
   is worktree-driven, so cancel (which removes the worktree) makes the
   project vanish. The rewrite's list is `.projects.json`-driven, so it must
   filter `Status == "canceled"` explicitly to match. Published projects
   stay (their worktree is preserved). `show` and `config` still work on
   canceled projects — the entry is the record.
5. **No `publications` field.** v1 recorded `{url, publishedAt}` on publish.
   The constitution says "Grind only produces the draft; n8n does everything
   else" — recording where the draft ended up is n8n's job. Deliberate
   omission.
6. **Publish requires clean worktrees.** Both `.main` and the project
   worktree must have no changes; the error tells the user exactly what to
   run (`grind save` / `grind save <project>`). This keeps the invariant
   "main is clean after every command" and makes the merge trivially safe.
7. **Merge before state change.** Publish merges first, then writes the
   draft + marks published + commits. A failed merge leaves nothing marked —
   this fixes v1's order, which committed the config *before* merging and
   could leave a project marked published with an unmerged branch.
8. **The draft body is the project's `.idea` file.** The founding document,
   which the user edits as the work product. Frontmatter carries the
   metadata n8n needs: `title` (the idea title), `type`, `date` (today,
   local), `author` (`my.name` from `.grind.json`, omitted when unset),
   `status: published`.
9. **Re-publish is allowed.** Merging again is a no-op when the branch is
   already merged; the draft is rewritten (n8n picks up the change). Cancel
   gates itself naturally: a canceled project has no worktree, so publish
   fails with "Project worktree does not exist."
10. **`status` joins the project config list as a read-only key.** `grind
    config <project>` shows `status = active|published|canceled` (effective
    value, like `longTerm`), but it is NOT settable — lifecycle verbs own
    it. Same pattern as `defaultBranch` in workspace config.

## Workspace layout (unchanged)

```
<workspace-root>/
├── .grind.repo.git/        # bare git repo (shared history)
├── .main/                  # main worktree (hidden from everyday use)
│   ├── .grind.json         # workspace config
│   ├── .projects.json      # ALL project state (single file, versioned)
│   ├── ideas/              # timestamped markdown idea files
│   ├── journal/            # daily markdown entries: YYYY-MM-DD.md
│   └── published/          # final-draft exports (n8n picks these up)
└── <project>/              # project worktrees (one per project, own branch)
```

Key invariants (MUST hold for every command in this slice):

1. **No shell interpolation.** Every git command runs via `os/exec` with an
   argv array. Never build a shell string.
2. **The main worktree is clean after every successful command.** Publish
   and cancel commit immediately, staging only the specific files they
   changed.
3. **Typed errors with exit codes.** User errors exit 1, system errors exit 2,
   unexpected errors exit 99. `main()` maps errors to exit codes. No
   `os.Exit` inside commands.
4. **Config files are JSON with 2-space indentation, written atomically** —
   the existing `config.Write`/`WriteProjects` already do this.

## Data model

Add to `ProjectEntry` in `internal/config/config.go`:

```go
// Status is the project lifecycle state: "" (active), "published", or
// "canceled". Unset means active — omitempty keeps the common case out of
// the file. Only the publish/cancel verbs write it; the config command
// lists it read-only.
Status string `json:"status,omitempty"`
```

## Git interface additions (`internal/git`)

Three new methods on `Git` (and their `execGit` implementations):

```go
// MergeBranch runs `git merge --no-ff <branch>` in the worktree, merging
// the branch into the worktree's current branch. publish uses it to bring
// a project branch into the default branch. --no-ff guarantees a visible
// merge commit in history even when the merge could fast-forward.
MergeBranch(worktreePath, branch string) error

// RemoveWorktree runs `git worktree remove --force <path>` in the bare
// repo. cancel uses it after the user confirmed the destructive action;
// --force is safe because the confirmation already warned about losing
// uncommitted work.
RemoveWorktree(repoPath, worktreePath string) error

// DeleteBranch runs `git branch -D <branch>` in the bare repo, deleting
// the branch without checking whether it is merged. cancel uses it after
// removing the worktree.
DeleteBranch(repoPath, branch string) error
```

Every fake git in the test files (`internal/cli/cli_test.go`,
`internal/projects/projects_test.go`, `internal/config/service_test.go`,
`internal/sync/sync_test.go`, `internal/status/status_test.go`,
`internal/tasks/tasks_test.go`, `internal/ideas/ideas_test.go`,
`internal/workspace/workspace_test.go`) must implement the three new
methods. The projects fake also needs per-worktree dirty control for the
publish cleanliness checks (e.g. a `dirtyWorktrees map[string]bool`).

## Commands in this slice

### `grind publish <project>`

Flow (in `projects.Service.Publish(ws, name)`):

1. Project must exist → user error `Project '<name>' does not exist.`
   (reuse `Require`).
2. Project worktree must exist → user error
   `Project worktree '<name>' does not exist.`
3. Both worktrees must be clean:
   - `.main` dirty → user error
     `Main worktree has uncommitted changes. Run 'grind save' to commit them.`
   - project dirty → user error
     `Project '<name>' has uncommitted changes. Run 'grind save <name>' to commit them.`
4. `s.Git.MergeBranch(ws.MainWorktree, name)` — merge the project branch
   into the default branch. On failure → user error
   `Merge failed for project '<name>'. Resolve conflicts in .main manually, then run 'grind save'.`
5. Write the final draft (see below), returning its path relative to `.main`.
6. Set `entry.Status = "published"`, write `.projects.json`.
7. `s.Git.Commit(ws.MainWorktree, "Publish project: "+name, ".projects.json", "published/"+name+".md")`.

Output:

```
Published project 'my-blog'. Draft exported to published/my-blog.md.
```

The draft file `published/<name>.md`:

```markdown
---
title: My Blog
type: blog
date: 2026-09-14
author: Lee
status: published
---

# My Blog

...full content of the project's .idea file...
```

- `title`: the idea title (`entry.Idea`), falling back to the project name.
- `type`: omitted when unset.
- `date`: today, LOCAL timezone, `2006-01-02` (consistent with the
  deadline/due-date decision — local, not UTC).
- `author`: `my.name` from `.grind.json`; omitted when unset.
- Body: the full content of `<worktree>/.idea`. If `.idea` is missing
  (shouldn't happen — Create seeds it), fall back to `entry.Idea`.
- The file ends with a newline.

### `grind cancel <project> [-y]`

Flow (CLI command + `projects.Service.Cancel(ws, name)`):

1. CLI: project must exist → user error `Project '<name>' does not exist.`
2. CLI: worktree must exist → user error
   `Project worktree '<name>' does not exist.`
3. CLI: the user must not be inside the project worktree (compare
   `os.Getwd()` against `ws.ProjectWorktreePath(name)`, prefix match) →
   user error
   `You are inside this project's worktree. Run 'grind cancel <name>' from the workspace root.`
   This check runs BEFORE the prompt, so the user never confirms an action
   that would immediately fail.
4. CLI: unless `-y/--yes`, prompt
   `Cancel project '<name>'? This will permanently delete the worktree, branch, and project state. [y/N]`
   reading from `cmd.InOrStdin()`. Only an explicit `y`/`yes`
   (case-insensitive) proceeds; anything else (including EOF) is a silent
   no-op, exit 0.
5. Service: `s.Git.RemoveWorktree(ws.BareRepo, worktreePath)`.
6. Service: `s.Git.DeleteBranch(ws.BareRepo, name)`.
7. Service: set `entry.Status = "canceled"`, write `.projects.json`.
8. Service: `s.Git.Commit(ws.MainWorktree, "Cancel project: "+name, ".projects.json")`.

Output:

```
Project 'my-blog' cancelled.
```

### `list projects` and `wwd` filter canceled

- `projects.Service.List` skips entries with `Status == "canceled"`.
- `status.Service.Status` skips entries with `Status == "canceled"`.

### `grind config <project>` shows status

`flattenProject` gains a read-only entry: `status` always shows its
effective value (`active` when unset), like `longTerm`. It is NOT added to
`projectKeys`, so setting it fails with the invalid-key message.

## Package structure

```
cmd/grind/main.go           # entry point; maps errors to exit codes
internal/cli/               # cobra commands (thin wiring only)
internal/workspace/         # discovery, paths, Workspace struct
internal/config/            # .grind.json + .projects.json (schema + Service)
internal/git/               # git interface + os/exec implementation (3 new methods)
internal/grinderr/          # typed errors (User, System) + exit codes
internal/projects/          # project lifecycle: Create, Publish, Cancel, List
internal/status/            # dashboard row computation (filters canceled)
```

### `internal/projects` (extend)

```go
// Publish merges the project branch into the default branch, exports the
// final draft to published/, and marks the project published. It requires
// both worktrees to be clean so the merge is safe and .main stays clean.
func (s *Service) Publish(ws *workspace.Workspace, name string) error

// Cancel removes the project's worktree and branch, then marks the
// project canceled in .projects.json. The entry stays as a record; the
// confirmation prompt lives in the CLI, not here.
func (s *Service) Cancel(ws *workspace.Workspace, name string) error
```

Plus an unexported `writeDraft(ws, entry, worktreePath) (string, error)`
helper that builds the frontmatter + body and returns the relative path
(`published/<name>.md`) for staging.

### `internal/cli` (extend)

- `publish.go` (NEW): `newPublishCmd(svc *projects.Service)` —
  `grind publish <project>`, `cobra.ExactArgs(1)`, thin: resolve workspace,
  call `svc.Publish`, print.
- `cancel.go` (NEW): `newCancelCmd(svc *projects.Service)` —
  `grind cancel <project> [-y]`, `cobra.ExactArgs(1)`, flag
  `BoolP("yes", "y", false, "skip the confirmation prompt")`. Does the
  existence/inside-worktree checks, prompts, then calls `svc.Cancel`.
- `confirm.go` (NEW): a small prompt helper:
  ```go
  // confirm prints prompt to out and reads a y/n answer from in. It
  // returns true only for an explicit "y" or "yes" (case-insensitive);
  // anything else — including EOF — is a no.
  func confirm(in io.Reader, out io.Writer, prompt string) (bool, error)
  ```
  Uses `bufio.Scanner`; the prompt ends with `[y/N] `.
- `root.go`: wire `newPublishCmd` and `newCancelCmd`, bump the version
  constant to `0.90.7`.
- `AGENTS.md`: mark slice 9 done in the slice table (the command-pattern
  table already has publish/cancel rows).

## Testing

- `internal/git` (real git in temp dirs, following the existing
  `exec_test.go` patterns): `MergeBranch` merges a branch into main and
  leaves a merge commit; `RemoveWorktree` removes a registered worktree;
  `DeleteBranch` deletes a branch.
- `internal/projects` with a fake git (records `MergeBranch`,
  `RemoveWorktree`, `DeleteBranch`, `Commit`; per-worktree dirty control):
  - Publish happy path: merge called with (main, name); draft file exists
    at `published/<name>.md` with the exact frontmatter + body; status =
    `published` in `.projects.json`; commit message `Publish project:
    <name>` staging exactly `[".projects.json", "published/<name>.md"]`.
  - Publish with dirty `.main` → exact user error; with dirty project →
    exact user error; nonexistent project; missing worktree; merge failure
    → resolve-conflicts user error.
  - Draft content: title from idea, type omitted when unset, author from
    `.grind.json` (and omitted when unset), local date, body from `.idea`.
  - Cancel happy path: `RemoveWorktree` + `DeleteBranch` called; status =
    `canceled`; commit `Cancel project: <name>` staging exactly
    `[".projects.json"]`.
  - Cancel nonexistent project; missing worktree.
  - `List` skips canceled projects but keeps published ones.
- `internal/status`: `Status` skips canceled projects.
- `internal/config`: `flattenProject` includes `status` with its effective
  value; setting `status` fails with the invalid-key message. (Existing
  exact-entry assertions in `service_test.go` must be updated.)
- `internal/cli` with the fake git:
  - `publish <project>` happy path.
  - `cancel <project> -y` happy path.
  - `cancel <project>` without `-y`: feed `y` via `root.SetIn` → proceeds;
    feed `n` → no-op (no git calls, exit 0).
  - `cancel` from inside the project worktree → error before any prompt.
  - `publish`/`cancel` not in a workspace → `Not in a grind workspace.`
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind publish <project>` merges the branch, exports the draft to
  `published/<name>.md`, marks the project published, and leaves `.main`
  clean.
- `grind cancel <project>` confirms (or `-y`), removes the worktree and
  branch, marks the project canceled, and leaves `.main` clean.
- Canceled projects no longer appear in `list projects` or `wwd`.
- `grind config <project>` shows `status` (read-only).
- All error messages match the spec exactly; user errors exit 1.
- AGENTS.md marks slice 9 done.
- `git -C .main status --porcelain` is empty after every successful command
  in this slice.
- All tests pass, `go vet` clean.