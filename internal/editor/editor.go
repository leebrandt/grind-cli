// Package editor launches the user's editor for grind files.
//
// The editor is resolved from $EDITOR, falling back to $VISUAL and then vi.
// The value is split on whitespace so entries like "code --wait" work.
package editor

import (
	"os"
	"os/exec"
	"strings"

	"github.com/leebrandt/grind/internal/grinderr"
)

// Open launches the user's editor on path, inheriting stdio so the editor
// can take over the terminal.
func Open(path string) error {
	cmd := editorCommand(path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return grinderr.WrapSystem(err, "run editor on %s", path)
	}
	return nil
}

// EditTemp creates a temp file with initial content, opens it in the
// editor, and returns the edited content. The temp file is removed when the
// function returns.
func EditTemp(prefix, initial string) (string, error) {
	tmp, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", grinderr.WrapSystem(err, "create temp file")
	}
	name := tmp.Name()
	defer os.Remove(name)

	if _, err := tmp.WriteString(initial); err != nil {
		tmp.Close()
		return "", grinderr.WrapSystem(err, "write temp file")
	}
	if err := tmp.Close(); err != nil {
		return "", grinderr.WrapSystem(err, "close temp file")
	}

	cmd := editorCommand(name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", grinderr.WrapSystem(err, "run editor on %s", name)
	}

	content, err := os.ReadFile(name)
	if err != nil {
		return "", grinderr.WrapSystem(err, "read edited file")
	}
	return string(content), nil
}

// editorCommand builds the editor command with the file path appended.
func editorCommand(path string) *exec.Cmd {
	parts := resolve()
	parts = append(parts, path)
	return exec.Command(parts[0], parts[1:]...)
}

// resolve returns the editor command from the environment, defaulting to vi.
func resolve() []string {
	raw := os.Getenv("EDITOR")
	if raw == "" {
		raw = os.Getenv("VISUAL")
	}
	if raw == "" {
		raw = "vi"
	}
	return strings.Fields(raw)
}
