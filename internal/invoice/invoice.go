// Package invoice turns a project's unbilled work sessions into a
// markdown invoice and marks those sessions invoiced, so the next invoice
// covers only new work.
//
// The package is pure with respect to the workspace: it computes an
// Invoice value from config structs and renders it to a string. That split
// is the point — the billing math (which sessions count, how days group,
// what an amount is) lives in plain functions that can be tested without a
// workspace on disk, and the one mutating method, Generate, is a short
// sequence over them.
//
// Two decisions shape everything here:
//   - Amounts come from Session.Rounded, never Session.Duration. Rounded is
//     frozen when the session ends, so a later change to billing.roundTo
//     cannot retroactively alter an issued invoice.
//   - All dates are LOCAL. Sessions are stored in UTC; grouping converts
//     each start into the user's local day before formatting it. This
//     deliberately avoids v1's UTC due-date bugs.
package invoice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/workspace"
)

// dateLayout is a local calendar date, the format every date on an invoice
// is written in.
const dateLayout = "2006-01-02"

// idLayout is the invoice identifier, which is also a directory name. The
// dashes replace colons so the ID is a legal path component everywhere and
// needs no escaping. The timestamp is unique per second, so it is the
// invoice number — no counter to keep in sync.
const idLayout = "20060102T15-04-05"

// invoiceFilename is the one file an invoice directory holds.
const invoiceFilename = "invoice.md"

// maxDescription caps the project description so a long idea cannot stretch
// the PROJECT section of the invoice.
const maxDescription = 100

// netTermsRe matches the "Net N" payment terms, case-insensitively and with
// surrounding whitespace trimmed.
var netTermsRe = regexp.MustCompile(`^net\s+(\d+)$`)

// Symbol is the currency mark on every amount. grind bills in USD and only
// USD — there is no currency setting, deliberately, because a setting that
// silently did nothing would be worse than no setting at all. Supporting
// another currency means threading a code through config, this package, and
// `show`, which is a deliberate decision, not a default.
const Symbol = "$"

// Service generates invoices. It needs git because generating an invoice
// marks sessions invoiced and commits — the same reason projects.Service
// carries a Git.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// Invoice is the computed, renderable invoice. Everything the markdown
// needs is already resolved here, so rendering is a pure function of this
// value — and a second writer (a PDF, say) can consume the same struct.
type Invoice struct {
	ProjectName  string
	Description  string  // first line of the project idea, capped at 100 chars
	From         string  // "FROM" block from .grind.json my.*
	To           string  // "TO" block from the project's client.*
	Rate         float64 // snapshotted at generation time
	PaymentTerms string  // verbatim from .grind.json, default "Net 30"
	InvoiceDate  string  // local YYYY-MM-DD
	DueDate      string  // local YYYY-MM-DD
	ID           string  // local 20060102T15-04-05
	Lines        []DayLine
	TotalSeconds int64
	TotalHours   float64
	Subtotal     float64
	// SessionCount is how many sessions the invoice bills. The day-line
	// count alone would hide how many separate sittings produced them,
	// which is what the user reads first.
	SessionCount int
}

// DayLine is one row of the time breakdown: all billable sessions that
// started on the same local date, summed.
type DayLine struct {
	Date    string
	Seconds int64
	Hours   float64
	Amount  float64
}

// Summary is a project's billing totals: all the work on it, split into what
// has been invoiced and what has not. `show --billing` prints it, so the
// answer to "what is left to bill?" is one flag away.
type Summary struct {
	SessionCount    int
	TotalSeconds    int64
	BilledSeconds   int64
	UnbilledSeconds int64
	TotalAmount     float64
	BilledAmount    float64
	UnbilledAmount  float64
}

// Summarize computes a project's billing totals from its sessions.
//
// The split follows the invoiced flag, and the amounts come from Rounded —
// the same source the invoice bills. An active session has Rounded 0 (it is
// written at end time), so it adds nothing to either side of the split
// rather than needing a special case here.
func Summarize(entry config.ProjectEntry) Summary {
	var s Summary
	for _, sess := range entry.Sessions {
		s.SessionCount++
		hours := float64(sess.Rounded) / 3600
		amount := hours * entry.Billing.Rate

		s.TotalSeconds += sess.Rounded
		s.TotalAmount += amount
		if sess.Invoiced {
			s.BilledSeconds += sess.Rounded
			s.BilledAmount += amount
		} else {
			s.UnbilledSeconds += sess.Rounded
			s.UnbilledAmount += amount
		}
	}
	return s
}

