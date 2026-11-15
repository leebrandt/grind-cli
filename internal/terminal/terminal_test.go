package terminal

import (
	"bytes"
	"os"
	"testing"
)

func TestIsTerminalOnBuffer(t *testing.T) {
	// Neither a test buffer nor an io.Pipe has a file descriptor to ask
	// the kernel about, so neither can be a terminal.
	var buf bytes.Buffer
	if IsTerminal(&buf) {
		t.Error("IsTerminal(bytes.Buffer) = true, want false")
	}
}

// TestIsTerminalOnDevNull is the reason this package exists. /dev/null is a
// character device, so a file-mode check calls it a terminal — but it is not
// one, and output redirected to it must stay free of ANSI codes.
func TestIsTerminalOnDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer f.Close()

	if IsTerminal(f) {
		t.Errorf("IsTerminal(%s) = true, want false", os.DevNull)
	}
}

func TestSizeOnBuffer(t *testing.T) {
	var buf bytes.Buffer
	if _, _, ok := Size(&buf); ok {
		t.Error("Size(bytes.Buffer) ok = true, want false")
	}
}

// TestSizeOnDevNull: a writer can be a real *os.File and still have no size,
// which is why Size returns an ok instead of a width of zero that looks like
// a very narrow terminal.
func TestSizeOnDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer f.Close()

	if _, _, ok := Size(f); ok {
		t.Errorf("Size(%s) ok = true, want false", os.DevNull)
	}
}
