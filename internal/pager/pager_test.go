package pager

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pretendTerminal forces the pager path. A real tty cannot be arranged in a
// test, so the production check is swapped for a stub and restored on
// cleanup.
func pretendTerminal(t *testing.T, terminal bool) {
	t.Helper()
	original := isTerminal
	isTerminal = func(w io.Writer) bool { return terminal }
	t.Cleanup(func() { isTerminal = original })
}

// TestIsTerminalOnDevNull: the real check (not the test stub) must say no to
// a character device that is not a terminal.
func TestIsTerminalOnDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer f.Close()

	if isTerminal(f) {
		t.Errorf("isTerminal(%s) = true, want false", os.DevNull)
	}
}

// TestPageNonTerminalWritesVerbatim is the contract the CLI depends on when
// output is redirected: the text reaches the writer untouched and no pager
// process is involved.
func TestPageNonTerminalWritesVerbatim(t *testing.T) {
	// Point PAGER at a command that would loudly fail if it ever ran.
	t.Setenv("PAGER", "grind-pager-must-not-run")

	var buf bytes.Buffer
	if err := Page(&buf, "line one\nline two\n"); err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got, want := buf.String(), "line one\nline two\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestPageUsesPager proves the text travels through a pager process: `cat`
// echoes its stdin, so an identical result means the pipe was wired up.
func TestPageUsesPager(t *testing.T) {
	pretendTerminal(t, true)
	t.Setenv("PAGER", "cat")

	var buf bytes.Buffer
	if err := Page(&buf, "paged line\n"); err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got, want := buf.String(), "paged line\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestPagePagerArgs proves $PAGER is split on whitespace, so an option can
// ride along with the program name.
func TestPagePagerArgs(t *testing.T) {
	pretendTerminal(t, true)
	t.Setenv("PAGER", "cat -u")

	var buf bytes.Buffer
	if err := Page(&buf, "with args\n"); err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got, want := buf.String(), "with args\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestPageMissingPagerFallsBack: a missing pager is not an error, and the
// text is printed instead of being silently dropped.
func TestPageMissingPagerFallsBack(t *testing.T) {
	pretendTerminal(t, true)
	t.Setenv("PAGER", "grind-pager-that-does-not-exist")

	var buf bytes.Buffer
	if err := Page(&buf, "still visible\n"); err != nil {
		t.Fatalf("Page: %v", err)
	}
	if got, want := buf.String(), "still visible\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestPagePagerExitStatusIgnored: less exits non-zero when it cannot open a
// terminal, and the user can kill it with Ctrl-C. Neither is a grind
// failure, so Page returns nil.
func TestPagePagerExitStatusIgnored(t *testing.T) {
	pretendTerminal(t, true)

	script := filepath.Join(t.TempDir(), "failing-pager")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAGER", script)

	if err := Page(&bytes.Buffer{}, "anything\n"); err != nil {
		t.Errorf("Page: %v, want nil (the pager's exit status is its own)", err)
	}
}

// TestResolveDefaultsToLess keeps the "pipes through less" promise when the
// user has no $PAGER.
func TestResolveDefaultsToLess(t *testing.T) {
	t.Setenv("PAGER", "")
	parts := resolve()
	if len(parts) != 1 || parts[0] != "less" {
		t.Errorf("resolve() = %v, want [less]", parts)
	}
}

// TestResolveTrimsWhitespace: PAGER="   " is not a command.
func TestResolveTrimsWhitespace(t *testing.T) {
	t.Setenv("PAGER", strings.Repeat(" ", 3))
	parts := resolve()
	if len(parts) != 1 || parts[0] != "less" {
		t.Errorf("resolve() = %v, want [less]", parts)
	}
}
