package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/leebrandt/grind/internal/grinderr"
)

// prompter reads answers from one scanner and writes questions to one
// writer. One scanner for the whole wizard is deliberate: bufio reads ahead,
// so creating a scanner per question would swallow answers typed ahead (or
// piped in) and every question after the first would see end of input.
type prompter struct {
	scanner *bufio.Scanner
	out     io.Writer
}

// newPrompter builds a prompter that reads answers from in and writes
// questions to out.
func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{scanner: bufio.NewScanner(in), out: out}
}

// ask writes label and returns the next answer with surrounding whitespace
// trimmed. ok is false at end of input; err is a system error ("read
// answer") when the reader itself fails. A final line without a trailing
// newline is still an answer — shell pipelines often end without one.
func (p *prompter) ask(label string) (answer string, ok bool, err error) {
	fmt.Fprint(p.out, label)
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			return "", false, grinderr.WrapSystem(err, "read answer")
		}
		return "", false, nil
	}
	return strings.TrimSpace(p.scanner.Text()), true, nil
}
