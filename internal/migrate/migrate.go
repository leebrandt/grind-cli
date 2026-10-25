// Package migrate converts a v1 grind workspace into the v2 layout in place.
//
// The package is split into two halves, and the split is the design:
//
//   - Plan reads the v1 workspace and computes everything the migration will
//     do, without writing a byte. `--dry-run` is "Plan and stop", the mapping
//     table and the createdAt chain are testable without arranging a
//     filesystem, and the overwhelming majority of failures (unparseable
//     config, a name that disagrees with its directory, a negative session
//     duration) abort before anything has changed.
//   - Apply executes a plan and commits the result in one commit.
//
// One step genuinely cannot be made atomic: `git worktree move` renames the
// main worktree from grind/ to .main/ and cannot be rolled back by writing a
// file. The migration is therefore RESUMABLE by definition — the v1 main
// worktree is "grind/ when it exists, otherwise .main/", so a run interrupted
// after the move finds the v1 files at their new path and finishes the job.
// An interrupted migration is a second invocation, not a manual repair.
package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/clock"
	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/ideas"
	"github.com/leebrandt/grind/internal/workspace"
)

// CommitMessage is the message of the single commit a migration makes.
// Exported because the CLI prints it back to the user, and a test asserts it.
const CommitMessage = "Migrate workspace from grind v1"

// firstTaskID is where the renumbered task block starts. v2's convention is
// that a project's first task is #100, so a migrated workspace hands out #100
// first and its IDs never collide with one a later `new task` issues.
const firstTaskID = 100

// layoutDirs are the directories v2 expects under the main worktree. ideas/
// and journal/ are not listed: v1 made them, and they hold files that move
// with the worktree for free.
var layoutDirs = []string{"projects", "invoices", "published"}

// InvoiceMove is one invoice directory relocating from v1's place on the
// project's state directory to v2's invoices/<project>/<id>/.
type InvoiceMove struct {
	Project string
	From    string
	To      string
}

// Plan is everything a migration is going to do, computed without touching the
// filesystem. It is what --dry-run prints and what Apply executes.
type Plan struct {
	Root string // workspace root
	// fromMain is the v1 main worktree: <root>/grind normally, <root>/.main
	// when a previous run already moved it. Unexported because it is an
	// implementation detail of resuming; use FromMain to read it.
	fromMain string
	// now is the instant the plan was computed. It is the last resort for
	// createdAt, and taking it once keeps every project in one plan dated
	// from the same moment.
	now time.Time

	MoveMain bool // true when the worktree still needs moving
	// Dirty records that the v1 main worktree had uncommitted changes before
	// the migration. v1's config and journal commands never committed, so this
	// is normal; the CLI tells the user to run `grind save` afterwards instead
	// of sweeping those files into the migration commit.
	Dirty      bool
	Config     config.GrindConfig
	Projects   config.ProjectsConfig
	Invoices   []InvoiceMove
	RemoveDirs []string // v1 project dirs to delete after consolidation
	CreateDirs []string // layout dirs v2 expects that v1 never made
	Warnings   []string // human-readable, already formatted
}

// FromMain returns the v1 main worktree the plan reads from.
func (p *Plan) FromMain() string { return p.fromMain }

// NewPlan reads a v1 workspace and produces the migration plan.
// It is pure with respect to the workspace: no writes, no renames.
//
// The constructor is NewPlan rather than Plan because Go does not allow a type
// and a function to share a name in one package, and the type is the half
// callers read (`p, err := migrate.NewPlan(...)`).
func NewPlan(root string, g git.Git) (*Plan, error) {
	return NewPlanAt(root, g, clock.Real{})
}

