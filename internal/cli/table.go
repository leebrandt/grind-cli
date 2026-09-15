package cli

import (
	"strings"
	"unicode/utf8"
)

// padRight returns s padded with trailing spaces to width runes. Callers
// pad the PLAIN text before applying color, so the padding math never
// counts ANSI escape codes.
func padRight(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// renderRow pads cells to their column widths and joins them with two
// spaces. The last cell is not padded — nothing follows it, so trailing
// spaces would only pollute the line.
func renderRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		if i < len(cells)-1 {
			c = padRight(c, widths[i])
		}
		b.WriteString(c)
	}
	return b.String()
}
