package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// rowRunes is the on-screen width of a rendered line, counting a multi-byte
// glyph as the single column it occupies.
func rowRunes(s string) int {
	return utf8.RuneCountInString(s)
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"fits", "my-blog", 10, "my-blog"},
		{"exact", "my-blog", 7, "my-blog"},
		{"cut", "my-blog", 5, "my-b…"},
		{"marker survives", "★ my-blog", 5, "★ my…"},
		// One column has room for the ellipsis or for a character, not
		// both; the ellipsis wins so the cut is visible.
		{"one column", "my-blog", 1, "…"},
		{"zero", "my-blog", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.in, tt.width); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
		})
	}
}

func TestTableWidth(t *testing.T) {
	// Columns plus the two-space gaps between them.
	if got, want := tableWidth([]int{4, 6, 3}), 4+6+3+2+2; got != want {
		t.Errorf("tableWidth([4 6 3]) = %d, want %d", got, want)
	}
	if got := tableWidth(nil); got != 0 {
		t.Errorf("tableWidth(nil) = %d, want 0", got)
	}
}

// TestFitColumnsNoConstraint pins the piped case: a writer that is not a
// terminal gets the natural table, not a truncated one.
func TestFitColumnsNoConstraint(t *testing.T) {
	headers := []string{"Name", "Worked"}
	rows := [][]string{{"my-long-project", "8.0h"}}

	gotHeaders, gotRows, widths := fitColumns(0, headers, rows, []int{0}, 1)

	if len(gotHeaders) != 2 {
		t.Errorf("headers = %q, want both columns kept", gotHeaders)
	}
	if gotRows[0][0] != "my-long-project" {
		t.Errorf("cell = %q, want it untouched", gotRows[0][0])
	}
	if widths[0] != len("my-long-project") {
		t.Errorf("width = %d, want the natural %d", widths[0], len("my-long-project"))
	}
}

// TestFitColumnsShrinksFlexibleFirst: a long name gives up characters before
// the table gives up a column.
func TestFitColumnsShrinksFlexibleFirst(t *testing.T) {
	headers := []string{"Name", "Worked", "Tasks"}
	rows := [][]string{{"a-very-long-project-name", "8.0h", "4"}}

	gotHeaders, gotRows, widths := fitColumns(35, headers, rows, []int{0}, 3)

	if len(gotHeaders) != 3 {
		t.Fatalf("headers = %q, want all three columns kept", gotHeaders)
	}
	if !strings.HasSuffix(gotRows[0][0], "…") {
		t.Errorf("name = %q, want it truncated with an ellipsis", gotRows[0][0])
	}
	if got := tableWidth(widths); got > 35 {
		t.Errorf("table is %d runes wide, want at most 35", got)
	}
}

// TestFitColumnsBalancesTextColumns: with two text columns, the widest gives
// up the next rune, so neither is butchered to keep the other whole.
func TestFitColumnsBalancesTextColumns(t *testing.T) {
	headers := []string{"#", "Project", "Task", "Due"}
	rows := [][]string{{
		"100", "a-rather-long-project-name", "write the introduction and the conclusion", "2026-09-30",
	}}

	_, gotRows, widths := fitColumns(50, headers, rows, []int{1, 2}, len(headers))

	if got := tableWidth(widths); got > 50 {
		t.Errorf("table is %d runes wide, want at most 50", got)
	}
	// Both text columns shrink; the small ones do not.
	if widths[1] >= len("a-rather-long-project-name") {
		t.Errorf("project width = %d, want it shrunk", widths[1])
	}
	if widths[2] >= len("write the introduction and the conclusion") {
		t.Errorf("task width = %d, want it shrunk", widths[2])
	}
	if widths[0] != len("100") || widths[3] != len("2026-09-30") {
		t.Errorf("fixed columns moved: %v", widths)
	}
	// Within a rune of each other: the widest-first rule balances them.
	if diff := widths[1] - widths[2]; diff > 1 || diff < -1 {
		t.Errorf("text widths %d and %d differ by %d, want them balanced", widths[1], widths[2], diff)
	}
	if !strings.HasSuffix(gotRows[0][1], "…") || !strings.HasSuffix(gotRows[0][2], "…") {
		t.Errorf("row = %q, want both text cells truncated", gotRows[0])
	}
}

