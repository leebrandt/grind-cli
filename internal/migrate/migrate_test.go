package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// v1ConfigJSON is a realistic v1 .grind.json: billing plus the optional blocks,
// and — the one key v2 deleted — a currency.
const v1ConfigJSON = `{
  "billing": {
    "roundTo": "quarter-hour",
    "defaultRate": 150
  },
  "my": {
    "name": "Lee Brandt",
    "company": "Lee Brandt LLC"
  },
  "currency": "USD",
  "paymentTerms": "Net 30",
  "projectTypes": ["blog", "song"]
}`

// addDate is what the fake git reports as the date the project config was
// first committed, so createdAt is a fixed fact in every assertion below.
var addDate = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeGit stubs git.Git for this package. Plan reads three methods
// (DefaultBranch, CommitAddDate, HasChanges) and Apply calls two more
// (MoveWorktree, Commit); the rest exist only to satisfy the interface.
type fakeGit struct {
	branch   string
	addDates map[string]time.Time
	dirty    bool
	moveErr  error // when set, MoveWorktree fails — the interruption test
	moves    [][2]string
	commits  []fakeCommit
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) DefaultBranch(repoPath string) (string, error) {
	if f.branch != "" {
		return f.branch, nil
	}
	return "main", nil
}

func (f *fakeGit) CommitAddDate(repoPath, branch, path string) (time.Time, error) {
	return f.addDates[path], nil
}

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) { return f.dirty, nil }

func (f *fakeGit) MoveWorktree(repoPath, from, to string) error {
	if f.moveErr != nil {
		return f.moveErr
	}
	f.moves = append(f.moves, [2]string{from, to})
	return nil
}

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}

func (f *fakeGit) InitBare(path string) error                              { return nil }
func (f *fakeGit) InitialCommit(repoPath, branch string) error             { return nil }
func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error { return nil }
func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error)     { return true, nil }
func (f *fakeGit) CreateBranch(repoPath, branch string) error              { return nil }
func (f *fakeGit) CommitAll(worktreePath, message string) error            { return nil }
func (f *fakeGit) RemoteURL(repoPath string) (string, error)               { return "", nil }
func (f *fakeGit) PushAll(repoPath string) error                           { return nil }
func (f *fakeGit) SetRemoteURL(repoPath, url string) error                 { return nil }
func (f *fakeGit) PushBranch(repoPath, branch string) error                { return nil }
func (f *fakeGit) FetchAll(repoPath string) error                          { return nil }
func (f *fakeGit) IsAncestor(repoPath, ancestor, descendant string) (bool, error) {
	return false, nil
}
func (f *fakeGit) FastForwardWorktree(worktreePath, branch string) error { return nil }
func (f *fakeGit) FastForwardRef(repoPath, branch string) error          { return nil }
func (f *fakeGit) ListRemoteBranches(repoPath string) ([]string, error)  { return nil, nil }
func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return time.Time{}, nil
}
func (f *fakeGit) MergeBranch(worktreePath, branch string) error      { return nil }
func (f *fakeGit) RemoveWorktree(repoPath, worktreePath string) error { return nil }
func (f *fakeGit) DeleteBranch(repoPath, branch string) error         { return nil }

// Ensure the stub satisfies the interface this package depends on.
var _ git.Git = (*fakeGit)(nil)

// newV1Workspace creates the skeleton of a v1 workspace in a temp dir: the
// bare repo and the main worktree v1 called grind/, holding .grind.json.
func newV1Workspace(t *testing.T, configJSON string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, ".grind.repo.git"),
		filepath.Join(root, "grind"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if configJSON != "" {
		writeFile(t, filepath.Join(root, "grind", ".grind.json"), configJSON)
	}
	return root
}

// writeV1Project writes a v1 project directory on the main worktree: the
// config plus the the-idea.md v1 always left beside it.
func writeV1Project(t *testing.T, root, name, projectJSON string) string {
	t.Helper()
	dir := filepath.Join(root, "grind", "projects", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".project.json"), projectJSON)
	writeFile(t, filepath.Join(dir, "the-idea.md"), "# "+name+"\n")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// planSingle plans a workspace holding one project called beta, so a test can
// assert one mapping rule at a time.
func planSingle(t *testing.T, g *fakeGit, projectJSON string) (*Plan, error) {
	t.Helper()
	root := newV1Workspace(t, v1ConfigJSON)
	writeV1Project(t, root, "beta", projectJSON)
	return NewPlan(root, g)
}

