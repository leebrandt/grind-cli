package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leebrandt/grind/internal/config"
)

// readProjectsConfig reads .projects.json from the workspace the test is
// standing in, so a test can assert what was actually persisted.
func readProjectsConfig(t *testing.T) config.ProjectsConfig {
	t.Helper()
	projects, err := config.ReadProjects(filepath.Join(".main", ".projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	return projects
}

// invoiceProject creates the project and backfills one 8h session, so there
// is something to bill. The values are stable regardless of the clock: a
// single session is always one day line, and `save -t 8h` always rounds to
// exactly 8h.
func invoiceProject(t *testing.T, fake *fakeGit, name string) {
	t.Helper()
	createProject(t, fake, name)
	if _, err := execute(t, fake, "save", name, "-t", "8h"); err != nil {
		t.Fatalf("save %s -t 8h: %v", name, err)
	}
}

// invoicePath returns the single generated invoice.md path under
// invoices/<project>/, or "" when there is none. The invoice ID is a
// timestamp, so the test discovers the directory rather than predicting it.
func invoicePath(t *testing.T, project string) string {
	t.Helper()
	dir := filepath.Join(".main", "invoices", project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name(), "invoice.md")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func TestInvoiceCommand(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	invoiceProject(t, fake, "my-blog")
	commitsBefore := len(fake.commits)

	out, err := execute(t, fake, "invoice", "my-blog")
	if err != nil {
		t.Fatalf("invoice my-blog: %v", err)
	}

	// The headline numbers: one 8h session at the default 150/hr.
	for _, want := range []string{
		"Invoiced 1 session(s) totalling 8.00h across 1 day(s).",
		"Subtotal: $1200.00",
		".main/invoices/my-blog/",
		"/invoice.md",
		"Due:      ",
		"(Net 30)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("invoice output = %q, missing %q", out, want)
		}
	}

	// The file exists where the output said it would.
	path := invoicePath(t, "my-blog")
	if path == "" {
		t.Fatal("no invoice.md was written")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The date column is today's date, which moves with the clock, so the
	// row is matched from the hours column onwards.
	for _, want := range []string{"# INVOICE", "**Invoice ID**:", "| 8.00 | $150.00 | $1200.00 |"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("invoice file missing %q:\n%s", want, content)
		}
	}

	// The session is marked invoiced.
	projects := readProjectsConfig(t)
	if !projects.Projects["my-blog"].Sessions[0].Invoiced {
		t.Error("session was not marked invoiced")
	}

	// One commit staging exactly the two paths the invoice touched.
	if len(fake.commits) != commitsBefore+1 {
		t.Fatalf("commits = %d, want %d", len(fake.commits), commitsBefore+1)
	}
	last := fake.commits[len(fake.commits)-1]
	if !strings.HasPrefix(last.message, "Invoice ") || !strings.HasSuffix(last.message, " for my-blog") {
		t.Errorf("commit message = %q", last.message)
	}
	if len(last.paths) != 2 {
		t.Fatalf("commit paths = %v, want 2", last.paths)
	}
	if last.paths[0] != ".projects.json" {
		t.Errorf("commit path 0 = %q, want .projects.json", last.paths[0])
	}
	if !strings.HasPrefix(last.paths[1], "invoices/my-blog/") || !strings.HasSuffix(last.paths[1], "/invoice.md") {
		t.Errorf("commit path 1 = %q", last.paths[1])
	}
}

func TestInvoiceCommandTwiceIsHarmless(t *testing.T) {
	// The second run has nothing to bill: exit 0 with a clear message, no
	// new commit. That is what makes the verb safe in a script.
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	invoiceProject(t, fake, "my-blog")

	if _, err := execute(t, fake, "invoice", "my-blog"); err != nil {
		t.Fatalf("invoice my-blog: %v", err)
	}
	commitsBefore := len(fake.commits)

	out, err := execute(t, fake, "invoice", "my-blog")
	if err != nil {
		t.Fatalf("invoice my-blog (second): %v", err)
	}
	want := "No unbilled sessions for 'my-blog'.\n"
	if out != want {
		t.Errorf("second run output = %q, want %q", out, want)
	}
	if len(fake.commits) != commitsBefore {
		t.Errorf("commits = %d, want %d (nothing to bill must not commit)", len(fake.commits), commitsBefore)
	}
}

func TestInvoiceCommandNoSessions(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	createProject(t, fake, "my-blog")

	out, err := execute(t, fake, "invoice", "my-blog")
	if err != nil {
		t.Fatalf("invoice my-blog: %v", err)
	}
	if out != "No unbilled sessions for 'my-blog'.\n" {
		t.Errorf("output = %q", out)
	}
	if invoicePath(t, "my-blog") != "" {
		t.Error("an invoice was written with nothing to bill")
	}
}

func TestInvoiceCommandDryRun(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	invoiceProject(t, fake, "my-blog")
	commitsBefore := len(fake.commits)

	out, err := execute(t, fake, "invoice", "my-blog", "--dry-run")
	if err != nil {
		t.Fatalf("invoice my-blog --dry-run: %v", err)
	}
	for _, want := range []string{
		"Would invoice 1 session(s) totalling 8.00h across 1 day(s).",
		"Subtotal: $1200.00",
		"(Dry run — nothing was written. Re-run without --dry-run to issue it.)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output = %q, missing %q", out, want)
		}
	}

	// Nothing was written, marked, or committed.
	if invoicePath(t, "my-blog") != "" {
		t.Error("dry run wrote an invoice file")
	}
	if len(fake.commits) != commitsBefore {
		t.Errorf("commits = %d, want %d (dry run must not commit)", len(fake.commits), commitsBefore)
	}
	projects := readProjectsConfig(t)
	if projects.Projects["my-blog"].Sessions[0].Invoiced {
		t.Error("dry run marked a session invoiced")
	}
}

