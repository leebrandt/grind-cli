# Spec 12: Interactive `new project`

**Slice 12 of the Go rewrite of the grind CLI.** This slice delivers the
interactive path for `grind new project`: with no arguments the command lists
the workspace's ideas, asks which one to promote, suggests a branch-safe
project name derived from that idea, shows a summary, and creates the project
only after the user confirms. The two-argument form keeps working with no
prompts, and both forms now print the same short success block.

The requirements live in `specs/prd.md` (the PRD for this slice). This spec is
the *how*: files, signatures, exact strings, ordering, and tests. Where the two
disagree, the PRD wins and this spec is wrong.

Everything else — a TUI picker, prompting for billing/deadline/client fields,
creating an idea from inside the wizard, promoting rejected ideas — is **not**
in this slice. See "Extension points".

The user is learning Go with this project. Code must be idiomatic, readable,
and commented with *why* (not *what*). No clever one-liners.

## Design decisions (agreed with the user)

1. **Two forms, one command.** `grind new project` (zero positional
   arguments) is interactive; `grind new project <name> <idea-number>` is the
   existing non-interactive path. Nothing prompts in the two-argument form.
2. **Exactly 0 or 2 arguments.** One argument is a mistake worth naming, and
   three is always a typo, so *any* other count returns one user error rather
   than cobra's generic `accepts at most 2 arg(s)`.
3. **No type prompt.** Type stays flag-only. `-t/--type` applies to both
   forms. In the interactive form it is validated *before* the idea list is
   printed, so an invalid type never costs the user a full wizard.
4. **Invalid answers re-prompt; they never exit.** The wizard prints the same
   user error the two-argument form would print and asks again. Only
   *user* errors re-prompt: a system error (a config file that cannot be
   read, a directory that cannot be stat'd) aborts, because looping on it
   would be an infinite prompt nobody can escape.
5. **Confirm before creating.** Name, branch, worktree, type, idea title and
   billing are printed, then `Create project? [Y/n]`. Nothing — no branch, no
   worktree, no state write, no idea deletion — happens before the answer.
6. **Suggest a name, but never suggest one that cannot be accepted.** The
   suggestion is a slug of the chosen idea's title. If that slug is already
   taken, reserved, or otherwise invalid in this workspace, the prompt shows
   no suggestion instead of inviting the user to press Enter into an error.
7. **EOF aborts, exit 0, nothing created.** Ctrl+D, a closed stdin, or
   `/dev/null` prints `Aborted.` and creates nothing. A final line without a
   trailing newline is still an answer — pipelines routinely end that way.
8. **One scanner for the whole wizard.** `bufio.Scanner` reads ahead, so a
   scanner per question would swallow answers typed ahead (or piped in) and
   every question after the first would see end of input. This is the single
   most important implementation detail in the slice.
9. **One success block, shared.** Both forms print
   `Created project:` / `Branch:` / `Worktree:` / `Next: work <name>` and
   nothing after it. The remaining-ideas list is removed: the wizard already
   showed the list, and `grind list ideas` is the command for it.
10. **Name rules have one home.** The wizard validates through
    `projects.ValidateNewName`, and `Create` uses the same two helpers it is
    built from. `Create`'s error precedence is unchanged (format, then type,
    then existence), because the helpers are split by phase rather than
    duplicated.
11. **Standard library only.** No new module in `go.mod`. Prompts are plain
    lines on stdout; a real terminal is not required, which is what makes the
    whole flow scriptable and testable with a `strings.Reader`.
12. **Docs and version move with the slice.** README documents both forms and
    the new output; AGENTS.md gains the slice row; `version` in
    `internal/cli/root.go` goes from `0.98.9` to `0.99.0`.

## Workspace layout

Unchanged. No new directories, no new files in the workspace, no new state.

## Data model

**No changes.** `.projects.json` keeps `version: 1`. The wizard produces the
same `ProjectEntry` `Create` already produces: name, type, idea H1, billing
copied from `.grind.json` defaults, `createdAt` from the clock.

## Commands in this slice

### `grind new project` — interactive

Run with zero positional arguments. Flow, in order:

1. `workspace.Require(".")` — same user error (`Not in a grind workspace.`,
   exit 1) as every other command.