func TestPlanMapsProjectFields(t *testing.T) {
	// The v1 shapes below are deliberately the real ones, copied from a
	// workspace grind v1 0.9.4 wrote.
	tests := []struct {
		name     string
		project  string
		want     config.ProjectEntry
		warnings []string
		wantErr  string
	}{
		{
			name:    "type and idea title are copied",
			project: `{"name":"beta","type":"song","idea":"# Write a song about rain","billing":{"roundTo":"quarter-hour","rate":150}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Type:      "song",
				Idea:      "Write a song about rain",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
		},
		{
			name:    "an idea body is dropped with a warning",
			project: `{"name":"beta","idea":"# Write a song\n\nAbout rain, and the noise it makes.","billing":{"roundTo":"quarter-hour","rate":150}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Idea:      "Write a song",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{"beta: the v1 idea had a body; only the title was kept"},
		},
		{
			name:    "an idea with no heading has no title",
			project: `{"name":"beta","idea":"just some words","billing":{"roundTo":"quarter-hour","rate":150}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{"beta: the v1 idea had no title heading; the project idea is empty"},
		},
		{
			name:    "no idea at all stays empty",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
		},
		{
			name:    "a missing rate falls back to the workspace default",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour"}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{"beta: billing.rate was missing, used workspace default 150"},
		},
		{
			name:    "a zero rate counts as missing",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":0}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{"beta: billing.rate was missing, used workspace default 150"},
		},
		{
			name:    "absent billing takes both workspace defaults",
			project: `{"name":"beta"}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{"beta: billing.rate was missing, used workspace default 150"},
		},
		{
			name:    "an empty roundTo falls back to the workspace default",
			project: `{"name":"beta","billing":{"rate":200}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 200},
				CreatedAt: addDate,
			},
		},
		{
			name:    "an active session comes along with a null end",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150},"time":[{"start":"2026-10-25T21:10:25.689Z","end":null,"duration":0,"rounded":0}]}`,
			want: config.ProjectEntry{
				Name:    "beta",
				Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				Sessions: []config.Session{
					{Start: time.Date(2026, 10, 25, 21, 10, 25, 689_000_000, time.UTC)},
				},
				CreatedAt: addDate,
			},
		},
		{
			name:    "an ended, invoiced session keeps its frozen numbers",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150},"time":[{"start":"2026-10-25T21:10:25.689Z","end":"2026-10-25T21:10:28.803Z","duration":3,"rounded":900,"invoiced":true}]}`,
			want: config.ProjectEntry{
				Name:    "beta",
				Billing: config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				Sessions: []config.Session{
					{
						Start:    time.Date(2026, 10, 25, 21, 10, 25, 689_000_000, time.UTC),
						End:      timePtr(time.Date(2026, 10, 25, 21, 10, 28, 803_000_000, time.UTC)),
						Duration: 3,
						Rounded:  900,
						Invoiced: true,
					},
				},
				CreatedAt: addDate,
			},
		},
		{
			name:    "a negative duration is rejected",
			project: `{"name":"beta","time":[{"start":"2026-10-25T21:10:25Z","duration":-5,"rounded":0}]}`,
			wantErr: "negative duration",
		},
		{
			name:    "a negative rounded is rejected",
			project: `{"name":"beta","time":[{"start":"2026-10-25T21:10:25Z","duration":5,"rounded":-900}]}`,
			wantErr: "negative rounded",
		},
		{
			name:    "client, repo, code and longTerm are copied",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150},"client":{"company":"Acme Corp","email":"ap@acme.example"},"repo":"https://example.com/beta.git","code":"BETA-1","longTerm":true}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				Client:    &config.ClientInfo{Company: "Acme Corp", Email: "ap@acme.example"},
				Repo:      "https://example.com/beta.git",
				Code:      "BETA-1",
				LongTerm:  true,
				CreatedAt: addDate,
			},
		},
		{
			name:    "a malformed deadline is dropped with a warning",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150},"deadline":"2026-13-45"}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{`beta: dropped malformed deadline "2026-13-45"`},
		},
		{
			name:    "a valid deadline is kept",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150},"deadline":"2026-11-01"}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				Deadline:  "2026-11-01",
				CreatedAt: addDate,
			},
		},
		{
			name:    "status is copied, absent status means active",
			project: `{"name":"beta","status":"published","billing":{"roundTo":"quarter-hour","rate":150}}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Status:    "published",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
		},
		{
			name:    "publications are dropped with the URLs preserved in a warning",
			project: `{"name":"beta","billing":{"roundTo":"quarter-hour","rate":150},"publications":[{"url":"https://example.com/rain","publishedAt":"2026-10-20"}]}`,
			want: config.ProjectEntry{
				Name:      "beta",
				Billing:   config.BillingEntry{RoundTo: "quarter-hour", Rate: 150},
				CreatedAt: addDate,
			},
			warnings: []string{"beta: dropped 1 publication: https://example.com/rain"},
		},
		{
			name:    "a name that disagrees with its directory is a system error",
			project: `{"name":"other","billing":{"roundTo":"quarter-hour","rate":150}}`,
			wantErr: `projects/beta/.project.json says name "other"; the directory says "beta".`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &fakeGit{addDates: map[string]time.Time{"projects/beta/.project.json": addDate}}
			plan, err := planSingle(t, g, tt.project)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("NewPlan() = nil error, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				var system *grinderr.System
				if !errors.As(err, &system) {
					t.Errorf("error type = %T, want a system error (exit 2)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewPlan() error = %v", err)
			}

			got, ok := plan.Projects.Projects["beta"]
			if !ok {
				t.Fatalf("no beta entry in %v", plan.Projects.Projects)
			}
			assertEntry(t, got, tt.want)
			if !equalStrings(plan.Warnings, tt.warnings) {
				t.Errorf("warnings = %v, want %v", plan.Warnings, tt.warnings)
			}
		})
	}
}

