---
title: Interactive `grind new project`
version: 0.1
date_created: 2026-11-14
last_updated: 2026-11-14
owner: Lee Brandt
status: Final
---

# Interactive `grind new project` — Product Requirements Document

## 1. Overview

`grind new project` currently requires the user to know and type two
positional arguments in one shot: a project name and an idea number. Both are
easy to get wrong — the idea number is a 0-based index printed by a different
command, and the name has to satisfy git branch rules the user may not
remember. This feature adds an interactive path: running `grind new project`
with no arguments shows the workspace's ideas, asks which one to promote,
suggests a project name derived from that idea, and confirms the creation
before anything is written. The existing two-argument form keeps working with
no prompts, and both forms share the same success output.

## 2. Problem Statement

Promoting an idea into a project today is a single, unforgiving command:

```bash
grind list ideas          # look up the number
grind new project my-blog 3 -t blog   # retype it correctly, or start over
```

Users must hold the idea number in their head between two commands, invent a
branch-safe name with no help, and only discover an invalid name, a taken
name, or a bad type after the command has already run. The name — which
becomes both the git branch and the on-disk worktree — is also the hardest
input in grind to get right, and the only one that cannot be changed later
without manual git surgery.

## 3. Goals

- A user who has never read the project-name rules can create a project
  from an idea without consulting documentation.
- A successful interactive creation takes no more than four answers: idea
  number, project name, and the confirmation (plus the `-t` flag only when a
  type is wanted).
- The user never retypes an entire command because of one bad input.
- The non-interactive form and every existing script, test, and documented
  example continue to work unchanged.
- No new third-party dependency is introduced.

## 4. Non-Goals

- This is not a full-screen TUI. There are no arrow keys, fuzzy search, or
  live filtering of the idea list.
- This does not add a prompt for project type. Type remains flag-only
  (`-t/--type`), exactly as today.
- This does not add prompts for the optional project fields that already
  exist in the data model: billing rate, rounding rule, deadline, client
  details, repository, code, or long-term flag.
- This does not add new flags or subcommands (`--idea`, `--name`,
  `--non-interactive`, a `--yes` shortcut, or an `init`-style wizard).
- This does not change `new idea`, `new task`, `publish`, `cancel`, or any
  other command.
- This does not change the project data model, the commit sequence of
  project creation, or the `.idea` seed written to the project branch.
- This does not allow creating a project without an idea, nor creating an
  idea from inside the wizard.
- This does not allow promoting a rejected idea.
- This does not change project-name rules, the idea numbering scheme, or
  billing defaults.
- There is no pagination, scrolling, or filtering of the idea list.

## 5. Users

| User | Need | Context |
|------|------|---------|
| The grind author (primary) | Promote ideas quickly while in a creative flow | Types on a Colemak-based Corne split keyboard; typos are common; runs grind inside a terminal in the workspace root |
| A returning user who forgot the numbering | See the ideas and pick one without running `list ideas` first | Has not used grind in days; the idea they want is somewhere in a list of a dozen |
| A scripted caller / CI job | Create projects deterministically with no terminal | Pipes commands or runs from a script; must keep working with the two-argument form |

## 6. Functional Requirements

### Invocation and compatibility

- **REQ-001**: The command `grind new project`, invoked with no positional
  arguments, shall start the interactive flow.
- **REQ-002**: The command `grind new project <name> <idea-number>`,
  invoked with exactly two positional arguments, shall create the project
  non-interactively, with no prompts and no questions of any kind.
- **REQ-003**: The command shall accept only 0 or 2 positional arguments.
  Any other count (1, 3, or more) shall fail with the user error
  `new project requires either no arguments (interactive) or <name> <idea-number>`
  and exit 1.
- **REQ-004**: The `-t/--type` flag shall apply to both forms. When given
  with the interactive form, the type shall be validated before any prompt is
  shown, and the interactive flow shall never prompt for a type.

### Pre-prompt validation

- **REQ-005**: Both forms shall require a workspace and shall fail with the
  existing "not in a workspace" user error when run outside one (exit 1).
- **REQ-006**: Both forms shall fail fast when `.projects.json` has
  uncommitted changes, using the existing message
  `You have uncommitted changes in .projects.json. Commit or discard them first.`
  (exit 1). In the interactive form this check shall run before the idea list
  is printed and before any question is asked.
