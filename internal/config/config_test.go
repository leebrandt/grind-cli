package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Billing.RoundTo != "quarter-hour" {
		t.Errorf("RoundTo = %q, want %q", cfg.Billing.RoundTo, "quarter-hour")
	}
	if cfg.Billing.DefaultRate != 150 {
		t.Errorf("DefaultRate = %v, want 150", cfg.Billing.DefaultRate)
	}
}

func TestValidTypes(t *testing.T) {
	cfg := Default()
	if got := ValidTypes(cfg); !reflect.DeepEqual(got, defaultTypes) {
		t.Errorf("ValidTypes(default) = %v, want %v", got, defaultTypes)
	}

	cfg.ProjectTypes = []string{"blog", "code"}
	if got := ValidTypes(cfg); !reflect.DeepEqual(got, []string{"blog", "code"}) {
		t.Errorf("ValidTypes(configured) = %v, want [blog code]", got)
	}
}

func TestValidateType(t *testing.T) {
	cfg := Default()

	t.Run("empty is allowed", func(t *testing.T) {
		if err := ValidateType(cfg, ""); err != nil {
			t.Errorf("ValidateType('') = %v, want nil", err)
		}
	})

	t.Run("valid type", func(t *testing.T) {
		if err := ValidateType(cfg, "blog"); err != nil {
			t.Errorf("ValidateType(blog) = %v, want nil", err)
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		err := ValidateType(cfg, "nonsense")
		if err == nil {
			t.Fatal("expected error")
		}
		want := "Invalid type: nonsense. Valid types: " + strings.Join(defaultTypes, ", ")
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("configured types", func(t *testing.T) {
		cfg.ProjectTypes = []string{"blog", "code"}
		if err := ValidateType(cfg, "code"); err != nil {
			t.Errorf("ValidateType(code) = %v, want nil", err)
		}
		if err := ValidateType(cfg, "nonsense"); err == nil {
			t.Error("ValidateType(nonsense) = nil, want error")
		}
	})
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".grind.json")

	cfg := Default()
	cfg.DefaultBranch = "main"
	cfg.ProjectTypes = []string{"blog", "code"}
	cfg.Currency = "USD"

	if err := Write(path, cfg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Billing.RoundTo != cfg.Billing.RoundTo {
		t.Errorf("RoundTo = %q, want %q", got.Billing.RoundTo, cfg.Billing.RoundTo)
	}
	if got.Billing.DefaultRate != cfg.Billing.DefaultRate {
		t.Errorf("DefaultRate = %v, want %v", got.Billing.DefaultRate, cfg.Billing.DefaultRate)
	}
	if got.DefaultBranch != cfg.DefaultBranch {
		t.Errorf("DefaultBranch = %q, want %q", got.DefaultBranch, cfg.DefaultBranch)
	}
	if len(got.ProjectTypes) != 2 || got.ProjectTypes[0] != "blog" {
		t.Errorf("ProjectTypes = %v, want [blog code]", got.ProjectTypes)
	}
	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", got.Currency)
	}
}

func TestWriteProjectsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".projects.json")

	cfg := DefaultProjects()
	if err := WriteProjects(path, cfg); err != nil {
		t.Fatalf("WriteProjects: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	want := "{\n  \"version\": 1,\n  \"nextTaskId\": 100,\n  \"projects\": {}\n}\n"
	if string(data) != want {
		t.Errorf("projects file = %q, want %q", string(data), want)
	}
}

func TestDefaultProjectsStartsTaskCounterAt100(t *testing.T) {
	cfg := DefaultProjects()
	if cfg.NextTaskID != 100 {
		t.Errorf("NextTaskID = %d, want 100", cfg.NextTaskID)
	}
}