2. `projectsSvc.EnsureProjectsClean(ws)` — the existing
   `You have uncommitted changes in .projects.json. Commit or discard them first.`
   (exit 1). Runs **before** the idea list and before any question, so no
   typing is wasted on a creation that cannot finish.
3. Read the workspace config with `projects.ReadConfig(ws)` (`.grind.json`,
   falling back to `config.Default()` when missing).
4. Read `-t/--type` and `config.ValidateType(cfg, projectType)` — existing
   `Invalid type: <type>. Valid types: <list>` (exit 1) before any prompt.
5. `ideasSvc.List(ws, ideas.ListOptions{})` — the same list `grind list ideas`
   shows: non-rejected, filename-sorted, numbered from 0.
6. If the list is empty, print
   `No ideas yet. Create one with: grind new idea "Your idea"` and return nil
   (exit 0). No prompt, nothing created. A workspace whose ideas are all
   rejected takes this path too.
7. Print `Select an idea:` then `idea.String()` for each idea, then a blank
   line.
8. Prompt `Idea number: ` until the answer resolves:
   - end of input → print `Aborted.` and return nil (nothing created);
   - `parseIdeaNumber` error → print `err.Error()` and ask again;
   - `ideasSvc.Resolve(ws, n)` error → print `err.Error()` and ask again.
   Resolving through `Resolve` rather than indexing the printed slice keeps
   the number meaning exactly what it means everywhere else.
9. Compute `projects.SuggestName(chosen.Title)`. If the suggestion is
   non-empty but `projectsSvc.ValidateNewName(ws, suggestion)` returns an
   error, drop it (prompt becomes `Project name: `). Only a *user* error
   means "unusable here" and drops the suggestion; a system error is returned,
   because it means the workspace cannot be read and hiding that behind a bare
   prompt would only postpone the failure.
10. Prompt `Project name [<suggestion>]: ` (or `Project name: `) until the
    answer validates:
    - end of input → `Aborted.`, return nil;
    - empty answer with a suggestion → use the suggestion;
    - otherwise the answer is the name;
    - `projectsSvc.ValidateNewName(ws, name)` error → print `err.Error()`,
      ask again with the same suggestion.
11. Print a blank line, then the summary (see "Output"), then prompt
    `Create project? [Y/n] ` until the answer is one of `""`, `y`, `yes`,
    `n`, `no` (case-insensitive; empty means yes). Any other answer re-asks
    the question only — the summary is not reprinted.
    - `n`/`no` → print `Aborted.`, return nil, nothing created;
    - end of input → `Aborted.`, return nil, nothing created.
12. `projectsSvc.Create(ws, name, projectType, chosen.Filename)` — the
    existing transaction: branch at an empty tree, worktree, `.idea` seed,
    `.projects.json`, idea deletion, all committed, `.main` clean.
13. `printCreatedProject(out, entry)` — the shared success block. Nothing is
    printed after it.

Every prompt writes to `cmd.OutOrStdout()` and every answer comes from
`cmd.InOrStdin()`, so `SetIn`/`SetOut` (and a pipe) drive the whole flow.

### `grind new project <name> <idea-number>` — non-interactive

The existing flow is unchanged step for step: require workspace, require a
clean `.projects.json`, parse the idea number, resolve the idea, read `-t`,
call `Create`. The only changes are output: it calls `printCreatedProject`
instead of printing the block and then re-listing the remaining ideas.

### Argument validation

Both forms hang off one cobra command:

```go
Use:   "project [<name> <idea-number>]",
Args:  validateProjectArgs,
```

```go
// validateProjectArgs accepts exactly zero arguments (interactive) or two
// (name and idea number). One argument is the mistake this feature exists to
// fix, and three is always a typo, so both get one message that says how to
// proceed instead of cobra's "accepts at most 2 arg(s)".
func validateProjectArgs(cmd *cobra.Command, args []string) error {
    if len(args) == 0 || len(args) == 2 {
        return nil
    }
    return grinderr.NewUser("new project requires either no arguments (interactive) or <name> <idea-number>")
}
```

`RunE` dispatches on `len(args)`: zero → `runNewProjectInteractive`, two →
`runNewProjectArgs`.

## Output

### Confirm summary

```text
Project:  my-blog
Branch:   my-blog
Worktree: my-blog/
Type:     blog
Idea:     My Blog
Rate:     $150/hr (quarter-hour)
```

