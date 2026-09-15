// Package config's Service reads and writes the workspace config files
// (.grind.json and .projects.json) as flattened key=value pairs for the
// `grind config` command.
//
// The git layer is injected so tests can substitute a fake and verify that
// every set commits immediately with the right message and staged file.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/git"
	"github.com/leebrandt/grind/internal/grinderr"
)

// Paths bundles the file paths a config operation touches. The CLI builds
// it from *workspace.Workspace; config cannot take the workspace itself
// because workspace imports config — a config→workspace import would be a
// cycle.
type Paths struct {
	ConfigPath   string // .main/.grind.json
	ProjectsPath string // .main/.projects.json
	MainWorktree string // .main
	BareRepo     string // .grind.repo.git
}

// Entry is one flattened key=value config pair.
type Entry struct {
	Key   string
	Value string
}

// Service reads and writes the workspace config files. The git layer is
// injected so tests can substitute a fake.
type Service struct {
	Git git.Git
}

// NewService returns a Service backed by g.
func NewService(g git.Git) *Service {
	return &Service{Git: g}
}

// workspaceKeys is the settable workspace key set. defaultBranch is listed
// when present but deliberately NOT settable — renaming the default branch
// is out of scope.
var workspaceKeys = []string{
	"billing.roundTo",
	"billing.defaultRate",
	"projectTypes",
	"my.name",
	"my.company",
	"my.address",
	"my.phone",
	"my.email",
	"my.taxId",
	"currency",
	"paymentTerms",
	"remote.url",
}

// projectKeys is the settable project key set.
var projectKeys = []string{
	"type",
	"billing.roundTo",
	"billing.rate",
	"client.contact",
	"client.company",
	"client.address",
	"client.phone",
	"client.email",
	"repo",
	"code",
	"longTerm",
	"deadline",
}

// HasProject reports whether project exists in .projects.json. The CLI
// uses it to decide whether the first argument names a project. A missing
// .projects.json is an empty workspace, so the answer is simply false.
func (s *Service) HasProject(p Paths, name string) (bool, error) {
	projects, err := readProjects(p.ProjectsPath)
	if err != nil {
		return false, err
	}
	_, ok := projects.Projects[name]
	return ok, nil
}

// List returns the flattened workspace config, sorted by key. A missing
// .grind.json is treated as the defaults, matching the projects package's
// tolerance for old workspaces.
func (s *Service) List(p Paths) ([]Entry, error) {
	cfg, err := readConfig(p.ConfigPath)
	if err != nil {
		return nil, err
	}
	return flattenConfig(cfg), nil
}

// Get returns one workspace config value. A key that is valid but unset
// (for example my.name on a fresh workspace) is reported as not found.
func (s *Service) Get(p Paths, key string) (string, error) {
	cfg, err := readConfig(p.ConfigPath)
	if err != nil {
		return "", err
	}
	for _, e := range flattenConfig(cfg) {
		if e.Key == key {
			return e.Value, nil
		}
	}
	return "", grinderr.NewUser("Key not found: " + key)
}

// Set validates key and value, writes .grind.json atomically, and commits
// immediately staging only that file. Setting remote.url also syncs the
// bare repo's origin so the URL is live the moment it is set.
func (s *Service) Set(p Paths, key, value string) error {
	cfg, err := readConfig(p.ConfigPath)
	if err != nil {
		return err
	}
	if err := setWorkspaceKey(&cfg, key, value); err != nil {
		return err
	}
	if err := Write(p.ConfigPath, cfg); err != nil {
		return err
	}
	if err := s.Git.Commit(p.MainWorktree, "Set config: "+key, ".grind.json"); err != nil {
		return err
	}
	if key == "remote.url" {
		if err := s.Git.SetRemoteURL(p.BareRepo, value); err != nil {
			return err
		}
	}
	return nil
}

// ProjectList returns the flattened config for one project, sorted by key.
func (s *Service) ProjectList(p Paths, project string) ([]Entry, error) {
	entry, err := s.requireProject(p, project)
	if err != nil {
		return nil, err
	}
	return flattenProject(entry), nil
}