// TestFitColumnsDropsTrailingColumns: when even a minimum-width flexible
// column will not fit, the least useful columns go, rightmost first.
func TestFitColumnsDropsTrailingColumns(t *testing.T) {
	headers := []string{"Project", "Worked", "Tasks", "Last Session", "Last Commit"}
	rows := [][]string{{
		"a-very-long-project-name", "12.5h", "7", "3h ago", "2d ago",
	}}

	gotHeaders, gotRows, widths := fitColumns(30, headers, rows, []int{0}, 3)

	want := []string{"Project", "Worked", "Tasks"}
	if len(gotHeaders) != len(want) {
		t.Fatalf("headers = %q, want %q", gotHeaders, want)
	}
	for i := range want {
		if gotHeaders[i] != want[i] {
			t.Fatalf("headers = %q, want %q", gotHeaders, want)
		}
	}
	// Three columns survive, so the cell slice must shrink with them.
	if len(gotRows[0]) != 3 {
		t.Errorf("row = %q, want three cells", gotRows[0])
	}
	if got := tableWidth(widths); got > 30 {
		t.Errorf("table is %d runes wide, want at most 30", got)
	}
}

// TestFitColumnsStopsAtMinColumns: a terminal narrower than the minimum is
// allowed to overflow. A table of pure ellipses would be worse.
func TestFitColumnsStopsAtMinColumns(t *testing.T) {
	headers := []string{"Project", "Worked", "Tasks", "Last Session", "Last Commit"}
	rows := [][]string{{
		"a-very-long-project-name", "12.5h", "7", "3h ago", "2d ago",
	}}

	gotHeaders, _, widths := fitColumns(10, headers, rows, []int{0}, 3)

	if len(gotHeaders) != 3 {
		t.Fatalf("headers = %q, want the minimum three columns", gotHeaders)
	}
	if widths[0] != minFlexibleWidth {
		t.Errorf("flexible width = %d, want the floor %d", widths[0], minFlexibleWidth)
	}
}

// TestFitColumnsNeverDropsFlexible: with the elastic column last, there is
// nothing behind it to drop, so it bottoms out at the floor instead.
func TestFitColumnsNeverDropsFlexible(t *testing.T) {
	headers := []string{"#", "Task"}
	rows := [][]string{{"100", "a task description that is far too long"}}

	gotHeaders, _, widths := fitColumns(5, headers, rows, []int{1}, 1)

	if len(gotHeaders) != 2 {
		t.Fatalf("headers = %q, want the flexible column kept", gotHeaders)
	}
	if widths[1] != minFlexibleWidth {
		t.Errorf("flexible width = %d, want the floor %d", widths[1], minFlexibleWidth)
	}
}

// TestRenderRowAligned pins the two rules that make the hwd table scannable:
// a right-aligned cell is padded on the left, and a left-aligned LAST cell is
// not padded at all (trailing spaces would only pollute the line).
func TestRenderRowAligned(t *testing.T) {
	widths := []int{6, 4, 4}

	t.Run("right aligns numerics", func(t *testing.T) {
		got := renderRowAligned([]string{"blog", "3.0h", "450"}, widths, []bool{false, true, true})
		want := "blog    3.0h   450"
		if got != want {
			t.Errorf("renderRowAligned = %q, want %q", got, want)
		}
	})

	t.Run("last cell unpadded when left", func(t *testing.T) {
		got := renderRowAligned([]string{"blog", "3.0h", "450"}, widths, []bool{false, false, false})
		want := "blog    3.0h  450"
		if got != want {
			t.Errorf("renderRowAligned = %q, want %q", got, want)
		}
	})

	t.Run("nil right means every cell left", func(t *testing.T) {
		aligned := renderRowAligned([]string{"blog", "3.0h", "450"}, widths, nil)
		if plain := renderRow([]string{"blog", "3.0h", "450"}, widths); aligned != plain {
			t.Errorf("renderRowAligned(nil) = %q, renderRow = %q, want identical", aligned, plain)
		}
	})

	t.Run("short right slice right-aligns the prefix only", func(t *testing.T) {
		// fitColumns drops trailing columns, so right loses the same ones;
		// only the surviving prefix is consulted.
		got := renderRowAligned([]string{"blog", "3.0h"}, []int{6, 4}, []bool{false, true})
		want := "blog    3.0h"
		if got != want {
			t.Errorf("renderRowAligned = %q, want %q", got, want)
		}
	})
}