- Labels are padded to a 10-character column (`"%-10s%s\n"`), one wider than
  `grind show`'s 9, because `Worktree:` is 9 characters and must still have a
  space before its value.
- `Type:` prints `—` when no type was given, matching `show` and
  `list projects`.
- `Rate:` reuses `invoice.Symbol` and `formatRate` from `show.go`, so the
  confirm screen shows exactly the numbers `Create` is about to store.
- The worktree is shown as `<name>/`, not the absolute path — the user is
  standing in the workspace root and the relative form is what `ls` shows.

### Success block (both forms)

```text
Created project: my-blog
Branch: my-blog
Worktree: my-blog/
Next: work my-blog
```

`Next:` names the verb that starts tracking time on the project just created.
No blank line and no remaining-ideas list follow it.

### Full interactive transcript

```text
$ grind new project
Select an idea:
  0. My Blog
  1. Video about Go

Idea number: 0
Project name [my-blog]:

Project:  my-blog
Branch:   my-blog
Worktree: my-blog/
Type:     —
Idea:     My Blog
Rate:     $150/hr (quarter-hour)

Create project? [Y/n] y
Created project: my-blog
Branch: my-blog
Worktree: my-blog/
Next: work my-blog
```

### Failure transcripts

Bad idea number, then a good one:

```text
Idea number: abc
Idea must be a number, got "abc"
Idea number: 99
Idea #99 not found. Run 'grind list ideas' to see available ideas.
Idea number: 0
```

Bad name, then a good one (the suggestion stays visible):

```text
Project name [my-blog]: my blog
Invalid project name "my blog": must not contain whitespace
Project name [my-blog]: my-blog
```

Declined:

```text
Create project? [Y/n] n
Aborted.
```

No ideas (exit 0, no prompts):

```text
$ grind new project
No ideas yet. Create one with: grind new idea "Your idea"
```

## Package structure

```
internal/projects/projects.go   # + ValidateNewName, + SuggestName, ReadConfig export, Create split
internal/cli/new.go             # arg routing, shared success block, non-interactive path
internal/cli/new_project.go     # NEW: the wizard and its prompt loops
internal/cli/prompt.go          # NEW: the one-scanner prompter
internal/cli/show.go            # unchanged; formatRate is reused
```

### `internal/projects` (extend)

**Rename `readConfig` → `ReadConfig`.** A one-word change, exported because
the CLI needs the billing defaults for the confirm summary and the type list
for the pre-prompt check. The missing-file fallback to `config.Default()`
stays exactly as it is; duplicating that fallback in the CLI would let the two
drift.

```go
// ReadConfig loads .grind.json, falling back to defaults when the file is
// missing (a workspace created by an older grind version may not have one).
// Exported because the interactive flow shows the billing values Create is
// about to copy and validates -t before asking anything.
func ReadConfig(ws *workspace.Workspace) (config.GrindConfig, error)
```

**Split name validation by phase, then compose it.**

```go
// ValidateNewName checks every rule Create enforces about a new project's
// name: format, reserved names, uniqueness in .projects.json, and a free
// worktree directory. It returns the same user error Create would, so the
// interactive flow can reject a name while the user is still typing instead
// of after the confirm screen.
func (s *Service) ValidateNewName(ws *workspace.Workspace, name string) error {
    if err := validateName(name); err != nil {
        return err
    }
    projects, err := config.ReadProjects(ws.ProjectsConfigPath())
    if err != nil {
        if !errors.Is(err, os.ErrNotExist) {
            return err
        }
        projects = config.DefaultProjects()
    }
    return s.nameAvailable(ws, projects, name)
}

// nameAvailable checks the two rules that need state rather than syntax: the
// name must not already be a project, and its worktree directory must be
// free. It takes the loaded projects file because Create already has it in
// hand; ValidateNewName loads its own because the interactive flow does not.
func (s *Service) nameAvailable(ws *workspace.Workspace, projects config.ProjectsConfig, name string) error
```

`nameAvailable` contains, unchanged, the two checks currently inline in
`Create`:

- `Project '<name>' already exists.`
- `Invalid project name %q: directory already exists`
- and the system-error wrap for a failed `os.Stat`.

`Create` keeps `validateName` where it is (first), keeps type validation where
it is (after `ReadConfig`), and replaces its inline existence/directory block
with `s.nameAvailable(ws, projects, name)`. Error precedence is therefore
identical to today's: format → type → existence → directory.