// NewPlanAt is NewPlan with an injectable clock. Production uses NewPlan; tests
// use this one to pin the instant that the createdAt chain falls back to.
func NewPlanAt(root string, g git.Git, clk clock.Clock) (*Plan, error) {
	ws, err := workspace.Find(root)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, grinderr.NewUser("Not in a grind workspace.")
	}

	// .projects.json is the v2 marker. v1 never wrote one, so finding it means
	// this workspace has already been migrated — or was born v2. This check
	// comes before locating the v1 worktree, because "already migrated" is the
	// more useful answer when both directories somehow exist.
	if exists(ws.ProjectsConfigPath()) {
		return nil, grinderr.NewUser("This workspace is already a v2 workspace.")
	}

	fromMain, moveMain, err := locateV1Main(ws)
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Root:     ws.Root,
		fromMain: fromMain,
		now:      clk.Now(),
		MoveMain: moveMain,
		Projects: config.DefaultProjects(),
	}

	// A dirty v1 main worktree is normal — v1's `config` and `journal` commands
	// wrote without committing — so it is recorded, not rejected. The CLI tells
	// the user to run `grind save` afterwards.
	dirty, err := g.HasChanges(fromMain)
	if err != nil {
		return nil, err
	}
	plan.Dirty = dirty

	v1Cfg, err := readV1Config(filepath.Join(fromMain, ".grind.json"))
	if err != nil {
		return nil, err
	}
	plan.Config = v1Cfg.GrindConfig
	// v2 bills in USD and only USD; the key was deleted rather than left inert,
	// because a setting that changes nothing is worse than one that is rejected.
	// Dropping it silently is right when READING (the unknown key is ignored),
	// but a migration that turns an AUD workspace into a $ one owes the user a
	// line saying so — and saying that the amounts were not converted.
	if v1Cfg.Currency != "" && !strings.EqualFold(v1Cfg.Currency, "USD") {
		plan.warnf("", "currency %q dropped: grind bills in USD only, and the amounts were not converted", v1Cfg.Currency)
	}

	branch, err := g.DefaultBranch(ws.BareRepo)
	if err != nil {
		return nil, err
	}

	names, err := projectNames(ws.Root, fromMain)
	if err != nil {
		return nil, err
	}

	nextTaskID := firstTaskID
	for _, name := range names {
		src, shadowed := locateProjectConfig(ws.Root, fromMain, name)
		v1, err := readV1Project(src)
		if err != nil {
			return nil, err
		}
		if shadowed != nil {
			if err := plan.warnShadowed(name, fromMain, src, *shadowed); err != nil {
				return nil, err
			}
		}

		// The project's createdAt comes from the commit that added its config
		// on the default branch, so ask git before mapping the entry.
		added, err := g.CommitAddDate(ws.BareRepo, branch, filepath.ToSlash(filepath.Join("projects", name, ".project.json")))
		if err != nil {
			return nil, err
		}

		entry, next, err := plan.mapProject(src, v1, added, nextTaskID)
		if err != nil {
			return nil, err
		}
		nextTaskID = next
		plan.Projects.Projects[name] = entry

		// v1's per-project directory on main held the config, the idea file and
		// the invoices. All three live elsewhere afterwards (or are dropped), so
		// the directory itself goes — left in place, it would put a stale idea
		// copy inside projects/, which slice 9 reserved for merged work product
		// and a later `publish` would `git rm -r`.
		//
		// Only when it exists: a workspace whose config lives solely in the
		// project worktree (the legacy layout) may have no directory on main at
		// all, and naming a path that was never there would make the commit's
		// `git add` fail on a pathspec that matches nothing.
		dir := filepath.Join(fromMain, "projects", name)
		if isDir(dir) {
			plan.RemoveDirs = append(plan.RemoveDirs, dir)
		}

		moves, err := invoiceMoves(fromMain, name)
		if err != nil {
			return nil, err
		}
		plan.Invoices = append(plan.Invoices, moves...)
	}
	plan.Projects.NextTaskID = nextTaskID

	for _, name := range layoutDirs {
		dir := filepath.Join(fromMain, name)
		if !isDir(dir) || !exists(filepath.Join(dir, ".gitkeep")) {
			plan.CreateDirs = append(plan.CreateDirs, dir)
		}
	}

	return plan, nil
}

