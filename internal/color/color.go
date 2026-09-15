// Package color wraps strings in ANSI color codes.
//
// Colors are only emitted when the output writer is a terminal. Piped
// output and test buffers get plain text, so scripts and tests never see
// escape codes.
//
// The codes are plain ANSI (code + text + reset) — no tabwriter escape
// bytes. Alignment is the consumer's job: pad the PLAIN text to its
// column width, then color the padded cell. Coloring first would let the
// codes count toward the width and push the visible text out of line.
// text/tabwriter's escape mechanism cannot help here: it only keeps tabs
// and newlines inside the escaped segment from terminating the cell, and
// the escaped text still counts toward the column width.
package color

import (
	"io"
	"os"
)

const (
	red    = "\x1b[31m"
	yellow = "\x1b[33m"
	green  = "\x1b[32m"
	dim    = "\x1b[2m"
	reset  = "\x1b[0m"
)

// Palette wraps strings in ANSI color codes. When the writer is not a
// terminal, the methods return the string unchanged so piped output stays
// clean and tests see plain text.
type Palette struct {
	enabled bool
}

// New returns a Palette that colors output written to w. Colors are only
// emitted when w is a terminal: an *os.File whose mode has ModeCharDevice
// set. Buffers and pipes get plain text.
func New(w io.Writer) Palette {
	f, ok := w.(*os.File)
	if !ok {
		return Palette{}
	}
	info, err := f.Stat()
	if err != nil {
		return Palette{}
	}
	return Palette{enabled: info.Mode()&os.ModeCharDevice != 0}
}

// Red wraps s in red, the urgency color for overdue or due-today tasks.
func (p Palette) Red(s string) string {
	return p.wrap(red, s)
}

// Yellow wraps s in yellow, the urgency color for tasks due within 3 days.
func (p Palette) Yellow(s string) string {
	return p.wrap(yellow, s)
}

// Green wraps s in green, the urgency color for tasks due in 4+ days.
func (p Palette) Green(s string) string {
	return p.wrap(green, s)
}

// Dim wraps s in the dim attribute, used for completed rows.
func (p Palette) Dim(s string) string {
	return p.wrap(dim, s)
}

// wrap applies code + s + reset when colors are enabled, otherwise returns
// s unchanged.
func (p Palette) wrap(code, s string) string {
	if !p.enabled {
		return s
	}
	return code + s + reset
}
