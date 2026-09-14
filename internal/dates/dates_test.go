package dates

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/leebrandt/grind/internal/grinderr"
)

// fixedNow is the reference date for all ParseDate tests. It is a local
// time (not UTC) so relative forms exercise the local-date behavior the
// spec demands.
var fixedNow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local)

func TestParseDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"today", "2026-07-15", false},
		{"tomorrow", "2026-07-16", false},
		{"3d", "2026-07-18", false},
		{"3days", "2026-07-18", false},
		{"1w", "2026-07-22", false},
		{"1week", "2026-07-22", false},
		{"2w", "2026-07-29", false},
		{"0720", "2026-07-20", false},
		{"1225", "2026-12-25", false},
		{"0101", "2026-01-01", false},
		{"072026", "2026-07-20", false},
		{"122525", "2025-12-25", false},
		{"20260720", "2026-07-20", false},
		{"2026-07-20", "2026-07-20", false},
		// Case-insensitive and whitespace-trimmed.
		{"Tomorrow", "2026-07-16", false},
		{"3DAYS", "2026-07-18", false},
		{" tomorrow ", "2026-07-16", false},
		// Errors: unparseable formats and impossible dates.
		{"banana", "", true},
		{"1340", "", true},       // month 13
		{"0230", "", true},       // Feb 30
		{"2026-02-30", "", true}, // Feb 30, ISO form
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDate(tt.input, fixedNow)
			if tt.err {
				if err == nil {
					t.Fatalf("ParseDate(%q) = %q, want error", tt.input, got)
				}
				var user *grinderr.User
				if !errors.As(err, &user) {
					t.Errorf("ParseDate(%q) error type = %T, want *grinderr.User", tt.input, err)
				}
				if !strings.Contains(err.Error(), "Unparseable date:") {
					t.Errorf("ParseDate(%q) error = %q, want Unparseable date message", tt.input, err.Error())
				}
				if !strings.Contains(err.Error(), tt.input) {
					t.Errorf("ParseDate(%q) error = %q, want input echoed", tt.input, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDate(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDateLeapYear(t *testing.T) {
	// 2024 is a leap year, so Feb 29 is valid; 2026 is not.
	got, err := ParseDate("022924", fixedNow)
	if err != nil {
		t.Fatalf("ParseDate(022924) error = %v", err)
	}
	if got != "2024-02-29" {
		t.Errorf("ParseDate(022924) = %q, want 2024-02-29", got)
	}

	if _, err := ParseDate("022926", fixedNow); err == nil {
		t.Error("ParseDate(022926) = nil, want error for non-leap year")
	}
}

func TestParseDateErrorEchoesRawInput(t *testing.T) {
	// The raw input (not the trimmed/lowercased form) must be echoed.
	_, err := ParseDate("  Banana  ", fixedNow)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != `Unparseable date: "  Banana  "` {
		t.Errorf("error = %q", err.Error())
	}
}