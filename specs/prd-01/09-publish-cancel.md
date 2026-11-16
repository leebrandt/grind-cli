# Spec 09: Publish / Cancel

**Slice 9 of the Go rewrite of the grind CLI.** This slice delivers the
project lifecycle verbs: `grind publish <project>` (merge the work into the
default branch and export a final draft for the content pipeline) and
`grind cancel <project>` (abandon a project, keeping the entry as a record).

Everything else (invoices, migrate) comes in later slices — do NOT build it.

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

1. **`grind publish <project> [-y]` — merge + export + mark.** Merges the
   project branch into the default branch (`.main` is always on it), exports
   a final-draft markdown file (frontmatter + body) into `published/`, and
   marks the project `published` in `.projects.json`. v1's `-u <url>` flag
   is dropped — the constitution says n8n owns the publication record.
2. **`grind cancel <project> [-y]` — mark canceled + cleanup.** Marks the
   project `canceled` in `.projects.json` (the entry STAYS as a record, v1
   behavior) and cleans up the worktree/branch per the user's choice.
   Cancel is local-only: no remote branch deletion — push/pull are the
   explicit sync verbs, and cancel must not hit the network.
2b. **Cancel also cancels the project's outstanding tasks.** Every open task
   (`Done == false`) gets a `Canceled` flag in the same write + commit as
   the status change. Done tasks stay done — they were completed before the
   project ended. Canceled tasks are hidden from `grind tasks` (both the
   open view and `-a`), hidden from `wwd`'s task section, and excluded from
   `wwd`'s task count and urgency. `done task N` on a canceled task refuses
   with `Task #N is canceled.` The flag (not deletion) preserves the record
   of what was planned — same philosophy as the project entry staying as a
   record.
