// Package pager sends long command output through the user's pager.
//
// The pager is resolved from $PAGER, falling back to less. Paging only
// happens when the destination is a terminal: output piped to a file or
// another program (or captured by a test) is written verbatim, because a
// pager in a pipe is not interactive and would only add escape codes to
// something the next program has to read.
package pager

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/leebrandt/grind/internal/grinderr"
	"github.com/leebrandt/grind/internal/terminal"
)

// Page writes text to out through the user's pager when out is a terminal.
// Otherwise it writes text straight through.
func Page(out io.Writer, text string) error {
	if !isTerminal(out) {
		_, err := io.WriteString(out, text)
		return err
	}

	parts := resolve()
	cmd := exec.Command(parts[0], parts[1:]...)
	// The text arrives on stdin while the pager paints the terminal — the
	// way git feeds less. less reads keystrokes from /dev/tty rather than
	// the stdin pipe, so interactivity survives the pipe.
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// No pager installed: the text must still reach the user.
		if errors.Is(err, exec.ErrNotFound) {
			_, writeErr := io.WriteString(out, text)
			return writeErr
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// The pager ran and chose how to stop — the user quit,
			// pressed Ctrl-C, or it could not open a terminal. Its exit
			// status is not grind's: the command itself succeeded.
			return nil
		}
		return grinderr.WrapSystem(err, "run pager %s", parts[0])
	}
	return nil
}

// resolve returns the pager command from $PAGER, defaulting to less. The
// value is split on whitespace so entries like "less -R" work.
func resolve() []string {
	raw := strings.TrimSpace(os.Getenv("PAGER"))
	if raw == "" {
		return []string{"less"}
	}
	return strings.Fields(raw)
}

// isTerminal is the paging decision, kept as a variable so tests can reach
// the pager path without a real tty; production never reassigns it.
var isTerminal = terminal.IsTerminal
