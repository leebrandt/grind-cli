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
	// can be verified without a real terminal. Each code is bracketed by
	// the tabwriter escape byte so it never counts toward column width.
	p := Palette{enabled: true}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Red", p.Red("x"), "\xff\x1b[31m\xffx\xff\x1b[0m\xff"},
		{"Yellow", p.Yellow("x"), "\xff\x1b[33m\xffx\xff\x1b[0m\xff"},
		{"Green", p.Green("x"), "\xff\x1b[32m\xffx\xff\x1b[0m\xff"},
		{"Dim", p.Dim("x"), "\xff\x1b[2m\xffx\xff\x1b[0m\xff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}