3. **One standard cleanup prompt for BOTH commands.** After the operation,
   both verbs ask the same question:
   ```
   Delete worktree and/or branch for 'my-blog'? [w/x/n]
   ```
   - `w` — remove the worktree, keep the branch (v1's `-d`)
   - `x` — remove both: worktree first, then branch (v1's `-D`)
   - `n` — keep both (v1's no-flag); the default on Enter/EOF
   There is deliberately NO "branch only" option: git refuses to delete a
   branch that is checked out in a worktree (`error: cannot delete branch
   '<b>' used by worktree at '<path>'`), so deleting the branch requires
   removing the worktree first. `-y` skips the prompt and means `x` (full
   cleanup) — the "yes, do the whole thing" semantics, and it keeps
   `grind cancel my-blog -y` behaving like the old cancel. The prompt
   re-asks on invalid input; EOF defaults to `n` (nothing deleted unless
   explicitly asked).
4. **`Status` field on `ProjectEntry`.** `Status string json:"status,omitempty"`.
   Unset means `active` (the common case stays out of the file). Values:
   `active` (unset), `published`, `canceled`.
5. **Canceled projects disappear from `list projects` and `wwd`.** v1's list
   is worktree-driven, so cancel (which removes the worktree) makes the
   project vanish. The rewrite's list is `.projects.json`-driven, so it must
   filter `Status == "canceled"` explicitly to match. Published projects
   stay (their worktree is preserved). `show` and `config` still work on
   canceled projects — the entry is the record.
6. **No `publications` field.** v1 recorded `{url, publishedAt}` on publish.
   The constitution says "Grind only produces the draft; n8n does everything
   else" — recording where the draft ended up is n8n's job. Deliberate
   omission (this is why `-u` is gone).
7. **Publish requires clean worktrees.** Both `.main` and the project
   worktree must have no changes; the error tells the user exactly what to
   run (`grind save` / `grind save <project>`). This keeps the invariant
   "main is clean after every command" and makes the merge trivially safe.
8. **Merge before state change.** Publish merges first, then writes the
   draft + marks published + commits. A failed merge leaves nothing marked —
   this fixes v1's order, which committed the config *before* merging and
   could leave a project marked published with an unmerged branch.
9. **The draft body is the project's `.idea` file.** The founding document,
   which the user edits as the work product. Frontmatter carries the
   metadata n8n needs: `title` (the idea title), `type`, `date` (today,
   local), `author` (`my.name` from `.grind.json`, omitted when unset),
   `status: published`.
10. **Re-publish is allowed.** Merging again is a no-op when the branch is
    already merged; the draft is rewritten (n8n picks up the change). Cancel
    gates itself naturally: a canceled project has no worktree, so publish
    fails with "Project worktree does not exist."
11. **`status` joins the project config list as a read-only key.** `grind
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

Add to `Task` in the same file:

```go
// Canceled marks an open task that was abandoned when its project was
// canceled. Canceled tasks are hidden from the task list and carry no
// urgency; the flag preserves the record of what was planned. Done
// tasks are never canceled — they were completed before the project
// ended.
Canceled bool `json:"canceled,omitempty"`
```

## Git interface additions (`internal/git`)

Three new methods on `Git` (and their `execGit` implementations):

```go
// MergeBranch runs `git merge --no-ff --allow-unrelated-histories <branch>`
// in the worktree, merging the branch into the worktree's current branch.
// publish uses it to bring a project branch into the default branch. --no-ff
// guarantees a visible merge commit in history even when the merge could
// fast-forward. --allow-unrelated-histories is REQUIRED: project branches
// start from an empty tree (CreateBranch), so they share no history with
// main — a plain merge fails with "refusing to merge unrelated histories".
// The merge brings the work product (the .idea file) into main's tree; there
// is no conflict because the project branch and main touch disjoint paths.
MergeBranch(worktreePath, branch string) error

// RemoveWorktree runs `git worktree remove --force <path>` in the bare
// repo. publish/cancel use it after the user chose to clean up; --force is
// safe because the cleanup prompt already warned about losing uncommitted
// work.
RemoveWorktree(repoPath, worktreePath string) error

// DeleteBranch runs `git branch -D <branch>` in the bare repo, deleting
// the branch without checking whether it is merged. It is only called
// AFTER the worktree is removed — git refuses to delete a branch that is
// checked out in a worktree.
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

### `grind publish <project> [-y]`

Flow:

1. `projects.Service.Publish(ws, name, cleanup)`:
   - Project must exist → user error `Project '<name>' does not exist.`
     (reuse `Require`).
   - Project worktree must exist → user error
     `Project worktree '<name>' does not exist.`
   - Both worktrees must be clean:
     - `.main` dirty → user error
       `Main worktree has uncommitted changes. Run 'grind save' to commit them.`
     - project dirty → user error
       `Project '<name>' has uncommitted changes. Run 'grind save <name>' to commit them.`
   - `s.Git.MergeBranch(ws.MainWorktree, name)` — merge the project branch
     into the default branch. On failure → user error
     `Merge failed for project '<name>'. Resolve conflicts in .main manually, then run 'grind save'.`
   - Write the final draft (see below), returning its path relative to `.main`.
   - Set `entry.Status = "published"`, write `.projects.json`.
   - `s.Git.Commit(ws.MainWorktree, "Publish project: "+name, ".projects.json", "published/"+name+".md")`.
   - Apply cleanup: `w` → `RemoveWorktree`; `x` → `RemoveWorktree` then
     `DeleteBranch`; `n` → nothing. Cleanup runs AFTER the publish commit —
     the work is already in main, so removing the worktree/branch loses
     nothing, and `.main` stays clean.
2. CLI: unless `-y`, prompt
   `Delete worktree and/or branch for '<name>'? [w/x/n] ` reading from
   `cmd.InOrStdin()`, re-asking on invalid input, defaulting to `n` on
   Enter/EOF. `-y` skips the prompt and means `x`.

Output:

```
$ grind publish my-blog
Delete worktree and/or branch for 'my-blog'? [w/x/n] x
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

Flow:

1. CLI: project must exist → user error `Project '<name>' does not exist.`
2. CLI: worktree must exist → user error
   `Project worktree '<name>' does not exist.`
3. CLI: the user must not be inside the project worktree (compare
   `os.Getwd()` against `ws.ProjectWorktreePath(name)`, prefix match) →
   user error
   `You are inside this project's worktree. Run 'grind cancel <name>' from the workspace root.`
   This check runs BEFORE the prompt, so the user never confirms an action
   that would immediately fail.
4. CLI: unless `-y`, prompt the same cleanup question as publish:
   `Delete worktree and/or branch for '<name>'? [w/x/n] `. This prompt IS
   the confirmation — picking `x` is the explicit destructive choice.
5. `projects.Service.Cancel(ws, name, cleanup)`:
   - Set `entry.Status = "canceled"`, mark every open task
     `Canceled = true` (done tasks untouched), write `.projects.json`.
   - `s.Git.Commit(ws.MainWorktree, "Cancel project: "+name, ".projects.json")`.
   - Apply cleanup: `w` → `RemoveWorktree`; `x` → `RemoveWorktree` then
     `DeleteBranch`; `n` → nothing. The record is committed BEFORE the
     cleanup so a cleanup failure leaves a canceled project with a lingering
     worktree (recoverable) rather than a deleted worktree with an active
     project (data loss with no record).

Output:

```
$ grind cancel my-blog
Delete worktree and/or branch for 'my-blog'? [w/x/n] x
Project 'my-blog' cancelled.
```

### `list projects` and `wwd` filter canceled

- `projects.Service.List` skips entries with `Status == "canceled"`.
- `status.Service.Status` skips entries with `Status == "canceled"`.
- `tasks.Service.List` and `status` (task count + `TaskUrgency`) skip tasks
  with `Canceled == true`; `tasks.Service.Complete` refuses them.

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

// Publish merges the project branch into the default branch, exports the
// final draft to published/, and marks the project published. It requires
// both worktrees to be clean so the merge is safe and .main stays clean.
// cleanup is applied AFTER the publish commit: the work is in main, so
// removing the worktree and/or branch loses nothing.
func (s *Service) Publish(ws *workspace.Workspace, name string, cleanup Cleanup) error

// Cancel marks the project canceled in .projects.json, then applies
// cleanup. The entry stays as a record; the confirmation prompt lives in
// the CLI, not here.
func (s *Service) Cancel(ws *workspace.Workspace, name string, cleanup Cleanup) error
```

Plus an unexported `writeDraft(ws, entry, worktreePath) (string, error)`
helper that builds the frontmatter + body and returns the relative path
(`published/<name>.md`) for staging, and an unexported
`applyCleanup(ws, name, cleanup) error` shared by Publish and Cancel.

### `internal/cli` (extend)

- `publish.go` (NEW): `newPublishCmd(svc *projects.Service)` —
  `grind publish <project> [-y]`, `cobra.ExactArgs(1)`, flag
  `BoolP("yes", "y", false, "skip the cleanup prompt (delete worktree and branch)")`.
  Thin: resolve workspace, prompt for cleanup, call `svc.Publish`, print.
- `cancel.go` (NEW): `newCancelCmd(svc *projects.Service)` —
  `grind cancel <project> [-y]`, `cobra.ExactArgs(1)`, same `-y` flag.
  Does the existence/inside-worktree checks, prompts, then calls
  `svc.Cancel`.
- `confirm.go` (NEW): the shared prompt helper:
  ```go
  // confirmCleanup asks what to do with the project's worktree and branch
  // after a lifecycle operation. It returns projects.CleanupBoth when yes
  // is set (the prompt is skipped); otherwise it reads a w/x/n answer
  // from in, re-prompting until the answer is valid. Enter or EOF defaults
  // to projects.CleanupNone — nothing is deleted unless explicitly asked.
  func confirmCleanup(in io.Reader, out io.Writer, project string, yes bool) (projects.Cleanup, error)
  ```
  Uses `bufio.Scanner`; the prompt ends with `[w/x/n] `.
- `root.go`: wire `newPublishCmd` and `newCancelCmd`, bump the version
  constant to `0.90.7`.
- `AGENTS.md`: mark slice 9 done in the slice table (the command-pattern
  table already has publish/cancel rows).

## Testing

- `internal/git` (real git in temp dirs, following the existing
  `exec_test.go` patterns): `MergeBranch` merges a branch into main and
  leaves a merge commit — including the production shape where the branch
  starts from an empty tree (unrelated histories); `RemoveWorktree` removes
  a registered worktree; `DeleteBranch` deletes a branch (and, as a
  regression guard, fails when the branch is still checked out in a
  worktree).
- `internal/projects` with a fake git (records `MergeBranch`,
  `RemoveWorktree`, `DeleteBranch`, `Commit`; per-worktree dirty control):
  - Publish happy path per cleanup choice: `n` → no cleanup calls; `w` →
    `RemoveWorktree` called, no `DeleteBranch`; `x` → both called, in
    order. Draft file exists at `published/<name>.md` with the exact
    frontmatter + body; status = `published` in `.projects.json`; commit
    message `Publish project: <name>` staging exactly
    `[".projects.json", "published/<name>.md"]`.
  - Publish with dirty `.main` → exact user error; with dirty project →
    exact user error; nonexistent project; missing worktree; merge failure
    → resolve-conflicts user error.
  - Draft content: title from idea, type omitted when unset, author from
    `.grind.json` (and omitted when unset), local date, body from `.idea`.
  - Cancel happy path per cleanup choice: status = `canceled`; commit
    `Cancel project: <name>` staging exactly `[".projects.json"]`; cleanup
    calls per choice.
  - Cancel marks open tasks `Canceled = true` and leaves done tasks
    untouched (the flag, never deletion).
  - Cancel nonexistent project; missing worktree.
  - `List` skips canceled projects but keeps published ones.
- `internal/status`: `Status` skips canceled projects; task count and
  `TaskUrgency` skip canceled tasks.
- `internal/tasks`: `List` hides canceled tasks in both views; `Complete`
  refuses a canceled task with `Task #N is canceled.`
- `internal/config`: `flattenProject` includes `status` with its effective
  value; setting `status` fails with the invalid-key message. (Existing
  exact-entry assertions in `service_test.go` must be updated.)
- `internal/cli` with the fake git:
  - `publish <project> -y` → no prompt, full cleanup.
  - `publish <project>` without `-y`: feed `w`/`x`/`n` via `root.SetIn` →
    matching cleanup; invalid input re-prompts; EOF → `n`.
  - `cancel <project> -y` happy path.
  - `cancel <project>` without `-y`: feed `n` → canceled, no cleanup.
  - `cancel` from inside the project worktree → error before any prompt.
  - `publish`/`cancel` not in a workspace → `Not in a grind workspace.`
- `go vet ./...` and `go build ./...` must pass. `go test ./...` must pass.

## Definition of done

- `grind publish <project>` merges the branch, exports the draft to
  `published/<name>.md`, marks the project published, and leaves `.main`
  clean.
- `grind cancel <project>` marks the project canceled, cancels its
  outstanding tasks, and leaves `.main` clean.
- Both verbs end with the same `[w/x/n]` cleanup prompt; `-y` skips it and
  deletes both worktree and branch.
- Canceled projects no longer appear in `list projects` or `wwd`.
- `grind config <project>` shows `status` (read-only).
- All error messages match the spec exactly; user errors exit 1.
- AGENTS.md marks slice 9 done.
- `git -C .main status --porcelain` is empty after every successful command
  in this slice.
- All tests pass, `go vet` clean.