// assertEntry compares everything except the session slice, which needs its
// own comparison because it holds pointers.
func assertEntry(t *testing.T, got, want config.ProjectEntry) {
	t.Helper()
	if len(got.Sessions) != len(want.Sessions) {
		t.Fatalf("sessions = %+v, want %+v", got.Sessions, want.Sessions)
	}
	for i := range want.Sessions {
		gs, ws := got.Sessions[i], want.Sessions[i]
		if !gs.Start.Equal(ws.Start) || gs.Duration != ws.Duration ||
			gs.Rounded != ws.Rounded || gs.Invoiced != ws.Invoiced {
			t.Errorf("session %d = %+v, want %+v", i, gs, ws)
		}
		if (gs.End == nil) != (ws.End == nil) {
			t.Errorf("session %d end = %v, want %v", i, gs.End, ws.End)
		} else if gs.End != nil && !gs.End.Equal(*ws.End) {
			t.Errorf("session %d end = %v, want %v", i, gs.End, ws.End)
		}
	}

	got.Sessions, want.Sessions = nil, nil
	if !entriesEqual(got, want) {
		t.Errorf("entry = %+v\nwant %+v", got, want)
	}
}

// entriesEqual compares two entries field by field, following the Client
// pointer so the comparison stays total.
func entriesEqual(a, b config.ProjectEntry) bool {
	if (a.Client == nil) != (b.Client == nil) {
		return false
	}
	if a.Client != nil && *a.Client != *b.Client {
		return false
	}
	a.Client, b.Client = nil, nil
	return a.Name == b.Name &&
		a.Type == b.Type &&
		a.Idea == b.Idea &&
		a.Billing == b.Billing &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.Repo == b.Repo &&
		a.Code == b.Code &&
		a.LongTerm == b.LongTerm &&
		a.Deadline == b.Deadline &&
		a.Status == b.Status &&
		len(a.Tasks) == len(b.Tasks)
}