- **REQ-007**: An invalid `-t/--type` value shall fail with the existing
  `Invalid type: <type>. Valid types: <list>` user error and exit 1, before
  any prompt is shown.

### The interactive flow

- **REQ-008**: The flow shall load and print the workspace's promotable
  ideas — non-rejected ideas only — in the same order and with the same
  numbering and rendering as `grind list ideas` (`<number>. <title>`).
- **REQ-009**: The idea list shall be preceded by a `Select an idea:` header
  and followed by a blank line before the first prompt.
- **REQ-010**: When there are no promotable ideas, the command shall print
  `No ideas yet. Create one with: grind new idea "Your idea"`, create
  nothing, ask nothing, and exit 0.
- **REQ-011**: The flow shall prompt `Idea number: ` and accept a base-10
  integer matching one of the printed idea numbers.
  - A non-numeric answer shall print the existing error
    `Idea must be a number, got "<answer>"` and re-prompt.
  - An out-of-range number shall print the existing error
    `Idea #<n> not found. Run 'grind list ideas' to see available ideas.`
    and re-prompt.
  - An empty answer shall re-prompt.
- **REQ-012**: After a valid idea is chosen, the flow shall prompt for the
  project name on one line, showing a suggested name in square brackets,
  e.g. `Project name [my-blog]: `.
- **REQ-013**: The suggested name shall be derived from the chosen idea's
  title as follows: lowercase the title; replace every run of characters
  outside `[a-z0-9]` with a single `-`; trim leading and trailing `-`.
  For example, `My Blog` becomes `my-blog`, `Go & Rust!` becomes `go-rust`,
  and `Café Notes` becomes `caf-notes`. If the result is empty, reserved, or
  otherwise not a valid project name, no suggestion shall be shown and the
  prompt shall read `Project name: `.
- **REQ-014**: An empty answer to the name prompt shall accept the suggested
  name when one is shown. When no suggestion is shown, an empty answer shall
  re-prompt.
- **REQ-015**: The name answer shall be validated against every rule the
  two-argument form enforces, including: non-empty, no whitespace, not
  reserved (`.main`, `.grind.repo.git`), a valid git branch name, not
  already present in `.projects.json`, and no existing directory at the
  worktree path. On failure, the flow shall print the same error message the
  two-argument form prints for that rule, then re-prompt with the suggestion
  still shown.
- **REQ-016**: Answers shall be trimmed of leading and trailing whitespace
  before validation. A name is never stored with surrounding whitespace.

### Confirmation

- **REQ-017**: Before creating anything, the flow shall print a summary
  block and a `Create project? [Y/n] ` prompt. The summary shall contain the
  project name, branch, worktree path (`<name>/`), type (`—` when empty),
  idea title, and billing rate and rounding rule, with values aligned in a
  fixed-width column.
- **REQ-018**: An empty answer, `y`, or `yes` (case-insensitive) shall
  proceed with creation. `n` or `no` (case-insensitive) shall print
  `Aborted.`, create nothing, and exit 0. Any other answer shall re-prompt.
- **REQ-019**: Nothing shall be written — no branch, no worktree, no state
  file, no idea deletion — before the confirmation is accepted.

### Abort and interruption

- **REQ-020**: End-of-input (Ctrl+D, a closed stdin, `/dev/null`) at any
  prompt shall print `Aborted.`, create nothing, and exit 0. End-of-input
  shall never silently accept a suggested default.
- **REQ-021**: Ctrl+C shall terminate the process by the normal signal path.
  Because nothing is written before confirmation, an interrupted wizard
  shall leave the workspace byte-for-byte unchanged.
- **REQ-022**: An I/O failure while reading an answer shall surface as a
  system error (exit 2), not as an abort.

### After creation

- **REQ-023**: A confirmed creation shall perform the same work as the
  two-argument form: create the branch at an empty tree, add the worktree,
  seed and commit `.idea` on the project branch, write and commit
  `.projects.json`, delete the idea file, commit the deletion, and leave
  `.main` clean.
