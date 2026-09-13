package grinderr

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"user", NewUser("bad input"), 1},
		{"system", NewSystem("git failed"), 2},
		{"unknown", errors.New("boom"), 99},
		{"wrapped user", fmt.Errorf("context: %w", NewUser("bad input")), 1},
		{"wrapped system", fmt.Errorf("context: %w", NewSystem("git failed")), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestWrapUserKeepsType(t *testing.T) {
	err := WrapUser(errors.New("parse"), "invalid number %q", "abc")
	var user *User
	if !errors.As(err, &user) {
		t.Fatal("expected wrapped error to still be a *User")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("ExitCode = %d, want 1", ExitCode(err))
	}
}

func TestWrapSystemKeepsType(t *testing.T) {
	err := WrapSystem(errors.New("exit status 1"), "git commit failed")
	var sys *System
	if !errors.As(err, &sys) {
		t.Fatal("expected wrapped error to still be a *System")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("ExitCode = %d, want 2", ExitCode(err))
	}
}
