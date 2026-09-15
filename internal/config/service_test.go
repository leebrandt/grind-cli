package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
)

// fakeGit records the calls config operations make so tests can assert what
// would have been sent to real git.
type fakeGit struct {
	commits       []fakeCommit
	setRemoteURLs []string
}

type fakeCommit struct {
	worktree string
	message  string
	paths    []string
}

func (f *fakeGit) InitBare(path string) error { return nil }

func (f *fakeGit) InitialCommit(repoPath, branch string) error { return nil }

func (f *fakeGit) AddWorktree(repoPath, worktreePath, branch string) error { return nil }

func (f *fakeGit) Commit(worktreePath, message string, paths ...string) error {
	f.commits = append(f.commits, fakeCommit{worktree: worktreePath, message: message, paths: paths})
	return nil
}

func (f *fakeGit) IsPathClean(worktreePath, path string) (bool, error) { return true, nil }

func (f *fakeGit) CreateBranch(repoPath, branch string) error { return nil }

func (f *fakeGit) HasChanges(worktreePath string) (bool, error) { return false, nil }

func (f *fakeGit) CommitAll(worktreePath, message string) error { return nil }

func (f *fakeGit) RemoteURL(repoPath string) (string, error) { return "", nil }

func (f *fakeGit) PushAll(repoPath string) error { return nil }

func (f *fakeGit) LastCommitDate(repoPath, branch string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeGit) DefaultBranch(repoPath string) (string, error) { return "main", nil }

func (f *fakeGit) SetRemoteURL(repoPath, url string) error {
	f.setRemoteURLs = append(f.setRemoteURLs, url)
	return nil
}

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

// Ensure the fake satisfies the interface the service depends on.
var _ git.Git = (*fakeGit)(nil)

// newTestPaths builds a Paths pointing at a fresh temp workspace. No config
// files are written, so tests start from the "missing file" state.
func newTestPaths(t *testing.T) (Paths, *fakeGit) {
	t.Helper()
	dir := t.TempDir()
	main := filepath.Join(dir, ".main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := Paths{
		ConfigPath:   filepath.Join(main, ".grind.json"),
		ProjectsPath: filepath.Join(main, ".projects.json"),
		MainWorktree: main,
		BareRepo:     filepath.Join(dir, ".grind.repo.git"),
	}
	return paths, &fakeGit{}
}

// writeConfig writes cfg to the workspace config path.
func writeConfig(t *testing.T, p Paths, cfg GrindConfig) {
	t.Helper()
	if err := Write(p.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
}

// writeProjects writes projects to the projects config path.
func writeProjects(t *testing.T, p Paths, projects ProjectsConfig) {
	t.Helper()
	if err := WriteProjects(p.ProjectsPath, projects); err != nil {
		t.Fatal(err)
	}
}

// projectWith returns a ProjectsConfig with one project of the given name.
func projectWith(name string) ProjectsConfig {
	cfg := DefaultProjects()
	cfg.Projects[name] = ProjectEntry{Name: name}
	return cfg
}

// assertLastCommit checks the most recent recorded commit.
func assertLastCommit(t *testing.T, fake *fakeGit, message string, paths ...string) {
	t.Helper()
	if len(fake.commits) == 0 {
		t.Fatal("no commits recorded")
	}
	last := fake.commits[len(fake.commits)-1]
	if last.message != message {
		t.Errorf("commit message = %q, want %q", last.message, message)
	}
	if !reflect.DeepEqual(last.paths, paths) {
		t.Errorf("commit paths = %v, want %v", last.paths, paths)
	}
}

// assertUserError checks that err is a user error with the exact message.
func assertUserError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error = %T, want *grinderr.User", err)
	}
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestMyConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".grind.json")

	cfg := Default()
	cfg.My = &MyConfig{
		Name:    "Lee",
		Company: "Acme",
		Address: "123 Main St",
		Phone:   "555-1234",
		Email:   "lee@example.com",
		TaxID:   "US-12345",
	}
	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.My == nil {
		t.Fatal("My is nil after round trip")
	}
	if *got.My != *cfg.My {
		t.Errorf("My = %+v, want %+v", *got.My, *cfg.My)
	}
}

func TestProjectEntryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".projects.json")

	cfg := DefaultProjects()
	cfg.Projects["my-blog"] = ProjectEntry{
		Name:    "my-blog",
		Type:    "blog",
		Idea:    "My Blog",
		Billing: BillingEntry{RoundTo: "half-hour", Rate: 250},
		Client: &ClientInfo{
			Contact: "Jane",
			Company: "Acme",
			Address: "456 Oak Ave",
			Phone:   "555-5678",
			Email:   "jane@example.com",
		},
		Repo:     "git@example.com:repo.git",
		Code:     "BLOG-1",
		LongTerm: true,
		Deadline: "2026-12-31",
	}
	if err := WriteProjects(path, cfg); err != nil {
		t.Fatalf("WriteProjects: %v", err)
	}

	got, err := ReadProjects(path)
	if err != nil {
		t.Fatalf("ReadProjects: %v", err)
	}
	entry := got.Projects["my-blog"]
	if entry.Client == nil {
		t.Fatal("Client is nil after round trip")
	}
	if *entry.Client != *cfg.Projects["my-blog"].Client {
		t.Errorf("Client = %+v, want %+v", *entry.Client, *cfg.Projects["my-blog"].Client)
	}
	if entry.Repo != "git@example.com:repo.git" {
		t.Errorf("Repo = %q", entry.Repo)
	}
	if entry.Code != "BLOG-1" {
		t.Errorf("Code = %q", entry.Code)
	}
	if !entry.LongTerm {
		t.Error("LongTerm = false, want true")
	}
	if entry.Deadline != "2026-12-31" {
		t.Errorf("Deadline = %q", entry.Deadline)
	}
}

func TestListFreshWorkspaceShowsBillingDefaults(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	entries, err := svc.List(paths)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "billing.defaultRate", Value: "150"},
		{Key: "billing.roundTo", Value: "quarter-hour"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %v, want %v", entries, want)
	}
}

func TestListConfiguredSortedAndCommaJoined(t *testing.T) {
	paths, fake := newTestPaths(t)
	cfg := Default()
	cfg.ProjectTypes = []string{"blog", "code"}
	cfg.My = &MyConfig{Name: "Lee", Email: "lee@example.com"}
	cfg.Currency = "USD"
	cfg.PaymentTerms = "Net 30"
	cfg.Remote = &RemoteConfig{URL: "git@example.com:repo.git"}
	cfg.DefaultBranch = "main"
	writeConfig(t, paths, cfg)

	svc := NewService(fake)
	entries, err := svc.List(paths)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "billing.defaultRate", Value: "150"},
		{Key: "billing.roundTo", Value: "quarter-hour"},
		{Key: "currency", Value: "USD"},
		{Key: "defaultBranch", Value: "main"},
		{Key: "my.email", Value: "lee@example.com"},
		{Key: "my.name", Value: "Lee"},
		{Key: "paymentTerms", Value: "Net 30"},
		{Key: "projectTypes", Value: "blog,code"},
		{Key: "remote.url", Value: "git@example.com:repo.git"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %v, want %v", entries, want)
	}
}