- **REQ-024**: Both forms shall print the same success block: the
  `Created project:` / `Branch:` / `Worktree:` lines, then
  `Next: work my-blog` (with the created project's name), and nothing after
  it. The remaining-ideas list shall not be printed.
- **REQ-025**: Re-prompt and validation messages shall be written to the
  same stream as the prompts and success output (stdout), so a piped stdin
  and redirected stdout keep questions, answers, and messages in order.
  Fatal errors continue to be printed by `main` to stderr.

## 7. Non-Functional Requirements

- **NFR-001**: The interactive flow shall run on the Go standard library
  alone. This feature shall add no module to `go.mod`.
- **NFR-002**: All prompts shall work when stdin is a pipe and stdout is a
  file, so the complete flow is drivable and testable by feeding a scripted
  sequence of answers. A real terminal shall not be required.
- **NFR-003**: All answer-reading for one wizard run shall share a single
  buffered input reader. Answers typed ahead, or supplied all at once by a
  pipe, shall not be lost between prompts.
- **NFR-004**: The feature shall add no measurable startup delay or extra
  process spawn to `new project`; it introduces no new external commands.
- **NFR-005**: The two-argument form shall remain non-interactive, perform
  the same commits, and use the same exit codes. Its only output changes are
  the two intentional ones shared with the interactive form: the `Next:` line
  names `work <name>` instead of `cd <name>`, and the remaining-ideas list is
  gone.

## 8. Data Contracts & Interface Shapes

### 8.1 Invocation matrix

| Invocation | Behavior |
|---|---|
| `grind new project` | Interactive flow |
| `grind new project -t blog` | Interactive flow with type `blog` |
| `grind new project <name> <idea-number>` | Non-interactive, today's behavior |
| `grind new project <name> <idea-number> -t <type>` | Non-interactive with type |
| `grind new project <name>` | User error, exit 1 |
| `grind new project a b c` | User error, exit 1 |

### 8.2 Interactive transcript (canonical example)

Workspace has two ideas, `0. My Blog` and `1. Video about Go`; no type flag.

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

Notes on the transcript:

- The blank line after `Project name [my-blog]:` is printed before the
  summary block so the confirm screen reads as a unit.
- The summary label column is 10 characters wide, one wider than
  `grind show`'s 9, because `Worktree:` is 9 characters and must still have a
  space before its value.
- The rate line matches `grind show`: `<symbol><rate>/hr (<roundTo>)`.
- The success block ends at the `Next:` line. Nothing is printed after it —
  in particular, the ideas are not re-listed.

### 8.3 Error and exit contract

| Condition | Output | Exit |
|---|---|---|
| Not in a workspace | Existing workspace user error | 1 |
| `.projects.json` dirty | Existing dirty-state user error | 1 |
| Invalid `-t` | Existing invalid-type user error | 1 |
| 1 positional argument | `new project requires either no arguments (interactive) or <name> <idea-number>` | 1 |
| 3+ positional arguments | Same argument-count user error as above | 1 |
| No ideas | `No ideas yet. Create one with: grind new idea "Your idea"` | 0 |
| Bad idea number | Existing idea-number error, then re-prompt | 0 on eventual success |
| Bad project name | Existing name error, then re-prompt | 0 on eventual success |
| Confirmation answered `n` | `Aborted.` | 0 |
| End-of-input at any prompt | `Aborted.` | 0 |
| Read failure | System error | 2 |
| Creation succeeds | Success block ending in `Next: work <name>` | 0 |

There is no exit path in which a prompt is answered invalidly and the
command exits non-zero; invalid answers always re-prompt. Only pre-prompt
failures, read failures, and creation failures exit non-zero.

### 8.4 Answer grammar

| Prompt | Accepted | Rejected (re-prompt) | End-of-input |
|---|---|---|---|
| `Idea number:` | A printed 0-based idea number | Non-integer, out of range, empty | Abort, exit 0 |
| `Project name [<slug>]:` | Any valid, unused project name; empty for the suggestion | Invalid format, reserved, existing project, existing directory, empty with no suggestion | Abort, exit 0 |
| `Create project? [Y/n]` | empty, `y`, `yes`, `n`, `no` (case-insensitive) | Anything else | Abort, exit 0 |

## 9. Acceptance Criteria

- **AC-001**: Given a workspace with two ideas and a clean `.projects.json`,
  When `grind new project` is run and `0`, Enter, and Enter are supplied,
  Then a project named after the first idea's slug is created and the summary
  shows that name, `Branch: <name>`, `Worktree: <name>/`, and the idea title.
- **AC-002**: Given the same workspace, When `grind new project` is run and
  `1`, `my-video`, `y` are supplied, Then the project `my-video` is created
  from idea 1, idea 1's file is deleted, and the output ends with
  `Next: work my-video` with no ideas listed after it.
- **AC-003**: Given an interactive run, When `abc` is entered at
  `Idea number:`, Then the message `Idea must be a number, got "abc"` is
  printed and `Idea number: ` is shown again, and no project is created yet.
- **AC-004**: Given an interactive run, When `99` is entered at
  `Idea number:`, Then the out-of-range idea message is printed and the
  prompt is shown again.
- **AC-005**: Given an interactive run, When `my blog` is entered at the
  name prompt, Then the whitespace name error is printed and the name prompt
  is shown again with the same suggestion.
- **AC-006**: Given a project named `my-blog` already exists, When `my-blog`
  is entered at the name prompt, Then `Project 'my-blog' already exists.` is
  printed and the name prompt is shown again.
- **AC-007**: Given a directory named `my-blog` exists in the workspace root
  but no project entry exists, When `my-blog` is entered at the name prompt,
  Then the existing "directory already exists" error is printed and the name
  prompt is shown again.
- **AC-008**: Given an interactive run at the confirmation prompt, When `n`
  is entered, Then `Aborted.` is printed, exit is 0, and `git status` in
  `.main` is unchanged, with no branch or worktree created.
- **AC-009**: Given an interactive run at the confirmation prompt, When
  Enter is pressed, Then the project is created.
- **AC-010**: Given an interactive run, When stdin reaches end-of-input at
  the name prompt, Then `Aborted.` is printed, exit is 0, and nothing is
  created — the suggested name is not accepted.
- **AC-011**: Given a workspace with zero non-rejected ideas, When
  `grind new project` is run, Then the no-ideas message is printed, exit is
  0, and no prompt is shown.
- **AC-012**: Given a workspace containing only rejected ideas, When
  `grind new project` is run, Then the no-ideas message is printed and the
  rejected idea is not listed.
- **AC-013**: Given `.projects.json` has uncommitted changes, When
  `grind new project` is run, Then the existing dirty-state error is printed,
  exit is 1, and no idea list or prompt is shown.
- **AC-014**: Given `grind new project -t nonsense` with no positional
  arguments, Then the existing invalid-type error is printed, exit is 1, and
  no prompt is shown.
- **AC-015**: Given `grind new project -t blog`, When the wizard is
  completed, Then the created project's type is `blog` and the type is never
  prompted for.
- **AC-016**: Given an idea titled `Café Notes`, When it is chosen, Then the
  name prompt suggests `caf-notes`.
- **AC-017**: Given an idea titled `Go & Rust!`, When it is chosen, Then the
  name prompt suggests `go-rust`.
- **AC-018**: Given an idea whose title produces an empty or reserved slug,
  When it is chosen, Then the name prompt shows no suggestion and an empty
  answer re-prompts.
- **AC-019**: Given all answers supplied at once on stdin (for example
  `0\nmy-blog\ny\n`), When the command runs, Then every answer is consumed
  by its intended prompt and the project is created.
- **AC-020**: Given the two-argument form `grind new project my-blog 0 -t blog`,
  When it runs, Then it performs the same commits and returns the same exit
  code as the current implementation, asks no questions, and prints the
  shared success block ending in `Next: work my-blog` with no ideas listed.
- **AC-021**: Given `grind new project my-blog` (one argument) or
  `grind new project a b c` (three arguments), Then the argument-count user
  error is printed and exit is 1.
- **AC-022**: Given any completed interactive creation, Then
  `git status --porcelain` in `.main` is empty and
  `ls <root>/<name>` contains only the `.idea` seed.

## 10. Open Questions

None. Every decision raised during the design interview is resolved in
Section 6 (type is flag-only, both forms coexist, invalid answers re-prompt,
no ideas exits 0, end-of-input aborts at exit 0, a slug suggestion is shown,
existing names are rejected during the prompt, one positional argument is an
error, the `Next:` line names `work <name>`, and ideas are not re-listed).

## 11. Out of Scope / Future Considerations

- Future: a selectable, arrow-key/fuzzy idea picker (would require a TUI
  dependency and a PTY-based test strategy).
- Future: probing for billing rate, rounding rule, deadline, and client
  details during creation.
- Future: creating a new idea inline from the wizard.
- Future: paginating or filtering a long idea list.
- Future: a `--non-interactive`/`--yes` escape hatch if the wizard ever
  becomes the default for the two-argument form (it is not in this version).
- Future: the same prompt vocabulary extracted for reuse by `new idea`,
  `new task`, and `publish`/`cancel`.