**Add the slug helper.** Pure, no workspace, so it lives beside the rules it
must satisfy:

```go
// SuggestName turns an idea title into a project-name suggestion: ASCII
// lower case, every run of characters outside [a-z0-9] collapsed to a single
// "-", with separators at either end never emitted. It returns "" when the
// result cannot be a valid project name (empty, or not branch-safe), so
// callers omit the suggestion rather than offer one that will be rejected.
//
// The workspace-independent half is deliberate: a suggestion that collides
// with an existing project is filtered by the caller, which has the
// workspace. Keeping this function pure is what makes its table test trivial.
func SuggestName(title string) string
```

Rules, in code terms:

```go
var b strings.Builder
pendingDash := false
for _, r := range title {
    if r >= 'A' && r <= 'Z' {
        r += 'a' - 'A'
    }
    if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
        // Emit the remembered separator only between words, never at the
        // start: that is what turns ".main" into "main" and "  spaced" into
        // "spaced" instead of leading with a dash.
        if pendingDash && b.Len() > 0 {
            b.WriteByte('-')
        }
        pendingDash = false
        b.WriteRune(r)
        continue
    }
    // Anything else (spaces, punctuation, non-ASCII letters) is a
    // separator. Remembering it rather than writing it immediately is what
    // collapses "Go & Rust!" into "go-rust", not "go---rust".
    pendingDash = true
}
```

The loop never emits a trailing separator, so no trim step is needed. The
function then returns `""` when `validateName(slug)` fails and `slug`
otherwise. A slug is always made of `[a-z0-9-]` with no leading or trailing
dash, so in practice the only way it fails is by being empty (a title with no
ASCII letters or digits). The check stays because it is the rule the
suggestion must satisfy, and a future change to either function should be
caught here rather than in a git error.

Because the slug rule is ASCII on purpose (`Café Notes` → `caf-notes`),
explicit ranges are used rather than `unicode.ToLower`, which would quietly
produce non-ASCII output the rule forbids.

### `internal/cli/prompt.go` (new)

```go
// prompter reads answers from one scanner and writes questions to one
// writer. One scanner for the whole wizard is deliberate: bufio reads ahead,
// so creating a scanner per question would swallow answers typed ahead (or
// piped in) and every question after the first would see end of input.
type prompter struct {
    scanner *bufio.Scanner
    out     io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter

// ask writes label and returns the next answer with surrounding whitespace
// trimmed. ok is false at end of input; err is a system error ("read
// answer") when the reader itself fails. A final line without a trailing
// newline is still an answer — shell pipelines often end without one.
func (p *prompter) ask(label string) (answer string, ok bool, err error)
```

`ask` is the only place that touches stdin. It wraps scanner errors with
`grinderr.WrapSystem(err, "read answer")` so every caller can simply return
them.

### `internal/cli/new.go` (change)

- `newProjectCmd` builds one command with `Use: "project [<name> <idea-number>]"`,
  `Args: validateProjectArgs`, and a `RunE` that dispatches on `len(args)`.
- Split today's body into `runNewProjectArgs(cmd, args, ideasSvc, projectsSvc)`
  — unchanged except that it ends with `printCreatedProject` instead of the
  remaining-ideas block — and `runNewProjectInteractive`.
- Add the shared printer:

```go
// printCreatedProject writes the block both invocation forms share. The
// Next hint names `work`, the verb that starts tracking time on the project
// that was just created.
func printCreatedProject(out io.Writer, entry *config.ProjectEntry) {
    fmt.Fprintf(out, "Created project: %s\n", entry.Name)
    fmt.Fprintf(out, "Branch: %s\n", entry.Name)
    fmt.Fprintf(out, "Worktree: %s/\n", entry.Name)
    fmt.Fprintf(out, "Next: work %s\n", entry.Name)
}
```

### `internal/cli/new_project.go` (new)

The wizard, in three small loops plus the orchestration. Each loop takes the
prompter and the thing it validates, and returns `(value, ok, error)` where
`ok == false` means end of input — leaving the `Aborted.` message to the one
caller that knows which prompt ended.

