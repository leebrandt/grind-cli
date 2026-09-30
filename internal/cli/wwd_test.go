package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestWwdCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	// Everything below runs at one fixed instant, and every "ago" in the
	// expected output is measured from it. Under the real clock these
	// assertions only held because the offsets were small; pinned, they
	// hold because they are arithmetic.
	clk := cliNow()

	// Backfill 8h: creates one ended session [now-8h, now] with 8h
	// rounded, so the row has deterministic values — Worked "8.0h",
	// Last Session "8h ago".
	if _, err := executeAt(t, fake, clk, "save", "my-blog", "-t", "8h"); err != nil {
		t.Fatalf("save my-blog -t 8h: %v", err)
	}
	if _, err := executeAt(t, fake, clk, "new", "task", "my-blog", "Write intro", "-d", "2020-01-01"); err != nil {
		t.Fatalf("new task: %v", err)
	}
	// The fake git reports the branch's last commit as 1 day before the
	// instant everything else ran at.
	fake.lastCommitDates = map[string]time.Time{
		"my-blog": clk.Now().Add(-24 * time.Hour),
	}

	commitsBefore := len(fake.commits)
	out, err := executeAt(t, fake, clk, "wwd")
	if err != nil {
		t.Fatalf("wwd: %v", err)
	}

	// Part 1: the status table — header, the project row, and its
	// computed cells.
	for _, want := range []string{
		"Project", "Worked", "Tasks", "Last Session", "Last Commit",
		"my-blog", "8.0h", "1", "8h ago", "1d ago",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("wwd output = %q, missing %q", out, want)
		}
	}

	// Part 2: the divider, boxed in blank lines.
	wantDivider := "\n " + strings.Repeat("─", 27) + " The GrindCLI " + strings.Repeat("─", 30) + "\n"
	if !strings.Contains(out, wantDivider) {
		t.Errorf("wwd output = %q, missing divider %q", out, wantDivider)
	}

	// Part 3: the open-task list, same rendering as `list tasks`.
	for _, want := range []string{"#", "Project", "Task", "Due", "100", "Write intro", "2020-01-01"} {
		if !strings.Contains(out, want) {
			t.Errorf("wwd output = %q, missing %q", out, want)
		}
	}

	// wwd is read-only: no commits may have been recorded.
	if len(fake.commits) != commitsBefore {
		t.Errorf("commits = %d, want %d (wwd must not commit)", len(fake.commits), commitsBefore)
	}
}

func TestWwdNoProjects(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "wwd")
	if err != nil {
		t.Fatalf("wwd: %v", err)
	}

	// v1 behavior: the empty state prints, then the divider and the
	// task list's empty state still follow.
	want := "No active projects. Create one with: grind new project \"name\" <idea-number>\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("output = %q, want prefix %q", out, want)
	}
	if !strings.Contains(out, "The GrindCLI") {
		t.Errorf("output = %q, missing divider", out)
	}
	if !strings.Contains(out, "All caught up! No open tasks.") {
		t.Errorf("output = %q, missing task empty state", out)
	}
}

func TestWwdHiddenFromHelp(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if strings.Contains(out, "wwd") {
		t.Errorf("--help output leaks the hidden command: %q", out)
	}
}

func TestWwdNotInWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	_, err = execute(t, &fakeGit{}, "wwd")
	if err == nil {
		t.Fatal("expected error outside a workspace")
	}
	if err.Error() != "Not in a grind workspace." {
		t.Errorf("error = %q", err.Error())
	}
}