func TestInvoiceCommandUnknownProject(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "invoice", "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "Project 'nope' does not exist." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestInvoiceCommandNotInWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)

	_, err = execute(t, &fakeGit{}, "invoice", "my-blog")
	if err == nil {
		t.Fatal("expected error outside a workspace")
	}
	if err.Error() != "Not in a grind workspace." {
		t.Errorf("error = %q", err.Error())
	}
}

func TestShowBilling(t *testing.T) {
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	invoiceProject(t, fake, "my-blog")

	out, err := execute(t, fake, "show", "my-blog", "--billing")
	if err != nil {
		t.Fatalf("show my-blog --billing: %v", err)
	}
	// The whole point of the flag: what is left to bill. 8h at 150/hr.
	// Amounts carry the currency symbol so they read exactly as they do on
	// the invoice file.
	for _, want := range []string{
		"Sessions:  1\n",
		"Total:     8.00h ($1200.00)\n",
		"Billed:    0.00h ($0.00)\n",
		"Unbilled:  8.00h ($1200.00)\n",
		"Rate:      $150.00/hr (quarter-hour)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show --billing output = %q, missing %q", out, want)
		}
	}

	// After invoicing, the split moves to the billed side.
	if _, err := execute(t, fake, "invoice", "my-blog"); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	out, err = execute(t, fake, "show", "my-blog", "-b")
	if err != nil {
		t.Fatalf("show my-blog -b: %v", err)
	}
	for _, want := range []string{
		"Billed:    8.00h ($1200.00)\n",
		"Unbilled:  0.00h ($0.00)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show --billing after invoicing = %q, missing %q", out, want)
		}
	}
}

func TestCurrencyIsNotConfigurable(t *testing.T) {
	// grind bills in USD only. `config currency` must say no rather than
	// accept a value that would not change a single amount — the whole
	// reason the key was removed instead of being left inert.
	fake, cleanup := runInWorkspace(t)
	defer cleanup()

	_, err := execute(t, fake, "config", "currency", "EUR")
	if err == nil {
		t.Fatal("config currency EUR succeeded, want an invalid-key error")
	}
	if !strings.Contains(err.Error(), "Invalid key for workspace config: currency") {
		t.Errorf("error = %q", err.Error())
	}

	// The key must not even be listed, so a user reading `grind config`
	// learns the currency is not a thing to configure.
	out, err := execute(t, fake, "config")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "currency") {
		t.Errorf("config list mentions currency:\n%s", out)
	}
}

func TestInvoiceIgnoresStaleV1CurrencyKey(t *testing.T) {
	// A v1 workspace has "currency" in .grind.json. It is not a known field
	// any more, so it is dropped on read and the invoice still bills in USD
	// rather than failing or honoring the stale code.
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	if _, err := execute(t, fake, "config", "my.company", "ACME"); err != nil {
		t.Fatal(err)
	}
	invoiceProject(t, fake, "my-blog")

	// Splice the v1 key straight into the file, behind grind's back.
	path := filepath.Join(".main", ".grind.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), `"billing"`, `"currency":"EUR",`+"\n  "+`"billing"`, 1)
	if patched == string(raw) {
		t.Fatal("could not splice the currency key into .grind.json")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := execute(t, fake, "invoice", "my-blog"); err != nil {
		t.Fatalf("invoice with a stale currency key: %v", err)
	}
	invoice, err := os.ReadFile(invoicePath(t, "my-blog"))
	if err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if strings.Contains(string(invoice), "EUR") {
		t.Errorf("invoice honored the stale currency key:\n%s", invoice)
	}
	if !strings.Contains(string(invoice), "$1200.00") {
		t.Errorf("invoice did not bill in USD:\n%s", invoice)
	}
}

func TestShowWithoutBillingIsUnchanged(t *testing.T) {
	// Without the flag, show prints exactly what it printed before this
	// slice: no session totals, and the rate without trailing zeros.
	fake, cleanup := runInWorkspace(t)
	defer cleanup()
	invoiceProject(t, fake, "my-blog")

	out, err := execute(t, fake, "show", "my-blog")
	if err != nil {
		t.Fatalf("show my-blog: %v", err)
	}
	if !strings.Contains(out, "Rate:    $150/hr (quarter-hour)") {
		t.Errorf("output = %q", out)
	}
	for _, unwanted := range []string{"Billed", "Unbilled", "Sessions:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("plain show leaked billing line %q: %q", unwanted, out)
		}
	}
}