func timePtr(t time.Time) *time.Time { return &t }

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPlanRenumbersTasksGlobally(t *testing.T) {
	// v1 allocated task IDs per project from 1, so both projects below have
	// a task #1. v2's `done task <id>` searches every project, so the two
	// would be ambiguous — renumbering into one block from 100 is mandatory.
	root := newV1Workspace(t, v1ConfigJSON)
	writeV1Project(t, root, "beta", `{"name":"beta","billing":{"rate":150},"tasks":[
		{"id":1,"description":"second project, first task","done":false,"createdAt":"2026-10-25T21:10:17.046Z"},
		{"id":2,"description":"second project, second task","done":true,"createdAt":"2026-10-25T21:10:18.046Z","completedAt":"2026-10-25T22:00:00Z","dueDate":"2026-11-01"}
	]}`)
	writeV1Project(t, root, "alpha", `{"name":"alpha","billing":{"rate":200},"tasks":[
		{"id":1,"description":"first project, first task","done":false,"createdAt":"2026-10-25T21:10:19.046Z"}
	]}`)

	plan, err := NewPlan(root, &fakeGit{addDates: map[string]time.Time{"projects/alpha/.project.json": addDate, "projects/beta/.project.json": addDate}})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	// Projects are visited in name order and tasks in stored order, so the
	// same workspace always migrates to the same IDs.
	wantIDs := map[string][]int{"alpha": {100}, "beta": {101, 102}}
	for name, ids := range wantIDs {
		entry := plan.Projects.Projects[name]
		var got []int
		for _, task := range entry.Tasks {
			got = append(got, task.ID)
		}
		if !equalInts(got, ids) {
			t.Errorf("%s task IDs = %v, want %v", name, got, ids)
		}
	}

	// nextTaskId lands one past the highest issued ID.
	if plan.Projects.NextTaskID != 103 {
		t.Errorf("NextTaskID = %d, want 103", plan.Projects.NextTaskID)
	}

	// The rest of each task survives: description, done, the dates, the
	// cancellation flag (absent in v1, so false).
	beta := plan.Projects.Projects["beta"].Tasks
	if beta[1].Description != "second project, second task" || !beta[1].Done {
		t.Errorf("task 102 = %+v", beta[1])
	}
	if beta[1].DueDate != "2026-11-01" {
		t.Errorf("task 102 due date = %q", beta[1].DueDate)
	}
	if beta[1].CompletedAt == nil {
		t.Error("task 102 lost its completedAt")
	}
	if beta[0].Canceled {
		t.Error("a task with no canceled field must not become canceled")
	}
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPlanCreatedAtFallbackChain(t *testing.T) {
	sessionStart := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	mtime := time.Date(2026, 7, 2, 8, 30, 0, 0, time.UTC)
	project := `{"name":"beta","billing":{"rate":150},"time":[{"start":"2026-06-01T09:00:00Z","end":"2026-06-01T10:00:00Z","duration":3600,"rounded":3600}]}`

	t.Run("the commit that added the config wins", func(t *testing.T) {
		root := newV1Workspace(t, v1ConfigJSON)
		cfgDir := writeV1Project(t, root, "beta", project)
		setMtime(t, filepath.Join(cfgDir, ".project.json"), mtime)

		g := &fakeGit{addDates: map[string]time.Time{"projects/beta/.project.json": addDate}}
		plan, err := NewPlan(root, g)
		if err != nil {
			t.Fatalf("NewPlan() error = %v", err)
		}
		if got := plan.Projects.Projects["beta"].CreatedAt; !got.Equal(addDate) {
			t.Errorf("CreatedAt = %v, want the adding commit's date %v", got, addDate)
		}
	})

	t.Run("otherwise the earliest session", func(t *testing.T) {
		root := newV1Workspace(t, v1ConfigJSON)
		writeV1Project(t, root, "beta", `{"name":"beta","billing":{"rate":150},"time":[
			{"start":"2026-06-02T09:00:00Z","duration":60,"rounded":900},
			{"start":"2026-06-01T09:00:00Z","duration":60,"rounded":900}
		]}`)

		// No add date: CommitAddDate returns the zero time.
		plan, err := NewPlan(root, &fakeGit{})
		if err != nil {
			t.Fatalf("NewPlan() error = %v", err)
		}
		if got := plan.Projects.Projects["beta"].CreatedAt; !got.Equal(sessionStart) {
			t.Errorf("CreatedAt = %v, want the earliest session start %v", got, sessionStart)
		}
	})

	t.Run("otherwise the config file's mtime", func(t *testing.T) {
		root := newV1Workspace(t, v1ConfigJSON)
		cfgDir := writeV1Project(t, root, "beta", `{"name":"beta","billing":{"rate":150}}`)
		setMtime(t, filepath.Join(cfgDir, ".project.json"), mtime)

		plan, err := NewPlan(root, &fakeGit{})
		if err != nil {
			t.Fatalf("NewPlan() error = %v", err)
		}
		if got := plan.Projects.Projects["beta"].CreatedAt; !got.Equal(mtime) {
			t.Errorf("CreatedAt = %v, want the config's mtime %v", got, mtime)
		}
		if len(plan.Warnings) != 0 {
			t.Errorf("warnings = %v, want none — an mtime is a real date", plan.Warnings)
		}
	})
}

func TestCreatedAtFallsBackToTheMigrationInstant(t *testing.T) {
	// The last resort of the createdAt chain: no adding commit, no sessions and
	// a config that cannot be stat'ed. It is nearly unreachable in practice,
	// which is exactly why it is tested directly — a fallback nobody exercises
	// is a fallback nobody knows works.
	root := newV1Workspace(t, v1ConfigJSON)
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	plan, err := NewPlanAt(root, &fakeGit{}, clock.NewFake(at))
	if err != nil {
		t.Fatalf("NewPlanAt() error = %v", err)
	}

	ghost := projectSource{name: "ghost", path: filepath.Join(root, "ghost", ".project.json")}
	if got := plan.createdAt(ghost, time.Time{}, nil); !got.Equal(at) {
		t.Errorf("createdAt() = %v, want the migration instant %v", got, at)
	}
	want := []string{"ghost: no creation date could be derived, used the migration instant"}
	if !equalStrings(plan.Warnings, want) {
		t.Errorf("warnings = %v, want %v", plan.Warnings, want)
	}
}

// setMtime pins a file's modification time so the createdAt fallback is a
// fixed fact rather than whenever the test happened to run.
func setMtime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func TestPlanCurrencyWarning(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		want     []string
	}{
		{"USD is what grind bills in", "USD", nil},
		{"absent is fine too", "", nil},
		{
			"AUD is dropped loudly because the amounts are not converted",
			"AUD",
			[]string{`currency "AUD" dropped: grind bills in USD only, and the amounts were not converted`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := `{"billing":{"roundTo":"quarter-hour","defaultRate":150}`
			if tt.currency != "" {
				cfg += `,"currency":"` + tt.currency + `"`
			}
			cfg += "}"

			root := newV1Workspace(t, cfg)
			writeV1Project(t, root, "beta", `{"name":"beta","billing":{"rate":150}}`)

			plan, err := NewPlan(root, &fakeGit{})
			if err != nil {
				t.Fatalf("NewPlan() error = %v", err)
			}
			if !equalStrings(plan.Warnings, tt.want) {
				t.Errorf("warnings = %v, want %v", plan.Warnings, tt.want)
			}
		})
	}
}

