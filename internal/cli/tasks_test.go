package cli

import (
	"strings"
	"testing"
)

func TestNewTaskCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	out, err := execute(t, fake, "new", "task", "my-blog", "Write intro", "-d", "2026-09-20")
	if err != nil {
		t.Fatalf("new task: %v", err)
	}
	if !strings.Contains(out, "Task 100 added: Write intro") {
		t.Errorf("output = %q", out)
	}

	// The last commit must be the add-task commit with only .projects.json.
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Add task: Write intro" {
		t.Errorf("last commit message = %q", last.message)
	}
	if len(last.paths) != 1 || last.paths[0] != ".projects.json" {
		t.Errorf("last commit paths = %v", last.paths)
	}
}

func TestNewTaskCommandUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "new", "task", "nope", "Write intro")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewTaskCommandEmptyDescription(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	_, err := execute(t, fake, "new", "task", "my-blog", "   ")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Task description must not be empty." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewTaskCommandBadDate(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	_, err := execute(t, fake, "new", "task", "my-blog", "Write intro", "-d", "banana")
	if err == nil {
		t.Fatal("expected error")
	}
	want := `Unparseable date: "banana"`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestNewTaskCommandErrorOrdering(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	// The spec's flow checks the project first, then the description, then
	// the date — so a missing project wins over a bad date.
	_, err := execute(t, fake, "new", "task", "nope", "", "-d", "banana")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q, want project error first", err.Error())
	}

	createProject(t, fake, "my-blog")
	// With a valid project, an empty description wins over a bad date.
	_, err = execute(t, fake, "new", "task", "my-blog", "", "-d", "banana")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Task description must not be empty." {
		t.Errorf("error = %q, want description error first", err.Error())
	}
}

func TestListTasksCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	if _, err := execute(t, fake, "new", "task", "my-blog", "Write intro", "-d", "2026-09-20"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "list", "tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#", "Project", "Task", "Due", "100", "my-blog", "Write intro", "2026-09-20"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, missing %q", out, want)
		}
	}
}

func TestListTasksSingleProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")
	createProject(t, fake, "other")

	if _, err := execute(t, fake, "new", "task", "my-blog", "Write intro"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "new", "task", "other", "Fix bug"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "list", "tasks", "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Write intro") {
		t.Errorf("output = %q, want my-blog task", out)
	}
	if strings.Contains(out, "Fix bug") {
		t.Errorf("output = %q, must not contain other project's task", out)
	}
	// The single-project view omits the Project column.
	if strings.Contains(out, "Project") {
		t.Errorf("output = %q, single-project view must omit Project column", out)
	}
}

func TestListTasksAllFlagIncludesCompleted(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	if _, err := execute(t, fake, "new", "task", "my-blog", "Write intro"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "done", "task", "100"); err != nil {
		t.Fatal(err)
	}

	// Without -a the completed task is hidden.
	out, err := execute(t, fake, "list", "tasks")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Write intro") {
		t.Errorf("open-only output = %q, must not contain completed task", out)
	}

	// With -a it appears.
	out, err = execute(t, fake, "list", "tasks", "-a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Write intro") {
		t.Errorf("all output = %q, want completed task", out)
	}

	// The long --all flag behaves the same.
	out, err = execute(t, fake, "list", "tasks", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Write intro") {
		t.Errorf("--all output = %q, want completed task", out)
	}
}

func TestListTasksEmptyStates(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	// All projects, no tasks.
	out, err := execute(t, fake, "list", "tasks")
	if err != nil {
		t.Fatal(err)
	}
	if out != "All caught up! No open tasks.\n" {
		t.Errorf("empty all output = %q", out)
	}

	// All projects, -a.
	out, err = execute(t, fake, "list", "tasks", "-a")
	if err != nil {
		t.Fatal(err)
	}
	if out != "No tasks yet.\n" {
		t.Errorf("empty all -a output = %q", out)
	}

	// Single project, no tasks.
	createProject(t, fake, "my-blog")
	out, err = execute(t, fake, "list", "tasks", "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if out != "No open tasks. Add one with: grind new task my-blog \"description\"\n" {
		t.Errorf("empty single output = %q", out)
	}

	// Single project, -a.
	out, err = execute(t, fake, "list", "tasks", "my-blog", "-a")
	if err != nil {
		t.Fatal(err)
	}
	if out != "No tasks yet.\n" {
		t.Errorf("empty single -a output = %q", out)
	}
}

func TestListTasksUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "list", "tasks", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' not found." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestTasksAliasCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	if _, err := execute(t, fake, "new", "task", "my-blog", "Write intro"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Write intro") {
		t.Errorf("alias output = %q", out)
	}
}

func TestDoneTaskCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	if _, err := execute(t, fake, "new", "task", "my-blog", "Write intro"); err != nil {
		t.Fatal(err)
	}

	out, err := execute(t, fake, "done", "task", "100")
	if err != nil {
		t.Fatalf("done task: %v", err)
	}
	if !strings.Contains(out, "Task 100 completed.") {
		t.Errorf("output = %q", out)
	}

	last := fake.commits[len(fake.commits)-1]
	if last.message != "Complete task #100" {
		t.Errorf("last commit message = %q", last.message)
	}
	if len(last.paths) != 1 || last.paths[0] != ".projects.json" {
		t.Errorf("last commit paths = %v", last.paths)
	}
}

func TestDoneTaskAlreadyDone(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	if _, err := execute(t, fake, "new", "task", "my-blog", "Write intro"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "done", "task", "100"); err != nil {
		t.Fatal(err)
	}
	commitsBefore := len(fake.commits)

	out, err := execute(t, fake, "done", "task", "100")
	if err != nil {
		t.Fatalf("done task (second): %v", err)
	}
	if !strings.Contains(out, "Task 100 is already completed.") {
		t.Errorf("output = %q", out)
	}
	// No pointless re-complete commit.
	if len(fake.commits) != commitsBefore {
		t.Errorf("commits = %d, want %d (no re-complete commit)", len(fake.commits), commitsBefore)
	}
}

func TestDoneTaskNonNumeric(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "done", "task", "abc")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != `Task ID must be a number, got "abc"` {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDoneTaskNotFound(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "done", "task", "999")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Task #999 not found." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDueColor(t *testing.T) {
	tests := []struct {
		dueDate string
		today   string
		want    string
	}{
		{"2026-07-14", "2026-07-15", "red"}, // overdue
		{"2026-07-15", "2026-07-15", "red"}, // due today
		{"2026-07-16", "2026-07-15", "yellow"},
		{"2026-07-18", "2026-07-15", "yellow"}, // within 3 days
		{"2026-07-19", "2026-07-15", "green"},  // 4+ days
		{"", "2026-07-15", ""},
	}
	for _, tt := range tests {
		t.Run(tt.dueDate+"/"+tt.today, func(t *testing.T) {
			if got := dueColor(tt.dueDate, tt.today); got != tt.want {
				t.Errorf("dueColor(%q, %q) = %q, want %q", tt.dueDate, tt.today, got, tt.want)
			}
		})
	}
}