// locateV1Main finds the v1 main worktree. grind/ wins when it exists;
// otherwise the workspace has already been moved and .main/ is where the v1
// files are — that is the whole resumability rule, in four lines.
//
// Both existing at once is not a state migration produced, so it is reported
// instead of guessed at: picking grind/ would try to rename it onto a directory
// that is already there, and grinding through that git error is worse than
// saying what is wrong.
func locateV1Main(ws *workspace.Workspace) (string, bool, error) {
	v1 := workspace.V1MainWorktreePath(ws.Root)
	if isDir(v1) {
		if isDir(ws.MainWorktree) {
			return "", false, grinderr.NewSystem(fmt.Sprintf(
				"both %s/ and %s/ exist: move or remove one before migrating",
				filepath.Base(v1), filepath.Base(ws.MainWorktree)))
		}
		return v1, true, nil
	}
	if isDir(ws.MainWorktree) {
		return ws.MainWorktree, false, nil
	}
	return "", false, grinderr.NewUser("Not in a grind workspace.")
}

// readV1Config reads v1's .grind.json. A v1 workspace always has one, so a
// missing file is a broken workspace, not an empty config.
func readV1Config(path string) (v1Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return v1Config{}, grinderr.WrapSystem(err, "read .grind.json")
	}
	var cfg v1Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return v1Config{}, grinderr.WrapSystem(err, "parse .grind.json")
	}
	return cfg, nil
}