func TestPlanMapsWorkspaceConfig(t *testing.T) {
	root := newV1Workspace(t, v1ConfigJSON)
	writeV1Project(t, root, "beta", `{"name":"beta","billing":{"rate":150}}`)

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	want := config.GrindConfig{
		Billing:      config.BillingConfig{RoundTo: "quarter-hour", DefaultRate: 150},
		ProjectTypes: []string{"blog", "song"},
		My:           &config.MyConfig{Name: "Lee Brandt", Company: "Lee Brandt LLC"},
		PaymentTerms: "Net 30",
	}
	got := plan.Config
	if got.Billing != want.Billing || got.PaymentTerms != want.PaymentTerms {
		t.Errorf("config = %+v, want %+v", got, want)
	}
	if got.My == nil || *got.My != *want.My {
		t.Errorf("my = %+v, want %+v", got.My, want.My)
	}
	if !equalStrings(got.ProjectTypes, want.ProjectTypes) {
		t.Errorf("projectTypes = %v, want %v", got.ProjectTypes, want.ProjectTypes)
	}
}

func TestPlanMovesInvoicesAndLayout(t *testing.T) {
	root := newV1Workspace(t, v1ConfigJSON)
	alphaDir := writeV1Project(t, root, "alpha", `{"name":"alpha","billing":{"rate":200}}`)
	invoiceDir := filepath.Join(alphaDir, "invoices", "2026-10-25T21-12-46")
	if err := os.MkdirAll(invoiceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(invoiceDir, "invoice.md"), "# INVOICE\n")
	writeFile(t, filepath.Join(invoiceDir, "invoice.pdf"), "%PDF-1.4\n")

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	if len(plan.Invoices) != 1 {
		t.Fatalf("invoices = %+v, want 1", plan.Invoices)
	}
	move := plan.Invoices[0]
	// The move happens inside the v1 main worktree, so the destination is
	// relative to grind/ for now — the worktree move carries it to .main/
	// afterwards. The v1 directory name is kept verbatim: v2 never parses an
	// existing invoice ID, and renaming it would rewrite history for cosmetics.
	if want := filepath.Join(root, "grind", "invoices", "alpha", "2026-10-25T21-12-46"); move.To != want {
		t.Errorf("invoice To = %q, want %q", move.To, want)
	}
	if move.Project != "alpha" {
		t.Errorf("invoice Project = %q", move.Project)
	}

	// The v1 project directory is deleted after its content is consolidated;
	// the idea file goes with it because the text already lives in `idea`.
	if !equalStrings(plan.RemoveDirs, []string{alphaDir}) {
		t.Errorf("RemoveDirs = %v, want [%s]", plan.RemoveDirs, alphaDir)
	}

	// v1 never made invoices/ or published/; projects/ exists but holds
	// project configs rather than merged work product, so all three get a
	// .gitkeep and the two missing ones get created.
	wantDirs := []string{
		filepath.Join(root, "grind", "projects"),
		filepath.Join(root, "grind", "invoices"),
		filepath.Join(root, "grind", "published"),
	}
	sort.Strings(wantDirs)
	gotDirs := append([]string(nil), plan.CreateDirs...)
	sort.Strings(gotDirs)
	if !equalStrings(gotDirs, wantDirs) {
		t.Errorf("CreateDirs = %v, want %v", gotDirs, wantDirs)
	}
}

func TestPlanAlreadyAMigratedWorkspace(t *testing.T) {
	root := newV1Workspace(t, v1ConfigJSON)
	// After a completed migration .projects.json exists on .main.
	if err := os.MkdirAll(filepath.Join(root, ".main"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".main", ".projects.json"), "{}\n")

	_, err := NewPlan(root, &fakeGit{})
	if err == nil {
		t.Fatal("NewPlan() = nil error, want a user error")
	}
	if err.Error() != "This workspace is already a v2 workspace." {
		t.Errorf("error = %q", err)
	}
	if code := grinderr.ExitCode(err); code != 1 {
		t.Errorf("ExitCode = %d, want 1 (user error)", code)
	}
}

func TestPlanResumesAfterTheMove(t *testing.T) {
	// An interrupted migration has already renamed grind/ to .main/. The v1
	// files are still there, so a second run must find them and finish.
	root := t.TempDir()
	for _, dir := range []string{".grind.repo.git", ".main"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, ".main", ".grind.json"), v1ConfigJSON)
	alphaDir := filepath.Join(root, ".main", "projects", "alpha")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(alphaDir, ".project.json"), `{"name":"alpha","billing":{"rate":200}}`)

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if plan.MoveMain {
		t.Error("MoveMain = true after the worktree was already moved")
	}
	if _, ok := plan.Projects.Projects["alpha"]; !ok {
		t.Errorf("projects = %v, want alpha", plan.Projects.Projects)
	}
}