// DaysFromPaymentTerms maps a payment-terms string to the number of days
// until an invoice is due: "Net 30" → 30, "net 7" → 7, "Due on receipt" → 0,
// anything unrecognized (including "") → 30.
//
// The 30-day fallback is v1's behavior and the safe default: an
// unrecognized term must never produce a due date in the past. Honoring
// the configured terms is what fixes v1 bug #4, where "Net 7" still got a
// 30-day due date.
func DaysFromPaymentTerms(terms string) int {
	trimmed := strings.TrimSpace(terms)
	if m := netTermsRe.FindStringSubmatch(strings.ToLower(trimmed)); m != nil {
		// The regexp guarantees digits, so ParseInt cannot fail.
		days, _ := strconv.Atoi(m[1])
		return days
	}
	if strings.Contains(strings.ToLower(trimmed), "due on receipt") {
		return 0
	}
	return 30
}

// Generate computes the invoice for a project's unbilled sessions and, unless
// dryRun is set, writes it, marks the sessions invoiced, and commits both in
// one commit.
//
// now is the instant every date on the invoice is measured from: the invoice
// date, the due date, and the invoice ID. It is a parameter rather than a
// call to time.Now() inside this package so the package never reads the
// clock — the caller decides what "now" means, which is what makes the date
// rules (local day grouping, payment terms) testable.
func (s *Service) Generate(ws *workspace.Workspace, name string, dryRun bool, now time.Time) (*Invoice, error) {
	cfg, err := readConfig(ws.GrindConfigPath())
	if err != nil {
		return nil, err
	}
	projects, entry, err := loadProject(ws, name)
	if err != nil {
		return nil, err
	}

	// A session is billable when it has ended and has not been invoiced. An
	// active session (End == nil) has Rounded 0: it contributes nothing yet,
	// and "not finished" is not the same as "unbilled work".
	//
	// The positions of the billable sessions are kept, not just the
	// sessions themselves: marking below writes back through these indexes,
	// so the "which sessions did we bill" question has exactly one answer in
	// this function. Re-deriving the rule a second time would be a way for
	// the two to drift apart and mark a session nobody charged for.
	var billableIdx []int
	for i, sess := range entry.Sessions {
		if sess.End != nil && !sess.Invoiced {
			billableIdx = append(billableIdx, i)
		}
	}
	// Nothing to bill is a success, not an error. Running invoice twice in
	// a row is harmless, which makes the verb safe in a script.
	if len(billableIdx) == 0 {
		return nil, nil
	}
	billable := make([]config.Session, 0, len(billableIdx))
	for _, i := range billableIdx {
		billable = append(billable, entry.Sessions[i])
	}

	// The payment terms come from .grind.json; the default keeps a workspace
	// with no config usable.
	terms := cfg.PaymentTerms
	if terms == "" {
		terms = "Net 30"
	}

	// The rate is snapshotted here: changing billing.rate later must not
	// rewrite an issued invoice. The markdown file is the durable record.
	rate := entry.Billing.Rate
	lines := dayLines(billable)
	inv := &Invoice{
		ProjectName:  name,
		Description:  firstLine(entry.Idea),
		From:         fromBlock(cfg.My),
		To:           toBlock(name, entry.Client),
		Rate:         rate,
		PaymentTerms: terms,
		InvoiceDate:  now.Format(dateLayout),
		// AddDate does calendar arithmetic, so a due date 30 days out is
		// 30 days out even across a daylight-saving change.
		DueDate:      now.AddDate(0, 0, DaysFromPaymentTerms(terms)).Format(dateLayout),
		ID:           now.Format(idLayout),
		Lines:        lines,
		SessionCount: len(billable),
	}
	// The per-day amounts and the totals are filled in from the same lines
	// the invoice renders, so the table and the subtotal can never disagree.
	for i := range lines {
		lines[i].Amount = lines[i].Hours * rate
		inv.TotalSeconds += lines[i].Seconds
		inv.Subtotal += lines[i].Amount
	}
	inv.TotalHours = float64(inv.TotalSeconds) / 3600

	if dryRun {
		// A dry run leaves the workspace exactly as it found it: no file,
		// no state change, no commit.
		return inv, nil
	}

	// Everything above only reads and computes, so nothing can be left
	// half-written. Render before writing, then write the file, then the
	// state, then commit both — one commit, so the invoice and the marking
	// can never disagree.
	content := renderMarkdown(inv)

	dir := ws.InvoiceDir(name, inv.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, grinderr.WrapSystem(err, "create invoice directory %s", dir)
	}
	invoicePath := filepath.Join(dir, invoiceFilename)
	if err := os.WriteFile(invoicePath, []byte(content), 0o644); err != nil {
		return nil, grinderr.WrapSystem(err, "write invoice file %s", invoicePath)
	}

	// Mark the billed sessions. The flag is never a deletion: the record
	// stays so .projects.json still shows what was worked and that it was
	// billed, which is the audit trail behind the invoice files.
	for _, i := range billableIdx {
		entry.Sessions[i].Invoiced = true
	}
	projects.Projects[name] = entry
	if err := config.WriteProjects(ws.ProjectsConfigPath(), projects); err != nil {
		return nil, err
	}

	// Paths are relative to .main and use forward slashes, which is what
	// git expects on every platform. Only the two files Generate wrote are
	// staged — never `git add -A`.
	invoiceRelPath := "invoices/" + name + "/" + inv.ID + "/" + invoiceFilename
	if err := s.Git.Commit(ws.MainWorktree, "Invoice "+inv.ID+" for "+name,
		".projects.json", invoiceRelPath); err != nil {
		return nil, err
	}

	return inv, nil
}