```go
// runNewProjectInteractive is the body of `grind new project` with no
// positional arguments. It reads flags, validates everything that can be
// validated before asking, drives the questions, and only then creates.
func runNewProjectInteractive(cmd *cobra.Command, ideasSvc *ideas.Service, projectsSvc *projects.Service) error

// askIdea asks for an idea number until one resolves. The list is printed by
// the caller; resolution goes through ideas.Service.Resolve so a number means
// the same thing here as in `reject idea` and the two-argument form.
func askIdea(p *prompter, ws *workspace.Workspace, svc *ideas.Service) (ideas.Idea, bool, error)

// askName asks for a project name until it passes ValidateNewName. A
// non-empty suggestion becomes the answer for an empty line, but never for
// end of input: Ctrl+D must abort, not create a project named after a slug.
// A rejected answer prints the same error the two-argument form would print.
func askName(p *prompter, ws *workspace.Workspace, svc *projects.Service, suggestion string) (string, bool, error)

// askConfirm asks the create question until the answer is one of "", y, yes,
// n, no. It returns proceed=false for "n", "no", and end of input; the caller
// prints "Aborted." for every false.
func askConfirm(p *prompter) (proceed bool, err error)
```

`askConfirm` must not use a bare `break` inside its `switch` — that exits the
switch, not the loop. Either label the loop or return from inside it:

```go
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
}
```

Re-prompt loops validate with `grinderr.ExitCode(err) != 1` to decide whether
to print and continue; anything that is not a user error returns immediately:

```go
if err := svc.ValidateNewName(ws, name); err != nil {
    if grinderr.ExitCode(err) != 1 {
        return "", false, err
    }
    fmt.Fprintln(p.out, err)
    continue
}
```

## Testing

### `internal/projects` (unit)

- `TestSuggestName`, table-driven. At minimum:

  | title | suggestion |
  |---|---|
  | `My Blog` | `my-blog` |
  | `Video about Go` | `video-about-go` |
  | `Go & Rust!` | `go-rust` |
  | `Café Notes` | `caf-notes` |
  | `ALLCAPS` | `allcaps` |
  | `a..b` | `a-b` |
  | `a/b` | `a-b` |
  | `  2026 goals  ` | `2026-goals` |
  | `trailing---` | `trailing` |
  | `!!!` | `""` |
  | `日本語` | `""` |
  | `.main` | `main` |
  | `HEAD` | `head` |

- `TestValidateNewName`: a clean name passes; whitespace, reserved names,
  `HEAD`, an existing project, and an existing directory each return the same
  message `validateName`/`Create` already returns. The directory case uses a
  temp workspace with a real directory at `ProjectWorktreePath(name)`.
- `Create`'s existing tests must pass unmodified — the refactor is a
  behaviour-preserving extraction, and they are the guard.

### `internal/cli` (with the fake git)

All wizard tests use the existing `runInWorkspace` + `executeWithIn` helpers;
`strings.NewReader` supplies the answers, which also proves the
no-terminal requirement.

- `TestNewProjectInteractive` — one idea, answers `0\nmy-blog\ny\n`:
  output contains `Select an idea:`, `0. My Blog`, `Idea number:`,
  `Project name [my-blog]:`, every summary line, the shared success block
  ending in `Next: work my-blog`, and **no** `Remaining ideas:`.
  `CreateBranch` was called once with `my-blog`; the commits are the seed,
  `.projects.json`, and the idea deletion; `my-blog/.idea` holds the full
  idea; the ideas dir is empty.
- `TestNewProjectInteractiveAcceptsSuggestion` — answers `0\n\ny\n`; project
  `my-blog` exists.
- `TestNewProjectInteractiveRepromptsBadIdea` — answers
  `abc\n99\n0\nmy-blog\ny\n`; output contains both error messages, and the
  project is created.
- `TestNewProjectInteractiveRepromptsBadName` — `0\nmy blog\nmy-blog\ny\n`;
  output contains `must not contain whitespace`; project created.
- `TestNewProjectInteractiveRepromptsExistingName` — pre-create `my-blog`,
  then `0\nmy-blog\nother\ny\n`; output contains
  `Project 'my-blog' already exists.` and `other` is created.
- `TestNewProjectInteractiveRepromptsExistingDirectory` — create a real
  directory named `taken` in the workspace root, then `0\ntaken\nfresh\ny\n`;
  output contains `directory already exists`.
- `TestNewProjectInteractiveDeclined` — `0\nmy-blog\nn\n`; output ends with
  `Aborted.`, `fake.createBranch` is empty, `.projects.json` unchanged.
