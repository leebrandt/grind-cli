package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// fakeGit records commits so task tests can assert what would have been
// sent to real git.
type fakeGit struct {
	commits []fakeCommit
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) InitBare(path string) error                 { return nil }
func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }
func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error {
	return nil
}
func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}
func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) { return true, nil }
func (f *fakeGit) CreateBranch(repoPath, branch string) error          { return nil }
func (f *fakeGit) HasChanges(worktreePath string) (bool, error)        { return false, nil }
func (f *fakeGit) CommitAll(worktreePath, message string) error         { return nil }
func (f *fakeGit) RemoteURL(repoPath string) (string, error)           { return "", nil }
func (f *fakeGit) PushAll(repoPath string) error                       { return nil }

// LastCommitDate returns the zero time: task operations never need commit
// dates, so the stub keeps the fake a complete git.Git.
func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeGit) DefaultBranch(repoPath string) (string, error) { return "main", nil }

func (f *fakeGit) SetRemoteURL(repoPath, url string) error { return nil }

func (f *fakeGit) PushBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) FetchAll(repoPath string) error { return nil }

func (f *fakeGit) IsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	return false, nil
}

func (f *fakeGit) FastForwardWorktree(worktreePath, branch string) error { return nil }

func (f *fakeGit) FastForwardRef(repoPath, branch string) error { return nil }

func (f *fakeGit) ListRemoteBranches(repoPath string) ([]string, error) { return nil, nil }

func (f *fakeGit) MergeBranch(worktreePath, branch string) error { return nil }

func (f *fakeGit) RemoveWorktree(repoPath, worktreePath string) error { return nil }

func (f *fakeGit) DeleteBranch(repoPath, branch string) error { return nil }

// Ensure the git package is linked in tests that reference the interface.
var _ git.Git = (*fakeGit)(nil)

// newTestWorkspace builds a workspace with a .projects.json file, without
// touching git.
func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, ".main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{
		Root:         dir,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
		MainWorktree: main,
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), config.DefaultProjects()); err != nil {
		t.Fatal(err)
	}
	return ws
}

// addProject writes a project entry into .projects.json with the given
// tasks, so task tests start from a known state.
func addProject(t *testing.T, ws *workspace.Workspace, name string, tasks ...config.Task) {
	t.Helper()
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := config.ProjectEntry{
		Name:      name,
		Type:      "blog",
		Idea:      "# Test\n",
		Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		Tasks:     tasks,
	}
	projects.Projects[name] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
}

// task builds a Task with the given fields and a fixed CreatedAt.
func task(id int, description, dueDate string, done bool) config.Task {
	created := time.Date(2026, 9, 13, 14, 30, 0, 0, time.UTC)
	t := config.Task{
		ID:          id,
		Description: description,
		CreatedAt:   created,
		DueDate:     dueDate,
		Done:        done,
	}
	if done {
		completed := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
		t.CompletedAt = &completed
	}
	return t
}

func TestAddTaskAssignsIDs(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	fake := &fakeGit{}
	svc := NewService(fake)

	first, err := svc.AddTask(ws, "my-blog", "Write intro", "2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 100 {
		t.Errorf("first ID = %d, want 100", first.ID)
	}
	if first.Description != "Write intro" {
		t.Errorf("Description = %q", first.Description)
	}
	if first.DueDate != "2026-09-20" {
		t.Errorf("DueDate = %q", first.DueDate)
	}
	if first.Done {
		t.Error("new task must not be done")
	}
	if first.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}

	second, err := svc.AddTask(ws, "my-blog", "Write outro", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != 101 {
		t.Errorf("second ID = %d, want 101", second.ID)
	}

	// The counter must have advanced in .projects.json.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if projects.NextTaskID != 102 {
		t.Errorf("NextTaskID = %d, want 102", projects.NextTaskID)
	}
	tasks := projects.Projects["my-blog"].Tasks
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
	if tasks[0].ID != 100 || tasks[1].ID != 101 {
		t.Errorf("task IDs = [%d %d], want [100 101]", tasks[0].ID, tasks[1].ID)
	}

	// Two commits, each staging only .projects.json on the main worktree.
	if len(fake.commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(fake.commits))
	}
	for i, c := range fake.commits {
		if c.worktree != ws.MainWorktree {
			t.Errorf("commit %d worktree = %q", i, c.worktree)
		}
		if len(c.paths) != 1 || c.paths[0] != ".projects.json" {
			t.Errorf("commit %d paths = %v", i, c.paths)
		}
	}
	if fake.commits[0].message != "Add task: Write intro" {
		t.Errorf("first commit message = %q", fake.commits[0].message)
	}
	if fake.commits[1].message != "Add task: Write outro" {
		t.Errorf("second commit message = %q", fake.commits[1].message)
	}
}