// projectNames lists the v1 projects, sorted by name. The order is part of the
// migration's contract: tasks are renumbered in this order, so the same
// workspace always migrates to the same IDs.
//
// A v1 project's name is its branch name, and branch names may contain slashes
// (feature/issue-feature). On main the config then sits one or more directories
// deeper (projects/feature/issue-feature/.project.json), so the main side is
// walked recursively and a project's name is its directory path under projects/.
//
// Names also come from the project worktrees, because each project keeps its
// own config at <name>/projects/<name>/.project.json.
func projectNames(root, fromMain string) ([]string, error) {
	seen := map[string]bool{}

	projectsDir := filepath.Join(fromMain, "projects")
	err := filepath.WalkDir(projectsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// No projects/ at all is an empty workspace, not an error.
			if path == projectsDir && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return grinderr.WrapSystem(err, "read %s", path)
		}
		if !entry.IsDir() || path == projectsDir {
			return nil
		}
		if exists(filepath.Join(path, ".project.json")) {
			rel, err := filepath.Rel(projectsDir, path)
			if err != nil {
				return grinderr.WrapSystem(err, "name project at %s", path)
			}
			seen[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, grinderr.WrapSystem(err, "read %s", root)
	}
	for _, entry := range entries {
		name := entry.Name()
		// Dot directories are the bare repo and the main worktree; grind/ is
		// v1's main worktree. A project worktree is the only other sibling that
		// can hold a project config.
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "grind" {
			continue
		}
		if exists(filepath.Join(root, name, "projects", name, ".project.json")) {
			seen[name] = true
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// projectSource is where a project's v1 config was found. rel is the path as an
// error message should name it — relative to where the user would look.
type projectSource struct {
	name string
	path string
	rel  string
}

// locateProjectConfig returns the config the migration uses for a project: the
// project-level copy at <name>/projects/<name>/.project.json when it exists,
// otherwise the copy on main at grind/projects/<name>/.project.json.
//
// This is the reverse of "main always wins", and the reason is what v1 did:
// each project was edited in its own worktree, so that copy is the one the
// project was working from. When both exist, the main copy is returned as
// shadowed, so the caller can report what the migration is about to drop.
func locateProjectConfig(root, fromMain, name string) (chosen projectSource, shadowed *projectSource) {
	project := projectSource{
		name: name,
		path: filepath.Join(root, name, "projects", name, ".project.json"),
		rel:  filepath.Join(name, "projects", name, ".project.json"),
	}
	main := projectSource{
		name: name,
		path: filepath.Join(fromMain, "projects", name, ".project.json"),
		rel:  filepath.Join("projects", name, ".project.json"),
	}
	if !exists(project.path) {
		return main, nil
	}
	if exists(main.path) {
		return project, &main
	}
	return project, nil
}

// warnShadowed reports a main-side copy the migration drops because the
// project-level copy was used instead. Identical files lose nothing and are not
// reported. Different files may hold sessions the kept copy does not, so the
// dropped copy's counts are printed for the user to check by hand.
func (p *Plan) warnShadowed(name, fromMain string, kept projectSource, dropped projectSource) error {
	same, err := sameFile(kept.path, dropped.path)
	if err != nil {
		return err
	}
	if same {
		return nil
	}
	old, err := readV1Project(dropped)
	if err != nil {
		return err
	}
	p.warnf(name, "dropped %s (%s, %s); kept %s",
		filepath.Join(filepath.Base(fromMain), dropped.rel),
		countOf(len(old.Time), "session"), countOf(len(old.Tasks), "task"), kept.rel)
	return nil
}

// countOf renders a count with the right noun: "1 session", "2 sessions".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// sameFile reports whether two files have identical bytes.
func sameFile(a, b string) (bool, error) {
	dataA, err := os.ReadFile(a)
	if err != nil {
		return false, grinderr.WrapSystem(err, "read %s", a)
	}
	dataB, err := os.ReadFile(b)
	if err != nil {
		return false, grinderr.WrapSystem(err, "read %s", b)
	}
	return bytes.Equal(dataA, dataB), nil
}

// readV1Project parses one .project.json, naming the file in any error so the
// user knows which project is broken.
func readV1Project(src projectSource) (v1Project, error) {
	data, err := os.ReadFile(src.path)
	if err != nil {
		return v1Project{}, grinderr.WrapSystem(err, "read %s", src.rel)
	}
	var v1 v1Project
	if err := json.Unmarshal(data, &v1); err != nil {
		return v1Project{}, grinderr.WrapSystem(err, "parse %s", src.rel)
	}
	return v1, nil
}

// mapProject turns one v1 project into a v2 entry, collecting warnings on the
// way. It also returns the next global task ID, because renumbering is global
// and the caller owns the counter.
func (p *Plan) mapProject(src projectSource, v1 v1Project, added time.Time, nextTaskID int) (config.ProjectEntry, int, error) {
	// A disagreeing name is a corrupt workspace, not a user mistake: the
	// directory is the project's identity everywhere v2 looks.
	if v1.Name != "" && v1.Name != src.name {
		return config.ProjectEntry{}, nextTaskID, grinderr.NewSystem(fmt.Sprintf(
			"%s says name %q; the directory says %q.", src.rel, v1.Name, src.name))
	}

	entry := config.ProjectEntry{
		Name:     src.name,
		Type:     v1.Type,
		Idea:     p.mapIdea(src.name, v1.Idea),
		Billing:  p.mapBilling(src.name, v1.Billing),
		Client:   v1.Client,
		Repo:     v1.Repo,
		Code:     v1.Code,
		LongTerm: v1.LongTerm,
		Deadline: p.mapDeadline(src.name, v1.Deadline),
		Status:   v1.Status,
	}

	sessions, err := p.mapSessions(src, v1.Time)
	if err != nil {
		return config.ProjectEntry{}, nextTaskID, err
	}
	entry.Sessions = sessions

	entry.Tasks, nextTaskID = p.mapTasks(v1.Tasks, nextTaskID)
	p.warnPublications(src.name, v1.Publications)
	entry.CreatedAt = p.createdAt(src, added, sessions)

	return entry, nextTaskID, nil
}

// mapIdea reduces v1's stored idea to the title.
//
// v1 stored the whole trimmed idea file — "# Second idea about video", markdown
// heading included — while v2's `new project` stores the H1 text only. A
// migrated project must be indistinguishable from a native one, so the same
// extraction runs here.
//
// Anything below the heading is lost. That is exactly what `new project` does
// today, so the result is consistent, but it IS a loss and it is reported.
func (p *Plan) mapIdea(project, idea string) string {
	if strings.TrimSpace(idea) == "" {
		return ""
	}
	title := ideas.ExtractTitle(idea)
	if title == "" {
		p.warnf(project, "the v1 idea had no title heading; the project idea is empty")
		return ""
	}
	if ideaHasBody(idea) {
		p.warnf(project, "the v1 idea had a body; only the title was kept")
	}
	return title
}

// ideaHasBody reports whether anything below the title heading carries content.
// Blank lines are not a body, and neither is leading text before the heading.
func ideaHasBody(idea string) bool {
	lines := strings.Split(idea, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			for _, rest := range lines[i+1:] {
				if strings.TrimSpace(rest) != "" {
					return true
				}
			}
			return false
		}
	}
	return false
}

// mapBilling fills the per-project billing block. v2 requires both fields, so
// whatever v1 left out comes from the workspace defaults. The rate earns a
// warning because it decides what the project bills, and a silent default would
// hide a real difference between two projects.
func (p *Plan) mapBilling(project string, v1 *v1Billing) config.BillingEntry {
	entry := config.BillingEntry{
		RoundTo: p.Config.Billing.RoundTo,
		Rate:    p.Config.Billing.DefaultRate,
	}
	if v1 == nil {
		p.warnf(project, "billing.rate was missing, used workspace default %s", formatRate(entry.Rate))
		return entry
	}
	if v1.RoundTo != "" {
		entry.RoundTo = v1.RoundTo
	}
	// A zero rate is indistinguishable from an absent one in v1's data, so both
	// mean "use the workspace default".
	if v1.Rate > 0 {
		entry.Rate = v1.Rate
	} else {
		p.warnf(project, "billing.rate was missing, used workspace default %s", formatRate(entry.Rate))
	}
	return entry
}

// mapSessions copies v1's `time` array into v2's `sessions`. The two shapes are
// identical, so the copy is a validation pass rather than a translation, and
// `end: null` simply stays nil — that is what an active session looks like in
// both versions.
func (p *Plan) mapSessions(src projectSource, sessions []config.Session) ([]config.Session, error) {
	if len(sessions) == 0 {
		return nil, nil
	}
	out := make([]config.Session, 0, len(sessions))
	for i, session := range sessions {
		// A negative duration would bill negative money, so refuse it rather
		// than carry it forward to be discovered on an invoice.
		if session.Duration < 0 {
			return nil, grinderr.NewSystem(fmt.Sprintf(
				"%s: session %d has a negative duration (%d)", src.rel, i+1, session.Duration))
		}
		if session.Rounded < 0 {
			return nil, grinderr.NewSystem(fmt.Sprintf(
				"%s: session %d has a negative rounded value (%d)", src.rel, i+1, session.Rounded))
		}
		out = append(out, session)
	}
	return out, nil
}

// mapTasks renumbers one project's tasks into the global block.
//
// v1 allocated task IDs per project from 1, so every project had a task #1. v2's
// `done task <id>` searches every project for the ID, which v1's numbering makes
// ambiguous the moment a workspace has two projects. Renumbering is mandatory,
// and it preserves order: projects by name, tasks as stored.
func (p *Plan) mapTasks(tasks []config.Task, nextTaskID int) ([]config.Task, int) {
	if len(tasks) == 0 {
		return nil, nextTaskID
	}
	out := make([]config.Task, len(tasks))
	copy(out, tasks)
	for i := range out {
		out[i].ID = nextTaskID
		nextTaskID++
	}
	return out, nextTaskID
}

// mapDeadline keeps a deadline v2 can read and drops one it cannot, with a
// warning. v2 validates deadlines when they are set, so a value that fails the
// same parse would be a date no command accepts and the project could never
// clear.
func (p *Plan) mapDeadline(project, deadline string) string {
	if deadline == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", deadline); err != nil {
		p.warnf(project, "dropped malformed deadline %q", deadline)
		return ""
	}
	return deadline
}

// warnPublications reports every v1 publication, because v2's ProjectEntry has
// no field for them and nothing reads them. Dropping is the honest option for
// this slice, so the URLs are printed for the user to recover by hand; adding a
// []Publication field is listed as an extension point.
func (p *Plan) warnPublications(project string, pubs []v1Publication) {
	if len(pubs) == 0 {
		return
	}
	urls := make([]string, 0, len(pubs))
	for _, pub := range pubs {
		urls = append(urls, pub.URL)
	}
	noun := "publications"
	if len(urls) == 1 {
		noun = "publication"
	}
	p.warnf(project, "dropped %d %s: %s", len(urls), noun, strings.Join(urls, ", "))
}

// createdAt derives a project's creation date, which v1 never stored, from the
// best evidence available:
//
//  1. the commit that first added projects/<name>/.project.json on the default
//     branch — the moment the project was really created;
//  2. otherwise the earliest session start, the earliest proof of work;
//  3. otherwise the config file's modification time;
//  4. otherwise the migration instant, with a warning. This is only reachable
//     if the file cannot be stat'ed after being read, but a date that comes from
//     nowhere should at least say so instead of silently being zero.
func (p *Plan) createdAt(src projectSource, added time.Time, sessions []config.Session) time.Time {
	if !added.IsZero() {
		return added
	}
	if earliest, ok := earliestSessionStart(sessions); ok {
		return earliest
	}
	if info, err := os.Stat(src.path); err == nil && !info.ModTime().IsZero() {
		return info.ModTime()
	}
	p.warnf(src.name, "no creation date could be derived, used the migration instant")
	return p.now
}

// earliestSessionStart returns the earliest non-zero session start.
func earliestSessionStart(sessions []config.Session) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, session := range sessions {
		if session.Start.IsZero() {
			continue
		}
		if !found || session.Start.Before(earliest) {
			earliest = session.Start
			found = true
		}
	}
	return earliest, found
}

// invoiceMoves lists the invoice directories v1 wrote under a project's state
// directory. v2 keeps invoices/ at the top of the workspace, one directory per
// project, because projects/ holds merged work product rather than billing
// records.
func invoiceMoves(fromMain, project string) ([]InvoiceMove, error) {
	dir := filepath.Join(fromMain, "projects", project, "invoices")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, grinderr.WrapSystem(err, "read %s", dir)
	}

	moves := make([]InvoiceMove, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		moves = append(moves, InvoiceMove{
			Project: project,
			From:    filepath.Join(dir, entry.Name()),
			// The v1 id is kept verbatim: v2 never parses an existing invoice
			// id, and renaming the directory would rewrite history for looks.
			To: filepath.Join(fromMain, "invoices", project, entry.Name()),
		})
	}
	return moves, nil
}