- `TestNewProjectInteractiveEOF` — `0\n`; output ends with `Aborted.`, nothing
  created, and the suggested name was not accepted.
- `TestNewProjectInteractiveNoIdeas` — no input; output is exactly the
  no-ideas line; no prompt appears; error is nil.
- `TestNewProjectInteractiveRejectedOnly` — create an idea, reject it, then
  run; same no-ideas line, and the rejected title is not listed.
- `TestNewProjectInteractiveDirtyProjects` —
  `fake.dirtyPaths[".projects.json"] = true`; the existing dirty-state error
  is returned, output contains no `Select an idea:`.
- `TestNewProjectInteractiveInvalidType` — `new project -t nope` with input
  supplied; the existing invalid-type error is returned and no prompt is
  printed.
- `TestNewProjectInteractiveTypeFlag` — `new project -t blog`, answers
  `0\nmy-blog\ny\n`; summary contains `Type:     blog` and the stored entry's
  type is `blog`.
- `TestNewProjectArgCount` — `new project my-blog` and
  `new project a b c` both return the argument-count user error
  (`grinderr.ExitCode(err) == 1`).
- `TestPrompterKeepsBufferedAnswers` — `newPrompter(strings.NewReader("a\nb\n"), &buf)`,
  two `ask` calls return `a` then `b`. This is the regression guard for the
  one-scanner rule.
- `TestPrompterAcceptsFinalLineWithoutNewline` — `"a\nb"` returns `a` then
  `b`, then `ok == false`.

### Existing tests that must change

- `TestNewProjectCommand` — `Next: cd my-blog` becomes `Next: work my-blog`,
  and the `Remaining ideas:` assertion is replaced by an assertion that the
  block is absent.
- `TestVersionFlag` — follows the version bump.

No other existing test asserts the changed output (checked: only
`cli_test.go` contains `Next: cd` / `Remaining ideas:`).

### Docs

- `README.md`: the `new` section documents both forms, the confirm screen, the
  `Next: work <name>` line, and that the ideas are no longer re-listed.
- `AGENTS.md`: slice table gains `12 | Interactive new project | ✅ done` (or
  the in-progress marker the slice workflow uses), and the command-pattern
  section notes that a bare `new project` is the interactive form.

## Extension points

Deliberately out of this slice, each additive:

- **TUI picker.** `prompter` is a struct, not a package-level singleton, so a
  future `huh`/`bubbletea` implementation can satisfy the same three `ask`
  call sites behind a small interface.
- **More questions.** Billing rate, rounding rule, deadline, client details —
  each is one more `askX` in `runNewProjectInteractive`, and each already has
  a `grind config <project>` path.
- **Inline idea creation.** The idea prompt could accept `n` to open
  `$EDITOR` and continue with the new idea.
- **Pagination.** The idea list is printed whole; a long list could page.
- **Reuse.** `prompter` is general enough for `publish`/`cancel`'s cleanup
  question (today a separate scanner in `confirm.go`), but this slice does not
  touch them: one consumer is not yet a shared abstraction.

## Definition of done

- `grind new project` with no arguments runs the transcript above
  end-to-end against real git: idea list, idea number, name suggestion,
  summary, confirm, creation.
- `grind new project <name> <idea-number>` still works with no prompts and
  prints the shared success block with `Next: work <name>`, and no idea list.
- Any positional-argument count other than 0 or 2 returns the argument-count
  user error, exit 1.
- Invalid idea numbers and invalid/duplicate names re-prompt with the exact
  messages in this spec; no invalid answer ever exits non-zero.
- `n` at the confirm, and end-of-input at any prompt, print `Aborted.`, exit
  0, and leave the workspace untouched.
- A workspace with no promotable ideas prints the hint and exits 0 without
  prompting.
- A dirty `.projects.json` and an invalid `-t` fail before the first prompt.
- `git -C .main status --porcelain` is empty after every successful creation,
  and the new worktree contains only the `.idea` seed.
- No new module in `go.mod`; prompts work with piped stdin and redirected
  stdout.
- `gofmt -l .` prints nothing; `go build ./...`, `go vet ./...`,
  `go test ./...`, `go test -race ./...`, and `TZ=Pacific/Niue go test ./...`
  all pass.
- README and AGENTS.md are updated; the version constant is bumped.