// dayLines groups billable sessions by local date, sorted ascending.
//
// The sort is not cosmetic: Go randomizes map iteration, so an unsorted
// grouping would emit a different row order on every run and the committed
// file would churn. Sorting is what makes the output deterministic and
// diffable. Amount is left at zero here — the rate is applied by the
// caller, which is the only place that knows it.
func dayLines(sessions []config.Session) []DayLine {
	secondsByDate := make(map[string]int64)
	for _, sess := range sessions {
		// Sessions are stored in UTC; the breakdown groups by the user's
		// local day, which is the day they actually worked.
		date := sess.Start.Local().Format(dateLayout)
		secondsByDate[date] += sess.Rounded
	}

	lines := make([]DayLine, 0, len(secondsByDate))
	for date, seconds := range secondsByDate {
		lines = append(lines, DayLine{
			Date:    date,
			Seconds: seconds,
			Hours:   float64(seconds) / 3600,
		})
	}
	// The dates are YYYY-MM-DD strings, so a string comparison IS a
	// chronological one — no parsing needed.
	sort.Slice(lines, func(i, j int) bool { return lines[i].Date < lines[j].Date })
	return lines
}

// block renders a FROM/TO address block from its non-empty fields in the
// order given, joined by newlines. An entirely empty block returns hint:
// a blank section tells the client nothing, so the invoice names the exact
// grind config command that fills it in instead.
func block(fields []string, hint string) string {
	set := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			set = append(set, f)
		}
	}
	if len(set) == 0 {
		return hint
	}
	return strings.Join(set, "\n")
}

// fromBlock renders the FROM address from the workspace's my.* config, in
// the order clients expect: company, name, address, phone, email, tax ID.
// A nil my (a config with no "my" block at all) is treated exactly like an
// empty one: nothing to print, so the hint.
func fromBlock(my *config.MyConfig) string {
	if my == nil {
		return block(nil, `(not configured — run grind config my.company "Your Company")`)
	}
	fields := []string{my.Company, my.Name, my.Address, my.Phone, my.Email}
	if my.TaxID != "" {
		fields = append(fields, "Tax ID: "+my.TaxID)
	}
	return block(fields, `(not configured — run grind config my.company "Your Company")`)
}

// toBlock renders the TO address from the project's client.* config, in the
// order company, contact, address, phone, email. The hint names the project
// because the client keys are per-project.
func toBlock(project string, client *config.ClientInfo) string {
	if client == nil {
		return block(nil, fmt.Sprintf(
			`(not configured — run grind config %s client.company "Client Name")`, project))
	}
	fields := []string{client.Company, client.Contact, client.Address, client.Phone, client.Email}
	return block(fields, fmt.Sprintf(
		`(not configured — run grind config %s client.company "Client Name")`, project))
}

