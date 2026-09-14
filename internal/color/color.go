// Package color wraps strings in ANSI color codes.
//
// Colors are only emitted when the output writer is a terminal. Piped
// output and test buffers get plain text, so scripts and tests never see
// escape codes.
//
// The palette brackets every ANSI code in text/tabwriter's escape
// character (0xff). tabwriter ignores escaped text when computing column
// widths, so colored cells stay aligned — without this, the escape codes
// would count as cell width and push the visible text out of line.
// Consumers that render through tabwriter must pass tabwriter.StripEscape
// so the escape bytes are removed from the output. The status/wwd slice
// will reuse this package.
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
	// escape is text/tabwriter.Escape: text bracketed by this byte is
	// passed through unchanged and excluded from column-width math. It is
	// a string constant because the literal "\xff" is the single byte
	// 0xff — string(0xff) would produce the UTF-8 encoding of U+00FF.
	escape = "\xff"
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
// s unchanged. The escape bytes bracket only the codes (never the text) so
// tabwriter strips them without losing the visible string.
func (p Palette) wrap(code, s string) string {
	if !p.enabled {
		return s
	}
	return escape + code + escape + s + escape + reset + escape
}