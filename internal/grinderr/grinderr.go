// Package grinderr defines the typed errors used across grind.
//
// The CLI distinguishes three kinds of failures:
//   - User errors (exit 1): bad input, missing data, user aborts.
//   - System errors (exit 2): I/O failures, git failures, editor failures.
//   - Anything else (exit 99): unexpected bugs.
//
// main() is the only place that maps an error to an exit code; commands
// never call os.Exit themselves.
package grinderr

import (
	"errors"
	"fmt"
)

// User represents a mistake the user can fix: a bad argument, a missing
// workspace, a file that does not exist, and so on.
type User struct {
	msg   string
	cause error
}

// Error implements the error interface.
func (e *User) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

// Unwrap lets errors.Is/errors.As see through wrapped user errors.
func (e *User) Unwrap() error { return e.cause }

// NewUser builds a user error from a plain message.
func NewUser(msg string) error {
	return &User{msg: msg}
}

// WrapUser wraps an underlying error (for example a strconv failure) while
// keeping the exit code of a user error.
func WrapUser(cause error, format string, args ...any) error {
	return &User{msg: fmt.Sprintf(format, args...), cause: cause}
}

// System represents a failure of the environment grind runs in: a git
// command that failed, a file that could not be written, an editor that
// crashed.
type System struct {
	msg   string
	cause error
}

// Error implements the error interface.
func (e *System) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

// Unwrap lets errors.Is/errors.As see through wrapped system errors.
func (e *System) Unwrap() error { return e.cause }

// NewSystem builds a system error from a plain message.
func NewSystem(msg string) error {
	return &System{msg: msg}
}

// WrapSystem wraps an underlying error while keeping the exit code of a
// system error.
func WrapSystem(cause error, format string, args ...any) error {
	return &System{msg: fmt.Sprintf(format, args...), cause: cause}
}

// ExitCode maps an error to the process exit code grind should use.
//
// The mapping mirrors v1's GrindError hierarchy: user errors exit 1,
// system errors exit 2, and anything unexpected exits 99 so it stands out
// in logs.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var user *User
	if errors.As(err, &user) {
		return 1
	}
	var sys *System
	if errors.As(err, &sys) {
		return 2
	}
	return 99
}