// warnf records a human-readable warning, prefixed with the project it is about
// (empty for workspace-level warnings). The CLI prints them verbatim.
func (p *Plan) warnf(project, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if project != "" {
		message = project + ": " + message
	}
	p.Warnings = append(p.Warnings, message)
}

// formatRate renders a rate the way a user reads it: 150, not 150.000000.
func formatRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', -1, 64)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// v1Config is .grind.json as v1 wrote it. It embeds v2's config type because
// every v2 field is a v1 field under the same name; only `currency` — the one
// key v2 deleted — needs declaring here.
type v1Config struct {
	config.GrindConfig
	Currency string `json:"currency,omitempty"`
}

// v1Project is projects/<name>/.project.json as v1 wrote it.
//
// Sessions and tasks reuse v2's types on purpose: v1's wire shape is identical,
// so a field added to config.Session or config.Task later is carried over by
// this migration instead of being silently dropped for lack of a declaration.
type v1Project struct {
	Name         string             `json:"name"`
	Type         string             `json:"type"`
	Idea         string             `json:"idea"`
	Time         []config.Session   `json:"time"`
	Billing      *v1Billing         `json:"billing"`
	Tasks        []config.Task      `json:"tasks"`
	Client       *config.ClientInfo `json:"client"`
	Repo         string             `json:"repo"`
	Code         string             `json:"code"`
	LongTerm     bool               `json:"longTerm"`
	Deadline     string             `json:"deadline"`
	Status       string             `json:"status"`
	Publications []v1Publication    `json:"publications"`
}

// v1Billing is a pointer in v1Project because a project may have no billing
// block at all, which is different from a block with a zero rate.
type v1Billing struct {
	RoundTo string  `json:"roundTo"`
	Rate    float64 `json:"rate"`
}

// v1Publication records where a project was published. v2 has no field for it.
type v1Publication struct {
	URL         string `json:"url"`
	PublishedAt string `json:"publishedAt"`
}
