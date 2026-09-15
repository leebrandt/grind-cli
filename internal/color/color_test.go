package color

import (
	"bytes"
	"testing"
)

func TestNewOnBufferIsPlain(t *testing.T) {
	// A bytes.Buffer is not a terminal, so colors must be stripped to keep
	// piped output clean and tests readable.
	var buf bytes.Buffer
	p := New(&buf)

	if got := p.Red("x"); got != "x" {
		t.Errorf("Red on buffer = %q, want plain %q", got, "x")
	}
	if got := p.Yellow("x"); got != "x" {
		t.Errorf("Yellow on buffer = %q, want plain %q", got, "x")
	}
	if got := p.Green("x"); got != "x" {
		t.Errorf("Green on buffer = %q, want plain %q", got, "x")
	}
	if got := p.Dim("x"); got != "x" {
		t.Errorf("Dim on buffer = %q, want plain %q", got, "x")
	}
}

func TestPaletteEnabledWrapsAndResets(t *testing.T) {
	// White-box: construct an enabled palette directly so the ANSI codes
	// can be verified without a real terminal. The codes are plain ANSI —
	// no tabwriter escape bytes — so consumers can pad plain text first
	// and color the padded cell without the codes shifting the layout.
	p := Palette{enabled: true}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Red", p.Red("x"), "\x1b[31mx\x1b[0m"},
		{"Yellow", p.Yellow("x"), "\x1b[33mx\x1b[0m"},
		{"Green", p.Green("x"), "\x1b[32mx\x1b[0m"},
		{"Dim", p.Dim("x"), "\x1b[2mx\x1b[0m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}