// ProjectGet returns one project config value.
func (s *Service) ProjectGet(p Paths, project, key string) (string, error) {
	entry, err := s.requireProject(p, project)
	if err != nil {
		return "", err
	}
	for _, e := range flattenProject(entry) {
		if e.Key == key {
			return e.Value, nil
		}
	}
	return "", grinderr.NewUser("Key not found: " + key)
}

// ProjectSet validates key and value, writes .projects.json atomically, and
// commits immediately staging only that file.
func (s *Service) ProjectSet(p Paths, project, key, value string) error {
	projects, err := readProjects(p.ProjectsPath)
	if err != nil {
		return err
	}
	entry, ok := projects.Projects[project]
	if !ok {
		return grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", project))
	}

	// The type key is validated against the effective project types from
	// .grind.json, so the workspace config is needed for that one key.
	cfg, err := readConfig(p.ConfigPath)
	if err != nil {
		return err
	}
	if err := setProjectKey(cfg, &entry, key, value); err != nil {
		return err
	}

	projects.Projects[project] = entry
	if err := WriteProjects(p.ProjectsPath, projects); err != nil {
		return err
	}
	return s.Git.Commit(p.MainWorktree, "Set config: "+project+" "+key, ".projects.json")
}

// requireProject loads one project entry, or fails with the spec's
// does-not-exist message.
func (s *Service) requireProject(p Paths, project string) (ProjectEntry, error) {
	projects, err := readProjects(p.ProjectsPath)
	if err != nil {
		return ProjectEntry{}, err
	}
	entry, ok := projects.Projects[project]
	if !ok {
		return ProjectEntry{}, grinderr.NewUser(fmt.Sprintf("Project '%s' does not exist.", project))
	}
	return entry, nil
}

// readConfig loads .grind.json, falling back to defaults when the file is
// missing (a workspace created by an older grind version may not have one).
func readConfig(path string) (GrindConfig, error) {
	cfg, err := Read(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return GrindConfig{}, err
	}
	return cfg, nil
}

// readProjects loads .projects.json, treating a missing file as an empty
// workspace.
func readProjects(path string) (ProjectsConfig, error) {
	cfg, err := ReadProjects(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultProjects(), nil
		}
		return ProjectsConfig{}, err
	}
	return cfg, nil
}