func TestAddTaskMissingCounterStartsAt100(t *testing.T) {
	ws := newTestWorkspace(t)
	// Simulate a workspace created before this slice: no nextTaskId field.
	projects := config.ProjectsConfig{
		Version:  1,
		Projects: map[string]config.ProjectEntry{},
	}
	projects.Projects["my-blog"] = config.ProjectEntry{
		Name:      "my-blog",
		Type:      "blog",
		Idea:      "# Test\n",
		Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{}
	svc := NewService(fake)

	task, err := svc.AddTask(ws, "my-blog", "First task", "")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != 100 {
		t.Errorf("ID = %d, want 100", task.ID)
	}
}

func TestAddTaskTrimsDescription(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	svc := NewService(&fakeGit{})

	task, err := svc.AddTask(ws, "my-blog", "  Write intro  ", "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Description != "Write intro" {
		t.Errorf("Description = %q, want trimmed %q", task.Description, "Write intro")
	}
}

func TestAddTaskUnknownProject(t *testing.T) {
	ws := newTestWorkspace(t)
	svc := NewService(&fakeGit{})

	_, err := svc.AddTask(ws, "nope", "Write intro", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error type = %T, want *grinderr.User", err)
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestAddTaskEmptyDescription(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog")
	svc := NewService(&fakeGit{})

	_, err := svc.AddTask(ws, "my-blog", "   ", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error type = %T, want *grinderr.User", err)
	}
	if err.Error() != "Task description must not be empty." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestListAllProjects(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog",
		task(100, "Write intro", "2026-09-20", false),
		task(101, "Write outro", "", false),
	)
	addProject(t, ws, "other",
		task(102, "Fix bug", "2026-09-18", false),
	)
	svc := NewService(&fakeGit{})

	rows, err := svc.List(ws, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Sorted by due date ascending; no-due last.
	want := []TaskRow{
		{ID: 102, Project: "other", Description: "Fix bug", DueDate: "2026-09-18"},
		{ID: 100, Project: "my-blog", Description: "Write intro", DueDate: "2026-09-20"},
		{ID: 101, Project: "my-blog", Description: "Write outro"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v, want %+v", rows, want)
	}
}

func TestListSingleProject(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog",
		task(100, "Write intro", "2026-09-20", false),
	)
	addProject(t, ws, "other",
		task(102, "Fix bug", "2026-09-18", false),
	)
	svc := NewService(&fakeGit{})

	rows, err := svc.List(ws, "my-blog", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Project != "my-blog" || rows[0].ID != 100 {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestListSingleProjectNotFound(t *testing.T) {
	ws := newTestWorkspace(t)
	svc := NewService(&fakeGit{})

	_, err := svc.List(ws, "nope", true)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error type = %T, want *grinderr.User", err)
	}
	if err.Error() != "Project 'nope' not found." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestListOpenOnlyFiltersDone(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog",
		task(100, "Write intro", "2026-09-20", false),
		task(101, "Write outro", "", true),
	)
	svc := NewService(&fakeGit{})

	open, err := svc.List(ws, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != 100 {
		t.Errorf("open rows = %+v, want only task 100", open)
	}

	all, err := svc.List(ws, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("all rows = %d, want 2", len(all))
	}
}

func TestListFiltersCanceled(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog",
		task(100, "Write intro", "2026-09-20", false),
		task(101, "Write outro", "", true),
		task(102, "Abandoned", "", false),
	)
	// Mark task 102 canceled, as `grind cancel` would.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := projects.Projects["my-blog"]
	entry.Tasks[2].Canceled = true
	projects.Projects["my-blog"] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&fakeGit{})

	// Canceled tasks are hidden from BOTH views — they are no longer
	// actionable, so even -a must not resurrect them.
	open, err := svc.List(ws, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != 100 {
		t.Errorf("open rows = %+v, want only task 100", open)
	}

	all, err := svc.List(ws, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("all rows = %d, want 2 (canceled task hidden)", len(all))
	}
}

func TestListSortsByDueThenID(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog",
		task(103, "Same due", "2026-09-20", false),
		task(100, "Same due", "2026-09-20", false),
		task(102, "No due", "", false),
		task(101, "Earlier", "2026-09-10", false),
	)
	svc := NewService(&fakeGit{})

	rows, err := svc.List(ws, "", true)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	// Due asc, equal due tiebreak by ID asc, no-due last.
	want := []int{101, 100, 103, 102}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("IDs = %v, want %v", ids, want)
	}
}

func TestListMissingFile(t *testing.T) {
	// A workspace without .projects.json simply has no tasks.
	ws := &workspace.Workspace{
		Root:         t.TempDir(),
		BareRepo:     filepath.Join(t.TempDir(), ".grind.repo.git"),
		MainWorktree: filepath.Join(t.TempDir(), ".main"),
	}
	svc := NewService(&fakeGit{})

	rows, err := svc.List(ws, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

func TestCompleteSetsDoneAndCommits(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog",
		task(100, "Write intro", "2026-09-20", false),
	)
	fake := &fakeGit{}
	svc := NewService(fake)

	before := time.Now().UTC()
	task, alreadyDone, err := svc.Complete(ws, 100)
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	if alreadyDone {
		t.Error("alreadyDone = true, want false")
	}
	if !task.Done {
		t.Error("task not marked done")
	}
	if task.CompletedAt == nil {
		t.Fatal("CompletedAt = nil, want set")
	}
	if task.CompletedAt.Before(before.Add(-time.Second)) || task.CompletedAt.After(after.Add(time.Second)) {
		t.Errorf("CompletedAt = %v, want around now", task.CompletedAt)
	}

	// The task must be persisted as done.
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	stored := projects.Projects["my-blog"].Tasks[0]
	if !stored.Done || stored.CompletedAt == nil {
		t.Errorf("stored task = %+v, want done with CompletedAt", stored)
	}

	if len(fake.commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(fake.commits))
	}
	c := fake.commits[0]
	if c.worktree != ws.MainWorktree {
		t.Errorf("commit worktree = %q, want %q", c.worktree, ws.MainWorktree)
	}
	if c.message != "Complete task #100" {
		t.Errorf("commit message = %q", c.message)
	}
	if len(c.paths) != 1 || c.paths[0] != ".projects.json" {
		t.Errorf("commit paths = %v", c.paths)
	}
}

func TestCompleteUnknownID(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog", task(100, "Write intro", "", false))
	svc := NewService(&fakeGit{})

	_, _, err := svc.Complete(ws, 999)
	if err == nil {
		t.Fatal("expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error type = %T, want *grinderr.User", err)
	}
	if err.Error() != "Task #999 not found." {
		t.Errorf("message = %q", err.Error())
	}
}

func TestCompleteSearchesAllProjects(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog", task(100, "Write intro", "", false))
	addProject(t, ws, "other", task(101, "Fix bug", "", false))
	fake := &fakeGit{}
	svc := NewService(fake)

	// Task 101 lives on the second project; the global ID must still find
	// it without a project name.
	task, alreadyDone, err := svc.Complete(ws, 101)
	if err != nil {
		t.Fatal(err)
	}
	if alreadyDone {
		t.Error("alreadyDone = true, want false")
	}
	if task.ID != 101 || !task.Done {
		t.Errorf("task = %+v, want done task 101", task)
	}

	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !projects.Projects["other"].Tasks[0].Done {
		t.Error("task on 'other' not marked done")
	}
	if projects.Projects["my-blog"].Tasks[0].Done {
		t.Error("task on 'my-blog' must stay open")
	}
}

func TestCompleteAlreadyDoneSkipsCommit(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog", task(100, "Write intro", "", true))
	fake := &fakeGit{}
	svc := NewService(fake)

	task, alreadyDone, err := svc.Complete(ws, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !alreadyDone {
		t.Error("alreadyDone = false, want true")
	}
	if !task.Done {
		t.Error("task should still be done")
	}
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0 (no pointless re-complete commit)", len(fake.commits))
	}
}

func TestCompleteRefusesCanceled(t *testing.T) {
	ws := newTestWorkspace(t)
	addProject(t, ws, "my-blog", task(100, "Write intro", "", false))
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	entry := projects.Projects["my-blog"]
	entry.Tasks[0].Canceled = true
	projects.Projects["my-blog"] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGit{}
	svc := NewService(fake)

	_, _, err = svc.Complete(ws, 100)
	if err == nil {
		t.Fatal("Complete on canceled task: expected error")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error = %T, want *grinderr.User", err)
	}
	if err.Error() != "Task #100 is canceled." {
		t.Errorf("error = %q", err.Error())
	}
	if len(fake.commits) != 0 {
		t.Errorf("commits = %d, want 0", len(fake.commits))
	}
}