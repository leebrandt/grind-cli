package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/grinderr"
)

// The wizard tests all drive the flow through a strings.Reader, which is
// also the proof that no terminal is required: a pipe of answers is enough.

func TestNewProjectInteractive(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy-blog\ny\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}

	// The idea list, the prompts, and the confirm summary all appear.
	for _, want := range []string{
		"Select an idea:",
		"0. My Blog",
		"Idea number: ",
		"Project name [my-blog]: ",
		"Project:  my-blog",
		"Branch:   my-blog",
		"Worktree: my-blog/",
		"Type:     —",
		"Idea:     My Blog",
		"Rate:     $150/hr (quarter-hour)",
		"Create project? [Y/n] ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// The success block is shared with the two-argument form and nothing
	// follows it — the ideas are not re-listed.
	if !strings.Contains(out, "Next: work my-blog") {
		t.Errorf("output missing Next hint:\n%s", out)
	}
	if strings.Contains(out, "Remaining ideas:") {
		t.Errorf("output should not re-list ideas:\n%s", out)
	}
	if !strings.HasSuffix(out, "Next: work my-blog\n") {
		t.Errorf("output should end with the Next line:\n%s", out)
	}

	// Creation went through the same transaction as the two-argument form.
	if len(fake.createBranch) != 1 || fake.createBranch[0] != "my-blog" {
		t.Errorf("CreateBranch calls = %v", fake.createBranch)
	}
	commits := fake.commits
	if len(commits) != 5 {
		t.Fatalf("commits = %d, want 5 (init + idea + seed + project + remove)", len(commits))
	}
	if got := commits[2].message; got != "Add idea: My Blog" {
		t.Errorf("seed commit = %q", got)
	}
	if got := commits[3].message; got != "Create project: my-blog" {
		t.Errorf("projects commit = %q", got)
	}
	if !strings.HasPrefix(commits[4].message, "Remove idea ") {
		t.Errorf("remove commit = %q", commits[4].message)
	}

	ideaFile, err := os.ReadFile(filepath.Join("my-blog", ".idea"))
	if err != nil {
		t.Fatalf("read .idea: %v", err)
	}
	if string(ideaFile) != "# My Blog\n" {
		t.Errorf(".idea content = %q, want %q", string(ideaFile), "# My Blog\n")
	}

	// The promoted idea is gone from .main/ideas.
	entries, err := os.ReadDir(filepath.Join(".main", "ideas"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("ideas dir still has %q after promotion", e.Name())
		}
	}
}

