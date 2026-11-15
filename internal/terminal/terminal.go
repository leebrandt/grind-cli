// Package terminal answers what grind needs to know about its destination:
// whether output is going to a terminal, and how big that terminal is.
//
// Both questions are ioctl queries against the file descriptor. The obvious
// stdlib alternative — os.File's Stat mode with ModeCharDevice — is wrong in
// the cases that matter: /dev/null and /dev/zero are character devices but
// not terminals, so output redirected to them would get ANSI codes and pager
// escapes it never asked for.
package terminal

import (
	"io"
	"os"

	"golang.org/x/term"
)

// IsTerminal reports whether w is an interactive terminal. A writer that is
// not an *os.File — a bytes.Buffer in a test, an io.Pipe — never is.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// Size returns the visible width and height of the terminal w writes to. ok
// is false when w is not a terminal or the size cannot be read; callers treat
// that as "no width constraint", which is what a pipe or a file wants anyway.
func Size(w io.Writer) (width, height int, ok bool) {
	f, isFile := w.(*os.File)
	if !isFile {
		return 0, 0, false
	}
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0, false
	}
	return width, height, true
}
