package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/color"
	"github.com/leebrandt/grind/internal/status"
	"github.com/leebrandt/grind/internal/tasks"
)

// statusRow is one dashboard row with a long name and every cell filled, so
// a width test has something worth shrinking.
func statusRow() []status.Row {
	return []status.Row{{
		Name:        "a-very-long-project-name",
		WorkedHours: "12.5h",
		TaskCount:   7,
		TaskUrgency: "none",
		LastSession: "3h ago",
		LastCommit:  "2d ago",
	}}
}

// renderStatusAt renders the table at a fixed width and returns its lines.
// The palette is the zero value, which is the disabled palette — tests that
// are about layout should not have to see escape codes.
func renderStatusAt(t *testing.T, width int) []string {
	t.Helper()
	var buf bytes.Buffer
	if err := renderStatusTable(&buf, statusRow(), color.Palette{}, width); err != nil {
		t.Fatalf("renderStatusTable: %v", err)
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

func TestStatusTableWideKeepsEveryColumn(t *testing.T) {
	lines := renderStatusAt(t, 100)

	header := lines[0]
	for _, want := range []string{"Project", "Worked", "Tasks", "Last Session", "Last Commit"} {
		if !strings.Contains(header, want) {
			t.Errorf("header = %q, missing %q", header, want)
		}
	}
	for _, line := range lines {
		if got := rowRunes(line); got > 100 {
			t.Errorf("line is %d runes, want at most 100: %q", got, line)
		}
	}
}

// TestStatusTableTruncatesLongNameFirst: at 60 columns there is still room
// for all five columns if the project name yields, which is the better trade.
func TestStatusTableTruncatesLongNameFirst(t *testing.T) {
	lines := renderStatusAt(t, 60)

	if !strings.Contains(lines[0], "Last Commit") {
		t.Errorf("header = %q, want every column kept", lines[0])
	}
	if !strings.Contains(lines[1], "…") {
		t.Errorf("row = %q, want the long name truncated", lines[1])
	}
	for _, line := range lines {
		if got := rowRunes(line); got > 60 {
			t.Errorf("line is %d runes, want at most 60: %q", got, line)
		}
	}
}

// TestStatusTableDropsTimeColumns: at 40 columns the name alone cannot absorb
// the overflow, so the oldest-looking columns go, rightmost first.
func TestStatusTableDropsTimeColumns(t *testing.T) {
	lines := renderStatusAt(t, 40)

	for _, gone := range []string{"Last Commit", "Last Session"} {
		if strings.Contains(lines[0], gone) {
			t.Errorf("header = %q, want %q dropped", lines[0], gone)
		}
	}
	for _, kept := range []string{"Project", "Worked", "Tasks"} {
		if !strings.Contains(lines[0], kept) {
			t.Errorf("header = %q, missing %q", lines[0], kept)
		}
	}
	for _, line := range lines {
		if got := rowRunes(line); got > 40 {
			t.Errorf("line is %d runes, want at most 40: %q", got, line)
		}
	}
}

func TestStatusTableUnconstrained(t *testing.T) {
	// width 0 is what a pipe or a captured buffer gets: natural widths, no
	// ellipsis, every column.
	lines := renderStatusAt(t, 0)

	if !strings.Contains(lines[1], "a-very-long-project-name") {
		t.Errorf("row = %q, want the full name", lines[1])
	}
	if !strings.Contains(lines[0], "Last Commit") {
		t.Errorf("header = %q, want every column", lines[0])
	}
}

func TestDividerFitsWidth(t *testing.T) {
	label := "The GrindCLI"

	// The natural divider is v1's exact string; a width constraint only
	// shortens it.
	full := divider(0)
	if got, want := rowRunes(full), 1+27+len(" The GrindCLI ")+30; got != want {
		t.Errorf("divider(0) is %d runes, want the full %d", got, want)
	}
	if !strings.Contains(full, label) {
		t.Errorf("divider(0) = %q, missing the label", full)
	}

	for _, width := range []int{74, 60, 40} {
		got := divider(width)
		if rowRunes(got) > width {
			t.Errorf("divider(%d) is %d runes, want it to fit", width, rowRunes(got))
		}
		if !strings.Contains(got, label) {
			t.Errorf("divider(%d) = %q, missing the label", width, got)
		}
	}
}

// TestTaskListTruncatesDescription: the task list shares the fitting rules
// with the dashboard, so a long description truncates instead of wrapping.
func TestTaskListTruncatesDescription(t *testing.T) {
	rows := []tasks.TaskRow{{
		ID:          100,
		Project:     "my-blog",
		Description: "a task description that is far too long for a narrow terminal",
		DueDate:     "2026-09-30",
	}}

	var buf bytes.Buffer
	err := renderTaskList(&buf, rows, "", false, "2026-09-30", color.Palette{}, 60)
	if err != nil {
		t.Fatalf("renderTaskList: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if !strings.Contains(lines[1], "…") {
		t.Errorf("row = %q, want the description truncated", lines[1])
	}
	for _, line := range lines {
		if got := rowRunes(line); got > 60 {
			t.Errorf("line is %d runes, want at most 60: %q", got, line)
		}
	}
}

// TestWwdCapturedOutputHasNoConstraint drives the command end to end. A
// buffer is not a terminal, so this pins the other half of the width rule:
// captured output keeps its natural width and loses nothing.
func TestWwdCapturedOutputHasNoConstraint(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	out, err := executeAt(t, fake, cliNow(), "wwd")
	if err != nil {
		t.Fatalf("wwd: %v", err)
	}
	// No terminal, no constraint: the full table, divider and task list.
	for _, want := range []string{"Last Commit", "The GrindCLI", "All caught up! No open tasks."} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}
