package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
)

// The v1 fixtures below are copied from a workspace grind v1 0.9.4 wrote:
// .grind.json has no trailing newline and carries a currency, alpha has an
// ended session, an active session, two tasks and an invoice, and beta has a
// publication and one task. Both projects start their task IDs at 1, which is
// exactly the collision the migration has to fix.
const v1GrindJSON = `{
  "billing": {
    "roundTo": "quarter-hour",
    "defaultRate": 150
  },
  "my": {
    "name": "Lee Brandt",
    "company": "Lee Brandt LLC",
    "address": "123 Main St",
    "phone": "555-0100",
    "email": "lee@example.com",
    "taxId": "12-3456789"
  },
  "currency": "USD",
  "paymentTerms": "Net 30"
}`

const v1AlphaJSON = `{
  "name": "alpha",
  "type": "blog",
  "idea": "# Second idea about video",
  "time": [
    {
      "start": "2026-10-25T20:00:00.000Z",
      "end": "2026-10-25T21:00:00.000Z",
      "duration": 3600,
      "rounded": 3600
    },
    {
      "start": "2026-10-25T21:10:25.689Z",
      "end": null,
      "duration": 0,
      "rounded": 0
    }
  ],
  "billing": {
    "roundTo": "quarter-hour",
    "rate": 200
  },
  "client": {
    "company": "Acme Corp"
  },
  "tasks": [
    {
      "id": 1,
      "description": "Write the intro",
      "done": false,
      "createdAt": "2026-10-25T21:10:17.046Z"
    },
    {
      "id": 2,
      "description": "Publish it",
      "done": false,
      "createdAt": "2026-10-25T21:10:17.144Z",
      "dueDate": "2026-11-01"
    }
  ]
}`

const v1BetaJSON = `{
  "name": "beta",
  "type": "song",
  "idea": "# Write a song about rain",
  "time": [],
  "billing": {
    "roundTo": "quarter-hour",
    "rate": 150
  },
  "tasks": [
    {
      "id": 1,
      "description": "Write verse one",
      "done": false,
      "createdAt": "2026-10-25T21:10:18.000Z"
    }
  ],
  "publications": [
    {
      "url": "https://example.com/rain",
      "publishedAt": "2026-10-20T00:00:00.000Z"
    }
  ]
}`

// migrateNow is the instant the CLI under test runs at. Migration dates come
// from git, but `wwd` and journal filenames read the clock, so pinning it keeps
// the whole run a fixed fact.
var migrateNow = time.Date(2026, 10, 26, 10, 0, 0, 0, time.Local)