func TestNewProjectInteractiveAcceptsSuggestion(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	// The second answer is empty: Enter accepts the suggested name.
	if _, err := executeWithIn(t, fake, strings.NewReader("0\n\ny\n"), "new", "project"); err != nil {
		t.Fatalf("new project: %v", err)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["my-blog"]; !exists {
		t.Error("my-blog was not created from the suggested name")
	}
}

func TestNewProjectInteractiveRepromptsBadIdea(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("abc\n99\n0\nmy-blog\ny\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.Contains(out, `Idea must be a number, got "abc"`) {
		t.Errorf("output missing non-numeric error:\n%s", out)
	}
	if !strings.Contains(out, "Idea #99 not found. Run 'grind list ideas' to see available ideas.") {
		t.Errorf("output missing out-of-range error:\n%s", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["my-blog"]; !exists {
		t.Error("my-blog was not created after re-prompting")
	}
}

func TestNewProjectInteractiveRepromptsBadName(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy blog\nmy-blog\ny\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.Contains(out, "must not contain whitespace") {
		t.Errorf("output missing whitespace error:\n%s", out)
	}
	// The suggestion stays visible on the re-prompt.
	if strings.Count(out, "Project name [my-blog]: ") != 2 {
		t.Errorf("name prompt shown %d times, want 2:\n%s",
			strings.Count(out, "Project name [my-blog]: "), out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["my-blog"]; !exists {
		t.Error("my-blog was not created after re-prompting")
	}
}

func TestNewProjectInteractiveRepromptsExistingName(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	// createProject promotes an idea into my-blog, so add a fresh idea for
	// the wizard to offer.
	createProject(t, fake, "my-blog")
	if _, err := execute(t, fake, "new", "idea", "Other Idea"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy-blog\nother\ny\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.Contains(out, "Project 'my-blog' already exists.") {
		t.Errorf("output missing already-exists error:\n%s", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["other"]; !exists {
		t.Error("other was not created after re-prompting")
	}
}

func TestNewProjectInteractiveRepromptsExistingDirectory(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	// A stale directory with no project entry behind it occupies the
	// worktree path; the wizard must reject the name and ask again.
	if err := os.Mkdir("taken", 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\ntaken\nfresh\ny\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.Contains(out, "directory already exists") {
		t.Errorf("output missing directory-exists error:\n%s", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["fresh"]; !exists {
		t.Error("fresh was not created after re-prompting")
	}
}

func TestNewProjectInteractiveDeclined(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy-blog\nn\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.HasSuffix(out, "Aborted.\n") {
		t.Errorf("output should end with Aborted.:\n%s", out)
	}

	// Nothing was created: no branch, no entry.
	if len(fake.createBranch) != 0 {
		t.Errorf("CreateBranch calls = %v, want none", fake.createBranch)
	}
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["my-blog"]; exists {
		t.Error("my-blog must not be created when the confirm is declined")
	}
}

func TestNewProjectInteractiveEOF(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	// End of input at the name prompt must abort, not accept the
	// suggested name: Ctrl+D is not Enter.
	out, err := executeWithIn(t, fake, strings.NewReader("0\n"), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.HasSuffix(out, "Aborted.\n") {
		t.Errorf("output should end with Aborted.:\n%s", out)
	}
	if len(fake.createBranch) != 0 {
		t.Errorf("CreateBranch calls = %v, want none", fake.createBranch)
	}
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects.Projects["my-blog"]; exists {
		t.Error("EOF must not accept the suggested name")
	}
}

func TestNewProjectInteractiveNoIdeas(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	out, err := executeWithIn(t, fake, strings.NewReader(""), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	want := "No ideas yet. Create one with: grind new idea \"Your idea\"\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if strings.Contains(out, "Idea number:") {
		t.Errorf("no prompt should be shown:\n%s", out)
	}
}

func TestNewProjectInteractiveRejectedOnly(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "Rejected Idea"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, fake, "reject", "idea", "0"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader(""), "new", "project")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	want := "No ideas yet. Create one with: grind new idea \"Your idea\"\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
	if strings.Contains(out, "Rejected Idea") {
		t.Errorf("rejected ideas must not be listed:\n%s", out)
	}
}

func TestNewProjectInteractiveDirtyProjects(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}
	fake.dirtyPaths = map[string]bool{".projects.json": true}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy-blog\ny\n"), "new", "project")
	if err == nil {
		t.Fatal("expected error for a dirty .projects.json")
	}
	if !strings.Contains(err.Error(), "You have uncommitted changes in .projects.json.") {
		t.Errorf("error = %q", err.Error())
	}
	if strings.Contains(out, "Select an idea:") {
		t.Errorf("the idea list must not be printed before the dirty check:\n%s", out)
	}
}

func TestNewProjectInteractiveInvalidType(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy-blog\ny\n"), "new", "project", "-t", "nope")
	if err == nil {
		t.Fatal("expected error for an invalid type")
	}
	if !strings.Contains(err.Error(), "Invalid type: nope.") {
		t.Errorf("error = %q", err.Error())
	}
	if strings.Contains(out, "Select an idea:") {
		t.Errorf("the idea list must not be printed before the type check:\n%s", out)
	}
}

func TestNewProjectInteractiveTypeFlag(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	if _, err := execute(t, fake, "new", "idea", "My Blog"); err != nil {
		t.Fatal(err)
	}

	out, err := executeWithIn(t, fake, strings.NewReader("0\nmy-blog\ny\n"), "new", "project", "-t", "blog")
	if err != nil {
		t.Fatalf("new project: %v", err)
	}
	if !strings.Contains(out, "Type:     blog") {
		t.Errorf("summary missing the type:\n%s", out)
	}
	if strings.Contains(out, "Type:     —") {
		t.Errorf("summary should not render the empty-type dash:\n%s", out)
	}

	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := projects.Projects["my-blog"].Type; got != "blog" {
		t.Errorf("stored Type = %q, want blog", got)
	}
}

func TestNewProjectArgCount(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	for _, args := range [][]string{
		{"new", "project", "my-blog"},
		{"new", "project", "a", "b", "c"},
	} {
		_, err := executeWithIn(t, fake, strings.NewReader(""), args...)
		if err == nil {
			t.Fatalf("%v: expected an argument-count error", args)
		}
		if grinderr.ExitCode(err) != 1 {
			t.Errorf("%v: exit code = %d, want 1", args, grinderr.ExitCode(err))
		}
		want := "new project requires either no arguments (interactive) or <name> <idea-number>"
		if err.Error() != want {
			t.Errorf("%v: error = %q, want %q", args, err.Error(), want)
		}
	}
}

func TestPrompterKeepsBufferedAnswers(t *testing.T) {
	var buf bytes.Buffer
	p := newPrompter(strings.NewReader("a\nb\n"), &buf)

	first, ok, err := p.ask("First: ")
	if err != nil || !ok || first != "a" {
		t.Fatalf("first ask = %q, %v, %v; want a, true, nil", first, ok, err)
	}
	second, ok, err := p.ask("Second: ")
	if err != nil || !ok || second != "b" {
		t.Fatalf("second ask = %q, %v, %v; want b, true, nil", second, ok, err)
	}

	// Both prompts were written, in order.
	if got := buf.String(); got != "First: Second: " {
		t.Errorf("prompt output = %q", got)
	}
}

func TestPrompterAcceptsFinalLineWithoutNewline(t *testing.T) {
	var buf bytes.Buffer
	p := newPrompter(strings.NewReader("a\nb"), &buf)

	first, ok, err := p.ask("First: ")
	if err != nil || !ok || first != "a" {
		t.Fatalf("first ask = %q, %v, %v; want a, true, nil", first, ok, err)
	}
	// A pipe often ends without a trailing newline; that last line is
	// still an answer.
	second, ok, err := p.ask("Second: ")
	if err != nil || !ok || second != "b" {
		t.Fatalf("second ask = %q, %v, %v; want b, true, nil", second, ok, err)
	}
	_, ok, err = p.ask("Third: ")
	if err != nil {
		t.Fatalf("third ask error = %v", err)
	}
	if ok {
		t.Error("third ask ok = true, want false at end of input")
	}
}