func TestPlanLegacyConfigLocation(t *testing.T) {
	// Older v1 workspaces kept .project.json in the project worktree, not on
	// main. v1's own `migrate` existed to consolidate them; this must read
	// them where they are.
	root := t.TempDir()
	for _, dir := range []string{".grind.repo.git", "grind", filepath.Join("grind", "projects", "alpha")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, "grind", ".grind.json"), v1ConfigJSON)
	legacy := filepath.Join(root, "alpha", "projects", "alpha")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(legacy, ".project.json"), `{"name":"alpha","type":"blog","billing":{"rate":200}}`)

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	entry, ok := plan.Projects.Projects["alpha"]
	if !ok {
		t.Fatalf("projects = %v, want alpha", plan.Projects.Projects)
	}
	if entry.Type != "blog" || entry.Billing.Rate != 200 {
		t.Errorf("alpha = %+v", entry)
	}
}

func TestPlanLegacyConfigWithoutADirectoryOnMain(t *testing.T) {
	// The legacy layout can leave a project with NOTHING on main: the config
	// lives only in the project worktree, and v1 never made grind/projects/<name>.
	// Naming that directory anyway would stage a pathspec that matches nothing,
	// and `git add` fails on those — the whole migration would refuse to commit.
	root := t.TempDir()
	for _, dir := range []string{".grind.repo.git", "grind"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, "grind", ".grind.json"), v1ConfigJSON)
	legacy := filepath.Join(root, "alpha", "projects", "alpha")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(legacy, ".project.json"), `{"name":"alpha","billing":{"rate":200}}`)

	g := &fakeGit{}
	plan, err := NewPlan(root, g)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if len(plan.RemoveDirs) != 0 {
		t.Errorf("RemoveDirs = %v, want none: nothing was there to remove", plan.RemoveDirs)
	}

	ws := &workspace.Workspace{
		Root:         root,
		BareRepo:     filepath.Join(root, ".grind.repo.git"),
		MainWorktree: filepath.Join(root, ".main"),
	}
	if err := Apply(ws, plan, g); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(g.commits) != 1 {
		t.Fatalf("commits = %v, want 1", g.commits)
	}
	if containsString(g.commits[0].paths, "projects/alpha") {
		t.Errorf("commit paths = %v, want no projects/alpha", g.commits[0].paths)
	}
}

func TestPlanEmptyWorkspace(t *testing.T) {
	root := newV1Workspace(t, v1ConfigJSON)

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if len(plan.Projects.Projects) != 0 {
		t.Errorf("projects = %v, want none", plan.Projects.Projects)
	}
	// A workspace with no tasks still starts the global counter at 100.
	if plan.Projects.NextTaskID != 100 {
		t.Errorf("NextTaskID = %d, want 100", plan.Projects.NextTaskID)
	}
	if plan.Projects.Version != 1 {
		t.Errorf("Version = %d, want 1", plan.Projects.Version)
	}
}

func TestPlanNotAWorkspace(t *testing.T) {
	tests := []struct {
		name string
		root func(t *testing.T) string
	}{
		{"no bare repo", func(t *testing.T) string { return t.TempDir() }},
		{
			"bare repo but no main worktree at all",
			func(t *testing.T) string {
				root := t.TempDir()
				if err := os.MkdirAll(filepath.Join(root, ".grind.repo.git"), 0o755); err != nil {
					t.Fatal(err)
				}
				return root
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewPlan(tt.root(t), &fakeGit{})
			if err == nil {
				t.Fatal("NewPlan() = nil error, want a user error")
			}
			if err.Error() != "Not in a grind workspace." {
				t.Errorf("error = %q", err)
			}
		})
	}
}

func TestPlanRefusesWhenBothMainWorktreesExist(t *testing.T) {
	// Migration never produces this state, so it is a hand-made mess. Guessing
	// which one is the v1 worktree would rename it onto the other one and fail
	// with a git error nobody can act on; saying so is cheaper.
	root := t.TempDir()
	for _, dir := range []string{".grind.repo.git", "grind", ".main"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, "grind", ".grind.json"), v1ConfigJSON)

	_, err := NewPlan(root, &fakeGit{})
	if err == nil {
		t.Fatal("NewPlan() = nil error, want the ambiguity error")
	}
	if !strings.Contains(err.Error(), "both grind/ and .main/ exist") {
		t.Errorf("error = %q", err)
	}
}