// flattenConfig turns a GrindConfig into sorted key=value entries. Empty
// values are omitted so a fresh workspace lists only the billing defaults,
// not a wall of empty my.* keys.
func flattenConfig(cfg GrindConfig) []Entry {
	entries := []Entry{
		{Key: "billing.roundTo", Value: cfg.Billing.RoundTo},
		{Key: "billing.defaultRate", Value: formatFloat(cfg.Billing.DefaultRate)},
	}
	if len(cfg.ProjectTypes) > 0 {
		entries = append(entries, Entry{Key: "projectTypes", Value: strings.Join(cfg.ProjectTypes, ",")})
	}
	if cfg.My != nil {
		entries = appendMyEntries(entries, cfg.My)
	}
	if cfg.Currency != "" {
		entries = append(entries, Entry{Key: "currency", Value: cfg.Currency})
	}
	if cfg.PaymentTerms != "" {
		entries = append(entries, Entry{Key: "paymentTerms", Value: cfg.PaymentTerms})
	}
	if cfg.Remote != nil && cfg.Remote.URL != "" {
		entries = append(entries, Entry{Key: "remote.url", Value: cfg.Remote.URL})
	}
	// defaultBranch is listed if present but NOT settable (spec decision 6).
	if cfg.DefaultBranch != "" {
		entries = append(entries, Entry{Key: "defaultBranch", Value: cfg.DefaultBranch})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries
}

// appendMyEntries adds the non-empty my.* fields to entries.
func appendMyEntries(entries []Entry, my *MyConfig) []Entry {
	if my.Name != "" {
		entries = append(entries, Entry{Key: "my.name", Value: my.Name})
	}
	if my.Company != "" {
		entries = append(entries, Entry{Key: "my.company", Value: my.Company})
	}
	if my.Address != "" {
		entries = append(entries, Entry{Key: "my.address", Value: my.Address})
	}
	if my.Phone != "" {
		entries = append(entries, Entry{Key: "my.phone", Value: my.Phone})
	}
	if my.Email != "" {
		entries = append(entries, Entry{Key: "my.email", Value: my.Email})
	}
	if my.TaxID != "" {
		entries = append(entries, Entry{Key: "my.taxId", Value: my.TaxID})
	}
	return entries
}

// flattenProject turns a ProjectEntry into sorted key=value entries.
// longTerm always shows its effective value (false when unset); the other
// keys are omitted when empty.
func flattenProject(entry ProjectEntry) []Entry {
	entries := []Entry{
		{Key: "billing.roundTo", Value: entry.Billing.RoundTo},
		{Key: "billing.rate", Value: formatFloat(entry.Billing.Rate)},
		{Key: "longTerm", Value: strconv.FormatBool(entry.LongTerm)},
	}
	if entry.Type != "" {
		entries = append(entries, Entry{Key: "type", Value: entry.Type})
	}
	if entry.Client != nil {
		if entry.Client.Contact != "" {
			entries = append(entries, Entry{Key: "client.contact", Value: entry.Client.Contact})
		}
		if entry.Client.Company != "" {
			entries = append(entries, Entry{Key: "client.company", Value: entry.Client.Company})
		}
		if entry.Client.Address != "" {
			entries = append(entries, Entry{Key: "client.address", Value: entry.Client.Address})
		}
		if entry.Client.Phone != "" {
			entries = append(entries, Entry{Key: "client.phone", Value: entry.Client.Phone})
		}
		if entry.Client.Email != "" {
			entries = append(entries, Entry{Key: "client.email", Value: entry.Client.Email})
		}
	}
	if entry.Repo != "" {
		entries = append(entries, Entry{Key: "repo", Value: entry.Repo})
	}
	if entry.Code != "" {
		entries = append(entries, Entry{Key: "code", Value: entry.Code})
	}
	if entry.Deadline != "" {
		entries = append(entries, Entry{Key: "deadline", Value: entry.Deadline})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries
}

// formatFloat renders a float without trailing zeros: 150 → "150",
// 250.5 → "250.5".
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// setWorkspaceKey applies one key=value pair to a GrindConfig. The switch
// is explicit over the known keys — the set is small and fixed, and
// reflection would hide bugs.
func setWorkspaceKey(cfg *GrindConfig, key, value string) error {
	switch key {
	case "billing.roundTo":
		if !validRoundTo(value) {
			return grinderr.NewUser(fmt.Sprintf(
				"Invalid roundTo value: %s. Valid options: quarter-hour, half-hour, hour", value))
		}
		cfg.Billing.RoundTo = value
	case "billing.defaultRate":
		rate, err := parsePositiveRate(value)
		if err != nil {
			return err
		}
		cfg.Billing.DefaultRate = rate
	case "projectTypes":
		types, err := parseProjectTypes(value)
		if err != nil {
			return err
		}
		cfg.ProjectTypes = types
	case "my.name":
		cfg.My = ensureMy(cfg.My)
		cfg.My.Name = value
	case "my.company":
		cfg.My = ensureMy(cfg.My)
		cfg.My.Company = value
	case "my.address":
		cfg.My = ensureMy(cfg.My)
		cfg.My.Address = value
	case "my.phone":
		cfg.My = ensureMy(cfg.My)
		cfg.My.Phone = value
	case "my.email":
		cfg.My = ensureMy(cfg.My)
		cfg.My.Email = value
	case "my.taxId":
		cfg.My = ensureMy(cfg.My)
		cfg.My.TaxID = value
	case "currency":
		cfg.Currency = value
	case "paymentTerms":
		cfg.PaymentTerms = value
	case "remote.url":
		// The spec requires a non-empty URL (there is no "clear the remote"
		// verb); git is the validator for the URL's format (spec decision 7),
		// so no format check here.
		if value == "" {
			return grinderr.NewUser("Invalid remote.url: must not be empty.")
		}
		cfg.Remote = &RemoteConfig{URL: value}
	default:
		return grinderr.NewUser(fmt.Sprintf(
			"Invalid key for workspace config: %s. Valid keys: %s",
			key, strings.Join(workspaceKeys, ", ")))
	}
	return nil
}

// setProjectKey applies one key=value pair to a ProjectEntry. The cfg
// argument supplies the effective project types for the type key.
func setProjectKey(cfg GrindConfig, entry *ProjectEntry, key, value string) error {
	switch key {
	case "type":
		if err := ValidateType(cfg, value); err != nil {
			return err
		}
		entry.Type = value
	case "billing.roundTo":
		if !validRoundTo(value) {
			return grinderr.NewUser(fmt.Sprintf(
				"Invalid roundTo value: %s. Valid options: quarter-hour, half-hour, hour", value))
		}
		entry.Billing.RoundTo = value
	case "billing.rate":
		rate, err := parsePositiveRate(value)
		if err != nil {
			return err
		}
		entry.Billing.Rate = rate
	case "client.contact":
		entry.Client = ensureClient(entry.Client)
		entry.Client.Contact = value
	case "client.company":
		entry.Client = ensureClient(entry.Client)
		entry.Client.Company = value
	case "client.address":
		entry.Client = ensureClient(entry.Client)
		entry.Client.Address = value
	case "client.phone":
		entry.Client = ensureClient(entry.Client)
		entry.Client.Phone = value
	case "client.email":
		entry.Client = ensureClient(entry.Client)
		entry.Client.Email = value
	case "repo":
		entry.Repo = value
	case "code":
		entry.Code = value
	case "longTerm":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		entry.LongTerm = b
	case "deadline":
		if !validDeadline(value) {
			return grinderr.NewUser(fmt.Sprintf(
				"Invalid deadline: %s. Expected format: YYYY-MM-DD.", value))
		}
		entry.Deadline = value
	default:
		return grinderr.NewUser(fmt.Sprintf(
			"Invalid key for project config: %s. Valid keys: %s",
			key, strings.Join(projectKeys, ", ")))
	}
	return nil
}

// ensureMy returns my, allocating it when nil so a my.* set on a fresh
// workspace creates the block instead of panicking.
func ensureMy(my *MyConfig) *MyConfig {
	if my == nil {
		return &MyConfig{}
	}
	return my
}

// ensureClient returns client, allocating it when nil so a client.* set on
// a project without a client block creates it.
func ensureClient(client *ClientInfo) *ClientInfo {
	if client == nil {
		return &ClientInfo{}
	}
	return client
}

// validRoundTo reports whether v is one of the three billing rounding
// options.
func validRoundTo(v string) bool {
	switch v {
	case "quarter-hour", "half-hour", "hour":
		return true
	}
	return false
}

// parsePositiveRate parses a billing rate that must be a positive number.
func parsePositiveRate(value string) (float64, error) {
	rate, err := strconv.ParseFloat(value, 64)
	if err != nil || rate <= 0 {
		return 0, grinderr.NewUser(fmt.Sprintf(
			"Invalid rate: %s. Must be a positive number.", value))
	}
	return rate, nil
}

// parseProjectTypes splits a comma-separated list into trimmed, non-empty
// types.
func parseProjectTypes(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	types := make([]string, 0, len(parts))
	for _, part := range parts {
		t := strings.TrimSpace(part)
		if t == "" {
			return nil, grinderr.NewUser(fmt.Sprintf(
				"Invalid projectTypes: %s. Must be a comma-separated list of types.", value))
		}
		types = append(types, t)
	}
	return types, nil
}

// parseBool parses the longTerm key's true|false values.
func parseBool(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, grinderr.NewUser(fmt.Sprintf(
		"Invalid longTerm value: %s. Must be true or false.", value))
}

// validDeadline reports whether v is a strict YYYY-MM-DD date. time.Parse
// rejects impossible calendar dates like 2026-02-30 and non-zero-padded
// months/days.
func validDeadline(v string) bool {
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}