func TestGet(t *testing.T) {
	paths, fake := newTestPaths(t)
	cfg := Default()
	cfg.My = &MyConfig{Name: "Lee"}
	writeConfig(t, paths, cfg)
	svc := NewService(fake)

	t.Run("existing", func(t *testing.T) {
		got, err := svc.Get(paths, "billing.defaultRate")
		if err != nil {
			t.Fatal(err)
		}
		if got != "150" {
			t.Errorf("value = %q, want 150", got)
		}
	})

	t.Run("nested", func(t *testing.T) {
		got, err := svc.Get(paths, "my.name")
		if err != nil {
			t.Fatal(err)
		}
		if got != "Lee" {
			t.Errorf("value = %q, want Lee", got)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, err := svc.Get(paths, "nope")
		assertUserError(t, err, "Key not found: nope")
	})

	t.Run("valid but unset", func(t *testing.T) {
		_, err := svc.Get(paths, "my.email")
		assertUserError(t, err, "Key not found: my.email")
	})
}

func TestSetRoundTo(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	if err := svc.Set(paths, "billing.roundTo", "half-hour"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Billing.RoundTo != "half-hour" {
		t.Errorf("RoundTo = %q, want half-hour", cfg.Billing.RoundTo)
	}
	assertLastCommit(t, fake, "Set config: billing.roundTo", ".grind.json")

	t.Run("invalid", func(t *testing.T) {
		err := svc.Set(paths, "billing.roundTo", "monthly")
		assertUserError(t, err, "Invalid roundTo value: monthly. Valid options: quarter-hour, half-hour, hour")
	})
}

func TestSetRate(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	if err := svc.Set(paths, "billing.defaultRate", "200"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Billing.DefaultRate != 200 {
		t.Errorf("DefaultRate = %v, want 200", cfg.Billing.DefaultRate)
	}

	t.Run("not a number", func(t *testing.T) {
		err := svc.Set(paths, "billing.defaultRate", "abc")
		assertUserError(t, err, "Invalid rate: abc. Must be a positive number.")
	})

	t.Run("not positive", func(t *testing.T) {
		err := svc.Set(paths, "billing.defaultRate", "-5")
		assertUserError(t, err, "Invalid rate: -5. Must be a positive number.")
	})
}

func TestSetProjectTypes(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	if err := svc.Set(paths, "projectTypes", "blog, code"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.ProjectTypes, []string{"blog", "code"}) {
		t.Errorf("ProjectTypes = %v, want [blog code]", cfg.ProjectTypes)
	}

	t.Run("empty", func(t *testing.T) {
		err := svc.Set(paths, "projectTypes", "")
		assertUserError(t, err, "Invalid projectTypes: . Must be a comma-separated list of types.")
	})

	t.Run("empty element", func(t *testing.T) {
		err := svc.Set(paths, "projectTypes", "blog,,code")
		assertUserError(t, err, "Invalid projectTypes: blog,,code. Must be a comma-separated list of types.")
	})
}

func TestSetMyFields(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	for _, tt := range []struct {
		key   string
		value string
	}{
		{"my.name", "Lee"},
		{"my.company", "Acme"},
		{"my.address", "123 Main St"},
		{"my.phone", "555-1234"},
		{"my.email", "lee@example.com"},
		{"my.taxId", "US-12345"},
	} {
		if err := svc.Set(paths, tt.key, tt.value); err != nil {
			t.Fatalf("Set(%s): %v", tt.key, err)
		}
	}

	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.My == nil {
		t.Fatal("My is nil after my.* sets")
	}
	if cfg.My.Name != "Lee" || cfg.My.Company != "Acme" || cfg.My.Address != "123 Main St" ||
		cfg.My.Phone != "555-1234" || cfg.My.Email != "lee@example.com" || cfg.My.TaxID != "US-12345" {
		t.Errorf("My = %+v", *cfg.My)
	}
}

func TestSetCurrencyAndPaymentTerms(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	if err := svc.Set(paths, "currency", "USD"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Set(paths, "paymentTerms", "Net 30"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", cfg.Currency)
	}
	if cfg.PaymentTerms != "Net 30" {
		t.Errorf("PaymentTerms = %q, want Net 30", cfg.PaymentTerms)
	}
}

func TestSetRemoteURLSyncsOrigin(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	if err := svc.Set(paths, "remote.url", "git@example.com:repo.git"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote == nil || cfg.Remote.URL != "git@example.com:repo.git" {
		t.Errorf("Remote = %+v, want URL git@example.com:repo.git", cfg.Remote)
	}
	if len(fake.setRemoteURLs) != 1 || fake.setRemoteURLs[0] != "git@example.com:repo.git" {
		t.Errorf("SetRemoteURL calls = %v, want [git@example.com:repo.git]", fake.setRemoteURLs)
	}
	assertLastCommit(t, fake, "Set config: remote.url", ".grind.json")

	t.Run("empty rejected", func(t *testing.T) {
		err := svc.Set(paths, "remote.url", "")
		assertUserError(t, err, "Invalid remote.url: must not be empty.")
		// The empty set must not have written anything or synced origin.
		if len(fake.setRemoteURLs) != 1 {
			t.Errorf("SetRemoteURL calls = %v, want still 1", fake.setRemoteURLs)
		}
		if len(fake.commits) != 1 {
			t.Errorf("commits = %v, want still 1", fake.commits)
		}
	})
}

func TestSetMissingConfigWritesDefaultsPlusChange(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	if err := svc.Set(paths, "billing.defaultRate", "200"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Read(paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Billing.DefaultRate != 200 {
		t.Errorf("DefaultRate = %v, want 200", cfg.Billing.DefaultRate)
	}
	// The defaults must be present alongside the change.
	if cfg.Billing.RoundTo != "quarter-hour" {
		t.Errorf("RoundTo = %q, want default quarter-hour", cfg.Billing.RoundTo)
	}
}

func TestSetInvalidKey(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	err := svc.Set(paths, "bogus", "value")
	want := "Invalid key for workspace config: bogus. Valid keys: " + strings.Join(workspaceKeys, ", ")
	assertUserError(t, err, want)
}

func TestHasProject(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	ok, err := svc.HasProject(paths, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("HasProject(my-blog) = false, want true")
	}

	ok, err = svc.HasProject(paths, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("HasProject(nope) = true, want false")
	}

	t.Run("missing projects file", func(t *testing.T) {
		paths2, fake2 := newTestPaths(t)
		ok, err := NewService(fake2).HasProject(paths2, "anything")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("HasProject on missing file = true, want false")
		}
	})
}

func TestProjectList(t *testing.T) {
	paths, fake := newTestPaths(t)
	projects := projectWith("my-blog")
	projects.Projects["my-blog"] = ProjectEntry{
		Name:    "my-blog",
		Type:    "blog",
		Billing: BillingEntry{RoundTo: "half-hour", Rate: 250},
		Client:  &ClientInfo{Contact: "Jane", Email: "jane@example.com"},
		Repo:    "git@example.com:repo.git",
		Code:    "BLOG-1",
		Deadline: "2026-12-31",
	}
	writeProjects(t, paths, projects)
	svc := NewService(fake)

	entries, err := svc.ProjectList(paths, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "billing.rate", Value: "250"},
		{Key: "billing.roundTo", Value: "half-hour"},
		{Key: "client.contact", Value: "Jane"},
		{Key: "client.email", Value: "jane@example.com"},
		{Key: "code", Value: "BLOG-1"},
		{Key: "deadline", Value: "2026-12-31"},
		{Key: "longTerm", Value: "false"},
		{Key: "repo", Value: "git@example.com:repo.git"},
		{Key: "status", Value: "active"},
		{Key: "type", Value: "blog"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %v, want %v", entries, want)
	}
}

func TestProjectListShowsLongTermEffectiveValue(t *testing.T) {
	paths, fake := newTestPaths(t)
	projects := projectWith("my-blog")
	projects.Projects["my-blog"] = ProjectEntry{Name: "my-blog", LongTerm: true}
	writeProjects(t, paths, projects)
	svc := NewService(fake)

	entries, err := svc.ProjectList(paths, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Key == "longTerm" && e.Value != "true" {
			t.Errorf("longTerm = %q, want true", e.Value)
		}
	}
}

func TestProjectListShowsStatusEffectiveValue(t *testing.T) {
	paths, fake := newTestPaths(t)
	projects := projectWith("my-blog")
	projects.Projects["my-blog"] = ProjectEntry{Name: "my-blog", Status: "published"}
	writeProjects(t, paths, projects)
	svc := NewService(fake)

	entries, err := svc.ProjectList(paths, "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Key == "status" && e.Value != "published" {
			t.Errorf("status = %q, want published", e.Value)
		}
	}
}

func TestProjectSetRefusesStatus(t *testing.T) {
	paths, fake := newTestPaths(t)
	projects := projectWith("my-blog")
	projects.Projects["my-blog"] = ProjectEntry{Name: "my-blog"}
	writeProjects(t, paths, projects)
	svc := NewService(fake)

	err := svc.ProjectSet(paths, "my-blog", "status", "published")
	if err == nil {
		t.Fatal("ProjectSet(status) = nil error, want refusal")
	}
	var user *grinderr.User
	if !errors.As(err, &user) {
		t.Fatalf("error = %T, want *grinderr.User", err)
	}
	if !strings.Contains(err.Error(), "Invalid key for project config: status") {
		t.Errorf("error = %q, want it to mention the invalid status key", err.Error())
	}
}

func TestProjectGet(t *testing.T) {
	paths, fake := newTestPaths(t)
	projects := projectWith("my-blog")
	projects.Projects["my-blog"] = ProjectEntry{Name: "my-blog", Type: "blog"}
	writeProjects(t, paths, projects)
	svc := NewService(fake)

	got, err := svc.ProjectGet(paths, "my-blog", "type")
	if err != nil {
		t.Fatal(err)
	}
	if got != "blog" {
		t.Errorf("value = %q, want blog", got)
	}

	t.Run("missing key", func(t *testing.T) {
		_, err := svc.ProjectGet(paths, "my-blog", "nope")
		assertUserError(t, err, "Key not found: nope")
	})

	t.Run("unknown project", func(t *testing.T) {
		_, err := svc.ProjectGet(paths, "nope", "type")
		assertUserError(t, err, "Project 'nope' does not exist.")
	})
}

func TestProjectSetTypeValidatedAgainstEffectiveTypes(t *testing.T) {
	paths, fake := newTestPaths(t)
	cfg := Default()
	cfg.ProjectTypes = []string{"blog", "code"}
	writeConfig(t, paths, cfg)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	if err := svc.ProjectSet(paths, "my-blog", "type", "code"); err != nil {
		t.Fatal(err)
	}
	projects, err := ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	if projects.Projects["my-blog"].Type != "code" {
		t.Errorf("Type = %q, want code", projects.Projects["my-blog"].Type)
	}
	assertLastCommit(t, fake, "Set config: my-blog type", ".projects.json")

	t.Run("invalid type", func(t *testing.T) {
		err := svc.ProjectSet(paths, "my-blog", "type", "nonsense")
		assertUserError(t, err, "Invalid type: nonsense. Valid types: blog, code")
	})
}

func TestProjectSetBilling(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	if err := svc.ProjectSet(paths, "my-blog", "billing.roundTo", "hour"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProjectSet(paths, "my-blog", "billing.rate", "250"); err != nil {
		t.Fatal(err)
	}

	projects, err := ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := projects.Projects["my-blog"]
	if entry.Billing.RoundTo != "hour" {
		t.Errorf("RoundTo = %q, want hour", entry.Billing.RoundTo)
	}
	if entry.Billing.Rate != 250 {
		t.Errorf("Rate = %v, want 250", entry.Billing.Rate)
	}

	t.Run("invalid roundTo", func(t *testing.T) {
		err := svc.ProjectSet(paths, "my-blog", "billing.roundTo", "monthly")
		assertUserError(t, err, "Invalid roundTo value: monthly. Valid options: quarter-hour, half-hour, hour")
	})

	t.Run("invalid rate", func(t *testing.T) {
		err := svc.ProjectSet(paths, "my-blog", "billing.rate", "free")
		assertUserError(t, err, "Invalid rate: free. Must be a positive number.")
	})
}

func TestProjectSetClientFields(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	for _, tt := range []struct {
		key   string
		value string
	}{
		{"client.contact", "Jane"},
		{"client.company", "Acme"},
		{"client.address", "456 Oak Ave"},
		{"client.phone", "555-5678"},
		{"client.email", "jane@example.com"},
	} {
		if err := svc.ProjectSet(paths, "my-blog", tt.key, tt.value); err != nil {
			t.Fatalf("ProjectSet(%s): %v", tt.key, err)
		}
	}

	projects, err := ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	client := projects.Projects["my-blog"].Client
	if client == nil {
		t.Fatal("Client is nil after client.* sets")
	}
	if client.Contact != "Jane" || client.Company != "Acme" || client.Address != "456 Oak Ave" ||
		client.Phone != "555-5678" || client.Email != "jane@example.com" {
		t.Errorf("Client = %+v", *client)
	}
}

func TestProjectSetRepoAndCode(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	if err := svc.ProjectSet(paths, "my-blog", "repo", "git@example.com:repo.git"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProjectSet(paths, "my-blog", "code", "BLOG-1"); err != nil {
		t.Fatal(err)
	}

	projects, err := ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := projects.Projects["my-blog"]
	if entry.Repo != "git@example.com:repo.git" {
		t.Errorf("Repo = %q", entry.Repo)
	}
	if entry.Code != "BLOG-1" {
		t.Errorf("Code = %q", entry.Code)
	}
}

func TestProjectSetLongTerm(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	if err := svc.ProjectSet(paths, "my-blog", "longTerm", "true"); err != nil {
		t.Fatal(err)
	}
	projects, err := ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !projects.Projects["my-blog"].LongTerm {
		t.Error("LongTerm = false, want true")
	}

	if err := svc.ProjectSet(paths, "my-blog", "longTerm", "false"); err != nil {
		t.Fatal(err)
	}
	projects, err = ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	if projects.Projects["my-blog"].LongTerm {
		t.Error("LongTerm = true, want false")
	}

	t.Run("invalid", func(t *testing.T) {
		err := svc.ProjectSet(paths, "my-blog", "longTerm", "yes")
		assertUserError(t, err, "Invalid longTerm value: yes. Must be true or false.")
	})
}

func TestProjectSetDeadline(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	if err := svc.ProjectSet(paths, "my-blog", "deadline", "2026-12-31"); err != nil {
		t.Fatal(err)
	}
	projects, err := ReadProjects(paths.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	if projects.Projects["my-blog"].Deadline != "2026-12-31" {
		t.Errorf("Deadline = %q, want 2026-12-31", projects.Projects["my-blog"].Deadline)
	}

	t.Run("impossible calendar date", func(t *testing.T) {
		err := svc.ProjectSet(paths, "my-blog", "deadline", "2026-02-30")
		assertUserError(t, err, "Invalid deadline: 2026-02-30. Expected format: YYYY-MM-DD.")
	})

	t.Run("wrong format", func(t *testing.T) {
		err := svc.ProjectSet(paths, "my-blog", "deadline", "12/31/2026")
		assertUserError(t, err, "Invalid deadline: 12/31/2026. Expected format: YYYY-MM-DD.")
	})
}

func TestProjectSetUnknownProject(t *testing.T) {
	paths, fake := newTestPaths(t)
	svc := NewService(fake)

	err := svc.ProjectSet(paths, "nope", "type", "blog")
	assertUserError(t, err, "Project 'nope' does not exist.")
}

func TestProjectSetInvalidKey(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	err := svc.ProjectSet(paths, "my-blog", "bogus", "value")
	want := "Invalid key for project config: bogus. Valid keys: " + strings.Join(projectKeys, ", ")
	assertUserError(t, err, want)
}

func TestProjectSetCommitMessageAndStagedFile(t *testing.T) {
	paths, fake := newTestPaths(t)
	writeProjects(t, paths, projectWith("my-blog"))
	svc := NewService(fake)

	if err := svc.ProjectSet(paths, "my-blog", "code", "BLOG-1"); err != nil {
		t.Fatal(err)
	}
	assertLastCommit(t, fake, "Set config: my-blog code", ".projects.json")
}