// firstLine returns the first line of s, capped at maxDescription runes.
// The idea is usually a single-line title, but a hand-edited one can be a
// paragraph, and a client-facing invoice should not carry the whole thing.
// Runes, not bytes, so a multi-byte character is never cut in half.
func firstLine(s string) string {
	first := s
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	runes := []rune(first)
	if len(runes) > maxDescription {
		first = string(runes[:maxDescription])
	}
	return first
}

// renderMarkdown renders the invoice as the committed .md file. It is a
// pure function of an Invoice value, so the exact bytes a commit contains
// can be asserted in a test without touching the filesystem.
func renderMarkdown(inv *Invoice) string {
	var b strings.Builder

	// The YAML frontmatter makes the invoice machine-readable, the same
	// convention published/<name>.md uses: anything watching the workspace
	// can parse an invoice without scraping the table below it.
	b.WriteString("---\n")
	fmt.Fprintf(&b, "invoiceId: %s\n", inv.ID)
	fmt.Fprintf(&b, "date: %s\n", inv.InvoiceDate)
	fmt.Fprintf(&b, "due: %s\n", inv.DueDate)
	fmt.Fprintf(&b, "project: %s\n", inv.ProjectName)
	fmt.Fprintf(&b, "rate: %s\n", formatNumber(inv.Rate))
	fmt.Fprintf(&b, "subtotal: %.2f\n", inv.Subtotal)
	b.WriteString("---\n")

	// The body follows v1's layout, which clients recognize.
	b.WriteString("\n# INVOICE\n\n")
	fmt.Fprintf(&b, "**Invoice Date**: %s\n", inv.InvoiceDate)
	fmt.Fprintf(&b, "**Invoice ID**: %s\n\n", inv.ID)

	b.WriteString("---\n\n## FROM\n")
	fmt.Fprintf(&b, "%s\n\n", inv.From)

	b.WriteString("## TO\n")
	fmt.Fprintf(&b, "%s\n\n", inv.To)

	b.WriteString("---\n\n## PROJECT\n")
	fmt.Fprintf(&b, "**Name**: %s\n", inv.ProjectName)
	fmt.Fprintf(&b, "**Description**: %s\n\n", inv.Description)

	b.WriteString("---\n\n## TIME BREAKDOWN\n\n")
	b.WriteString("| Date | Hours | Rate | Amount |\n")
	b.WriteString("|------|-------|------|--------|\n")
	for _, line := range inv.Lines {
		fmt.Fprintf(&b, "| %s | %.2f | %s%.2f | %s%.2f |\n",
			line.Date, line.Hours, Symbol, inv.Rate, Symbol, line.Amount)
	}

	fmt.Fprintf(&b, "\n**Subtotal**: %s%.2f\n", Symbol, inv.Subtotal)
	fmt.Fprintf(&b, "**Total**: %s%.2f\n\n", Symbol, inv.Subtotal)

	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "**Payment Terms**: %s\n", inv.PaymentTerms)
	fmt.Fprintf(&b, "**Due Date**: %s\n", inv.DueDate)

	return b.String()
}

// formatNumber renders a number without trailing zeros: 150 → "150",
// 150.5 → "150.5". The frontmatter rate is a number, not an amount, so it
// reads cleanly either way. Precision -1 avoids the scientific notation %g
// would produce for large values.
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// readConfig loads .grind.json, falling back to the defaults when the file
// is missing — a workspace created by an older grind version may not have
// one. Mirrors the same fallback in the projects and config packages.
func readConfig(path string) (config.GrindConfig, error) {
	cfg, err := config.Read(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.Default(), nil
		}
		return config.GrindConfig{}, err
	}
	return cfg, nil
}

// loadProject reads .projects.json and returns the config plus the named
// project entry. A missing file or an unknown name is the same user error
// the rest of grind uses for a project that is not there.
func loadProject(ws *workspace.Workspace, name string) (config.ProjectsConfig, config.ProjectEntry, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config.ProjectsConfig{}, config.ProjectEntry{}, notExist(name)
		}
		return config.ProjectsConfig{}, config.ProjectEntry{}, err
	}
	entry, ok := projects.Projects[name]
	if !ok {
		return config.ProjectsConfig{}, config.ProjectEntry{}, notExist(name)
	}
	return projects, entry, nil
}

// notExist builds the shared "no such project" user error. The wording
// matches projects.loadProject exactly so the user learns one message.
func notExist(name string) error {
	return grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", name))
}
