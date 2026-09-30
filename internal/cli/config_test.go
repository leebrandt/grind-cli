package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/config"
)

func TestConfigCommandListsWorkspace(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "config")
	if err != nil {
		t.Fatal(err)
	}
	want := "billing.defaultRate = 150\nbilling.roundTo = quarter-hour\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestConfigCommandGet(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "config", "billing.defaultRate")
	if err != nil {
		t.Fatal(err)
	}
	if out != "150\n" {
		t.Errorf("output = %q, want %q", out, "150\n")
	}
}

func TestConfigCommandSet(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := execute(t, fake, "config", "billing.defaultRate", "200")
	if err != nil {
		t.Fatal(err)
	}
	if out != "billing.defaultRate = 200\n" {
		t.Errorf("output = %q, want %q", out, "billing.defaultRate = 200\n")
	}

	// The file must be updated on disk.
	cfg, err := config.Read(filepath.Join(".main", ".grind.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Billing.DefaultRate != 200 {
		t.Errorf("DefaultRate = %v, want 200", cfg.Billing.DefaultRate)
	}

	// The last commit must be the set-config commit staging only
	// .grind.json.
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Set config: billing.defaultRate" {
		t.Errorf("commit message = %q", last.message)
	}
	if len(last.paths) != 1 || last.paths[0] != ".grind.json" {
		t.Errorf("commit paths = %v", last.paths)
	}
}

func TestConfigCommandProjectScope(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	// List project config.
	out, err := execute(t, fake, "config", "my-blog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "type = blog") {
		t.Errorf("list output = %q, want type = blog", out)
	}
	if !strings.Contains(out, "billing.rate = 150") {
		t.Errorf("list output = %q, want billing.rate = 150", out)
	}

	// Set a project key.
	out, err = execute(t, fake, "config", "my-blog", "billing.rate", "250")
	if err != nil {
		t.Fatal(err)
	}
	if out != "billing.rate = 250\n" {
		t.Errorf("set output = %q", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if projects.Projects["my-blog"].Billing.Rate != 250 {
		t.Errorf("Rate = %v, want 250", projects.Projects["my-blog"].Billing.Rate)
	}

	// The last commit must be the project set-config commit.
	last := fake.commits[len(fake.commits)-1]
	if last.message != "Set config: my-blog billing.rate" {
		t.Errorf("commit message = %q", last.message)
	}
	if len(last.paths) != 1 || last.paths[0] != ".projects.json" {
		t.Errorf("commit paths = %v", last.paths)
	}

	// Get a project key.
	out, err = execute(t, fake, "config", "my-blog", "billing.rate")
	if err != nil {
		t.Fatal(err)
	}
	if out != "250\n" {
		t.Errorf("get output = %q, want %q", out, "250\n")
	}
}

func TestConfigCommandUnknownKey(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "config", "bogus", "value")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid key for workspace config: bogus.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestConfigCommandInvalidValue(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "config", "billing.roundTo", "monthly")
	if err == nil {
		t.Fatal("expected error")
	}
	want := "Invalid roundTo value: monthly. Valid options: quarter-hour, half-hour, hour"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestConfigCommandNotInWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	_, err = execute(t, &fakeGit{}, "config")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Not in a grind workspace.") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestConfigCommandTooManyArguments(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	// Three args in workspace scope: the first is not a project, so there
	// is no scope that can use three values.
	_, err := execute(t, fake, "config", "a", "b", "c")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Too many arguments") {
		t.Errorf("error = %q", err.Error())
	}
}