func TestPlanWritesNothing(t *testing.T) {
	root := newV1Workspace(t, v1ConfigJSON)
	alphaDir := writeV1Project(t, root, "alpha", `{"name":"alpha","billing":{"rate":200}}`)
	invoiceDir := filepath.Join(alphaDir, "invoices", "2026-10-25T21-12-46")
	if err := os.MkdirAll(invoiceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(invoiceDir, "invoice.md"), "# INVOICE\n")

	before := snapshot(t, root)

	if _, err := NewPlan(root, &fakeGit{}); err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	after := snapshot(t, root)
	if len(before) != len(after) {
		t.Fatalf("plan changed the tree:\nbefore %v\nafter %v", before, after)
	}
	for path, state := range before {
		if after[path] != state {
			t.Errorf("%s changed: %q → %q", path, state, after[path])
		}
	}
}

// snapshot records every path under root with its size and modification time,
// so a test can prove nothing was written — a dry run must not create so much
// as .main/.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = info.ModTime().String() + "/" + strconv.FormatInt(info.Size(), 10)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestApplyConsolidatesAndCommits(t *testing.T) {
	// Apply needs no worktree move to be exercised: this is the resume case,
	// where the v1 files already sit in .main.
	root := t.TempDir()
	for _, dir := range []string{".grind.repo.git", ".main"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, ".main", ".grind.json"), v1ConfigJSON)
	alphaDir := filepath.Join(root, ".main", "projects", "alpha")
	if err := os.MkdirAll(filepath.Join(alphaDir, "invoices", "2026-10-25T21-12-46"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(alphaDir, ".project.json"), `{"name":"alpha","billing":{"rate":200}}`)
	writeFile(t, filepath.Join(alphaDir, "the-idea.md"), "# Alpha\n")
	writeFile(t, filepath.Join(alphaDir, "invoices", "2026-10-25T21-12-46", "invoice.md"), "# INVOICE\n")

	g := &fakeGit{}
	plan, err := NewPlan(root, g)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}

	ws := &workspace.Workspace{
		Root:         root,
		BareRepo:     filepath.Join(root, ".grind.repo.git"),
		MainWorktree: filepath.Join(root, ".main"),
	}
	if err := Apply(ws, plan, g); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// The v1 project directory is gone, config, idea and all.
	if _, err := os.Stat(alphaDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("v1 project dir still exists: %v", err)
	}

	// The invoice moved, with its id intact.
	invoice := filepath.Join(root, ".main", "invoices", "alpha", "2026-10-25T21-12-46", "invoice.md")
	if _, err := os.Stat(invoice); err != nil {
		t.Errorf("invoice not at %s: %v", invoice, err)
	}

	// Both config files are written and readable.
	projects, err := config.ReadProjects(filepath.Join(root, ".main", ".projects.json"))
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	if _, ok := projects.Projects["alpha"]; !ok {
		t.Errorf("projects = %v, want alpha", projects.Projects)
	}
	if _, err := config.Read(filepath.Join(root, ".main", ".grind.json")); err != nil {
		t.Fatalf("Read config: %v", err)
	}

	// One commit, and it stages exactly the migration's own paths.
	if len(g.commits) != 1 {
		t.Fatalf("commits = %v, want 1", g.commits)
	}
	commit := g.commits[0]
	if commit.message != CommitMessage {
		t.Errorf("commit message = %q, want %q", commit.message, CommitMessage)
	}
	if commit.worktree != filepath.Join(root, ".main") {
		t.Errorf("commit worktree = %q", commit.worktree)
	}
	for _, want := range []string{
		".grind.json",
		".projects.json",
		"projects/.gitkeep",
		"invoices/.gitkeep",
		"published/.gitkeep",
		"invoices/alpha/2026-10-25T21-12-46",
		"projects/alpha",
	} {
		if !containsString(commit.paths, want) {
			t.Errorf("commit paths = %v, missing %q", commit.paths, want)
		}
	}
}