// buildV1Workspace writes a complete v1 workspace to a fresh temp dir and
// returns its root. It uses real git (plumbing via internal/git, so no shell)
// rather than the v1 binary, which keeps the test fast and deterministic.
func buildV1Workspace(t *testing.T) string {
	t.Helper()
	// Commit identity comes from the environment so the test never depends on
	// the developer's git config.
	t.Setenv("GIT_AUTHOR_NAME", "Grind Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "grind-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Grind Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "grind-test@example.com")

	root := t.TempDir()
	g := git.New()
	bare := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bare); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bare, "main"); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "grind")
	if err := g.AddWorktree(bare, main, "main"); err != nil {
		t.Fatal(err)
	}

	writeV1File(t, root, "grind/.grind.json", v1GrindJSON)
	writeV1File(t, root, "grind/journal/2026-10-25.md", "# Journal\n\nMigrated a workspace today.\n")
	writeV1File(t, root, "grind/ideas/20261025161013.md", "# Second idea about video\n")
	if err := g.Commit(main, "Initialize grind workspace",
		".grind.json", "journal/2026-10-25.md", "ideas/20261025161013.md"); err != nil {
		t.Fatal(err)
	}

	// Each project's config is committed separately and at a distinct date, so
	// createdAt has a real adding commit to find.
	t.Setenv("GIT_AUTHOR_DATE", "2026-10-25T16:10:13-05:00")
	writeV1File(t, root, "grind/projects/alpha/.project.json", v1AlphaJSON)
	writeV1File(t, root, "grind/projects/alpha/the-idea.md", "# Second idea about video\n")
	if err := g.Commit(main, "Create project: alpha",
		"projects/alpha/.project.json", "projects/alpha/the-idea.md"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_AUTHOR_DATE", "2026-10-25T16:10:16-05:00")
	writeV1File(t, root, "grind/projects/beta/.project.json", v1BetaJSON)
	writeV1File(t, root, "grind/projects/beta/the-idea.md", "# Write a song about rain\n")
	if err := g.Commit(main, "Create project: beta",
		"projects/beta/.project.json", "projects/beta/the-idea.md"); err != nil {
		t.Fatal(err)
	}

	// v1 wrote invoices under the project's state directory, with the PDF the
	// browser saved beside the markdown.
	t.Setenv("GIT_AUTHOR_DATE", "2026-10-25T16:12:46-05:00")
	writeV1File(t, root, "grind/projects/alpha/invoices/2026-10-25T21-12-46/invoice.md", "# INVOICE\n")
	writeV1File(t, root, "grind/projects/alpha/invoices/2026-10-25T21-12-46/invoice.pdf", "%PDF-1.4\n")
	if err := g.Commit(main, "Invoice 2026-10-25T21-12-46 for alpha",
		"projects/alpha/invoices"); err != nil {
		t.Fatal(err)
	}

	// The project worktrees v1 created: siblings of the main worktree, on their
	// own branches. Their .git files point at .grind.repo.git/worktrees/<name>,
	// not at the main worktree, which is why the move leaves them working.
	for _, name := range []string{"alpha", "beta"} {
		if err := g.CreateBranch(bare, name); err != nil {
			t.Fatal(err)
		}
		if err := g.AddWorktree(bare, filepath.Join(root, name), name); err != nil {
			t.Fatal(err)
		}
		writeV1File(t, root, filepath.Join(name, "work.md"), "# Work on "+name+"\n")
		if err := g.Commit(filepath.Join(root, name), "Start work on "+name, "work.md"); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// writeV1File writes a file inside a v1 workspace, creating parent directories.
// path is relative to root.
func writeV1File(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runGrind executes the command tree against the real git implementation, from
// inside the workspace at dir. The fake-git helpers in cli_test.go cannot do
// this: migration is mostly filesystem and git surgery, and faking either
// would test the fake.
func runGrind(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)

	root := NewRootCmdWithClock(git.New(), clock.NewFake(migrateNow))
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// gitAt runs git in dir and returns its trimmed output, failing the test if git
// fails — every call below is an assertion about the resulting repository.
func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestMigrateCommandConvertsAV1Workspace(t *testing.T) {
	root := buildV1Workspace(t)

	out, err := runGrind(t, root, "migrate")
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}

	for _, want := range []string{
		"Migrating workspace to the v2 layout...",
		"Main worktree   grind/ → .main/",
		"Projects        2 migrated (alpha, beta)",
		"Tasks           3 renumbered (100-102)",
		"Sessions        2 migrated",
		"Invoices        1 moved",
		"beta: dropped 1 publication: https://example.com/rain",
		"Committed: Migrate workspace from grind v1",
		"Next: grind wwd",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// The layout moved in place: .main/ is here, grind/ is gone, and the
	// worktree did NOT land inside the bare repo.
	main := filepath.Join(root, ".main")
	if _, err := os.Stat(main); err != nil {
		t.Fatalf(".main was not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "grind")); !os.IsNotExist(err) {
		t.Errorf("grind/ still exists after migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".grind.repo.git", ".main")); !os.IsNotExist(err) {
		t.Error("worktree landed inside the bare repo at .grind.repo.git/.main")
	}

	// The main worktree is clean: the migration committed everything it wrote.
	if status := gitAt(t, main, "status", "--porcelain"); status != "" {
		t.Errorf("main worktree not clean after migration:\n%s", status)
	}
	if subject := gitAt(t, main, "log", "-1", "--format=%s"); subject != "Migrate workspace from grind v1" {
		t.Errorf("last commit subject = %q", subject)
	}

	// Ideas and journal moved with the worktree, contents unchanged.
	if got, err := os.ReadFile(filepath.Join(main, "journal", "2026-10-25.md")); err != nil || !strings.Contains(string(got), "Migrated a workspace") {
		t.Errorf("journal did not move intact: %v %q", err, got)
	}

	// .projects.json holds both projects, tasks renumbered from 100.
	projects, err := config.ReadProjects(filepath.Join(main, ".projects.json"))
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	if len(projects.Projects) != 2 {
		t.Fatalf("projects = %v, want alpha and beta", projects.Projects)
	}
	if projects.NextTaskID != 103 {
		t.Errorf("NextTaskID = %d, want 103", projects.NextTaskID)
	}
	alpha := projects.Projects["alpha"]
	if alpha.Billing.Rate != 200 {
		t.Errorf("alpha rate = %v, want 200", alpha.Billing.Rate)
	}
	if alpha.Client == nil || alpha.Client.Company != "Acme Corp" {
		t.Errorf("alpha client = %+v", alpha.Client)
	}
	if alpha.Idea != "Second idea about video" {
		t.Errorf("alpha idea = %q, want the title without the markdown heading", alpha.Idea)
	}
	if len(alpha.Sessions) != 2 {
		t.Fatalf("alpha sessions = %+v, want 2", alpha.Sessions)
	}
	if alpha.Sessions[1].End != nil {
		t.Errorf("the active session gained an end time: %+v", alpha.Sessions[1])
	}

	var taskIDs []int
	for _, name := range []string{"alpha", "beta"} {
		for _, task := range projects.Projects[name].Tasks {
			taskIDs = append(taskIDs, task.ID)
		}
	}
	if len(taskIDs) != 3 || taskIDs[0] != 100 || taskIDs[2] != 102 {
		t.Errorf("task IDs = %v, want 100-102", taskIDs)
	}

	// createdAt comes from the commit that added the project's config, not
	// from the migration instant.
	added := gitAt(t, main, "log", "--diff-filter=A", "--format=%aI", "refs/heads/main", "--", "projects/alpha/.project.json")
	wantCreated, err := time.Parse(time.RFC3339, added)
	if err != nil {
		t.Fatalf("parse %q: %v", added, err)
	}
	if !alpha.CreatedAt.Equal(wantCreated) {
		t.Errorf("alpha createdAt = %v, want the adding commit's date %v", alpha.CreatedAt, wantCreated)
	}

	// currency is gone from the rewritten config, and the file is in v2's
	// canonical form (trailing newline, 2-space indent).
	cfgBytes, err := os.ReadFile(filepath.Join(main, ".grind.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgBytes), "currency") {
		t.Errorf(".grind.json still carries a currency key:\n%s", cfgBytes)
	}
	if !bytes.HasSuffix(cfgBytes, []byte("\n")) {
		t.Error(".grind.json has no trailing newline")
	}
	cfg, err := config.Read(filepath.Join(main, ".grind.json"))
	if err != nil {
		t.Fatalf("Read config: %v", err)
	}
	if cfg.PaymentTerms != "Net 30" || cfg.My == nil || cfg.My.Company != "Lee Brandt LLC" {
		t.Errorf("workspace config lost fields: %+v", cfg)
	}

	// The invoice moved to the v2 layout, id and PDF intact.
	invoiceDir := filepath.Join(main, "invoices", "alpha", "2026-10-25T21-12-46")
	for _, file := range []string{"invoice.md", "invoice.pdf"} {
		if _, err := os.Stat(filepath.Join(invoiceDir, file)); err != nil {
			t.Errorf("%s did not move: %v", file, err)
		}
	}

	// The v1 project state directories are gone.
	for _, name := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(main, "projects", name)); !os.IsNotExist(err) {
			t.Errorf("projects/%s still exists: %v", name, err)
		}
	}
}

func TestMigrateCommandLeavesProjectWorktreesWorking(t *testing.T) {
	root := buildV1Workspace(t)
	if _, err := runGrind(t, root, "migrate"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, name := range []string{"alpha", "beta"} {
		worktree := filepath.Join(root, name)
		// git -C <project> status must still work: the worktree's .git file
		// points at the bare repo's metadata, not at the main worktree, so the
		// rename cannot break it.
		gitAt(t, worktree, "status", "--porcelain")
		if branch := gitAt(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"); branch != name {
			t.Errorf("%s is on branch %q", name, branch)
		}
		if _, err := os.Stat(filepath.Join(worktree, "work.md")); err != nil {
			t.Errorf("%s lost its work product: %v", name, err)
		}
	}
}

func TestMigrateCommandV2CommandsWorkAfterwards(t *testing.T) {
	root := buildV1Workspace(t)
	if _, err := runGrind(t, root, "migrate"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	out, err := runGrind(t, root, "projects")
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("grind projects output = %q", out)
	}

	if _, err := runGrind(t, root, "wwd"); err != nil {
		t.Fatalf("wwd: %v", err)
	}

	out, err = runGrind(t, root, "show", "alpha")
	if err != nil {
		t.Fatalf("show alpha: %v", err)
	}
	for _, want := range []string{"Name:    alpha", "Rate:    $200/hr", "Second idea about video"} {
		if !strings.Contains(out, want) {
			t.Errorf("show alpha output = %q, missing %q", out, want)
		}
	}

	// The migrated session is ended and uninvoiced, so it is billable.
	out, err = runGrind(t, root, "invoice", "alpha", "-n")
	if err != nil {
		t.Fatalf("invoice alpha -n: %v", err)
	}
	if !strings.Contains(out, "Would invoice 1 session(s) totalling 1.00h") {
		t.Errorf("invoice output = %q", out)
	}
}

func TestMigrateCommandDryRunWritesNothing(t *testing.T) {
	root := buildV1Workspace(t)

	out, err := runGrind(t, root, "migrate", "--dry-run")
	if err != nil {
		t.Fatalf("migrate --dry-run: %v", err)
	}
	if !strings.Contains(out, "Migrating workspace to the v2 layout (dry run — nothing written)") {
		t.Errorf("dry run output = %q", out)
	}
	for _, want := range []string{
		"Main worktree   grind/ → .main/",
		"Projects        2 migrated (alpha, beta)",
		"Tasks           3 renumbered (100-102)",
		"Committed:",
	} {
		got := strings.Contains(out, want)
		if want == "Committed:" {
			if got {
				t.Errorf("dry run claims to have committed:\n%s", out)
			}
			continue
		}
		if !got {
			t.Errorf("dry run output missing %q:\n%s", want, out)
		}
	}

	// Nothing was written — not even .main.
	if _, err := os.Stat(filepath.Join(root, ".main")); !os.IsNotExist(err) {
		t.Errorf("--dry-run created .main: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "grind", ".grind.json")); err != nil {
		t.Errorf("--dry-run disturbed the v1 worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "grind", ".projects.json")); !os.IsNotExist(err) {
		t.Error("--dry-run wrote .projects.json")
	}
}

func TestMigrateCommandTwiceIsAUserError(t *testing.T) {
	root := buildV1Workspace(t)
	if _, err := runGrind(t, root, "migrate"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	head := gitAt(t, filepath.Join(root, ".main"), "rev-parse", "HEAD")

	out, err := runGrind(t, root, "migrate")
	if err == nil {
		t.Fatalf("second migrate = nil error, want a user error\n%s", out)
	}
	if err.Error() != "This workspace is already a v2 workspace." {
		t.Errorf("error = %q", err)
	}
	if code := grinderr.ExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}

	// And nothing changed.
	if after := gitAt(t, filepath.Join(root, ".main"), "rev-parse", "HEAD"); after != head {
		t.Errorf("second run moved HEAD: %s → %s", head, after)
	}
	if status := gitAt(t, filepath.Join(root, ".main"), "status", "--porcelain"); status != "" {
		t.Errorf("second run left the worktree dirty:\n%s", status)
	}
}

func TestMigrateCommandResumesAfterTheMove(t *testing.T) {
	root := buildV1Workspace(t)

	// Simulate a migration interrupted right after the worktree move: the
	// directory was renamed, nothing else was done.
	if err := git.New().MoveWorktree(
		filepath.Join(root, ".grind.repo.git"),
		filepath.Join(root, "grind"),
		filepath.Join(root, ".main"),
	); err != nil {
		t.Fatal(err)
	}

	out, err := runGrind(t, root, "migrate")
	if err != nil {
		t.Fatalf("migrate after an interrupted run: %v\n%s", err, out)
	}
	if strings.Contains(out, "grind/ → .main/") {
		t.Errorf("a resumed run claims to move the worktree again:\n%s", out)
	}

	projects, err := config.ReadProjects(filepath.Join(root, ".main", ".projects.json"))
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	if len(projects.Projects) != 2 {
		t.Errorf("projects = %v, want both", projects.Projects)
	}
	if status := gitAt(t, filepath.Join(root, ".main"), "status", "--porcelain"); status != "" {
		t.Errorf("worktree not clean after resumed migration:\n%s", status)
	}
}

func TestMigrateCommandKeepsUncommittedFilesDirty(t *testing.T) {
	root := buildV1Workspace(t)

	// v1's config and journal commands wrote without committing, so a
	// workspace in everyday use is routinely dirty.
	t.Setenv("GIT_AUTHOR_DATE", "2026-10-25T17:00:00-05:00")
	writeV1File(t, root, "grind/journal/2026-10-26.md", "# Journal\n\nUnsaved thoughts.\n")
	writeV1File(t, root, "grind/.grind.json", strings.Replace(v1GrindJSON, `"paymentTerms"`, `"projectTypes": ["blog"],
  "paymentTerms"`, 1))

	out, err := runGrind(t, root, "migrate")
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Uncommitted files were already present in grind/ — run 'grind save' to commit them.") {
		t.Errorf("output does not tell the user to save:\n%s", out)
	}

	main := filepath.Join(root, ".main")
	// The untracked journal entry is still untracked: the migration commits
	// only its own paths, never everything that happened to be dirty.
	if status := gitAt(t, main, "status", "--porcelain"); status != "?? journal/2026-10-26.md" {
		t.Errorf("status after migration = %q, want only the untracked journal file", status)
	}

	// The migration commit itself touched only migration paths.
	changed := gitAt(t, main, "show", "--name-only", "--format=", "HEAD")
	for _, path := range strings.Split(changed, "\n") {
		if strings.HasPrefix(path, "journal/2026-10-26") {
			t.Errorf("migration commit swept up %q", path)
		}
	}

	// And the user's .grind.json edit survived, because it is a migration path.
	cfg, err := config.Read(filepath.Join(main, ".grind.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ProjectTypes) != 1 || cfg.ProjectTypes[0] != "blog" {
		t.Errorf("projectTypes = %v, want the uncommitted edit", cfg.ProjectTypes)
	}
}

func TestMigrateCommandEmptyWorkspace(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "Grind Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "grind-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Grind Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "grind-test@example.com")

	root := t.TempDir()
	g := git.New()
	bare := filepath.Join(root, ".grind.repo.git")
	if err := g.InitBare(bare); err != nil {
		t.Fatal(err)
	}
	if err := g.InitialCommit(bare, "main"); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(root, "grind")
	if err := g.AddWorktree(bare, main, "main"); err != nil {
		t.Fatal(err)
	}
	writeV1File(t, root, "grind/.grind.json", v1GrindJSON)
	if err := g.Commit(main, "Initialize grind workspace", ".grind.json"); err != nil {
		t.Fatal(err)
	}

	out, err := runGrind(t, root, "migrate")
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}

	projects, err := config.ReadProjects(filepath.Join(root, ".main", ".projects.json"))
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	if len(projects.Projects) != 0 {
		t.Errorf("projects = %v, want none", projects.Projects)
	}
	if projects.NextTaskID != 100 {
		t.Errorf("NextTaskID = %d, want 100", projects.NextTaskID)
	}
	if status := gitAt(t, filepath.Join(root, ".main"), "status", "--porcelain"); status != "" {
		t.Errorf("worktree not clean after migrating an empty workspace:\n%s", status)
	}
}

func TestMigrateCommandReadsALegacyConfigLocation(t *testing.T) {
	// Older v1 workspaces kept .project.json in the project worktree. Those
	// still exist, and v1's own `migrate` used to consolidate them.
	root := buildV1Workspace(t)
	if err := os.Remove(filepath.Join(root, "grind", "projects", "alpha", ".project.json")); err != nil {
		t.Fatal(err)
	}
	gitAt(t, filepath.Join(root, "grind"), "rm", "--quiet", "projects/alpha/.project.json")

	legacyDir := filepath.Join(root, "alpha", "projects", "alpha")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, ".project.json"), []byte(v1AlphaJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runGrind(t, root, "migrate")
	if err != nil {
		t.Fatalf("migrate: %v\n%s", err, out)
	}

	projects, err := config.ReadProjects(filepath.Join(root, ".main", ".projects.json"))
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	alpha, ok := projects.Projects["alpha"]
	if !ok {
		t.Fatalf("projects = %v, want alpha from the worktree config", projects.Projects)
	}
	if alpha.Billing.Rate != 200 || len(alpha.Sessions) != 2 {
		t.Errorf("alpha = %+v", alpha)
	}
}

func TestWwdExplainsAV1Workspace(t *testing.T) {
	root := buildV1Workspace(t)

	out, err := runGrind(t, root, "wwd")
	if err == nil {
		t.Fatalf("wwd in a v1 workspace = nil error, want the migrate hint\n%s", out)
	}
	want := "This workspace was created by grind v1. Run 'grind migrate' to convert it."
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), ".projects.json") {
		t.Error("the guard did not replace the bare os.ErrNotExist message")
	}
}
