// Package config loads and saves the workspace configuration files.
//
// .grind.json holds workspace-level config (billing defaults, project
// types, professional info). .projects.json holds all project state in a
// single versioned file. Both are written atomically: write a temp file in
// the same directory, then os.Rename over the target so a crash never
// leaves a half-written config behind.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/leebrandt/grind/internal/grinderr"
)

// BillingConfig holds per-workspace billing defaults.
type BillingConfig struct {
	RoundTo     string  `json:"roundTo"`
	DefaultRate float64 `json:"defaultRate"`
}

// MyConfig holds professional info about the workspace owner.
type MyConfig struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// RemoteConfig holds the remote URL for cross-machine sync.
type RemoteConfig struct {
	URL string `json:"url,omitempty"`
}

// GrindConfig is the typed shape of .grind.json. The fields beyond Billing
// are part of the v1 schema even though later slices use them.
type GrindConfig struct {
	Billing       BillingConfig `json:"billing"`
	DefaultBranch string        `json:"defaultBranch,omitempty"`
	ProjectTypes  []string      `json:"projectTypes,omitempty"`
	My            *MyConfig     `json:"my,omitempty"`
	Currency      string        `json:"currency,omitempty"`
	PaymentTerms  string        `json:"paymentTerms,omitempty"`
	Remote        *RemoteConfig `json:"remote,omitempty"`
}

// Default returns the default workspace configuration.
func Default() GrindConfig {
	return GrindConfig{
		Billing: BillingConfig{
			RoundTo:     "quarter-hour",
			DefaultRate: 150,
		},
	}
}

// Read loads a GrindConfig from path.
func Read(path string) (GrindConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GrindConfig{}, grinderr.WrapSystem(err, "read config file")
	}
	var cfg GrindConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return GrindConfig{}, grinderr.WrapSystem(err, "parse config file")
	}
	return cfg, nil
}

// Write saves cfg to path atomically.
func Write(path string, cfg GrindConfig) error {
	return writeJSON(path, cfg)
}

// ProjectsConfig is the typed shape of .projects.json. It holds all
// project state in a single versioned file so future migrations can bump
// the version field.
type ProjectsConfig struct {
	Version int `json:"version"`
	// NextTaskID is the next global task ID to hand out. It starts at 100
	// and only ever increments; IDs are never reused across projects. A
	// missing field (a workspace created before this slice) is treated as
	// 100 by the tasks package.
	NextTaskID int                     `json:"nextTaskId"`
	Projects   map[string]ProjectEntry `json:"projects"`
}

// ProjectEntry is the per-project state block. The domain type lives here
// (not in internal/projects) because projects imports workspace, which
// imports config — a projects-owned type would create an import cycle.
type ProjectEntry struct {
	Name      string       `json:"name"`
	Type      string       `json:"type,omitempty"`
	Idea      string       `json:"idea"`
	Billing   BillingEntry `json:"billing"`
	CreatedAt time.Time    `json:"createdAt"`
	Sessions  []Session    `json:"sessions,omitempty"`
	Tasks     []Task       `json:"tasks,omitempty"`
}

// Task is one item on a project's task list. The domain type lives here
// (not in internal/tasks) for the same reason ProjectEntry does: tasks
// imports workspace, which imports config — a tasks-owned type would
// create an import cycle.
type Task struct {
	ID          int        `json:"id"`
	Description string     `json:"description"`
	Done        bool       `json:"done"`
	CreatedAt   time.Time  `json:"createdAt"`
	// CompletedAt is set when the task flips to done; it stays nil while
	// the task is open.
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	// DueDate is an optional LOCAL YYYY-MM-DD date. Local, not UTC — this
	// deliberately fixes v1's bug where "due today" was computed against
	// UTC and mislabeled tasks in negative-offset timezones.
	DueDate string `json:"dueDate,omitempty"`
}

// Session is one work session on a project. Start is set when the session
// begins; End stays nil while the session is active. Duration and Rounded
// are in seconds and are written when the session ends — storing the rounded
// value at end time freezes the billing math, so a later change to roundTo
// never rewrites history.
type Session struct {
	Start    time.Time  `json:"start"`
	End      *time.Time `json:"end,omitempty"`
	Duration int64      `json:"duration,omitempty"`
	Rounded  int64      `json:"rounded,omitempty"`
}

// BillingEntry is the per-project billing block. Each project carries its
// own copy of the workspace defaults so later rate changes do not rewrite
// history — per-project billing is a hard requirement from the design
// constitution.
type BillingEntry struct {
	RoundTo string  `json:"roundTo"`
	Rate    float64 `json:"rate"`
}

// DefaultProjects returns the empty projects skeleton. The task counter
// starts at 100 (agreed with the user — the first task is #100, not #1).
func DefaultProjects() ProjectsConfig {
	return ProjectsConfig{
		Version:    1,
		NextTaskID: 100,
		Projects:   map[string]ProjectEntry{},
	}
}

// WriteProjects saves the projects config atomically.
func WriteProjects(path string, cfg ProjectsConfig) error {
	return writeJSON(path, cfg)
}

// ReadProjects loads a ProjectsConfig from path. A missing file surfaces as
// a system error; callers decide whether to treat it as an empty workspace.
func ReadProjects(path string) (ProjectsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectsConfig{}, grinderr.WrapSystem(err, "read projects file")
	}
	var cfg ProjectsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProjectsConfig{}, grinderr.WrapSystem(err, "parse projects file")
	}
	return cfg, nil
}

// writeJSON marshals v with 2-space indentation and writes it atomically.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return grinderr.WrapSystem(err, "encode JSON")
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return grinderr.WrapSystem(err, "create temp file")
	}
	tmpName := tmp.Name()
	// Clean up the temp file if anything goes wrong before the rename.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return grinderr.WrapSystem(err, "write temp file")
	}
	if err := tmp.Close(); err != nil {
		return grinderr.WrapSystem(err, "close temp file")
	}
	// CreateTemp creates files with 0600; config files should be readable
	// by the owner's group and others, so widen to the usual 0644.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return grinderr.WrapSystem(err, "set temp file permissions")
	}
	if err := os.Rename(tmpName, path); err != nil {
		return grinderr.WrapSystem(err, "replace file")
	}
	return nil
}