func TestApplyWritesStateBeforeItMovesAnything(t *testing.T) {
	// The move is the one step that cannot be undone by writing a file, so
	// everything the migration needs must already be on disk when it runs.
	// Simulate the interruption — git refuses the move — and assert the v1 data
	// survived, nothing was destroyed, and a second run plans the same
	// migration instead of giving up.
	root := newV1Workspace(t, v1ConfigJSON)
	writeV1Project(t, root, "alpha", `{"name":"alpha","billing":{"rate":200}}`)

	ws := &workspace.Workspace{
		Root:         root,
		BareRepo:     filepath.Join(root, ".grind.repo.git"),
		MainWorktree: filepath.Join(root, ".main"),
	}

	failing := &fakeGit{moveErr: errors.New("git worktree move: boom")}
	plan, err := NewPlan(root, failing)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if !plan.MoveMain {
		t.Fatal("MoveMain = false, want a move for an unmigrated workspace")
	}
	if err := Apply(ws, plan, failing); err == nil {
		t.Fatal("Apply() = nil, want the injected move failure")
	}

	// The consolidated state is already written, so the retried move carries it
	// along instead of leaving it to be recomputed from files that are gone.
	projects, err := config.ReadProjects(filepath.Join(root, "grind", ".projects.json"))
	if err != nil {
		t.Fatalf("ReadProjects after a failed move: %v", err)
	}
	if _, ok := projects.Projects["alpha"]; !ok {
		t.Errorf("projects = %v, want alpha", projects.Projects)
	}

	// Nothing was destroyed: deleting the v1 project directories is the last
	// step, after the move has succeeded.
	if _, err := os.Stat(filepath.Join(root, "grind", "projects", "alpha", ".project.json")); err != nil {
		t.Errorf("v1 project config is gone after a failed move: %v", err)
	}

	// A failed attempt commits nothing and never creates .main.
	if len(failing.commits) != 0 {
		t.Errorf("commits = %v, want none", failing.commits)
	}
	if _, err := os.Stat(filepath.Join(root, ".main")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".main exists after a failed move: %v", err)
	}

	// The next invocation re-derives the migration from the v1 configs it can
	// still read — not "already migrated", not an empty workspace.
	g := &fakeGit{}
	plan, err = NewPlan(root, g)
	if err != nil {
		t.Fatalf("NewPlan() after the failed move: %v", err)
	}
	if len(plan.Projects.Projects) != 1 {
		t.Errorf("retry projects = %v, want alpha", plan.Projects.Projects)
	}
	if err := Apply(ws, plan, g); err != nil {
		t.Fatalf("Apply() on retry = %v", err)
	}
	if len(g.commits) != 1 {
		t.Errorf("retry commits = %v, want 1", g.commits)
	}
}

// writeWorktreeConfig writes a project's own copy of its config, at the path v1
// used for it: <root>/<name>/projects/<name>/.project.json.
func writeWorktreeConfig(t *testing.T, root, name, projectJSON string) {
	t.Helper()
	dir := filepath.Join(root, name, "projects", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".project.json"), projectJSON)
}

func TestPlanPrefersTheProjectLevelCopy(t *testing.T) {
	// Both copies exist and disagree. v1 edited the project in its own worktree,
	// so that copy is the one the migration keeps. The main copy is dropped, and
	// the warning says how many sessions it had so the user can check it.
	root := newV1Workspace(t, v1ConfigJSON)
	writeV1Project(t, root, "alpha", `{"name":"alpha","billing":{"rate":200},"time":[{"start":"2026-01-01T00:00:00Z","end":"2026-01-01T01:00:00Z","duration":3600,"rounded":3600}]}`)
	writeWorktreeConfig(t, root, "alpha", `{"name":"alpha","billing":{"rate":300}}`)

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	alpha := plan.Projects.Projects["alpha"]
	if alpha.Billing.Rate != 300 {
		t.Errorf("rate = %v, want 300 from the project-level copy", alpha.Billing.Rate)
	}
	if len(alpha.Sessions) != 0 {
		t.Errorf("sessions = %v, want none: the main copy's session is dropped", alpha.Sessions)
	}

	want := "alpha: migrated from alpha/projects/alpha/.project.json (0 sessions, 0 tasks); discarded the older grind/projects/alpha/.project.json (1 session, 0 tasks)"
	if !containsString(plan.Warnings, want) {
		t.Errorf("warnings = %v, want %q", plan.Warnings, want)
	}
}

func TestPlanIdenticalCopiesWarnNothing(t *testing.T) {
	// Identical copies lose nothing, so there is nothing to tell the user.
	root := newV1Workspace(t, v1ConfigJSON)
	same := `{"name":"alpha","billing":{"rate":200}}`
	writeV1Project(t, root, "alpha", same)
	writeWorktreeConfig(t, root, "alpha", same)

	plan, err := NewPlan(root, &fakeGit{})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "discarded") {
			t.Errorf("warnings = %v, want no discarded-copy warning for identical files", plan.Warnings)
		}
	}
}

func TestPlanFindsSlashNamedProjectAndCleansItsParent(t *testing.T) {
	// v1 names a project after its branch, so "feature/issue-feature" is one
	// project whose config sits two directories deep on main. The migration
	// must find it under that name, and once it is removed the empty "feature"
	// directory it leaves behind must go too.
	root := newV1Workspace(t, v1ConfigJSON)
	writeV1Project(t, root, "feature/issue-feature", `{"name":"feature/issue-feature","billing":{"rate":150}}`)

	g := &fakeGit{}
	plan, err := NewPlan(root, g)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	if _, ok := plan.Projects.Projects["feature/issue-feature"]; !ok {
		t.Fatalf("projects = %v, want feature/issue-feature", plan.Projects.Projects)
	}

	ws := &workspace.Workspace{
		Root:         root,
		BareRepo:     filepath.Join(root, ".grind.repo.git"),
		MainWorktree: filepath.Join(root, ".main"),
	}
	if err := Apply(ws, plan, g); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".main", "projects", "feature")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty projects/feature/ left behind: %v", err)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
