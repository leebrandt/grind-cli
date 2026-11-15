package cli

import (
	"strings"
	"unicode/utf8"
)

// minFlexibleWidth is the narrowest a text column may shrink to. Below this
// a table of ellipses says less than one that overflows the terminal, so the
// shrinking stops here and accepts the wrap.
const minFlexibleWidth = 12

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

// truncate shortens s to at most width runes, marking the cut with an
// ellipsis. The marker survives truncation because it is a prefix of the
// text, which is what keeps a long-term project recognizable in a narrow
// table. A width of zero or less returns the empty string.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:width-1]) + "…"
}

// tableWidth is the number of runes a rendered row occupies: the columns
// plus the two-space gap between each pair.
func tableWidth(widths []int) int {
	if len(widths) == 0 {
		return 0
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	return total + 2*(len(widths)-1)
}

// naturalWidths returns each column's width: the widest of its header and
// cells, measured in runes so a multi-byte glyph counts as the single column
// it occupies on screen.
func naturalWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	return widths
}

// fitColumns narrows a table to width runes.
//
// shrinkable lists the columns allowed to lose characters — the text columns
// whose length the user controls. They yield first and together: the widest
// one gives up a rune, then the widest of the rest, so two long columns end up
// balanced instead of one being butchered. A long project name therefore gets
// truncated before the table gives up a whole column of information.
//
// Only when even the floors (minFlexibleWidth per shrinkable column) will not
// fit does a column disappear, from the right edge, down to minColumns. Never
// a shrinkable one: that is the column carrying the data. A dashboard that
// loses "Last Commit" on a narrow terminal still says something useful; one
// that wraps every row says almost nothing.
//
// width <= 0 means "no constraint": the input comes back untouched with its
// natural widths, which is what a piped or redirected destination gets.
//
// Rows and headers are slices of plain text. Callers apply color afterwards,
// so truncating here can never cut an escape sequence in half.
func fitColumns(width int, headers []string, rows [][]string, shrinkable []int, minColumns int) ([]string, [][]string, []int) {
	widths := naturalWidths(headers, rows)
	if width <= 0 {
		return headers, rows, widths
	}

	for len(headers) > minColumns {
		if minimumWidth(widths, shrinkable) <= width {
			break
		}
		last := len(headers) - 1
		if isShrinkable(shrinkable, last) {
			break
		}
		headers = headers[:last]
		widths = widths[:last]
		for i := range rows {
			rows[i] = rows[i][:last]
		}
	}

	shrinkColumns(widths, shrinkable, tableWidth(widths)-width)

	// Cells keep their full text until now, so the truncation happens
	// exactly once per column, against the width that survived.
	for _, i := range shrinkable {
		headers[i] = truncate(headers[i], widths[i])
		for _, row := range rows {
			row[i] = truncate(row[i], widths[i])
		}
	}

	return headers, rows, widths
}

// isShrinkable reports whether column i is one of the text columns.
func isShrinkable(shrinkable []int, column int) bool {
	for _, i := range shrinkable {
		if i == column {
			return true
		}
	}
	return false
}

// minimumWidth is how wide the table could ever be: every shrinkable column
// at its floor, everything else at its natural width.
func minimumWidth(widths []int, shrinkable []int) int {
	total := tableWidth(widths)
	for _, i := range shrinkable {
		if widths[i] > minFlexibleWidth {
			total -= widths[i] - minFlexibleWidth
		}
	}
	return total
}

// shrinkColumns takes excess runes out of the shrinkable columns, widest
// first, stopping at the floor. Runes it cannot take stay in the table as
// overflow — the honest outcome on a terminal too narrow for the floors.
func shrinkColumns(widths []int, shrinkable []int, excess int) {
	for ; excess > 0; excess-- {
		widest := -1
		for _, i := range shrinkable {
			if widths[i] <= minFlexibleWidth {
				continue
			}
			if widest < 0 || widths[i] > widths[widest] {
				widest = i
			}
		}
		if widest < 0 {
			return
		}
		widths[widest]--
	}
}
